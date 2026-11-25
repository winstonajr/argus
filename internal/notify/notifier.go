package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
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

type systemMeta struct {
	group string
	tags  []string
}

// Notifier dispatches status change alerts to configured channels with cooldown control.
type Notifier struct {
	cfg             config.AlertsConfig
	httpClient      *http.Client
	telegramBaseURL string
	mu              sync.Mutex
	wg              sync.WaitGroup
	lastSent        map[string]time.Time
	downSince       map[string]time.Time
	downEvents      map[string]monitor.ChangeEvent
	systems         map[string]systemMeta
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
		downEvents:      make(map[string]monitor.ChangeEvent),
		systems:         make(map[string]systemMeta),
	}
}

// SetSystems updates system metadata used for alert routing (groups and tags).
func (n *Notifier) SetSystems(systems []config.SystemConfig) {
	meta := make(map[string]systemMeta, len(systems))
	for _, s := range systems {
		meta[s.ID] = systemMeta{
			group: strings.TrimSpace(s.Group),
			tags:  s.Tags,
		}
	}
	n.mu.Lock()
	n.systems = meta
	n.mu.Unlock()
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
	if strings.TrimSpace(cfg.Webhook.URL) != "" ||
		strings.TrimSpace(cfg.Slack.WebhookURL) != "" ||
		strings.TrimSpace(cfg.Discord.WebhookURL) != "" ||
		strings.TrimSpace(cfg.Teams.WebhookURL) != "" ||
		(strings.TrimSpace(cfg.Ntfy.URL) != "" && strings.TrimSpace(cfg.Ntfy.Topic) != "") ||
		(strings.TrimSpace(cfg.WhatsApp.URL) != "" && strings.TrimSpace(cfg.WhatsApp.Target) != "") ||
		(strings.TrimSpace(cfg.Telegram.BotToken) != "" && strings.TrimSpace(cfg.Telegram.ChatID) != "") ||
		(strings.TrimSpace(cfg.Email.SMTPHost) != "" && len(cfg.Email.To) > 0) {
		return true
	}
	for _, r := range cfg.Routes {
		if strings.TrimSpace(r.Webhook.URL) != "" ||
			strings.TrimSpace(r.Slack.WebhookURL) != "" ||
			strings.TrimSpace(r.Discord.WebhookURL) != "" ||
			strings.TrimSpace(r.Teams.WebhookURL) != "" ||
			(strings.TrimSpace(r.Ntfy.URL) != "" && strings.TrimSpace(r.Ntfy.Topic) != "") ||
			(strings.TrimSpace(r.WhatsApp.URL) != "" && strings.TrimSpace(r.WhatsApp.Target) != "") ||
			(strings.TrimSpace(r.Telegram.BotToken) != "" && strings.TrimSpace(r.Telegram.ChatID) != "") ||
			strings.TrimSpace(r.Telegram.ChatID) != "" ||
			len(r.Email.To) > 0 {
			return true
		}
	}
	return false
}

// HandleEvent evaluates a status transition and dispatches notifications asynchronously.
func (n *Notifier) HandleEvent(e monitor.ChangeEvent) {
	key := e.SystemID + ":" + e.Kind
	if e.InMaintenance || e.SuppressedBy != "" {
		if e.ToStatus == monitor.StatusUP {
			n.mu.Lock()
			delete(n.downSince, key)
			delete(n.downEvents, key)
			n.mu.Unlock()
		}
		return
	}

	now := time.Now()

	var shouldSend bool
	var mttr time.Duration

	n.mu.Lock()
	cfg := resolveAlertConfig(n.cfg, n.systems[e.SystemID], e.SystemID)
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
		n.downEvents[key] = e
		cdKey := key + ":DOWN"
		if last, ok := n.lastSent[cdKey]; !ok || cfg.Cooldown <= 0 || now.Sub(last) >= cfg.Cooldown {
			n.lastSent[cdKey] = now
			shouldSend = true
		}

	case monitor.StatusDegraded:
		if cfg.NotifyOnDegraded {
			cdKey := key + ":DEGRADED"
			if last, ok := n.lastSent[cdKey]; !ok || cfg.Cooldown <= 0 || now.Sub(last) >= cfg.Cooldown {
				n.lastSent[cdKey] = now
				shouldSend = true
			}
		}

	case monitor.StatusUP:
		delete(n.downEvents, key)
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

	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.dispatch(context.Background(), cfg, e, mttr)
	}()
}

