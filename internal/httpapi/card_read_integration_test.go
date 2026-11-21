package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestCardReadCursorHTTPFlow(t *testing.T) {
	for _, kind := range []domain.CardKind{domain.CardKindDiscussion, domain.CardKindTodo, domain.CardKindOngoing} {
		t.Run(string(kind), func(t *testing.T) {
			testCardReadCursorHTTPFlow(t, kind)
		})
	}
}

func testCardReadCursorHTTPFlow(t *testing.T, kind domain.CardKind) {
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
	project, err := dataStore.CreateProject(ctx, workspaceID, "Unread Test", "")
	if err != nil {
		t.Fatal(err)
	}
	var card db.Card
	switch kind {
	case domain.CardKindDiscussion:
		card, err = dataStore.CreateDiscussion(ctx, workspaceID, project.ID, humanSourceID)
	case domain.CardKindTodo:
		card, err = dataStore.CreateTodoCard(ctx, workspaceID, project.ID, humanSourceID, "Unread task", "", uuid.NullUUID{}, 1)
	case domain.CardKindOngoing:
		card, err = dataStore.CreateOngoingCard(ctx, workspaceID, project.ID, humanSourceID, "Unread plan", "", uuid.NullUUID{})
	}
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

	// 详情只提供卡片本体，前端进入详情时不能依赖其中的 unread 字段。
	detailRec := doJSON(t, handler, http.MethodGet,
		"/api/v1/cards/"+card.ID.String(), nil, cookie, nil)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("get card: %d %s", detailRec.Code, detailRec.Body.String())
	}
	var detail cardDetailResponse
	decodeRecorder(t, detailRec, &detail)
	if detail.Card.Unread != nil || len(detail.Timeline) == 0 {
		t.Fatalf("unexpected detail read contract: %+v", detail)
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

func TestCardMarkUnreadHTTPFlowAndIsolation(t *testing.T) {
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
	project, err := dataStore.CreateProject(ctx, workspaceID, "Manual Unread Test", "")
	if err != nil {
		t.Fatal(err)
	}
	emptyDiscussion, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, humanSourceID)
	if err != nil {
		t.Fatal(err)
	}
	todoCard, err := dataStore.CreateTodoCard(ctx, workspaceID, project.ID, humanSourceID, "Todo unread", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ongoingCard, err := dataStore.CreateOngoingCard(ctx, workspaceID, project.ID, humanSourceID, "Ongoing unread", "", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}

	getSummary := func(cardID uuid.UUID) cardResponse {
		t.Helper()
		rec := doJSON(t, handler, http.MethodGet,
			"/api/v1/projects/"+project.ID.String()+"/cards", nil, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list cards: %d %s", rec.Code, rec.Body.String())
		}
		var cards []cardResponse
		decodeRecorder(t, rec, &cards)
		for _, item := range cards {
			if item.ID == cardID.String() {
				if item.Unread == nil || item.MarkedUnread == nil {
					t.Fatalf("card summary omitted unread fields: %+v", item)
				}
				return item
			}
		}
		t.Fatalf("card %s missing from summary", cardID)
		return cardResponse{}
	}

	// 1. 没有任何消息的空白卡片手动标为未读后，依然稳定返回 unread=true & marked_unread=true（跨请求持久且幂等）。
	for _, cardID := range []uuid.UUID{emptyDiscussion.ID, todoCard.ID, ongoingCard.ID} {
		initial := getSummary(cardID)
		if *initial.Unread || *initial.MarkedUnread {
			t.Fatalf("expected initial read state false, got %+v", initial)
		}

		for attempt := 0; attempt < 2; attempt++ {
			unreadRec := doJSON(t, handler, http.MethodPost,
				"/api/v1/cards/"+cardID.String()+"/unread", map[string]any{}, cookie, nil)
			if unreadRec.Code != http.StatusOK {
				t.Fatalf("mark unread attempt %d: %d %s", attempt, unreadRec.Code, unreadRec.Body.String())
			}
			var resp cardReadResponse
			decodeRecorder(t, unreadRec, &resp)
			if !resp.Unread || !resp.MarkedUnread {
				t.Fatalf("unexpected mark unread response: %+v", resp)
			}
		}

		afterUnread := getSummary(cardID)
		if !*afterUnread.Unread || !*afterUnread.MarkedUnread {
			t.Fatalf("card %s should remain unread after mark unread: %+v", cardID, afterUnread)
		}
	}

	// 2. 已有消息且已读到最新后，再次手动标未读应保留已读游标且恢复未读状态；再标已读则复位 marked_unread=false。
	agent, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Unread Agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspaceID, CardID: emptyDiscussion.ID, AuthorSourceID: humanSourceID,
		Content: "hello", IdempotencyKey: "manual-unread-msg-1",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimCardTurnAttempt(ctx, workspaceID, emptyDiscussion.ID, agent.ID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: emptyDiscussion.ID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: agent.ID, IdempotencyKey: "manual-unread-reply-1",
		Message:     store.ReplyMessage{Type: "text", Content: "done", EndTurn: true},
		FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}

	readRec := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+emptyDiscussion.ID.String()+"/read", map[string]any{}, cookie, nil)
	if readRec.Code != http.StatusOK {
		t.Fatalf("mark read: %d %s", readRec.Code, readRec.Body.String())
	}
	var readResp cardReadResponse
	decodeRecorder(t, readRec, &readResp)
	if readResp.Unread || readResp.MarkedUnread || readResp.LastReadMessageSequence == 0 {
		t.Fatalf("unexpected mark read response: %+v", readResp)
	}
	afterRead := getSummary(emptyDiscussion.ID)
	if *afterRead.Unread || *afterRead.MarkedUnread {
		t.Fatalf("expected cleared unread state: %+v", afterRead)
	}

	unreadRec := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+emptyDiscussion.ID.String()+"/unread", map[string]any{}, cookie, nil)
	if unreadRec.Code != http.StatusOK {
		t.Fatalf("mark unread after read: %d %s", unreadRec.Code, unreadRec.Body.String())
	}
	var unreadResp cardReadResponse
	decodeRecorder(t, unreadRec, &unreadResp)
	if !unreadResp.Unread || !unreadResp.MarkedUnread || unreadResp.LastReadMessageSequence != readResp.LastReadMessageSequence {
		t.Fatalf("mark unread should preserve read cursor: read=%+v unread=%+v", readResp, unreadResp)
	}
	afterReUnread := getSummary(emptyDiscussion.ID)
	if !*afterReUnread.Unread || !*afterReUnread.MarkedUnread || *afterReUnread.LastReadMessageSequence != readResp.LastReadMessageSequence {
		t.Fatalf("summary after mark unread mismatch: %+v", afterReUnread)
	}

	// 3. 多人（Human Source）隔离：另一位成员看到的是各自独立的已读/未读状态，不被当前用户手动标未读污染。
	otherHuman, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindHuman, "Teammate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.MarkCardRead(ctx, workspaceID, emptyDiscussion.ID, otherHuman.ID); err != nil {
		t.Fatalf("other human mark read: %v", err)
	}
	otherSummaries, err := dataStore.ListProjectCardSummaries(ctx, workspaceID, project.ID, otherHuman.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range otherSummaries {
		if item.ID == emptyDiscussion.ID {
			if item.MarkedUnread || item.Unread {
				t.Fatalf("other human source was polluted by manual unread: %+v", item)
			}
		}
		if item.ID == todoCard.ID {
			if item.MarkedUnread || item.Unread {
				t.Fatalf("other human source saw manual unread on empty todo card: %+v", item)
			}
		}
	}

	// 4. 清除已读复位 marked_unread=false。
	clearRec := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+emptyDiscussion.ID.String()+"/read", map[string]any{}, cookie, nil)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear unread: %d %s", clearRec.Code, clearRec.Body.String())
	}
	afterClear := getSummary(emptyDiscussion.ID)
	if *afterClear.Unread || *afterClear.MarkedUnread {
		t.Fatalf("mark read did not clear marked_unread: %+v", afterClear)
	}

	// 5. 跨 Workspace 权限隔离与未鉴权拦截。
	otherWS, err := dataStore.CreateWorkspace(ctx, "Other Workspace")
	if err != nil {
		t.Fatal(err)
	}
	otherWSSource, err := dataStore.CreateSource(ctx, otherWS.ID, domain.SourceKindHuman, "Other Owner")
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := dataStore.CreateProject(ctx, otherWS.ID, "Other Project", "")
	if err != nil {
		t.Fatal(err)
	}
	otherCard, err := dataStore.CreateDiscussion(ctx, otherWS.ID, otherProject.ID, otherWSSource.ID)
	if err != nil {
		t.Fatal(err)
	}
	crossWS := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+otherCard.ID.String()+"/unread", map[string]any{}, cookie, nil)
	if crossWS.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace mark unread = %d, want 404 (%s)", crossWS.Code, crossWS.Body.String())
	}
	unauth := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+emptyDiscussion.ID.String()+"/unread", map[string]any{}, nil, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mark unread = %d, want 401", unauth.Code)
	}
}
