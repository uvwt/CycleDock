package modelclient

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestChatStreamEmitsDeltasAndReassemblesTools(t *testing.T) {
	data := strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}",
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"list_\",\"arguments\":\"{\\\"id\\\":\"}}]}}]}",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"cards\",\"arguments\":\"42}\"}}]}}]}",
		"data: [DONE]",
	}, "\n")
	var deltas []string
	got, err := readChatStream(strings.NewReader(data), func(s string) { deltas = append(deltas, s) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Hello" || strings.Join(deltas, "") != "Hello" {
		t.Fatalf("content=%q deltas=%v", got.Content, deltas)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Function.Name != "list_cards" || got.ToolCalls[0].Function.Arguments != `{"id":42}` {
		t.Fatalf("unexpected tools %+v", got.ToolCalls)
	}
}
func TestChatStreamRejectsMalformedData(t *testing.T) {
	_, err := readChatStream(strings.NewReader("data: {invalid-json}\n"), nil, nil)
	if err == nil {
		t.Fatal("malformed stream must fail")
	}
}

func TestChatToolInputsAreAvailableBeforeCompletion(t *testing.T) {
	input, output := io.Pipe()
	defer input.Close()
	first := make(chan ToolCall, 1)
	result := make(chan error, 1)
	go func() {
		_, err := readChatStream(input, nil, func(call ToolCall) {
			select {
			case first <- call:
			default:
			}
		})
		result <- err
	}()
	packet := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"reply_message","arguments":"{\"content\":\"hello"}}]}}]}`
	if _, err := fmt.Fprintln(output, packet); err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-first:
		if call.ID != "call-1" || call.Function.Arguments != `{"content":"hello` {
			t.Fatalf("call=%+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("tool input buffered until completion")
	}
	fmt.Fprintln(output, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"}"}}]}}]}`)
	fmt.Fprintln(output, "data: [DONE]")
	output.Close()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
