package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"argus/internal/i18n"
	"argus/internal/monitor"
)

type sseEvent struct {
	Event string
	Data  string
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	lang := s.messagesForRequest(w, r).Lang
	ch := make(chan sseEvent, 64)
	s.sseMu.Lock()
	s.sseClients[ch] = lang
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, ch)
		s.sseMu.Unlock()
	}()

	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	if st := s.connectivityStatus(); st.Offline {
		if ev, ok := buildConnectivitySSE(st, i18n.For(lang)); ok {
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data); err != nil {
				return
			}
		}
	}
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) broadcastSystemUpdate(sysID string) {
	s.sseMu.RLock()
	langsNeeded := make(map[string]struct{}, 2)
	for _, lang := range s.sseClients {
		langsNeeded[lang] = struct{}{}
	}
	s.sseMu.RUnlock()
	if len(langsNeeded) == 0 {
		return
	}

	snap, ok := s.store.GetSystem(sysID)
	if !ok {
		return
	}

	eventsByLang := make(map[string][]sseEvent, len(langsNeeded))
	for lang := range langsNeeded {
		t := i18n.For(lang)
		var cardBuf bytes.Buffer
		if err := s.cardTmpl.Execute(&cardBuf, toCardViewModelWithLang(snap, t)); err != nil {
			continue
		}

		cardPayload, err := json.Marshal(struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			HTML   string `json:"html"`
		}{
			ID:     sysID,
			Status: string(snap.WorstStatus),
			HTML:   cardBuf.String(),
		})
		if err != nil {
			continue
		}

		events := []sseEvent{{Event: "card", Data: string(cardPayload)}}
		if statsHTML, err := s.renderStatsHTML(t); err == nil {
			if statsPayload, err := json.Marshal(struct {
				HTML string `json:"html"`
			}{HTML: statsHTML}); err == nil {
				events = append(events, sseEvent{Event: "stats", Data: string(statsPayload)})
			}
		}
		eventsByLang[lang] = events
	}

	s.sseMu.RLock()
	for ch, lang := range s.sseClients {
		events := eventsByLang[lang]
		if len(events) == 0 || cap(ch)-len(ch) < len(events) {
			continue
		}
		for _, ev := range events {
			ch <- ev
		}
	}
	s.sseMu.RUnlock()
}

// BroadcastConnectivity pushes internet connectivity status updates to connected SSE clients.
func (s *Server) BroadcastConnectivity(st monitor.ConnectivityStatus) {
	s.sseMu.RLock()
	langsNeeded := make(map[string]struct{}, 2)
	for _, lang := range s.sseClients {
		langsNeeded[lang] = struct{}{}
	}
	s.sseMu.RUnlock()
	if len(langsNeeded) == 0 {
		return
	}

	eventsByLang := make(map[string]sseEvent, len(langsNeeded))
	for lang := range langsNeeded {
		if ev, ok := buildConnectivitySSE(st, i18n.For(lang)); ok {
			eventsByLang[lang] = ev
		}
	}

	s.sseMu.RLock()
	for ch, lang := range s.sseClients {
		ev, ok := eventsByLang[lang]
		if !ok || cap(ch)-len(ch) < 1 {
			continue
		}
		ch <- ev
	}
	s.sseMu.RUnlock()
}

func buildConnectivitySSE(st monitor.ConnectivityStatus, t i18n.Messages) (sseEvent, bool) {
	lastText := formatOfflineLastCheck(st.LastChecked, t)
	payload, err := json.Marshal(struct {
		Offline       bool   `json:"offline"`
		LastCheckText string `json:"lastCheckText"`
	}{
		Offline:       st.Offline,
		LastCheckText: lastText,
	})
	if err != nil {
		return sseEvent{}, false
	}
	return sseEvent{Event: "connectivity", Data: string(payload)}, true
}

func formatOfflineLastCheck(ts time.Time, t i18n.Messages) string {
	timeStr := "—"
	if !ts.IsZero() {
		timeStr = ts.Local().Format("15:04:05")
	}
	return fmt.Sprintf(t.OfflineLastCheckFmt, timeStr)
}
