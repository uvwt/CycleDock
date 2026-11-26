package agentartifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"google.golang.org/adk/v2/artifact"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/sqldb"
)

// ErrNotFound 表示请求的 Artifact 不存在，或已超过保留期被后台清理任务回收。
var ErrNotFound = errors.New("artifact not found or expired")

// Service 提供基于 sqldb 的双数据库（PostgreSQL / SQLite）Artifact 持久化实现。
type Service struct {
	db *sqldb.DB
}

// PostgresService 为历史名称别名，保持既有调用方平滑过渡。
type PostgresService = Service

func NewService(db *sqldb.DB) *Service {
	return &Service{db: db}
}

func NewPostgresService(db *sqldb.DB) *Service {
	return NewService(db)
}

func (s *Service) Save(ctx context.Context, req *artifact.SaveRequest) (*artifact.SaveResponse, error) {
	if req == nil {
		return nil, errors.New("artifact save request is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.Part == nil {
		return nil, errors.New("artifact part is required")
	}

	rawPart, _, err := agentdata.MarshalJSONB(req.Part)
	if err != nil {
		return nil, fmt.Errorf("encode artifact part: %w", err)
	}
	metadata := normalizeMetadata(req.CustomMetadata)
	rawMetadata, _, err := agentdata.MarshalJSONB(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode artifact metadata: %w", err)
	}

	var version int64
	err = s.db.WithTx(ctx, func(tx *sqldb.Tx) error {
		// 同一逻辑文件的版本号由事务锁串行分配：
		// PostgreSQL 使用事务级 advisory lock，SQLite 由单写事务（BEGIN IMMEDIATE）天然串行化。
		lockKeyBytes, err := json.Marshal([]string{req.AppName, req.UserID, req.SessionID, req.FileName})
		if err != nil {
			return fmt.Errorf("encode artifact lock key: %w", err)
		}
		if err := tx.AdvisoryLock(ctx, string(lockKeyBytes)); err != nil {
			return fmt.Errorf("lock artifact version: %w", err)
		}

		version = req.Version
		if version == 0 {
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(MAX(version), 0) + 1
				FROM agent_artifacts
				WHERE app_name=$1 AND user_id=$2 AND session_id=$3 AND file_name=$4
			`, req.AppName, req.UserID, req.SessionID, req.FileName).Scan(&version); err != nil {
				return fmt.Errorf("allocate artifact version: %w", err)
			}
		}
		now := time.Now().UTC()
		_, err = tx.Exec(ctx, `
			INSERT INTO agent_artifacts(
				app_name, user_id, session_id, file_name, version,
				part, mime_type, custom_metadata, created_at
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`, req.AppName, req.UserID, req.SessionID, req.FileName, version,
			sqldb.JSON(rawPart), partMIMEType(req.Part), sqldb.JSON(rawMetadata), now)
		if err != nil {
			return fmt.Errorf("save artifact %q version %d: %w", req.FileName, version, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &artifact.SaveResponse{Version: version}, nil
}

func (s *Service) Load(ctx context.Context, req *artifact.LoadRequest) (*artifact.LoadResponse, error) {
	if req == nil {
		return nil, errors.New("artifact load request is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}

	query := `
		SELECT part
		FROM agent_artifacts
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3 AND file_name=$4`
	args := []any{req.AppName, req.UserID, req.SessionID, req.FileName}
	if req.Version > 0 {
		query += " AND version=$5"
		args = append(args, req.Version)
	} else {
		query += " ORDER BY version DESC LIMIT 1"
	}

	var raw []byte
	if err := s.db.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
		if sqldb.IsNoRows(err) {
			return nil, fmt.Errorf("%w: artifact %q not found (it may have expired and been cleaned up)", ErrNotFound, req.FileName)
		}
		return nil, fmt.Errorf("load artifact %q: %w", req.FileName, err)
	}
	var part genai.Part
	if err := json.Unmarshal(raw, &part); err != nil {
		return nil, fmt.Errorf("decode artifact %q: %w", req.FileName, err)
	}
	return &artifact.LoadResponse{Part: &part}, nil
}

func (s *Service) Delete(ctx context.Context, req *artifact.DeleteRequest) error {
	if req == nil {
		return errors.New("artifact delete request is nil")
	}
	if err := req.Validate(); err != nil {
		return err
	}
	query := `
		DELETE FROM agent_artifacts
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3 AND file_name=$4`
	args := []any{req.AppName, req.UserID, req.SessionID, req.FileName}
	if req.Version > 0 {
		query += " AND version=$5"
		args = append(args, req.Version)
	}
	if _, err := s.db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("delete artifact %q: %w", req.FileName, err)
	}
	return nil
}

func (s *Service) List(ctx context.Context, req *artifact.ListRequest) (*artifact.ListResponse, error) {
	if req == nil {
		return nil, errors.New("artifact list request is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT file_name
		FROM agent_artifacts
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3
		ORDER BY file_name
	`, req.AppName, req.UserID, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan artifact name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	return &artifact.ListResponse{FileNames: names}, nil
}

func (s *Service) Versions(ctx context.Context, req *artifact.VersionsRequest) (*artifact.VersionsResponse, error) {
	if req == nil {
		return nil, errors.New("artifact versions request is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT version
		FROM agent_artifacts
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3 AND file_name=$4
		ORDER BY version
	`, req.AppName, req.UserID, req.SessionID, req.FileName)
	if err != nil {
		return nil, fmt.Errorf("list artifact versions: %w", err)
	}
	defer rows.Close()

	versions := make([]int64, 0)
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan artifact version: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list artifact versions: %w", err)
	}
	return &artifact.VersionsResponse{Versions: versions}, nil
}

func (s *Service) GetArtifactVersion(
	ctx context.Context,
	req *artifact.GetArtifactVersionRequest,
) (*artifact.GetArtifactVersionResponse, error) {
	if req == nil {
		return nil, errors.New("artifact version request is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	query := `
		SELECT version, mime_type, custom_metadata, created_at
		FROM agent_artifacts
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3 AND file_name=$4`
	args := []any{req.AppName, req.UserID, req.SessionID, req.FileName}
	if req.Version > 0 {
		query += " AND version=$5"
		args = append(args, req.Version)
	} else {
		query += " ORDER BY version DESC LIMIT 1"
	}

	var version int64
	var mimeType string
	var rawMetadata []byte
	var createdAt time.Time
	err := s.db.QueryRow(ctx, query, args...).Scan(&version, &mimeType, &rawMetadata, &createdAt)
	if sqldb.IsNoRows(err) {
		return nil, fmt.Errorf("%w: artifact %q not found (it may have expired and been cleaned up)", ErrNotFound, req.FileName)
	}
	if err != nil {
		return nil, fmt.Errorf("load artifact metadata %q: %w", req.FileName, err)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
		return nil, fmt.Errorf("decode artifact metadata %q: %w", req.FileName, err)
	}
	return &artifact.GetArtifactVersionResponse{
		ArtifactVersion: &artifact.ArtifactVersion{
			Version:        version,
			CanonicalURI:   canonicalURI(req.AppName, req.UserID, req.SessionID, req.FileName, version),
			CustomMetadata: metadata,
			CreateTime:     createdAt,
			MimeType:       mimeType,
		},
	}, nil
}

func normalizeMetadata(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		cleanKey, _ := agentdata.NormalizeText(key)
		cleanVal, _ := agentdata.NormalizeText(fmt.Sprint(value))
		out[cleanKey] = cleanVal
	}
	return out
}

func partMIMEType(part *genai.Part) string {
	switch {
	case part == nil:
		return ""
	case part.InlineData != nil:
		return part.InlineData.MIMEType
	case part.FileData != nil:
		return part.FileData.MIMEType
	case part.Text != "":
		return "text/plain"
	default:
		return ""
	}
}

func canonicalURI(appName, userID, sessionID, fileName string, version int64) string {
	return fmt.Sprintf(
		"cycledock-artifact://%s/%s/%s/%s?version=%d",
		url.PathEscape(appName),
		url.PathEscape(userID),
		url.PathEscape(sessionID),
		url.PathEscape(fileName),
		version,
	)
}

var _ artifact.Service = (*Service)(nil)
