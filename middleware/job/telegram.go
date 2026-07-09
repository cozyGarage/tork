package job

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/runabol/tork"
	"github.com/runabol/tork/datastore"
)

// ponytail: telegramAPIBase is overridable in tests only.
var telegramAPIBase = "https://api.telegram.org/bot%s/sendMessage"

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// TelegramConfig configures global job-failure alerts.
type TelegramConfig struct {
	Enabled  bool
	Token    string
	ChatID   string
	OnStates []string
	LogLines int
}

type taskLogStore interface {
	GetTaskLogParts(ctx context.Context, taskID, q string, page, size int) (*datastore.Page[*tork.TaskLogPart], error)
}

// Telegram sends a Telegram message when a job enters a configured state (default FAILED).
func Telegram(ds taskLogStore, cfg TelegramConfig) MiddlewareFunc {
	if cfg.LogLines <= 0 {
		cfg.LogLines = 10
	}
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, et EventType, j *tork.Job) error {
			if err := next(ctx, et, j); err != nil {
				return err
			}
			if !cfg.Enabled || cfg.Token == "" || cfg.ChatID == "" {
				return nil
			}
			if et != StateChange {
				return nil
			}
			if !slices.Contains(cfg.OnStates, string(j.State)) {
				return nil
			}
			ft := failedTask(j)
			if ft == nil {
				return nil
			}
			job := j
			task := ft
			go func() {
				sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := sendTelegramAlert(sendCtx, ds, cfg, job, task); err != nil {
					log.Info().Err(err).Msg("[Telegram] error sending job alert")
				}
			}()
			return nil
		}
	}
}

func failedTask(j *tork.Job) *tork.Task {
	for i := len(j.Execution) - 1; i >= 0; i-- {
		if j.Execution[i].State == tork.TaskStateFailed {
			return j.Execution[i]
		}
	}
	for i := len(j.Execution) - 1; i >= 0; i-- {
		if j.Execution[i].Error != "" {
			return j.Execution[i]
		}
	}
	return nil
}

func sendTelegramAlert(ctx context.Context, ds taskLogStore, cfg TelegramConfig, j *tork.Job, ft *tork.Task) error {
	text := buildTelegramMessage(ctx, ds, cfg, j, ft)
	url := fmt.Sprintf(telegramAPIBase, cfg.Token)
	body, err := json.Marshal(map[string]string{
		"chat_id":    cfg.ChatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func buildTelegramMessage(ctx context.Context, ds taskLogStore, cfg TelegramConfig, j *tork.Job, ft *tork.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚠️ Job failed: <b>%s</b>\n", escHTML(j.Name))
	fmt.Fprintf(&b, "id: %s\n", escHTML(j.ID))
	fmt.Fprintf(&b, "task: %s\n", escHTML(ft.Name))
	if ft.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", escHTML(ft.Error))
	}
	if tail := logTail(ctx, ds, ft.ID, cfg.LogLines); tail != "" {
		b.WriteString("\n--- log tail ---\n")
		b.WriteString(escHTML(tail))
	}
	text := b.String()
	if len(text) > 4096 {
		text = text[:4093] + "..."
	}
	return text
}

func logTail(ctx context.Context, ds taskLogStore, taskID string, maxLines int) string {
	if taskID == "" || maxLines <= 0 {
		return ""
	}
	page, err := ds.GetTaskLogParts(ctx, taskID, "", 1, maxLines)
	if err != nil || page == nil || len(page.Items) == 0 {
		return ""
	}
	var lines []string
	for i := len(page.Items) - 1; i >= 0; i-- {
		for _, line := range strings.Split(page.Items[i].Contents, "\n") {
			line = strings.TrimRight(stripANSI(line), "\r")
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func escHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
