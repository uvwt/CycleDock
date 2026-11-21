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

func TestChatStreamParsesFinalUsageChunk(t *testing.T) {
	data := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Answer"}}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":600,"completion_tokens":45,"prompt_tokens_details":{"cached_tokens":200},"completion_tokens_details":{"reasoning_tokens":15}}}`,
		`data: [DONE]`,
	}, "\n")
	got, err := readChatStream(strings.NewReader(data), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Answer" {
		t.Fatalf("content=%q", got.Content)
	}
	if got.Usage == nil {
		t.Fatal("expected usage from final stream chunk")
	}
	want := TokenUsage{
		InputTokens:           600,
		OutputTokens:          45,
		CachedInputTokens:     200,
		ReasoningOutputTokens: 15,
	}
	if *got.Usage != want {
		t.Fatalf("usage=%+v, want %+v", *got.Usage, want)
	}
}

func TestChatStreamParsesResponseModel(t *testing.T) {
	// 末尾 usage-only chunk 不带 model 字段时，不能覆盖前面已解析到的实际模型名称。
	data := strings.Join([]string{
		`data: {"model":" gemini-2.5-pro-preview-05-06 ","choices":[{"delta":{"content":"Hel"}}]}`,
		`data: {"model":"gemini-2.5-pro-preview-05-06","choices":[{"delta":{"content":"lo"}}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10}}`,
		`data: [DONE]`,
	}, "\n")
	got, err := readChatStream(strings.NewReader(data), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResponseModel != "gemini-2.5-pro-preview-05-06" {
		t.Fatalf("ResponseModel=%q, want gemini-2.5-pro-preview-05-06", got.ResponseModel)
	}
}

