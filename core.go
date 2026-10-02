package agentworker

import (
	"webtyp.com/agent"
	"webtyp.com/artifacts"
	"webtyp.com/device"
	"webtyp.com/fetch"
	"webtyp.com/files"
	"webtyp.com/fmt"
	"webtyp.com/llm"
	"webtyp.com/weights"
)

type core struct {
	setup       Setup
	store       *artifacts.Store
	files       files.Store
	detect      func() (device.Profile, error)
	bench       func() device.Rate
	post        func(Event)
	manifestURL string
	newDecider  func(w *weights.Artifact, merges []byte, s DeciderSpec) (decider, error)
	newWriter   func(w *weights.Artifact, merges []byte, s WriterSpec) (llm.Client, error)
	agent       *agent.Agent
	decider     decider
	cacheSaved  bool
}

type decider interface {
	llm.Decider
	llm.TokenCounter
	SaveDecisionCache() ([]byte, bool, error)
	LoadDecisionCache([]byte) error
}

func (c *core) handle(r Request) (*Event, error) {
	switch r.Kind {
	case RequestStart:
		if c.agent != nil {
			return &Event{Kind: EventReady, Persisted: r.Persisted}, nil
		}
		e, err := c.start(r.Persisted)
		if err != nil {
			return &Event{Kind: EventFailed, Text: err.Error()}, nil
		}
		return e, nil
	case RequestRun:
		if c.agent == nil {
			return nil, fmt.Errf("agentworker: not ready; send RequestStart first")
		}
		reply, err := c.agent.Run(nil, r.SessionID, r.Text)
		return c.handleReply(r.SessionID, reply, err)
	case RequestConfirm:
		if c.agent == nil {
			return nil, fmt.Errf("agentworker: not ready; send RequestStart first")
		}
		reply, err := c.agent.Confirm(nil, r.SessionID)
		return c.handleReply(r.SessionID, reply, err)
	case RequestDecline:
		if c.agent == nil {
			return nil, fmt.Errf("agentworker: not ready; send RequestStart first")
		}
		reply, err := c.agent.Decline(nil, r.SessionID)
		return c.handleReply(r.SessionID, reply, err)
	default:
		return nil, fmt.Errf("agentworker: unknown request kind %v", r.Kind)
	}
}

func (c *core) handleReply(sessionID string, reply agent.Reply, err error) (*Event, error) {
	if err != nil {
		return &Event{Kind: EventFailed, Text: err.Error()}, nil
	}

	if !c.cacheSaved {
		data, ok, err := c.decider.SaveDecisionCache()
		if err != nil {
			c.post(Event{Kind: EventFailed, Text: err.Error()})
		} else if ok {
			c.files.WriteFile(DecisionCacheFile, data)
			c.cacheSaved = true
		}
	}

	var pending []Pending
	for _, p := range reply.Pending {
		pending = append(pending, Pending{ID: p.ID, Name: p.Name, Input: p.Input})
	}

	return &Event{Kind: EventReply, SessionID: sessionID, Text: reply.Text, Pending: pending}, nil
}

