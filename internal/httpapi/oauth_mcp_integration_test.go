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
		"add_comment": false, "create_followup": false, "claim_work": false,
		"heartbeat_run": false, "report_progress": false, "add_finding": false,
		"check_in": false, "finish_run": false, "confirm_card": false,
	}
	for _, tool := range tools.Tools {
		if _, ok := requiredTools[tool.Name]; ok {
			requiredTools[tool.Name] = true
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
	followup := callMCPTool[mcpserver.FollowupOutput](t, ctx, clientSession, "create_followup", map[string]any{
		"card_id": created.Card.ID, "content": "Execute protocol E2E work", "idempotency_key": "mcp-e2e-followup",
	})
	if followup.TriggerID == "" {
		t.Fatalf("create_followup output: %+v", followup)
	}
	claimed := callMCPTool[mcpserver.RunOutput](t, ctx, clientSession, "claim_work", map[string]any{
		"card_id": created.Card.ID,
	})
	if claimed.Run.ID == "" || claimed.Run.Status != "running" {
		t.Fatalf("claim_work output: %+v", claimed)
	}
	progress := callMCPTool[mcpserver.ProgressOutput](t, ctx, clientSession, "report_progress", map[string]any{
		"run_id": claimed.Run.ID, "stage": "verify", "summary": "protocol path verified",
		"event_content": "OAuth and Streamable HTTP session verified", "idempotency_key": "mcp-e2e-progress",
	})
	if progress.EventID == "" {
		t.Fatalf("report_progress output: %+v", progress)
	}
	finished := callMCPTool[mcpserver.FinishRunOutput](t, ctx, clientSession, "finish_run", map[string]any{
		"run_id": claimed.Run.ID, "outcome": "completed",
		"result": "Official MCP SDK E2E completed", "idempotency_key": "mcp-e2e-finish",
	})
	if finished.Run.Status != "completed" {
		t.Fatalf("finish_run output: %+v", finished)
	}
	cardDetail := callMCPTool[mcpserver.GetCardOutput](t, ctx, clientSession, "get_card", map[string]any{
		"card_id": created.Card.ID, "event_limit": 50,
	})
	if cardDetail.CurrentRun != nil {
		t.Fatalf("completed card still has current run: %+v", cardDetail.CurrentRun)
	}
	var sawResult bool
	for _, event := range cardDetail.Timeline {
		if event.Type == "result" && strings.Contains(event.Content, "Official MCP SDK") {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("completed result missing from durable timeline: %+v", cardDetail.Timeline)
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
