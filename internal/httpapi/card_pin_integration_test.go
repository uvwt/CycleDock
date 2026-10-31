package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCardPin_置顶与取消置顶(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	var person identityResponse
	decodeRecorder(t, login, &person)
	workspaceID := uuid.MustParse(person.Workspace.ID)
	sourceID := uuid.MustParse(person.Source.ID)

	ctx := context.Background()
	project, err := dataStore.CreateProject(ctx, workspaceID, "Pin Test Project", "", "")
	if err != nil {
		t.Fatal(err)
	}

	cardA, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	cardB, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	_ = cardB

	// 1. 置顶 cardA
	pinRec := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+cardA.ID.String()+"/pin", map[string]any{"pinned": true}, cookie, nil)
	if pinRec.Code != http.StatusOK {
		t.Fatalf("置顶请求失败: %d %s", pinRec.Code, pinRec.Body.String())
	}
	var pinnedCard cardResponse
	decodeRecorder(t, pinRec, &pinnedCard)
	if pinnedCard.PinnedAt == nil {
		t.Fatalf("置顶后 pinned_at 必须非空")
	}

	// 2. 查询卡片摘要列表，cardA 必须排在 cardB 前面（因为 cardA 置顶）
	listRec := doJSON(t, handler, http.MethodGet, "/api/v1/projects/"+project.ID.String()+"/cards", nil, cookie, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("获取卡片列表失败: %d %s", listRec.Code, listRec.Body.String())
	}
	var cards []cardResponse
	decodeRecorder(t, listRec, &cards)
	if len(cards) != 2 || cards[0].ID != cardA.ID.String() {
		t.Fatalf("置顶卡片 cardA 必须排在第一位: got=%+v", cards)
	}

	// 3. 取消置顶 cardA
	unpinRec := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+cardA.ID.String()+"/pin", map[string]any{"pinned": false}, cookie, nil)
	if unpinRec.Code != http.StatusOK {
		t.Fatalf("取消置顶失败: %d %s", unpinRec.Code, unpinRec.Body.String())
	}
	var unpinnedCard cardResponse
	decodeRecorder(t, unpinRec, &unpinnedCard)
	if unpinnedCard.PinnedAt != nil {
		t.Fatalf("取消置顶后 pinned_at 必须为空: %+v", unpinnedCard.PinnedAt)
	}

	_ = pool
}
