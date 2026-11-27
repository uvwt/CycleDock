package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/sqldb"
)

const (
	// DefaultRetentionDays 是历史过程事件与非图片工具产物的默认保留天数。
	DefaultRetentionDays = 7
	// DefaultInterval 是后台定时清理任务的默认执行周期。
	DefaultInterval = 6 * time.Hour
	// DefaultBatchSize 是每个短事务内最多删除的记录数，避免长事务与锁表影响线上对话。
	DefaultBatchSize = 500
	// DefaultMaxBatchesPerRun 是单轮清理任务的最大批次数，防止单次清理在积压场景下无限占用资源。
	DefaultMaxBatchesPerRun = 200

	// advisoryLockKey 用于避免多副本同时执行保留策略清理。
	advisoryLockKey = "cycledock:retention_cleaner"
)

// Config 描述 ADK 过程表（agent_session_events / agent_artifacts）的后台分级保留配置。
type Config struct {
	Enabled        bool
	DryRun         bool
	RetentionDays  int
	Interval       time.Duration
	BatchSize      int
	MaxBatches     int
	StartupDelay   time.Duration
}

// Normalized 返回补全默认值的配置副本，不修改调用方原始结构体。
func (c Config) Normalized() Config {
	out := c
	if out.RetentionDays <= 0 {
		out.RetentionDays = DefaultRetentionDays
	}
	if out.Interval <= 0 {
		out.Interval = DefaultInterval
	}
	if out.BatchSize <= 0 {
		out.BatchSize = DefaultBatchSize
	}
	if out.MaxBatches <= 0 {
		out.MaxBatches = DefaultMaxBatchesPerRun
	}
	if out.StartupDelay < 0 {
		out.StartupDelay = 0
	}
	return out
}

// RunStats 汇总单次清理执行（或 dry-run 演练）的命中与删除统计。
type RunStats struct {
	DryRun                      bool
	Cutoff                      time.Time
	SkippedByLock               bool
	ClosedSessionEventsDeleted  int64
	ClosedArtifactsDeleted      int64
	ActiveSessionEventsDeleted  int64
	ActiveArtifactsDeleted      int64
	BatchesExecuted             int
	ReachedBatchLimit           bool
	Duration                    time.Duration
}

// TotalDeleted 返回本轮删除（或 dry-run 预估待删除）的总记录数。
func (s RunStats) TotalDeleted() int64 {
	return s.ClosedSessionEventsDeleted +
		s.ClosedArtifactsDeleted +
		s.ActiveSessionEventsDeleted +
		s.ActiveArtifactsDeleted
}

// Cleaner 负责按卡片生命周期与 compaction 覆盖边界，周期性分批清理过期的
// agent_session_events 和 agent_artifacts。
//
// 所有清理操作严格只作用于 ADK 过程表，绝不修改 events（用户可见气泡）、
// agent_model_calls（耗时与 Token 统计）、card_turns、turn_attempts 或 cards 元数据。
type Cleaner struct {
	db     *sqldb.DB
	config Config
	logger *slog.Logger
	now    func() time.Time
}

// NewCleaner 创建分级保留清理器。
func NewCleaner(db *sqldb.DB, cfg Config, logger *slog.Logger) *Cleaner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Cleaner{
		db:     db,
		config: cfg.Normalized(),
		logger: logger,
		now:    time.Now,
	}
}

// Run 启动后台定时清理循环，直到 ctx 被取消。
func (c *Cleaner) Run(ctx context.Context) {
	if !c.config.Enabled {
		c.logger.Info("db retention cleaner disabled")
		return
	}
	c.logger.Info("db retention cleaner started",
		"dry_run", c.config.DryRun,
		"retention_days", c.config.RetentionDays,
		"interval", c.config.Interval.String(),
		"batch_size", c.config.BatchSize,
		"max_batches", c.config.MaxBatches,
	)

	if c.config.StartupDelay > 0 {
		timer := time.NewTimer(c.config.StartupDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	c.runLogged(ctx)

	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runLogged(ctx)
		}
	}
}

