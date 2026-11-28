package modelclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResponsesTransportStreamsTextAndToolCalls(t *testing.T) {
	var requestBody struct {
		Model        string          `json:"model"`
		Instructions string          `json:"instructions"`
		Input        []responsesItem `json:"input"`
		Tools        []struct {
			Strict     *bool          `json:"strict"`
			Parameters map[string]any `json:"parameters"`
		} `json:"tools"`
		Store  bool `json:"store"`
		Stream bool `json:"stream"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Test-Provider"); got != "subscription" {
			t.Errorf("X-Test-Provider = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"I will "}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"check."}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"id\":7}"}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"type":"response.completed","response":{"output":[],"usage":{"input_tokens":520,"output_tokens":90,"input_tokens_details":{"cached_tokens":128},"output_tokens_details":{"reasoning_tokens":24}}}}`)
		fmt.Fprintln(w)
	}))
	defer server.Close()

	client := New()
	completion, err := client.Complete(context.Background(), Config{
		BaseURL:   server.URL + "/v1",
		Model:     "subscription-model",
		APIKey:    "oauth-token",
		Timeout:   time.Second,
		Transport: TransportResponses,
		Headers:   map[string]string{"X-Test-Provider": "subscription"},
	}, []Message{
		{Role: "system", Content: "Follow workspace rules."},
		{Role: "user", Content: "Check task 7."},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID: "previous_call", Type: "function",
				Function: ToolFunction{Name: "prior_lookup", Arguments: `{"id":6}`},
			}},
		},
		{Role: "tool", ToolCallID: "previous_call", Content: `{"ok":true}`},
	}, []ToolDefinition{{
		Type: "function",
		Function: ToolDefinitionFunc{
			Name: "lookup",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "integer"},
					"detail": map[string]any{"type": "string"},
				},
				"required": []string{"id"},
			},
		},
	}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if completion.Content != "I will check." {
		t.Fatalf("content = %q", completion.Content)
	}
	if len(completion.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(completion.ToolCalls))
	}
	if completion.ToolCalls[0].ID != "call_1" || completion.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("unexpected tool call: %#v", completion.ToolCalls[0])
	}
	wantUsage := TokenUsage{
		InputTokens:           520,
		OutputTokens:          90,
		CachedInputTokens:     128,
		ReasoningOutputTokens: 24,
	}
	if completion.Usage == nil || *completion.Usage != wantUsage {
		t.Fatalf("completion.Usage = %#v, want %#v", completion.Usage, wantUsage)
	}
	if requestBody.Model != "subscription-model" || requestBody.Instructions != "Follow workspace rules." {
		t.Fatalf("unexpected request metadata: %#v", requestBody)
	}
	if requestBody.Store || !requestBody.Stream {
		t.Fatalf("Responses plan usage must send store=false stream=true: %#v", requestBody)
	}
	// 必须显式传 false；省略字段会允许服务端把可选参数自动改成必填。
	if len(requestBody.Tools) != 1 || requestBody.Tools[0].Strict == nil || *requestBody.Tools[0].Strict {
		t.Fatalf("Responses tools must explicitly disable strict normalization: %+v", requestBody.Tools)
	}
	required, _ := requestBody.Tools[0].Parameters["required"].([]any)
	if len(required) != 1 || required[0] != "id" {
		t.Fatalf("optional tool parameter became required: %+v", requestBody.Tools[0].Parameters)
	}
	if len(requestBody.Input) != 3 {
		t.Fatalf("input item count = %d, want 3", len(requestBody.Input))
	}
	if requestBody.Input[1].Type != "function_call" || requestBody.Input[2].Type != "function_call_output" {
		t.Fatalf("tool history was not converted for Responses: %#v", requestBody.Input)
	}
}

func TestResponsesTransportRejectsMalformedSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, "data: {not-json}")
	}))
	defer server.Close()

	_, err := New().Complete(context.Background(), Config{
		BaseURL:   server.URL,
		Model:     "model",
		Transport: TransportResponses,
	}, []Message{{Role: "user", Content: "hello"}}, nil)
	if err == nil || !strings.Contains(err.Error(), ErrMalformedResponse.Error()) {
		t.Fatalf("expected malformed response error, got %v", err)
	}
}

