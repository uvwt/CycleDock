package store

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const DefaultClaimLease = 2 * time.Minute

type Store struct {
	pool          *sqldb.DB
	queries       *db.Queries
	mcpCipher     cipher.AEAD
	modelCipher   cipher.AEAD
	triggerCipher cipher.AEAD
	channelCipher cipher.AEAD
	// workReady 在可能产生可认领 Trigger 的事务提交后叫醒本机 Dispatcher。
	// 未注册时写入仍然成功，只是要等 Dispatcher 下一轮轮询。
	workReady func()
	// workspaceChanged 在卡片消息、工具审批或状态变更提交后叫醒工作区级 SSE 与 Web Push 投递。
	workspaceChanged func(workspaceID uuid.UUID)
}

func New(pool *sqldb.DB) *Store {
	return &Store{pool: pool, queries: db.New(pool)}
}

// Pool 返回底层连接池，供同进程后台服务做受控只读扫描。
func (s *Store) Pool() *sqldb.DB {
	return s.pool
}

// SetWorkReadyNotifier 注册事务提交后的叫醒函数。进程启动时由 Dispatcher 注册一次。
// 评论、回复续跑、人工重试和 MCP followup 都会调用它；Schedule 与 lease 恢复仍由
// Dispatcher 自己的维护循环立刻认领，不经过这里。
func (s *Store) SetWorkReadyNotifier(notify func()) {
	s.workReady = notify
}

// SetWorkspaceChangedNotifier 注册工作区卡片变化后的叫醒函数。
// 进程启动时由 realtime.Hub 注册一次，确保消息提交、审批挂起与状态流转后零延迟唤醒 SSE 与 Web Push。
func (s *Store) SetWorkspaceChangedNotifier(notify func(workspaceID uuid.UUID)) {
	s.workspaceChanged = notify
}

func (s *Store) notifyWorkReady() {
	if s.workReady != nil {
		s.workReady()
	}
}

func (s *Store) notifyWorkspaceChanged(workspaceID uuid.UUID) {
	if s.workspaceChanged != nil && workspaceID != uuid.Nil {
		s.workspaceChanged(workspaceID)
	}
}

func (s *Store) CreateWorkspace(ctx context.Context, name string) (db.Workspace, error) {
	return s.queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: uuid.New(), Name: name})
}

func (s *Store) CreateSource(ctx context.Context, workspaceID uuid.UUID, kind domain.SourceKind, name string) (db.Source, error) {
	return s.queries.CreateSource(ctx, db.CreateSourceParams{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Kind:        string(kind),
		Name:        name,
	})
}

func (s *Store) CreateProject(ctx context.Context, workspaceID uuid.UUID, name, description string) (db.Project, error) {
	return s.queries.CreateProject(ctx, db.CreateProjectParams{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Name:        name,
		Description: description,
	})
}

func (s *Store) CreateDiscussion(
	ctx context.Context,
	workspaceID, projectID, sourceID uuid.UUID,
	preferredModelRouteID ...uuid.NullUUID,
) (db.Card, error) {
	var preferredRouteID uuid.NullUUID
	if len(preferredModelRouteID) > 0 {
		preferredRouteID = preferredModelRouteID[0]
	}
	card, err := s.queries.CreateCard(ctx, db.CreateCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		Kind:                  string(domain.CardKindDiscussion),
		DiscussionState:       sqldb.Text{String: string(domain.DiscussionStateActive), Valid: true},
		RequiredConfirmations: 1,
		CreatedBySourceID:     sourceID,
		PreferredModelRouteID: preferredRouteID,
	})
	if err == nil {
		s.notifyWorkspaceChanged(workspaceID)
	}
	return card, err
}