func (c *Cleaner) runLogged(ctx context.Context) {
	stats, err := c.RunOnce(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.logger.Error("db retention cleanup failed", "error", err)
		return
	}
	if stats.SkippedByLock {
		c.logger.Info("db retention cleanup skipped because another worker holds the advisory lock")
		return
	}
	c.logger.Info("db retention cleanup completed",
		"dry_run", stats.DryRun,
		"cutoff", stats.Cutoff.UTC().Format(time.RFC3339),
		"closed_session_events", stats.ClosedSessionEventsDeleted,
		"closed_artifacts", stats.ClosedArtifactsDeleted,
		"active_session_events", stats.ActiveSessionEventsDeleted,
		"active_artifacts", stats.ActiveArtifactsDeleted,
		"total", stats.TotalDeleted(),
		"batches", stats.BatchesExecuted,
		"reached_batch_limit", stats.ReachedBatchLimit,
		"duration_ms", stats.Duration.Milliseconds(),
	)
}

// RunOnce 执行一轮完整的分级保留清理（或 dry-run 统计）。
// 每次执行可安全重试，且通过跨库 session lock 防止多实例并发重入。
func (c *Cleaner) RunOnce(ctx context.Context) (RunStats, error) {
	started := c.now().UTC()
	cutoff := started.Add(-time.Duration(c.config.RetentionDays) * 24 * time.Hour)
	stats := RunStats{
		DryRun: c.config.DryRun,
		Cutoff: cutoff,
	}

	unlock, locked, err := c.db.TrySessionLock(ctx, advisoryLockKey)
	if err != nil {
		return stats, fmt.Errorf("try retention advisory lock: %w", err)
	}
	if !locked {
		stats.SkippedByLock = true
		stats.Duration = c.now().UTC().Sub(started)
		return stats, nil
	}
	defer unlock()

	if c.config.DryRun {
		if err := c.countDryRun(ctx, cutoff, &stats); err != nil {
			return stats, err
		}
		stats.Duration = c.now().UTC().Sub(started)
		return stats, nil
	}

	remainingBatches := c.config.MaxBatches
	runLoop := func(step func(context.Context, time.Time, int) (int64, error), counter *int64) error {
		for remainingBatches > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			deleted, err := step(ctx, cutoff, c.config.BatchSize)
			if err != nil {
				return err
			}
			if deleted == 0 {
				return nil
			}
			*counter += deleted
			stats.BatchesExecuted++
			remainingBatches--
			if deleted < int64(c.config.BatchSize) {
				return nil
			}
		}
		stats.ReachedBatchLimit = true
		return nil
	}

	// 先清理已完结/已归档卡片的产物与事件，再清理活跃卡片中被 compaction 覆盖的过期明细。
	if err := runLoop(c.deleteClosedArtifactsBatch, &stats.ClosedArtifactsDeleted); err != nil {
		return stats, err
	}
	if err := runLoop(c.deleteClosedSessionEventsBatch, &stats.ClosedSessionEventsDeleted); err != nil {
		return stats, err
	}
	if err := runLoop(c.deleteActiveArtifactsBatch, &stats.ActiveArtifactsDeleted); err != nil {
		return stats, err
	}
	if err := runLoop(c.deleteActiveSessionEventsBatch, &stats.ActiveSessionEventsDeleted); err != nil {
		return stats, err
	}

	stats.Duration = c.now().UTC().Sub(started)
	return stats, nil
}