// Close waits for in-flight notifications to finish until ctx expires.
func (n *Notifier) Close(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
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
		Kind:       "dns",
		URL:        "https://argus.local",
		IP:         "127.0.0.1",
		FromStatus: monitor.StatusUnknown,
		ToStatus:   monitor.StatusUP,
		Diagnosis:  i18n.Pick("Teste manual de notificação", "Manual notification test"),
		Detail:     i18n.Pick("Se você recebeu esta mensagem, o canal de alertas está operacional.", "If you received this message, the alert channel is operational."),
		StatusCode: 200,
		Latency:    12 * time.Millisecond,
		OccurredAt: time.Now(),
	}

	ac := buildAlertContext(ev, 0, cfg.PublicURL, true)
	return n.sendToChannels(ctx, cfg, ev, 0, ac)
}

func (n *Notifier) dispatch(ctx context.Context, cfg config.AlertsConfig, e monitor.ChangeEvent, mttr time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	ac := buildAlertContext(e, mttr, cfg.PublicURL, false)
	_, _ = n.sendToChannels(ctx, cfg, e, mttr, ac)
}

func (n *Notifier) sendToChannels(ctx context.Context, cfg config.AlertsConfig, e monitor.ChangeEvent, mttr time.Duration, ac alertContext) (int, error) {
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

	title := ac.PlainTitle
	body := ac.PlainBody

	if url := strings.TrimSpace(cfg.Webhook.URL); url != "" {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			payload := map[string]any{
				"system_id":    e.SystemID,
				"system_name":  e.SystemName,
				"kind":         e.Kind,
				"url":          e.URL,
				"ip":           e.IP,
				"tcp_port":     e.TCPPort,
				"from_status":  string(e.FromStatus),
				"to_status":    string(e.ToStatus),
				"diagnosis":    e.Diagnosis,
				"detail":       e.Detail,
				"status_code":  e.StatusCode,
				"latency_ms":   e.Latency.Milliseconds(),
				"error":        e.Error,
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
			payload := buildSlackPayload(ac)
			recordResult("slack", n.postJSON(ctx, u, payload, nil))
		}(url)
	}

	if url := strings.TrimSpace(cfg.Discord.WebhookURL); url != "" {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			payload := buildDiscordPayload(ac, e.OccurredAt)
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
			payload := map[string]any{
				"chat_id":                  cfg.Telegram.ChatID,
				"text":                     formatTelegramHTML(ac),
				"parse_mode":               "HTML",
				"disable_web_page_preview": true,
			}
			recordResult("telegram", n.postJSON(ctx, u, payload, nil))
		}(tgURL)
	}

	if strings.TrimSpace(cfg.Email.SMTPHost) != "" && len(cfg.Email.To) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordResult("email", n.sendEmail(ctx, cfg.Email, ac.EmailSubject, body, formatEmailHTML(ac)))
		}()
	}

	if u := strings.TrimSpace(cfg.Teams.WebhookURL); u != "" {
		wg.Add(1)
		go func(webhookURL string) {
			defer wg.Done()
			recordResult("teams", n.postJSON(ctx, webhookURL, buildTeamsPayload(ac), nil))
		}(u)
	}

	if base := strings.TrimRight(strings.TrimSpace(cfg.Ntfy.URL), "/"); base != "" && strings.TrimSpace(cfg.Ntfy.Topic) != "" {
		ntfyURL := base + "/" + strings.TrimLeft(strings.TrimSpace(cfg.Ntfy.Topic), "/")
		wg.Add(1)
		go func(u string, token string) {
			defer wg.Done()
			recordResult("ntfy", n.sendNtfy(ctx, u, token, e, ac))
		}(ntfyURL, strings.TrimSpace(cfg.Ntfy.Token))
	}

	if u := strings.TrimSpace(cfg.WhatsApp.URL); u != "" && strings.TrimSpace(cfg.WhatsApp.Target) != "" {
		wg.Add(1)
		go func(waURL string, waCfg config.WhatsAppAlertConfig) {
			defer wg.Done()
			payload := map[string]any{
				"target":    waCfg.Target,
				"number":    waCfg.Target,
				"text":      ac.PlainTitle + "\n\n" + ac.PlainBody,
				"system_id": e.SystemID,
				"status":    string(e.ToStatus),
			}
			var headers map[string]string
			if tok := strings.TrimSpace(waCfg.Token); tok != "" {
				headers = map[string]string{"Authorization": "Bearer " + tok}
			}
			recordResult("whatsapp", n.postJSON(ctx, waURL, payload, headers))
		}(u, cfg.WhatsApp)
	}

	wg.Wait()
	return sent, errors.Join(errs...)
}

