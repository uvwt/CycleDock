package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/store/db"
	"github.com/uvwt/CycleDock/internal/trigger"
)

const (
	maxTriggerCooldownSeconds = 86400
	defaultDeliveryListLimit  = 50
	maxDeliveryListLimit      = 200
)

type WorkspaceTrigger struct {
	ID                  uuid.UUID
	WorkspaceID         uuid.UUID
	Name                string
	Provider            domain.TriggerProvider
	AuthMode            domain.TriggerAuthMode
	SignatureHeader     string
	SecretCiphertext    []byte
	SecretHint          string
	Enabled             bool
	RoutingMode         domain.TriggerRoutingMode
	TargetProjectID     uuid.NullUUID
	TargetProjectName   string
	TargetCardID        uuid.NullUUID
	TargetCardProjectID uuid.NullUUID
	TargetCardKind      domain.CardKind
	TargetCardTitle     string
	DispatchMode        domain.DispatchMode
	FilterConfig        trigger.FilterConfig
	CooldownSeconds     int
	LastTriggeredAt     *time.Time
	LastDeliveryStatus  domain.TriggerDeliveryStatus
	LastDeliveryReason  string
	LastDeliveryAt      *time.Time
	CreatedBySourceID   uuid.NullUUID
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// TriggerCardRef 表示 Webhook 投递所唤醒或新建的关联卡片摘要，供前端日志直接跳转。
type TriggerCardRef struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Kind      domain.CardKind
	Title     string
}

type CreateTriggerInput struct {
	Name            string
	Provider        domain.TriggerProvider
	AuthMode        domain.TriggerAuthMode
	SignatureHeader string
	Secret          string
	Enabled         *bool
	RoutingMode     domain.TriggerRoutingMode
	TargetProjectID uuid.NullUUID
	TargetCardID    uuid.NullUUID
	DispatchMode    domain.DispatchMode
	FilterConfig    trigger.FilterConfig
	CooldownSeconds int
}

type UpdateTriggerInput struct {
	Name            *string
	Enabled         *bool
	SignatureHeader *string
	RoutingMode     *domain.TriggerRoutingMode
	TargetProjectID *uuid.NullUUID
	TargetCardID    *uuid.NullUUID
	DispatchMode    *domain.DispatchMode
	FilterConfig    *trigger.FilterConfig
	CooldownSeconds *int
}

type TriggerDelivery struct {
	ID                      uuid.UUID
	WorkspaceID             uuid.UUID
	TriggerID               uuid.UUID
	TriggerName             string
	DeliveryID              string
	Provider                domain.TriggerProvider
	EventType               string
	EventAction             string
	SignatureValid          bool
	Status                  domain.TriggerDeliveryStatus
	Reason                  string
	MatchKeys               []string
	PayloadSummary          string
	TriggeredTurnID         uuid.NullUUID
	TriggeredWatcherTurnIDs []uuid.UUID
	CreatedCardID           uuid.NullUUID
	LinkedCards             []TriggerCardRef
	ReceivedAt              time.Time
}

type WebhookProcessResult struct {
	Delivery     TriggerDelivery
	Reused       bool
	WokenCardIDs []uuid.UUID
}

// ConfigureTriggerEncryption 配置 Webhook Secret 的 AES-256-GCM 加密主密钥。
// 未单独配置时回退使用 MCP/Model 服务端加密密钥；两者皆缺失时拒绝保存触发器密钥，避免明文落库。
func (s *Store) ConfigureTriggerEncryption(encodedKey string) error {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		s.triggerCipher = nil
		return nil
	}
	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
		hex.DecodeString,
	}
	var key []byte
	for _, decode := range decoders {
		decoded, err := decode(encodedKey)
		if err == nil && len(decoded) == 32 {
			key = decoded
			break
		}
	}
	if len(key) != 32 {
		return errors.New("CYCLEDOCK_TRIGGER_ENCRYPTION_KEY must encode exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create trigger secret cipher: %w", err)
	}
	s.triggerCipher, err = cipher.NewGCM(block)
	return err
}

func (s *Store) activeTriggerCipher() cipher.AEAD {
	if s.triggerCipher != nil {
		return s.triggerCipher
	}
	if s.mcpCipher != nil {
		return s.mcpCipher
	}
	return s.modelCipher
}

func (s *Store) encryptTriggerSecret(workspaceID, triggerID, secret string) ([]byte, error) {
	aead := s.activeTriggerCipher()
	if aead == nil {
		return nil, errors.New("trigger secret encryption key is not configured")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate trigger secret nonce: %w", err)
	}
	aad := []byte("cycledock/trigger/" + workspaceID + "/" + triggerID)
	return aead.Seal(nonce, nonce, []byte(secret), aad), nil
}

// DecryptTriggerSecret 仅在服务端 Webhook 验签路径调用，绝不返回前端或写入日志。
func (s *Store) DecryptTriggerSecret(t WorkspaceTrigger) (string, error) {
	if len(t.SecretCiphertext) == 0 {
		return "", errors.New("trigger secret ciphertext is empty")
	}
	aead := s.activeTriggerCipher()
	if aead == nil {
		return "", errors.New("trigger secret encryption key is not configured")
	}
	nonceSize := aead.NonceSize()
	if len(t.SecretCiphertext) < nonceSize+aead.Overhead() {
		return "", errors.New("invalid trigger secret ciphertext")
	}
	aad := []byte("cycledock/trigger/" + t.WorkspaceID.String() + "/" + t.ID.String())
	plain, err := aead.Open(nil, t.SecretCiphertext[:nonceSize], t.SecretCiphertext[nonceSize:], aad)
	if err != nil {
		return "", errors.New("trigger secret decryption failed")
	}
	return string(plain), nil
}

func generateWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return "whsec_" + hex.EncodeToString(buf), nil
}

func formatSecretHint(secret string) string {
	trimmed := strings.TrimSpace(secret)
	if len(trimmed) <= 4 {
		return "****"
	}
	return "****" + trimmed[len(trimmed)-4:]
}

