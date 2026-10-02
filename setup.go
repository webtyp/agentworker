package agentworker

import (
	"webtyp.com/agent"
	"webtyp.com/decoder"
	"webtyp.com/llm"
)

// DeciderSpec names the decision model's files in the artifacts manifest and its shape.
type DeciderSpec struct {
	Weights     string         // artifact id of the weights, e.g. "decider-0.8b"
	Merges      string         // artifact id of the companion .merges file
	Decoder     decoder.Config // qwen.Qwen35_08B for decider-0.8b
	Temperature float64        // decision temperature (decider-0.8b: 1.03)
}

// WriterSpec names the optional writer model's files and its shape.
type WriterSpec struct {
	Weights string // e.g. "writer-lfm-350m"
	Merges  string
	Decoder decoder.Config // lfm.LFM25_350M
}

// Models are what agentworker built; the application's AgentConfig receives them.
type Models struct {
	Decider llm.Decider
	Writer  llm.Client       // nil when the device could not take the writer (D-PWA-2)
	Tokens  llm.TokenCounter // the decider's tokenizer
}

// Setup is everything the Worker binary declares.
type Setup struct {
	Dir     string      // OPFS directory of this module (D-PWA-10), e.g. "cote"
	Decider DeciderSpec // required
	Writer  *WriterSpec // optional; dropped when the device cannot hold or run it
	// AgentConfig returns the application's agent configuration (Texts, Templates, Guard, Memory,
	// IDGen, Clock, ToolIndex, LocalTools, MCPServers...). agentworker then sets Decider, Writer
	// and Tokens from m, overriding whatever the function put there.
	AgentConfig func(m Models) (agent.Config, error)
}

const (
	// DecisionCacheFile is where the decision cache is kept, in Setup.Dir (D27).
	DecisionCacheFile = "decision.cache"
	// BenchBudgetMs is how long the Worker measures its kernel: one nn.MatVecQ8Block32 over a
	// 3584×1024 int8 matrix. A manifest's min_rate is in runs per second of that kernel (≈ 770
	// plain, ≈ 1640 SIMD under TinyGo 0.41 on the machine of nn/docs/PERFORMANCE.md).
	BenchBudgetMs = 200
)