func resolveAlertConfig(base config.AlertsConfig, meta systemMeta, sysID string) config.AlertsConfig {
	if len(base.Routes) == 0 {
		return base
	}
	res := base
	for _, r := range base.Routes {
		if !routeMatches(r, meta, sysID) {
			continue
		}
		if strings.TrimSpace(r.Webhook.URL) != "" {
			res.Webhook = r.Webhook
		}
		if strings.TrimSpace(r.Slack.WebhookURL) != "" {
			res.Slack = r.Slack
		}
		if strings.TrimSpace(r.Discord.WebhookURL) != "" {
			res.Discord = r.Discord
		}
		if strings.TrimSpace(r.Teams.WebhookURL) != "" {
			res.Teams = r.Teams
		}
		if strings.TrimSpace(r.Telegram.BotToken) != "" {
			res.Telegram.BotToken = r.Telegram.BotToken
		}
		if strings.TrimSpace(r.Telegram.ChatID) != "" {
			res.Telegram.ChatID = r.Telegram.ChatID
		}
		if strings.TrimSpace(r.Email.SMTPHost) != "" {
			res.Email.SMTPHost = r.Email.SMTPHost
		}
		if r.Email.SMTPPort > 0 {
			res.Email.SMTPPort = r.Email.SMTPPort
		}
		if strings.TrimSpace(r.Email.Username) != "" {
			res.Email.Username = r.Email.Username
		}
		if strings.TrimSpace(r.Email.Password) != "" {
			res.Email.Password = r.Email.Password
		}
		if strings.TrimSpace(r.Email.From) != "" {
			res.Email.From = r.Email.From
		}
		if len(r.Email.To) > 0 {
			res.Email.To = r.Email.To
		}
		if strings.TrimSpace(r.Ntfy.URL) != "" {
			res.Ntfy.URL = r.Ntfy.URL
		}
		if strings.TrimSpace(r.Ntfy.Topic) != "" {
			res.Ntfy.Topic = r.Ntfy.Topic
		}
		if strings.TrimSpace(r.Ntfy.Token) != "" {
			res.Ntfy.Token = r.Ntfy.Token
		}
		if strings.TrimSpace(r.WhatsApp.URL) != "" {
			res.WhatsApp.URL = r.WhatsApp.URL
		}
		if strings.TrimSpace(r.WhatsApp.Token) != "" {
			res.WhatsApp.Token = r.WhatsApp.Token
		}
		if strings.TrimSpace(r.WhatsApp.Target) != "" {
			res.WhatsApp.Target = r.WhatsApp.Target
		}
	}
	return res
}

func routeMatches(r config.AlertRouteConfig, meta systemMeta, sysID string) bool {
	for _, id := range r.MatchSystems {
		if strings.EqualFold(strings.TrimSpace(id), sysID) {
			return true
		}
	}
	if meta.group != "" {
		for _, g := range r.MatchGroups {
			if strings.EqualFold(strings.TrimSpace(g), meta.group) {
				return true
			}
		}
	}
	if len(meta.tags) > 0 {
		for _, rt := range r.MatchTags {
			for _, st := range meta.tags {
				if strings.EqualFold(strings.TrimSpace(rt), strings.TrimSpace(st)) {
					return true
				}
			}
		}
	}
	return false
}

// StartEscalationLoop periodically re-sends reminders for prolonged DOWN systems when repeat_interval > 0.
func (n *Notifier) StartEscalationLoop(ctx context.Context, isEligible func(sysID string) bool) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n.checkEscalations(ctx, isEligible)
			}
		}
	}()
}

func (n *Notifier) checkEscalations(ctx context.Context, isEligible func(sysID string) bool) {
	now := time.Now()
	type pendingReminder struct {
		cfg   config.AlertsConfig
		event monitor.ChangeEvent
	}
	var toSend []pendingReminder

	n.mu.Lock()
	interval := n.cfg.RepeatInterval
	if interval <= 0 || len(n.downEvents) == 0 {
		n.mu.Unlock()
		return
	}
	for key, ev := range n.downEvents {
		if isEligible != nil && !isEligible(ev.SystemID) {
			continue
		}
		cdKey := key + ":DOWN"
		last, ok := n.lastSent[cdKey]
		if !ok || now.Sub(last) < interval {
			continue
		}
		cfg := resolveAlertConfig(n.cfg, n.systems[ev.SystemID], ev.SystemID)
		if !alertsEnabled(cfg) {
			continue
		}
		n.lastSent[cdKey] = now
		dur := now.Sub(n.downSince[key]).Truncate(time.Second)
		remEv := ev
		remEv.OccurredAt = now
		remEv.Diagnosis = i18n.Pick(
			fmt.Sprintf("Lembrete: fora do ar há %s (%s)", dur, ev.Diagnosis),
			fmt.Sprintf("Reminder: down for %s (%s)", dur, ev.Diagnosis),
		)
		toSend = append(toSend, pendingReminder{cfg: cfg, event: remEv})
	}
	n.mu.Unlock()

	for _, item := range toSend {
		n.dispatch(ctx, item.cfg, item.event, 0)
	}
}

