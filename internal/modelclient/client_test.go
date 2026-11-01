package modelclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompleteSendsBearerAndDecodesToolCall(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"echo","arguments":"{\"value\":\"ok\"}"}}]}}]}`))
	}))
	defer server.Close()

	result, err := New().Complete(context.Background(), Config{
		BaseURL: server.URL, Model: "test-model", APIKey: "test-secret",
	}, []Message{{Role: "user", Content: "test"}}, []ToolDefinition{{
		Type: "function",
		Function: ToolDefinitionFunc{
			Name: "echo", Parameters: map[string]any{"type": "object"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer test-secret" {
		t.Fatalf("authorization=%q", authorization)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Name != "echo" {
		t.Fatalf("tool calls=%+v", result.ToolCalls)
	}
}

func TestCompleteHonorsContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"late"}}]}`))
	}))
	defer server.Close()

	started := time.Now()
	_, err := New().Complete(context.Background(), Config{
		BaseURL: server.URL, Model: "test", Timeout: 40 * time.Millisecond,
	}, []Message{{Role: "user", Content: "wait"}}, nil)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("timeout was not bounded: %v", time.Since(started))
	}
}

func TestCompleteRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "5000000")
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	_, err := New().Complete(context.Background(), Config{
		BaseURL: server.URL, Model: "test",
	}, []Message{{Role: "user", Content: "test"}}, nil)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error=%v", err)
	}
}

func TestCompleteBoundsProviderErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat("provider detail ", 100), http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := New().Complete(context.Background(), Config{
		BaseURL: server.URL, Model: "test",
	}, []Message{{Role: "user", Content: "test"}}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("error=%v", err)
	}
	if len([]rune(apiErr.Body)) > 503 {
		t.Fatalf("provider error body was not bounded: %d", len([]rune(apiErr.Body)))
	}
}
