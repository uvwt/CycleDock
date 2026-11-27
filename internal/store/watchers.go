package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/trigger"
)

const (
	defaultWatcherListLimit = 50
	maxWatcherListLimit     = 200
)

type CardWatcher struct {
	ID                 uuid.UUID
	WorkspaceID        uuid.UUID
	CardID             uuid.UUID
	Kind               domain.WatcherKind
	MatchKey           string
	Note               string
	DispatchMode       domain.DispatchMode
	Status             domain.WatcherStatus
	ExpiresAt          time.Time
	CreatedBySourceID  uuid.NullUUID
	CreatedByAttemptID uuid.NullUUID
	TriggeredTurnID    uuid.NullUUID
	ResolvedReason     string
	ResolvedAt         *time.Time
	IdempotencyKey     string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// WorkspaceCardWatcher 在基础 Watcher 之上附带卡片与项目元信息，供工作区级等待总览直接展示和跳转。
type WorkspaceCardWatcher struct {
	CardWatcher
	ProjectID   uuid.UUID
	ProjectName string
	CardTitle   string
	CardKind    domain.CardKind
}

type CreateCardWatcherParams struct {
	WorkspaceID        uuid.UUID
	CardID             uuid.UUID
	Kind               domain.WatcherKind
	MatchKey           string
	Note               string
	DispatchMode       domain.DispatchMode
	ExpiresAt          time.Time
	CreatedBySourceID  uuid.NullUUID
	CreatedByAttemptID uuid.NullUUID
	IdempotencyKey     string
}

type CreateCardWatcherResult struct {
	Watcher CardWatcher
	Reused  bool
}

func (s *Store) CreateCardWatcher(
	ctx context.Context,
	p CreateCardWatcherParams,
) (CreateCardWatcherResult, error) {
	matchKey := trigger.NormalizeMatchKey(p.MatchKey)
	note, _ := agentdata.NormalizeText(strings.TrimSpace(p.Note))
	idempotencyKey, _ := agentdata.NormalizeText(strings.TrimSpace(p.IdempotencyKey))

	switch p.Kind {
	case domain.WatcherKindEvent:
		if matchKey == "" {
			return CreateCardWatcherResult{}, errors.New("match_key is required for event watcher")
		}
		if len(matchKey) > 256 {
			return CreateCardWatcherResult{}, errors.New("match_key is too long")
		}
	case domain.WatcherKindWakeup:
		if matchKey != "" {
			return CreateCardWatcherResult{}, errors.New("wakeup watcher must not specify match_key")
		}
		if note == "" {
			return CreateCardWatcherResult{}, errors.New("note is required for wakeup watcher")
		}
	default:
		return CreateCardWatcherResult{}, fmt.Errorf("invalid watcher kind %q", p.Kind)
	}

	dispatchMode, err := validateDispatchMode(p.DispatchMode)
	if err != nil {
		return CreateCardWatcherResult{}, err
	}
	if p.ExpiresAt.IsZero() {
		return CreateCardWatcherResult{}, errors.New("expires_at is required")
	}
	expiresAt := p.ExpiresAt.UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateCardWatcherResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return CreateCardWatcherResult{}, err
	}
	active, err := isCardRunnableTx(ctx, tx, p.WorkspaceID, p.CardID)
	if err != nil {
		return CreateCardWatcherResult{}, err
	}
	if !active {
		return CreateCardWatcherResult{}, fmt.Errorf("%w: card is not active for watcher registration", domain.ErrInvalidTransition)
	}

	if idempotencyKey != "" {
		existing, found, err := findCardWatcherByIdempotency(ctx, tx, p.WorkspaceID, p.CardID, idempotencyKey)
		if err != nil {
			return CreateCardWatcherResult{}, err
		}
		if found {
			if existing.Kind != p.Kind || existing.MatchKey != matchKey || existing.Note != note || existing.DispatchMode != dispatchMode {
				return CreateCardWatcherResult{}, domain.ErrIdempotencyMismatch
			}
			if err := tx.Commit(ctx); err != nil {
				return CreateCardWatcherResult{}, err
			}
			return CreateCardWatcherResult{Watcher: existing, Reused: true}, nil
		}
	}

	watcherID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO card_watchers (
			id, workspace_id, card_id, kind, match_key, note,
			dispatch_mode, status, expires_at,
			created_by_source_id, created_by_attempt_id, idempotency_key
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'armed',$8,$9,$10,NULLIF($11,''))
	`, watcherID, p.WorkspaceID, p.CardID, string(p.Kind), matchKey, note,
		string(dispatchMode), expiresAt,
		nullUUIDValue(p.CreatedBySourceID), nullUUIDValue(p.CreatedByAttemptID), idempotencyKey)
	if err != nil {
		return CreateCardWatcherResult{}, mapConstraintError(err)
	}

	created, err := loadCardWatcher(ctx, tx, p.WorkspaceID, p.CardID, watcherID)
	if err != nil {
		return CreateCardWatcherResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateCardWatcherResult{}, err
	}
	return CreateCardWatcherResult{Watcher: created, Reused: false}, nil
}

func (s *Store) ListCardWatchers(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	armedOnly bool,
	limit int,
) ([]CardWatcher, error) {
	if limit <= 0 {
		limit = defaultWatcherListLimit
	}
	if limit > maxWatcherListLimit {
		limit = maxWatcherListLimit
	}
	query := `
		SELECT id, workspace_id, card_id, kind, match_key, note,
		       dispatch_mode, status, expires_at,
		       created_by_source_id, created_by_attempt_id, triggered_turn_id,
		       resolved_reason, resolved_at, COALESCE(idempotency_key, ''),
		       created_at, updated_at
		FROM card_watchers
		WHERE workspace_id=$1 AND card_id=$2
	`
	if armedOnly {
		query += ` AND status='armed'`
	}
	query += ` ORDER BY created_at DESC LIMIT $3`

	rows, err := s.pool.Query(ctx, query, workspaceID, cardID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CardWatcher
	for rows.Next() {
		item, err := scanCardWatcher(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListWorkspaceWatchers 返回当前工作区（可按项目筛选）下挂起或历史的一次性事件等待与延时自唤醒。
func (s *Store) ListWorkspaceWatchers(
	ctx context.Context,
	workspaceID uuid.UUID,
	projectID uuid.NullUUID,
	armedOnly bool,
	limit int,
) ([]WorkspaceCardWatcher, error) {
	if limit <= 0 {
		limit = defaultWatcherListLimit
	}
	if limit > maxWatcherListLimit {
		limit = maxWatcherListLimit
	}

	args := []any{workspaceID}
	var b strings.Builder
	b.WriteString(`
		SELECT w.id, w.workspace_id, w.card_id, w.kind, w.match_key, w.note,
		       w.dispatch_mode, w.status, w.expires_at,
		       w.created_by_source_id, w.created_by_attempt_id, w.triggered_turn_id,
		       w.resolved_reason, w.resolved_at, COALESCE(w.idempotency_key, ''),
		       w.created_at, w.updated_at,
		       c.project_id, p.name, c.title, c.kind
		FROM card_watchers w
		JOIN cards c ON c.workspace_id = w.workspace_id AND c.id = w.card_id
		JOIN projects p ON p.workspace_id = c.workspace_id AND p.id = c.project_id
		WHERE w.workspace_id = $1
	`)
	if projectID.Valid {
		args = append(args, projectID.UUID)
		fmt.Fprintf(&b, " AND c.project_id = $%d", len(args))
	}
	if armedOnly {
		b.WriteString(" AND w.status = 'armed'")
		b.WriteString(" ORDER BY w.expires_at ASC, w.created_at DESC")
	} else {
		b.WriteString(" ORDER BY CASE WHEN w.status = 'armed' THEN 0 ELSE 1 END, w.expires_at ASC, w.created_at DESC")
	}
	args = append(args, limit)
	fmt.Fprintf(&b, " LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkspaceCardWatcher
	for rows.Next() {
		var item WorkspaceCardWatcher
		var kind, dispatchMode, status, cardKind string
		var resolvedAt *time.Time
		if err := rows.Scan(
			&item.ID, &item.WorkspaceID, &item.CardID, &kind, &item.MatchKey, &item.Note,
			&dispatchMode, &status, &item.ExpiresAt,
			&item.CreatedBySourceID, &item.CreatedByAttemptID, &item.TriggeredTurnID,
			&item.ResolvedReason, &resolvedAt, &item.IdempotencyKey,
			&item.CreatedAt, &item.UpdatedAt,
			&item.ProjectID, &item.ProjectName, &item.CardTitle, &cardKind,
		); err != nil {
			return nil, err
		}
		item.Kind = domain.WatcherKind(kind)
		item.DispatchMode = domain.DispatchMode(dispatchMode)
		item.Status = domain.WatcherStatus(status)
		item.CardKind = domain.CardKind(cardKind)
		item.ExpiresAt = item.ExpiresAt.UTC()
		if resolvedAt != nil {
			t := resolvedAt.UTC()
			item.ResolvedAt = &t
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CancelCardWatcher(
	ctx context.Context,
	workspaceID, cardID, watcherID uuid.UUID,
	reason string,
) (CardWatcher, error) {
	reason, _ = agentdata.NormalizeText(strings.TrimSpace(reason))
	if reason == "" {
		reason = "cancelled"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CardWatcher{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return CardWatcher{}, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE card_watchers
		SET status='cancelled', resolved_reason=$4, resolved_at=now(), updated_at=now()
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3 AND status='armed'
	`, workspaceID, cardID, watcherID, reason)
	if err != nil {
		return CardWatcher{}, err
	}
	if tag.RowsAffected() == 0 {
		existing, loadErr := loadCardWatcher(ctx, tx, workspaceID, cardID, watcherID)
		if loadErr != nil {
			return CardWatcher{}, loadErr
		}
		if err := tx.Commit(ctx); err != nil {
			return CardWatcher{}, err
		}
		return existing, fmt.Errorf("%w: watcher is already %s", domain.ErrConflict, existing.Status)
	}
	updated, err := loadCardWatcher(ctx, tx, workspaceID, cardID, watcherID)
	if err != nil {
		return CardWatcher{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CardWatcher{}, err
	}
	return updated, nil
}