// SendSLAReport dispatches a periodic SLA summary report to all configured global channels.
func (n *Notifier) SendSLAReport(ctx context.Context, title, summary string) error {
	n.mu.Lock()
	cfg := n.cfg
	n.mu.Unlock()
	if !alertsEnabled(cfg) {
		return ErrNoChannelsConfigured
	}
	ev := monitor.ChangeEvent{
		SystemID:   "argus-sla-report",
		SystemName: title,
		Kind:       "sla",
		ToStatus:   monitor.StatusUP,
		Diagnosis:  title,
		Detail:     summary,
		OccurredAt: time.Now(),
	}
	ac := buildAlertContext(ev, 0, cfg.PublicURL, false)
	ac.PlainTitle = "📊 " + title
	ac.EmailSubject = "📊 " + title
	ac.PlainBody = summary
	_, err := n.sendToChannels(ctx, cfg, ev, 0, ac)
	return err
}

// SendConnectivityNotification dispatches an internet outage or recovery notice to all configured global channels.
func (n *Notifier) SendConnectivityNotification(ctx context.Context, offline bool, since, until time.Time) error {
	n.mu.Lock()
	cfg := n.cfg
	n.mu.Unlock()
	if !alertsEnabled(cfg) {
		return ErrNoChannelsConfigured
	}

	var title, summary string
	var status monitor.Status
	if offline {
		status = monitor.StatusDegraded
		title = "Argus — Sem conexão com a internet"
		when := since.Local().Format("02/01/2006 15:04:05")
		summary = fmt.Sprintf("Monitoramento pausado às %s para evitar falsos alertas e poluição do histórico.", when)
	} else {
		status = monitor.StatusUP
		title = "Argus — Conexão com a internet restabelecida"
		dur := until.Sub(since).Truncate(time.Second)
		if dur < 0 {
			dur = 0
		}
		summary = fmt.Sprintf(
			"A internet ficou fora do ar por %s (de %s até %s).\nMonitoramento retomado automaticamente.",
			dur.String(),
			since.Local().Format("02/01/2006 15:04:05"),
			until.Local().Format("02/01/2006 15:04:05"),
		)
	}

	ev := monitor.ChangeEvent{
		SystemID:   "argus-connectivity",
		SystemName: title,
		Kind:       "connectivity",
		ToStatus:   status,
		Diagnosis:  title,
		Detail:     summary,
		OccurredAt: time.Now(),
	}
	ac := buildAlertContext(ev, 0, cfg.PublicURL, false)
	if offline {
		ac.PlainTitle = "⚠️ " + title
		ac.EmailSubject = "⚠️ " + title
	} else {
		ac.PlainTitle = "✅ " + title
		ac.EmailSubject = "✅ " + title
	}
	ac.PlainBody = summary
	_, err := n.sendToChannels(ctx, cfg, ev, 0, ac)
	return err
}

func (n *Notifier) sendNtfy(ctx context.Context, url, token string, e monitor.ChangeEvent, ac alertContext) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(ac.PlainBody))
	if err != nil {
		return err
	}
	req.Header.Set("Title", sanitizeHeader(ac.PlainTitle))
	priority := "default"
	tags := "white_check_mark"
	switch e.ToStatus {
	case monitor.StatusDown:
		priority = "urgent"
		tags = "rotating_light"
	case monitor.StatusDegraded:
		priority = "high"
		tags = "warning"
	}
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tags)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ac.PublicURL != "" {
		req.Header.Set("Click", ac.PublicURL)
	}
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func buildTeamsPayload(ac alertContext) map[string]any {
	color := "Good"
	switch ac.Badge {
	case "FORA DO AR", "DOWN":
		color = "Attention"
	case "INSTÁVEL", "DEGRADED":
		color = "Warning"
	}

	facts := []map[string]string{
		{"title": i18n.Pick("Sistema", "System"), "value": fmt.Sprintf("%s (%s)", ac.SystemName, ac.KindUpper)},
	}
	if ac.URL != "" {
		facts = append(facts, map[string]string{"title": "DNS", "value": ac.URL})
	}
	if ac.IP != "" {
		facts = append(facts, map[string]string{"title": "IP", "value": ac.IP})
	}
	if ac.TCPPort > 0 {
		facts = append(facts, map[string]string{"title": i18n.Pick("Porta TCP", "TCP Port"), "value": fmt.Sprintf("%d", ac.TCPPort)})
	}
	if ac.Detail != "" {
		facts = append(facts, map[string]string{"title": i18n.Pick("Detalhe", "Detail"), "value": ac.Detail})
	}
	if ac.MTTR != "" {
		facts = append(facts, map[string]string{"title": "MTTR", "value": ac.MTTR})
	}
	if len(ac.ImpactedSystems) > 0 {
		facts = append(facts, map[string]string{
			"title": i18n.Pick("Sistemas impactados", "Impacted systems"),
			"value": fmt.Sprintf("%d (%s)", len(ac.ImpactedSystems), strings.Join(ac.ImpactedSystems, ", ")),
		})
	}
	if ac.OccurredAt != "" {
		facts = append(facts, map[string]string{"title": i18n.Pick("Horário", "Time"), "value": ac.OccurredAt})
	}

	body := []map[string]any{
		{
			"type":   "TextBlock",
			"size":   "Medium",
			"weight": "Bolder",
			"color":  color,
			"text":   ac.PlainTitle,
			"wrap":   true,
		},
	}
	if ac.Diagnosis != "" {
		body = append(body, map[string]any{
			"type": "TextBlock",
			"text": ac.Diagnosis,
			"wrap": true,
		})
	}
	body = append(body, map[string]any{
		"type":  "FactSet",
		"facts": facts,
	})

	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body":    body,
	}
	if ac.PublicURL != "" {
		card["actions"] = []map[string]any{
			{
				"type":  "Action.OpenUrl",
				"title": i18n.Pick("Abrir no Argus", "Open in Argus"),
				"url":   ac.PublicURL,
			},
		}
	}

	return map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     card,
			},
		},
	}
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
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(v))
}

