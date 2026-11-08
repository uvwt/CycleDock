package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	artifactExternalizeChars = 12000
	artifactPreviewChars     = 4000
	artifactReadDefaultChars = 12000
	artifactReadMaxChars     = 20000
)

type attemptToolRuntime struct {
	store           *store.Store
	context         store.AgentCardContext
	mcpSessions     *mcpclient.SessionSet
	artifactService artifact.Service
	historyService  *agentsession.PostgresService
	replies         *replyState
	live            *realtime.Hub
	activeModel     func() string
}

func (r attemptToolRuntime) activeModelName() string {
	if r.activeModel == nil {
		return ""
	}
	return r.activeModel()
}

type replyState struct {
	mu         sync.Mutex
	ended      bool
	previewIDs map[string]string
}

func buildADKTools(runtime attemptToolRuntime) ([]tool.Tool, error) {
	definitions := internalToolDefinitions()
	// cycledock_inspect_card_session 仅供 Discussion（主聊天/全局协调）会话挂载。
	// Todo 与 Ongoing 等具体工作卡片相互自治，禁止翻阅彼此底层会话，协作必须通过 cycledock_send_card_message 显式委派。
	if runtime.context.CardKind != domain.CardKindDiscussion {
		filtered := make([]modelclient.ToolDefinition, 0, len(definitions))
		for _, definition := range definitions {
			if definition.Function.Name != "cycledock_inspect_card_session" {
				filtered = append(filtered, definition)
			}
		}
		definitions = filtered
	}
	if runtime.context.IsBuiltInProactive {
		// 内置定时主动任务只允许查询信息并最终向主聊天发送 1 条气泡，不挂载建卡或改日程等写工具。
		filtered := make([]modelclient.ToolDefinition, 0, 3)
		for _, definition := range definitions {
			switch definition.Function.Name {
			case "reply_message", "search_mcp_tools", "call_mcp_tool":
				filtered = append(filtered, definition)
			}
		}
		definitions = filtered
	}
	if runtime.artifactService != nil {
		definitions = append(definitions, artifactToolDefinition())
	}
	if runtime.historyService != nil {
		definitions = append(definitions, historyToolDefinition())
	}

	out := make([]tool.Tool, 0, len(definitions))
	for _, definition := range definitions {
		handler, err := runtime.internalHandler(definition.Function.Name)
		if err != nil {
			return nil, err
		}
		converted, err := definitionToADKTool(definition, handler)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}

	if runtime.mcpSessions != nil {
		for _, definition := range runtime.mcpSessions.NativeToolDefinitions() {
			name := definition.Function.Name
			converted, err := definitionToADKTool(
				definition,
				func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
					raw, err := runtime.mcpSessions.Call(ctx, name, args)
					if err != nil {
						// 通道错误是这一次调用的结果，交回模型用白话说明。
						// 标成整轮失败会停在上一条进度，用户看不到这次为什么没做成。
						return map[string]any{"error": err.Error()}, nil
					}
					if decoded, err := decodeADKToolResult(raw); err == nil {
						runtime.publishNexusTask(name, decoded)
					}
					return runtime.prepareMCPResult(ctx, name, raw)
				},
			)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
	}

	return out, nil
}

func definitionToADKTool(
	definition modelclient.ToolDefinition,
	handler func(adkagent.Context, map[string]any) (map[string]any, error),
) (tool.Tool, error) {
	schema, err := jsonSchemaFromMap(definition.Function.Parameters)
	if err != nil {
		return nil, fmt.Errorf("convert tool %q schema: %w", definition.Function.Name, err)
	}
	return functiontool.New[map[string]any, map[string]any](
		functiontool.Config{
			Name:        definition.Function.Name,
			Description: definition.Function.Description,
			InputSchema: schema,
		},
		handler,
	)
}