// ProcessDueCardWatchers 扫描指定 Workspace 下到期的 armed card_watchers：
// - wakeup 类型：到点触发延时自唤醒，写入 TurnSourceTrigger 并置为 triggered；
// - event 类型：等待超时仍未收到匹配 Webhook 时执行一次兜底唤醒，写入 TurnSourceTrigger 并置为 expired。
func (s *Store) ProcessDueCardWatchers(
	ctx context.Context,
	workspaceID uuid.UUID,
	now time.Time,
	limit int,
) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 100
	}
	now = now.UTC()

	rows, err := s.pool.Query(ctx, `
		SELECT id
		FROM card_watchers
		WHERE workspace_id=$1
		  AND status='armed'
		  AND expires_at <= $2
		ORDER BY expires_at ASC
		LIMIT $3
	`, workspaceID, now, limit)
	if err != nil {
		return nil, err
	}
	var watcherIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		watcherIDs = append(watcherIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	created := make([]uuid.UUID, 0, len(watcherIDs))
	for _, watcherID := range watcherIDs {
		turnID, ok, err := s.processDueCardWatcher(ctx, workspaceID, watcherID, now)
		if err != nil {
			return created, err
		}
		if ok {
			created = append(created, turnID)
		}
	}
	if len(created) > 0 {
		s.notifyWorkReady()
	}
	return created, nil
}

