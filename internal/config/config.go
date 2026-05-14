package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"github.com/subosito/gotenv"
	"tradingbot/pkg/types"
)

// Root is the full application configuration.
type Root struct {
	Runtime    RuntimeConfig   `mapstructure:"runtime"`
	Risk       RiskConfig      `mapstructure:"risk"`
	Execution  ExecutionConfig `mapstructure:"execution"`
	Storage    StorageConfig   `mapstructure:"storage"`
	Telegram   TelegramConfig  `mapstructure:"telegram"`
	Exchanges  []ExchangeCfg   `mapstructure:"exchanges"`
	Strategies []StrategyCfg   `mapstructure:"strategies"`
	Backtest   BacktestConfig  `mapstructure:"backtest"`
	ML         MLConfig        `mapstructure:"ml"`

	// StartupHints is filled by Load when Telegram token resolution needs explanation (not from YAML).
	StartupHints []string `mapstructure:"-"`
}

type RuntimeConfig struct {
	Mode            string        `mapstructure:"mode"` // paper, live, backtest
	LogLevel        string        `mapstructure:"log_level"`
	MetricsAddr     string        `mapstructure:"metrics_addr"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	// TradeLogVerbose adds intent_id, venue_chain, fees, rtt, and exchange ids to order_submit / order_filled.
	// Core side (buy/sell), qty, and notional % of equity are logged at info whenever orders run (paper or live).
	// In paper runtime mode with an external venue (e.g. OANDA), logs the would-be intent but does not send to the broker.
	TradeLogVerbose bool `mapstructure:"trade_log_verbose"`
	// LoopLogVerbose logs every trading loop iteration per instrument (mid, book top, signal count).
	LoopLogVerbose bool `mapstructure:"loop_log_verbose"`
	// LogReconcilePositionDrift compares broker open positions to SQLite fill-derived net qty per venue+symbol and logs warn on mismatch.
	LogReconcilePositionDrift bool `mapstructure:"log_reconcile_position_drift"`
	// ReconcileDriftMinUnits minimum |broker_qty − ledger_qty| to log (0 = log any drift above 1e-9).
	ReconcileDriftMinUnits float64 `mapstructure:"reconcile_drift_min_units"`
	// Instruments optional explicit loop symbols, e.g. ["OANDA:EUR_USD"]. Empty = union of enabled strategy universes, else default PAPER:BTCUSDT.
	Instruments []string `mapstructure:"instruments"`
}

type RiskConfig struct {
	// MaxRiskPerTradePct caps loss at the stop (when stop-loss is set) as % of equity,
	// or caps entry notional vs equity when no stop is used (legacy / non-bracket venues).
	MaxRiskPerTradePct    float64       `mapstructure:"max_risk_per_trade_pct"`
	MaxPortfolioLeverage  float64       `mapstructure:"max_portfolio_leverage"`
	MaxVenueExposurePct   float64       `mapstructure:"max_venue_exposure_pct"`
	MaxSlippageBpsDefault float64       `mapstructure:"max_slippage_bps_default"`
	DailyDrawdownWarnPct  float64       `mapstructure:"daily_drawdown_warn_pct"`
	DailyDrawdownSoftPct  float64       `mapstructure:"daily_drawdown_soft_pct"`
	DailyDrawdownHardPct  float64       `mapstructure:"daily_drawdown_hard_pct"`
	VaRHorizon            time.Duration `mapstructure:"var_horizon"`
	VaRConfidence         float64       `mapstructure:"var_confidence"`
	CVaRConfidence        float64       `mapstructure:"cvar_confidence"`
	KillSwitchFile        string        `mapstructure:"kill_switch_file"`
	MinBookDepthUSD       float64       `mapstructure:"min_book_depth_usd"`
	// RequireBrokerStops: when true, OANDA intents must include stop + take-profit prices.
	RequireBrokerStops bool `mapstructure:"require_broker_stops"`
	// MaxNotionalPerTradePct optional secondary cap: entry notional vs equity (0 = disabled).
	MaxNotionalPerTradePct float64 `mapstructure:"max_notional_per_trade_pct"`
	// MaxPositionHold wall-clock max time to hold a position opened by this bot (0 = disabled). Uses fill timestamps; does not apply to pre-existing broker positions until the next bot-originated add.
	MaxPositionHold time.Duration `mapstructure:"max_position_hold"`
	// ExitOnOppositeSignal: before a new entry, flatten if signal direction opposes current snapshot position (reverse waits until next tick).
	ExitOnOppositeSignal bool `mapstructure:"exit_on_opposite_signal"`
}

type ExecutionConfig struct {
	MaxParallelVenues     int     `mapstructure:"max_parallel_venues"`
	LatencyWarnMs         float64 `mapstructure:"latency_warn_ms"`
	LatencyCancelMs       float64 `mapstructure:"latency_cancel_ms"`
	TWAPMinSliceMs        int     `mapstructure:"twap_min_slice_ms"`
	EnableMakerPreference bool    `mapstructure:"enable_maker_preference"`
	// RouterDefaultRPS is the default token bucket rate for PlaceOrder per venue (0 = use router built-in default).
	RouterDefaultRPS float64 `mapstructure:"router_default_rps"`
	// RouterBurst is the token bucket burst size (0 = max(1, int(default RPS))).
	RouterBurst int `mapstructure:"router_burst"`
	// RouterVenueRPS optional per-venue RPS overrides (e.g. OANDA: 8).
	RouterVenueRPS map[string]float64 `mapstructure:"router_venue_rps"`
	// RouterFallbackVenues: after the intent's primary venue, try these in order until PlaceOrder succeeds (same intent/symbol on each — only list compatible venues).
	RouterFallbackVenues []string `mapstructure:"router_fallback_venues"`
}

type StorageConfig struct {
	Driver      string `mapstructure:"driver"` // sqlite, postgres
	DSN         string `mapstructure:"dsn"`
	EncryptKeys bool   `mapstructure:"encrypt_keys"`
}

type TelegramConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	BotToken       string        `mapstructure:"bot_token"`
	AllowedUserIDs []int64       `mapstructure:"allowed_user_ids"`
	ReportChatIDs  []int64       `mapstructure:"report_chat_ids"` // hourly summary recipients (private chat id with bot)
	ReportInterval time.Duration `mapstructure:"report_interval"` // default 1h when report_chat_ids set; 0 = use 1h
}

type ExchangeCfg struct {
	Name          string            `mapstructure:"name"`
	Enabled       bool              `mapstructure:"enabled"`
	APIKeyEnc     string            `mapstructure:"api_key_enc"`
	APISecretEnc  string            `mapstructure:"api_secret_enc"`
	PassphraseEnc string            `mapstructure:"passphrase_enc"`
	Testnet       bool              `mapstructure:"testnet"`
	Extra         map[string]string `mapstructure:"extra"` // account_id (OANDA), base_url (OKX), sidecar_url (CCXT), etc.
}

type StrategyCfg struct {
	ID       string         `mapstructure:"id"`
	Type     string         `mapstructure:"type"`
	Enabled  bool           `mapstructure:"enabled"`
	Weight   float64        `mapstructure:"weight"`
	Params   map[string]any `mapstructure:"params"`
	Universe []string       `mapstructure:"universe"`
}

type BacktestConfig struct {
	DataDir          string  `mapstructure:"data_dir"`
	Instrument       string  `mapstructure:"instrument"` // optional e.g. OANDA:EUR_USD or EUR_USD; default legacy BTCUSDT
	InitialEquity    float64 `mapstructure:"initial_equity"`
	FeeBps           float64 `mapstructure:"fee_bps"`
	SlippageBps      float64 `mapstructure:"slippage_bps"`
	WalkForwardTrain int     `mapstructure:"walk_forward_train_bars"`
	WalkForwardTest  int     `mapstructure:"walk_forward_test_bars"`
	WalkForwardStep  int     `mapstructure:"walk_forward_step_bars"`
}

type MLConfig struct {
	ONNXModelPath     string  `mapstructure:"onnx_model_path"`
	DriftZThreshold   float64 `mapstructure:"drift_z_threshold"`
	EWMAHalfLifeBars  int     `mapstructure:"ewma_half_life_bars"`
	EnableRemoteInfer string  `mapstructure:"enable_remote_infer_url"` // optional HTTP hook
}

// Load reads YAML configuration from path.
func Load(path string) (*Root, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("TRADING")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	_ = v.BindEnv("telegram.bot_token", "TRADING_TELEGRAM_BOT_TOKEN")
	_ = v.BindEnv("telegram.enabled", "TRADING_TELEGRAM_ENABLED")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var c Root
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	applyTelegramEnvOverrides(&c)
	c.StartupHints = mergeTelegramBotToken(&c, path)

	mergeStrategyRiskFlags(&c)

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyTelegramEnvOverrides sets telegram options from env when set (token is resolved in mergeTelegramBotToken).
func applyTelegramEnvOverrides(c *Root) {
	if s := strings.TrimSpace(os.Getenv("TRADING_TELEGRAM_ENABLED")); s != "" {
		c.Telegram.Enabled = envBool(s)
	}
	if s := strings.TrimSpace(os.Getenv("TRADING_TELEGRAM_ALLOWED_USER_IDS")); s != "" {
		var ids []int64
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			id, err := strconv.ParseInt(p, 10, 64)
			if err != nil {
				continue
			}
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			c.Telegram.AllowedUserIDs = ids
		}
	}
	if s := strings.TrimSpace(os.Getenv("TRADING_TELEGRAM_REPORT_CHAT_IDS")); s != "" {
		var ids []int64
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			id, err := strconv.ParseInt(p, 10, 64)
			if err != nil {
				continue
			}
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			c.Telegram.ReportChatIDs = ids
		}
	}
}

func envBool(s string) bool {
	ls := strings.ToLower(s)
	return ls == "1" || ls == "true" || ls == "yes" || ls == "on"
}

// mergeTelegramBotToken fills BotToken from env aliases, token file paths, or .env (tolerant parse).
// Returns hints for logging when the token is still missing.
func mergeTelegramBotToken(c *Root, configPath string) []string {
	var hints []string
	if strings.TrimSpace(c.Telegram.BotToken) != "" {
		return nil
	}
	if tok := strings.TrimSpace(os.Getenv("TRADING_TELEGRAM_BOT_TOKEN")); tok != "" {
		c.Telegram.BotToken = tok
		return []string{"token from TRADING_TELEGRAM_BOT_TOKEN"}
	}
	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "BOT_TOKEN", "TRADING_BOT_TOKEN"} {
		if tok := strings.TrimSpace(os.Getenv(key)); tok != "" {
			c.Telegram.BotToken = tok
			return []string{"token from environment variable " + key}
		}
	}
	for _, key := range []string{"TRADING_TELEGRAM_TOKEN_FILE", "TELEGRAM_TOKEN_FILE"} {
		p := strings.TrimSpace(os.Getenv(key))
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			hints = append(hints, fmt.Sprintf("%s=%q: %v", key, p, err))
			continue
		}
		if tok := strings.TrimSpace(string(b)); tok != "" {
			c.Telegram.BotToken = tok
			return append(hints, fmt.Sprintf("token from file %q (env %s)", p, key))
		}
		hints = append(hints, fmt.Sprintf("%s=%q is empty", key, p))
	}

	envKeys := []string{"TRADING_TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN", "BOT_TOKEN"}
	var paths []string
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, ".env"))
	}
	if abs, err := filepath.Abs(configPath); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(abs), ".env"))
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			hints = append(hints, fmt.Sprintf("missing: %s", p))
			continue
		}
		if st.Size() == 0 {
			hints = append(hints, fmt.Sprintf("empty file: %s", p))
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			hints = append(hints, fmt.Sprintf("read %s: %v", p, err))
			continue
		}
		b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
		// Parse (not Read): strict mode fails the whole file on one bad line.
		env := gotenv.Parse(bytes.NewReader(b))
		if len(env) == 0 {
			hints = append(hints, fmt.Sprintf("%s: no variables parsed (check format: NAME=value per line)", p))
			continue
		}
		for _, ek := range envKeys {
			if tok := strings.TrimSpace(env[ek]); tok != "" {
				c.Telegram.BotToken = tok
				return append(hints, fmt.Sprintf("token from %s key %s", p, ek))
			}
		}
		var similar []string
		for k := range env {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "telegram") || (strings.Contains(lk, "bot") && strings.Contains(lk, "token")) {
				similar = append(similar, k)
			}
		}
		hints = append(hints, fmt.Sprintf("%s: have %d keys, no TRADING_TELEGRAM_BOT_TOKEN; similar key names: %v", p, len(env), similar))
	}
	return hints
}

// mergeStrategyRiskFlags merges strategy-level knobs into risk.* (OR semantics so YAML risk.* stays authoritative when already true).
func mergeStrategyRiskFlags(c *Root) {
	for _, s := range c.Strategies {
		if !s.Enabled {
			continue
		}
		if strings.ToLower(strings.TrimSpace(s.Type)) != "ema_cross_atr" {
			continue
		}
		if paramBoolOR(s.Params, "exit_on_opposite_signal", "exit_on_opposite") {
			c.Risk.ExitOnOppositeSignal = true
		}
	}
}

func paramBoolOR(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if paramBool(m, k) {
			return true
		}
	}
	return false
}

func paramBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		ls := strings.ToLower(strings.TrimSpace(b))
		return ls == "true" || ls == "1" || ls == "yes" || ls == "on"
	case int:
		return b != 0
	case int64:
		return b != 0
	case float64:
		return b != 0
	default:
		return false
	}
}

func (c *Root) validate() error {
	if c.Risk.MaxRiskPerTradePct <= 0 || c.Risk.MaxRiskPerTradePct > 0.5 {
		return fmt.Errorf("risk.max_risk_per_trade_pct must be in (0, 0.5] (capital rule: max 0.5%%)")
	}
	if c.Risk.DailyDrawdownHardPct <= 0 {
		return fmt.Errorf("daily_drawdown_hard_pct must be positive")
	}
	if c.Risk.MaxNotionalPerTradePct < 0 || c.Risk.MaxNotionalPerTradePct > 100 {
		return fmt.Errorf("risk.max_notional_per_trade_pct must be in [0, 100] (0 disables cap)")
	}
	if c.Risk.MaxPositionHold < 0 {
		return fmt.Errorf("risk.max_position_hold must be >= 0")
	}
	m := types.Mode(strings.ToLower(c.Runtime.Mode))
	if m != types.ModePaper && m != types.ModeLive && m != types.ModeBacktest {
		return fmt.Errorf("runtime.mode must be paper, live, or backtest")
	}
	if c.Runtime.ReconcileDriftMinUnits < 0 {
		return fmt.Errorf("runtime.reconcile_drift_min_units must be >= 0")
	}
	return nil
}

// Mode returns typed runtime mode.
func (c *Root) Mode() types.Mode {
	return types.Mode(strings.ToLower(c.Runtime.Mode))
}
