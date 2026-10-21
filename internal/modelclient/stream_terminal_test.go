package modelclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A proxy or provider may close an SSE response cleanly before its terminal event.
// Without this guard the Runner would permanently write partial output as a Result.
func TestIncompleteChatStreamCannotComplete(t *testing.T) {
	data := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	got, err := readChatStream(strings.NewReader(data), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "before [DONE]") {
		t.Fatalf("truncated completion must fail: %+v err=%v", got, err)
	}
}

func TestIncompleteResponsesStreamCannotComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"partial"}`)
	}))
	defer server.Close()
	_, err := New().Complete(context.Background(), Config{BaseURL: server.URL, Model: "test", Transport: TransportResponses, Timeout: time.Second, OnTextDelta: func(string) {}}, []Message{{Role: "user", Content: "hello"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "before response.completed") {
		t.Fatalf("truncated responses must fail: %v", err)
	}
}
