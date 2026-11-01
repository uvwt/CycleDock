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
		fmt.Fprintln(w, `data: {"type":"response.completed","response":{"output":[]}}`)
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