func (s *Store) processDueCardWatcher(
	ctx context.Context,
	workspaceID, watcherID uuid.UUID,
	now time.Time,
) (uuid.UUID, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	var cardID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT card_id
		FROM card_watchers
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, watcherID).Scan(&cardID)
	if errors.Is(err, sqldb.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}

	w, err := loadCardWatcherForUpdate(ctx, tx, workspaceID, watcherID)
	if errors.Is(err, domain.ErrNotFound) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if w.Status != domain.WatcherStatusArmed || w.ExpiresAt.After(now) {
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, false, err
		}
		return uuid.Nil, false, nil
	}

	active, err := isCardRunnableTx(ctx, tx, workspaceID, w.CardID)
	if err != nil {
		return uuid.Nil, false, err
	}
	if !active {
		if _, err := tx.Exec(ctx, `
			UPDATE card_watchers
			SET status='cancelled', resolved_reason='card_inactive', resolved_at=$3, updated_at=$3
			WHERE workspace_id=$1 AND id=$2
		`, workspaceID, w.ID, now); err != nil {
			return uuid.Nil, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, false, err
		}
		return uuid.Nil, false, nil
	}

	var nextStatus domain.WatcherStatus
	var resolvedReason string
	var idempotencyKey string
	var content string

	switch w.Kind {
	case domain.WatcherKindWakeup:
		nextStatus = domain.WatcherStatusTriggered
		resolvedReason = "wakeup_due"
		idempotencyKey = "watcher-wakeup:" + w.ID.String()
		content = fmt.Sprintf(
			"[延时自唤醒] 已到达预设唤醒时间（%s）。\n你此前登记的回查事项：%s",
			w.ExpiresAt.UTC().Format(time.RFC3339),
			w.Note,
		)
	case domain.WatcherKindEvent:
		nextStatus = domain.WatcherStatusExpired
		resolvedReason = "timeout_fallback"
		idempotencyKey = "watcher-timeout:" + w.ID.String()
		var b strings.Builder
		b.WriteString(fmt.Sprintf(
			"[等待事件超时兜底唤醒] 等待外部事件（match_key=%s）已到达超时时间（%s），期间未收到匹配 Webhook 回调。",
			w.MatchKey,
			w.ExpiresAt.UTC().Format(time.RFC3339),
		))
		if w.Note != "" {
			b.WriteString("\n你此前登记的等待说明：")
			b.WriteString(w.Note)
		}
		b.WriteString("\n请主动核查外部状态，决定继续推进、重新注册等待或向用户同步最新进展。")
		content = b.String()
	default:
		return uuid.Nil, false, fmt.Errorf("unexpected watcher kind %q", w.Kind)
	}

	if err := supersedeAttemptForTurn(
		ctx, tx, workspaceID, w.CardID, w.DispatchMode,
		"superseded by interrupting card watcher wakeup", now,
	); err != nil {
		return uuid.Nil, false, err
	}
	turn, err := insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:    workspaceID,
		CardID:         w.CardID,
		Source:         domain.TurnSourceTrigger,
		Content:        content,
		DispatchMode:   w.DispatchMode,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := supersedePendingToolApprovalsTx(
		ctx, tx, workspaceID, w.CardID,
		"已有定时唤醒/超时兜底触发新轮次，旧审批已自动失效",
	); err != nil {
		return uuid.Nil, false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE card_watchers
		SET status=$3, triggered_turn_id=$4, resolved_reason=$5, resolved_at=$6, updated_at=$6
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, w.ID, string(nextStatus), turn.ID, resolvedReason, now); err != nil {
		return uuid.Nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return turn.ID, true, nil
}

