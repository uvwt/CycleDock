package attachments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultTTL             = 30 * 24 * time.Hour
	DefaultMaxTotalBytes   = int64(1 << 30) // 1 GiB
	DefaultCleanupInterval = 15 * time.Minute
)

var (
	ErrNotFound = errors.New("attachment not found")
	ErrExpired  = errors.New("attachment has expired and been purged")
)

type Config struct {
	Dir                string
	TTL                time.Duration
	MaxTotalBytes      int64
	MaxUploadBytes     int64
	MaxTextUploadBytes int64
	MaxDimension       int
	CleanupInterval    time.Duration
	Logger             *slog.Logger
}

type Attachment struct {
	ID                 uuid.UUID  `json:"id"`
	WorkspaceID        uuid.UUID  `json:"workspace_id"`
	CardID             *uuid.UUID `json:"card_id,omitempty"`
	UploadedBySourceID uuid.UUID  `json:"-"`
	URL                string     `json:"url"`
	Kind               string     `json:"kind"`
	FileName           string     `json:"file_name,omitempty"`
	StorageEncoding    string     `json:"storage_encoding,omitempty"`
	MimeType           string     `json:"mime_type"`
	SizeBytes          int64      `json:"size_bytes"`
	StoredSizeBytes    int64      `json:"stored_size_bytes,omitempty"`
	LineCount          int        `json:"line_count,omitempty"`
	Width              int        `json:"width"`
	Height             int        `json:"height"`
	SHA256             string     `json:"-"`
	RelativePath       string     `json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
	ExpiresAt          time.Time  `json:"expires_at"`
	PurgedAt           *time.Time `json:"-"`
}

type OpenedAttachment struct {
	Attachment
	File *os.File
}

type UploadParams struct {
	WorkspaceID        uuid.UUID
	CardID             *uuid.UUID
	UploadedBySourceID uuid.UUID
	FileName           string
	RawData            []byte
}

type CleanupStats struct {
	ExpiredRows  int
	EvictedRows  int
	DeletedFiles int
	FreedBytes   int64
}

// Service 管理本地临时磁盘附件（图片及 gzip 压缩纯文本/日志/代码）的落盘、鉴权读取以及 TTL + 总容量上限双重自动清理。
// 本地目录独立于 PostgreSQL 与云端 R2 备份，数据库仅保存轻量元数据行。
type Service struct {
	pool               *pgxpool.Pool
	dir                string
	ttl                time.Duration
	maxTotalBytes      int64
	maxUploadBytes     int64
	maxTextUploadBytes int64
	maxDimension       int
	cleanupInterval    time.Duration
	logger             *slog.Logger
}

func DefaultUploadDir() string {
	return filepath.Join(os.TempDir(), "cycledock-uploads")
}

func AttachmentURL(workspaceID, attachmentID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/workspaces/%s/attachments/%s", workspaceID.String(), attachmentID.String())
}

func New(pool *pgxpool.Pool, cfg Config) (*Service, error) {
	if pool == nil {
		return nil, errors.New("attachments service requires database pool")
	}
	dir := strings.TrimSpace(cfg.Dir)
	if dir == "" {
		dir = DefaultUploadDir()
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve upload dir: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return nil, fmt.Errorf("create upload dir %q: %w", absDir, err)
	}
	// 启动时校验目录可写，尽早暴露容器挂载权限问题。
	probe, err := os.CreateTemp(absDir, ".cycledock-write-probe-*")
	if err != nil {
		return nil, fmt.Errorf("upload dir %q is not writable: %w", absDir, err)
	}
	probePath := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probePath)

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	maxTotal := cfg.MaxTotalBytes
	if maxTotal <= 0 {
		maxTotal = DefaultMaxTotalBytes
	}
	maxUpload := cfg.MaxUploadBytes
	if maxUpload <= 0 {
		maxUpload = DefaultMaxUploadBytes
	}
	maxTextUpload := cfg.MaxTextUploadBytes
	if maxTextUpload <= 0 {
		maxTextUpload = DefaultMaxTextUploadBytes
	}
	maxDim := cfg.MaxDimension
	if maxDim <= 0 {
		maxDim = DefaultMaxDimension
	}
	interval := cfg.CleanupInterval
	if interval <= 0 {
		interval = DefaultCleanupInterval
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		pool:               pool,
		dir:                absDir,
		ttl:                ttl,
		maxTotalBytes:      maxTotal,
		maxUploadBytes:     maxUpload,
		maxTextUploadBytes: maxTextUpload,
		maxDimension:       maxDim,
		cleanupInterval:    interval,
		logger:             logger,
	}, nil
}

func (s *Service) Dir() string               { return s.dir }
func (s *Service) MaxUploadBytes() int64     { return s.maxUploadBytes }
func (s *Service) MaxTextUploadBytes() int64 { return s.maxTextUploadBytes }

// Upload 校验并压缩用户上传的图片或纯文本/日志/代码文件，按 workspace_id/YYYY-MM/<id>.<ext> 落盘（同工作区内按 SHA-256 复用文件），
// 并在 workspace_attachments 中写入轻量元数据记录。
func (s *Service) Upload(ctx context.Context, p UploadParams) (Attachment, error) {
	if p.WorkspaceID == uuid.Nil || p.UploadedBySourceID == uuid.Nil {
		return Attachment{}, errors.New("workspace_id and uploaded_by_source_id are required")
	}
	if int64(len(p.RawData)) > s.maxUploadBytes {
		return Attachment{}, fmt.Errorf("%w: max %d bytes", ErrImageTooLarge, s.maxUploadBytes)
	}

	if p.CardID != nil && *p.CardID != uuid.Nil {
		var exists bool
		err := s.pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM cards WHERE workspace_id = $1 AND id = $2)
		`, p.WorkspaceID, *p.CardID).Scan(&exists)
		if err != nil {
			return Attachment{}, fmt.Errorf("check attachment card: %w", err)
		}
		if !exists {
			return Attachment{}, ErrNotFound
		}
	}

	var (
		kind            string
		fileName        string
		storageEncoding string
		mimeType        string
		sizeBytes       int64
		storedSizeBytes int64
		lineCount       int
		width           int
		height          int
		sha256Hex       string
		fileExt         string
		diskBytes       []byte
	)

	if _, _, isImg := DetectImageFormat(p.RawData); isImg {
		processed, err := ProcessImage(p.RawData, s.maxDimension)
		if err != nil {
			return Attachment{}, err
		}
		kind = KindImage
		fileName = SanitizeFileName(p.FileName)
		storageEncoding = EncodingIdentity
		mimeType = processed.MimeType
		sizeBytes = processed.SizeBytes
		storedSizeBytes = processed.SizeBytes
		width = processed.Width
		height = processed.Height
		sha256Hex = processed.SHA256
		fileExt = processed.Ext
		diskBytes = processed.Data
	} else if IsAllowedTextFileName(p.FileName) {
		processed, err := ProcessTextFile(p.FileName, p.RawData, s.maxTextUploadBytes)
		if err != nil {
			return Attachment{}, err
		}
		kind = KindText
		fileName = processed.FileName
		storageEncoding = processed.StorageEncoding
		mimeType = processed.MimeType
		sizeBytes = processed.SizeBytes
		storedSizeBytes = processed.StoredSizeBytes
		lineCount = processed.LineCount
		sha256Hex = processed.SHA256
		fileExt = processed.Ext
		diskBytes = processed.CompressedData
	} else {
		if len(p.RawData) == 0 {
			return Attachment{}, ErrEmptyImage
		}
		return Attachment{}, fmt.Errorf(
			"%w: only JPEG/PNG/WebP/GIF images and plain text/log/code files are allowed",
			ErrUnsupportedFormat,
		)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	expiresAt := now.Add(s.ttl)
	id := uuid.New()

	// 同工作区内按 SHA-256 去重：仅当复用目标磁盘文件真实存在时才共享 relative_path，绝不跨工作区共享。
	var relPath string
	var reusedFile bool
	var existingPath, existingMime, existingEncoding string
	var existingSize, existingStoredSize int64
	var existingLines, existingW, existingH int
	err := s.pool.QueryRow(ctx, `
		SELECT relative_path, mime_type, storage_encoding, size_bytes, stored_size_bytes, line_count, width, height
		FROM workspace_attachments
		WHERE workspace_id = $1
		  AND kind = $2
		  AND sha256 = $3
		  AND purged_at IS NULL
		  AND expires_at > $4
		ORDER BY created_at DESC
		LIMIT 1
	`, p.WorkspaceID, kind, sha256Hex, now).Scan(
		&existingPath, &existingMime, &existingEncoding, &existingSize, &existingStoredSize, &existingLines, &existingW, &existingH,
	)
	if err == nil && existingPath != "" {
		if absPath, pathErr := s.resolveSafePath(existingPath); pathErr == nil {
			if stat, statErr := os.Stat(absPath); statErr == nil && !stat.IsDir() && stat.Size() > 0 {
				relPath = existingPath
				mimeType = existingMime
				storageEncoding = existingEncoding
				sizeBytes = existingSize
				storedSizeBytes = existingStoredSize
				lineCount = existingLines
				width = existingW
				height = existingH
				reusedFile = true
			}
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, fmt.Errorf("lookup deduplicated attachment: %w", err)
	}

	var writtenAbsPath string
	if !reusedFile {
		relPath = path.Join(p.WorkspaceID.String(), now.Format("2006-01"), id.String()+fileExt)
		absPath, err := s.resolveSafePath(relPath)
		if err != nil {
			return Attachment{}, err
		}
		if err := writeAtomicFile(absPath, diskBytes); err != nil {
			return Attachment{}, fmt.Errorf("store attachment file: %w", err)
		}
		writtenAbsPath = absPath
	}

	var cardIDArg *uuid.UUID
	if p.CardID != nil && *p.CardID != uuid.Nil {
		cid := *p.CardID
		cardIDArg = &cid
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO workspace_attachments (
			id, workspace_id, card_id, uploaded_by_source_id,
			kind, file_name, storage_encoding,
			mime_type, size_bytes, stored_size_bytes, line_count, width, height,
			sha256, relative_path, created_at, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`,
		id, p.WorkspaceID, cardIDArg, p.UploadedBySourceID,
		kind, fileName, storageEncoding,
		mimeType, sizeBytes, storedSizeBytes, lineCount, width, height,
		sha256Hex, relPath, now, expiresAt,
	)
	if err != nil {
		if writtenAbsPath != "" {
			_ = os.Remove(writtenAbsPath)
		}
		return Attachment{}, fmt.Errorf("insert attachment metadata: %w", err)
	}

	return Attachment{
		ID:                 id,
		WorkspaceID:        p.WorkspaceID,
		CardID:             cardIDArg,
		UploadedBySourceID: p.UploadedBySourceID,
		URL:                AttachmentURL(p.WorkspaceID, id),
		Kind:               kind,
		FileName:           fileName,
		StorageEncoding:    storageEncoding,
		MimeType:           mimeType,
		SizeBytes:          sizeBytes,
		StoredSizeBytes:    storedSizeBytes,
		LineCount:          lineCount,
		Width:              width,
		Height:             height,
		SHA256:             sha256Hex,
		RelativePath:       relPath,
		CreatedAt:          now,
		ExpiresAt:          expiresAt,
	}, nil
}

// Open 按工作区隔离校验附件元数据并打开本地文件流。
// 若附件已过期、已清理或底层磁盘文件已缺失，统一返回 ErrExpired（HTTP 层映射为 410 Gone）。
func (s *Service) Open(ctx context.Context, workspaceID, attachmentID uuid.UUID) (OpenedAttachment, error) {
	if workspaceID == uuid.Nil || attachmentID == uuid.Nil {
		return OpenedAttachment{}, ErrNotFound
	}
	var att Attachment
	err := s.pool.QueryRow(ctx, `
		SELECT
			id, workspace_id, card_id, uploaded_by_source_id,
			kind, file_name, storage_encoding,
			mime_type, size_bytes, stored_size_bytes, line_count, width, height,
			sha256, relative_path, created_at, expires_at, purged_at
		FROM workspace_attachments
		WHERE id = $1 AND workspace_id = $2
	`, attachmentID, workspaceID).Scan(
		&att.ID, &att.WorkspaceID, &att.CardID, &att.UploadedBySourceID,
		&att.Kind, &att.FileName, &att.StorageEncoding,
		&att.MimeType, &att.SizeBytes, &att.StoredSizeBytes, &att.LineCount, &att.Width, &att.Height,
		&att.SHA256, &att.RelativePath, &att.CreatedAt, &att.ExpiresAt, &att.PurgedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return OpenedAttachment{}, ErrNotFound
	}
	if err != nil {
		return OpenedAttachment{}, fmt.Errorf("load attachment metadata: %w", err)
	}
	att.URL = AttachmentURL(att.WorkspaceID, att.ID)

	now := time.Now().UTC()
	if att.PurgedAt != nil || !att.ExpiresAt.After(now) {
		return OpenedAttachment{Attachment: att}, ErrExpired
	}

	absPath, err := s.resolveSafePath(att.RelativePath)
	if err != nil {
		return OpenedAttachment{Attachment: att}, ErrExpired
	}
	file, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 宿主机目录重建或手工清理后懒同步 purged_at，保持元数据与磁盘一致。
			_, _ = s.pool.Exec(ctx, `
				UPDATE workspace_attachments
				SET purged_at = COALESCE(purged_at, $3)
				WHERE id = $1 AND workspace_id = $2
			`, attachmentID, workspaceID, now)
			return OpenedAttachment{Attachment: att}, ErrExpired
		}
		return OpenedAttachment{}, fmt.Errorf("open attachment file: %w", err)
	}
	return OpenedAttachment{Attachment: att, File: file}, nil
}

// ReadBytes 按需从本地磁盘读取附件逻辑内容：图片返回落盘字节，gzip 文本附件透明解压为 UTF-8 原始字节。
func (s *Service) ReadBytes(ctx context.Context, workspaceID, attachmentID uuid.UUID) (Attachment, []byte, error) {
	opened, err := s.Open(ctx, workspaceID, attachmentID)
	if err != nil {
		return opened.Attachment, nil, err
	}
	defer opened.File.Close()

	if opened.StorageEncoding == EncodingGzip {
		data, err := DecompressGzip(opened.File, s.maxTextUploadBytes)
		if err != nil {
			return opened.Attachment, nil, fmt.Errorf("read gzip attachment file: %w", err)
		}
		return opened.Attachment, data, nil
	}

	data, err := io.ReadAll(io.LimitReader(opened.File, s.maxUploadBytes+1))
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("read attachment file: %w", err)
	}
	if int64(len(data)) > s.maxUploadBytes {
		return Attachment{}, nil, ErrImageTooLarge
	}
	return opened.Attachment, data, nil
}

// RunBackgroundCleanup 周期性执行 TTL 过期淘汰与容量上限 LRU 清理，进程退出时优雅停止。
func (s *Service) RunBackgroundCleanup(ctx context.Context) {
	// 启动后立即清理一次，确保停机期间过期的附件尽快释放磁盘。
	if stats, err := s.RunCleanup(ctx, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("initial attachment cleanup failed", "error", err)
	} else if stats.ExpiredRows > 0 || stats.EvictedRows > 0 {
		s.logger.Info("attachment cleanup completed",
			"expired_rows", stats.ExpiredRows,
			"evicted_rows", stats.EvictedRows,
			"deleted_files", stats.DeletedFiles,
			"freed_bytes", stats.FreedBytes,
		)
	}

	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats, err := s.RunCleanup(ctx, time.Now().UTC())
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				s.logger.Warn("periodic attachment cleanup failed", "error", err)
				continue
			}
			if stats.ExpiredRows > 0 || stats.EvictedRows > 0 {
				s.logger.Info("attachment cleanup completed",
					"expired_rows", stats.ExpiredRows,
					"evicted_rows", stats.EvictedRows,
					"deleted_files", stats.DeletedFiles,
					"freed_bytes", stats.FreedBytes,
				)
			}
		}
	}
}

// RunCleanup 按两个维度执行自动淘汰：
// 1. 超过 expires_at 的 TTL 过期附件；
// 2. 活跃附件磁盘总占用超过 maxTotalBytes 时，按最早创建时间（LRU/FIFO）逐批淘汰直至回到水位线以下。
func (s *Service) RunCleanup(ctx context.Context, now time.Time) (CleanupStats, error) {
	now = now.UTC()
	var stats CleanupStats

	// 第一阶段：清理所有已超过保留时长 TTL 的记录。
	rows, err := s.pool.Query(ctx, `
		UPDATE workspace_attachments
		SET purged_at = $1
		WHERE purged_at IS NULL AND expires_at <= $1
		RETURNING relative_path, stored_size_bytes
	`, now)
	if err != nil {
		return stats, fmt.Errorf("purge expired attachments: %w", err)
	}
	type purgedFile struct {
		relPath   string
		sizeBytes int64
	}
	var expiredPaths []purgedFile
	for rows.Next() {
		var pf purgedFile
		if err := rows.Scan(&pf.relPath, &pf.sizeBytes); err != nil {
			rows.Close()
			return stats, fmt.Errorf("scan expired attachment: %w", err)
		}
		expiredPaths = append(expiredPaths, pf)
		stats.ExpiredRows++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return stats, err
	}

	seen := make(map[string]struct{}, len(expiredPaths))
	for _, pf := range expiredPaths {
		if _, dup := seen[pf.relPath]; dup {
			continue
		}
		seen[pf.relPath] = struct{}{}
		deleted, err := s.unlinkIfUnreferenced(ctx, pf.relPath)
		if err != nil {
			return stats, err
		}
		if deleted {
			stats.DeletedFiles++
			stats.FreedBytes += pf.sizeBytes
		}
	}

	// 第二阶段：检查活跃附件的去重后总磁盘占用，若超过上限则按最早创建时间淘汰。
	totalBytes, err := s.activeUniqueBytes(ctx)
	if err != nil {
		return stats, err
	}
	for totalBytes > s.maxTotalBytes {
		var victimID uuid.UUID
		var relPath string
		var sizeBytes int64
		err := s.pool.QueryRow(ctx, `
			SELECT id, relative_path, stored_size_bytes
			FROM workspace_attachments
			WHERE purged_at IS NULL
			ORDER BY created_at ASC, id ASC
			LIMIT 1
		`).Scan(&victimID, &relPath, &sizeBytes)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("select oldest attachment for eviction: %w", err)
		}

		// 若同一物理文件被多条活跃记录引用，逐条淘汰最老记录；当最后一条引用被淘汰时物理文件被真正删除。
		if _, err := s.pool.Exec(ctx, `
			UPDATE workspace_attachments
			SET purged_at = $2
			WHERE id = $1 AND purged_at IS NULL
		`, victimID, now); err != nil {
			return stats, fmt.Errorf("mark evicted attachment: %w", err)
		}
		stats.EvictedRows++

		deleted, err := s.unlinkIfUnreferenced(ctx, relPath)
		if err != nil {
			return stats, err
		}
		if deleted {
			stats.DeletedFiles++
			stats.FreedBytes += sizeBytes
			totalBytes -= sizeBytes
			if totalBytes < 0 {
				totalBytes = 0
			}
		}
	}

	return stats, nil
}

func (s *Service) activeUniqueBytes(ctx context.Context) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(stored_size_bytes), 0)
		FROM (
			SELECT DISTINCT ON (relative_path) relative_path, stored_size_bytes
			FROM workspace_attachments
			WHERE purged_at IS NULL
			ORDER BY relative_path, created_at DESC
		) active_files
	`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("compute active attachment bytes: %w", err)
	}
	return total, nil
}

func (s *Service) unlinkIfUnreferenced(ctx context.Context, relPath string) (bool, error) {
	var stillReferenced bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM workspace_attachments
			WHERE relative_path = $1 AND purged_at IS NULL
		)
	`, relPath).Scan(&stillReferenced); err != nil {
		return false, fmt.Errorf("check attachment path references: %w", err)
	}
	if stillReferenced {
		return false, nil
	}
	absPath, err := s.resolveSafePath(relPath)
	if err != nil {
		return false, nil
	}
	if err := os.Remove(absPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("remove attachment file %q: %w", relPath, err)
	}
	s.removeEmptyParentDirs(filepath.Dir(absPath))
	return true, nil
}

func (s *Service) resolveSafePath(relPath string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relPath)))
	if cleaned == "" || cleaned == "." || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("invalid attachment relative path %q", relPath)
	}
	absPath := filepath.Join(s.dir, cleaned)
	relCheck, err := filepath.Rel(s.dir, absPath)
	if err != nil || relCheck == "." || strings.HasPrefix(relCheck, "..") {
		return "", fmt.Errorf("attachment path escapes upload root: %q", relPath)
	}
	return absPath, nil
}

func (s *Service) removeEmptyParentDirs(dir string) {
	for cur := dir; cur != s.dir && strings.HasPrefix(cur, s.dir); cur = filepath.Dir(cur) {
		if err := os.Remove(cur); err != nil {
			break
		}
	}
}

func writeAtomicFile(dest string, data []byte) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
