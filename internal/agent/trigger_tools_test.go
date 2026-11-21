package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/session"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestTriggerTools_ToolGuardClassification(t *testing.T) {
	if !isSafeReadOnlyTool("cycledock_list_triggers", nil) {
		t.Fatalf("expected cycledock_list_triggers to be classified as safe read-only")
	}
	for _, writeTool := range []string{
		"cycledock_create_trigger",
		"cycledock_update_trigger",
		"cycledock_delete_trigger",
	} {
		if isSafeReadOnlyTool(writeTool, nil) {
			t.Fatalf("expected %q NOT to be in safeReadOnlyToolNames", writeTool)
		}
	}
}

func TestTriggerTools_CardKindMountingBoundaries(t *testing.T) {
	hasTool := func(kind domain.CardKind, toolName string) bool {
		tools, err := buildADKTools(attemptToolRuntime{
			context: store.AgentCardContext{
				CardKind: kind,
			},
		})
		if err != nil {
			t.Fatalf("buildADKTools(%s): %v", kind, err)
		}
		for _, item := range tools {
			if item.Name() == toolName {
				return true
			}
		}
		return false
	}

	for _, kind := range []domain.CardKind{domain.CardKindDiscussion, domain.CardKindTodo, domain.CardKindOngoing} {
		if !hasTool(kind, "cycledock_list_triggers") {
			t.Fatalf("expected cycledock_list_triggers to be mounted on %s card", kind)
		}
	}
	for _, writeTool := range []string{"cycledock_create_trigger", "cycledock_update_trigger", "cycledock_delete_trigger"} {
		if !hasTool(domain.CardKindDiscussion, writeTool) {
			t.Fatalf("expected %s to be mounted on discussion card", writeTool)
		}
		if !hasTool(domain.CardKindTodo, writeTool) {
			t.Fatalf("expected %s to be mounted on todo card", writeTool)
		}
		if hasTool(domain.CardKindOngoing, writeTool) {
			t.Fatalf("expected %s NOT to be mounted on ongoing card", writeTool)
		}
	}
}

func TestTriggerTools_WebhookURLConstruction(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	if got := triggerWebhookURL("", id); got != "/api/v1/triggers/11111111-2222-3333-4444-555555555555/webhook" {
		t.Fatalf("expected relative path when PublicBaseURL is empty, got %q", got)
	}
	if got := triggerWebhookURL("https://dock.example.com///", id); got != "https://dock.example.com/api/v1/triggers/11111111-2222-3333-4444-555555555555/webhook" {
		t.Fatalf("expected full webhook url with trimmed slash, got %q", got)
	}
}

