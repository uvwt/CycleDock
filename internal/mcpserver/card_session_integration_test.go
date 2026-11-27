package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	cycledockoauth "github.com/uvwt/CycleDock/internal/oauth"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/testdb"
)

type testBearerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (rt testBearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+rt.token)
	return rt.base.RoundTrip(clone)
}

func setupTestMCPClient(
	t *testing.T,
	pool *sqldb.DB,
	dataStore *store.Store,
	sessionService *agentsession.Service,
	identity auth.Identity,
) *mcp.ClientSession {
	t.Helper()

	var serverURL string
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	serverURL = ts.URL

	oauthService, err := cycledockoauth.New(pool, cycledockoauth.Config{PublicURL: serverURL})
	if err != nil {
		t.Fatalf("create oauth service: %v", err)
	}
	handlers := New(dataStore, sessionService, oauthService, nil)
	mux.Handle("/mcp", handlers.MCP)

	ctx := context.Background()
	oauthClient, err := oauthService.RegisterClient(ctx, "MCP Test Client", []string{"http://127.0.0.1:65530/cb"})
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	code, err := oauthService.Authorize(ctx, identity, cycledockoauth.AuthorizeRequest{
		ClientID:      oauthClient.ID,
		RedirectURI:   oauthClient.RedirectURIs[0],
		CodeChallenge: challenge,
		Scope:         cycledockoauth.ScopeMCP,
		Resource:      oauthService.Resource(),
	})
	if err != nil {
		t.Fatalf("authorize oauth client: %v", err)
	}
	tokens, err := oauthService.ExchangeCode(ctx, cycledockoauth.CodeTokenRequest{
		Code:         code.Code,
		ClientID:     oauthClient.ID,
		RedirectURI:  oauthClient.RedirectURIs[0],
		CodeVerifier: verifier,
		Resource:     oauthService.Resource(),
	})
	if err != nil {
		t.Fatalf("exchange oauth code: %v", err)
	}

	httpClient := &http.Client{
		Transport: testBearerRoundTripper{
			base:  ts.Client().Transport,
			token: tokens.AccessToken,
		},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-session-integration", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   serverURL + "/mcp",
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		t.Fatalf("connect official MCP client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func appendSessionEvents(
	t *testing.T,
	sessionService *agentsession.Service,
	workspaceID, cardID uuid.UUID,
	events ...*session.Event,
) {
	t.Helper()
	ctx := context.Background()
	appName, userID, sessionID := agentsession.Identity(workspaceID, cardID)
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("create ADK session: %v", err)
	}
	for _, evt := range events {
		if err := sessionService.AppendEvent(ctx, created.Session, evt); err != nil {
			t.Fatalf("append ADK session event %s: %v", evt.ID, err)
		}
	}
}

func callToolJSON[Out any](t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (Out, string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%s) transport/protocol error: %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	var out Out
	if !res.IsError {
		if res.StructuredContent != nil {
			raw, err := json.Marshal(res.StructuredContent)
			if err != nil {
				t.Fatalf("marshal structured content for %s: %v", name, err)
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("unmarshal structured content for %s: %v (raw=%s)", name, err, raw)
			}
		} else if text != "" {
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatalf("unmarshal text content for %s: %v (raw=%s)", name, err, text)
			}
		}
	}
	return out, text, res.IsError
}

