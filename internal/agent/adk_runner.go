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
	adkrunner "google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
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
	toolRuntime := attemptToolRuntime{
		store:           r.store,
		context:         cardContext,
		mcpSessions:     mcpSessions,
		artifactService: r.artifactService,
		historyService:  historyService,
		fatal:           &attemptToolFatal{},
	}
	tools, err := buildADKTools(toolRuntime)
	if err != nil {
		return err
	}
	llm, err := newADKRouteModel(
		r.model,
		r.modelAuth,
		r.live,
		cardContext.WorkspaceID,
		cardContext.CardID,
		cardContext.AttemptID,
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
		nil,
		cardContext.WorkspaceID,
		cardContext.CardID,
		cardContext.AttemptID,
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

	runCtx = agentsession.WithAttempt(runCtx, cardContext.AttemptID)
	userMessage := genai.NewContentFromText(cardContext.TurnContent, genai.RoleUser)
	var finalText string
	for event, eventErr := range adkRunner.Run(
		runCtx,
		userID,
		sessionID,
		userMessage,
		agent.RunConfig{},
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
		if event == nil || event.Content == nil || !event.IsFinalResponse() {
			continue
		}
		if text := textFromADKContent(event.Content); text != "" {
			finalText = text
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
		if err := r.store.CompleteAttempt(
			runCtx,
			cardContext.WorkspaceID,
			cardContext.CardID,
			cardContext.AttemptID,
			cardContext.RunnerSourceID,
			finalText,
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
	out.WriteString("- Use cycledock_add_finding only for distilled durable business facts; the Session already preserves ordinary tool evidence.\n")
	out.WriteString("- If an earlier FunctionResponse contains artifact_ref, use cycledock_read_artifact to inspect that historical payload instead of re-running the real tool merely to recover old evidence.\n")
	out.WriteString("- If context compaction no longer includes an exact old value, use cycledock_search_session_history before re-querying a real read-only tool just to recover history.\n")
	out.WriteString("- Use cycledock_report_progress for meaningful execution stages.\n")
	out.WriteString("- Only an active Discussion may generate Todo/Ongoing work.\n")
	out.WriteString("- On Ongoing Cards, use cycledock_check_in when you establish a health fact.\n")
	out.WriteString("- If human input is required, call cycledock_wait_for_human and stop.\n")
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
