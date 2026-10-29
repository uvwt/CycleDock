package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

// 只读取顶层 content 字符串；其他参数必须完整跳过，不能把嵌套内容或原始 JSON 展示给用户。
func replyPreviewContent(arguments string) string {
	decoder := json.NewDecoder(strings.NewReader(arguments))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ""
		}
		if key == "content" {
			rest := strings.TrimSpace(arguments[decoder.InputOffset():])
			if !strings.HasPrefix(rest, ":") {
				return ""
			}
			return partialReplyString(strings.TrimSpace(rest[1:]))
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ""
		}
	}
	return ""
}

// 等待转义序列、UTF-8 字符和 UTF-16 代理对完整后再预览，避免短暂出现乱码或 JSON 转义符。
func partialReplyString(raw string) string {
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	end := 1
	for end < len(raw) && end <= 20000 {
		if raw[end] == '"' {
			var text string
			if json.Unmarshal([]byte(raw[:end+1]), &text) == nil {
				return text
			}
			return ""
		}
		size := 1
		if raw[end] == '\\' {
			if end+1 >= len(raw) {
				break
			}
			size = 2
			if raw[end+1] == 'u' {
				if end+6 > len(raw) {
					break
				}
				code, err := strconv.ParseUint(raw[end+2:end+6], 16, 16)
				if err != nil {
					return ""
				}
				size = 6
				if code >= 0xD800 && code <= 0xDBFF {
					if end+12 > len(raw) {
						break
					}
					if raw[end+6:end+8] != `\u` {
						return ""
					}
					low, err := strconv.ParseUint(raw[end+8:end+12], 16, 16)
					if err != nil || low < 0xDC00 || low > 0xDFFF {
						return ""
					}
					size = 12
				} else if utf16.IsSurrogate(rune(code)) {
					return ""
				}
			}
		} else {
			character, width := utf8.DecodeRuneInString(raw[end:])
			if character == utf8.RuneError && width == 1 {
				break
			}
			size = width
		}
		if end+size > 20001 {
			break
		}
		end += size
	}
	var text string
	if json.Unmarshal([]byte(raw[:end]+`"`), &text) != nil {
		return ""
	}
	return text
}

// CustomMetadata 只携带批次和显示顺序；正文使用 ADK 官方 FunctionCall.PartialArgs。
const (
	replyGenerationKey = "cycledock.reply_generation"
	replyOrderKey      = "cycledock.reply_order"
	modelStageKey      = "cycledock.model_stage"
)

func replyGenerationResponse(generation string) *model.LLMResponse {
	return &model.LLMResponse{Partial: true,
		Content:        &genai.Content{Role: string(genai.RoleModel)},
		CustomMetadata: map[string]any{replyGenerationKey: generation},
	}
}

func modelStageResponse(stage string) *model.LLMResponse {
	return &model.LLMResponse{Partial: true,
		Content:        &genai.Content{Role: string(genai.RoleModel)},
		CustomMetadata: map[string]any{modelStageKey: stage},
	}
}

func previewToolCalls(generation string, modelCall int, emit func(*model.LLMResponse) bool) func(modelclient.ToolCall) {
	sent := make(map[string]string)
	return func(call modelclient.ToolCall) {
		if call.ID == "" || len(call.ID) > 400 || call.Index < 0 || call.Index >= 128 || call.Function.Name != "reply_message" {
			return
		}
		text := replyPreviewContent(call.Function.Arguments)
		previous := sent[call.ID]
		if text == previous || !strings.HasPrefix(text, previous) {
			return
		}
		sent[call.ID] = text
		ongoing := true
		emit(&model.LLMResponse{
			Partial: true,
			Content: &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{
				FunctionCall: &genai.FunctionCall{ID: call.ID, Name: call.Function.Name,
					WillContinue: &ongoing,
					PartialArgs:  []*genai.PartialArg{{JsonPath: "$.content", StringValue: text[len(previous):], WillContinue: &ongoing}},
				},
			}}},
			CustomMetadata: map[string]any{replyGenerationKey: generation, replyOrderKey: modelCall*128 + call.Index},
		})
	}
}