func loadArmedEventWatchersForUpdate(
	ctx context.Context,
	tx *sqldb.Tx,
	workspaceID uuid.UUID,
	matchKeys []string,
) ([]CardWatcher, error) {
	if len(matchKeys) == 0 {
		return nil, nil
	}
	args := []any{workspaceID}
	inClause := sqldb.AppendInPlaceholders(&args, matchKeys)
	rows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT id, workspace_id, card_id, kind, match_key, note,
		       dispatch_mode, status, expires_at,
		       created_by_source_id, created_by_attempt_id, triggered_turn_id,
		       resolved_reason, resolved_at, COALESCE(idempotency_key, ''),
		       created_at, updated_at
		FROM card_watchers
		WHERE workspace_id=$1
		  AND status='armed'
		  AND kind='event'
		  AND lower(match_key) IN (%s)
		ORDER BY created_at ASC
		FOR UPDATE
	`, inClause), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CardWatcher
	for rows.Next() {
		item, err := scanCardWatcher(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func findCardWatcherByIdempotency(
	ctx context.Context,
	tx *sqldb.Tx,
	workspaceID, cardID uuid.UUID,
	key string,
) (CardWatcher, bool, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, workspace_id, card_id, kind, match_key, note,
		       dispatch_mode, status, expires_at,
		       created_by_source_id, created_by_attempt_id, triggered_turn_id,
		       resolved_reason, resolved_at, COALESCE(idempotency_key, ''),
		       created_at, updated_at
		FROM card_watchers
		WHERE workspace_id=$1 AND card_id=$2 AND idempotency_key=$3
	`, workspaceID, cardID, key)
	item, err := scanCardWatcher(row)
	if errors.Is(err, sqldb.ErrNoRows) {
		return CardWatcher{}, false, nil
	}
	if err != nil {
		return CardWatcher{}, false, err
	}
	return item, true, nil
}

