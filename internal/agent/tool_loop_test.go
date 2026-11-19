package agent

import (
	"fmt"
	"testing"

	"github.com/uvwt/CycleDock/internal/modelclient"
)

func TestRepeatedToolLoopRequiresCompleteUnchangedRounds(t *testing.T) {
	for _, test := range []struct {
		name   string
		rounds int
		change func([]modelclient.Message)
		want   bool
	}{
		{name: "eight unchanged rounds", rounds: 8, want: true},
		{name: "seven rounds are allowed", rounds: 7},
		{name: "new input resets repetition", rounds: 8, change: func(messages []modelclient.Message) {
			messages[6] = modelclient.Message{Role: "user", Content: "继续新任务"}
		}},
		{name: "changed arguments reset repetition", rounds: 8, change: func(messages []modelclient.Message) {
			messages[6].ToolCalls[0].Function.Arguments = `{"item":2}`
		}},
		{name: "changed results reset repetition", rounds: 8, change: func(messages []modelclient.Message) {
			messages[7].Content = `{"state":"done"}`
		}},
		{name: "mismatched response is not a complete round", rounds: 8, change: func(messages []modelclient.Message) {
			messages[7].ToolCallID = "other-call"
		}},
		{name: "unfinished parallel calls are not a complete round", rounds: 8, change: func(messages []modelclient.Message) {
			messages[6].ToolCalls = append(messages[6].ToolCalls, modelclient.ToolCall{ID: "pending"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var messages []modelclient.Message
			for i := 0; i < test.rounds; i++ {
				id := fmt.Sprintf("call-%d", i)
				messages = append(messages,
					modelclient.Message{Role: "assistant", Content: fmt.Sprintf("检查进度 %d", i), ToolCalls: []modelclient.ToolCall{{
						ID: id, Type: "function", Function: modelclient.ToolFunction{Name: "inspect", Arguments: `{"item":1}`},
					}}},
					modelclient.Message{Role: "tool", ToolCallID: id, Name: "inspect", Content: `{"state":"pending"}`},
				)
			}
			if test.change != nil {
				test.change(messages)
			}
			if got := repeatedToolLoop(messages); got != test.want {
				t.Fatalf("repeated=%v, want %v", got, test.want)
			}
		})
	}
}

func TestRepeatedToolLoopSkipsSyntheticToolImageUserMessages(t *testing.T) {
	var messages []modelclient.Message
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("call-%d", i)
		messages = append(messages,
			modelclient.Message{
				Role: "assistant",
				ToolCalls: []modelclient.ToolCall{{
					ID:       id,
					Type:     "function",
					Function: modelclient.ToolFunction{Name: "phone_screenshot", Arguments: `{}`},
				}},
			},
			modelclient.Message{
				Role:       "tool",
				ToolCallID: id,
				Name:       "phone_screenshot",
				Content:    `{"image_artifacts":[{"artifact_ref":"shot.png","artifact_version":1}]}`,
			},
			modelclient.Message{
				Role: "user",
				Parts: []modelclient.ContentPart{
					modelclient.NewTextPart("Image #1 returned by tool phone_screenshot:"),
					modelclient.NewImagePartFromBytes("image/png", []byte{0x89, 0x50, 0x4e, 0x47}),
				},
			},
		)
	}
	if !repeatedToolLoop(messages) {
		t.Fatal("expected repeatedToolLoop=true when identical screenshot calls repeat 8 times even with synthetic image user messages")
	}
}