func TestBuiltInTriggerTools_CRUDDefaultsDedupAndSecretRedaction(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	if err := dataStore.ConfigureTriggerEncryption("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="); err != nil {
		t.Fatalf("configure trigger encryption: %v", err)
	}
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Trigger Tools Workspace")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	todoCard, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "配置 GitHub Webhook", "自动建触发器并配置仓库", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	var step atomic.Int32
	var createdID string
	var createdSecret string
	var modelSawSecret bool
	var listContainedSecret bool

	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !requestIncludesTool(body, "cycledock_list_triggers") ||
			!requestIncludesTool(body, "cycledock_create_trigger") ||
			!requestIncludesTool(body, "cycledock_update_trigger") ||
			!requestIncludesTool(body, "cycledock_delete_trigger") {
			t.Errorf("expected all four trigger tools on todo card")
		}

		w.Header().Set("Content-Type", "application/json")
		switch step.Add(1) {
		case 1:
			// 1. 最小参数创建 GitHub 触发器（验证默认值与完整 URL）
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "create-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "cycledock_create_trigger",
					Arguments: `{"provider":"github","idempotency_key":"gh-ci-1"}`,
				},
			}})
		case 2:
			// 校验模型在同一轮立即拿到了完整 webhook_url 和一次性明文 webhook_secret
			for _, msg := range body.Messages {
				if msg.Role != "tool" {
					continue
				}
				var parsed map[string]any
				if err := json.Unmarshal([]byte(msg.Content), &parsed); err != nil {
					t.Logf("unmarshal tool msg failed: %v, content=%s", err, msg.Content)
					continue
				}
				t.Logf("step 2 tool msg (%s): %s", msg.ToolCallID, msg.Content)
				if s, ok := parsed["webhook_secret"].(string); ok && s != "" && s != agentdata.Redacted {
					createdSecret = s
					modelSawSecret = true
				}
				if trig, ok := parsed["trigger"].(map[string]any); ok {
					if id, ok := trig["id"].(string); ok {
						createdID = id
					}
					if urlVal, _ := trig["webhook_url"].(string); !strings.HasPrefix(urlVal, "https://cycledock.example.test/api/v1/triggers/") {
						t.Errorf("expected full public webhook_url, got %q", urlVal)
					}
					if trig["routing_mode"] != "match_watchers" || trig["dispatch_mode"] != "enqueue" || trig["enabled"] != true {
						t.Errorf("unexpected default trigger fields: %#v", trig)
					}
				}
			}
			// 2. 用相同 idempotency_key 再次创建应去重复用，并列出触发器验证不含明文密钥
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "create-dup",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_create_trigger",
						Arguments: `{"provider":"github","idempotency_key":"gh-ci-1"}`,
					},
				},
				{
					ID:   "list-1",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_list_triggers",
						Arguments: `{"provider":"github"}`,
					},
				},
			})
		case 3:
			for _, msg := range body.Messages {
				if msg.Role == "tool" && msg.ToolCallID == "list-1" {
					if createdSecret != "" && strings.Contains(msg.Content, createdSecret) {
						listContainedSecret = true
					}
				}
			}
			// 3. 更新为禁用并改名
			updArgs, _ := json.Marshal(map[string]any{
				"trigger_id": createdID,
				"name":       "GitHub CI（已暂停）",
				"enabled":    false,
			})
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "upd-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "cycledock_update_trigger",
					Arguments: string(updArgs),
				},
			}})
		case 4:
			// 4. 删除该触发器并结束本轮
			delArgs, _ := json.Marshal(map[string]any{
				"trigger_id": createdID,
			})
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "del-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "cycledock_delete_trigger",
					Arguments: string(delArgs),
				},
			}})
		case 5:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "reply-end",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"触发器生命周期验证完成。","end_turn":true,"idempotency_key":"trigger-tools-reply"}`,
				},
			}})
		default:
			t.Errorf("unexpected step %d", step.Load())
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "trigger-test-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, todoCard.ID, human.ID, "请测试触发器工具", "req-trigger-tools", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	sessionService := agentsession.NewPostgresService(pool)
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		PublicBaseURL:     "https://cycledock.example.test",
		SessionService:    sessionService,
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatalf("RunAttempt failed: %v", err)
	}

	if !modelSawSecret || createdSecret == "" {
		t.Fatalf("expected model to receive one-time plaintext webhook_secret during creation turn")
	}
	if listContainedSecret {
		t.Fatalf("cycledock_list_triggers must NEVER expose plaintext webhook_secret")
	}

	// 验证最终已被删除
	remaining, err := dataStore.ListTriggers(ctx, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected trigger to be deleted, found %d remaining", len(remaining))
	}

	// 验证 PostgreSQL 会话持久化层已经将 webhook_secret 脱敏为 [REDACTED]，绝不落库明文
	appName, userID, sessionID := agentsession.Identity(workspace.ID, todoCard.ID)
	loadedSession, err := sessionService.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("load persisted session: %v", err)
	}
	foundRedacted := false
	for evt := range loadedSession.Session.Events().All() {
		rawEvt, _ := json.Marshal(evt)
		text := string(rawEvt)
		if strings.Contains(text, createdSecret) {
			t.Fatalf("plaintext webhook_secret leaked into persisted agent_session_events!")
		}
		if strings.Contains(text, `"webhook_secret":"[REDACTED]"`) {
			foundRedacted = true
		}
	}
	if !foundRedacted {
		t.Fatalf("expected persisted session events to contain redacted webhook_secret")
	}
}

func TestBuiltInTriggerTools_OngoingCardCannotWrite(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Ongoing Trigger Guard Workspace")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	ongoingCard, err := dataStore.CreateOngoingCard(ctx, workspace.ID, project.ID, human.ID, "巡检卡", "持续巡检", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}

	rt := attemptToolRuntime{
		store: dataStore,
		context: store.AgentCardContext{
			WorkspaceID:    workspace.ID,
			ProjectID:      project.ID,
			CardID:         ongoingCard.ID,
			CardKind:       domain.CardKindOngoing,
			RunnerSourceID: human.ID,
		},
	}

	if _, err := rt.createTriggerTool(nil, map[string]any{"provider": "github"}); err == nil {
		t.Fatalf("expected ongoing card createTriggerTool to fail")
	}
	if _, err := rt.updateTriggerTool(nil, map[string]any{"trigger_id": uuid.NewString()}); err == nil {
		t.Fatalf("expected ongoing card updateTriggerTool to fail")
	}
	if _, err := rt.deleteTriggerTool(nil, map[string]any{"trigger_id": uuid.NewString()}); err == nil {
		t.Fatalf("expected ongoing card deleteTriggerTool to fail")
	}
}