func defaultAuthModeForProvider(provider domain.TriggerProvider, requested domain.TriggerAuthMode) (domain.TriggerAuthMode, error) {
	switch provider {
	case domain.TriggerProviderGitHub:
		if requested != "" && requested != domain.TriggerAuthGitHubHMAC {
			return "", fmt.Errorf("github trigger requires auth_mode=%s", domain.TriggerAuthGitHubHMAC)
		}
		return domain.TriggerAuthGitHubHMAC, nil
	case domain.TriggerProviderCircleCI:
		if requested != "" && requested != domain.TriggerAuthCircleCIHMAC {
			return "", fmt.Errorf("circleci trigger requires auth_mode=%s", domain.TriggerAuthCircleCIHMAC)
		}
		return domain.TriggerAuthCircleCIHMAC, nil
	case domain.TriggerProviderGeneric:
		if requested == "" {
			return domain.TriggerAuthGenericHMAC, nil
		}
		if requested != domain.TriggerAuthGenericHMAC && requested != domain.TriggerAuthBearer {
			return "", fmt.Errorf("generic trigger requires auth_mode=%s or %s", domain.TriggerAuthGenericHMAC, domain.TriggerAuthBearer)
		}
		return requested, nil
	default:
		return "", fmt.Errorf("unsupported trigger provider %q", provider)
	}
}

func validateTriggerRouting(
	routingMode domain.TriggerRoutingMode,
	targetProjectID, targetCardID uuid.NullUUID,
) error {
	switch routingMode {
	case domain.TriggerRoutingFixedCard:
		if !targetCardID.Valid {
			return errors.New("fixed_card routing requires target_card_id")
		}
	case domain.TriggerRoutingCreateTodo:
		if !targetProjectID.Valid {
			return errors.New("create_todo routing requires target_project_id")
		}
		if targetCardID.Valid {
			return errors.New("create_todo routing cannot specify target_card_id")
		}
	case domain.TriggerRoutingMatchWatchers:
		if targetCardID.Valid {
			return errors.New("match_watchers routing cannot specify target_card_id")
		}
	default:
		return fmt.Errorf("unsupported routing_mode %q", routingMode)
	}
	return nil
}

func validateDispatchMode(mode domain.DispatchMode) (domain.DispatchMode, error) {
	if mode == "" {
		return domain.DispatchModeEnqueue, nil
	}
	if mode != domain.DispatchModeEnqueue && mode != domain.DispatchModeInterrupt {
		return "", fmt.Errorf("invalid dispatch_mode %q", mode)
	}
	return mode, nil
}

