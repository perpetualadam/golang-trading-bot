package notify

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"
)

// Controller is implemented by the main runner for Telegram commands.
type Controller interface {
	StartTrading(ctx context.Context) error
	StopTrading(ctx context.Context) error
	PauseNewEntries()
	ResumeNewEntries()
	StatusText(ctx context.Context) string
	BalancesText(ctx context.Context) string
	DailyText(ctx context.Context) string
	ReportText(ctx context.Context) string
	PerformanceText(ctx context.Context) string
	RequestNuke()
	ConfirmNuke(ctx context.Context) error
	IsNukePending() bool
}

// TelegramBot wraps telegram-bot-api with auth and commands.
type TelegramBot struct {
	log   zerolog.Logger
	bot   *tgbotapi.BotAPI
	ctl   Controller
	allow map[int64]struct{}
	mu    sync.Mutex
}

func NewTelegramBot(token string, allowed []int64, ctl Controller, log zerolog.Logger) (*TelegramBot, error) {
	b, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	// Long polling (GetUpdates) returns nothing while a webhook is set — clear it if present.
	wh, err := b.GetWebhookInfo()
	if err != nil {
		log.Warn().Err(err).Msg("telegram getWebhookInfo")
	} else if wh.IsSet() {
		log.Warn().Str("url", wh.URL).Msg("telegram had a webhook set; deleting so long polling works")
		if _, err := b.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: true}); err != nil {
			return nil, fmt.Errorf("deleteWebhook: %w", err)
		}
	}
	log.Info().Str("username", b.Self.UserName).Int64("id", b.Self.ID).Msg("telegram bot connected")
	allow := make(map[int64]struct{})
	for _, id := range allowed {
		allow[id] = struct{}{}
	}
	return &TelegramBot{bot: b, ctl: ctl, allow: allow, log: log}, nil
}

func (t *TelegramBot) authorized(id int64) bool {
	if len(t.allow) == 0 {
		return true
	}
	_, ok := t.allow[id]
	return ok
}

// Run blocks on updates until ctx done.
func (t *TelegramBot) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			t.log.Error().Interface("recover", r).Msg("telegram Run panic")
		}
	}()
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	ch := t.bot.GetUpdatesChan(u)
	t.log.Info().Msg("telegram long polling started (send /status in a private chat with the bot)")
	for {
		select {
		case <-ctx.Done():
			t.bot.StopReceivingUpdates()
			return
		case upd, ok := <-ch:
			if !ok {
				t.log.Info().Msg("telegram updates channel closed")
				return
			}
			if upd.Message == nil {
				continue
			}
			if upd.Message.From == nil {
				continue
			}
			if !t.authorized(upd.Message.From.ID) {
				t.log.Debug().Int64("user_id", upd.Message.From.ID).Msg("telegram: ignored (not in allowed_user_ids)")
				continue
			}
			t.handle(ctx, upd.Message)
		}
	}
}

// telegramBaseCommand turns "/status@MyBot" into "/status" so switches match client behavior.
func telegramBaseCommand(firstToken string) string {
	s := strings.ToLower(strings.TrimSpace(firstToken))
	if i := strings.IndexByte(s, '@'); i > 0 {
		s = s[:i]
	}
	return s
}