// 下面四条 SQL 共用同一套卡片分级判定语义：
// 1. 已完结/已归档卡片：todo/done、ongoing/archived、discussion/archived，且 cards.updated_at < $cutoff，
//    同时保护项目主聊天（projects.main_discussion_id）永不误判为归档卡，并跳过当前存在 running Attempt 的卡片。
// 2. 活跃卡片：除上述完结/归档态之外的所有卡片（含主聊天、discussion/active、ongoing/active、ongoing/paused、
//    todo/todo、todo/working、todo/review），同样跳过存在 running Attempt 的卡片，且严格以该卡片“最新 1 条
//    compaction 摘要事件”为锚点：
//    - 必留最新 1 条 compaction 摘要事件本身；
//    - 必留所有 occurred_at > latest_compaction_end 的尾部事件；
//    - 仅清理同时满足 occurred_at <= latest_compaction_end 且 occurred_at < $cutoff 的旧原始事件，
//      以及早于最新一条的旧 compaction 摘要事件（同样要求其 end_timestamp/occurred_at < $cutoff）；
//    - 产物仅清理同时满足 mime_type NOT LIKE 'image/%'、created_at <= latest_compaction_end 且 created_at < $cutoff。
//
// 关于已完结/已归档卡片对 agent_session_events 的保留：
// 若卡片存在 compaction 摘要，保留最新 1 条 compaction 摘要事件（仅占极少空间，却保证若用户未来重开该卡片或归档后恢复时，
// ADK 依然有上下文摘要可衔接）；其余所有事件在超过保留窗口后全部清理。若卡片从未产生 compaction，则超过保留窗口的
// 事件全部清理（用户可见气泡仍完整保存在 events 表中）。

const closedCardPredicate = `
    (
        (c.kind = 'todo' AND c.todo_state = 'done')
        OR (c.kind = 'ongoing' AND c.ongoing_state = 'archived')
        OR (c.kind = 'discussion' AND c.discussion_state = 'archived')
    )
    AND c.updated_at < $2
    AND NOT EXISTS (
        SELECT 1 FROM projects p
        WHERE p.main_discussion_id = c.id
    )
    AND NOT EXISTS (
        SELECT 1 FROM turn_attempts ta
        WHERE ta.workspace_id = c.workspace_id
          AND ta.card_id = c.id
          AND ta.status = 'running'
    )
`

const activeCardPredicate = `
    (
        NOT (
            (c.kind = 'todo' AND c.todo_state = 'done')
            OR (c.kind = 'ongoing' AND c.ongoing_state = 'archived')
            OR (c.kind = 'discussion' AND c.discussion_state = 'archived')
        )
        OR EXISTS (
            SELECT 1 FROM projects p
            WHERE p.main_discussion_id = c.id
        )
    )
    AND NOT EXISTS (
        SELECT 1 FROM turn_attempts ta
        WHERE ta.workspace_id = c.workspace_id
          AND ta.card_id = c.id
          AND ta.status = 'running'
    )
`

// latestCompactionCTE 提取每个会话最新一条有效 compaction 摘要事件的 sequence 与 endTimestamp。
// 使用跨库标准窗口函数 ROW_NUMBER()，在 PostgreSQL 和 SQLite 下返回严格一致的最新锚点。
func (c *Cleaner) latestCompactionCTE() string {
	if c.db.IsPostgres() {
		return `
latest_compaction AS (
    SELECT app_name, user_id, session_id, latest_compaction_seq, latest_compaction_end
    FROM (
        SELECT
            e.app_name,
            e.user_id,
            e.session_id,
            e.sequence AS latest_compaction_seq,
            (e.event -> 'actions' -> 'compaction' ->> 'endTimestamp')::timestamptz AS latest_compaction_end,
            ROW_NUMBER() OVER (
                PARTITION BY e.app_name, e.user_id, e.session_id
                ORDER BY (e.event -> 'actions' -> 'compaction' ->> 'endTimestamp')::timestamptz DESC, e.sequence DESC
            ) AS rn
        FROM agent_session_events e
        WHERE e.app_name = $1
          AND (e.event -> 'actions' -> 'compaction') IS NOT NULL
          AND NULLIF(e.event -> 'actions' -> 'compaction' ->> 'endTimestamp', '') IS NOT NULL
    ) ranked
    WHERE rn = 1
)
`
	}
	// SQLite：json_extract 读取 endTimestamp（UTC RFC3339），并兼容不同小数位数时采用 julianday 排序。
	return `
latest_compaction AS (
    SELECT app_name, user_id, session_id, latest_compaction_seq, latest_compaction_end
    FROM (
        SELECT
            e.app_name,
            e.user_id,
            e.session_id,
            e.sequence AS latest_compaction_seq,
            json_extract(e.event, '$.actions.compaction.endTimestamp') AS latest_compaction_end,
            ROW_NUMBER() OVER (
                PARTITION BY e.app_name, e.user_id, e.session_id
                ORDER BY julianday(json_extract(e.event, '$.actions.compaction.endTimestamp')) DESC, e.sequence DESC
            ) AS rn
        FROM agent_session_events e
        WHERE e.app_name = $1
          AND json_extract(e.event, '$.actions.compaction') IS NOT NULL
          AND NULLIF(json_extract(e.event, '$.actions.compaction.endTimestamp'), '') IS NOT NULL
    ) ranked
    WHERE rn = 1
)
`
}

