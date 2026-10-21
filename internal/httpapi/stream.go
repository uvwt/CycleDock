package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/realtime"
)

// streamCard is authenticated by requireHuman before upgrading to a long-lived response.
// PostgreSQL remains the durable event source even when an in-memory stream is interrupted.
func (api *API) streamCard(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if _, err := api.store.GetCard(r.Context(), identity.WorkspaceID, cardID); err != nil {
		api.handleError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "stream unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	send := func(event realtime.Event, seq int64) bool {
		data, err := realtime.Encode(event)
		if err != nil {
			return false
		}
		if seq > 0 {
			if _, err = fmt.Fprintf(w, "id: %d\n", seq); err != nil {
				return false
			}
		}
		if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	var sub realtime.Subscriber
	var snapshot *realtime.Event
	if api.live != nil {
		sub, snapshot = api.live.Subscribe(cardID)
		defer sub.Close()
	}
	if snapshot != nil {
		if !send(*snapshot, 0) {
			return
		}
	}
	// 浏览器重新连接时重新读取 REST；这里的事件仅用于即时提示刷新。
	var lastSequence int64
	if err := api.pool.QueryRow(r.Context(), `SELECT COALESCE(MAX(sequence),0) FROM events WHERE workspace_id=$1 AND card_id=$2`, identity.WorkspaceID, cardID).Scan(&lastSequence); err != nil {
		return
	}
	tick := time.NewTicker(1500 * time.Millisecond)
	defer tick.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	// Session is checked at connection time. Force a fresh authenticated handshake periodically
	// so revoked or expired sessions never retain indefinite realtime access.
	expire := time.NewTimer(15 * time.Minute)
	defer expire.Stop()
	ctx := r.Context()
	var streamEvents <-chan realtime.Event
	if api.live != nil {
		streamEvents = sub.Events
	}
	lastStage := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-expire.C:
			return
		case event, ok := <-streamEvents:
			if !ok {
				return
			}
			if !send(event, 0) {
				return
			}
		case <-tick.C:
			if err := api.streamPoll(ctx, identity.WorkspaceID, cardID, &lastSequence, &lastStage, send); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (api *API) streamPoll(ctx context.Context, workspaceID, cardID uuid.UUID, lastSeq *int64, lastStage *string, send func(realtime.Event, int64) bool) error {
	var latest int64
	if err := api.pool.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0) FROM events WHERE workspace_id=$1 AND card_id=$2`, workspaceID, cardID).Scan(&latest); err != nil {
		return err
	}
	if latest > *lastSeq {
		*lastSeq = latest
		if !send(realtime.Event{Type: "event.created"}, latest) {
			return context.Canceled
		}
	}
	var runID, runStatus, stage string
	err := api.pool.QueryRow(ctx, `
 SELECT id::text,status,current_stage
 FROM agent_runs WHERE workspace_id=$1 AND card_id=$2
 ORDER BY created_at DESC LIMIT 1
 `, workspaceID, cardID).Scan(&runID, &runStatus, &stage)
	if err == nil {
		key := runID + ":" + runStatus + ":" + stage
		if key != *lastStage {
			*lastStage = key
			if !send(realtime.Event{Type: "run.stage", RunID: runID, Stage: stage}, 0) {
				return context.Canceled
			}
		}
	}
	return nil
}
