package agentworker

import (
	"webtyp.com/context"
	"webtyp.com/llm"
)

const nanosPerMilli = 1_000_000

// timedDecider measures every decision of the agent's turn into c.decides.
type timedDecider struct {
	llm.Decider
	c *core
}

func (t timedDecider) Decide(ctx *context.Context, q llm.Question) (llm.Decision, error) {
	start := t.c.now()
	d, err := t.Decider.Decide(ctx, q)
	t.c.decides = append(t.c.decides, int64(t.c.now()-start)/nanosPerMilli)
	return d, err
}
