package ml

import (
	"testing"
)

func TestParseInferResponse_object(t *testing.T) {
	out, err := parseInferResponse([]byte(`{"output": [0.5, -0.25]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != 0.5 || out[1] != -0.25 {
		t.Fatalf("%v", out)
	}
}

func TestParseInferResponse_array(t *testing.T) {
	out, err := parseInferResponse([]byte(`[1, 2, 3]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatal(out)
	}
}
