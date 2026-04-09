package ml

// ONNXRunner is a hook for github.com/yalue/onnxruntime_go (CGO) or remote HTTP infer.
// Kept interface-only so default builds stay pure Go.
type ONNXRunner interface {
	Predict(features []float32) ([]float32, error)
}

type noopONNX struct{}

func (noopONNX) Predict(features []float32) ([]float32, error) {
	return nil, nil
}

// NewNoopONNX returns stub runner.
func NewNoopONNX() ONNXRunner { return noopONNX{} }