func (c *core) start(persisted bool) (*Event, error) {
	p, err := c.detect()
	if err != nil {
		return nil, err
	}
	rate := c.bench()

	ch := make(chan struct {
		m   artifacts.Manifest
		err error
	})
	fetch.Get(c.manifestURL).Send(func(resp *fetch.Response, err error) {
		if err != nil {
			ch <- struct {
				m   artifacts.Manifest
				err error
			}{err: err}
			return
		}
		m, err := artifacts.ParseManifest(resp.Body())
		ch <- struct {
			m   artifacts.Manifest
			err error
		}{m, err}
	})
	res := <-ch
	if res.err != nil {
		return nil, res.err
	}
	m := res.m

	decWeightsArtifact, decWOk := m.Find(c.setup.Decider.Weights)
	if !decWOk {
		return nil, fmt.Errf("agentworker: the manifest has no artifact %q", c.setup.Decider.Weights)
	}
	decMergesArtifact, decMOk := m.Find(c.setup.Decider.Merges)
	if !decMOk {
		return nil, fmt.Errf("agentworker: the manifest has no artifact %q", c.setup.Decider.Merges)
	}

	decShortfalls := decWeightsArtifact.Needs.Check(p, rate)
	var blocking []string
	for _, s := range decShortfalls {
		if s.Blocking() {
			blocking = append(blocking, s.String())
		}
	}
	if len(blocking) > 0 {
		return &Event{Kind: EventAsleep, Artifact: c.setup.Decider.Weights, Shortfalls: blocking}, nil
	}

	writerOff := false
	var writerWeightsArtifact, writerMergesArtifact *artifacts.Artifact
	if c.setup.Writer != nil {
		writerWArt, writerWOk := m.Find(c.setup.Writer.Weights)
		if !writerWOk {
			return nil, fmt.Errf("agentworker: the manifest has no artifact %q", c.setup.Writer.Weights)
		}
		writerWeightsArtifact = &writerWArt

		writerMArt, writerMOk := m.Find(c.setup.Writer.Merges)
		if !writerMOk {
			return nil, fmt.Errf("agentworker: the manifest has no artifact %q", c.setup.Writer.Merges)
		}
		writerMergesArtifact = &writerMArt

		wShortfalls := writerWeightsArtifact.Needs.Check(p, rate)
		var wBlocking []string
		for _, s := range wShortfalls {
			if s.Blocking() {
				wBlocking = append(wBlocking, s.String())
			}
		}
		if len(wBlocking) > 0 {
			writerOff = true
		} else {
			var needDownload int64
			if ok := c.store.Has(decWeightsArtifact); !ok {
				needDownload += decWeightsArtifact.Size
			}
			if ok := c.store.Has(decMergesArtifact); !ok {
				needDownload += decMergesArtifact.Size
			}
			if ok := c.store.Has(*writerWeightsArtifact); !ok {
				needDownload += writerWeightsArtifact.Size
			}
			if ok := c.store.Has(*writerMergesArtifact); !ok {
				needDownload += writerMergesArtifact.Size
			}
			if p.Free() < needDownload {
				writerOff = true
			}
		}
	}

	ensure := func(a *artifacts.Artifact, progress func(done, total int64)) error {
		var lastPct int64 = -1
		return c.store.Ensure(*a, p, func(done, total int64) {
			pct := done * 100 / total
			if pct > lastPct || done == total {
				lastPct = pct
				progress(done, total)
			}
		})
	}

	err = ensure(&decWeightsArtifact, func(done, total int64) {
		c.post(Event{Kind: EventProgress, Artifact: decWeightsArtifact.ID, Done: done, Total: total})
	})
	if err == artifacts.ErrNoSpace {
		return &Event{Kind: EventAsleep, Artifact: decWeightsArtifact.ID, Shortfalls: []string{"space"}}, nil
	}
	if err != nil {
		return nil, err
	}

	err = ensure(&decMergesArtifact, func(done, total int64) {
		c.post(Event{Kind: EventProgress, Artifact: decMergesArtifact.ID, Done: done, Total: total})
	})
	if err == artifacts.ErrNoSpace {
		return &Event{Kind: EventAsleep, Artifact: decMergesArtifact.ID, Shortfalls: []string{"space"}}, nil
	}
	if err != nil {
		return nil, err
	}

	if !writerOff && c.setup.Writer != nil {
		err = ensure(writerWeightsArtifact, func(done, total int64) {
			c.post(Event{Kind: EventProgress, Artifact: writerWeightsArtifact.ID, Done: done, Total: total})
		})
		if err == artifacts.ErrNoSpace {
			writerOff = true
		} else if err != nil {
			return nil, err
		}

		if !writerOff {
			err = ensure(writerMergesArtifact, func(done, total int64) {
				c.post(Event{Kind: EventProgress, Artifact: writerMergesArtifact.ID, Done: done, Total: total})
			})
			if err == artifacts.ErrNoSpace {
				writerOff = true
			} else if err != nil {
				return nil, err
			}
		}
	}

	decWBytes, err := c.store.Read(decWeightsArtifact)
	if err != nil {
		return nil, err
	}
	decWeightsArt, err := weights.Open(decWBytes)
	if err != nil {
		return nil, err
	}
	decMBytes, err := c.store.Read(decMergesArtifact)
	if err != nil {
		return nil, err
	}
	c.decider, err = c.newDecider(decWeightsArt, decMBytes, c.setup.Decider)
	if err != nil {
		return nil, err
	}

	var writer llm.Client
	if !writerOff && c.setup.Writer != nil {
		wWBytes, err := c.store.Read(*writerWeightsArtifact)
		if err != nil {
			return nil, err
		}
		wWeightsArt, err := weights.Open(wWBytes)
		if err != nil {
			return nil, err
		}
		wMBytes, err := c.store.Read(*writerMergesArtifact)
		if err != nil {
			return nil, err
		}
		writer, err = c.newWriter(wWeightsArt, wMBytes, *c.setup.Writer)
		if err != nil {
			return nil, err
		}
	}

	cacheData, err := c.files.ReadFile(DecisionCacheFile)
	if err == nil {
		err = c.decider.LoadDecisionCache(cacheData)
		if err != nil {
			c.files.RemoveFile(DecisionCacheFile)
		}
	}

	models := Models{
		Decider: c.decider,
		Writer:  writer,
		Tokens:  c.decider,
	}

	cfg, err := c.setup.AgentConfig(models)
	if err != nil {
		return nil, err
	}
	cfg.Decider = models.Decider
	cfg.Writer = models.Writer
	cfg.Tokens = models.Tokens

	c.agent, err = agent.New(cfg)
	if err != nil {
		return nil, err
	}

	keep := []string{decWeightsArtifact.ID, decMergesArtifact.ID}
	if !writerOff && c.setup.Writer != nil {
		keep = append(keep, writerWeightsArtifact.ID, writerMergesArtifact.ID)
	}
	c.store.Prune(nil)

	return &Event{Kind: EventReady, WriterOff: writerOff, Persisted: persisted}, nil
}
