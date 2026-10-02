package agentworker

import (
	"webtyp.com/nn"
)

// benchKernel returns one run of the benchmark kernel (see BenchBudgetMs). The matrix is
// allocated and filled once, deterministically.
func benchKernel() func() {
	const rows, cols = 3584, 1024
	dst := make([]float32, rows)
	xq := make([]int8, cols)
	xs := make([]float32, cols/32)
	q := make([]byte, rows*cols)
	scales := make([]float32, rows*(cols/32))

	for i := range xq {
		xq[i] = int8(i % 127)
	}
	for i := range xs {
		xs[i] = float32(i) / 100
	}
	for i := range q {
		q[i] = byte(i % 127)
	}
	for i := range scales {
		scales[i] = float32(i) / 100
	}

	return func() {
		_ = nn.MatVecQ8Block32(dst, xq, xs, q, scales, rows, cols)
	}
}