// CreateTodoCardWithOptions 创建人工待办卡片，并在 autoStart 为 true 时在同一事务内排入首轮系统执行。
func (s *Store) CreateTodoCardWithOptions(
	ctx context.Context,
	workspaceID, projectID, sourceID uuid.UUID,
	title, description string,
	sourceDiscussionID uuid.NullUUID,
	requiredConfirmations int32,
	autoStart bool,
) (db.Card, error) {
	if requiredConfirmations <= 0 {
		requiredConfirmations = 1
	}
	title, _ = agentdata.NormalizeText(strings.TrimSpace(title))
	description, _ = agentdata.NormalizeText(strings.TrimSpace(description))

	if !autoStart {
		card, err := s.queries.CreateCard(ctx, db.CreateCardParams{
			ID:                    uuid.New(),
			WorkspaceID:           workspaceID,
			ProjectID:             projectID,
			Kind:                  string(domain.CardKindTodo),
			Title:                 title,
			Description:           description,
			TodoState:             sqldb.Text{String: string(domain.TodoStateTodo), Valid: true},
			SourceDiscussionID:    sourceDiscussionID,
			RequiredConfirmations: requiredConfirmations,
			CreatedBySourceID:     sourceID,
		})
		if err == nil {
			s.notifyWorkspaceChanged(workspaceID)
		}
		return card, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Card{}, err
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)
	card, err := q.CreateCard(ctx, db.CreateCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		Kind:                  string(domain.CardKindTodo),
		Title:                 title,
		Description:           description,
		TodoState:             sqldb.Text{String: string(domain.TodoStateTodo), Valid: true},
		SourceDiscussionID:    sourceDiscussionID,
		RequiredConfirmations: requiredConfirmations,
		CreatedBySourceID:     sourceID,
	})
	if err != nil {
		return db.Card{}, err
	}

	initialTurnContent := buildInitialWorkTurnContent(title, description)
	turnRequestHash := stableRequestHash(
		"manual_todo_initial_turn",
		workspaceID.String(),
		card.ID.String(),
		sourceID.String(),
		initialTurnContent,
	)
	if _, err := insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:       workspaceID,
		CardID:            card.ID,
		Source:            domain.TurnSourceSystem,
		Content:           initialTurnContent,
		CreatedBySourceID: uuid.NullUUID{UUID: sourceID, Valid: true},
		DispatchMode:      domain.DispatchModeEnqueue,
		IdempotencyKey:    "manual-todo-start:" + card.ID.String(),
		RequestHash:       turnRequestHash,
	}); err != nil {
		return db.Card{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return db.Card{}, err
	}
	s.notifyWorkspaceChanged(workspaceID)
	s.notifyWorkReady()
	return card, nil
}

func (s *Store) CreateTodoCard(
	ctx context.Context,
	workspaceID, projectID, sourceID uuid.UUID,
	title, description string,
	sourceDiscussionID uuid.NullUUID,
	requiredConfirmations int32,
) (db.Card, error) {
	return s.CreateTodoCardWithOptions(
		ctx, workspaceID, projectID, sourceID,
		title, description, sourceDiscussionID, requiredConfirmations, false,
	)
}

func (s *Store) CreateOngoingCard(
	ctx context.Context,
	workspaceID, projectID, sourceID uuid.UUID,
	title, description string,
	sourceDiscussionID uuid.NullUUID,
) (db.Card, error) {
	card, err := s.queries.CreateCard(ctx, db.CreateCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		Kind:                  string(domain.CardKindOngoing),
		Title:                 strings.TrimSpace(title),
		Description:           strings.TrimSpace(description),
		OngoingState:          sqldb.Text{String: string(domain.OngoingStateActive), Valid: true},
		SourceDiscussionID:    sourceDiscussionID,
		RequiredConfirmations: 1,
		CreatedBySourceID:     sourceID,
	})
	if err == nil {
		s.notifyWorkspaceChanged(workspaceID)
	}
	return card, err
}

func (s *Store) GetCard(ctx context.Context, workspaceID, cardID uuid.UUID) (db.Card, error) {
	card, err := s.queries.GetCard(ctx, db.GetCardParams{WorkspaceID: workspaceID, ID: cardID})
	if errors.Is(err, sqldb.ErrNoRows) {
		return db.Card{}, domain.ErrNotFound
	}
	return card, err
}

func (s *Store) ListCardEvents(ctx context.Context, workspaceID, cardID uuid.UUID) ([]db.Event, error) {
	return s.queries.ListCardEvents(ctx, db.ListCardEventsParams{WorkspaceID: workspaceID, CardID: cardID})
}

func mapConstraintError(err error) error {
	if sqldb.IsUniqueViolation(err) {
		if name := sqldb.ConstraintName(err); name != "" {
			return fmt.Errorf("%w: %s", domain.ErrConflict, name)
		}
		return fmt.Errorf("%w: %v", domain.ErrConflict, err)
	}
	return err
}
