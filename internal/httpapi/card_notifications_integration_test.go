package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

type spyPushSender struct {
	key      string
	sent     []pushPayload
	endpoints []string
}

func (s *spyPushSender) PublicKey() string {
	if s.key != "" {
		return s.key
	}
	return "test-vapid-public-key"
}

func (s *spyPushSender) Send(_ context.Context, endpoint, _, _, _ string, payload []byte) error {
	var msg pushPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		return err
	}
	s.endpoints = append(s.endpoints, endpoint)
	s.sent = append(s.sent, msg)
	return nil
}

func TestCardNotificationsMute_APIAndPushDelivery(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	var person identityResponse
	decodeRecorder(t, login, &person)
	workspaceID := uuid.MustParse(person.Workspace.ID)
	humanAccountID := uuid.MustParse(person.HumanAccountID)
	sourceID := uuid.MustParse(person.Source.ID)

	ctx := context.Background()
	project, err := dataStore.CreateProject(ctx, workspaceID, "Mute Test Project", "")
	if err != nil {
		t.Fatal(err)
	}

	mainDiscussion, _, err := dataStore.EnsureMainDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	discussion, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	todo, err := dataStore.CreateTodoCard(ctx, workspaceID, project.ID, sourceID, "测试 Todo", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ongoing, err := dataStore.CreateOngoingCard(ctx, workspaceID, project.ID, sourceID, "测试定时任务", "", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}

	// 1. 默认开启推送（notifications_muted = false），主讨论与各类型卡片均可正常静音与恢复
	for _, tc := range []struct {
		name string
		card uuid.UUID
	}{
		{name: "main_discussion", card: mainDiscussion.ID},
		{name: "discussion", card: discussion.ID},
		{name: "todo", card: todo.ID},
		{name: "ongoing", card: ongoing.ID},
	} {
		getRec := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+tc.card.String(), nil, cookie, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("%s get failed: %d %s", tc.name, getRec.Code, getRec.Body.String())
		}
		var detail cardDetailResponse
		decodeRecorder(t, getRec, &detail)
		if detail.Card.NotificationsMuted {
			t.Fatalf("%s notifications_muted default = true, want false", tc.name)
		}

		muteRec := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+tc.card.String()+"/notifications", map[string]any{"muted": true}, cookie, nil)
		if muteRec.Code != http.StatusOK {
			t.Fatalf("%s mute failed: %d %s", tc.name, muteRec.Code, muteRec.Body.String())
		}
		var mutedResp cardResponse
		decodeRecorder(t, muteRec, &mutedResp)
		if !mutedResp.NotificationsMuted {
			t.Fatalf("%s after mute notifications_muted = false, want true", tc.name)
		}
	}

	// 2. 校验项目卡片列表与主讨论接口均返回 notifications_muted = true
	listRec := doJSON(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID.String()+"/cards", nil, cookie, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list cards failed: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed []cardResponse
	decodeRecorder(t, listRec, &listed)
	if len(listed) != 4 {
		t.Fatalf("listed count = %d, want 4", len(listed))
	}
	for _, item := range listed {
		if !item.NotificationsMuted {
			t.Fatalf("listed card %s (%s) notifications_muted = false, want true", item.ID, item.Kind)
		}
	}

	mainRec := doJSON(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID.String()+"/main-discussion", nil, cookie, nil)
	if mainRec.Code != http.StatusOK {
		t.Fatalf("get main-discussion failed: %d %s", mainRec.Code, mainRec.Body.String())
	}
	var mainResp cardResponse
	decodeRecorder(t, mainRec, &mainResp)
	if !mainResp.NotificationsMuted {
		t.Fatalf("main-discussion notifications_muted = false, want true")
	}

	// 3. 验证隔离性：其他 Workspace 的卡片不可被当前 Workspace 静音
	if _, err := dataStore.SetCardNotificationsMuted(ctx, uuid.New(), discussion.ID, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace mute err = %v, want ErrNotFound", err)
	}

	// 4. 校验静音状态切换会改变 cardUpdate.Revision，但不算作新消息（不误发 Web Push）
	// 先把 discussion 与 mainDiscussion 取消静音用于测试不同分支
	unmuteDiscRec := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+discussion.ID.String()+"/notifications", map[string]any{"muted": false}, cookie, nil)
	if unmuteDiscRec.Code != http.StatusOK {
		t.Fatalf("unmute discussion failed: %d %s", unmuteDiscRec.Code, unmuteDiscRec.Body.String())
	}

	sender := &spyPushSender{}
	api := &API{pool: pool, store: dataStore, push: sender}

	if err := dataStore.SavePushSubscription(ctx, workspaceID, humanAccountID, store.PushSubscription{
		Endpoint:   "https://push.example/sub-1",
		P256dh:     "p256dh-key",
		AuthSecret: "auth-secret",
	}); err != nil {
		t.Fatal(err)
	}
	// 让订阅立刻进入空闲（后台关闭）状态
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, humanAccountID, "https://push.example/sub-1"); err != nil {
		t.Fatal(err)
	}

	// 首次巡检仅建立 push_card_revisions 基线，不发推送
	if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("initial baseline sent = %d, want 0", len(sender.sent))
	}

	updatesBeforeMute, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var beforeDiscUpdate cardUpdate
	for _, u := range updatesBeforeMute {
		if u.ID == discussion.ID.String() {
			beforeDiscUpdate = u
		}
	}
	if beforeDiscUpdate.NotificationsMuted {
		t.Fatalf("discussion update should not be muted before test")
	}

	// 切换静音：revision 必须变更以同步多端，但 deliverWorkspacePushes 不应推送任何消息
	remuteRec := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+discussion.ID.String()+"/notifications", map[string]any{"muted": true}, cookie, nil)
	if remuteRec.Code != http.StatusOK {
		t.Fatalf("remute failed: %d %s", remuteRec.Code, remuteRec.Body.String())
	}
	updatesAfterMute, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var afterDiscUpdate cardUpdate
	for _, u := range updatesAfterMute {
		if u.ID == discussion.ID.String() {
			afterDiscUpdate = u
		}
	}
	if !afterDiscUpdate.NotificationsMuted {
		t.Fatalf("afterDiscUpdate.NotificationsMuted = false, want true")
	}
	if afterDiscUpdate.Revision == beforeDiscUpdate.Revision {
		t.Fatalf("toggling mute should advance revision for multi-tab sync")
	}
	if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("toggling mute must not send push, got %d", len(sender.sent))
	}

	// 5. 静音期间主聊天与普通讨论产生新消息（模型回复）：不发送 Web Push，但推进 push_card_revisions 快照
	runner, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatal(err)
	}
	appendAgentReply := func(cardID uuid.UUID, text string) {
		t.Helper()
		turn, err := dataStore.CreateHumanTurn(ctx, workspaceID, cardID, sourceID, "trigger: "+text, uuid.NewString(), domain.DispatchModeEnqueue)
		if err != nil {
			t.Fatal(err)
		}
		claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now().UTC(), time.Minute)
		if err != nil || claim == nil || claim.Turn.ID != turn.Turn.ID {
			t.Fatalf("claim failed: %v %+v", err, claim)
		}
		if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
			WorkspaceID:    workspaceID,
			CardID:         cardID,
			AttemptID:      claim.Attempt.ID,
			RunnerSourceID: runner.ID,
			Message:        store.ReplyMessage{Type: "text", Content: text, EndTurn: true},
			IdempotencyKey: uuid.NewString(),
			FinishOnEnd:    true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendAgentReply(mainDiscussion.ID, "主聊天静音期间回复")
	appendAgentReply(discussion.ID, "讨论静音期间回复")

	if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("muted cards must not trigger web push, got %+v", sender.sent)
	}

	// 检查 snapshot 已被推进到最新 EventSequence
	snapshots, err := dataStore.ListPushCardSnapshots(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	updatesNow, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	latestRevByCard := make(map[string]cardUpdate, len(updatesNow))
	for _, u := range updatesNow {
		latestRevByCard[u.ID] = u
	}
	for _, snap := range snapshots {
		if snap.CardID == discussion.ID || snap.CardID == mainDiscussion.ID {
			want := latestRevByCard[snap.CardID.String()]
			if snap.Revision != want.Revision || snap.EventSequence != want.EventSequence {
				t.Fatalf("muted snapshot not advanced for %s: got rev=%s seq=%s, want rev=%s seq=%s",
					snap.CardID, snap.Revision, snap.EventSequence, want.Revision, want.EventSequence)
			}
		}
	}

	// 6. 恢复推送后：不会补推静音期间的历史消息
	for _, id := range []uuid.UUID{mainDiscussion.ID, discussion.ID} {
		rec := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+id.String()+"/notifications", map[string]any{"muted": false}, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("unmute %s failed: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("unmuting must not replay old messages, got %+v", sender.sent)
	}

	// 7. 恢复推送后再产生新消息：正常发送 Web Push
	appendAgentReply(mainDiscussion.ID, "主聊天恢复后新回复")
	if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0].CardID != mainDiscussion.ID.String() || sender.sent[0].Body != "主聊天恢复后新回复" {
		t.Fatalf("expected 1 push for unmuted main discussion, got %+v", sender.sent)
	}
}