func (t *TelegramBot) handle(ctx context.Context, m *tgbotapi.Message) {
	text := strings.TrimSpace(m.Text)
	if !strings.HasPrefix(text, "/") {
		return
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := telegramBaseCommand(parts[0])
	var reply string
	switch cmd {
	case "/start":
		if err := t.ctl.StartTrading(ctx); err != nil {
			reply = "start error: " + err.Error()
		} else {
			reply = "Trading loop armed (capital-first guards active)."
		}
	case "/stop":
		if err := t.ctl.StopTrading(ctx); err != nil {
			reply = "stop error: " + err.Error()
		} else {
			reply = "Trading loop stopped."
		}
	case "/pause":
		t.ctl.PauseNewEntries()
		reply = "Paused: no new entries; managing existing positions continues."
	case "/resume":
		t.ctl.ResumeNewEntries()
		reply = "Resumed: new entries allowed if risk checks pass."
	case "/status":
		reply = t.ctl.StatusText(ctx)
	case "/balance", "/balances":
		reply = t.ctl.BalancesText(ctx)
	case "/daily":
		reply = t.ctl.DailyText(ctx)
	case "/report":
		reply = t.ctl.ReportText(ctx)
	case "/performance":
		reply = t.ctl.PerformanceText(ctx)
	case "/nuke":
		t.ctl.RequestNuke()
		reply = "NUKE requested. Send /confirm nuke within 60s to flatten all and halt."
	case "/confirm":
		if len(parts) >= 2 && strings.EqualFold(parts[1], "nuke") {
			if !t.ctl.IsNukePending() {
				reply = "No pending nuke confirmation."
				break
			}
			if err := t.ctl.ConfirmNuke(ctx); err != nil {
				reply = "nuke error: " + err.Error()
			} else {
				reply = "NUKE executed: positions flattened (best effort), bot halted."
			}
		} else {
			reply = "Usage: /confirm nuke"
		}
	default:
		t.log.Debug().Str("cmd", cmd).Msg("telegram: unknown command")
		reply = "Unknown command. Try /status, /performance, /report, /balance, /daily, /pause, /resume, /stop."
	}
	t.sendTextChunks(m.Chat.ID, reply)
}

// RunPeriodicReports sends ReportText to each chatID every `interval` until ctx is cancelled.
func (t *TelegramBot) RunPeriodicReports(ctx context.Context, interval time.Duration, chatIDs []int64) {
	if len(chatIDs) == 0 || interval <= 0 {
		return
	}
	t.log.Info().Dur("interval", interval).Int("chats", len(chatIDs)).Msg("telegram periodic reports started")
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.sendScheduledReport(chatIDs)
		}
	}
}

func (t *TelegramBot) sendScheduledReport(chatIDs []int64) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	text := t.ctl.ReportText(reqCtx)
	for _, id := range chatIDs {
		t.sendTextChunks(id, text)
	}
}

func (t *TelegramBot) sendTextChunks(chatID int64, s string) {
	for _, chunk := range splitTelegramUTF16Safe(s, 3800) {
		msg := tgbotapi.NewMessage(chatID, chunk)
		if _, err := t.bot.Send(msg); err != nil {
			t.log.Warn().Err(err).Int64("chat_id", chatID).Msg("telegram send failed")
		}
	}
}

// splitTelegramUTF16Safe splits so each part stays under Telegram's ~4096 *character* limit (they count UTF-16 code units).
func splitTelegramUTF16Safe(s string, maxUTF16 int) []string {
	if s == "" {
		return nil
	}
	if maxUTF16 < 256 {
		maxUTF16 = 256
	}
	var out []string
	for len(s) > 0 {
		nRunes := 0
		u16 := 0
		i := 0
		for i < len(s) {
			r, w := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && w == 1 {
				i += w
				continue
			}
			ru := utf16Len(r)
			if u16+ru > maxUTF16 && nRunes > 0 {
				break
			}
			u16 += ru
			nRunes++
			i += w
		}
		if nRunes == 0 {
			if i == 0 {
				break
			}
			// e.g. trailing invalid UTF-8 skipped without forming a rune
		}
		out = append(out, s[:i])
		s = s[i:]
	}
	return out
}

func utf16Len(r rune) int {
	if r <= 0xffff {
		return 1
	}
	return 2
}

// SendText sends a plain message to a chat (for alerts).
func (t *TelegramBot) SendText(chatID int64, s string) {
	t.sendTextChunks(chatID, s)
}

// SendPhotoBytes sends PNG.
func (t *TelegramBot) SendPhotoBytes(chatID int64, caption string, png []byte) {
	ph := tgbotapi.FileBytes{Name: "chart.png", Bytes: png}
	doc := tgbotapi.NewPhoto(chatID, ph)
	doc.Caption = caption
	_, _ = t.bot.Send(doc)
}

// FormatRiskAlert builds a concise risk message.
func FormatRiskAlert(kind string, detail float64) string {
	return fmt.Sprintf("RISK [%s]: %.4f", kind, detail)
}
