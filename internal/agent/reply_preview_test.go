package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store/db"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func TestReplyPreviewHandlesEveryFragmentBoundary(t *testing.T) {
	for _, content := range []string{"你好，世界🌍", "line one\n\"quoted\"\\path\tend"} {
		raw, _ := json.Marshal(map[string]any{"content": content, "type": "text"})
		for i := 0; i <= len(raw); i++ {
			got := replyPreviewContent(string(raw[:i]))
			if !utf8.ValidString(got) || !strings.HasPrefix(content, got) {
				t.Fatalf("fragment %q preview=%q want prefix of %q", raw[:i], got, content)
			}
		}
		if got := replyPreviewContent(string(raw)); got != content {
			t.Fatalf("got %q want %q", got, content)
		}
	}
	escaped := `{"content":"\u4f60\u597d\ud83c\udf0d"}`
	for i := 0; i <= len(escaped); i++ {
		if got := replyPreviewContent(escaped[:i]); !strings.HasPrefix("你好🌍", got) {
			t.Fatalf("escaped fragment %q preview=%q", escaped[:i], got)
		}
	}
}

func TestReplyPreviewNeverExposesOtherFields(t *testing.T) {
	for _, raw := range []string{
		`{"credential":"secret","widget":{"content":"private"}}`,
		`{"widget":{"content":"private"},"content":`,
		`{"widget":{"content":"private`,
		`{"content":{"nested":"private"}}`,
		`{"content":"bad\xescape"}`,
	} {
		if got := replyPreviewContent(raw); got != "" {
			t.Fatalf("unexpected preview %q for %s", got, raw)
		}
	}
	if got := replyPreviewContent(`{"widget":{"content":"private"},"content":"public`); got != "public" {
		t.Fatalf("top-level content=%q", got)
	}
}

func TestToolPreviewProducesADKPartialArgsOnlyForReplyContent(t *testing.T) {
	var responses []*model.LLMResponse
	emit := func(response *model.LLMResponse) bool { responses = append(responses, response); return true }
	preview := previewToolCalls("generation", 1, emit)
	preview(modelclient.ToolCall{ID: "secret", Function: modelclient.ToolFunction{Name: "other_tool", Arguments: `{"content":"do not show"}`}})
	preview(modelclient.ToolCall{ID: "reply", Function: modelclient.ToolFunction{Name: "reply_message", Arguments: `{"content":"你`}})
	preview(modelclient.ToolCall{ID: "reply", Function: modelclient.ToolFunction{Name: "reply_message", Arguments: `{"content":"你好`}})
	preview(modelclient.ToolCall{ID: "reply", Function: modelclient.ToolFunction{Name: "reply_message", Arguments: `{"content":"你好`}})
	if len(responses) != 2 {
		t.Fatalf("responses=%+v", responses)
	}
	for i, want := range []string{"你", "好"} {
		response := responses[i]
		call := response.Content.Parts[0].FunctionCall
		if !response.Partial || call.ID != "reply" || call.PartialArgs[0].JsonPath != "$.content" || call.PartialArgs[0].StringValue != want || !*call.WillContinue {
			t.Fatalf("response=%+v call=%+v", response, call)
		}
	}
}

func TestModelFailoverReplacesDraftWithoutExposingRawText(t *testing.T) {
	server := func(text string, complete bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			arguments, _ := json.Marshal(map[string]any{"content": text, "type": "text"})
			packet, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
				"content":    "PRIVATE MODEL TEXT",
				"tool_calls": []any{map[string]any{"index": 0, "id": "same-call", "function": map[string]string{"name": "reply_message", "arguments": string(arguments)}}},
			}}}})
			fmt.Fprintf(w, "data: %s\n\n", packet)
			if complete {
				fmt.Fprintln(w, "data: [DONE]")
			}
		}))
	}
	primary, backup := server("primary draft", false), server("backup reply", true)
	defer primary.Close()
	defer backup.Close()
	hub := realtime.NewHub()
	card, attempt := uuid.New(), uuid.New()
	hub.Publish(card, realtime.Event{Type: "attempt.started", AttemptID: attempt.String()})
	sub, _ := hub.Subscribe(card)
	defer sub.Close()
	llm, err := newADKRouteModel(modelclient.New(), nil, uuid.New(), []db.WorkspaceModelRoute{
		{Enabled: true, ProviderBaseUrl: primary.URL, Model: "primary"},
		{Enabled: true, ProviderBaseUrl: backup.URL, Model: "backup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher := replyEventPublisher{hub: hub, cardID: card, attemptID: attempt}
	var responses int
	for response, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("Reply", genai.RoleUser)},
	}, true) {
		if err != nil {
			t.Fatal(err)
		}
		if response.Partial {
			publisher.publish(&session.Event{LLMResponse: *response})
			continue
		}
		responses++
		if response.Content.Parts[len(response.Content.Parts)-1].FunctionCall == nil {
			t.Fatal("expected complete tool call")
		}
	}
	if responses != 1 {
		t.Fatalf("responses=%d", responses)
	}
	var ids []string
	for len(sub.Events) > 0 {
		event := <-sub.Events
		if strings.Contains(event.Text, "PRIVATE") {
			t.Fatal("raw model text leaked")
		}
		if event.Type == "reply.preview" {
			ids = append(ids, event.ReplyID)
		}
	}
	restored, snapshot := hub.Subscribe(card)
	defer restored.Close()
	if len(ids) != 2 || ids[0] == ids[1] || len(snapshot.Replies) != 1 || snapshot.Replies[0].Text != "backup reply" {
		t.Fatalf("ids=%v snapshot=%+v", ids, snapshot)
	}
}

func TestStoppingADKStreamCancelsModelRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":"reply_message","arguments":"{\"content\":\"hello"}}]}}]}`)
		w.(http.Flusher).Flush()
		<-request.Context().Done()
		close(disconnected)
	}))
	defer server.Close()
	llm, err := newADKRouteModel(modelclient.New(), nil, uuid.New(), []db.WorkspaceModelRoute{{Enabled: true, ProviderBaseUrl: server.URL, Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	var previews int
	for response, err := range llm.GenerateContent(ctx, &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("Reply", genai.RoleUser)}}, true) {
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Content.Parts) > 0 {
			previews++
			break
		}
	}
	if previews != 1 {
		t.Fatalf("previews=%d", previews)
	}
	select {
	case <-disconnected:
	case <-ctx.Done():
		t.Fatal("model request remained open after consumer stopped")
	}
}

func TestNonStreamingADKCallReturnsOnlyFinalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Stream {
			t.Error("non-streaming ADK call enabled tool previews")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"summary"}}]}`)
	}))
	defer server.Close()
	llm, err := newADKRouteModel(modelclient.New(), nil, uuid.New(), []db.WorkspaceModelRoute{{Enabled: true, ProviderBaseUrl: server.URL, Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for response, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("Summarize", genai.RoleUser)}}, false) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if response.Partial || response.Content.Parts[0].Text != "summary" {
			t.Fatalf("response=%+v", response)
		}
	}
	if count != 1 {
		t.Fatalf("responses=%d", count)
	}
}