func TestResponsesTransportSerializesMultimodalInputImageAndPreservesStringWithoutImages(t *testing.T) {
	var capturedRaw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRaw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"image parsed"}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"type":"response.completed","response":{"output":[]}}`)
		fmt.Fprintln(w)
	}))
	defer server.Close()

	completion, err := New().Complete(context.Background(), Config{
		BaseURL:   server.URL + "/v1",
		Model:     "gpt-vision",
		Transport: TransportResponses,
	}, []Message{
		{Role: "user", Content: "check screenshot"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:       "call_img",
				Type:     "function",
				Function: ToolFunction{Name: "view_image", Arguments: `{}`},
			}},
		},
		{Role: "tool", ToolCallID: "call_img", Content: `{"mime_type":"image/jpeg"}`},
		{
			Role: "user",
			Parts: []ContentPart{
				NewTextPart("Tool image from view_image:"),
				NewImagePartFromBase64("image/jpeg", "/9j/4AAQ"),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if completion.Content != "image parsed" {
		t.Fatalf("completion.Content=%q, want image parsed", completion.Content)
	}

	var wireReq struct {
		Input []struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			CallID  string          `json:"call_id"`
			Output  string          `json:"output"`
			Content json.RawMessage `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(capturedRaw, &wireReq); err != nil {
		t.Fatalf("decode wire request: %v", err)
	}
	if len(wireReq.Input) != 4 {
		t.Fatalf("input len=%d, want 4", len(wireReq.Input))
	}
	if string(wireReq.Input[0].Content) != `"check screenshot"` {
		t.Fatalf("input[0].Content=%s, want plain JSON string", string(wireReq.Input[0].Content))
	}
	if wireReq.Input[1].Type != "function_call" || wireReq.Input[2].Type != "function_call_output" {
		t.Fatalf("unexpected tool items: %+v / %+v", wireReq.Input[1], wireReq.Input[2])
	}

	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
		Detail   string `json:"detail"`
	}
	if err := json.Unmarshal(wireReq.Input[3].Content, &parts); err != nil {
		t.Fatalf("decode multimodal input[3].Content: %v (raw=%s)", err, string(wireReq.Input[3].Content))
	}
	if len(parts) != 2 {
		t.Fatalf("parts len=%d, want 2", len(parts))
	}
	if parts[0].Type != "input_text" || parts[0].Text != "Tool image from view_image:" {
		t.Fatalf("parts[0]=%+v", parts[0])
	}
	if parts[1].Type != "input_image" || parts[1].ImageURL != "data:image/jpeg;base64,/9j/4AAQ" || parts[1].Detail != "auto" {
		t.Fatalf("parts[1]=%+v", parts[1])
	}
}

