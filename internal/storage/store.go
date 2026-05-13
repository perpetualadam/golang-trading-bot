package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
	"tradingbot/pkg/types"
)

// Store is SQLite-backed event log (swap DSN for PostgreSQL later).
type Store struct {
	db *sql.DB
}

func OpenSQLite(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT
);
CREATE TABLE IF NOT EXISTS fills (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  venue TEXT,
  symbol TEXT,
  side TEXT,
  qty REAL,
  price REAL,
  fee REAL
);
`)
	return err
}

func (s *Store) LogEvent(ctx context.Context, kind string, payload any) error {
	var b []byte
	if payload != nil {
		var err error
		b, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(ts, kind, payload) VALUES(?,?,?)`,
		time.Now().Unix(), kind, string(b))
	return err
}

func (s *Store) LogFill(ctx context.Context, f types.Fill) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO fills(ts, venue, symbol, side, qty, price, fee) VALUES(?,?,?,?,?,?,?)`,
		f.Time.Unix(), string(f.Instrument.Venue), f.Instrument.Symbol, string(f.Side), f.Qty, f.Price, f.Fee)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// FillSummary is aggregate stats from the fills table (execution records).
type FillSummary struct {
	TotalFills int64
	Fills24h   int64
	Buys       int64
	Sells      int64
	FeesTotal  float64
	// NetCashFlow uses the same buy/sell cash approximation as DailyPnLFromFills (not closed-trade PnL).
	NetCashFlow float64
}

// FillSummary returns lifetime and last-24h fill stats.
func (s *Store) FillSummary(ctx context.Context) (FillSummary, error) {
	since := time.Now().UTC().Add(-24 * time.Hour).Unix()
	row := s.db.QueryRowContext(ctx, `
SELECT
  COUNT(*),
  SUM(CASE WHEN ts >= ? THEN 1 ELSE 0 END),
  SUM(CASE WHEN side = 'buy' THEN 1 ELSE 0 END),
  SUM(CASE WHEN side = 'sell' THEN 1 ELSE 0 END),
  COALESCE(SUM(fee), 0),
  COALESCE(SUM(CASE WHEN side = 'buy' THEN -(qty*price+fee) ELSE (qty*price-fee) END), 0)
FROM fills`, since)
	var fs FillSummary
	if err := row.Scan(&fs.TotalFills, &fs.Fills24h, &fs.Buys, &fs.Sells, &fs.FeesTotal, &fs.NetCashFlow); err != nil {
		return fs, fmt.Errorf("fill summary: %w", err)
	}
	return fs, nil
}

// DailyPnLFromFills approximates realized PnL from fills table (placeholder; upgrade with positions).
func (s *Store) DailyPnLFromFills(ctx context.Context) (float64, error) {
	since := time.Now().UTC().Add(-24 * time.Hour).Unix()
	row := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(
	CASE WHEN side='buy' THEN -(qty*price+fee) ELSE (qty*price-fee) END
),0) FROM fills WHERE ts >= ?`, since)
	var pnl float64
	if err := row.Scan(&pnl); err != nil {
		return 0, fmt.Errorf("daily pnl: %w", err)
	}
	return pnl, nil
}
