package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"argus/internal/config"
	"argus/internal/i18n"
	"argus/internal/monitor"
)

// Notifier dispatches status change alerts to configured channels with cooldown control.
type Notifier struct {
	cfg             config.AlertsConfig
	httpClient      *http.Client
	telegramBaseURL string
	mu              sync.Mutex
	lastSent        map[string]time.Time
	downSince       map[string]time.Time
}

// New creates a Notifier from AlertsConfig.
func New(cfg config.AlertsConfig) *Notifier {
	return &Notifier{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		telegramBaseURL: "https://api.telegram.org",
		lastSent:        make(map[string]time.Time),
		downSince:       make(map[string]time.Time),
	}
}

// UpdateConfig replaces the alerts configuration at runtime.
func (n *Notifier) UpdateConfig(cfg config.AlertsConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cfg = cfg
}

// Enabled returns true if at least one notification channel is configured.
func (n *Notifier) Enabled() bool {
	n.mu.Lock()
	cfg := n.cfg
	n.mu.Unlock()
	return alertsEnabled(cfg)
}

func alertsEnabled(cfg config.AlertsConfig) bool {
	return strings.TrimSpace(cfg.Webhook.URL) != "" ||
		strings.TrimSpace(cfg.Slack.WebhookURL) != "" ||
		strings.TrimSpace(cfg.Discord.WebhookURL) != "" ||
		(strings.TrimSpace(cfg.Telegram.BotToken) != "" && strings.TrimSpace(cfg.Telegram.ChatID) != "") ||
		(strings.TrimSpace(cfg.Email.SMTPHost) != "" && len(cfg.Email.To) > 0)
}

// HandleEvent evaluates a status transition and dispatches notifications asynchronously.
func (n *Notifier) HandleEvent(e monitor.ChangeEvent) {
	if e.InMaintenance {
		return
	}

	key := e.SystemID + ":" + e.Kind
	now := time.Now()

	var shouldSend bool
	var mttr time.Duration

	n.mu.Lock()
	cfg := n.cfg
	if !alertsEnabled(cfg) {
		n.mu.Unlock()
		return
	}
	switch e.ToStatus {
	case monitor.StatusDown:
		if _, exists := n.downSince[key]; !exists {
			if !e.OccurredAt.IsZero() {
				n.downSince[key] = e.OccurredAt
			} else {
				n.downSince[key] = now
			}
		}
		if last, ok := n.lastSent[key]; !ok || cfg.Cooldown <= 0 || now.Sub(last) >= cfg.Cooldown {
			n.lastSent[key] = now
			shouldSend = true
		}

	case monitor.StatusDegraded:
		if cfg.NotifyOnDegraded {
			if last, ok := n.lastSent[key]; !ok || cfg.Cooldown <= 0 || now.Sub(last) >= cfg.Cooldown {
				n.lastSent[key] = now
				shouldSend = true
			}
		}

	case monitor.StatusUP:
		if start, ok := n.downSince[key]; ok {
			if !e.OccurredAt.IsZero() && e.OccurredAt.After(start) {
				mttr = e.OccurredAt.Sub(start)
			} else {
				mttr = now.Sub(start)
			}
			delete(n.downSince, key)
			shouldSend = true
		} else if cfg.NotifyOnDegraded && e.FromStatus == monitor.StatusDegraded {
			shouldSend = true
		}
	}
	n.mu.Unlock()

	if !shouldSend {
		return
	}

	go n.dispatch(context.Background(), cfg, e, mttr)
}

// ErrNoChannelsConfigured is returned when testing alerts without any configured channel.
var ErrNoChannelsConfigured = errors.New("nenhum canal de notificação configurado")

// SendTest dispatches a synthetic test alert synchronously to all configured channels.
func (n *Notifier) SendTest(ctx context.Context) (int, error) {
	n.mu.Lock()
	cfg := n.cfg
	n.mu.Unlock()

	if !alertsEnabled(cfg) {
		return 0, ErrNoChannelsConfigured
	}

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	ev := monitor.ChangeEvent{
		SystemID:   "argus-test",
		SystemName: "Argus NOC",
		Kind:       i18n.Pick("teste", "test"),
		FromStatus: monitor.StatusUnknown,
		ToStatus:   monitor.StatusUP,
		Diagnosis:  i18n.Pick("Teste manual de notificação", "Manual notification test"),
		Detail:     i18n.Pick("Se você recebeu esta mensagem, o canal de alertas está operacional.", "If you received this message, the alert channel is operational."),
		OccurredAt: time.Now(),
	}

	title := i18n.Pick("[TESTE] Argus — Teste de Notificação", "[TEST] Argus — Notification Test")
	body := i18n.Pick(
		"Diagnóstico: Teste manual de notificação\nDetalhe: Se você recebeu esta mensagem, o canal de alertas está operacional.",
		"Diagnosis: Manual notification test\nDetail: If you received this message, the alert channel is operational.",
	)
	return n.sendToChannels(ctx, cfg, ev, 0, title, body)
}

func (n *Notifier) dispatch(ctx context.Context, cfg config.AlertsConfig, e monitor.ChangeEvent, mttr time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	title, body := formatAlertMessage(e, mttr)
	_, _ = n.sendToChannels(ctx, cfg, e, mttr, title, body)
}