func (n *Notifier) sendEmail(ctx context.Context, emailCfg config.EmailAlertConfig, subject, plainBody, htmlBody string) error {
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
	if len(cleanTo) == 0 {
		return errors.New("no valid email recipients")
	}
	normalizeCRLF := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
	}
	encodedSubject := mime.QEncoding.Encode("utf-8", sanitizeHeader(subject))
	now := time.Now().UTC()
	msgID := fmt.Sprintf("<%d@argus.local>", now.UnixNano())

	var msg string
	if strings.TrimSpace(htmlBody) == "" {
		msg = fmt.Sprintf("Date: %s\r\nMessage-ID: %s\r\nFrom: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
			now.Format(time.RFC1123Z), msgID, from, strings.Join(cleanTo, ", "), encodedSubject, normalizeCRLF(plainBody))
	} else {
		boundary := fmt.Sprintf("argus-alt-%d", now.UnixNano())
		msg = fmt.Sprintf(
			"Date: %s\r\nMessage-ID: %s\r\nFrom: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n--%s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n\r\n--%s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n\r\n--%s--\r\n",
			now.Format(time.RFC1123Z), msgID, from, strings.Join(cleanTo, ", "), encodedSubject, boundary,
			boundary, normalizeCRLF(plainBody),
			boundary, normalizeCRLF(htmlBody),
			boundary,
		)
	}

	var conn net.Conn
	var err error
	if port == 465 {
		dialer := &tls.Dialer{
			Config: &tls.Config{ServerName: emailCfg.SMTPHost},
		}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
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

	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: emailCfg.SMTPHost}); err != nil {
				return err
			}
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

type alertContext struct {
	Emoji           string
	Badge           string
	ColorHex        string
	ColorInt        int
	SystemName      string
	KindUpper       string
	URL             string
	IP              string
	TCPPort         int
	Diagnosis       string
	Detail          string
	OccurredAt      string
	MTTR            string
	PublicURL       string
	ImpactedSystems []string
	IsRecovery      bool
	PlainTitle      string
	PlainBody       string
	EmailSubject    string
}

func statusVisual(status monitor.Status, isTest bool) (emoji, badge, colorHex string, colorInt int) {
	if isTest {
		return "🧪", i18n.Pick("TESTE", "TEST"), "#2563EB", 0x2563EB
	}
	switch status {
	case monitor.StatusDown:
		return "🔴", i18n.Pick("FORA DO AR", "DOWN"), "#DC2626", 0xDC2626
	case monitor.StatusDegraded:
		return "🟡", i18n.Pick("INSTÁVEL", "DEGRADED"), "#D97706", 0xD97706
	case monitor.StatusUP:
		return "🟢", i18n.Pick("RECUPERADO", "RECOVERED"), "#16A34A", 0x16A34A
	default:
		return "⚪", string(status), "#64748B", 0x64748B
	}
}

func composeDetailLine(e monitor.ChangeEvent) string {
	var parts []string
	if d := strings.TrimSpace(e.Detail); d != "" {
		parts = append(parts, d)
	}
	if e.StatusCode > 0 {
		codeStr := fmt.Sprintf("HTTP %d", e.StatusCode)
		if !strings.Contains(strings.Join(parts, " "), codeStr) {
			parts = append(parts, codeStr)
		}
	}
	if e.Latency > 0 {
		parts = append(parts, e.Latency.Truncate(time.Millisecond).String())
	}
	if errMsg := strings.TrimSpace(e.Error); errMsg != "" && !strings.Contains(strings.Join(parts, " "), errMsg) {
		parts = append(parts, errMsg)
	}
	return strings.Join(parts, " · ")
}