func (c *Cleaner) cardJoinOn(alias string) string {
	if c.db.IsPostgres() {
		return fmt.Sprintf(`%s.app_name = $1 AND %s.user_id = c.workspace_id::text AND %s.session_id = c.id::text`, alias, alias, alias)
	}
	return fmt.Sprintf(`%s.app_name = $1 AND %s.user_id = c.workspace_id AND %s.session_id = c.id`, alias, alias, alias)
}

func (c *Cleaner) compactionExpr(alias string) string {
	if c.db.IsPostgres() {
		return fmt.Sprintf(`(%s.event -> 'actions' -> 'compaction')`, alias)
	}
	return fmt.Sprintf(`json_extract(%s.event, '$.actions.compaction')`, alias)
}

func (c *Cleaner) timeLTE(lhs, rhs string) string {
	if c.db.IsPostgres() {
		return lhs + " <= " + rhs
	}
	return fmt.Sprintf("julianday(%s) <= julianday(%s)", lhs, rhs)
}

func (c *Cleaner) countDryRun(ctx context.Context, cutoff time.Time, stats *RunStats) error {
	appName := agentsession.AppName
	cte := c.latestCompactionCTE()
	compExpr := c.compactionExpr("e")

	if err := c.db.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_artifacts a
		JOIN cards c
		  ON `+c.cardJoinOn("a")+`
		WHERE a.mime_type NOT LIKE 'image/%'
		  AND `+closedCardPredicate,
		appName, cutoff,
	).Scan(&stats.ClosedArtifactsDeleted); err != nil {
		return fmt.Errorf("dry-run count closed artifacts: %w", err)
	}

	if err := c.db.QueryRow(ctx, `
		WITH `+cte+`
		SELECT count(*)
		FROM agent_session_events e
		JOIN cards c
		  ON `+c.cardJoinOn("e")+`
		LEFT JOIN latest_compaction lc
		  ON lc.app_name = e.app_name
		 AND lc.user_id = e.user_id
		 AND lc.session_id = e.session_id
		WHERE `+closedCardPredicate+`
		  AND e.occurred_at < $2
		  AND (lc.latest_compaction_seq IS NULL OR e.sequence <> lc.latest_compaction_seq)
	`, appName, cutoff).Scan(&stats.ClosedSessionEventsDeleted); err != nil {
		return fmt.Errorf("dry-run count closed session events: %w", err)
	}

	if err := c.db.QueryRow(ctx, `
		WITH `+cte+`
		SELECT count(*)
		FROM agent_artifacts a
		JOIN cards c
		  ON `+c.cardJoinOn("a")+`
		JOIN latest_compaction lc
		  ON lc.app_name = a.app_name
		 AND lc.user_id = a.user_id
		 AND lc.session_id = a.session_id
		WHERE a.mime_type NOT LIKE 'image/%'
		  AND `+c.timeLTE("a.created_at", "lc.latest_compaction_end")+`
		  AND a.created_at < $2
		  AND `+activeCardPredicate,
		appName, cutoff,
	).Scan(&stats.ActiveArtifactsDeleted); err != nil {
		return fmt.Errorf("dry-run count active artifacts: %w", err)
	}

	if err := c.db.QueryRow(ctx, `
		WITH `+cte+`
		SELECT count(*)
		FROM agent_session_events e
		JOIN cards c
		  ON `+c.cardJoinOn("e")+`
		JOIN latest_compaction lc
		  ON lc.app_name = e.app_name
		 AND lc.user_id = e.user_id
		 AND lc.session_id = e.session_id
		WHERE `+activeCardPredicate+`
		  AND e.sequence <> lc.latest_compaction_seq
		  AND (
		      (
		          `+compExpr+` IS NULL
		          AND `+c.timeLTE("e.occurred_at", "lc.latest_compaction_end")+`
		          AND e.occurred_at < $2
		      )
		      OR (
		          `+compExpr+` IS NOT NULL
		          AND e.occurred_at < $2
		      )
		  )
	`, appName, cutoff).Scan(&stats.ActiveSessionEventsDeleted); err != nil {
		return fmt.Errorf("dry-run count active session events: %w", err)
	}

	return nil
}

func (c *Cleaner) deleteClosedArtifactsBatch(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if c.db.IsPostgres() {
		return c.execBatchDelete(ctx, `
			WITH target AS (
				SELECT a.ctid
				FROM agent_artifacts a
				JOIN cards c
				  ON `+c.cardJoinOn("a")+`
				WHERE a.mime_type NOT LIKE 'image/%'
				  AND `+closedCardPredicate+`
				ORDER BY a.created_at ASC
				LIMIT $3
			)
			DELETE FROM agent_artifacts a
			USING target
			WHERE a.ctid = target.ctid
		`, agentsession.AppName, cutoff, batchSize)
	}
	return c.execBatchDelete(ctx, `
		DELETE FROM agent_artifacts
		WHERE rowid IN (
			SELECT a.rowid
			FROM agent_artifacts a
			JOIN cards c
			  ON `+c.cardJoinOn("a")+`
			WHERE a.mime_type NOT LIKE 'image/%'
			  AND `+closedCardPredicate+`
			ORDER BY a.created_at ASC
			LIMIT $3
		)
	`, agentsession.AppName, cutoff, batchSize)
}

func (c *Cleaner) deleteClosedSessionEventsBatch(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	cte := c.latestCompactionCTE()
	if c.db.IsPostgres() {
		return c.execBatchDelete(ctx, `
			WITH `+cte+`,
			target AS (
				SELECT e.sequence
				FROM agent_session_events e
				JOIN cards c
				  ON `+c.cardJoinOn("e")+`
				LEFT JOIN latest_compaction lc
				  ON lc.app_name = e.app_name
				 AND lc.user_id = e.user_id
				 AND lc.session_id = e.session_id
				WHERE `+closedCardPredicate+`
				  AND e.occurred_at < $2
				  AND (lc.latest_compaction_seq IS NULL OR e.sequence <> lc.latest_compaction_seq)
				ORDER BY e.sequence ASC
				LIMIT $3
			)
			DELETE FROM agent_session_events e
			USING target
			WHERE e.sequence = target.sequence
		`, agentsession.AppName, cutoff, batchSize)
	}
	return c.execBatchDelete(ctx, `
		WITH `+cte+`
		DELETE FROM agent_session_events
		WHERE sequence IN (
			SELECT e.sequence
			FROM agent_session_events e
			JOIN cards c
			  ON `+c.cardJoinOn("e")+`
			LEFT JOIN latest_compaction lc
			  ON lc.app_name = e.app_name
			 AND lc.user_id = e.user_id
			 AND lc.session_id = e.session_id
			WHERE `+closedCardPredicate+`
			  AND e.occurred_at < $2
			  AND (lc.latest_compaction_seq IS NULL OR e.sequence <> lc.latest_compaction_seq)
			ORDER BY e.sequence ASC
			LIMIT $3
		)
	`, agentsession.AppName, cutoff, batchSize)
}

func (c *Cleaner) deleteActiveArtifactsBatch(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	cte := c.latestCompactionCTE()
	if c.db.IsPostgres() {
		return c.execBatchDelete(ctx, `
			WITH `+cte+`,
			target AS (
				SELECT a.ctid
				FROM agent_artifacts a
				JOIN cards c
				  ON `+c.cardJoinOn("a")+`
				JOIN latest_compaction lc
				  ON lc.app_name = a.app_name
				 AND lc.user_id = a.user_id
				 AND lc.session_id = a.session_id
				WHERE a.mime_type NOT LIKE 'image/%'
				  AND `+c.timeLTE("a.created_at", "lc.latest_compaction_end")+`
				  AND a.created_at < $2
				  AND `+activeCardPredicate+`
				ORDER BY a.created_at ASC
				LIMIT $3
			)
			DELETE FROM agent_artifacts a
			USING target
			WHERE a.ctid = target.ctid
		`, agentsession.AppName, cutoff, batchSize)
	}
	return c.execBatchDelete(ctx, `
		WITH `+cte+`
		DELETE FROM agent_artifacts
		WHERE rowid IN (
			SELECT a.rowid
			FROM agent_artifacts a
			JOIN cards c
			  ON `+c.cardJoinOn("a")+`
			JOIN latest_compaction lc
			  ON lc.app_name = a.app_name
			 AND lc.user_id = a.user_id
			 AND lc.session_id = a.session_id
			WHERE a.mime_type NOT LIKE 'image/%'
			  AND `+c.timeLTE("a.created_at", "lc.latest_compaction_end")+`
			  AND a.created_at < $2
			  AND `+activeCardPredicate+`
			ORDER BY a.created_at ASC
			LIMIT $3
		)
	`, agentsession.AppName, cutoff, batchSize)
}

func (c *Cleaner) deleteActiveSessionEventsBatch(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	cte := c.latestCompactionCTE()
	compExpr := c.compactionExpr("e")
	if c.db.IsPostgres() {
		return c.execBatchDelete(ctx, `
			WITH `+cte+`,
			target AS (
				SELECT e.sequence
				FROM agent_session_events e
				JOIN cards c
				  ON `+c.cardJoinOn("e")+`
				JOIN latest_compaction lc
				  ON lc.app_name = e.app_name
				 AND lc.user_id = e.user_id
				 AND lc.session_id = e.session_id
				WHERE `+activeCardPredicate+`
				  AND e.sequence <> lc.latest_compaction_seq
				  AND (
				      (
				          `+compExpr+` IS NULL
				          AND `+c.timeLTE("e.occurred_at", "lc.latest_compaction_end")+`
				          AND e.occurred_at < $2
				      )
				      OR (
				          `+compExpr+` IS NOT NULL
				          AND e.occurred_at < $2
				      )
				  )
				ORDER BY e.sequence ASC
				LIMIT $3
			)
			DELETE FROM agent_session_events e
			USING target
			WHERE e.sequence = target.sequence
		`, agentsession.AppName, cutoff, batchSize)
	}
	return c.execBatchDelete(ctx, `
		WITH `+cte+`
		DELETE FROM agent_session_events
		WHERE sequence IN (
			SELECT e.sequence
			FROM agent_session_events e
			JOIN cards c
			  ON `+c.cardJoinOn("e")+`
			JOIN latest_compaction lc
			  ON lc.app_name = e.app_name
			 AND lc.user_id = e.user_id
			 AND lc.session_id = e.session_id
			WHERE `+activeCardPredicate+`
			  AND e.sequence <> lc.latest_compaction_seq
			  AND (
			      (
			          `+compExpr+` IS NULL
			          AND `+c.timeLTE("e.occurred_at", "lc.latest_compaction_end")+`
			          AND e.occurred_at < $2
			      )
			      OR (
			          `+compExpr+` IS NOT NULL
			          AND e.occurred_at < $2
			      )
			  )
			ORDER BY e.sequence ASC
			LIMIT $3
		)
	`, agentsession.AppName, cutoff, batchSize)
}

func (c *Cleaner) execBatchDelete(ctx context.Context, query string, args ...any) (int64, error) {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin retention batch tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// PostgreSQL 下为每个小批次设置有限的 lock_timeout，宁可让路给在线对话也不阻塞业务写入。
	if tx.Dialect() == sqldb.DialectPostgres {
		if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '3s'`); err != nil {
			return 0, fmt.Errorf("set retention lock_timeout: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("execute retention batch delete: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit retention batch tx: %w", err)
	}
	return tag.RowsAffected(), nil
}
