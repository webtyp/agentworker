//go:build !wasm

package agentworker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"webtyp.com/agent"
	"webtyp.com/artifacts"
	"webtyp.com/context"
	"webtyp.com/device"
	"webtyp.com/files"
	"webtyp.com/files/mem"
	"webtyp.com/llm"
	"webtyp.com/weights"
)

// fakeDecider answers every question with its first option and counts the cache calls.
type fakeDecider struct {
	saves   int
	loads   int
	loaded  []byte
	loadErr error
}

func (f *fakeDecider) Decide(ctx *context.Context, q llm.Question) (llm.Decision, error) {
	return llm.Decision{Choice: 0, Confidence: 1}, nil
}
func (f *fakeDecider) CountTokens(text string) int { return len(text) }
func (f *fakeDecider) SaveDecisionCache() ([]byte, bool, error) {
	f.saves++
	return []byte("saved-cache"), true, nil
}
func (f *fakeDecider) LoadDecisionCache(b []byte) error {
	f.loads++
	f.loaded = b
	return f.loadErr
}

type fakeWriter struct{}

func (fakeWriter) Generate(ctx *context.Context, req llm.Request) (llm.Response, error) {
	return llm.Response{Text: "writer"}, nil
}

type fixedID struct{ n int }

func (g *fixedID) NewID() string { g.n++; return "id" + string(rune('0'+g.n)) }

// file is one artifact served by the test server.
type file struct {
	id, version string
	data        []byte
	needs       device.Requirement
}

type harness struct {
	t        *testing.T
	c        *core
	fs       *mem.Files
	posts    []Event
	requests int32 // artifact requests (the manifest excluded)
	dec      *fakeDecider
	models   Models
}

func modelFile(id string, needs device.Requirement) file {
	one := weights.TensorInput{Name: "w", DType: weights.Float32, Shape: []int{1}, Data: []byte{0, 0, 128, 63}}
	data, err := weights.WriteArtifact(id, 1, weights.TokenizerConfig{}, []weights.TensorInput{one})
	if err != nil {
		panic(err)
	}
	return file{id: id, version: "v1", data: data, needs: needs}
}

func mergesFile(id string) file {
	return file{id: id, version: "v1", data: []byte("a b\n")}
}

// standard is the decider + writer set every test starts from.
func standard() []file {
	return []file{
		modelFile("decider", device.Requirement{}),
		mergesFile("decider-merges"),
		modelFile("writer", device.Requirement{}),
		mergesFile("writer-merges"),
	}
}