func buildAlertContext(e monitor.ChangeEvent, mttr time.Duration, publicURL string, isTest bool) alertContext {
	emoji, badge, colorHex, colorInt := statusVisual(e.ToStatus, isTest)
	sysName := strings.TrimSpace(e.SystemName)
	if sysName == "" {
		sysName = e.SystemID
	}
	kindUpper := strings.ToUpper(strings.TrimSpace(e.Kind))
	if kindUpper == "" {
		kindUpper = "CHECK"
	}

	var occurredStr string
	if !e.OccurredAt.IsZero() {
		occurredStr = e.OccurredAt.Format(i18n.Pick("02/01/2006 15:04:05", "2006-01-02 15:04:05"))
	}

	var mttrStr string
	if e.ToStatus == monitor.StatusUP && mttr > 0 {
		mttrStr = mttr.Truncate(time.Second).String()
	}

	detail := composeDetailLine(e)
	diag := strings.TrimSpace(e.Diagnosis)
	pub := strings.TrimRight(strings.TrimSpace(publicURL), "/")

	plainTitle := fmt.Sprintf("%s [%s] %s (%s)", emoji, badge, sysName, kindUpper)
	emailSubject := plainTitle
	if diag != "" {
		emailSubject += " — " + diag
	} else if mttrStr != "" {
		emailSubject += " — MTTR: " + mttrStr
	}

	ac := alertContext{
		Emoji:           emoji,
		Badge:           badge,
		ColorHex:        colorHex,
		ColorInt:        colorInt,
		SystemName:      sysName,
		KindUpper:       kindUpper,
		URL:             strings.TrimSpace(e.URL),
		IP:              strings.TrimSpace(e.IP),
		TCPPort:         e.TCPPort,
		Diagnosis:       diag,
		Detail:          detail,
		OccurredAt:      occurredStr,
		MTTR:            mttrStr,
		PublicURL:       pub,
		ImpactedSystems: e.ImpactedSystems,
		IsRecovery:      e.ToStatus == monitor.StatusUP && !isTest,
		PlainTitle:      plainTitle,
		EmailSubject:    emailSubject,
	}
	ac.PlainBody = formatPlainBody(ac)
	return ac
}

func formatPlainBody(ac alertContext) string {
	var lines []string
	lines = append(lines, i18n.Pick("Sistema: ", "System: ")+ac.SystemName+" ["+ac.KindUpper+"]")
	if ac.URL != "" {
		lines = append(lines, "DNS: "+ac.URL)
	}
	if ac.IP != "" {
		lines = append(lines, "IP: "+ac.IP)
	}
	if ac.TCPPort > 0 {
		lines = append(lines, fmt.Sprintf("%s%d", i18n.Pick("Porta TCP: ", "TCP Port: "), ac.TCPPort))
	}

	problemLabel := i18n.Pick("Problema: ", "Issue: ")
	if ac.IsRecovery {
		problemLabel = "Status: "
	}
	if ac.Diagnosis != "" {
		lines = append(lines, "", problemLabel+ac.Diagnosis)
	}
	if ac.Detail != "" {
		lines = append(lines, i18n.Pick("Detalhe: ", "Detail: ")+ac.Detail)
	}
	if ac.MTTR != "" {
		lines = append(lines, i18n.Pick("Duração da queda (MTTR): ", "Downtime (MTTR): ")+ac.MTTR)
	}
	if len(ac.ImpactedSystems) > 0 {
		lines = append(lines, fmt.Sprintf("%s%d (%s)", i18n.Pick("Sistemas impactados: ", "Impacted systems: "), len(ac.ImpactedSystems), strings.Join(ac.ImpactedSystems, ", ")))
	}
	if ac.OccurredAt != "" {
		lines = append(lines, i18n.Pick("Horário: ", "Time: ")+ac.OccurredAt)
	}
	if ac.PublicURL != "" {
		lines = append(lines, "", i18n.Pick("Painel: ", "Dashboard: ")+ac.PublicURL)
	}
	return strings.Join(lines, "\n")
}

