package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tradingbot/pkg/types"
)

func TestNetOpenQtyByVenueSymbol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	ins := types.Instrument{Venue: "OANDA", Symbol: "EUR_USD"}
	t0 := time.Now().UTC()
	if err := s.LogFill(ctx, types.Fill{Instrument: ins, Side: types.SideBuy, Qty: 100, Price: 1.1, Time: t0}); err != nil {
		t.Fatal(err)
	}
	if err := s.LogFill(ctx, types.Fill{Instrument: ins, Side: types.SideSell, Qty: 40, Price: 1.2, Time: t0}); err != nil {
		t.Fatal(err)
	}
	m, err := s.NetOpenQtyByVenueSymbol(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := m["OANDA:EUR_USD"]; got != 60 {
		t.Fatalf("net qty got %v want 60", got)
	}
}
