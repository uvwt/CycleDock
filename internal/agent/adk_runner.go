package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/platform"
	adkrunner "google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	// Tail retention is the only ADK compaction strategy that bounds prompt
	// growth. Keep a raw tail large enough for recent tool call/response pairs.
	defaultCompactionTokenThreshold = 24000
	defaultCompactionEventRetention = 20
)

func (r *Runner) RunAttempt(ctx context.Context, claim store.AttemptClaim) (runErr error) {
	if r.sessionService == nil {
		return errors.New("ADK session service is not configured")
	}

	cardContext, err := r.store.LoadAgentCardContext(
		ctx, claim.Attempt.WorkspaceID, claim.Attempt.ID, claim.Attempt.RunnerSourceID,
	)
	if err != nil {
		return err
	}
	settings, err := r.store.GetAgentSettings(ctx, claim.Attempt.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load agent settings: %w", err)
	}
	if settings.SourceID != claim.Attempt.RunnerSourceID || !runnableAgentSettings(settings) {
		return errors.New("built-in agent model configuration changed")
	}

	if cardContext.CardKind == domain.CardKindTodo &&
		cardContext.TodoState != string(domain.TodoStateWorking) {
		if err := r.store.MoveTodo(
			ctx,
			cardContext.WorkspaceID,
			cardContext.CardID,
			cardContext.RunnerSourceID,
			domain.TodoStateWorking,
		); err != nil {
			return fmt.Errorf("move card to working: %w", err)
		}
		cardContext, err = r.store.LoadAgentCardContext(
			ctx, claim.Attempt.WorkspaceID, claim.Attempt.ID, claim.Attempt.RunnerSourceID,
		)
		if err != nil {
			return err
		}
	}

	if r.live != nil {
		r.live.Publish(cardContext.CardID, realtime.Event{
			Type:      "attempt.started",
			AttemptID: cardContext.AttemptID.String(),
			StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
		defer func() {
			eventType := "attempt.finished"
			if runErr != nil {
				eventType = "attempt.failed"
			}
			r.live.Publish(cardContext.CardID, realtime.Event{
				Type: eventType, AttemptID: cardContext.AttemptID.String(),
			})
		}()
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatErrors := make(chan error, 1)
	go r.heartbeatAttempt(runCtx, cardContext, heartbeatErrors, cancel)

	mcpSessions, nexusContext, err := r.openADKMCPSessions(runCtx, cardContext.WorkspaceID)
	if err != nil {
		return err
	}
	defer mcpSessions.Close()

	historyService, _ := r.sessionService.(*agentsession.PostgresService)
	replies := &replyState{}
	toolRuntime := attemptToolRuntime{
		store:           r.store,
		context:         cardContext,
		mcpSessions:     mcpSessions,
		artifactService: r.artifactService,
		historyService:  historyService,
		fatal:           &attemptToolFatal{},
		replies:         replies,
	}
	tools, err := buildADKTools(toolRuntime)
	if err != nil {
		return err
	}
	// 仅预览 reply_message 正文；持久用户消息仍统一由工具事务创建。
	llm, err := newADKRouteModel(
		r.model,
		r.modelAuth,
		cardContext.WorkspaceID,
		settings.Routes,
	)
	if err != nil {
		return err
	}

	// Compaction uses the same ordered model routes but never publishes its
	// internal summarization text to the Card's live SSE stream.
	summaryLLM, err := newADKRouteModel(
		r.model,
		r.modelAuth,
		cardContext.WorkspaceID,
		settings.Routes,
	)
	if err != nil {
		return err
	}
	summarizer, err := compaction.NewLLMSummarizer(compaction.LLMSummarizerConfig{
		Model:               summaryLLM,
		MaxToolContentChars: 4000,
		MaxTranscriptChars:  120000,
		Timeout:             20 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create ADK context summarizer: %w", err)
	}

	instructions := buildADKInstructions(cardContext, settings.SystemInstructions, nexusContext)
	rootAgent, err := llmagent.New(llmagent.Config{
		Name:            "cycle_agent",
		Description:     "CycleDock built-in agent for the current persistent Card Session.",
		Model:           llm,
		IncludeContents: llmagent.IncludeContentsDefault,
		InstructionProvider: func(agent.ReadonlyContext) (string, error) {
			return instructions, nil
		},
		Tools: tools,
		BeforeToolCallbacks: []llmagent.BeforeToolCallback{
			func(ctx agent.Context, _ tool.Tool, _ map[string]any) (map[string]any, error) {
				replies.mu.Lock()
				defer replies.mu.Unlock()
				if replies.ended {
					// 同一批中结束消息后的工具也要返回响应，但不得再产生副作用。
					ctx.Actions().SkipSummarization = true
					return map[string]any{"error": "turn already ended"}, nil
				}
				return nil, nil
			},
		},
	})
	if err != nil {
		return fmt.Errorf("create ADK agent: %w", err)
	}

	appName, userID, sessionID := agentsession.Identity(
		cardContext.WorkspaceID, cardContext.CardID,
	)
	if err := ensureADKSession(runCtx, r.sessionService, appName, userID, sessionID); err != nil {
		return err
	}
	adkRunner, err := adkrunner.New(adkrunner.Config{
		AppName:         appName,
		Agent:           rootAgent,
		SessionService:  r.sessionService,
		ArtifactService: r.artifactService,
		Compaction: &compaction.Config{
			TokenThreshold:     r.compactionTokenThreshold,
			EventRetentionSize: r.compactionEventRetention,
			Summarizer:         summarizer,
		},
	})
	if err != nil {
		return fmt.Errorf("create ADK runner: %w", err)
	}

	// 工具可能发送消息、结束 Turn 或产生副作用，必须按模型给出的顺序执行。
	// 使用 ADK 官方执行器，避免默认并发让结束消息抢先关闭前面的回复。
	runCtx = platform.WithTaskRunner(runCtx, func(ctx context.Context, tasks []func(context.Context)) {
		for _, task := range tasks {
			task(ctx)
		}
	})
	runCtx = agentsession.WithAttempt(runCtx, cardContext.AttemptID)
	userMessage := genai.NewContentFromText(cardContext.TurnContent, genai.RoleUser)
	previews := replyEventPublisher{hub: r.live, cardID: cardContext.CardID, attemptID: cardContext.AttemptID}
	var finalText string
	for event, eventErr := range adkRunner.Run(
		runCtx,
		userID,
		sessionID,
		userMessage,
		agent.RunConfig{StreamingMode: agent.StreamingModeSSE},
	) {
		if eventErr != nil {
			select {
			case heartbeatErr := <-heartbeatErrors:
				if heartbeatErr != nil {
					return heartbeatErr
				}
			default:
			}
			return fmt.Errorf("ADK invocation failed: %w", eventErr)
		}
		previews.publish(event)
		if event == nil || event.Partial || event.Content == nil ||
			event.Content.Role != genai.RoleModel || event.Actions.Compaction != nil {
			continue
		}
		if event.IsFinalResponse() {
			finalText = textFromADKContent(event.Content)
		}
	}
	if toolErr := toolRuntime.fatal.get(); toolErr != nil {
		return fmt.Errorf("MCP tool call failed: %w", toolErr)
	}

	select {
	case heartbeatErr := <-heartbeatErrors:
		if heartbeatErr != nil {
			return heartbeatErr
		}
	default:
	}

	attempt, err := r.store.GetAttempt(runCtx, cardContext.WorkspaceID, cardContext.AttemptID)
	if err != nil {
		return err
	}
	switch attempt.Status {
	case domain.AttemptStatusWaitingHuman:
		return nil
	case domain.AttemptStatusRunning:
		// 未调用结束工具的旧式模型输出走同一个持久消息通道兜底，
		// 不能回到旧 result 气泡和临时流文本两套并行机制。
		replies.mu.Lock()
		ended := replies.ended
		replies.mu.Unlock()
		if !ended && finalText != "" {
			if _, _, err := r.store.PublishReply(runCtx, store.PublishReplyParams{
				WorkspaceID: cardContext.WorkspaceID, CardID: cardContext.CardID,
				AttemptID: cardContext.AttemptID, RunnerSourceID: cardContext.RunnerSourceID,
				Message:        store.ReplyMessage{Type: "text", Content: finalText, EndTurn: true},
				IdempotencyKey: "runner-final-" + cardContext.AttemptID.String(),
			}); err != nil {
				return err
			}
		}
		if err := r.store.CompleteAttempt(
			runCtx,
			cardContext.WorkspaceID,
			cardContext.CardID,
			cardContext.AttemptID,
			cardContext.RunnerSourceID,
			time.Now().UTC(),
		); err != nil {
			return err
		}
		return nil
	case domain.AttemptStatusSuperseded,
		domain.AttemptStatusCancelled,
		domain.AttemptStatusExpired:
		return nil
	default:
		return fmt.Errorf("unexpected attempt status after ADK invocation: %s", attempt.Status)
	}
}

func ensureADKSession(
	ctx context.Context,
	service session.Service,
	appName, userID, sessionID string,
) error {
	_, err := service.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, session.ErrNotFound) {
		return fmt.Errorf("load ADK session: %w", err)
	}
	_, err = service.Create(ctx, &session.CreateRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("create ADK session: %w", err)
	}
	return nil
}

func (r *Runner) openADKMCPSessions(
	ctx context.Context,
	workspaceID uuid.UUID,
) (*mcpclient.SessionSet, string, error) {
	connections, err := r.store.ListEnabledMCPConnections(ctx, workspaceID)
	if err != nil {
		return nil, "", fmt.Errorf("load MCP connections: %w", err)
	}
	mcpConnections := make([]mcpclient.Connection, 0, len(connections)+1)
	for _, connection := range connections {
		token, err := r.store.MCPBearerToken(connection)
		if err != nil {
			return nil, "", fmt.Errorf("decrypt MCP bearer token for %s: %w", connection.Name, err)
		}
		mcpConnections = append(mcpConnections, mcpclient.Connection{
			Name:        connection.Name,
			URL:         connection.Url,
			BearerToken: token,
			Timeout:     time.Duration(connection.TimeoutMs) * time.Millisecond,
		})
	}

	nexusDockMounted := false
	if r.nexusDock != nil {
		connection, err := r.nexusDock.Runtime(ctx, workspaceID)
		switch {
		case errors.Is(err, nexusdock.ErrNotConnected):
		case err != nil:
			return nil, "", fmt.Errorf("load NexusDock remote device connection: %w", err)
		default:
			mcpConnections = append(mcpConnections, mcpclient.Connection{
				Name:            firstPartyNexusDockServerName,
				URL:             connection.URL,
				BearerToken:     connection.AccessToken,
				Timeout:         30 * time.Second,
				NativeToolNames: true,
				HiddenRemoteTools: map[string]bool{
					"agentdock_context": true,
				},
			})
			nexusDockMounted = true
		}
	}

	sessions, err := mcpclient.Connect(ctx, mcpConnections)
	if err != nil {
		return nil, "", err
	}
	if !nexusDockMounted {
		return sessions, "", nil
	}
	bootstrap, err := nexusDockExecutionTargets(ctx, sessions)
	if err != nil {
		sessions.Close()
		return nil, "", fmt.Errorf("bootstrap NexusDock remote devices: %w", err)
	}
	return sessions, bootstrap, nil
}

func (r *Runner) heartbeatAttempt(
	ctx context.Context,
	card store.AgentCardContext,
	errCh chan<- error,
	cancel context.CancelFunc,
) {
	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			err := r.store.HeartbeatAttempt(
				ctx,
				card.WorkspaceID,
				card.CardID,
				card.AttemptID,
				now.UTC(),
				r.leaseDuration,
			)
			if err == nil {
				continue
			}
			// choice 消息会在数据库事务内先切换 waiting_human，然后 ADK
			// 才写回 FunctionResponse。该正常状态转换不能被 heartbeat 误判成失败。
			if errors.Is(err, domain.ErrAttemptNotCurrent) {
				attempt, lookupErr := r.store.GetAttempt(ctx, card.WorkspaceID, card.AttemptID)
				if lookupErr == nil && attempt.Status == domain.AttemptStatusWaitingHuman {
					return
				}
			}
			select {
			case errCh <- fmt.Errorf("attempt heartbeat failed: %w", err):
			default:
			}
			cancel()
			return
		}
	}
}

func buildADKInstructions(
	card store.AgentCardContext,
	workspaceInstructions string,
	nexusContext string,
) string {
	var out strings.Builder
	out.WriteString("You are CycleDock's built-in Cycle Agent.\n\n")
	out.WriteString("This Card is a persistent Agent Session. Prior user messages, model responses, tool calls, and tool responses from this Card can be present in the Session history. Use them when they answer the current follow-up. Do not repeat a read-only tool call merely because it happened in an earlier Turn. Re-query when the user asks for current/fresh state, when the old result may be stale, or when a write action must actually be performed.\n\n")
	out.WriteString("Rules:\n")
	out.WriteString("- Treat the current Project/Card business metadata below as authoritative.\n")
	out.WriteString("- All user-visible content must use reply_message. Each call creates exactly one separate bubble; you may call it several times per Turn. Do not put replies in plain assistant text.\n")
	out.WriteString("- Send progress as reply_message(type=text,end_turn=false); finish with end_turn=true. A choice reply must include widget.question and widget.options and end_turn=true, which waits for human input. Use a unique idempotency_key for each bubble.\n")
	out.WriteString("- If an earlier FunctionResponse contains artifact_ref, use cycledock_read_artifact to inspect that historical payload instead of re-running the real tool merely to recover old evidence.\n")
	out.WriteString("- If context compaction no longer includes an exact old value, use cycledock_search_session_history before re-querying a real read-only tool just to recover history.\n")
	out.WriteString("- Only an active Discussion may generate Todo/Ongoing work.\n")
	out.WriteString("- On Ongoing Cards, use cycledock_check_in when you establish a health fact.\n")
	out.WriteString("- Use cycledock_confirm_card only after verified Todo work. Human Review -> Done is never your action.\n")
	out.WriteString("- Never output chain-of-thought or hidden reasoning.\n")
	if extra := strings.TrimSpace(workspaceInstructions); extra != "" {
		out.WriteString("\nWorkspace Agent instructions:\n")
		out.WriteString(extra)
		out.WriteString("\n")
	}
	out.WriteString("\nCurrent business context:\n")
	fmt.Fprintf(&out, "- Workspace: %s\n", card.WorkspaceName)
	fmt.Fprintf(&out, "- Project: %s\n", card.ProjectName)
	if value := strings.TrimSpace(card.ProjectDescription); value != "" {
		fmt.Fprintf(&out, "- Project description: %s\n", value)
	}
	if value := strings.TrimSpace(card.ProjectInstructions); value != "" {
		fmt.Fprintf(&out, "- Project instructions: %s\n", value)
	}
	fmt.Fprintf(&out, "- Card ID: %s\n", card.CardID)
	fmt.Fprintf(&out, "- Card kind: %s\n", card.CardKind)
	fmt.Fprintf(&out, "- Card title: %s\n", card.CardTitle)
	if value := strings.TrimSpace(card.CardDescription); value != "" {
		fmt.Fprintf(&out, "- Card description: %s\n", value)
	}
	if card.DiscussionState != "" {
		fmt.Fprintf(&out, "- Discussion state: %s\n", card.DiscussionState)
	}
	if card.TodoState != "" {
		fmt.Fprintf(&out, "- Todo state: %s\n", card.TodoState)
	}
	if card.OngoingState != "" {
		fmt.Fprintf(&out, "- Ongoing state: %s\n", card.OngoingState)
	}
	fmt.Fprintf(&out, "- Required confirmations: %d\n", card.RequiredConfirmations)
	fmt.Fprintf(&out, "- Pending Turns behind this Attempt: %d\n", card.PendingTurnCount)
	fmt.Fprintf(&out, "- Current Turn source: %s\n", card.TurnSource)
	fmt.Fprintf(&out, "- Current Attempt: #%d (%s)\n", card.AttemptNumber, card.AttemptReason)
	if strings.TrimSpace(nexusContext) != "" {
		out.WriteString("\nAvailable NexusDock execution targets:\n")
		out.WriteString(nexusContext)
		out.WriteString("\n")
	}
	return out.String()
}

func textFromADKContent(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var out strings.Builder
	for _, part := range content.Parts {
		if part == nil || part.Thought || part.Text == "" {
			continue
		}
		out.WriteString(part.Text)
	}
	return strings.TrimSpace(out.String())
}

func logADKAttemptFailure(logger *slog.Logger, claim store.AttemptClaim, err error) {
	if logger == nil {
		return
	}
	logger.Error(
		"ADK attempt failed",
		"workspace_id", claim.Attempt.WorkspaceID,
		"card_id", claim.Attempt.CardID,
		"turn_id", claim.Attempt.TurnID,
		"attempt_id", claim.Attempt.ID,
		"error", err,
	)
}
