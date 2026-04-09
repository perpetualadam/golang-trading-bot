package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/subosito/gotenv"
	"tradingbot/internal/app"
	"tradingbot/internal/backtest"
	"tradingbot/internal/config"
	"tradingbot/internal/exchange"
	"tradingbot/internal/execution"
	"tradingbot/internal/metrics"
	"tradingbot/internal/notify"
	"tradingbot/internal/portfolio"
	"tradingbot/internal/risk"
	"tradingbot/internal/secrets"
	"tradingbot/internal/storage"
	"tradingbot/internal/strategy"
	"tradingbot/pkg/types"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to YAML config")
	flag.Parse()

	// Load .env: process cwd, then same directory as config file (e.g. configs/.env).
	_ = gotenv.Load()
	if abs, err := filepath.Abs(*cfgPath); err == nil {
		_ = gotenv.Load(filepath.Join(filepath.Dir(abs), ".env"))
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal().Err(err).Msg("config")
	}

	level, err := zerolog.ParseLevel(cfg.Runtime.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.TimeFieldFormat = time.RFC3339
	zlog := zerolog.New(os.Stdout).With().Timestamp().Logger().Level(level)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.Mode() == types.ModeBacktest {
		runBacktest(ctx, cfg, zlog)
		return
	}

	// Optional decrypt for live keys (populate connectors when adapters exist).
	if k, err := secrets.DeriveKey(); err == nil {
		for i := range cfg.Exchanges {
			if cfg.Exchanges[i].APIKeyEnc != "" {
				if plain, err := secrets.DecryptString(cfg.Exchanges[i].APIKeyEnc, k); err == nil {
					cfg.Exchanges[i].APIKeyEnc = plain // repurpose field in-memory only
				}
			}
			if cfg.Exchanges[i].APISecretEnc != "" {
				if plain, err := secrets.DecryptString(cfg.Exchanges[i].APISecretEnc, k); err == nil {
					cfg.Exchanges[i].APISecretEnc = plain
				}
			}
			if cfg.Exchanges[i].PassphraseEnc != "" {
				if plain, err := secrets.DecryptString(cfg.Exchanges[i].PassphraseEnc, k); err == nil {
					cfg.Exchanges[i].PassphraseEnc = plain
				}
			}
		}
	}

	reg := exchange.NewRegistry()
	paper := exchange.NewPaperConnector("PAPER", 100_000)
	reg.Register(paper)
	if err := exchange.RegisterFromConfig(reg, cfg.Exchanges, zlog); err != nil {
		zlog.Fatal().Err(err).Msg("exchange registry")
	}

	rt := execution.NewRouter(reg, cfg.Execution.MaxParallelVenues, cfg.Execution.LatencyWarnMs, cfg.Execution.LatencyCancelMs, 25)
	pf := portfolio.NewState(100_000)
	riskEng := risk.NewEngine(&cfg.Risk, pf)

	strats, err := strategy.BuildFromConfig(cfg.Strategies)
	if err != nil {
		zlog.Fatal().Err(err).Msg("strategies")
	}
	stack := strategy.NewStack(strats)

	var store *storage.Store
	if cfg.Storage.Driver == "sqlite" && cfg.Storage.DSN != "" {
		st, err := storage.OpenSQLite(cfg.Storage.DSN)
		if err != nil {
			zlog.Fatal().Err(err).Msg("storage")
		}
		defer st.Close()
		store = st
	}

	runner := app.NewRunner(cfg, zlog, reg, riskEng, rt, stack, pf, store, paper)

	if cfg.Runtime.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		srv := &http.Server{Addr: cfg.Runtime.MetricsAddr, Handler: mux}
		go func() {
			zlog.Info().Str("addr", cfg.Runtime.MetricsAddr).Msg("metrics")
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				zlog.Error().Err(err).Msg("metrics server")
			}
		}()
		defer func() { _ = srv.Shutdown(context.Background()) }()
	}

	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
		tb, err := notify.NewTelegramBot(cfg.Telegram.BotToken, cfg.Telegram.AllowedUserIDs, runner, zlog)
		if err != nil {
			zlog.Fatal().Err(err).Msg("telegram")
		}
		go tb.Run(ctx)
		zlog.Info().Msg("telegram bot listening for commands")
	} else if cfg.Telegram.Enabled {
		zlog.Warn().Msg("telegram.enabled but bot_token still empty after scanning env + .env files")
		for _, h := range cfg.StartupHints {
			zlog.Warn().Str("detail", h).Msg("telegram token hint")
		}
		if len(cfg.StartupHints) == 0 {
			zlog.Warn().Msg("no .env diagnostics — create .env in project root with: TRADING_TELEGRAM_BOT_TOKEN=your_token")
		}
	}

	if err := runner.StartTrading(ctx); err != nil {
		zlog.Fatal().Err(err).Msg("start")
	}

	zlog.Info().Str("mode", string(cfg.Mode())).Msg("capital-first bot running")
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Runtime.ShutdownTimeout)
	defer cancel()
	_ = runner.StopTrading(shutdownCtx)
	zlog.Info().Msg("shutdown complete")
}