func (n *Notifier) sendToChannels(ctx context.Context, cfg config.AlertsConfig, e monitor.ChangeEvent, mttr time.Duration, title, body string) (int, error) {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		sent int
		errs []error
	)

	recordResult := func(channel string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			log.Printf("[WARN] notify %s failed: %v", channel, err)
			errs = append(errs, fmt.Errorf("%s: %w", channel, err))
		} else {
			sent++
		}
	}

	if url := strings.TrimSpace(cfg.Webhook.URL); url != "" {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			payload := map[string]any{
				"system_id":    e.SystemID,
				"system_name":  e.SystemName,
				"kind":         e.Kind,
				"from_status":  string(e.FromStatus),
				"to_status":    string(e.ToStatus),
				"diagnosis":    e.Diagnosis,
				"detail":       e.Detail,
				"mttr_seconds": int64(mttr.Seconds()),
				"occurred_at":  e.OccurredAt,
				"title":        title,
				"message":      body,
			}
			recordResult("webhook", n.postJSON(ctx, u, payload, cfg.Webhook.Headers))
		}(url)
	}

	if url := strings.TrimSpace(cfg.Slack.WebhookURL); url != "" {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			payload := map[string]string{
				"text": fmt.Sprintf("*%s*\n%s", title, body),
			}
			recordResult("slack", n.postJSON(ctx, u, payload, nil))
		}(url)
	}

	if url := strings.TrimSpace(cfg.Discord.WebhookURL); url != "" {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			payload := map[string]string{
				"content": fmt.Sprintf("**%s**\n%s", title, body),
			}
			recordResult("discord", n.postJSON(ctx, u, payload, nil))
		}(url)
	}

	if token := strings.TrimSpace(cfg.Telegram.BotToken); token != "" && strings.TrimSpace(cfg.Telegram.ChatID) != "" {
		baseURL := strings.TrimRight(n.telegramBaseURL, "/")
		if baseURL == "" {
			baseURL = "https://api.telegram.org"
		}
		tgURL := fmt.Sprintf("%s/bot%s/sendMessage", baseURL, token)
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			payload := map[string]string{
				"chat_id":    cfg.Telegram.ChatID,
				"text":       fmt.Sprintf("<b>%s</b>\n%s", html.EscapeString(title), html.EscapeString(body)),
				"parse_mode": "HTML",
			}
			recordResult("telegram", n.postJSON(ctx, u, payload, nil))
		}(tgURL)
	}

	if strings.TrimSpace(cfg.Email.SMTPHost) != "" && len(cfg.Email.To) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordResult("email", n.sendEmail(ctx, cfg.Email, title, body))
		}()
	}

	wg.Wait()
	return sent, errors.Join(errs...)
}

func (n *Notifier) postJSON(ctx context.Context, url string, payload any, headers map[string]string) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(v))
}

func (n *Notifier) sendEmail(ctx context.Context, emailCfg config.EmailAlertConfig, subject, body string) error {
	port := emailCfg.SMTPPort
	if port <= 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", emailCfg.SMTPHost, port)
	from := sanitizeHeader(emailCfg.From)
	if from == "" {
		from = sanitizeHeader(emailCfg.Username)
	}
	cleanTo := make([]string, 0, len(emailCfg.To))
	for _, rcpt := range emailCfg.To {
		if clean := sanitizeHeader(rcpt); clean != "" {
			cleanTo = append(cleanTo, clean)
		}
	}
	encodedSubject := mime.QEncoding.Encode("utf-8", sanitizeHeader(subject))
	now := time.Now().UTC()
	msgID := fmt.Sprintf("<%d@argus.local>", now.UnixNano())
	msg := fmt.Sprintf("Date: %s\r\nMessage-ID: %s\r\nFrom: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		now.Format(time.RFC1123Z), msgID, from, strings.Join(cleanTo, ", "), encodedSubject, body)

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	c, err := smtp.NewClient(conn, emailCfg.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: emailCfg.SMTPHost}); err != nil {
			return err
		}
	}
	if emailCfg.Username != "" {
		auth := smtp.PlainAuth("", emailCfg.Username, emailCfg.Password, emailCfg.SMTPHost)
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range cleanTo {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write([]byte(msg)); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func formatAlertMessage(e monitor.ChangeEvent, mttr time.Duration) (string, string) {
	var badge string
	switch e.ToStatus {
	case monitor.StatusDown:
		badge = i18n.Pick("[FORA DO AR]", "[DOWN]")
	case monitor.StatusDegraded:
		badge = i18n.Pick("[INSTÁVEL]", "[DEGRADED]")
	case monitor.StatusUP:
		badge = i18n.Pick("[RECUPERADO]", "[RECOVERED]")
	default:
		badge = fmt.Sprintf("[%s]", e.ToStatus)
	}

	title := fmt.Sprintf("%s %s (%s)", badge, e.SystemName, strings.ToUpper(e.Kind))
	var lines []string
	if e.Diagnosis != "" {
		lines = append(lines, i18n.Pick("Diagnóstico: ", "Diagnosis: ")+e.Diagnosis)
	}
	if e.Detail != "" {
		lines = append(lines, i18n.Pick("Detalhe: ", "Detail: ")+e.Detail)
	}
	if e.ToStatus == monitor.StatusUP && mttr > 0 {
		lines = append(lines, i18n.Pick("Duração da queda (MTTR): ", "Downtime duration (MTTR): ")+mttr.Truncate(time.Second).String())
	}
	return title, strings.Join(lines, "\n")
}
