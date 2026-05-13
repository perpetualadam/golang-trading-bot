package app

import (
	"testing"

	"tradingbot/internal/config"
	"tradingbot/pkg/types"
)

func TestExecutionVenueChain_primaryAndFallbacksDeduped(t *testing.T) {
	r := &Runner{cfg: &config.Root{Execution: config.ExecutionConfig{
		RouterFallbackVenues: []string{"okx", "BINANCE", "okx"},
	}}}
	got := r.executionVenueChain("BINANCE")
	want := []types.Venue{"BINANCE", "OKX"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestExecutionVenueChain_emptyPrimaryUsesPaper(t *testing.T) {
	r := &Runner{cfg: &config.Root{Execution: config.ExecutionConfig{
		RouterFallbackVenues: []string{"OKX"},
	}}}
	got := r.executionVenueChain("")
	if len(got) != 2 || got[0] != "PAPER" || got[1] != "OKX" {
		t.Fatalf("got %v", got)
	}
}