func TestListModelsSupportsSubscriptionCatalogShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []ModelInfo
	}{
		{
			name: "chatgpt",
			body: `{"models":[
				{"slug":"gpt-visible","display_name":"GPT Visible","visibility":"list","supported_in_api":true},
				{"slug":"hidden","display_name":"Hidden","visibility":"hidden","supported_in_api":true},
				{"slug":"unsupported","display_name":"Unsupported","visibility":"list","supported_in_api":false}
			]}`,
			want: []ModelInfo{{ID: "gpt-visible", Name: "GPT Visible"}},
		},
		{
			name: "grok",
			body: `{"data":[{"id":"grok-model","name":"Grok Model"}]}`,
			want: []ModelInfo{{ID: "grok-model", Name: "Grok Model"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()

			got, err := New().ListModels(context.Background(), Config{BaseURL: server.URL + "/v1"})
			if err != nil {
				t.Fatalf("ListModels: %v", err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("model count = %d, want %d: %#v", len(got), len(test.want), got)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("model[%d] = %#v, want %#v", i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestResponsesToolInputDeltasUseCallIDAndKeepCallsSeparate(t *testing.T) {
	data := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"item-a","call_id":"call-a","type":"function_call","name":"reply_message","arguments":""}}`,
		`data: {"type":"response.output_item.added","output_index":2,"item":{"id":"item-b","call_id":"call-b","type":"function_call","name":"reply_message","arguments":""}}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"item-b","delta":"{\"content\":\"second"}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"item-a","delta":"{\"content\":\"first"}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"id":"item-a","call_id":"call-a","type":"function_call","name":"reply_message","arguments":"{\"content\":\"first\"}"}}`,
		`data: {"type":"response.output_item.done","output_index":2,"item":{"id":"item-b","call_id":"call-b","type":"function_call","name":"reply_message","arguments":"{\"content\":\"second\"}"}}`,
		`data: {"type":"response.completed","response":{"output":[]}}`,
	}, "\n")
	var updates []ToolCall
	completion, err := readResponsesStream(&http.Response{Body: io.NopCloser(strings.NewReader(data))}, nil, func(call ToolCall) { updates = append(updates, call) })
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 4 || updates[0].ID != "call-b" || updates[0].Index != 2 || updates[1].ID != "call-a" || updates[1].Function.Arguments != `{"content":"first` {
		t.Fatalf("updates=%+v", updates)
	}
	if len(completion.ToolCalls) != 2 || completion.ToolCalls[0].ID != "call-a" {
		t.Fatalf("completion=%+v", completion)
	}
}

func TestResponsesStreamParsesResponseModel(t *testing.T) {
	t.Run("prefers response.model from response events", func(t *testing.T) {
		data := strings.Join([]string{
			`data: {"type":"response.created","response":{"model":"gpt-5.4-mini-2026"}}`,
			`data: {"type":"response.output_text.delta","delta":"done"}`,
			`data: {"type":"response.completed","response":{"model":"gpt-5.4-mini-2026-04-01","output":[]}}`,
		}, "\n")
		got, err := readResponsesStream(&http.Response{Body: io.NopCloser(strings.NewReader(data))}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.ResponseModel != "gpt-5.4-mini-2026-04-01" {
			t.Fatalf("ResponseModel=%q, want gpt-5.4-mini-2026-04-01", got.ResponseModel)
		}
	})

	t.Run("accepts top-level model field fallback", func(t *testing.T) {
		data := strings.Join([]string{
			`data: {"type":"response.output_text.delta","model":"proxy-resolved-model","delta":"ok"}`,
			`data: {"type":"response.completed","response":{"output":[]}}`,
		}, "\n")
		got, err := readResponsesStream(&http.Response{Body: io.NopCloser(strings.NewReader(data))}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.ResponseModel != "proxy-resolved-model" {
			t.Fatalf("ResponseModel=%q, want proxy-resolved-model", got.ResponseModel)
		}
	})
}

func TestResponsesReasoningEffortSerializationAndFallback(t *testing.T) {
	t.Run("omits reasoning for regular chat when ReasoningEffort is empty", func(t *testing.T) {
		var rawBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
				t.Errorf("decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"ok"}`)
			fmt.Fprintln(w, `data: {"type":"response.completed","response":{"output":[]}}`)
		}))
		defer server.Close()

		client := NewWithHTTPClient(server.Client())
		_, err := client.Complete(context.Background(), Config{
			BaseURL:   server.URL + "/v1",
			Model:     "gpt-5.4-mini",
			APIKey:    "test-token",
			Timeout:   time.Second,
			Transport: TransportResponses,
		}, []Message{{Role: "user", Content: "hi"}}, nil)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if _, exists := rawBody["reasoning"]; exists {
			t.Fatalf("expected reasoning field to be omitted for regular request, got %#v", rawBody["reasoning"])
		}
	})

	t.Run("serializes reasoning.effort when configured on Responses transport", func(t *testing.T) {
		var rawBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
				t.Errorf("decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"allow"}`)
			fmt.Fprintln(w, `data: {"type":"response.completed","response":{"output":[]}}`)
		}))
		defer server.Close()

		client := NewWithHTTPClient(server.Client())
		_, err := client.Complete(context.Background(), Config{
			BaseURL:         server.URL + "/v1",
			Model:           "gpt-5.4-mini",
			APIKey:          "test-token",
			Timeout:         time.Second,
			Transport:       TransportResponses,
			ReasoningEffort: "low",
		}, []Message{{Role: "user", Content: "guard check"}}, nil)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		reasoning, ok := rawBody["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "low" {
			t.Fatalf("expected reasoning.effort=low, got %#v", rawBody["reasoning"])
		}
	})

	t.Run("retries without reasoning and caches unsupported model on 400", func(t *testing.T) {
		var seen []bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var rawBody map[string]any
			if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
				t.Errorf("decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, hasReasoning := rawBody["reasoning"]
			seen = append(seen, hasReasoning)
			if hasReasoning {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'reasoning' is not supported with this model."}}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"ok"}`)
			fmt.Fprintln(w, `data: {"type":"response.completed","response":{"output":[]}}`)
		}))
		defer server.Close()

		client := NewWithHTTPClient(server.Client())
		cfg := Config{
			BaseURL:         server.URL + "/v1",
			Model:           "non-reasoning-model",
			APIKey:          "test-token",
			Timeout:         time.Second,
			Transport:       TransportResponses,
			ReasoningEffort: "low",
		}
		if _, err := client.Complete(context.Background(), cfg, []Message{{Role: "user", Content: "first"}}, nil); err != nil {
			t.Fatalf("first Complete: %v", err)
		}
		if _, err := client.Complete(context.Background(), cfg, []Message{{Role: "user", Content: "second"}}, nil); err != nil {
			t.Fatalf("second Complete: %v", err)
		}
		if len(seen) != 3 || !seen[0] || seen[1] || seen[2] {
			t.Fatalf("expected reasoning sequence [true, false, false], got %#v", seen)
		}
	})

	t.Run("chat completions transport ignores ReasoningEffort for upstream compatibility", func(t *testing.T) {
		var rawBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
				t.Errorf("decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"role": "assistant", "content": "ok"}},
				},
			})
		}))
		defer server.Close()

		client := NewWithHTTPClient(server.Client())
		_, err := client.Complete(context.Background(), Config{
			BaseURL:         server.URL,
			Model:           "generic-compatible-model",
			Timeout:         time.Second,
			ReasoningEffort: "low",
		}, []Message{{Role: "user", Content: "hi"}}, nil)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if _, exists := rawBody["reasoning"]; exists {
			t.Fatalf("chat completions request must not include reasoning field: %#v", rawBody)
		}
		if _, exists := rawBody["reasoning_effort"]; exists {
			t.Fatalf("chat completions request must not include reasoning_effort field: %#v", rawBody)
		}
	})
}


