package ml

// ONNXRunner is the inference boundary for the ml_meta strategy.
//
// ## What is integrated in this repo
//   - cmd/bot wires StrategyDeps.Infer into strategy.BuildFromConfig (see main.go).
//   - ml_meta calls Predict([]float32) on that runner; noop returns nil → no signals.
//   - ml.enable_remote_infer_url → ml.NewHTTPInfer POSTs features; see http_infer.go.
//
// ## onnx_model_path (YAML)
//   Not loaded automatically yet. If you set it without enable_remote_infer_url, startup logs a warn.
//
// ## Local ONNX (CGO) — where to look
//   Primary community option: https://github.com/yalue/onnxruntime_go
//   — Go bindings to Microsoft’s ONNX Runtime C API; you install the onnxruntime
//   shared library for your OS/arch, enable CGO, and implement ONNXRunner by mapping
//   your model’s input/output tensors to/from []float32 (shape is model-specific).
//   Upstream runtime downloads: https://github.com/microsoft/onnxruntime/releases
//   Examples: https://github.com/yalue/onnxruntime_go_examples
//
// Alternatives: run inference in a small sidecar (Python onnxruntime, etc.) and point
// ml.enable_remote_infer_url at it — no CGO in the Go binary.
type ONNXRunner interface {
	Predict(features []float32) ([]float32, error)
}

type noopONNX struct{}

func (noopONNX) Predict(features []float32) ([]float32, error) {
	return nil, nil
}

// NewNoopONNX returns stub runner.
func NewNoopONNX() ONNXRunner { return noopONNX{} }
