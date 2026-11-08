package agent

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentconfig"
)

type captureSummarizerLLM struct {
	lastPrompt string
	reply      string
}

func (m *captureSummarizerLLM) Name() string {
	return "capture-summarizer-llm"
}

func (m *captureSummarizerLLM) GenerateContent(
	_ context.Context,
	req *model.LLMRequest,
	_ bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var builder strings.Builder
		for _, content := range req.Contents {
			if content == nil {
				continue
			}
			for _, part := range content.Parts {
				if part != nil && part.Text != "" {
					builder.WriteString(part.Text)
				}
			}
		}
		m.lastPrompt = builder.String()
		yield(&model.LLMResponse{
			Content:      genai.NewContentFromText(m.reply, genai.RoleModel),
			FinishReason: genai.FinishReasonStop,
			TurnComplete: true,
		}, nil)
	}
}

func TestCompactionPromptTemplate_包含结构化章节与防退化规则(t *testing.T) {
	if !strings.Contains(compactionPromptTemplate, compaction.ConversationHistoryPlaceholder) {
		t.Fatalf("compactionPromptTemplate 缺少占位符 %q", compaction.ConversationHistoryPlaceholder)
	}

	llm := &captureSummarizerLLM{reply: "Conversation Language: 中文\n\n## Objective\n修复订单校验"}
	summarizer, err := compaction.NewLLMSummarizer(compaction.LLMSummarizerConfig{
		Model:               llm,
		PromptTemplate:      compactionPromptTemplate,
		MaxToolContentChars: 4000,
		MaxTranscriptChars:  120000,
		Timeout:             agentconfig.DefaultModelRequestTimeout,
	})
	if err != nil {
		t.Fatalf("创建 LLMSummarizer 失败: %v", err)
	}

	now := time.Now().UTC()
	events := []*session.Event{
		{
			ID:        "ev-1",
			Author:    "user",
			Timestamp: now,
			LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText("请排查 internal/service/order.go 的 ValidateOrder 空指针问题并完成修复。", genai.RoleUser),
			},
		},
		{
			ID:        "ev-2",
			Author:    "cycle_agent",
			Timestamp: now.Add(time.Second),
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						genai.NewPartFromFunctionCall("call_mcp_tool", map[string]any{
							"tool_id":   "workspace:read_file",
							"arguments": map[string]any{"path": "internal/service/order.go"},
						}),
					},
				},
			},
		},
		{
			ID:        "ev-3",
			Author:    "cycle_agent",
			Timestamp: now.Add(2 * time.Second),
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						genai.NewPartFromFunctionResponse("call_mcp_tool", map[string]any{
							"artifact_ref": "artifact://workspace/card/tool-output-1",
							"version":      1,
							"preview":      "FAIL: TestValidateOrderNilItems panic at internal/service/order.go:84\n" + strings.Repeat("stack trace line\n", 400),
						}),
					},
				},
			},
		},
	}

	result, err := summarizer.SummarizeEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("SummarizeEvents 执行失败: %v", err)
	}
	if result.Content == nil {
		t.Fatal("SummarizeEvents 返回了空摘要")
	}

	requiredHeadings := []string{
		"Conversation Language:",
		"## Objective",
		"## Requirements",
		"## Decisions",
		"## Work State",
		"### Completed",
		"### Active",
		"### Blocked",
		"## Next Move",
		"## Relevant Files",
		"## Important Context",
	}
	for _, heading := range requiredHeadings {
		if !strings.Contains(llm.lastPrompt, heading) {
			t.Errorf("发往压缩模型的 Prompt 缺少章节或指令 %q", heading)
		}
	}

	// 验证单条工具输出超过 4,000 字符被截断时，头部的错误结论与外置 Artifact 引用仍然完整保留在压缩输入中。
	for _, preserved := range []string{
		"artifact://workspace/card/tool-output-1",
		"FAIL: TestValidateOrderNilItems panic at internal/service/order.go:84",
		"[truncated ",
	} {
		if !strings.Contains(llm.lastPrompt, preserved) {
			t.Errorf("发往压缩模型的 Prompt 未保留关键证据 %q: %s", preserved, llm.lastPrompt)
		}
	}
	if strings.Contains(llm.lastPrompt, compaction.ConversationHistoryPlaceholder) {
		t.Error("发往压缩模型的 Prompt 未替换 {conversation_history} 占位符")
	}
}
