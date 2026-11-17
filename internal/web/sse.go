package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type sseEvent struct {
	Event string
	Data  string
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan sseEvent, 64)
	s.sseMu.Lock()
	s.sseClients[ch] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, ch)
		s.sseMu.Unlock()
	}()

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
			flusher.Flush()
		case <-keepAlive.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) broadcastSystemUpdate(sysID string) {
	s.sseMu.RLock()
	hasClients := len(s.sseClients) > 0
	s.sseMu.RUnlock()
	if !hasClients {
		return
	}

	snap, ok := s.store.GetSystem(sysID)
	if !ok {
		return
	}

	t := s.defaultMessages()
	var cardBuf bytes.Buffer
	if err := s.cardTmpl.Execute(&cardBuf, toCardViewModelWithLang(snap, t)); err != nil {
		return
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
		return
	}

	events := []sseEvent{{Event: "card", Data: string(cardPayload)}}

	if statsHTML, err := s.renderStatsHTML(t); err == nil {
		if statsPayload, err := json.Marshal(struct {
			HTML string `json:"html"`
		}{HTML: statsHTML}); err == nil {
			events = append(events, sseEvent{Event: "stats", Data: string(statsPayload)})
		}
	}

	s.sseMu.RLock()
	for ch := range s.sseClients {
		for _, ev := range events {
			select {
			case ch <- ev:
			default:
			}
		}
	}
	s.sseMu.RUnlock()
}