// 只持久化可见正文，不把不完整工具参数或内部推理写入 ADK Session。
type replyEventPublisher struct {
	store             *store.Store
	context           store.AgentCardContext
	replies           *replyState
	texts             map[string]string
	hub               *realtime.Hub
	cardID, attemptID uuid.UUID
	generation        string
	previewIDs        map[string]string
}

func (p *replyEventPublisher) publish(ctx context.Context, event *session.Event) error {
	if event == nil || event.Content == nil {
		return nil
	}
	if event.Partial {
		if stage, ok := event.CustomMetadata[modelStageKey].(string); ok {
			p.emit(p.cardID, realtime.Event{Type: "attempt.stage", AttemptID: p.attemptID.String(), Stage: stage})
		}
		generation, ok := event.CustomMetadata[replyGenerationKey].(string)
		if !ok || generation == "" {
			return nil
		}
		if p.generation != generation {
			p.generation = generation
			p.previewIDs = make(map[string]string)
			p.texts = make(map[string]string)
			if p.replies != nil {
				p.replies.mu.Lock()
				p.replies.previewIDs = make(map[string]string)
				p.replies.mu.Unlock()
			}
			p.emit(p.cardID, realtime.Event{Type: "reply.reset", AttemptID: p.attemptID.String()})
		}
		order, _ := event.CustomMetadata[replyOrderKey].(int)
		for _, part := range event.Content.Parts {
			if part == nil || part.Thought || part.FunctionCall == nil {
				continue
			}
			call := part.FunctionCall
			if call.Name != "reply_message" || call.ID == "" {
				continue
			}
			id := generation + ":" + call.ID
			for _, arg := range call.PartialArgs {
				if arg == nil || arg.JsonPath != "$.content" || arg.StringValue == "" {
					continue
				}
				p.texts[id] += arg.StringValue
				if p.store != nil {
					if err := p.store.SaveReplyPreview(ctx, store.ReplyPreviewParams{
						WorkspaceID: p.context.WorkspaceID, CardID: p.cardID,
						AttemptID: p.attemptID, RunnerSourceID: p.context.RunnerSourceID,
						PreviewID: id, Content: p.texts[id],
					}); err != nil {
						return err
					}
				}
				p.previewIDs[call.ID] = id
				if p.replies != nil {
					p.replies.mu.Lock()
					if p.replies.previewIDs == nil {
						p.replies.previewIDs = make(map[string]string)
					}
					p.replies.previewIDs[call.ID] = id
					p.replies.mu.Unlock()
				}
				p.emit(p.cardID, realtime.Event{Type: "reply.delta", AttemptID: p.attemptID.String(), ReplyID: id, Text: arg.StringValue, Order: order})
			}
		}
		return nil
	}
	// 工具成功响应由 ADK 转发后再发布提交关联，包含 end_turn 后被拒绝的同批工具响应。
	for _, part := range event.Content.Parts {
		if part == nil || part.FunctionResponse == nil || part.FunctionResponse.Name != "reply_message" {
			continue
		}
		response := part.FunctionResponse
		id := p.previewIDs[response.ID]
		if id == "" {
			continue
		}
		update := realtime.Event{Type: "reply.discarded", AttemptID: p.attemptID.String(), ReplyID: id}
		if raw, ok := response.Response["event_id"].(string); ok {
			if eventID, err := uuid.Parse(raw); err == nil {
				update.Type = "reply.committed"
				update.EventID = eventID.String()
			}
		}
		p.emit(p.cardID, update)
	}
	return nil
}

func (p *replyEventPublisher) emit(cardID uuid.UUID, event realtime.Event) {
	if p.hub != nil {
		p.hub.Publish(cardID, event)
	}
}
