package agentartifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/genai"
)

type PostgresService struct {
	pool *pgxpool.Pool
}

func NewPostgresService(pool *pgxpool.Pool) *PostgresService {
	return &PostgresService{pool: pool}
}

func (s *PostgresService) Save(ctx context.Context, req *artifact.SaveRequest) (*artifact.SaveResponse, error) {
	if req == nil {
		return nil, errors.New("artifact save request is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.Part == nil {
		return nil, errors.New("artifact part is required")
	}

	rawPart, err := json.Marshal(req.Part)
	if err != nil {
		return nil, fmt.Errorf("encode artifact part: %w", err)
	}
	metadata := normalizeMetadata(req.CustomMetadata)
	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode artifact metadata: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin artifact save: %w", err)
	}
	defer tx.Rollback(ctx)

	// Version numbers are scoped to one logical file. The advisory lock keeps
	// concurrent writers deterministic without introducing a separate heads table.
	lockKeyBytes, err := json.Marshal([]string{req.AppName, req.UserID, req.SessionID, req.FileName})
	if err != nil {
		return nil, fmt.Errorf("encode artifact lock key: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", string(lockKeyBytes)); err != nil {
		return nil, fmt.Errorf("lock artifact version: %w", err)
	}

	version := req.Version
	if version == 0 {
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(version), 0) + 1
			FROM agent_artifacts
			WHERE app_name=$1 AND user_id=$2 AND session_id=$3 AND file_name=$4
		`, req.AppName, req.UserID, req.SessionID, req.FileName).Scan(&version); err != nil {
			return nil, fmt.Errorf("allocate artifact version: %w", err)
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO agent_artifacts(
			app_name, user_id, session_id, file_name, version,
			part, mime_type, custom_metadata
		)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8::jsonb)
	`, req.AppName, req.UserID, req.SessionID, req.FileName, version,
		rawPart, partMIMEType(req.Part), rawMetadata)
	if err != nil {
		return nil, fmt.Errorf("save artifact %q version %d: %w", req.FileName, version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit artifact save: %w", err)
	}
	return &artifact.SaveResponse{Version: version}, nil
}

func (s *PostgresService) Load(ctx context.Context, req *artifact.LoadRequest) (*artifact.LoadResponse, error) {
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
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("artifact %q not found", req.FileName)
		}
		return nil, fmt.Errorf("load artifact %q: %w", req.FileName, err)
	}
	var part genai.Part
	if err := json.Unmarshal(raw, &part); err != nil {
		return nil, fmt.Errorf("decode artifact %q: %w", req.FileName, err)
	}
	return &artifact.LoadResponse{Part: &part}, nil
}

func (s *PostgresService) Delete(ctx context.Context, req *artifact.DeleteRequest) error {
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
	if _, err := s.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("delete artifact %q: %w", req.FileName, err)
	}
	return nil
}

func (s *PostgresService) List(ctx context.Context, req *artifact.ListRequest) (*artifact.ListResponse, error) {
	if req == nil {
		return nil, errors.New("artifact list request is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
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

func (s *PostgresService) Versions(ctx context.Context, req *artifact.VersionsRequest) (*artifact.VersionsResponse, error) {
	if req == nil {
		return nil, errors.New("artifact versions request is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
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

func (s *PostgresService) GetArtifactVersion(
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
	err := s.pool.QueryRow(ctx, query, args...).Scan(&version, &mimeType, &rawMetadata, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("artifact %q not found", req.FileName)
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
		out[key] = fmt.Sprint(value)
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

var _ artifact.Service = (*PostgresService)(nil)
