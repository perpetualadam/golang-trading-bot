# Upgrade and migration notes

This project is built so core trading logic stays in Go while storage, messaging, and ML can be swapped without rewriting the engine.

## SQLite to PostgreSQL

- Replace `internal/storage/store.go` DSN handling: use `database/sql` with `github.com/lib/pq` or `github.com/uptrace/bun/driver/pgdriver`.
- Keep the same table shapes (`events`, `fills`) or add a migration tool (e.g. goose, golang-migrate).
- Config: set `storage.driver: postgres` and `storage.dsn` to a Postgres URL.

## In-memory / channels to Redis

- Today: goroutines + `rate.Limiter` and in-process state.
- Later: publish order intents and fills to Redis streams or lists; consume with workers. Keep `exchange.Connector` implementations thin so they can be called from workers.

## Heavy ML (Python, GPU, Transformers)

- Keep `internal/ml.ONNXRunner` as the boundary: implement with `onnxruntime` (CGO) or HTTP client to a sidecar.
- Training stays outside this binary; export ONNX or serve a small HTTP infer service.

## Multi-exchange live adapters

- Built-in REST `Connector` types live under `internal/exchange/` (Binance spot, Bybit v5, OKX, Deribit, Kraken, Coinbase Exchange, Alpaca, OANDA, Yahoo chart, Finnhub, Twelve Data, Polygon, plus `PaperConnector`).
- Enable entries under `exchanges:` in YAML; `cmd/bot` calls `exchange.RegisterFromConfig` after optional key decryption.
- **CCXT sidecar:** set `name: ccxt` and `extra.sidecar_url` to a small HTTP service you host (Python/Node + CCXT) implementing the routes documented on `CcxtSidecar` in `internal/exchange/ccxt_sidecar.go` (`/book`, `/order`, `/balances`, `/positions`, `/cancel`, `/cancel_all`, `/health`).
- Yahoo / Finnhub / Twelve Data / Polygon adapters are **read-only** (quotes for risk or research); orders return `exchange.ErrReadOnly`.
- Optional: add WebSocket feeds per venue without changing the `Connector` interface used by the router.

## Orchestration

- Single binary fits systemd or Docker. For Kubernetes later: one Deployment per role (executor, router, risk sidecar) only if you outgrow one process; the code is modular to split by package boundaries.

## Encrypted API keys

- Set `TRADING_MASTER_KEY` in the environment. Store ciphertext in YAML (`api_key_enc`, `api_secret_enc`, `passphrase_enc`). Offline helper: `go run ./cmd/secrets encrypt "plaintext"` (loads `.env` like the bot). Decrypt at startup in `cmd/bot` via `internal/secrets`.

## Telegram

- Set `telegram.bot_token` or env `TRADING_TELEGRAM_BOT_TOKEN` if you wire viper to read it (extend `config.Load` if needed).
- Always set `allowed_user_ids` in production.
