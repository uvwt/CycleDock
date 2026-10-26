package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPBearerTokenSaveEditTestAndClear(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d", login.Code)
	}
	cookie := responseCookie(t, login, sessionCookieName)

	const firstToken = "secret-mcp-token-for-integration"
	var receivedBearer atomic.Value
	receivedBearer.Store("")
	server := mcp.NewServer(&mcp.Implementation{Name: "token-test", Version: "1.0.0"}, nil)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBearer.Store(r.Header.Get("Authorization"))
		streamable.ServeHTTP(w, r)
	}))
	defer remote.Close()

	create := doJSON(t, handler, http.MethodPost, "/api/v1/agent/mcp-connections", map[string]any{
		"name": "secure-mcp", "url": remote.URL, "enabled": true,
		"bearer_token": firstToken, "timeout_ms": 5000,
	}, cookie, nil)
	if create.Code != http.StatusCreated || bytes.Contains(create.Body.Bytes(), []byte(firstToken)) {
		t.Fatalf("create response status=%d body=%s", create.Code, create.Body.String())
	}
	var saved mcpConnectionResponse
	decodeRecorder(t, create, &saved)
	if !saved.TokenConfigured {
		t.Fatal("token should be configured")
	}
	id := uuid.MustParse(saved.ID)
	var ciphertext []byte
	if err := pool.QueryRow(context.Background(),
		"SELECT bearer_token_ciphertext FROM workspace_mcp_connections WHERE id=$1", id,
	).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) == 0 || bytes.Contains(ciphertext, []byte(firstToken)) {
		t.Fatal("token not stored as ciphertext")
	}

	list := doJSON(t, handler, http.MethodGet, "/api/v1/agent/mcp-connections", nil, cookie, nil)
	if list.Code != http.StatusOK || bytes.Contains(list.Body.Bytes(), []byte(firstToken)) {
		t.Fatalf("list exposed token: %s", list.Body.String())
	}
	verify := func(expected string) {
		t.Helper()
		result := doJSON(t, handler, http.MethodPost, "/api/v1/agent/mcp-test", map[string]any{
			"connection_id": saved.ID, "name": "secure-mcp", "url": remote.URL, "timeout_ms": 5000,
		}, cookie, nil)
		if result.Code != http.StatusOK || !bytes.Contains(result.Body.Bytes(), []byte(`"ok":true`)) {
			t.Fatalf("saved token test: %d %s", result.Code, result.Body.String())
		}
		if receivedBearer.Load().(string) != expected || bytes.Contains(result.Body.Bytes(), []byte(firstToken)) {
			t.Fatal("unexpected Authorization header or secret in response")
		}
	}
	verify("Bearer " + firstToken)

	updateURL := "/api/v1/agent/mcp-connections/" + saved.ID
	// 不能借用 connection_id 把服务端解密的 Token 发到其他 URL。
	changedURL := doJSON(t, handler, http.MethodPost, "/api/v1/agent/mcp-test", map[string]any{
		"connection_id": saved.ID, "name": "secure-mcp",
		"url": "https://attacker.example/mcp", "timeout_ms": 5000,
	}, cookie, nil)
	if changedURL.Code != http.StatusBadRequest || bytes.Contains(changedURL.Body.Bytes(), []byte(firstToken)) {
		t.Fatalf("changed URL unexpectedly accepted: %d %s", changedURL.Code, changedURL.Body.String())
	}
	changedConnection := doJSON(t, handler, http.MethodPut, updateURL, map[string]any{
		"name": "secure-mcp", "url": "https://attacker.example/mcp", "enabled": true, "timeout_ms": 5000,
	}, cookie, nil)
	if changedConnection.Code != http.StatusConflict {
		t.Fatalf("changing URL without re-entering token returned %d", changedConnection.Code)
	}
	update := func(fields map[string]any) mcpConnectionResponse {
		t.Helper()
		data := map[string]any{"name": "secure-mcp", "url": remote.URL, "timeout_ms": 5000, "enabled": true}
		for key, value := range fields {
			data[key] = value
		}
		response := doJSON(t, handler, http.MethodPut, updateURL, data, cookie, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("update failed: %d %s", response.Code, response.Body.String())
		}
		var changed mcpConnectionResponse
		decodeRecorder(t, response, &changed)
		return changed
	}
	if !update(map[string]any{}).TokenConfigured {
		t.Fatal("empty update erased existing token")
	}
	verify("Bearer " + firstToken)
	if !update(map[string]any{"bearer_token": "replacement-secret"}).TokenConfigured {
		t.Fatal("replacement token not configured")
	}
	verify("Bearer replacement-secret")
	if update(map[string]any{"clear_bearer_token": true}).TokenConfigured {
		t.Fatal("token was not cleared")
	}
	verify("")
	connection, err := dataStore.GetMCPConnection(context.Background(), uuid.Nil, id)
	if err == nil || connection.ID != uuid.Nil {
		t.Fatal("cross-workspace connection lookup was not denied")
	}
}