func (s *Store) CreateTrigger(
	ctx context.Context,
	workspaceID, actorSourceID uuid.UUID,
	input CreateTriggerInput,
) (WorkspaceTrigger, string, error) {
	name, _ := agentdata.NormalizeText(strings.TrimSpace(input.Name))
	if name == "" {
		return WorkspaceTrigger{}, "", errors.New("trigger name is required")
	}
	authMode, err := defaultAuthModeForProvider(input.Provider, input.AuthMode)
	if err != nil {
		return WorkspaceTrigger{}, "", err
	}
	dispatchMode, err := validateDispatchMode(input.DispatchMode)
	if err != nil {
		return WorkspaceTrigger{}, "", err
	}
	if input.CooldownSeconds < 0 || input.CooldownSeconds > maxTriggerCooldownSeconds {
		return WorkspaceTrigger{}, "", fmt.Errorf("cooldown_seconds must be between 0 and %d", maxTriggerCooldownSeconds)
	}
	input.FilterConfig.Normalize()
	if err := input.FilterConfig.Validate(); err != nil {
		return WorkspaceTrigger{}, "", err
	}

	targetProjectID := input.TargetProjectID
	targetCardID := input.TargetCardID
	if err := validateTriggerRouting(input.RoutingMode, targetProjectID, targetCardID); err != nil {
		return WorkspaceTrigger{}, "", err
	}

	secret := strings.TrimSpace(input.Secret)
	if secret == "" {
		secret, err = generateWebhookSecret()
		if err != nil {
			return WorkspaceTrigger{}, "", err
		}
	} else if len(secret) < 8 {
		return WorkspaceTrigger{}, "", errors.New("trigger secret must be at least 8 characters")
	}

	triggerID := uuid.New()
	ciphertext, err := s.encryptTriggerSecret(workspaceID.String(), triggerID.String(), secret)
	if err != nil {
		return WorkspaceTrigger{}, "", err
	}
	filterJSON, err := json.Marshal(input.FilterConfig)
	if err != nil {
		return WorkspaceTrigger{}, "", fmt.Errorf("encode filter_config: %w", err)
	}

	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	sigHeader := strings.TrimSpace(input.SignatureHeader)
	if authMode == domain.TriggerAuthGenericHMAC && sigHeader == "" {
		sigHeader = trigger.DefaultGenericSignatureHeader
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkspaceTrigger{}, "", err
	}
	defer tx.Rollback(ctx)

	if targetCardID.Valid {
		var cardProjectID uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT project_id FROM cards WHERE workspace_id=$1 AND id=$2
		`, workspaceID, targetCardID.UUID).Scan(&cardProjectID)
		if errors.Is(err, sqldb.ErrNoRows) {
			return WorkspaceTrigger{}, "", domain.ErrNotFound
		}
		if err != nil {
			return WorkspaceTrigger{}, "", err
		}
		targetProjectID = uuid.NullUUID{UUID: cardProjectID, Valid: true}
	} else if targetProjectID.Valid {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM projects WHERE workspace_id=$1 AND id=$2)
		`, workspaceID, targetProjectID.UUID).Scan(&exists); err != nil {
			return WorkspaceTrigger{}, "", err
		}
		if !exists {
			return WorkspaceTrigger{}, "", domain.ErrNotFound
		}
	}

	var createdSourceID uuid.NullUUID
	if actorSourceID != uuid.Nil {
		createdSourceID = uuid.NullUUID{UUID: actorSourceID, Valid: true}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO workspace_triggers (
			id, workspace_id, name, provider, auth_mode, signature_header,
			secret_ciphertext, secret_hint, enabled, routing_mode,
			target_project_id, target_card_id, dispatch_mode,
			filter_config, cooldown_seconds, created_by_source_id
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	`, triggerID, workspaceID, name, string(input.Provider), string(authMode), sigHeader,
		ciphertext, formatSecretHint(secret), enabled, string(input.RoutingMode),
		nullUUIDValue(targetProjectID), nullUUIDValue(targetCardID), string(dispatchMode),
		filterJSON, input.CooldownSeconds, nullUUIDValue(createdSourceID),
	)
	if err != nil {
		return WorkspaceTrigger{}, "", mapConstraintError(err)
	}

	created, err := loadTriggerRow(ctx, tx, `
		SELECT id, workspace_id, name, provider, auth_mode, signature_header,
		       secret_ciphertext, secret_hint, enabled, routing_mode,
		       target_project_id, target_card_id, dispatch_mode,
		       filter_config, cooldown_seconds, last_triggered_at,
		       created_by_source_id, created_at, updated_at
		FROM workspace_triggers
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, triggerID)
	if err != nil {
		return WorkspaceTrigger{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkspaceTrigger{}, "", err
	}
	return created, secret, nil
}

func (s *Store) GetTrigger(ctx context.Context, workspaceID, triggerID uuid.UUID) (WorkspaceTrigger, error) {
	return loadTriggerRow(ctx, s.pool, `
		SELECT id, workspace_id, name, provider, auth_mode, signature_header,
		       secret_ciphertext, secret_hint, enabled, routing_mode,
		       target_project_id, target_card_id, dispatch_mode,
		       filter_config, cooldown_seconds, last_triggered_at,
		       created_by_source_id, created_at, updated_at
		FROM workspace_triggers
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, triggerID)
}

// GetTriggerByID 仅供公开 Webhook 入口按 UUID 定位触发器，随后必须经过验签才可进入处理。
func (s *Store) GetTriggerByID(ctx context.Context, triggerID uuid.UUID) (WorkspaceTrigger, error) {
	return loadTriggerRow(ctx, s.pool, `
		SELECT id, workspace_id, name, provider, auth_mode, signature_header,
		       secret_ciphertext, secret_hint, enabled, routing_mode,
		       target_project_id, target_card_id, dispatch_mode,
		       filter_config, cooldown_seconds, last_triggered_at,
		       created_by_source_id, created_at, updated_at
		FROM workspace_triggers
		WHERE id=$1
	`, triggerID)
}

func (s *Store) ListTriggers(ctx context.Context, workspaceID uuid.UUID) ([]WorkspaceTrigger, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.workspace_id, t.name, t.provider, t.auth_mode, t.signature_header,
		       t.secret_ciphertext, t.secret_hint, t.enabled, t.routing_mode,
		       t.target_project_id, t.target_card_id, t.dispatch_mode,
		       t.filter_config, t.cooldown_seconds, t.last_triggered_at,
		       t.created_by_source_id, t.created_at, t.updated_at,
		       COALESCE(p.name, ''),
		       c.project_id,
		       COALESCE(c.kind, ''),
		       COALESCE(c.title, ''),
		       COALESCE((
		         SELECT d.status FROM trigger_deliveries d
		         WHERE d.workspace_id = t.workspace_id AND d.trigger_id = t.id
		         ORDER BY d.received_at DESC LIMIT 1
		       ), ''),
		       COALESCE((
		         SELECT d.reason FROM trigger_deliveries d
		         WHERE d.workspace_id = t.workspace_id AND d.trigger_id = t.id
		         ORDER BY d.received_at DESC LIMIT 1
		       ), ''),
		       (
		         SELECT d.received_at FROM trigger_deliveries d
		         WHERE d.workspace_id = t.workspace_id AND d.trigger_id = t.id
		         ORDER BY d.received_at DESC LIMIT 1
		       )
		FROM workspace_triggers t
		LEFT JOIN projects p ON p.workspace_id = t.workspace_id AND p.id = t.target_project_id
		LEFT JOIN cards c ON c.workspace_id = t.workspace_id AND c.id = t.target_card_id
		WHERE t.workspace_id=$1
		ORDER BY t.created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkspaceTrigger
	for rows.Next() {
		var item WorkspaceTrigger
		var provider, authMode, routingMode, dispatchMode string
		var targetCardKind, lastDeliveryStatus string
		var filterBytes []byte
		var lastTriggered, lastDeliveryAt *time.Time
		if err := rows.Scan(
			&item.ID, &item.WorkspaceID, &item.Name, &provider, &authMode, &item.SignatureHeader,
			&item.SecretCiphertext, &item.SecretHint, &item.Enabled, &routingMode,
			&item.TargetProjectID, &item.TargetCardID, &dispatchMode,
			&filterBytes, &item.CooldownSeconds, &lastTriggered,
			&item.CreatedBySourceID, &item.CreatedAt, &item.UpdatedAt,
			&item.TargetProjectName,
			&item.TargetCardProjectID,
			&targetCardKind,
			&item.TargetCardTitle,
			&lastDeliveryStatus,
			&item.LastDeliveryReason,
			&lastDeliveryAt,
		); err != nil {
			return nil, err
		}
		item.Provider = domain.TriggerProvider(provider)
		item.AuthMode = domain.TriggerAuthMode(authMode)
		item.RoutingMode = domain.TriggerRoutingMode(routingMode)
		item.DispatchMode = domain.DispatchMode(dispatchMode)
		if targetCardKind != "" {
			item.TargetCardKind = domain.CardKind(targetCardKind)
		}
		if lastDeliveryStatus != "" {
			item.LastDeliveryStatus = domain.TriggerDeliveryStatus(lastDeliveryStatus)
		}
		if lastTriggered != nil {
			t := lastTriggered.UTC()
			item.LastTriggeredAt = &t
		}
		if lastDeliveryAt != nil {
			t := lastDeliveryAt.UTC()
			item.LastDeliveryAt = &t
		}
		if len(filterBytes) > 0 {
			if err := json.Unmarshal(filterBytes, &item.FilterConfig); err != nil {
				return nil, fmt.Errorf("decode trigger filter_config: %w", err)
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpdateTrigger(
	ctx context.Context,
	workspaceID, triggerID uuid.UUID,
	input UpdateTriggerInput,
) (WorkspaceTrigger, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkspaceTrigger{}, err
	}
	defer tx.Rollback(ctx)

	current, err := loadTriggerRow(ctx, tx, `
		SELECT id, workspace_id, name, provider, auth_mode, signature_header,
		       secret_ciphertext, secret_hint, enabled, routing_mode,
		       target_project_id, target_card_id, dispatch_mode,
		       filter_config, cooldown_seconds, last_triggered_at,
		       created_by_source_id, created_at, updated_at
		FROM workspace_triggers
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, workspaceID, triggerID)
	if err != nil {
		return WorkspaceTrigger{}, err
	}

	if input.Name != nil {
		name, _ := agentdata.NormalizeText(strings.TrimSpace(*input.Name))
		if name == "" {
			return WorkspaceTrigger{}, errors.New("trigger name cannot be empty")
		}
		current.Name = name
	}
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.SignatureHeader != nil {
		current.SignatureHeader = strings.TrimSpace(*input.SignatureHeader)
	}
	if input.RoutingMode != nil {
		current.RoutingMode = *input.RoutingMode
	}
	if input.TargetProjectID != nil {
		current.TargetProjectID = *input.TargetProjectID
	}
	if input.TargetCardID != nil {
		current.TargetCardID = *input.TargetCardID
	}
	if current.RoutingMode == domain.TriggerRoutingCreateTodo || current.RoutingMode == domain.TriggerRoutingMatchWatchers {
		current.TargetCardID = uuid.NullUUID{}
	}
	if err := validateTriggerRouting(current.RoutingMode, current.TargetProjectID, current.TargetCardID); err != nil {
		return WorkspaceTrigger{}, err
	}
	if input.DispatchMode != nil {
		mode, err := validateDispatchMode(*input.DispatchMode)
		if err != nil {
			return WorkspaceTrigger{}, err
		}
		current.DispatchMode = mode
	}
	if input.CooldownSeconds != nil {
		if *input.CooldownSeconds < 0 || *input.CooldownSeconds > maxTriggerCooldownSeconds {
			return WorkspaceTrigger{}, fmt.Errorf("cooldown_seconds must be between 0 and %d", maxTriggerCooldownSeconds)
		}
		current.CooldownSeconds = *input.CooldownSeconds
	}
	if input.FilterConfig != nil {
		cfg := *input.FilterConfig
		cfg.Normalize()
		if err := cfg.Validate(); err != nil {
			return WorkspaceTrigger{}, err
		}
		current.FilterConfig = cfg
	}

	if current.TargetCardID.Valid {
		var cardProjectID uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT project_id FROM cards WHERE workspace_id=$1 AND id=$2
		`, workspaceID, current.TargetCardID.UUID).Scan(&cardProjectID)
		if errors.Is(err, sqldb.ErrNoRows) {
			return WorkspaceTrigger{}, domain.ErrNotFound
		}
		if err != nil {
			return WorkspaceTrigger{}, err
		}
		current.TargetProjectID = uuid.NullUUID{UUID: cardProjectID, Valid: true}
	} else if current.TargetProjectID.Valid {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM projects WHERE workspace_id=$1 AND id=$2)
		`, workspaceID, current.TargetProjectID.UUID).Scan(&exists); err != nil {
			return WorkspaceTrigger{}, err
		}
		if !exists {
			return WorkspaceTrigger{}, domain.ErrNotFound
		}
	}

	filterJSON, err := json.Marshal(current.FilterConfig)
	if err != nil {
		return WorkspaceTrigger{}, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE workspace_triggers
		SET name=$3, enabled=$4, signature_header=$5, routing_mode=$6,
		    target_project_id=$7, target_card_id=$8, dispatch_mode=$9,
		    filter_config=$10, cooldown_seconds=$11, updated_at=now()
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, triggerID, current.Name, current.Enabled, current.SignatureHeader,
		string(current.RoutingMode), nullUUIDValue(current.TargetProjectID),
		nullUUIDValue(current.TargetCardID), string(current.DispatchMode),
		filterJSON, current.CooldownSeconds)
	if err != nil {
		return WorkspaceTrigger{}, mapConstraintError(err)
	}
	updated, err := loadTriggerRow(ctx, tx, `
		SELECT id, workspace_id, name, provider, auth_mode, signature_header,
		       secret_ciphertext, secret_hint, enabled, routing_mode,
		       target_project_id, target_card_id, dispatch_mode,
		       filter_config, cooldown_seconds, last_triggered_at,
		       created_by_source_id, created_at, updated_at
		FROM workspace_triggers
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, triggerID)
	if err != nil {
		return WorkspaceTrigger{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkspaceTrigger{}, err
	}
	return updated, nil
}

func (s *Store) RotateTriggerSecret(
	ctx context.Context,
	workspaceID, triggerID uuid.UUID,
	customSecret string,
) (WorkspaceTrigger, string, error) {
	secret := strings.TrimSpace(customSecret)
	var err error
	if secret == "" {
		secret, err = generateWebhookSecret()
		if err != nil {
			return WorkspaceTrigger{}, "", err
		}
	} else if len(secret) < 8 {
		return WorkspaceTrigger{}, "", errors.New("trigger secret must be at least 8 characters")
	}
	ciphertext, err := s.encryptTriggerSecret(workspaceID.String(), triggerID.String(), secret)
	if err != nil {
		return WorkspaceTrigger{}, "", err
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE workspace_triggers
		SET secret_ciphertext=$3, secret_hint=$4, updated_at=now()
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, triggerID, ciphertext, formatSecretHint(secret))
	if err != nil {
		return WorkspaceTrigger{}, "", err
	}
	if tag.RowsAffected() != 1 {
		return WorkspaceTrigger{}, "", domain.ErrNotFound
	}
	updated, err := s.GetTrigger(ctx, workspaceID, triggerID)
	if err != nil {
		return WorkspaceTrigger{}, "", err
	}
	return updated, secret, nil
}

func (s *Store) DeleteTrigger(ctx context.Context, workspaceID, triggerID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM workspace_triggers
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, triggerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListTriggerDeliveries(
	ctx context.Context,
	workspaceID, triggerID uuid.UUID,
	limit int,
) ([]TriggerDelivery, error) {
	return s.listDeliveries(ctx, workspaceID, uuid.NullUUID{UUID: triggerID, Valid: true}, limit)
}

// ListWorkspaceTriggerDeliveries 返回当前工作区最近所有触发器的投递审计记录，便于集中排障。
func (s *Store) ListWorkspaceTriggerDeliveries(
	ctx context.Context,
	workspaceID uuid.UUID,
	limit int,
) ([]TriggerDelivery, error) {
	return s.listDeliveries(ctx, workspaceID, uuid.NullUUID{}, limit)
}

func (s *Store) listDeliveries(
	ctx context.Context,
	workspaceID uuid.UUID,
	triggerID uuid.NullUUID,
	limit int,
) ([]TriggerDelivery, error) {
	if limit <= 0 {
		limit = defaultDeliveryListLimit
	}
	if limit > maxDeliveryListLimit {
		limit = maxDeliveryListLimit
	}

	args := []any{workspaceID}
	var b strings.Builder
	b.WriteString(`
		SELECT d.id, d.workspace_id, d.trigger_id, COALESCE(t.name, ''),
		       d.delivery_id, d.provider,
		       d.event_type, d.event_action, d.signature_valid, d.status, d.reason,
		       d.match_keys, d.payload_summary, d.triggered_turn_id,
		       d.triggered_watcher_turn_ids, d.created_card_id, d.received_at
		FROM trigger_deliveries d
		LEFT JOIN workspace_triggers t
		  ON t.workspace_id = d.workspace_id AND t.id = d.trigger_id
		WHERE d.workspace_id = $1
	`)
	if triggerID.Valid {
		args = append(args, triggerID.UUID)
		fmt.Fprintf(&b, " AND d.trigger_id = $%d", len(args))
	}
	args = append(args, limit)
	fmt.Fprintf(&b, " ORDER BY d.received_at DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TriggerDelivery
	for rows.Next() {
		var item TriggerDelivery
		var provider, status string
		if err := rows.Scan(
			&item.ID, &item.WorkspaceID, &item.TriggerID, &item.TriggerName,
			&item.DeliveryID, &provider,
			&item.EventType, &item.EventAction, &item.SignatureValid, &status, &item.Reason,
			&item.MatchKeys, &item.PayloadSummary, &item.TriggeredTurnID,
			&item.TriggeredWatcherTurnIDs, &item.CreatedCardID, &item.ReceivedAt,
		); err != nil {
			return nil, err
		}
		item.Provider = domain.TriggerProvider(provider)
		item.Status = domain.TriggerDeliveryStatus(status)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachDeliveryLinkedCards(ctx, workspaceID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachDeliveryLinkedCards 批量反查投递所唤醒或新建的卡片摘要，避免列表逐行 N+1 查询。
func (s *Store) attachDeliveryLinkedCards(
	ctx context.Context,
	workspaceID uuid.UUID,
	items []TriggerDelivery,
) error {
	if len(items) == 0 {
		return nil
	}
	turnIDSet := make(map[uuid.UUID]struct{})
	cardIDSet := make(map[uuid.UUID]struct{})
	for _, item := range items {
		if item.CreatedCardID.Valid {
			cardIDSet[item.CreatedCardID.UUID] = struct{}{}
		}
		if item.TriggeredTurnID.Valid {
			turnIDSet[item.TriggeredTurnID.UUID] = struct{}{}
		}
		for _, turnID := range item.TriggeredWatcherTurnIDs {
			turnIDSet[turnID] = struct{}{}
		}
	}

	turnToCard := make(map[uuid.UUID]TriggerCardRef, len(turnIDSet))
	if len(turnIDSet) > 0 {
		turnIDs := make([]uuid.UUID, 0, len(turnIDSet))
		for id := range turnIDSet {
			turnIDs = append(turnIDs, id)
		}
		turnArgs := []any{workspaceID}
		turnIn := sqldb.AppendInPlaceholders(&turnArgs, turnIDs)
		rows, err := s.pool.Query(ctx, fmt.Sprintf(`
			SELECT ct.id, c.id, c.project_id, c.kind, c.title
			FROM card_turns ct
			JOIN cards c ON c.workspace_id = ct.workspace_id AND c.id = ct.card_id
			WHERE ct.workspace_id = $1 AND ct.id IN (%s)
		`, turnIn), turnArgs...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var turnID uuid.UUID
			var ref TriggerCardRef
			var kind string
			if err := rows.Scan(&turnID, &ref.ID, &ref.ProjectID, &kind, &ref.Title); err != nil {
				rows.Close()
				return err
			}
			ref.Kind = domain.CardKind(kind)
			turnToCard[turnID] = ref
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}

	cardByID := make(map[uuid.UUID]TriggerCardRef, len(cardIDSet))
	if len(cardIDSet) > 0 {
		cardIDs := make([]uuid.UUID, 0, len(cardIDSet))
		for id := range cardIDSet {
			cardIDs = append(cardIDs, id)
		}
		cardArgs := []any{workspaceID}
		cardIn := sqldb.AppendInPlaceholders(&cardArgs, cardIDs)
		rows, err := s.pool.Query(ctx, fmt.Sprintf(`
			SELECT id, project_id, kind, title
			FROM cards
			WHERE workspace_id = $1 AND id IN (%s)
		`, cardIn), cardArgs...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var ref TriggerCardRef
			var kind string
			if err := rows.Scan(&ref.ID, &ref.ProjectID, &kind, &ref.Title); err != nil {
				rows.Close()
				return err
			}
			ref.Kind = domain.CardKind(kind)
			cardByID[ref.ID] = ref
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}

	for i := range items {
		seen := make(map[uuid.UUID]struct{})
		appendCard := func(ref TriggerCardRef) {
			if _, ok := seen[ref.ID]; ok {
				return
			}
			seen[ref.ID] = struct{}{}
			items[i].LinkedCards = append(items[i].LinkedCards, ref)
		}
		if items[i].CreatedCardID.Valid {
			if ref, ok := cardByID[items[i].CreatedCardID.UUID]; ok {
				appendCard(ref)
			}
		}
		if items[i].TriggeredTurnID.Valid {
			if ref, ok := turnToCard[items[i].TriggeredTurnID.UUID]; ok {
				appendCard(ref)
			}
		}
		for _, turnID := range items[i].TriggeredWatcherTurnIDs {
			if ref, ok := turnToCard[turnID]; ok {
				appendCard(ref)
			}
		}
	}
	return nil
}

// RecordRejectedWebhookDelivery 持久化验签失败的审计记录。
// 因为 signature_valid=false 不参与 trigger_deliveries_verified_dedup_uq 唯一去重，
// 攻击者无法通过伪造 delivery_id 提前阻断后续合法签名投递。
func (s *Store) RecordRejectedWebhookDelivery(
	ctx context.Context,
	trig WorkspaceTrigger,
	deliveryID, eventType, eventAction, reason string,
	now time.Time,
) (TriggerDelivery, error) {
	now = now.UTC()
	if strings.TrimSpace(deliveryID) == "" {
		deliveryID = "rejected:" + uuid.NewString()
	}
	id := uuid.New()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trigger_deliveries (
			id, workspace_id, trigger_id, delivery_id, provider,
			event_type, event_action, signature_valid, status, reason, received_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,false,'rejected',$8,$9)
	`, id, trig.WorkspaceID, trig.ID, deliveryID, string(trig.Provider),
		strings.TrimSpace(eventType), strings.TrimSpace(eventAction), strings.TrimSpace(reason), now)
	if err != nil {
		return TriggerDelivery{}, err
	}
	return TriggerDelivery{
		ID:             id,
		WorkspaceID:    trig.WorkspaceID,
		TriggerID:      trig.ID,
		DeliveryID:     deliveryID,
		Provider:       trig.Provider,
		EventType:      strings.TrimSpace(eventType),
		EventAction:    strings.TrimSpace(eventAction),
		SignatureValid: false,
		Status:         domain.TriggerDeliveryRejected,
		Reason:         strings.TrimSpace(reason),
		ReceivedAt:     now,
	}, nil
}

