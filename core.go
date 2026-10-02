package agentworker

import (
	"webtyp.com/agent"
	"webtyp.com/artifacts"
	"webtyp.com/context"
	"webtyp.com/device"
	"webtyp.com/fetch"
	"webtyp.com/files"
	"webtyp.com/fmt"
	"webtyp.com/llm"
	"webtyp.com/weights"
)

const (
	errNotReady      = "agentworker: not ready; send RequestStart first"
	errNoArtifact    = "agentworker: the manifest has no artifact %q"
	errManifestFetch = "agentworker: %s answered %d"
)

// core is the Worker's logic over injected dependencies: Serve wires the browser ones, the tests
// fakes.
type core struct {
	setup       Setup
	store       *artifacts.Store
	files       files.Store // same directory: the decision cache lives here
	detect      func() (device.Profile, error)
	bench       func() device.Rate
	post        func(Event) // js.PostToPage in the Worker
	manifestURL string
	newDecider  func(w *weights.Artifact, merges []byte, s DeciderSpec) (decider, error)
	newWriter   func(w *weights.Artifact, merges []byte, s WriterSpec) (llm.Client, error)

	agent     *agent.Agent // nil until ready
	decider   decider
	writerOff bool
	cacheDone bool // the decision cache was loaded or saved during this start (D27)
}

// decider is what agentworker needs from the decision model (qwen.Model satisfies it).
type decider interface {
	llm.Decider
	llm.TokenCounter
	SaveDecisionCache() ([]byte, bool, error)
	LoadDecisionCache([]byte) error
}

// handle answers one request. A failure is returned as an error: the page receives it as
// EventFailed.
func (c *core) handle(ctx *context.Context, r Request) (*Event, error) {
	if r.Kind == RequestStart {
		return c.start(r.Persisted)
	}
	if c.agent == nil {
		return nil, fmt.Err(errNotReady)
	}
	var reply agent.Reply
	var err error
	switch r.Kind {
	case RequestRun:
		reply, err = c.agent.Run(ctx, r.SessionID, r.Text)
	case RequestConfirm:
		reply, err = c.agent.Confirm(ctx, r.SessionID)
	case RequestDecline:
		reply, err = c.agent.Decline(ctx, r.SessionID)
	default:
		return nil, fmt.Errf(errBadMessage, fmt.Sprintf("request kind %d", r.Kind))
	}
	if err != nil {
		return nil, err
	}
	c.saveDecisionCache()
	return replyEvent(r.SessionID, reply.Text, reply.Pending), nil
}

// start checks the device, downloads what is missing, loads the models and builds the agent.
func (c *core) start(persisted bool) (*Event, error) {
	if c.agent != nil {
		return &Event{Kind: EventReady, WriterOff: c.writerOff, Persisted: persisted}, nil
	}
	p, err := c.detect()
	if err != nil {
		return nil, err
	}
	rate := c.bench()
	m, err := c.manifest()
	if err != nil {
		return nil, err
	}

	decW, err := find(m, c.setup.Decider.Weights)
	if err != nil {
		return nil, err
	}
	decM, err := find(m, c.setup.Decider.Merges)
	if err != nil {
		return nil, err
	}
	if short := blocking(decW.Needs.Check(p, rate)); len(short) > 0 {
		return &Event{Kind: EventAsleep, Artifact: decW.ID, Shortfalls: short}, nil
	}
	need := []artifacts.Artifact{decW, decM}

	// The writer goes first when the device cannot take both models (D-PWA-2).
	var writer []artifacts.Artifact // weights, merges; nil = no writer
	writerOff := false
	if ws := c.setup.Writer; ws != nil {
		wW, err := find(m, ws.Weights)
		if err != nil {
			return nil, err
		}
		wM, err := find(m, ws.Merges)
		if err != nil {
			return nil, err
		}
		writer = []artifacts.Artifact{wW, wM}
		if len(blocking(wW.Needs.Check(p, rate))) > 0 || p.Free() < c.missing(need)+c.missing(writer) {
			writer, writerOff = nil, true
		}
	}

	for _, a := range need {
		err := c.ensure(a, p)
		if err == artifacts.ErrNoSpace {
			return &Event{Kind: EventAsleep, Artifact: a.ID, Shortfalls: []string{device.ShortSpace.String()}}, nil
		}
		if err != nil {
			return nil, err
		}
	}
	for _, a := range writer {
		err := c.ensure(a, p)
		if err == artifacts.ErrNoSpace {
			writer, writerOff = nil, true
			break
		}
		if err != nil {
			return nil, err
		}
	}

	w, merges, err := c.open(decW, decM)
	if err != nil {
		return nil, err
	}
	d, err := c.newDecider(w, merges, c.setup.Decider)
	if err != nil {
		return nil, err
	}
	var wr llm.Client
	if writer != nil {
		w, merges, err := c.open(writer[0], writer[1])
		if err != nil {
			return nil, err
		}
		if wr, err = c.newWriter(w, merges, *c.setup.Writer); err != nil {
			return nil, err
		}
	}
	c.loadDecisionCache(d)

	cfg, err := c.setup.AgentConfig(Models{Decider: d, Writer: wr, Tokens: d})
	if err != nil {
		return nil, err
	}
	cfg.Decider, cfg.Writer, cfg.Tokens = d, wr, d
	ag, err := agent.New(cfg)
	if err != nil {
		return nil, err
	}

	// Older versions go only now, once the new ones work (D-PWA-8).
	if err := c.store.Prune(append(need, writer...)); err != nil {
		c.post(Event{Kind: EventFailed, Text: err.Error()})
	}
	c.agent, c.decider, c.writerOff = ag, d, writerOff
	return &Event{Kind: EventReady, WriterOff: writerOff, Persisted: persisted}, nil
}