func newHarness(t *testing.T, fs []file, p device.Profile, withWriter bool) *harness {
	h := &harness{t: t, fs: mem.New(), dec: &fakeDecider{}}
	var m artifacts.Manifest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == artifacts.ManifestPath {
			data, _ := m.Encode()
			w.Write(data)
			return
		}
		atomic.AddInt32(&h.requests, 1)
		for _, f := range fs {
			if r.URL.Path == "/"+f.id {
				http.ServeContent(w, r, f.id, time.Time{}, bytes.NewReader(f.data))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	for _, f := range fs {
		sum := sha256.Sum256(f.data)
		m.Artifacts = append(m.Artifacts, artifacts.Artifact{
			ID: f.id, Version: f.version, URL: srv.URL + "/" + f.id,
			Size: int64(len(f.data)), SHA256: hex.EncodeToString(sum[:]), Needs: f.needs,
		})
	}

	s := Setup{
		Decider: DeciderSpec{Weights: "decider", Merges: "decider-merges"},
		AgentConfig: func(m Models) (agent.Config, error) {
			h.models = m
			return agent.Config{
				Texts:     texts(),
				Memory:    agent.NewMemMemory(),
				IDGen:     &fixedID{},
				ToolIndex: agent.NewMemToolIndex(),
			}, nil
		},
	}
	if withWriter {
		s.Writer = &WriterSpec{Weights: "writer", Merges: "writer-merges"}
	}
	h.c = &core{
		setup:       s,
		store:       artifacts.New(h.fs),
		files:       h.fs,
		detect:      func() (device.Profile, error) { return p, nil },
		bench:       func() device.Rate { return 1000 },
		post:        func(e Event) { h.posts = append(h.posts, e) },
		manifestURL: srv.URL + artifacts.ManifestPath,
		newDecider: func(w *weights.Artifact, merges []byte, s DeciderSpec) (decider, error) {
			return h.dec, nil
		},
		newWriter: func(w *weights.Artifact, merges []byte, s WriterSpec) (llm.Client, error) {
			return fakeWriter{}, nil
		},
	}
	return h
}

func texts() agent.Texts {
	return agent.Texts{
		Speaker: "A staff member", Assistant: "an assistant", NoToolOption: "no tool",
		NoTool: "hello", Refused: "refused", TooLong: "too long", Clarify: "clarify",
		Confirm: "confirm", Declined: "declined", Failed: "failed", Yes: "yes", No: "no",
		Found: "found", WriterSystem: "system", DataLabel: "data", QuestionLabel: "question",
	}
}

// roomy is a secure device with space for everything.
var roomy = device.Profile{Secure: true, SIMD: true, Quota: 1 << 30}

func (h *harness) start() *Event {
	h.t.Helper()
	e, err := h.c.handle(context.Background(), Request{Kind: RequestStart, Persisted: true})
	if err != nil {
		h.t.Fatalf("start: %v", err)
	}
	return e
}

func (h *harness) run(text string) *Event {
	h.t.Helper()
	e, err := h.c.handle(context.Background(), Request{Kind: RequestRun, SessionID: "s1", Text: text})
	if err != nil {
		h.t.Fatalf("run: %v", err)
	}
	return e
}

func (h *harness) progress(id string) []Event {
	var out []Event
	for _, e := range h.posts {
		if e.Kind == EventProgress && e.Artifact == id {
			out = append(out, e)
		}
	}
	return out
}

func TestStart_DownloadsLoadsAndIsReady(t *testing.T) {
	fs := standard()
	h := newHarness(t, fs, roomy, true)
	e := h.start()
	if e.Kind != EventReady || e.WriterOff || !e.Persisted {
		t.Fatalf("got %+v, want EventReady{WriterOff: false, Persisted: true}", e)
	}
	for _, f := range fs {
		p := h.progress(f.id)
		if len(p) == 0 || p[len(p)-1].Done != p[len(p)-1].Total {
			t.Errorf("%s: progress %+v, want it to end at Done == Total", f.id, p)
		}
		if _, err := h.fs.ReadFile(f.id + "/" + f.version); err != nil {
			t.Errorf("%s not stored: %v", f.id, err)
		}
	}
	if h.models.Writer == nil {
		t.Error("AgentConfig got no writer")
	}
	if again := h.start(); again.Kind != EventReady || h.requests != int32(len(fs)) {
		t.Errorf("second start: %+v after %d requests, want EventReady and no new download", again, h.requests)
	}
}

func TestStart_AsleepWhenDeviceShort(t *testing.T) {
	fs := standard()
	fs[0].needs = device.Requirement{MinTier: device.TierSIMD}
	h := newHarness(t, fs, device.Profile{Secure: true, Quota: 1 << 30}, true)
	e := h.start()
	if e.Kind != EventAsleep || e.Artifact != "decider" || strings.Join(e.Shortfalls, ",") != "tier" {
		t.Fatalf("got %+v, want EventAsleep{decider, [tier]}", e)
	}
	if h.requests != 0 {
		t.Errorf("%d artifact requests, want 0", h.requests)
	}
}

func TestStart_InsecureContextSleeps(t *testing.T) {
	h := newHarness(t, standard(), device.Profile{Secure: false, SIMD: true, Quota: 1 << 30}, true)
	e := h.start()
	if e.Kind != EventAsleep || len(e.Shortfalls) == 0 || e.Shortfalls[0] != "secure" {
		t.Fatalf("got %+v, want EventAsleep with secure", e)
	}
}

func TestStart_DropsWriterWhenNoSpaceForBoth(t *testing.T) {
	fs := standard()
	decider := int64(len(fs[0].data) + len(fs[1].data))
	h := newHarness(t, fs, device.Profile{Secure: true, Quota: decider + 1}, true)
	e := h.start()
	if e.Kind != EventReady || !e.WriterOff {
		t.Fatalf("got %+v, want EventReady{WriterOff: true}", e)
	}
	if len(h.progress("writer")) != 0 || len(h.progress("writer-merges")) != 0 {
		t.Error("the writer was downloaded")
	}
	if h.models.Writer != nil {
		t.Error("AgentConfig got a writer")
	}
}

func TestStart_DropsWriterOnItsShortfall(t *testing.T) {
	fs := standard()
	fs[2].needs = device.Requirement{MinRate: 5000}
	h := newHarness(t, fs, roomy, true)
	if e := h.start(); e.Kind != EventReady || !e.WriterOff {
		t.Fatalf("got %+v, want EventReady{WriterOff: true}", e)
	}
}

func TestStart_LoadsDecisionCache(t *testing.T) {
	h := newHarness(t, standard(), roomy, false)
	h.fs.WriteFile(DecisionCacheFile, []byte("old-cache"))
	h.start()
	if h.dec.loads != 1 || string(h.dec.loaded) != "old-cache" {
		t.Fatalf("LoadDecisionCache called %d times with %q", h.dec.loads, h.dec.loaded)
	}
	h.run("hola")
	if h.dec.saves != 0 {
		t.Errorf("SaveDecisionCache called %d times after a loaded cache, want 0", h.dec.saves)
	}
}

func TestStart_BadDecisionCacheRemoved(t *testing.T) {
	h := newHarness(t, standard(), roomy, false)
	h.dec.loadErr = errString("other weights")
	h.fs.WriteFile(DecisionCacheFile, []byte("foreign"))
	if e := h.start(); e.Kind != EventReady {
		t.Fatalf("got %+v, want EventReady", e)
	}
	if _, err := h.fs.ReadFile(DecisionCacheFile); err != files.ErrNotExist {
		t.Errorf("cache file still there (err %v)", err)
	}
}

func TestRun_SavesDecisionCacheOnce(t *testing.T) {
	h := newHarness(t, standard(), roomy, false)
	h.start()
	e := h.run("hola")
	if e.Kind != EventReply || e.SessionID != "s1" || e.Text != "hello" {
		t.Fatalf("got %+v, want EventReply{s1, hello}", e)
	}
	data, err := h.fs.ReadFile(DecisionCacheFile)
	if err != nil || string(data) != "saved-cache" {
		t.Fatalf("cache file %q (err %v), want saved-cache", data, err)
	}
	h.run("otra vez")
	if h.dec.saves != 1 {
		t.Errorf("SaveDecisionCache called %d times, want 1", h.dec.saves)
	}
}

func TestRun_BeforeStartFails(t *testing.T) {
	h := newHarness(t, standard(), roomy, false)
	_, err := h.c.handle(context.Background(), Request{Kind: RequestRun, SessionID: "s1", Text: "hola"})
	if err == nil || err.Error() != errNotReady {
		t.Fatalf("got %v, want %q", err, errNotReady)
	}
}

func TestStart_PrunesOldVersions(t *testing.T) {
	h := newHarness(t, standard(), roomy, false)
	h.fs.WriteFile("decider/v0", []byte("old"))
	h.fs.WriteFile("decider/v0.ok", []byte("x"))
	h.fs.WriteFile("index", []byte("decider/v0\n"))
	h.start()
	if _, err := h.fs.ReadFile("decider/v0"); err != files.ErrNotExist {
		t.Errorf("old version still stored (err %v)", err)
	}
	if _, err := h.fs.ReadFile("decider/v1"); err != nil {
		t.Errorf("current version removed: %v", err)
	}
}

func TestStart_ProgressThrottled(t *testing.T) {
	fs := standard()
	fs[1].data = bytes.Repeat([]byte("a b\n"), 10<<20/4) // 10 MiB of merges
	h := newHarness(t, fs, roomy, false)
	h.start()
	if p := h.progress("decider-merges"); len(p) == 0 || len(p) > 101 {
		t.Fatalf("%d progress events, want 1..101", len(p))
	}
}

type errString string

func (e errString) Error() string { return string(e) }
