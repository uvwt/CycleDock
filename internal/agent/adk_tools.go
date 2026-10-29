package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	fatal           *attemptToolFatal
	replies         *replyState
}

type replyState struct {
	mu         sync.Mutex
	ended      bool
	previewIDs map[string]string
}

type attemptToolFatal struct {
	mu  sync.Mutex
	err error
}

func (f *attemptToolFatal) set(err error) {
	if f == nil || err == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		f.err = err
	}
}

func (f *attemptToolFatal) get() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func buildADKTools(runtime attemptToolRuntime) ([]tool.Tool, error) {
	definitions := internalToolDefinitions()
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
		for _, definition := range runtime.mcpSessions.ToolDefinitions() {
			name := definition.Function.Name
			converted, err := definitionToADKTool(
				definition,
				func(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
					raw, err := runtime.mcpSessions.Call(ctx, name, args)
					if err != nil {
						runtime.fatal.set(err)
						// Persist the FunctionResponse and make it the terminal event.
						// Runner will turn the recorded fatal into an Attempt failure.
						ctx.Actions().SkipSummarization = true
						return map[string]any{"error": err.Error()}, nil
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
			eventID, reused, err := r.store.PublishReply(ctx, store.PublishReplyParams{
				WorkspaceID: r.context.WorkspaceID, CardID: r.context.CardID,
				AttemptID: r.context.AttemptID, RunnerSourceID: r.context.RunnerSourceID,
				Message: input.ReplyMessage, IdempotencyKey: input.IdempotencyKey,
				PreviewID: r.replies.previewIDs[ctx.FunctionCallID()],
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