// manifest fetches and parses the artifacts manifest. Call it from a goroutine: it blocks on the
// network.
func (c *core) manifest() (artifacts.Manifest, error) {
	type result struct {
		m   artifacts.Manifest
		err error
	}
	ch := make(chan result, 1)
	fetch.Get(c.manifestURL).Send(func(resp *fetch.Response, err error) {
		switch {
		case err != nil:
			ch <- result{err: err}
		case resp.Status != 200:
			ch <- result{err: fmt.Errf(errManifestFetch, c.manifestURL, resp.Status)}
		default:
			m, err := artifacts.ParseManifest(resp.Body())
			ch <- result{m, err}
		}
	})
	res := <-ch
	return res.m, res.err
}

// ensure downloads a, posting at most one EventProgress per 1 % of its size plus the final one.
func (c *core) ensure(a artifacts.Artifact, p device.Profile) error {
	last := int64(-1)
	return c.store.Ensure(a, p, func(done, total int64) {
		pct := int64(100)
		if total > 0 {
			pct = done * 100 / total
		}
		if pct <= last && done != total {
			return
		}
		last = pct
		c.post(Event{Kind: EventProgress, Artifact: a.ID, Done: done, Total: total})
	})
}

// missing returns the bytes still to download for the artifacts not yet stored.
func (c *core) missing(as []artifacts.Artifact) int64 {
	var n int64
	for _, a := range as {
		if !c.store.Has(a) {
			n += a.Size
		}
	}
	return n
}

// open reads a model's stored weights and merges.
func (c *core) open(w, m artifacts.Artifact) (*weights.Artifact, []byte, error) {
	data, err := c.store.Read(w)
	if err != nil {
		return nil, nil, err
	}
	art, err := weights.Open(data)
	if err != nil {
		return nil, nil, err
	}
	merges, err := c.store.Read(m)
	if err != nil {
		return nil, nil, err
	}
	return art, merges, nil
}

// loadDecisionCache restores the decision cache saved by an earlier start. A cache the model
// refuses belongs to other weights: it is removed and saved again after the first reply.
func (c *core) loadDecisionCache(d decider) {
	data, err := c.files.ReadFile(DecisionCacheFile)
	if err != nil {
		return
	}
	if d.LoadDecisionCache(data) != nil {
		_ = c.files.RemoveFile(DecisionCacheFile)
		return
	}
	c.cacheDone = true
}

// saveDecisionCache keeps the decision cache once per start, after the first decision filled it:
// the tool-list prefix does not change while the Worker lives (D27). A failure is reported, not
// fatal.
func (c *core) saveDecisionCache() {
	if c.cacheDone {
		return
	}
	data, ok, err := c.decider.SaveDecisionCache()
	if err == nil && !ok {
		return // no decision yet; try after the next reply
	}
	c.cacheDone = true
	if err == nil {
		err = c.files.WriteFile(DecisionCacheFile, data)
	}
	if err != nil {
		c.post(Event{Kind: EventFailed, Text: err.Error()})
	}
}

func find(m artifacts.Manifest, id string) (artifacts.Artifact, error) {
	a, ok := m.Find(id)
	if !ok {
		return a, fmt.Errf(errNoArtifact, id)
	}
	return a, nil
}

// blocking returns the names of the shortfalls that forbid the download.
func blocking(s []device.Shortfall) []string {
	var out []string
	for _, f := range s {
		if f.Blocking() {
			out = append(out, f.String())
		}
	}
	return out
}
