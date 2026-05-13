package ml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// HTTPInfer POSTs a JSON payload to a remote inference service and reads a float score vector.
// Request body: {"features":[...],"input":[...]} (both keys set for compatibility).
// Response: first non-empty array among keys output, outputs, y, scores, logits
// or a bare JSON array of numbers.
type HTTPInfer struct {
	URL    string
	Client *http.Client
}

// NewHTTPInfer returns a predictor; client must be non-nil.
func NewHTTPInfer(url string, client *http.Client) *HTTPInfer {
	return &HTTPInfer{URL: url, Client: client}
}

func (h *HTTPInfer) Predict(features []float32) ([]float32, error) {
	if h == nil || h.Client == nil || h.URL == "" {
		return nil, nil
	}
	payload := map[string]any{
		"features": features,
		"input":    features,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("remote infer: status %d body %s", resp.StatusCode, string(b))
	}
	return parseInferResponse(b)
}

func parseInferResponse(b []byte) ([]float32, error) {
	var top []float64
	if err := json.Unmarshal(b, &top); err == nil && len(top) > 0 {
		return floats64To32(top), nil
	}
	var wrap map[string]json.RawMessage
	if err := json.Unmarshal(b, &wrap); err != nil {
		return nil, fmt.Errorf("infer response: %w", err)
	}
	for _, key := range []string{"output", "outputs", "y", "scores", "logits", "data"} {
		raw, ok := wrap[key]
		if !ok || len(raw) == 0 {
			continue
		}
		var xs []float64
		if err := json.Unmarshal(raw, &xs); err == nil && len(xs) > 0 {
			return floats64To32(xs), nil
		}
	}
	return nil, fmt.Errorf("infer response: no numeric array in keys output/outputs/y/scores/logits")
}

func floats64To32(xs []float64) []float32 {
	out := make([]float32, len(xs))
	for i, x := range xs {
		out[i] = float32(x)
	}
	return out
}