func TestMCPCardSessionToolsIntegration(t *testing.T) {
	testdb.ForEach(t, func(t *testing.T, pool *sqldb.DB) {
		ctx := context.Background()
		dataStore := store.New(pool)
		sessionService := agentsession.NewService(pool)

		// 通过真实人类认证创建 Workspace A 及其主成员身份（包含 Discussion / Todo / Ongoing 三类卡片）
		humanAuth, err := auth.New(pool, auth.Config{AdminPassword: "mcp-session-test-password"})
		if err != nil {
			t.Fatalf("create human auth: %v", err)
		}
		humanSession, err := humanAuth.Login(ctx, "mcp-session-test-password")
		if err != nil {
			t.Fatalf("human login: %v", err)
		}
		wsAID := humanSession.Identity.WorkspaceID
		humanAID := humanSession.Identity.SourceID
		humanAName := humanSession.Identity.SourceName
		agentA, err := dataStore.CreateSource(ctx, wsAID, domain.SourceKindCycleAgent, "Cycle Agent")
		if err != nil {
			t.Fatal(err)
		}
		projectA, err := dataStore.CreateProject(ctx, wsAID, "Project A", "")
		if err != nil {
			t.Fatal(err)
		}

		discussionCard, err := dataStore.CreateDiscussion(ctx, wsAID, projectA.ID, humanAID)
		if err != nil {
			t.Fatal(err)
		}
		todoCard, err := dataStore.CreateTodoCard(ctx, wsAID, projectA.ID, humanAID, "Todo Integration", "verify mcp session", uuid.NullUUID{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		ongoingCard, err := dataStore.CreateOngoingCard(ctx, wsAID, projectA.ID, humanAID, "Ongoing Monitor", "watch health", uuid.NullUUID{})
		if err != nil {
			t.Fatal(err)
		}
		emptyTodoCard, err := dataStore.CreateTodoCard(ctx, wsAID, projectA.ID, humanAID, "Empty Todo", "no runs yet", uuid.NullUUID{}, 1)
		if err != nil {
			t.Fatal(err)
		}

		// 1) 为 Discussion 写入对话与底层执行记录
		if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
			WorkspaceID: wsAID, CardID: discussionCard.ID, AuthorSourceID: humanAID,
			Content: "先梳理下接口方案", IdempotencyKey: "disc-msg-1", DispatchMode: domain.DispatchModeEnqueue,
		}); err != nil {
			t.Fatal(err)
		}
		discClaim, err := dataStore.ClaimCardTurnAttempt(ctx, wsAID, discussionCard.ID, agentA.ID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
			WorkspaceID: wsAID, CardID: discussionCard.ID, AttemptID: discClaim.Attempt.ID, RunnerSourceID: agentA.ID,
			Message: store.ReplyMessage{Type: "text", Content: "方案已整理完毕", EndTurn: true}, IdempotencyKey: "disc-reply-1", FinishOnEnd: true,
		}); err != nil {
			t.Fatal(err)
		}
		appendSessionEvents(t, sessionService, wsAID, discussionCard.ID,
			&session.Event{
				ID: "disc-evt-1", InvocationID: "inv-disc-1", Author: "cycle_agent", Timestamp: time.Now().UTC().Add(-2 * time.Second),
				LLMResponse: model.LLMResponse{Content: genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromText("discussion-keyword-alpha verified"),
				}, genai.RoleModel)},
			},
		)

		// 2) 为 Todo 写入评论、choice/图片气泡、status_change 杂项，以及含大 tool_results 和 inline_images 的底层过程记录
		if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
			WorkspaceID: wsAID, CardID: todoCard.ID, AuthorSourceID: humanAID,
			Content: "请执行集成排查", IdempotencyKey: "todo-msg-1", DispatchMode: domain.DispatchModeEnqueue,
		}); err != nil {
			t.Fatal(err)
		}
		// 主动写入 status_change，验证 simple 模式过滤纯状态流转。
		if err := dataStore.MoveTodo(ctx, wsAID, todoCard.ID, agentA.ID, domain.TodoStateWorking); err != nil {
			t.Fatal(err)
		}
		todoClaim, err := dataStore.ClaimCardTurnAttempt(ctx, wsAID, todoCard.ID, agentA.ID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
			WorkspaceID: wsAID, CardID: todoCard.ID, AttemptID: todoClaim.Attempt.ID, RunnerSourceID: agentA.ID,
			Message: store.ReplyMessage{
				Type:    "text",
				Content: "排查截图如下",
				Images:  []string{"https://cdn.example.com/todo-shot.png"},
				EndTurn: false,
			},
			IdempotencyKey: "todo-reply-1",
		}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
			WorkspaceID: wsAID, CardID: todoCard.ID, AttemptID: todoClaim.Attempt.ID, RunnerSourceID: agentA.ID,
			Message: store.ReplyMessage{
				Type:    "choice",
				Content: "需要确认部署方式",
				EndTurn: true,
				Widget: &store.ChoiceWidget{
					Question: "是否现在发布？",
					Options: []store.ChoiceOption{
						{Value: "yes", Label: "立即发布"},
						{Value: "no", Label: "暂缓"},
					},
				},
			},
			IdempotencyKey: "todo-reply-2",
			FinishOnEnd:    true,
		}); err != nil {
			t.Fatal(err)
		}
		hugeOutput := "marker-todo-huge-result " + strings.Repeat("LOG_LINE_0123456789\n", 400)
		appendSessionEvents(t, sessionService, wsAID, todoCard.ID,
			&session.Event{
				ID: "todo-evt-call", InvocationID: "inv-todo-1", Author: "cycle_agent", Timestamp: time.Now().UTC().Add(-3 * time.Second),
				LLMResponse: model.LLMResponse{Content: genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromFunctionCall("exec_command", map[string]any{"cmd": "go test ./..."}),
				}, genai.RoleModel)},
			},
			&session.Event{
				ID: "todo-evt-result", InvocationID: "inv-todo-1", Author: "cycle_agent", Timestamp: time.Now().UTC().Add(-2 * time.Second),
				LLMResponse: model.LLMResponse{Content: genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromFunctionResponse("exec_command", map[string]any{
						"stdout":        hugeOutput,
						"inline_images": []any{map[string]any{"mime_type": "image/png", "data": "BASE64_BINARY"}},
					}),
				}, genai.RoleUser)},
			},
		)

		// 3) 为 Ongoing 写入巡检记录、checkin_ack 杂项与历史执行事件
		if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
			WorkspaceID: wsAID, CardID: ongoingCard.ID, AuthorSourceID: humanAID,
			Content: "开始每小时巡检", IdempotencyKey: "ong-msg-1", DispatchMode: domain.DispatchModeEnqueue,
		}); err != nil {
			t.Fatal(err)
		}
		ongClaim, err := dataStore.ClaimCardTurnAttempt(ctx, wsAID, ongoingCard.ID, agentA.ID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		warnStatus := domain.CheckinWarning
		if _, _, err := dataStore.AddAttemptEvent(ctx, store.AttemptEventParams{
			WorkspaceID: wsAID, CardID: ongoingCard.ID, AttemptID: ongClaim.Attempt.ID, RunnerSourceID: agentA.ID,
			Type: domain.EventTypeCheckin, Content: "磁盘使用率 88%", Checkin: &warnStatus, IdempotencyKey: "ong-checkin-1",
		}); err != nil {
			t.Fatal(err)
		}
		if err := dataStore.AcknowledgeCheckin(ctx, wsAID, ongoingCard.ID, humanAID, "known"); err != nil {
			t.Fatal(err)
		}
		appendSessionEvents(t, sessionService, wsAID, ongoingCard.ID,
			&session.Event{
				ID: "ong-evt-1", InvocationID: "inv-ong-1", Author: "cycle_agent", Timestamp: time.Now().UTC().Add(-1 * time.Second),
				LLMResponse: model.LLMResponse{Content: genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromText("ongoing-disk-metric-88pct recorded"),
				}, genai.RoleModel)},
			},
		)

		// 准备 Workspace B 用于跨 Workspace 越权测试
		wsB, err := dataStore.CreateWorkspace(ctx, "Workspace B")
		if err != nil {
			t.Fatal(err)
		}
		humanB, err := dataStore.CreateSource(ctx, wsB.ID, domain.SourceKindHuman, "Bob")
		if err != nil {
			t.Fatal(err)
		}
		projectB, err := dataStore.CreateProject(ctx, wsB.ID, "Project B", "")
		if err != nil {
			t.Fatal(err)
		}
		foreignCard, err := dataStore.CreateTodoCard(ctx, wsB.ID, projectB.ID, humanB.ID, "Foreign Secret Card", "secret", uuid.NullUUID{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
			WorkspaceID: wsB.ID, CardID: foreignCard.ID, AuthorSourceID: humanB.ID,
			Content: "Top secret message", IdempotencyKey: "foreign-msg-1", DispatchMode: domain.DispatchModeEnqueue,
		}); err != nil {
			t.Fatal(err)
		}
		appendSessionEvents(t, sessionService, wsB.ID, foreignCard.ID,
			&session.Event{
				ID: "foreign-evt-1", InvocationID: "inv-foreign-1", Author: "cycle_agent", Timestamp: time.Now().UTC(),
				LLMResponse: model.LLMResponse{Content: genai.NewContentFromParts([]*genai.Part{
					genai.NewPartFromText("foreign-secret-token-xyz"),
				}, genai.RoleModel)},
			},
		)

		clientA := setupTestMCPClient(t, pool, dataStore, sessionService, humanSession.Identity)

		t.Run("simple mode works for discussion todo ongoing and filters status noise", func(t *testing.T) {
			// Discussion（默认 mode=simple）
			discOut, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientA, "get_card_session", map[string]any{
				"card_id": discussionCard.ID.String(),
			})
			if isErr {
				t.Fatalf("discussion simple error: %s", errText)
			}
			if discOut.Mode != "simple" || discOut.CardKind != "discussion" || discOut.Count != 2 || discOut.Truncated {
				t.Fatalf("unexpected discussion simple output: %+v", discOut)
			}
			if discOut.Bubbles[0].Author != humanAName || discOut.Bubbles[1].Content != "方案已整理完毕" {
				t.Fatalf("unexpected discussion bubbles: %+v", discOut.Bubbles)
			}

			// Todo（验证图片、choice 与过滤 status_change）
			todoOut, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientA, "get_card_session", map[string]any{
				"card_id": todoCard.ID.String(),
				"mode":    "simple",
			})
			if isErr {
				t.Fatalf("todo simple error: %s", errText)
			}
			if todoOut.CardKind != "todo" || todoOut.Count != 3 {
				t.Fatalf("expected 3 todo bubbles (status_change filtered), got %+v", todoOut)
			}
			if len(todoOut.Bubbles[1].Images) != 1 || todoOut.Bubbles[1].Images[0] != "https://cdn.example.com/todo-shot.png" {
				t.Fatalf("expected todo image bubble, got %+v", todoOut.Bubbles[1])
			}
			if !strings.Contains(todoOut.Bubbles[2].Content, "是否现在发布？") || !strings.Contains(todoOut.Bubbles[2].Content, "1. 立即发布") {
				t.Fatalf("expected formatted choice bubble, got %+v", todoOut.Bubbles[2])
			}
			for _, b := range todoOut.Bubbles {
				if strings.Contains(b.Content, "todo -> working") {
					t.Fatalf("status_change noise leaked into simple bubbles: %+v", todoOut.Bubbles)
				}
			}

			// Todo limit 截断
			limitedOut, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientA, "get_card_session", map[string]any{
				"card_id": todoCard.ID.String(),
				"mode":    "simple",
				"limit":   1,
			})
			if isErr || limitedOut.Count != 1 || !strings.Contains(limitedOut.Bubbles[0].Content, "是否现在发布？") {
				t.Fatalf("expected last 1 bubble, got out=%+v err=%s", limitedOut, errText)
			}

			// Ongoing（验证 checkin 保留、checkin_ack 过滤）
			ongOut, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientA, "get_card_session", map[string]any{
				"card_id": ongoingCard.ID.String(),
				"mode":    "simple",
			})
			if isErr {
				t.Fatalf("ongoing simple error: %s", errText)
			}
			if ongOut.CardKind != "ongoing" || ongOut.Count != 2 {
				t.Fatalf("expected 2 ongoing bubbles (checkin_ack filtered), got %+v", ongOut)
			}
			if ongOut.Bubbles[1].Content != "磁盘使用率 88%" {
				t.Fatalf("unexpected ongoing checkin bubble: %+v", ongOut.Bubbles[1])
			}
		})

		t.Run("full mode supports all card kinds truncates huge tool_results and keeps image placeholder", func(t *testing.T) {
			discFull, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientA, "get_card_session", map[string]any{
				"card_id": discussionCard.ID.String(),
				"mode":    "full",
			})
			if isErr || discFull.Mode != "full" || discFull.Count != 1 || discFull.Truncated {
				t.Fatalf("unexpected discussion full output: %+v err=%s", discFull, errText)
			}
			if !strings.Contains(discFull.Rounds[0].Text, "discussion-keyword-alpha") {
				t.Fatalf("unexpected discussion full round: %+v", discFull.Rounds[0])
			}

			ongFull, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientA, "get_card_session", map[string]any{
				"card_id": ongoingCard.ID.String(),
				"mode":    "full",
			})
			if isErr || ongFull.Count != 1 || !strings.Contains(ongFull.Rounds[0].Text, "ongoing-disk-metric-88pct") {
				t.Fatalf("unexpected ongoing full output: %+v err=%s", ongFull, errText)
			}

			todoFull, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientA, "get_card_session", map[string]any{
				"card_id": todoCard.ID.String(),
				"mode":    "full",
			})
			if isErr {
				t.Fatalf("todo full error: %s", errText)
			}
			if todoFull.Count != 2 || !todoFull.Truncated {
				t.Fatalf("expected 2 rounds with truncated=true, got count=%d truncated=%v", todoFull.Count, todoFull.Truncated)
			}
			if len(todoFull.Rounds[0].ToolCalls) != 1 || todoFull.Rounds[0].ToolCalls[0]["name"] != "exec_command" {
				t.Fatalf("expected tool_calls in round 0, got %+v", todoFull.Rounds[0])
			}
			resultJSON, err := json.Marshal(todoFull.Rounds[1].ToolResults)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(resultJSON), truncatedSuffix) {
				t.Fatalf("expected truncated tool_results, got: %s", resultJSON)
			}
			if !strings.Contains(string(resultJSON), imagePayloadOmittedPlaceholder) {
				t.Fatalf("expected sanitized inline_images placeholder, got: %s", resultJSON)
			}
			if strings.Contains(string(resultJSON), "BASE64_BINARY") {
				t.Fatalf("raw image base64 leaked into full session output: %s", resultJSON)
			}
		})

		t.Run("full mode on empty session returns helpful note suggesting simple mode", func(t *testing.T) {
			emptyOut, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientA, "get_card_session", map[string]any{
				"card_id": emptyTodoCard.ID.String(),
				"mode":    "full",
			})
			if isErr {
				t.Fatalf("empty full error: %s", errText)
			}
			if emptyOut.Count != 0 || emptyOut.Truncated {
				t.Fatalf("expected 0 rounds untruncated, got %+v", emptyOut)
			}
			if !strings.Contains(emptyOut.Note, `mode="simple"`) {
				t.Fatalf("expected note suggesting mode=simple, got %q", emptyOut.Note)
			}
		})

		t.Run("search_card_session searches discussion todo ongoing and handles misses", func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				cardID uuid.UUID
				query  string
				want   string
			}{
				{"discussion", discussionCard.ID, "discussion-keyword-alpha", "discussion-keyword-alpha"},
				{"todo", todoCard.ID, "marker-todo-huge-result", "marker-todo-huge-result"},
				{"ongoing", ongoingCard.ID, "ongoing-disk-metric-88pct", "ongoing-disk-metric-88pct"},
			} {
				out, errText, isErr := callToolJSON[SearchCardSessionOutput](t, clientA, "search_card_session", map[string]any{
					"card_id": tc.cardID.String(),
					"query":   tc.query,
					"limit":   5,
				})
				if isErr {
					t.Fatalf("search %s failed: %s", tc.name, errText)
				}
				if out.Count != 1 || !strings.Contains(out.Hits[0].Content, tc.want) {
					t.Fatalf("search %s unexpected output: %+v", tc.name, out)
				}
			}

			missOut, errText, isErr := callToolJSON[SearchCardSessionOutput](t, clientA, "search_card_session", map[string]any{
				"card_id": todoCard.ID.String(),
				"query":   "non-existent-keyword-999",
			})
			if isErr || missOut.Count != 0 || missOut.Note == "" {
				t.Fatalf("expected empty search with note, got out=%+v err=%s", missOut, errText)
			}
		})

		t.Run("parameter validation rejects invalid inputs", func(t *testing.T) {
			cases := []struct {
				tool string
				args map[string]any
			}{
				{"get_card_session", map[string]any{"card_id": "not-a-uuid"}},
				{"get_card_session", map[string]any{"card_id": todoCard.ID.String(), "mode": "verbose"}},
				{"get_card_session", map[string]any{"card_id": todoCard.ID.String(), "limit": -1}},
				{"get_card_session", map[string]any{"card_id": todoCard.ID.String(), "limit": 201}},
				{"search_card_session", map[string]any{"card_id": "bad-id", "query": "abc"}},
				{"search_card_session", map[string]any{"card_id": todoCard.ID.String(), "query": "   "}},
				{"search_card_session", map[string]any{"card_id": todoCard.ID.String(), "query": strings.Repeat("q", 241)}},
				{"search_card_session", map[string]any{"card_id": todoCard.ID.String(), "query": "ok", "limit": -1}},
				{"search_card_session", map[string]any{"card_id": todoCard.ID.String(), "query": "ok", "limit": 13}},
			}
			for _, tc := range cases {
				_, errText, isErr := callToolJSON[map[string]any](t, clientA, tc.tool, tc.args)
				if !isErr || !strings.Contains(errText, "INVALID_REQUEST:") {
					t.Fatalf("%s(%+v): expected INVALID_REQUEST error, got isErr=%v text=%q", tc.tool, tc.args, isErr, errText)
				}
			}
		})

		t.Run("cross workspace access is denied for simple full and search", func(t *testing.T) {
			for _, tc := range []struct {
				tool string
				args map[string]any
			}{
				{"get_card_session", map[string]any{"card_id": foreignCard.ID.String(), "mode": "simple"}},
				{"get_card_session", map[string]any{"card_id": foreignCard.ID.String(), "mode": "full"}},
				{"search_card_session", map[string]any{"card_id": foreignCard.ID.String(), "query": "foreign-secret-token-xyz"}},
			} {
				_, errText, isErr := callToolJSON[map[string]any](t, clientA, tc.tool, tc.args)
				if !isErr || !strings.Contains(errText, "NOT_FOUND:") {
					t.Fatalf("cross-workspace %s(%+v): expected NOT_FOUND, got isErr=%v text=%q", tc.tool, tc.args, isErr, errText)
				}
			}
		})

		t.Run("nil sessionService keeps simple working and returns clear error for full and search", func(t *testing.T) {
			clientNoSession := setupTestMCPClient(t, pool, dataStore, nil, humanSession.Identity)

			simpleOut, errText, isErr := callToolJSON[GetCardSessionOutput](t, clientNoSession, "get_card_session", map[string]any{
				"card_id": todoCard.ID.String(),
				"mode":    "simple",
			})
			if isErr || simpleOut.Count != 3 {
				t.Fatalf("simple mode should work without sessionService: out=%+v err=%s", simpleOut, errText)
			}

			_, fullErr, isFullErr := callToolJSON[GetCardSessionOutput](t, clientNoSession, "get_card_session", map[string]any{
				"card_id": todoCard.ID.String(),
				"mode":    "full",
			})
			if !isFullErr || !strings.Contains(fullErr, "UNAVAILABLE:") {
				t.Fatalf("expected UNAVAILABLE error for full mode with nil sessionService, got isErr=%v text=%q", isFullErr, fullErr)
			}

			_, searchErr, isSearchErr := callToolJSON[SearchCardSessionOutput](t, clientNoSession, "search_card_session", map[string]any{
				"card_id": todoCard.ID.String(),
				"query":   "marker-todo-huge-result",
			})
			if !isSearchErr || !strings.Contains(searchErr, "UNAVAILABLE:") {
				t.Fatalf("expected UNAVAILABLE error for search with nil sessionService, got isErr=%v text=%q", isSearchErr, searchErr)
			}
		})
	})
}