func formatTelegramHTML(ac alertContext) string {
	esc := html.EscapeString
	var b strings.Builder

	fmt.Fprintf(&b, "%s <b>%s — %s</b> <code>[%s]</code>\n\n",
		ac.Emoji, esc(ac.Badge), esc(ac.SystemName), esc(ac.KindUpper))

	fmt.Fprintf(&b, "<b>%s:</b> %s\n", i18n.Pick("Sistema", "System"), esc(ac.SystemName))
	if ac.URL != "" {
		fmt.Fprintf(&b, "<b>DNS:</b> <code>%s</code>\n", esc(ac.URL))
	}
	if ac.IP != "" {
		fmt.Fprintf(&b, "<b>IP:</b> <code>%s</code>\n", esc(ac.IP))
	}
	if ac.TCPPort > 0 {
		fmt.Fprintf(&b, "<b>%s:</b> <code>%d</code>\n", i18n.Pick("Porta TCP", "TCP Port"), ac.TCPPort)
	}

	problemLabel := i18n.Pick("Problema", "Issue")
	if ac.IsRecovery {
		problemLabel = "Status"
	}
	if ac.Diagnosis != "" || ac.Detail != "" || ac.MTTR != "" || ac.OccurredAt != "" {
		b.WriteByte('\n')
	}
	if ac.Diagnosis != "" {
		fmt.Fprintf(&b, "<b>%s:</b> %s\n", problemLabel, esc(ac.Diagnosis))
	}
	if ac.Detail != "" {
		fmt.Fprintf(&b, "<b>%s:</b> <code>%s</code>\n", i18n.Pick("Detalhe", "Detail"), esc(ac.Detail))
	}
	if ac.MTTR != "" {
		fmt.Fprintf(&b, "<b>%s:</b> <code>%s</code>\n", i18n.Pick("Duração da queda (MTTR)", "Downtime (MTTR)"), esc(ac.MTTR))
	}
	if len(ac.ImpactedSystems) > 0 {
		fmt.Fprintf(&b, "<b>%s:</b> %d (%s)\n", i18n.Pick("Sistemas impactados", "Impacted systems"), len(ac.ImpactedSystems), esc(strings.Join(ac.ImpactedSystems, ", ")))
	}
	if ac.OccurredAt != "" {
		fmt.Fprintf(&b, "<b>%s:</b> %s\n", i18n.Pick("Horário", "Time"), esc(ac.OccurredAt))
	}
	if ac.PublicURL != "" {
		fmt.Fprintf(&b, "\n<a href=\"%s\">%s</a>", esc(ac.PublicURL), esc(i18n.Pick("Abrir no Argus", "Open in Argus")))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatEmailHTML(ac alertContext) string {
	esc := html.EscapeString
	var rows strings.Builder
	addRow := func(label, value string, mono bool) {
		if value == "" {
			return
		}
		valHTML := esc(value)
		if mono {
			valHTML = fmt.Sprintf(`<code style="background:#f1f5f9;padding:2px 6px;border-radius:4px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px;color:#0f172a;">%s</code>`, valHTML)
		}
		fmt.Fprintf(&rows,
			`<tr><td style="padding:8px 12px;border-bottom:1px solid #e2e8f0;color:#64748b;font-size:13px;width:150px;vertical-align:top;">%s</td><td style="padding:8px 12px;border-bottom:1px solid #e2e8f0;color:#0f172a;font-size:13px;font-weight:500;">%s</td></tr>`,
			esc(label), valHTML)
	}

	addRow(i18n.Pick("Sistema", "System"), ac.SystemName, false)
	addRow(i18n.Pick("Camada", "Layer"), ac.KindUpper, true)
	addRow("DNS", ac.URL, true)
	addRow("IP", ac.IP, true)
	if ac.TCPPort > 0 {
		addRow(i18n.Pick("Porta TCP", "TCP Port"), fmt.Sprintf("%d", ac.TCPPort), true)
	}
	addRow(i18n.Pick("Duração (MTTR)", "Downtime (MTTR)"), ac.MTTR, true)
	addRow(i18n.Pick("Horário", "Time"), ac.OccurredAt, false)

	var diagBlock string
	if ac.Diagnosis != "" || ac.Detail != "" {
		problemLabel := i18n.Pick("Problema identificado", "Detected issue")
		if ac.IsRecovery {
			problemLabel = i18n.Pick("Status atual", "Current status")
		}
		var detailHTML string
		if ac.Detail != "" {
			detailHTML = fmt.Sprintf(`<div style="margin-top:6px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;color:#475569;">%s</div>`, esc(ac.Detail))
		}
		diagText := ac.Diagnosis
		if diagText == "" {
			diagText = ac.Badge
		}
		diagBlock = fmt.Sprintf(
			`<div style="background:#f8fafc;border-left:4px solid %s;padding:12px 14px;border-radius:4px;margin-bottom:18px;"><div style="font-size:11px;text-transform:uppercase;letter-spacing:0.05em;color:#64748b;margin-bottom:4px;">%s</div><div style="font-size:15px;font-weight:600;color:#0f172a;">%s</div>%s</div>`,
			ac.ColorHex, esc(problemLabel), esc(diagText), detailHTML)
	}

	var ctaBlock string
	if ac.PublicURL != "" {
		ctaBlock = fmt.Sprintf(
			`<div style="margin-top:20px;"><a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;font-size:13px;font-weight:600;padding:10px 16px;border-radius:6px;">%s</a></div>`,
			esc(ac.PublicURL), ac.ColorHex, esc(i18n.Pick("Abrir no Argus", "Open in Argus")))
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="margin:0;padding:20px;background:#f1f5f9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
  <div style="max-width:580px;margin:0 auto;background:#ffffff;border-radius:8px;overflow:hidden;border:1px solid #e2e8f0;">
    <div style="background:%s;color:#ffffff;padding:16px 20px;">
      <div style="font-size:12px;font-weight:600;letter-spacing:0.06em;opacity:0.9;">%s %s · %s</div>
      <div style="font-size:20px;font-weight:700;margin-top:4px;">%s</div>
    </div>
    <div style="padding:20px;">
      %s
      <table style="width:100%%;border-collapse:collapse;">%s</table>
      %s
    </div>
    <div style="padding:12px 20px;background:#f8fafc;border-top:1px solid #e2e8f0;font-size:11px;color:#94a3b8;">Argus Monitor</div>
  </div>
</body>
</html>`, ac.ColorHex, ac.Emoji, esc(ac.Badge), esc(ac.KindUpper), esc(ac.SystemName), diagBlock, rows.String(), ctaBlock)
}

func buildSlackPayload(ac alertContext) map[string]any {
	var lines []string
	lines = append(lines, fmt.Sprintf("*%s:* %s (`%s`)", i18n.Pick("Sistema", "System"), ac.SystemName, ac.KindUpper))
	if ac.URL != "" {
		lines = append(lines, fmt.Sprintf("*DNS:* `%s`", ac.URL))
	}
	if ac.IP != "" {
		lines = append(lines, fmt.Sprintf("*IP:* `%s`", ac.IP))
	}
	if ac.TCPPort > 0 {
		lines = append(lines, fmt.Sprintf("*%s:* `%d`", i18n.Pick("Porta TCP", "TCP Port"), ac.TCPPort))
	}
	if ac.Diagnosis != "" {
		label := i18n.Pick("Problema", "Issue")
		if ac.IsRecovery {
			label = "Status"
		}
		lines = append(lines, fmt.Sprintf("*%s:* %s", label, ac.Diagnosis))
	}
	if ac.Detail != "" {
		lines = append(lines, fmt.Sprintf("*%s:* `%s`", i18n.Pick("Detalhe", "Detail"), ac.Detail))
	}
	if ac.MTTR != "" {
		lines = append(lines, fmt.Sprintf("*MTTR:* `%s`", ac.MTTR))
	}
	if ac.OccurredAt != "" {
		lines = append(lines, fmt.Sprintf("*%s:* %s", i18n.Pick("Horário", "Time"), ac.OccurredAt))
	}
	if ac.PublicURL != "" {
		lines = append(lines, fmt.Sprintf("<%s|%s>", ac.PublicURL, i18n.Pick("Abrir no Argus", "Open in Argus")))
	}

	return map[string]any{
		"text": ac.PlainTitle,
		"attachments": []map[string]any{
			{
				"color":     ac.ColorHex,
				"title":     ac.PlainTitle,
				"text":      strings.Join(lines, "\n"),
				"mrkdwn_in": []string{"text"},
				"footer":    "Argus Monitor",
			},
		},
	}
}

func buildDiscordPayload(ac alertContext, occurredAt time.Time) map[string]any {
	fields := []map[string]any{
		{"name": i18n.Pick("Sistema", "System"), "value": ac.SystemName, "inline": true},
		{"name": i18n.Pick("Camada", "Layer"), "value": fmt.Sprintf("`%s`", ac.KindUpper), "inline": true},
	}
	if ac.URL != "" {
		fields = append(fields, map[string]any{"name": "DNS", "value": fmt.Sprintf("`%s`", ac.URL), "inline": false})
	}
	if ac.IP != "" {
		fields = append(fields, map[string]any{"name": "IP", "value": fmt.Sprintf("`%s`", ac.IP), "inline": true})
	}
	if ac.TCPPort > 0 {
		fields = append(fields, map[string]any{"name": i18n.Pick("Porta TCP", "TCP Port"), "value": fmt.Sprintf("`%d`", ac.TCPPort), "inline": true})
	}
	if ac.Detail != "" {
		fields = append(fields, map[string]any{"name": i18n.Pick("Detalhe", "Detail"), "value": fmt.Sprintf("`%s`", ac.Detail), "inline": false})
	}
	if ac.MTTR != "" {
		fields = append(fields, map[string]any{"name": i18n.Pick("Duração (MTTR)", "Downtime (MTTR)"), "value": fmt.Sprintf("`%s`", ac.MTTR), "inline": true})
	}

	embed := map[string]any{
		"title":  ac.PlainTitle,
		"color":  ac.ColorInt,
		"fields": fields,
		"footer": map[string]string{"text": "Argus Monitor"},
	}
	if ac.Diagnosis != "" {
		embed["description"] = ac.Diagnosis
	}
	if ac.PublicURL != "" {
		embed["url"] = ac.PublicURL
	}
	if !occurredAt.IsZero() {
		embed["timestamp"] = occurredAt.UTC().Format(time.RFC3339)
	}

	return map[string]any{
		"embeds": []map[string]any{embed},
	}
}
