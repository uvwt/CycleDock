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

func TestCardReadCursorHTTPFlow(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var identity identityResponse
	decodeRecorder(t, login, &identity)
	workspaceID := uuid.MustParse(identity.Workspace.ID)
	humanSourceID := uuid.MustParse(identity.Source.ID)

	ctx := context.Background()
	project, err := dataStore.CreateProject(ctx, workspaceID, "Unread Test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, humanSourceID)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Unread Agent")
	if err != nil {
		t.Fatal(err)
	}

	getCardSummary := func() cardResponse {
		t.Helper()
		rec := doJSON(t, handler, http.MethodGet,
			"/api/v1/projects/"+project.ID.String()+"/cards", nil, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list cards: %d %s", rec.Code, rec.Body.String())
		}
		var cards []cardResponse
		decodeRecorder(t, rec, &cards)
		for _, item := range cards {
			if item.ID == card.ID.String() {
				if item.Unread == nil || item.LatestMessageSequence == nil ||
					item.LastReadMessageSequence == nil {
					t.Fatalf("card summary omitted read state: %+v", item)
				}
				return item
			}
		}
		t.Fatalf("card %s missing from summary", card.ID)
		return cardResponse{}
	}

	initial := getCardSummary()
	if *initial.Unread || *initial.LatestMessageSequence != 0 ||
		*initial.LastReadMessageSequence != 0 {
		t.Fatalf("new card read state: %+v", initial)
	}

	// 自己发送的消息会推进消息序号，但不能产生自己的未读蓝点。
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspaceID, CardID: card.ID, AuthorSourceID: humanSourceID,
		Content: "请检查", IdempotencyKey: "read-self-1",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	afterOwnMessage := getCardSummary()
	if *afterOwnMessage.Unread || *afterOwnMessage.LatestMessageSequence == 0 {
		t.Fatalf("own message produced unread state: %+v", afterOwnMessage)
	}

	claim, err := dataStore.ClaimCardTurnAttempt(
		ctx, workspaceID, card.ID, agent.ID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: card.ID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: agent.ID, IdempotencyKey: "read-reply-1",
		Message:     store.ReplyMessage{Type: "text", Content: "检查完成", EndTurn: true},
		FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	afterReply := getCardSummary()
	if !*afterReply.Unread ||
		*afterReply.LatestMessageSequence <= *afterReply.LastReadMessageSequence {
		t.Fatalf("agent reply did not produce unread state: %+v", afterReply)
	}

	readRec := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/read", map[string]any{}, cookie, nil)
	if readRec.Code != http.StatusOK {
		t.Fatalf("mark read: %d %s", readRec.Code, readRec.Body.String())
	}
	var read cardReadResponse
	decodeRecorder(t, readRec, &read)
	afterRead := getCardSummary()
	if *afterRead.Unread ||
		*afterRead.LastReadMessageSequence != read.LastReadMessageSequence ||
		read.LastReadMessageSequence != *afterReply.LatestMessageSequence {
		t.Fatalf("read cursor not persisted: read=%+v card=%+v", read, afterRead)
	}

	// 元数据变化不属于消息，不允许重新制造未读蓝点。
	if _, err := dataStore.UpdateCardText(ctx, workspaceID, card.ID, "改过标题", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetCardPinned(ctx, workspaceID, card.ID, true); err != nil {
		t.Fatal(err)
	}
	afterMetadata := getCardSummary()
	if *afterMetadata.Unread ||
		*afterMetadata.LatestMessageSequence != *afterRead.LatestMessageSequence ||
		*afterMetadata.LastReadMessageSequence != *afterRead.LastReadMessageSequence {
		t.Fatalf("metadata change altered read state: before=%+v after=%+v", afterRead, afterMetadata)
	}

	// 读后自己的下一条消息仍不算未读；只有其他来源的新可见消息才重新点亮。
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspaceID, CardID: card.ID, AuthorSourceID: humanSourceID,
		Content: "继续检查", IdempotencyKey: "read-self-2",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	if ownAgain := getCardSummary(); *ownAgain.Unread {
		t.Fatalf("second own message produced unread state: %+v", ownAgain)
	}
	claim, err = dataStore.ClaimCardTurnAttempt(
		ctx, workspaceID, card.ID, agent.ID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: card.ID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: agent.ID, IdempotencyKey: "read-reply-2",
		Message:     store.ReplyMessage{Type: "text", Content: "再次完成", EndTurn: true},
		FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if final := getCardSummary(); !*final.Unread {
		t.Fatalf("new agent reply did not restore unread state: %+v", final)
	}
}