// ProcessVerifiedWebhookDelivery 在单个数据库事务内原子完成：
// 1) (trigger_id, delivery_id) 幂等去重；
// 2) 中间态/过滤事件直接落审计表并返回，零 Turn 零 Token；
// 3) 优先匹配工作区内 armed 状态的 card_watchers 并唤醒原卡片；
// 4) 再按 Trigger 的持久化路由规则与冷却窗口决定是否入队 Turn 或新建 Todo 卡片。
func (s *Store) ProcessVerifiedWebhookDelivery(
	ctx context.Context,
	trig WorkspaceTrigger,
	event trigger.ParsedEvent,
	now time.Time,
) (WebhookProcessResult, error) {
	now = now.UTC()
	deliveryID := strings.TrimSpace(event.DeliveryID)
	if deliveryID == "" {
		return WebhookProcessResult{}, errors.New("delivery_id is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WebhookProcessResult{}, err
	}
	defer tx.Rollback(ctx)

	lockedTrig, err := loadTriggerRow(ctx, tx, `
		SELECT id, workspace_id, name, provider, auth_mode, signature_header,
		       secret_ciphertext, secret_hint, enabled, routing_mode,
		       target_project_id, target_card_id, dispatch_mode,
		       filter_config, cooldown_seconds, last_triggered_at,
		       created_by_source_id, created_at, updated_at
		FROM workspace_triggers
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, trig.WorkspaceID, trig.ID)
	if err != nil {
		return WebhookProcessResult{}, err
	}

	matchKeys := event.MatchKeys
	if matchKeys == nil {
		matchKeys = []string{}
	}
	summary := truncateTriggerSummary(event.Summary, 400)

	deliveryRowID := uuid.New()
	var insertedID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO trigger_deliveries (
			id, workspace_id, trigger_id, delivery_id, provider,
			event_type, event_action, signature_valid, status, reason,
			match_keys, payload_summary, received_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,true,'ignored','processing',$8,$9,$10)
		ON CONFLICT (trigger_id, delivery_id) WHERE (signature_valid = true)
		DO NOTHING
		RETURNING id
	`, deliveryRowID, lockedTrig.WorkspaceID, lockedTrig.ID, deliveryID, string(lockedTrig.Provider),
		event.EventType, event.Action, matchKeys, summary, now).Scan(&insertedID)
	if errors.Is(err, sqldb.ErrNoRows) {
		existing, loadErr := loadVerifiedDelivery(ctx, tx, lockedTrig.WorkspaceID, lockedTrig.ID, deliveryID)
		if loadErr != nil {
			return WebhookProcessResult{}, loadErr
		}
		if err := tx.Commit(ctx); err != nil {
			return WebhookProcessResult{}, err
		}
		return WebhookProcessResult{Delivery: existing, Reused: true}, nil
	}
	if err != nil {
		return WebhookProcessResult{}, err
	}

	status := domain.TriggerDeliveryIgnored
	reason := ""
	var primaryTurnID uuid.NullUUID
	var createdCardID uuid.NullUUID
	watcherTurnIDs := make([]uuid.UUID, 0)
	wokenCards := make(map[uuid.UUID]bool)
	wokenCardList := make([]uuid.UUID, 0)

	recordWokenCard := func(cardID uuid.UUID) {
		if !wokenCards[cardID] {
			wokenCards[cardID] = true
			wokenCardList = append(wokenCardList, cardID)
		}
	}

	switch {
	case !lockedTrig.Enabled:
		status = domain.TriggerDeliveryIgnored
		reason = "trigger_disabled"

	case event.Skip:
		status = domain.TriggerDeliveryFiltered
		reason = event.SkipReason

	default:
		// 第一阶段：优先唤醒工作区内命中的一次性 card_watchers。
		if len(matchKeys) > 0 {
			matched, err := loadArmedEventWatchersForUpdate(ctx, tx, lockedTrig.WorkspaceID, matchKeys)
			if err != nil {
				return WebhookProcessResult{}, err
			}
			for _, w := range matched {
				if err := lockCard(ctx, tx, lockedTrig.WorkspaceID, w.CardID); err != nil {
					if errors.Is(err, domain.ErrNotFound) {
						continue
					}
					return WebhookProcessResult{}, err
				}
				active, err := isCardRunnableTx(ctx, tx, lockedTrig.WorkspaceID, w.CardID)
				if err != nil {
					return WebhookProcessResult{}, err
				}
				if !active {
					if _, err := tx.Exec(ctx, `
						UPDATE card_watchers
						SET status='cancelled', resolved_reason='card_inactive', resolved_at=$3, updated_at=$3
						WHERE workspace_id=$1 AND id=$2
					`, lockedTrig.WorkspaceID, w.ID, now); err != nil {
						return WebhookProcessResult{}, err
					}
					continue
				}

				if err := supersedeAttemptForTurn(
					ctx, tx, lockedTrig.WorkspaceID, w.CardID, w.DispatchMode,
					"superseded by interrupting trigger watcher", now,
				); err != nil {
					return WebhookProcessResult{}, err
				}
				content := trigger.FormatUntrustedEnvelope(lockedTrig.Name, event, w.Note)
				turn, err := insertTurn(ctx, tx, insertTurnParams{
					WorkspaceID:    lockedTrig.WorkspaceID,
					CardID:         w.CardID,
					Source:         domain.TurnSourceTrigger,
					Content:        content,
					DispatchMode:   w.DispatchMode,
					IdempotencyKey: fmt.Sprintf("watcher:%s:%s", w.ID, deliveryID),
				})
				if err != nil {
					return WebhookProcessResult{}, err
				}
				if err := supersedePendingToolApprovalsTx(
					ctx, tx, lockedTrig.WorkspaceID, w.CardID,
					"已有外部事件唤醒卡片，旧审批已自动失效",
				); err != nil {
					return WebhookProcessResult{}, err
				}
				if _, err := tx.Exec(ctx, `
					UPDATE card_watchers
					SET status='triggered', triggered_turn_id=$3, resolved_reason=$4, resolved_at=$5, updated_at=$5
					WHERE workspace_id=$1 AND id=$2
				`, lockedTrig.WorkspaceID, w.ID, turn.ID, "matched_webhook:"+deliveryID, now); err != nil {
					return WebhookProcessResult{}, err
				}
				watcherTurnIDs = append(watcherTurnIDs, turn.ID)
				recordWokenCard(w.CardID)
			}
		}

		// 第二阶段：执行 Trigger 的持久化路由规则与冷却窗口防抖。
		inCooldown := lockedTrig.CooldownSeconds > 0 &&
			lockedTrig.LastTriggeredAt != nil &&
			now.Sub(*lockedTrig.LastTriggeredAt) < time.Duration(lockedTrig.CooldownSeconds)*time.Second

		switch lockedTrig.RoutingMode {
		case domain.TriggerRoutingMatchWatchers:
			if len(watcherTurnIDs) > 0 {
				status = domain.TriggerDeliveryTriggered
				reason = "matched_watchers"
			} else {
				status = domain.TriggerDeliveryIgnored
				reason = "no_matching_watcher"
			}

		case domain.TriggerRoutingFixedCard:
			targetCardID := lockedTrig.TargetCardID.UUID
			switch {
			case wokenCards[targetCardID]:
				// 目标卡片已由同一事件命中的 watcher 唤醒，避免同一投递重复入队两轮。
				status = domain.TriggerDeliveryTriggered
				reason = "watcher_woke_target_card"
			case inCooldown:
				if len(watcherTurnIDs) > 0 {
					status = domain.TriggerDeliveryTriggered
					reason = "watchers_triggered_rule_in_cooldown"
				} else {
					status = domain.TriggerDeliveryCooldown
					reason = "cooldown_active"
				}
			default:
				if err := lockCard(ctx, tx, lockedTrig.WorkspaceID, targetCardID); err != nil {
					if errors.Is(err, domain.ErrNotFound) {
						status = domain.TriggerDeliveryIgnored
						reason = "target_card_not_found"
						break
					}
					return WebhookProcessResult{}, err
				}
				active, err := isCardRunnableTx(ctx, tx, lockedTrig.WorkspaceID, targetCardID)
				if err != nil {
					return WebhookProcessResult{}, err
				}
				if !active {
					if len(watcherTurnIDs) > 0 {
						status = domain.TriggerDeliveryTriggered
						reason = "watchers_triggered_target_card_inactive"
					} else {
						status = domain.TriggerDeliveryIgnored
						reason = "target_card_inactive"
					}
					break
				}
				if err := supersedeAttemptForTurn(
					ctx, tx, lockedTrig.WorkspaceID, targetCardID, lockedTrig.DispatchMode,
					"superseded by interrupting webhook trigger", now,
				); err != nil {
					return WebhookProcessResult{}, err
				}
				content := trigger.FormatUntrustedEnvelope(lockedTrig.Name, event, "")
				turn, err := insertTurn(ctx, tx, insertTurnParams{
					WorkspaceID:    lockedTrig.WorkspaceID,
					CardID:         targetCardID,
					Source:         domain.TurnSourceTrigger,
					Content:        content,
					DispatchMode:   lockedTrig.DispatchMode,
					IdempotencyKey: fmt.Sprintf("trigger:%s:%s", lockedTrig.ID, deliveryID),
				})
				if err != nil {
					return WebhookProcessResult{}, err
				}
				if err := supersedePendingToolApprovalsTx(
					ctx, tx, lockedTrig.WorkspaceID, targetCardID,
					"已有外部事件触发新轮次，旧审批已自动失效",
				); err != nil {
					return WebhookProcessResult{}, err
				}
				if _, err := tx.Exec(ctx, `
					UPDATE workspace_triggers
					SET last_triggered_at=$3, updated_at=$3
					WHERE workspace_id=$1 AND id=$2
				`, lockedTrig.WorkspaceID, lockedTrig.ID, now); err != nil {
					return WebhookProcessResult{}, err
				}
				primaryTurnID = uuid.NullUUID{UUID: turn.ID, Valid: true}
				recordWokenCard(targetCardID)
				status = domain.TriggerDeliveryTriggered
				reason = "fixed_card_triggered"
			}

		case domain.TriggerRoutingCreateTodo:
			if inCooldown {
				if len(watcherTurnIDs) > 0 {
					status = domain.TriggerDeliveryTriggered
					reason = "watchers_triggered_rule_in_cooldown"
				} else {
					status = domain.TriggerDeliveryCooldown
					reason = "cooldown_active"
				}
				break
			}
			creatorSourceID, err := resolveTriggerCardCreatorSourceTx(ctx, tx, lockedTrig)
			if err != nil {
				return WebhookProcessResult{}, err
			}
			title := buildTriggeredTodoTitle(lockedTrig.Name, event)
			envelope := trigger.FormatUntrustedEnvelope(lockedTrig.Name, event, "")
			q := s.queries.WithTx(tx)
			card, err := q.CreateCard(ctx, db.CreateCardParams{
				ID:                    uuid.New(),
				WorkspaceID:           lockedTrig.WorkspaceID,
				ProjectID:             lockedTrig.TargetProjectID.UUID,
				Kind:                  string(domain.CardKindTodo),
				Title:                 title,
				Description:           envelope,
				TodoState:             sqldb.Text{String: string(domain.TodoStateTodo), Valid: true},
				RequiredConfirmations: 1,
				CreatedBySourceID:     creatorSourceID,
			})
			if err != nil {
				return WebhookProcessResult{}, mapConstraintError(err)
			}
			turn, err := insertTurn(ctx, tx, insertTurnParams{
				WorkspaceID:    lockedTrig.WorkspaceID,
				CardID:         card.ID,
				Source:         domain.TurnSourceTrigger,
				Content:        envelope,
				DispatchMode:   lockedTrig.DispatchMode,
				IdempotencyKey: fmt.Sprintf("trigger:%s:%s", lockedTrig.ID, deliveryID),
			})
			if err != nil {
				return WebhookProcessResult{}, err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE workspace_triggers
				SET last_triggered_at=$3, updated_at=$3
				WHERE workspace_id=$1 AND id=$2
			`, lockedTrig.WorkspaceID, lockedTrig.ID, now); err != nil {
				return WebhookProcessResult{}, err
			}
			createdCardID = uuid.NullUUID{UUID: card.ID, Valid: true}
			primaryTurnID = uuid.NullUUID{UUID: turn.ID, Valid: true}
			recordWokenCard(card.ID)
			status = domain.TriggerDeliveryTriggered
			reason = "todo_created"
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE trigger_deliveries
		SET status=$3, reason=$4, triggered_turn_id=$5,
		    triggered_watcher_turn_ids=$6, created_card_id=$7
		WHERE workspace_id=$1 AND id=$2
	`, lockedTrig.WorkspaceID, deliveryRowID, string(status), reason,
		nullUUIDValue(primaryTurnID), watcherTurnIDs, nullUUIDValue(createdCardID))
	if err != nil {
		return WebhookProcessResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WebhookProcessResult{}, err
	}

	if len(wokenCardList) > 0 {
		s.notifyWorkReady()
	}

	return WebhookProcessResult{
		Delivery: TriggerDelivery{
			ID:                      deliveryRowID,
			WorkspaceID:             lockedTrig.WorkspaceID,
			TriggerID:               lockedTrig.ID,
			DeliveryID:              deliveryID,
			Provider:                lockedTrig.Provider,
			EventType:               event.EventType,
			EventAction:             event.Action,
			SignatureValid:          true,
			Status:                  status,
			Reason:                  reason,
			MatchKeys:               matchKeys,
			PayloadSummary:          summary,
			TriggeredTurnID:         primaryTurnID,
			TriggeredWatcherTurnIDs: watcherTurnIDs,
			CreatedCardID:           createdCardID,
			ReceivedAt:              now,
		},
		Reused:       false,
		WokenCardIDs: wokenCardList,
	}, nil
}

func isCardRunnableTx(ctx context.Context, tx *sqldb.Tx, workspaceID, cardID uuid.UUID) (bool, error) {
	var runnable bool
	err := tx.QueryRow(ctx, `
		SELECT (
			(kind = 'discussion' AND discussion_state = 'active')
			OR (kind = 'todo' AND todo_state <> 'done')
			OR (kind = 'ongoing' AND ongoing_state = 'active')
		)
		FROM cards
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, cardID).Scan(&runnable)
	if errors.Is(err, sqldb.ErrNoRows) {
		return false, nil
	}
	return runnable, err
}

func resolveTriggerCardCreatorSourceTx(ctx context.Context, tx *sqldb.Tx, trig WorkspaceTrigger) (uuid.UUID, error) {
	if trig.CreatedBySourceID.Valid {
		return trig.CreatedBySourceID.UUID, nil
	}
	var sourceID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT source_id FROM workspace_agent_settings WHERE workspace_id=$1
	`, trig.WorkspaceID).Scan(&sourceID)
	if err == nil {
		return sourceID, nil
	}
	if !errors.Is(err, sqldb.ErrNoRows) {
		return uuid.Nil, err
	}
	err = tx.QueryRow(ctx, `
		SELECT id FROM sources WHERE workspace_id=$1 ORDER BY created_at ASC LIMIT 1
	`, trig.WorkspaceID).Scan(&sourceID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve workspace source for triggered todo: %w", err)
	}
	return sourceID, nil
}

func buildTriggeredTodoTitle(triggerName string, event trigger.ParsedEvent) string {
	label := strings.TrimSpace(event.Title)
	if label == "" {
		label = strings.TrimSpace(event.Summary)
	}
	if label == "" {
		label = fmt.Sprintf("%s %s", event.EventType, event.Action)
	}
	prefix := fmt.Sprintf("[%s] ", strings.TrimSpace(triggerName))
	return truncateTriggerSummary(prefix+strings.TrimSpace(label), 120)
}

func truncateTriggerSummary(s string, maxRunes int) string {
	s, _ = agentdata.NormalizeText(strings.TrimSpace(s))
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "..."
}

type triggerQueryRow interface {
	QueryRow(ctx context.Context, sql string, args ...any) *sqldb.Row
}

func loadTriggerRow(ctx context.Context, q triggerQueryRow, query string, args ...any) (WorkspaceTrigger, error) {
	row := q.QueryRow(ctx, query, args...)
	item, err := scanTrigger(row)
	if errors.Is(err, sqldb.ErrNoRows) {
		return WorkspaceTrigger{}, domain.ErrNotFound
	}
	return item, err
}

type scannableRow interface {
	Scan(dest ...any) error
}

func scanTrigger(row scannableRow) (WorkspaceTrigger, error) {
	var out WorkspaceTrigger
	var provider, authMode, routingMode, dispatchMode string
	var filterBytes []byte
	var lastTriggered *time.Time
	err := row.Scan(
		&out.ID, &out.WorkspaceID, &out.Name, &provider, &authMode, &out.SignatureHeader,
		&out.SecretCiphertext, &out.SecretHint, &out.Enabled, &routingMode,
		&out.TargetProjectID, &out.TargetCardID, &dispatchMode,
		&filterBytes, &out.CooldownSeconds, &lastTriggered,
		&out.CreatedBySourceID, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return WorkspaceTrigger{}, err
	}
	out.Provider = domain.TriggerProvider(provider)
	out.AuthMode = domain.TriggerAuthMode(authMode)
	out.RoutingMode = domain.TriggerRoutingMode(routingMode)
	out.DispatchMode = domain.DispatchMode(dispatchMode)
	if lastTriggered != nil {
		t := lastTriggered.UTC()
		out.LastTriggeredAt = &t
	}
	if len(filterBytes) > 0 {
		if err := json.Unmarshal(filterBytes, &out.FilterConfig); err != nil {
			return WorkspaceTrigger{}, fmt.Errorf("decode trigger filter_config: %w", err)
		}
	}
	return out, nil
}

func loadVerifiedDelivery(
	ctx context.Context,
	tx *sqldb.Tx,
	workspaceID, triggerID uuid.UUID,
	deliveryID string,
) (TriggerDelivery, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, workspace_id, trigger_id, delivery_id, provider,
		       event_type, event_action, signature_valid, status, reason,
		       match_keys, payload_summary, triggered_turn_id,
		       triggered_watcher_turn_ids, created_card_id, received_at
		FROM trigger_deliveries
		WHERE workspace_id=$1 AND trigger_id=$2 AND delivery_id=$3 AND signature_valid=true
	`, workspaceID, triggerID, deliveryID)
	return scanTriggerDelivery(row)
}

func scanTriggerDelivery(row scannableRow) (TriggerDelivery, error) {
	var out TriggerDelivery
	var provider, status string
	err := row.Scan(
		&out.ID, &out.WorkspaceID, &out.TriggerID, &out.DeliveryID, &provider,
		&out.EventType, &out.EventAction, &out.SignatureValid, &status, &out.Reason,
		&out.MatchKeys, &out.PayloadSummary, &out.TriggeredTurnID,
		&out.TriggeredWatcherTurnIDs, &out.CreatedCardID, &out.ReceivedAt,
	)
	if err != nil {
		return TriggerDelivery{}, err
	}
	out.Provider = domain.TriggerProvider(provider)
	out.Status = domain.TriggerDeliveryStatus(status)
	return out, nil
}
