package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	// 立即提交响应头，避免空闲 Card 的浏览器必须等到 20 秒心跳才触发 onopen。
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()
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
	var attemptID, attemptStatus, stage string
	err := api.pool.QueryRow(ctx, `
 SELECT id::text,status,current_stage
 FROM turn_attempts WHERE workspace_id=$1 AND card_id=$2
 ORDER BY created_at DESC LIMIT 1
 `, workspaceID, cardID).Scan(&attemptID, &attemptStatus, &stage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key := attemptID + ":" + attemptStatus + ":" + stage
	if key == *lastStage {
		return nil
	}
	*lastStage = key
	// 跨实例 Run 状态变化也须能让客户端收口；实时 token 本身由执行实例发送。
	eventType := "attempt.stage"
	switch attemptStatus {
	case "completed", "superseded", "cancelled", "expired":
		eventType = "attempt.finished"
	case "failed":
		eventType = "attempt.failed"
	case "queued":
		stage = "排队中"
	case "waiting_human":
		stage = "等待人工回复"
	case "running":
		if stage == "" {
			stage = "正在执行"
		}
	}
	if !send(realtime.Event{Type: eventType, AttemptID: attemptID, Stage: stage}, 0) {
		return context.Canceled
	}
	return nil
}
