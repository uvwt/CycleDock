package modelclient

import (
	"strings"
	"testing"
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
	got, err := readChatStream(strings.NewReader(data), func(s string) { deltas = append(deltas, s) })
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
	_, err := readChatStream(strings.NewReader("data: {invalid-json}\n"), nil)
	if err == nil {
		t.Fatal("malformed stream must fail")
	}
}
