package agentworker

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"webtyp.com/agent"
	"webtyp.com/artifacts"
	"webtyp.com/device"
	"webtyp.com/files/mem"
	"webtyp.com/llm"
	"webtyp.com/weights"
)

type fakeDecider struct {
	SaveCalls int
	LoadCalls int
	LoadData  []byte
}

func (f *fakeDecider) SaveDecisionCache() ([]byte, bool, error) {
	f.SaveCalls++
	return []byte("cache"), true, nil
}
func (f *fakeDecider) LoadDecisionCache(b []byte) error {
	f.LoadCalls++
	f.LoadData = b
	return nil
}
func (f *fakeDecider) CountTokens(text string) (int, error) { return 0, nil }
func (f *fakeDecider) Name() string                         { return "fake-decider" }
func (f *fakeDecider) Template() (llm.Template, error)      { return llm.Template{}, nil }
func (f *fakeDecider) Chat(req llm.Request) (llm.Reply, error) {
	return llm.Reply{Text: "reply"}, nil
}
func (f *fakeDecider) GeneratePrefix(req llm.PrefixRequest) (llm.PrefixReply, error) {
	return llm.PrefixReply{}, nil
}

type fakeWriter struct{}

func (f *fakeWriter) Name() string                    { return "fake-writer" }
func (f *fakeWriter) Template() (llm.Template, error) { return llm.Template{}, nil }
func (f *fakeWriter) Chat(req llm.Request) (llm.Reply, error) {
	return llm.Reply{Text: "writer-reply"}, nil
}

func TestStart_DownloadsLoadsAndIsReady(t *testing.T) {
	manifest := []byte(`{
		"artifacts": [
			{"id": "decider-weights", "size": 100, "url": "decider.bin"},
			{"id": "decider-merges", "size": 100, "url": "merges.txt"},
			{"id": "writer-weights", "size": 100, "url": "writer.bin"},
			{"id": "writer-merges", "size": 100, "url": "wmerges.txt"}
		]
	}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "artifacts.json") {
			http.ServeContent(w, r, "artifacts.json", time.Time{}, bytes.NewReader(manifest))
			return
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(make([]byte, 100)))
	}))
	defer srv.Close()

	fs := mem.New()
	store := artifacts.New(fs)

	var posts []Event
	c := core{
		setup: Setup{
			Decider: DeciderSpec{Weights: "decider-weights", Merges: "decider-merges"},
			Writer:  &WriterSpec{Weights: "writer-weights", Merges: "writer-merges"},
			AgentConfig: func(m Models) (agent.Config, error) {
				return agent.Config{
					Decider: m.Decider,
					Writer:  m.Writer,
					Tokens:  m.Tokens,
				}, nil
			},
		},
		store: store,
		files: fs,
		detect: func() (device.Profile, error) {
			return device.Profile{SIMD: true, Disk: device.Disk{Quota: 1000, Usage: 0}}, nil
		},
		bench:       func() device.Rate { return 2000 },
		post:        func(e Event) { posts = append(posts, e) },
		manifestURL: srv.URL + "/artifacts.json",
		newDecider: func(w *weights.Artifact, merges []byte, s DeciderSpec) (decider, error) {
			return &fakeDecider{}, nil
		},
		newWriter: func(w *weights.Artifact, merges []byte, s WriterSpec) (llm.Client, error) {
			return &fakeWriter{}, nil
		},
	}

	evt, err := c.handle(Request{Kind: RequestStart, Persisted: true})
	if err != nil {
		t.Fatal(err)
	}

	if evt.Kind != EventReady {
		t.Errorf("expected EventReady, got %v", evt.Kind)
	}
	if evt.WriterOff {
		t.Errorf("writer should not be off")
	}

	var hasProgress bool
	for _, p := range posts {
		if p.Kind == EventProgress && p.Done == p.Total {
			hasProgress = true
		}
	}
	if !hasProgress {
		t.Errorf("missing final progress events")
	}
}

// TODO: other tests per PLAN.md