func loadCardWatcher(
	ctx context.Context,
	tx *sqldb.Tx,
	workspaceID, cardID, watcherID uuid.UUID,
) (CardWatcher, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, workspace_id, card_id, kind, match_key, note,
		       dispatch_mode, status, expires_at,
		       created_by_source_id, created_by_attempt_id, triggered_turn_id,
		       resolved_reason, resolved_at, COALESCE(idempotency_key, ''),
		       created_at, updated_at
		FROM card_watchers
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
	`, workspaceID, cardID, watcherID)
	item, err := scanCardWatcher(row)
	if errors.Is(err, sqldb.ErrNoRows) {
		return CardWatcher{}, domain.ErrNotFound
	}
	return item, err
}

func loadCardWatcherForUpdate(
	ctx context.Context,
	tx *sqldb.Tx,
	workspaceID, watcherID uuid.UUID,
) (CardWatcher, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, workspace_id, card_id, kind, match_key, note,
		       dispatch_mode, status, expires_at,
		       created_by_source_id, created_by_attempt_id, triggered_turn_id,
		       resolved_reason, resolved_at, COALESCE(idempotency_key, ''),
		       created_at, updated_at
		FROM card_watchers
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, workspaceID, watcherID)
	item, err := scanCardWatcher(row)
	if errors.Is(err, sqldb.ErrNoRows) {
		return CardWatcher{}, domain.ErrNotFound
	}
	return item, err
}

func scanCardWatcher(row scannableRow) (CardWatcher, error) {
	var out CardWatcher
	var kind, dispatchMode, status string
	var resolvedAt *time.Time
	err := row.Scan(
		&out.ID, &out.WorkspaceID, &out.CardID, &kind, &out.MatchKey, &out.Note,
		&dispatchMode, &status, &out.ExpiresAt,
		&out.CreatedBySourceID, &out.CreatedByAttemptID, &out.TriggeredTurnID,
		&out.ResolvedReason, &resolvedAt, &out.IdempotencyKey,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return CardWatcher{}, err
	}
	out.Kind = domain.WatcherKind(kind)
	out.DispatchMode = domain.DispatchMode(dispatchMode)
	out.Status = domain.WatcherStatus(status)
	out.ExpiresAt = out.ExpiresAt.UTC()
	if resolvedAt != nil {
		t := resolvedAt.UTC()
		out.ResolvedAt = &t
	}
	return out, nil
}