func jsonSchemaFromMap(value map[string]any) (*jsonschema.Schema, error) {
	if value == nil {
		value = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}

func (r attemptToolRuntime) internalHandler(
	name string,
) (func(adkagent.Context, map[string]any) (map[string]any, error), error) {
	switch name {
	case "search_mcp_tools":
		return r.searchMCPToolsTool, nil
	case "call_mcp_tool":
		return r.callMCPToolTool, nil
	case "cycledock_list_schedules":
		return r.listSchedulesTool, nil
	case "cycledock_create_schedule":
		return r.createScheduleTool, nil
	case "cycledock_update_schedule":
		return r.updateScheduleTool, nil
	case "cycledock_delete_schedule":
		return r.deleteScheduleTool, nil
	case "reply_message":
		return func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
			// 回复状态同时由 Runner 的工具边界检查读取。
			r.replies.mu.Lock()
			defer r.replies.mu.Unlock()
			if r.replies.ended {
				return nil, fmt.Errorf("reply_message: turn already ended")
			}
			raw, err := json.Marshal(args)
			if err != nil {
				return nil, err
			}
			var input struct {
				store.ReplyMessage
				IdempotencyKey string `json:"idempotency_key"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			if r.context.IsBuiltInProactive {
				if !input.EndTurn {
					return map[string]any{
						"error": "内置主动消息轮次禁止发送中间进度气泡（end_turn=false）；请先完成所有只读查询，最后只调用一次 end_turn=true 的 reply_message 发送最终的 1 条气泡。",
					}, nil
				}
				if strings.TrimSpace(input.Type) == "choice" {
					return map[string]any{
						"error": "内置主动消息轮次仅支持发送普通气泡（type=text），不支持 choice 提问。",
					}, nil
				}
			}
			eventID, reused, err := r.store.PublishReply(ctx, store.PublishReplyParams{
				WorkspaceID: r.context.WorkspaceID, CardID: r.context.CardID,
				AttemptID: r.context.AttemptID, RunnerSourceID: r.context.RunnerSourceID,
				Message: input.ReplyMessage, IdempotencyKey: input.IdempotencyKey,
				PreviewID: r.replies.previewIDs[ctx.FunctionCallID()],
				Model:     r.activeModelName(),
			})
			if err != nil {
				return nil, err
			}
			if input.EndTurn {
				r.replies.ended = true
				// 先保存 ADK FunctionResponse，然后停止循环；Runner 负责正常结算。
				ctx.Actions().SkipSummarization = true
			}
			return map[string]any{
				"ok": true, "event_id": eventID.String(), "reused": reused,
			}, nil
		}, nil

	case "cycledock_check_in":
		return func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
			status := domain.CheckinStatus(stringArg(args, "status"))
			eventID, reused, err := r.store.AddAttemptEvent(ctx, store.AttemptEventParams{
				WorkspaceID:    r.context.WorkspaceID,
				CardID:         r.context.CardID,
				AttemptID:      r.context.AttemptID,
				RunnerSourceID: r.context.RunnerSourceID,
				Type:           domain.EventTypeCheckin,
				Content:        stringArg(args, "content"),
				Checkin:        &status,
				IdempotencyKey: stringArg(args, "idempotency_key"),
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"ok": true, "event_id": eventID.String(), "reused": reused,
			}, nil
		}, nil

	case "cycledock_generate_work":
		return func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
			kind := domain.CardKind(stringArg(args, "kind"))
			required := int32Arg(args, "required_confirmations")
			result, err := r.store.GenerateWorkFromDiscussion(ctx, store.GenerateWorkParams{
				WorkspaceID:           r.context.WorkspaceID,
				DiscussionID:          r.context.CardID,
				SourceID:              r.context.RunnerSourceID,
				Kind:                  kind,
				Title:                 stringArg(args, "title"),
				Description:           stringArg(args, "description"),
				RequiredConfirmations: required,
				IdempotencyKey:        stringArg(args, "idempotency_key"),
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"ok":      true,
				"card_id": result.Card.ID.String(),
				"kind":    result.Card.Kind,
				"reused":  result.Reused,
			}, nil
		}, nil

	case "cycledock_send_card_message":
		return func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
			cardID, err := uuid.Parse(stringArg(args, "card_id"))
			if err != nil {
				return nil, errors.New("card_id must be a valid UUID")
			}
			if cardID == r.context.CardID {
				return nil, errors.New("target card must differ from the current card")
			}
			// Workspace 和发送者来自当前执行身份；记录源会话卡片以支持跨卡片溯源与跳转，不复制源 Session。
			result, err := r.store.SubmitAgentTurn(
				ctx, r.context.WorkspaceID, cardID, r.context.RunnerSourceID,
				stringArg(args, "content"), stringArg(args, "idempotency_key"),
				uuid.NullUUID{UUID: r.context.CardID, Valid: true},
			)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"ok": true, "card_id": cardID.String(),
				"event_id": result.EventID.String(), "turn_id": result.TurnID.String(),
				"reused": result.Reused,
			}, nil
		}, nil

	case "cycledock_confirm_card":
		return func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
			result, err := r.store.ConfirmCard(
				ctx,
				r.context.WorkspaceID,
				r.context.CardID,
				r.context.RunnerSourceID,
				stringArg(args, "content"),
				stringArg(args, "idempotency_key"),
			)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"ok":                 true,
				"event_id":           result.EventID.String(),
				"confirmation_count": result.ConfirmationCount,
				"moved_to_review":    result.MovedToReview,
				"reused":             result.Reused,
			}, nil
		}, nil

	case "cycledock_inspect_card_session":
		return func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
			// 仅允许 Discussion 卡片查阅下属具体工作卡片的会话；Todo / Ongoing 之间必须通过消息委派。
			if r.context.CardKind != domain.CardKindDiscussion {
				return nil, errors.New("only discussion cards can inspect other card sessions; use cycledock_send_card_message to communicate between work cards")
			}
			cardIDStr := stringArg(args, "card_id")
			targetCardID := r.context.CardID
			if strings.TrimSpace(cardIDStr) != "" {
				parsed, err := uuid.Parse(strings.TrimSpace(cardIDStr))
				if err != nil {
					return nil, fmt.Errorf("invalid card_id: %w", err)
				}
				targetCardID = parsed
			}

			mode := strings.ToLower(strings.TrimSpace(stringArg(args, "mode")))
			if mode == "" {
				mode = "simple"
			}
			if mode != "simple" && mode != "full" {
				return nil, fmt.Errorf("unsupported mode %q: must be 'simple' or 'full'", mode)
			}

			if r.store == nil {
				return nil, errors.New("store is not configured")
			}

			// Validate target card existence and make sure it is a non-discussion card within the current workspace.
			targetCard, err := r.store.GetCard(ctx, r.context.WorkspaceID, targetCardID)
			if err != nil {
				return nil, fmt.Errorf("target card not found in current workspace: %w", err)
			}
			if targetCard.Kind == string(domain.CardKindDiscussion) {
				return nil, errors.New("cannot inspect discussion cards with cycledock_inspect_card_session; only todo and ongoing cards are allowed")
			}

			limit := 50
			if val, ok := args["limit"].(float64); ok && val > 0 {
				limit = int(val)
			} else if val, ok := args["limit"].(int); ok && val > 0 {
				limit = val
			}
			if limit > 200 {
				limit = 200
			}

			if mode == "full" {
				if r.historyService == nil {
					return nil, errors.New("history service is not configured for full session inspection")
				}
				rounds, err := r.historyService.InspectFullSession(ctx, r.context.WorkspaceID, targetCardID, limit)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"card_id":    targetCardID.String(),
					"card_title": targetCard.Title,
					"card_kind":  string(targetCard.Kind),
					"mode":       "full",
					"count":      len(rounds),
					"rounds":     rounds,
				}, nil
			}

			// Simple mode: extract user-visible bubbles from card timeline.
			timeline, err := r.store.ListCardTimeline(ctx, r.context.WorkspaceID, targetCardID)
			if err != nil {
				return nil, fmt.Errorf("list card timeline: %w", err)
			}

			type bubbleItem struct {
				Author    string    `json:"author"`
				Content   string    `json:"content"`
				Timestamp time.Time `json:"timestamp"`
			}
			type timelineMessage struct {
				Type    string `json:"type"`
				Content string `json:"content"`
				Widget  *struct {
					Question string `json:"question"`
					Options  []struct {
						Value string `json:"value"`
						Label string `json:"label"`
					} `json:"options"`
				} `json:"widget"`
			}
			bubbles := make([]bubbleItem, 0, len(timeline))
			for _, event := range timeline {
				author := "Cycle"
				if event.ActorKind == "human" {
					if event.ActorName != "" {
						author = event.ActorName
					} else {
						author = "User"
					}
				} else if event.ActorName != "" && event.ActorName != "Cycle Agent" {
					author = event.ActorName
				}

				var content string
				if len(event.Message) > 0 {
					var msg timelineMessage
					if err := json.Unmarshal(event.Message, &msg); err == nil {
						content = msg.Content
						if msg.Type == "choice" && msg.Widget != nil {
							var choiceLines []string
							if strings.TrimSpace(content) != "" {
								choiceLines = append(choiceLines, content)
							}
							if strings.TrimSpace(msg.Widget.Question) != "" {
								choiceLines = append(choiceLines, msg.Widget.Question)
							}
							for i, opt := range msg.Widget.Options {
								label := opt.Label
								if label == "" {
									label = opt.Value
								}
								choiceLines = append(choiceLines, fmt.Sprintf("%d. %s", i+1, label))
							}
							content = strings.Join(choiceLines, "\n")
						}
					}
				}
				if strings.TrimSpace(content) == "" && strings.TrimSpace(event.Content) != "" {
					content = event.Content
				}
				if strings.TrimSpace(content) == "" {
					continue
				}

				bubbles = append(bubbles, bubbleItem{
					Author:    author,
					Content:   content,
					Timestamp: event.CreatedAt.Time,
				})
			}

			if len(bubbles) > limit {
				bubbles = bubbles[len(bubbles)-limit:]
			}

			return map[string]any{
				"card_id":    targetCardID.String(),
				"card_title": targetCard.Title,
				"card_kind":  string(targetCard.Kind),
				"mode":       "simple",
				"count":      len(bubbles),
				"bubbles":    bubbles,
			}, nil
		}, nil

	case "cycledock_read_artifact":
		return func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
			if r.artifactService == nil {
				return nil, errors.New("artifact service is not configured")
			}
			fileName := stringArg(args, "file_name")
			if fileName == "" {
				return nil, errors.New("file_name is required")
			}
			version := int64Arg(args, "version")
			offset := intArg(args, "offset")
			if offset < 0 {
				return nil, errors.New("offset must be non-negative")
			}
			maxChars := intArg(args, "max_chars")
			if maxChars <= 0 {
				maxChars = artifactReadDefaultChars
			}
			if maxChars > artifactReadMaxChars {
				maxChars = artifactReadMaxChars
			}

			appName, userID, sessionID := agentsession.Identity(
				r.context.WorkspaceID, r.context.CardID,
			)
			metadata, err := r.artifactService.GetArtifactVersion(ctx, &artifact.GetArtifactVersionRequest{
				AppName: appName, UserID: userID, SessionID: sessionID,
				FileName: fileName, Version: version,
			})
			if err != nil {
				return nil, err
			}
			resolvedVersion := metadata.ArtifactVersion.Version
			loaded, err := r.artifactService.Load(ctx, &artifact.LoadRequest{
				AppName: appName, UserID: userID, SessionID: sessionID,
				FileName: fileName, Version: resolvedVersion,
			})
			if err != nil {
				return nil, err
			}
			content, err := artifactPartText(loaded.Part)
			if err != nil {
				return nil, err
			}
			runes := []rune(content)
			if offset > len(runes) {
				return nil, fmt.Errorf("offset %d exceeds artifact length %d", offset, len(runes))
			}
			end := min(offset+maxChars, len(runes))
			return map[string]any{
				"file_name":     fileName,
				"version":       resolvedVersion,
				"content":       string(runes[offset:end]),
				"offset":        offset,
				"next_offset":   end,
				"total_chars":   len(runes),
				"truncated":     end < len(runes),
				"canonical_uri": metadata.ArtifactVersion.CanonicalURI,
				"mime_type":     metadata.ArtifactVersion.MimeType,
			}, nil
		}, nil
	case "cycledock_search_session_history":
		return func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
			if r.historyService == nil {
				return nil, errors.New("session history service is not configured")
			}
			hits, err := r.historyService.SearchHistory(
				ctx,
				r.context.WorkspaceID,
				r.context.CardID,
				stringArg(args, "query"),
				intArg(args, "limit"),
			)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(hits))
			for _, hit := range hits {
				items = append(items, map[string]any{
					"event_id":      hit.EventID,
					"invocation_id": hit.InvocationID,
					"occurred_at":   hit.OccurredAt.UTC().Format(time.RFC3339Nano),
					"author":        hit.Author,
					"content":       hit.Content,
				})
			}
			return map[string]any{"hits": items, "count": len(items)}, nil
		}, nil
	default:
		return nil, fmt.Errorf("unsupported CycleDock ADK tool %q", name)
	}
}

func artifactToolDefinition() modelclient.ToolDefinition {
	return modelclient.ToolDefinition{
		Type: "function",
		Function: modelclient.ToolDefinitionFunc{
			Name:        "cycledock_read_artifact",
			Description: "Read a chunk of a large prior tool result that CycleDock externalized from this Card Session. Use artifact_ref from an earlier FunctionResponse; do not re-run the real tool just to recover old evidence.",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"file_name": map[string]any{"type": "string"},
					"version":   map[string]any{"type": "integer", "minimum": 0},
					"offset":    map[string]any{"type": "integer", "minimum": 0},
					"max_chars": map[string]any{"type": "integer", "minimum": 1, "maximum": artifactReadMaxChars},
				},
				"required": []string{"file_name"},
			},
		},
	}
}

func historyToolDefinition() modelclient.ToolDefinition {
	return modelclient.ToolDefinition{
		Type: "function",
		Function: modelclient.ToolDefinitionFunc{
			Name:        "cycledock_search_session_history",
			Description: "Search exact persisted events from older turns in this Card Session when compacted context no longer contains a specific prior value. Search history before re-running a real read-only tool merely to recover old evidence.",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "A specific remembered value, tool name, resource name, or phrase to find in this Card's persisted Session history.",
					},
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 12},
				},
				"required": []string{"query"},
			},
		},
	}
}

func (r attemptToolRuntime) prepareMCPResult(
	ctx context.Context,
	toolName, raw string,
) (map[string]any, error) {
	decoded, err := decodeADKToolResult(raw)
	if err != nil {
		return nil, err
	}
	safe := agentdata.Sanitize(decoded).(map[string]any)
	encoded, err := json.Marshal(safe)
	if err != nil {
		return nil, fmt.Errorf("encode MCP result: %w", err)
	}
	if r.artifactService == nil || len([]rune(string(encoded))) <= artifactExternalizeChars {
		return safe, nil
	}

	appName, userID, sessionID := agentsession.Identity(
		r.context.WorkspaceID, r.context.CardID,
	)
	fileName := fmt.Sprintf(
		"tool-result-%s-%s.json",
		r.context.AttemptID,
		uuid.NewString(),
	)
	saved, err := r.artifactService.Save(ctx, &artifact.SaveRequest{
		AppName: appName, UserID: userID, SessionID: sessionID, FileName: fileName,
		Part: genai.NewPartFromBytes(encoded, "application/json"),
		CustomMetadata: map[string]any{
			"tool_name":  toolName,
			"attempt_id": r.context.AttemptID.String(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("externalize MCP result: %w", err)
	}
	runes := []rune(string(encoded))
	previewEnd := min(artifactPreviewChars, len(runes))
	return map[string]any{
		"artifact_ref":     fileName,
		"artifact_version": saved.Version,
		"preview":          string(runes[:previewEnd]),
		"total_chars":      len(runes),
		"truncated":        true,
	}, nil
}

func artifactPartText(part *genai.Part) (string, error) {
	switch {
	case part == nil:
		return "", errors.New("artifact payload is empty")
	case part.InlineData != nil:
		return string(part.InlineData.Data), nil
	case part.Text != "":
		return part.Text, nil
	default:
		return "", errors.New("artifact payload is not readable as text")
	}
}

func decodeADKToolResult(raw string) (map[string]any, error) {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return map[string]any{"result": raw}, nil
	}
	if object, ok := value.(map[string]any); ok {
		return object, nil
	}
	return map[string]any{"result": value}, nil
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func int32Arg(args map[string]any, key string) int32 {
	return int32(int64Arg(args, key))
}

func intArg(args map[string]any, key string) int {
	return int(int64Arg(args, key))
}

func int64Arg(args map[string]any, key string) int64 {
	switch value := args[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case json.Number:
		parsed, _ := strconv.ParseInt(value.String(), 10, 64)
		return parsed
	default:
		return 0
	}
}

func (r attemptToolRuntime) searchMCPToolsTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}

	// 1. Workspace scope 来自认证上下文（当前 Attempt 的 CardContext），不信任模型传入的 scope。
	enabledConnections, err := r.store.ListEnabledMCPConnections(ctx, r.context.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("list enabled MCP connections: %w", err)
	}

	query := strings.TrimSpace(stringArg(args, "query"))
	connFilter := strings.TrimSpace(stringArg(args, "connection_name"))
	toolIDFilter := strings.TrimSpace(stringArg(args, "tool_id"))
	limit := intArg(args, "limit")
	if limit <= 0 {
		limit = 5
	}
	if limit > 15 {
		limit = 15
	}

	if len(enabledConnections) == 0 || r.mcpSessions == nil {
		if connFilter != "" {
			return map[string]any{
				"tools":   []any{},
				"count":   0,
				"message": fmt.Sprintf("MCP 连接 %q 在当前工作区不存在或未启用。", connFilter),
			}, nil
		}
		return map[string]any{
			"tools":   []any{},
			"count":   0,
			"message": "当前工作区未启用任何外部 MCP 连接。可在工作区设置中配置并启用 MCP 连接。",
		}, nil
	}

	enabledMap := make(map[string]bool, len(enabledConnections))
	for _, c := range enabledConnections {
		enabledMap[strings.ToLower(c.Name)] = true
	}

	// 若指定了连接名称过滤，先检查该连接是否存在且已启用
	if connFilter != "" && !enabledMap[strings.ToLower(connFilter)] {
		return map[string]any{
			"tools":   []any{},
			"count":   0,
			"message": fmt.Sprintf("MCP 连接 %q 在当前工作区不存在或未启用。", connFilter),
		}, nil
	}

	// 3. 获取所有外部 MCP 工具，并仅保留当前工作区启用的连接中的工具
	allExternal := r.mcpSessions.ExternalTools()
	var candidates []mcpclient.Tool
	for _, tool := range allExternal {
		if enabledMap[strings.ToLower(tool.ServerName)] {
			if connFilter == "" || strings.EqualFold(tool.ServerName, connFilter) {
				candidates = append(candidates, tool)
			}
		}
	}

	// 4. 工具评分与匹配：支持自然语言关键词、可选连接筛选与精确工具 ID
	type scoredTool struct {
		tool  mcpclient.Tool
		score int
	}

	var matched []scoredTool

	// 4.1 精确 tool_id 匹配
	if toolIDFilter != "" {
		for _, tool := range candidates {
			toolID := tool.ToolID()
			if strings.EqualFold(toolID, toolIDFilter) ||
				strings.EqualFold(tool.ServerName+"/"+tool.RemoteName, toolIDFilter) {
				matched = append(matched, scoredTool{tool: tool, score: 1000})
			}
		}
		// 若未在缓存中直接匹配到，尝试刷新一次服务器工具目录以避免遗漏
		if len(matched) == 0 {
			if sName, _, ok := mcpclient.ParseToolID(toolIDFilter); ok && enabledMap[strings.ToLower(sName)] {
				_ = r.mcpSessions.RefreshServer(ctx, sName)
				for _, tool := range r.mcpSessions.ExternalTools() {
					if enabledMap[strings.ToLower(tool.ServerName)] &&
						(strings.EqualFold(tool.ToolID(), toolIDFilter) ||
							strings.EqualFold(tool.ServerName+"/"+tool.RemoteName, toolIDFilter)) {
						matched = append(matched, scoredTool{tool: tool, score: 1000})
					}
				}
			}
		}
	} else if query != "" {
		// 4.2 自然语言关键词与工具名匹配
		exactIDServer, exactIDRemote, isExactID := mcpclient.ParseToolID(query)
		tokens := tokenizeSearchQuery(query)

		for _, tool := range candidates {
			score := 0

			// 若 query 直接是稳定 tool_id 形式（如 conn:tool 或 conn/tool）
			if isExactID && strings.EqualFold(tool.ServerName, exactIDServer) && strings.EqualFold(tool.RemoteName, exactIDRemote) {
				score += 500
			}

			lowerQuery := strings.ToLower(query)
			lowerName := strings.ToLower(tool.RemoteName)
			lowerDesc := strings.ToLower(tool.Description)
			lowerServer := strings.ToLower(tool.ServerName)

			if lowerName == lowerQuery {
				score += 200
			} else if strings.Contains(lowerName, lowerQuery) {
				score += 100
			}
			if strings.Contains(lowerDesc, lowerQuery) {
				score += 40
			}

			for _, token := range tokens {
				switch {
				case lowerName == token:
					score += 50
				case strings.Contains(lowerName, token):
					score += 25
				case lowerServer == token:
					score += 20
				case strings.Contains(lowerDesc, token):
					score += 10
				}
			}

			if score > 0 {
				matched = append(matched, scoredTool{tool: tool, score: score})
			}
		}
	} else {
		// 4.3 未传 query 且未传 tool_id：列出连接内全部可用工具
		for _, tool := range candidates {
			matched = append(matched, scoredTool{tool: tool, score: 1})
		}
	}

	// 5. 排序：分数降序；分数相同时按 tool_id 升序保持确定性
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].score != matched[j].score {
			return matched[i].score > matched[j].score
		}
		return matched[i].tool.ToolID() < matched[j].tool.ToolID()
	})

	if len(matched) > limit {
		matched = matched[:limit]
	}

	// 6. 构造输出：返回少量匹配工具的稳定 ID、连接名称、工具名称、说明和完整参数 Schema
	type toolOutput struct {
		ToolID         string         `json:"tool_id"`
		ConnectionName string         `json:"connection_name"`
		ToolName       string         `json:"tool_name"`
		Description    string         `json:"description"`
		InputSchema    map[string]any `json:"input_schema"`
	}

	results := make([]toolOutput, 0, len(matched))
	for _, item := range matched {
		results = append(results, toolOutput{
			ToolID:         item.tool.ToolID(),
			ConnectionName: item.tool.ServerName,
			ToolName:       item.tool.RemoteName,
			Description:    item.tool.Description,
			InputSchema:    item.tool.InputSchema,
		})
	}

	if len(results) == 0 {
		return map[string]any{
			"tools":   []any{},
			"count":   0,
			"message": "未找到匹配的外部 MCP 工具。可尝试更换查询词、置空 query 查看全部可用工具，或检查工作区连接设置。",
		}, nil
	}

	return map[string]any{
		"tools": results,
		"count": len(results),
	}, nil
}

func (r attemptToolRuntime) callMCPToolTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}

	toolID := stringArg(args, "tool_id")
	if toolID == "" {
		return map[string]any{
			"error": "缺少必填参数 tool_id。请先使用 search_mcp_tools 查找可用工具并获取对应的 tool_id（格式如 'connection_name:tool_name'）。",
		}, nil
	}

	serverName, remoteName, ok := mcpclient.ParseToolID(toolID)
	if !ok {
		return map[string]any{
			"error":   fmt.Sprintf("tool_id 格式不正确：%q。正确格式为 '<connection_name>:<tool_name>'。请使用 search_mcp_tools 检索有效工具。", toolID),
			"tool_id": toolID,
		}, nil
	}

	// 1. 验证连接归属与启用状态：必须属于当前 Workspace 且处于 enabled 状态
	enabledConnections, err := r.store.ListEnabledMCPConnections(ctx, r.context.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("verify MCP connection status: %w", err)
	}
	var canonicalServerName string
	for _, c := range enabledConnections {
		if strings.EqualFold(c.Name, serverName) {
			canonicalServerName = c.Name
			break
		}
	}
	if canonicalServerName == "" {
		return map[string]any{
			"error":           fmt.Sprintf("MCP 连接 %q 在当前工作区未启用或不存在。请检查工作区设置或调用 search_mcp_tools 查找其他可用工具。", serverName),
			"tool_id":         toolID,
			"connection_name": serverName,
		}, nil
	}
	serverName = canonicalServerName

	if r.mcpSessions == nil {
		return map[string]any{
			"error":   "MCP 运行时会话不可用。",
			"tool_id": toolID,
		}, nil
	}

	// 2. 查找工具定义
	tool, found := r.mcpSessions.GetExternalTool(serverName, remoteName)
	if !found {
		// 动态刷新一次工具目录，防止服务器端新增工具未载入
		_ = r.mcpSessions.RefreshServer(ctx, serverName)
		tool, found = r.mcpSessions.GetExternalTool(serverName, remoteName)
	}
	if !found {
		return map[string]any{
			"error":           fmt.Sprintf("在连接 %q 上未找到工具 %q。该工具可能已被移除，请使用 search_mcp_tools 重新检索可用工具。", serverName, remoteName),
			"tool_id":         toolID,
			"connection_name": serverName,
			"tool_name":       remoteName,
		}, nil
	}

	// 3. 参数按工具实际 inputSchema 校验
	toolArgs, _ := args["arguments"].(map[string]any)
	if toolArgs == nil {
		toolArgs = map[string]any{}
	}

	if err := validateToolArguments(tool.InputSchema, toolArgs); err != nil {
		// 刷新一次 schema 再次尝试校验，避免因服务端 schema 更新后本地缓存未刷新导致的误判
		if refreshErr := r.mcpSessions.RefreshServer(ctx, serverName); refreshErr == nil {
			if refreshedTool, ok := r.mcpSessions.GetExternalTool(serverName, remoteName); ok {
				tool = refreshedTool
				err = validateToolArguments(tool.InputSchema, toolArgs)
			}
		}
		if err != nil {
			return map[string]any{
				"error":           fmt.Sprintf("参数校验失败：%v。请依据该工具的 input_schema 修正 arguments 参数，禁止盲目重试或静默改写。", err),
				"tool_id":         tool.ToolID(),
				"connection_name": serverName,
				"tool_name":       remoteName,
				"input_schema":    tool.InputSchema,
			}, nil
		}
	}

	// 4. 调用远程 MCP 服务
	raw, err := r.mcpSessions.CallExternal(ctx, serverName, remoteName, toolArgs)
	if err != nil {
		// 通道与执行错误作为工具响应返回给模型，保留上下文与定位证据
		return map[string]any{
			"error":           fmt.Sprintf("MCP 工具调用失败：%v", err),
			"tool_id":         tool.ToolID(),
			"connection_name": serverName,
			"tool_name":       remoteName,
		}, nil
	}

	// 5. 结果处理：脱敏、Artifact 外置与 Session Event 证据保留
	prepared, err := r.prepareMCPResult(ctx, tool.ToolID(), raw)
	if err != nil {
		return nil, fmt.Errorf("prepare MCP result for %s: %w", tool.ToolID(), err)
	}

	out := map[string]any{
		"tool_id":         tool.ToolID(),
		"connection_name": serverName,
		"tool_name":       remoteName,
	}
	for k, v := range prepared {
		out[k] = v
	}
	return out, nil
}

func validateToolArguments(schemaMap map[string]any, args map[string]any) error {
	if schemaMap == nil {
		return nil
	}
	schema, err := jsonSchemaFromMap(schemaMap)
	if err != nil {
		return fmt.Errorf("invalid json schema: %w", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("resolve json schema: %w", err)
	}
	if args == nil {
		args = map[string]any{}
	}
	return resolved.Validate(args)
}

func tokenizeSearchQuery(query string) []string {
	var tokens []string
	fields := strings.Fields(strings.ToLower(query))
	for _, field := range fields {
		cleaned := strings.Trim(field, ".,!?:;\"'()[]{}/\\-_")
		if cleaned != "" {
			tokens = append(tokens, cleaned)
		}
	}
	return tokens
}
