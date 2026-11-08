package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestOngoingCheckinAcknowledge_WarningAndCriticalAndSubsequentCheckin(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	var person identityResponse
	decodeRecorder(t, login, &person)
	workspaceID := uuid.MustParse(person.Workspace.ID)
	humanSourceID := uuid.MustParse(person.Source.ID)

	ctx := context.Background()
	project, err := dataStore.CreateProject(ctx, workspaceID, "Ongoing Ack Project", "")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatal(err)
	}
	ongoing, err := dataStore.CreateOngoingCard(ctx, workspaceID, project.ID, humanSourceID, "Monitor Disk", "Check disk usage", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	todo, err := dataStore.CreateTodoCard(ctx, workspaceID, project.ID, humanSourceID, "Normal Todo", "Do something", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 非 ongoing 卡片确认巡检返回 409 Conflict
	todoAck := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+todo.ID.String()+"/acknowledge-checkin", map[string]any{}, cookie, nil)
	if todoAck.Code != http.StatusConflict {
		t.Fatalf("expected 409 for todo card, got %d %s", todoAck.Code, todoAck.Body.String())
	}

	// 2. 尚未巡检（unknown）时确认巡检返回 409 Conflict
	unknownAck := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+ongoing.ID.String()+"/acknowledge-checkin", map[string]any{}, cookie, nil)
	if unknownAck.Code != http.StatusConflict {
		t.Fatalf("expected 409 when no checkin exists, got %d %s", unknownAck.Code, unknownAck.Body.String())
	}

	recordCheckin := func(status domain.CheckinStatus, content, key string) {
		t.Helper()
		comment := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+ongoing.ID.String()+"/comments", map[string]any{
			"content":       "trigger " + key,
			"dispatch_mode": "enqueue",
		}, cookie, map[string]string{"Idempotency-Key": "turn-" + key})
		if comment.Code != http.StatusCreated {
			t.Fatalf("create turn %s: %d %s", key, comment.Code, comment.Body.String())
		}
		claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agent.ID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatalf("claim turn %s: %v", key, err)
		}
		if _, _, err := dataStore.AddAttemptEvent(ctx, store.AttemptEventParams{
			WorkspaceID:    workspaceID,
			CardID:         ongoing.ID,
			AttemptID:      claim.Attempt.ID,
			RunnerSourceID: agent.ID,
			Type:           domain.EventTypeCheckin,
			Content:        content,
			Checkin:        &status,
			IdempotencyKey: "checkin-" + key,
		}); err != nil {
			t.Fatalf("add checkin %s: %v", key, err)
		}
		if err := dataStore.CompleteAttempt(ctx, workspaceID, ongoing.ID, claim.Attempt.ID, agent.ID, time.Now().UTC()); err != nil {
			t.Fatalf("complete attempt %s: %v", key, err)
		}
	}

	// 3. 记录 warning 巡检并验证人工确认
	recordCheckin(domain.CheckinWarning, "Disk usage at 82%", "w1")

	detailBefore := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+ongoing.ID.String(), nil, cookie, nil)
	var detailData cardDetailResponse
	decodeRecorder(t, detailBefore, &detailData)
	if detailData.Health != string(domain.CheckinWarning) {
		t.Fatalf("expected warning health before ack, got %q", detailData.Health)
	}

	ackWarning := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+ongoing.ID.String()+"/acknowledge-checkin", map[string]any{}, cookie, nil)
	if ackWarning.Code != http.StatusOK {
		t.Fatalf("acknowledge warning checkin failed: %d %s", ackWarning.Code, ackWarning.Body.String())
	}
	var ackedCard cardResponse
	decodeRecorder(t, ackWarning, &ackedCard)
	if ackedCard.Health != string(domain.CheckinAcknowledged) {
		t.Fatalf("expected acknowledged health in response, got %q", ackedCard.Health)
	}

	// 再次确认已确认的巡检应返回 409 Conflict
	dupAck := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+ongoing.ID.String()+"/acknowledge-checkin", map[string]any{}, cookie, nil)
	if dupAck.Code != http.StatusConflict {
		t.Fatalf("expected 409 for already acknowledged checkin, got %d %s", dupAck.Code, dupAck.Body.String())
	}

	// 验证详情与卡片列表均体现为 acknowledged，且时间线包含 checkin_ack 事件
	detailAfterWarning := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+ongoing.ID.String(), nil, cookie, nil)
	decodeRecorder(t, detailAfterWarning, &detailData)
	if detailData.Health != string(domain.CheckinAcknowledged) || detailData.Card.Health != string(domain.CheckinAcknowledged) {
		t.Fatalf("expected detail health acknowledged, got detail=%q card=%q", detailData.Health, detailData.Card.Health)
	}
	lastEv := detailData.Timeline[len(detailData.Timeline)-1]
	if lastEv.Type != string(domain.EventTypeCheckinAck) || lastEv.Content != "warning -> acknowledged" {
		t.Fatalf("unexpected last timeline event after warning ack: %+v", lastEv)
	}

	listCards := doJSON(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID.String()+"/cards", nil, cookie, nil)
	var cards []cardResponse
	decodeRecorder(t, listCards, &cards)
	var foundOngoing *cardResponse
	for i := range cards {
		if cards[i].ID == ongoing.ID.String() {
			foundOngoing = &cards[i]
			break
		}
	}
	if foundOngoing == nil || foundOngoing.Health != string(domain.CheckinAcknowledged) {
		t.Fatalf("expected ongoing card in list to have acknowledged health, got %+v", foundOngoing)
	}

	// 4. 新一次巡检为 critical，健康状态恢复为 critical，并支持再次人工确认
	recordCheckin(domain.CheckinCritical, "Disk usage at 96%", "c1")

	detailCritical := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+ongoing.ID.String(), nil, cookie, nil)
	decodeRecorder(t, detailCritical, &detailData)
	if detailData.Health != string(domain.CheckinCritical) {
		t.Fatalf("expected new critical checkin to override acknowledged, got %q", detailData.Health)
	}

	ackCritical := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+ongoing.ID.String()+"/acknowledge-checkin", map[string]any{}, cookie, nil)
	if ackCritical.Code != http.StatusOK {
		t.Fatalf("acknowledge critical checkin failed: %d %s", ackCritical.Code, ackCritical.Body.String())
	}
	decodeRecorder(t, ackCritical, &ackedCard)
	if ackedCard.Health != string(domain.CheckinAcknowledged) {
		t.Fatalf("expected acknowledged health after critical ack, got %q", ackedCard.Health)
	}

	// 5. 新一次巡检为 healthy，健康状态变为 healthy，且不允许确认 healthy
	recordCheckin(domain.CheckinHealthy, "Cleaned logs, disk at 45%", "h1")
	healthyAck := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+ongoing.ID.String()+"/acknowledge-checkin", map[string]any{}, cookie, nil)
	if healthyAck.Code != http.StatusConflict {
		t.Fatalf("expected 409 when checkin is healthy, got %d %s", healthyAck.Code, healthyAck.Body.String())
	}
}
