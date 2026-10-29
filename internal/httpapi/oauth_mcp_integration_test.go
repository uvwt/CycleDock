package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/mcpserver"
	cycledockoauth "github.com/uvwt/CycleDock/internal/oauth"
)

func TestOAuthHTTPToOfficialMCPClientEndToEnd(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)

	humanAuth, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatalf("create human auth: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	publicURL := "http://" + listener.Addr().String()

	oauthService, err := cycledockoauth.New(pool, cycledockoauth.Config{PublicURL: publicURL})
	if err != nil {
		t.Fatalf("create OAuth service: %v", err)
	}
	mcpHandlers := mcpserver.New(dataStore, oauthService, nil)
	handler := NewRouter(pool, dataStore, humanAuth, Config{
		OAuth: oauthService, MCP: mcpHandlers.MCP, MCPMetadata: mcpHandlers.ProtectedResourceMetadata,
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	metadataResponse, err := http.Get(publicURL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if metadataResponse.StatusCode != http.StatusOK {
		t.Fatalf("protected resource metadata status=%d", metadataResponse.StatusCode)
	}
	var resourceMetadata map[string]any
	decodeHTTPBody(t, metadataResponse, &resourceMetadata)
	if resourceMetadata["resource"] != publicURL+"/mcp" {
		t.Fatalf("unexpected protected resource metadata: %+v", resourceMetadata)
	}

	unauthorized, err := http.Get(publicURL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MCP status=%d", unauthorized.StatusCode)
	}
	if header := unauthorized.Header.Get("WWW-Authenticate"); !strings.Contains(header, "resource_metadata=") {
		t.Fatalf("MCP 401 missing resource_metadata challenge: %q", header)
	}

	callbackURL := "http://127.0.0.1:65534/callback"
	registrationBody, _ := json.Marshal(map[string]any{
		"client_name":                "Phase 3 Official MCP Client",
		"redirect_uris":              []string{callbackURL},
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
	registerRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, publicURL+"/oauth/register", bytes.NewReader(registrationBody))
	registerRequest.Header.Set("Content-Type", "application/json")
	registerResponse, err := http.DefaultClient.Do(registerRequest)
	if err != nil {
		t.Fatal(err)
	}
	if registerResponse.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(registerResponse.Body)
		t.Fatalf("DCR status=%d body=%s", registerResponse.StatusCode, body)
	}
	var registration oauthClientRegistrationResponse
	decodeHTTPBody(t, registerResponse, &registration)
	if registration.ClientID == "" {
		t.Fatal("DCR did not return client_id")
	}

	jar, _ := cookiejar.New(nil)
	browser := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	loginBody := []byte(`{"password":"` + httpIntegrationPassword + `"}`)
	loginRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, publicURL+"/api/v1/auth/login", bytes.NewReader(loginBody))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse, err := browser.Do(loginRequest)
	if err != nil {
		t.Fatal(err)
	}
	if loginResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResponse.Body)
		t.Fatalf("human login status=%d body=%s", loginResponse.StatusCode, body)
	}
	var human identityResponse
	decodeHTTPBody(t, loginResponse, &human)
	workspaceID := uuid.MustParse(human.Workspace.ID)
	project, err := dataStore.CreateProject(ctx, workspaceID, "MCP E2E Project", "created for protocol test", "Preserve durable findings.")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	verifier := strings.Repeat("e", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := "mcp-e2e-state"
	authorizeValues := url.Values{
		"response_type":         {"code"},
		"client_id":             {registration.ClientID},
		"redirect_uri":          {callbackURL},
		"scope":                 {cycledockoauth.ScopeMCP},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {publicURL + "/mcp"},
	}
	consentResponse, err := browser.Get(publicURL + "/oauth/authorize?" + authorizeValues.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if consentResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(consentResponse.Body)
		t.Fatalf("consent status=%d body=%s", consentResponse.StatusCode, body)
	}
	_ = consentResponse.Body.Close()

	authorizeValues.Set("decision", "approve")
	approveRequest, _ := http.NewRequestWithContext(
		ctx, http.MethodPost, publicURL+"/oauth/authorize",
		strings.NewReader(authorizeValues.Encode()),
	)
	approveRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	approveResponse, err := browser.Do(approveRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer approveResponse.Body.Close()
	if approveResponse.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(approveResponse.Body)
		t.Fatalf("approve status=%d body=%s", approveResponse.StatusCode, body)
	}
	redirect, err := url.Parse(approveResponse.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := redirect.Query().Get("code")
	if code == "" || redirect.Query().Get("state") != state || redirect.Query().Get("iss") != publicURL {
		t.Fatalf("invalid authorization redirect: %s", redirect.String())
	}

	tokenValues := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {registration.ClientID},
		"redirect_uri":  {callbackURL},
		"code_verifier": {verifier},
		"resource":      {publicURL + "/mcp"},
	}
	tokenRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, publicURL+"/oauth/token", strings.NewReader(tokenValues.Encode()))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResponse, err := http.DefaultClient.Do(tokenRequest)
	if err != nil {
		t.Fatal(err)
	}
	if tokenResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResponse.Body)
		t.Fatalf("token status=%d body=%s", tokenResponse.StatusCode, body)
	}
	var tokens oauthTokenResponse
	decodeHTTPBody(t, tokenResponse, &tokens)
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("OAuth token response incomplete: %+v", tokens)
	}

	mcpHTTPClient := &http.Client{Transport: bearerRoundTripper{
		base: http.DefaultTransport, token: tokens.AccessToken,
	}}
	client := mcp.NewClient(&mcp.Implementation{Name: "cycledock-phase3-e2e", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: publicURL + "/mcp", HTTPClient: mcpHTTPClient,
	}, nil)
	if err != nil {
		t.Fatalf("official MCP client connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	requiredTools := map[string]bool{
		"list_projects": false, "list_cards": false, "get_card": false,
		"create_discussion": false, "generate_work": false, "update_card": false, "move_card": false,
		"add_comment": false, "create_turn": false, "claim_attempt": false,
		"heartbeat_attempt": false, "reply_message": false,
		"check_in": false, "finish_attempt": false, "confirm_card": false,
	}
	legacyTools := map[string]bool{
		"create_followup": true, "claim_work": true, "heartbeat_run": true, "finish_run": true,
		"report_progress": true, "add_finding": true,
	}
	for _, tool := range tools.Tools {
		if _, ok := requiredTools[tool.Name]; ok {
			requiredTools[tool.Name] = true
		}
		if legacyTools[tool.Name] {
			t.Fatalf("legacy Trigger/Run MCP tool is still exposed: %s", tool.Name)
		}
	}
	for name, found := range requiredTools {
		if !found {
			t.Fatalf("MCP tool %s was not discovered", name)
		}
	}

	projects := callMCPTool[mcpserver.ListProjectsOutput](t, ctx, clientSession, "list_projects", map[string]any{})
	if len(projects.Projects) != 1 || projects.Projects[0].ID != project.ID.String() {
		t.Fatalf("MCP project scope mismatch: %+v", projects)
	}
	discussion := callMCPTool[mcpserver.CardOutput](t, ctx, clientSession, "create_discussion", map[string]any{
		"project_id": project.ID.String(), "idempotency_key": "mcp-e2e-discussion",
	})
	if discussion.Card.ID == "" || discussion.Card.Kind != "discussion" {
		t.Fatalf("create_discussion output: %+v", discussion)
	}
	created := callMCPTool[mcpserver.CardOutput](t, ctx, clientSession, "generate_work", map[string]any{
		"discussion_id": discussion.Card.ID, "kind": "todo", "title": "MCP work",
		"description": "created through the official MCP client", "idempotency_key": "mcp-e2e-generate",
	})
	if created.Card.ID == "" || created.Card.ProjectID != project.ID.String() ||
		created.Card.SourceDiscussionID != discussion.Card.ID {
		t.Fatalf("generate_work output: %+v", created)
	}
	callMCPTool[mcpserver.CardOutput](t, ctx, clientSession, "move_card", map[string]any{
		"card_id": created.Card.ID, "state": "working",
	})
	reviewAttempt, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "move_card",
		Arguments: map[string]any{
			"card_id": created.Card.ID, "state": "review",
		},
	})
	if err != nil {
		t.Fatalf("move_card review protocol error: %v", err)
	}
	if !reviewAttempt.IsError {
		t.Fatalf("external MCP bypassed confirm_card and moved directly to review: %+v", reviewAttempt)
	}
	turn := callMCPTool[mcpserver.TurnOutput](t, ctx, clientSession, "create_turn", map[string]any{
		"card_id": created.Card.ID, "content": "Execute protocol E2E work", "idempotency_key": "mcp-e2e-turn",
	})
	if turn.TurnID == "" || turn.EventID == "" {
		t.Fatalf("create_turn output: %+v", turn)
	}
	claimed := callMCPTool[mcpserver.AttemptOutput](t, ctx, clientSession, "claim_attempt", map[string]any{
		"card_id": created.Card.ID,
	})
	if claimed.Attempt.ID == "" || claimed.Attempt.Status != "running" || claimed.Attempt.TurnID != turn.TurnID {
		t.Fatalf("claim_attempt output: %+v", claimed)
	}
	replayedClaim := callMCPTool[mcpserver.AttemptOutput](t, ctx, clientSession, "claim_attempt", map[string]any{
		"card_id": created.Card.ID,
	})
	if !replayedClaim.Reused || replayedClaim.Attempt.ID != claimed.Attempt.ID {
		t.Fatalf("claim_attempt replay did not reuse current attempt: first=%+v replay=%+v", claimed, replayedClaim)
	}
	heartbeat := callMCPTool[mcpserver.AttemptOutput](t, ctx, clientSession, "heartbeat_attempt", map[string]any{
		"attempt_id": claimed.Attempt.ID,
	})
	if heartbeat.Attempt.ID != claimed.Attempt.ID || heartbeat.Attempt.Status != "running" {
		t.Fatalf("heartbeat_attempt output: %+v", heartbeat)
	}
	progress := callMCPTool[mcpserver.EventOutput](t, ctx, clientSession, "reply_message", map[string]any{
		"attempt_id": claimed.Attempt.ID, "type": "text", "content": "OAuth and Streamable HTTP session verified",
		"end_turn": false, "idempotency_key": "mcp-e2e-progress",
	})
	if progress.EventID == "" {
		t.Fatalf("reply_message progress output: %+v", progress)
	}
	finished := callMCPTool[mcpserver.EventOutput](t, ctx, clientSession, "reply_message", map[string]any{
		"attempt_id": claimed.Attempt.ID, "type": "text",
		"content": "Official MCP SDK E2E completed", "end_turn": true,
		"idempotency_key": "mcp-e2e-final",
	})
	if finished.EventID == "" {
		t.Fatalf("final reply_message output: %+v", finished)
	}
	cardDetail := callMCPTool[mcpserver.GetCardOutput](t, ctx, clientSession, "get_card", map[string]any{
		"card_id": created.Card.ID, "event_limit": 50,
	})
	if cardDetail.CurrentAttempt != nil {
		t.Fatalf("completed card still has current attempt: %+v", cardDetail.CurrentAttempt)
	}
	if len(cardDetail.PendingTurns) != 0 {
		t.Fatalf("completed turn remained pending: %+v", cardDetail.PendingTurns)
	}
	var sawAttempt bool
	for _, attempt := range cardDetail.AttemptHistory {
		if attempt.ID == claimed.Attempt.ID && attempt.Status == "completed" {
			sawAttempt = true
		}
	}
	if !sawAttempt {
		t.Fatalf("completed attempt missing from history: %+v", cardDetail.AttemptHistory)
	}
	var sawResult bool
	for _, event := range cardDetail.Timeline {
		if event.Type == "message" && strings.Contains(event.Content, "Official MCP SDK") {
			if event.AttemptID != claimed.Attempt.ID {
				t.Fatalf("result attempt_id=%q want=%q", event.AttemptID, claimed.Attempt.ID)
			}
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("completed result missing from durable timeline: %+v", cardDetail.Timeline)
	}

	// 无可见回复时可单独结算状态；重试不生成 Result 或额外气泡。
	callMCPTool[mcpserver.TurnOutput](t, ctx, clientSession, "create_turn", map[string]any{
		"card_id": created.Card.ID, "content": "Status-only completion", "idempotency_key": "status-only-turn",
	})
	statusClaim := callMCPTool[mcpserver.AttemptOutput](t, ctx, clientSession, "claim_attempt", map[string]any{
		"card_id": created.Card.ID,
	})
	for i := 0; i < 2; i++ {
		statusFinished := callMCPTool[mcpserver.FinishAttemptOutput](t, ctx, clientSession, "finish_attempt", map[string]any{
			"attempt_id": statusClaim.Attempt.ID, "outcome": "completed",
		})
		if statusFinished.Attempt.Status != "completed" {
			t.Fatalf("status-only completion: %+v", statusFinished)
		}
	}
	afterStatus := callMCPTool[mcpserver.GetCardOutput](t, ctx, clientSession, "get_card", map[string]any{
		"card_id": created.Card.ID, "event_limit": 50,
	})
	for _, event := range afterStatus.Timeline {
		if event.AttemptID == statusClaim.Attempt.ID {
			t.Fatalf("status-only completion created a bubble: %+v", event)
		}
	}

	otherWorkspace, err := dataStore.CreateWorkspace(ctx, "Other MCP Workspace")
	if err != nil {
		t.Fatal(err)
	}
	otherHuman, err := dataStore.CreateSource(ctx, otherWorkspace.ID, domain.SourceKindHuman, "Other Human")
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := dataStore.CreateProject(ctx, otherWorkspace.ID, "Private Project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	otherCard, err := dataStore.CreateTodoCard(ctx, otherWorkspace.ID, otherProject.ID, otherHuman.ID, "Private Card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	crossTenant, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_card", Arguments: map[string]any{"card_id": otherCard.ID.String()},
	})
	if err != nil {
		t.Fatalf("cross-workspace get_card protocol error: %v", err)
	}
	if !crossTenant.IsError {
		t.Fatalf("OAuth Workspace token read another Workspace card: %+v", crossTenant.StructuredContent)
	}
}

type bearerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (transport bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

func callMCPTool[T any](
	t *testing.T,
	ctx context.Context,
	session *mcp.ClientSession,
	name string,
	arguments any,
) T {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call MCP tool %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("MCP tool %s returned error: %+v", name, result.Content)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("encode MCP tool %s structured content: %v", name, err)
	}
	var output T
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatalf("decode MCP tool %s output %s: %v", name, encoded, err)
	}
	return output
}

func decodeHTTPBody(t *testing.T, response *http.Response, dst any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(dst); err != nil {
		t.Fatalf("decode HTTP %s: %v", response.Request.URL, err)
	}
}