func runBacktest(ctx context.Context, cfg *config.Root, zlog zerolog.Logger) {
	strats, err := strategy.BuildFromConfig(cfg.Strategies)
	if err != nil {
		zlog.Fatal().Err(err).Msg("strategies")
	}
	stack := strategy.NewStack(strats)
	ins := types.Instrument{Venue: "PAPER", Symbol: "BTCUSDT", Base: "BTC", Quote: "USDT", Kind: types.MarketSpot, Class: types.AssetCrypto}
	path := strings.TrimSpace(cfg.Backtest.DataDir)
	if path == "" {
		zlog.Fatal().Msg("backtest.data_dir is required (path to OHLCV CSV); no bundled sample data")
	}
	bs, err := backtest.LoadCSV(path, ins)
	if err != nil {
		zlog.Fatal().Err(err).Str("path", path).Msg("load csv")
	}
	windows := backtest.WalkForward(len(bs.Closes), cfg.Backtest.WalkForwardTrain, cfg.Backtest.WalkForwardTest, cfg.Backtest.WalkForwardStep)
	zlog.Info().Int("windows", len(windows)).Msg("walk-forward")
	if len(windows) == 0 {
		res := backtest.RunSimple(ctx, bs, stack, cfg.Backtest.FeeBps, cfg.Backtest.SlippageBps, cfg.Backtest.InitialEquity)
		zlog.Info().Float64("final", res.EquityFinal).Float64("max_dd_pct", res.MaxDDPct).Int("trades", res.Trades).Msg("backtest")
		return
	}
	for i, w := range windows {
		sub := sliceSeries(bs, w.TestStart, w.TestEnd)
		res := backtest.RunSimple(ctx, sub, stack, cfg.Backtest.FeeBps, cfg.Backtest.SlippageBps, cfg.Backtest.InitialEquity)
		zlog.Info().Int("i", i).Int("test_start", w.TestStart).Float64("final", res.EquityFinal).Msg("wf window")
	}
}

func sliceSeries(bs *backtest.BarSeries, a, b int) *backtest.BarSeries {
	out := &backtest.BarSeries{Instrument: bs.Instrument}
	out.Timestamps = append(out.Timestamps, bs.Timestamps[a:b]...)
	out.Opens = append(out.Opens, bs.Opens[a:b]...)
	out.Highs = append(out.Highs, bs.Highs[a:b]...)
	out.Lows = append(out.Lows, bs.Lows[a:b]...)
	out.Closes = append(out.Closes, bs.Closes[a:b]...)
	out.Volumes = append(out.Volumes, bs.Volumes[a:b]...)
	return out
}
