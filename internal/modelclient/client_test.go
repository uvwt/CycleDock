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

func TestCompleteDecodesChatCompletionsUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"done"}}],
			"usage":{
				"prompt_tokens":1280,
				"completion_tokens":340,
				"prompt_tokens_details":{"cached_tokens":512},
				"completion_tokens_details":{"reasoning_tokens":96}
			}
		}`))
	}))
	defer server.Close()

	result, err := New().Complete(context.Background(), Config{
		BaseURL: server.URL, Model: "test-model",
	}, []Message{{Role: "user", Content: "check usage"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil {
		t.Fatal("expected non-nil Usage")
	}
	want := TokenUsage{
		InputTokens:           1280,
		OutputTokens:          340,
		CachedInputTokens:     512,
		ReasoningOutputTokens: 96,
	}
	if *result.Usage != want {
		t.Fatalf("usage=%+v, want %+v", *result.Usage, want)
	}
}

func TestCompleteStreamingFallsBackWhenStreamOptionsUnsupported(t *testing.T) {
	calls := 0
	var sawStreamOptions []bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := readBounded(r.Body, r.ContentLength, maxResponseBytes)
		hasStreamOptions := strings.Contains(string(raw), `"stream_options"`)
		sawStreamOptions = append(sawStreamOptions, hasStreamOptions)
		if hasStreamOptions {
			http.Error(w, `{"error":{"message":"unknown field stream_options"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	client := New()
	first, err := client.Complete(context.Background(), Config{
		BaseURL:     server.URL,
		Model:       "relay-model",
		OnTextDelta: func(string) {},
	}, []Message{{Role: "user", Content: "first"}}, nil)
	if err != nil {
		t.Fatalf("first streaming call failed: %v", err)
	}
	if first.Content != "ok" || first.Usage != nil {
		t.Fatalf("first completion=%+v", first)
	}

	second, err := client.Complete(context.Background(), Config{
		BaseURL:     server.URL,
		Model:       "relay-model",
		OnTextDelta: func(string) {},
	}, []Message{{Role: "user", Content: "second"}}, nil)
	if err != nil {
		t.Fatalf("second streaming call failed: %v", err)
	}
	if second.Content != "ok" {
		t.Fatalf("second completion=%+v", second)
	}
	if calls != 3 {
		t.Fatalf("server calls=%d, want 3 (1 rejected with stream_options + 1 retry + 1 cached no-stream_options)", calls)
	}
	if len(sawStreamOptions) != 3 || !sawStreamOptions[0] || sawStreamOptions[1] || sawStreamOptions[2] {
		t.Fatalf("sawStreamOptions=%v, want [true false false]", sawStreamOptions)
	}
}
