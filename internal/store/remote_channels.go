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
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

// RemoteChannelConfig 保存非敏感通道配置（明文 JSONB）。
type RemoteChannelConfig struct {
	BaseURL         string `json:"base_url,omitempty"`
	Domain          string `json:"domain,omitempty"`
	WSURL           string `json:"ws_url,omitempty"`
	BotID           string `json:"bot_id,omitempty"`
	AppID           string `json:"app_id,omitempty"`
	ClientID        string `json:"client_id,omitempty"`
	TargetChatID    string `json:"target_chat_id,omitempty"`
	TargetChatType  string `json:"target_chat_type,omitempty"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
	ProxyURL        string `json:"proxy_url,omitempty"`
	WeixinUserID    string `json:"weixin_user_id,omitempty"`
}

// RemoteChannelSecrets 保存所有需要服务端 AES-256-GCM 加密的敏感凭据。
// 绝不直接序列化返回给前端，也绝不写入日志。
type RemoteChannelSecrets struct {
	BotToken      string `json:"bot_token,omitempty"`
	Secret        string `json:"secret,omitempty"`
	AppSecret     string `json:"app_secret,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
	WebhookURL    string `json:"webhook_url,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

// HasAny 返回是否已配置至少一项敏感凭据。
func (s RemoteChannelSecrets) HasAny() bool {
	return strings.TrimSpace(s.BotToken) != "" ||
		strings.TrimSpace(s.Secret) != "" ||
		strings.TrimSpace(s.AppSecret) != "" ||
		strings.TrimSpace(s.ClientSecret) != "" ||
		strings.TrimSpace(s.WebhookURL) != "" ||
		strings.TrimSpace(s.WebhookSecret) != ""
}

// Hints 返回前端可安全展示的脱敏掩码摘要。
func (s RemoteChannelSecrets) Hints() map[string]string {
	hints := make(map[string]string)
	if v := strings.TrimSpace(s.BotToken); v != "" {
		hints["bot_token"] = formatSecretHint(v)
	}
	if v := strings.TrimSpace(s.Secret); v != "" {
		hints["secret"] = formatSecretHint(v)
	}
	if v := strings.TrimSpace(s.AppSecret); v != "" {
		hints["app_secret"] = formatSecretHint(v)
	}
	if v := strings.TrimSpace(s.ClientSecret); v != "" {
		hints["client_secret"] = formatSecretHint(v)
	}
	if v := strings.TrimSpace(s.WebhookURL); v != "" {
		hints["webhook_url"] = maskWebhookURL(v)
	}
	if v := strings.TrimSpace(s.WebhookSecret); v != "" {
		hints["webhook_secret"] = formatSecretHint(v)
	}
	return hints
}

func maskWebhookURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return formatSecretHint(raw)
	}
	return parsed.Scheme + "://" + parsed.Host + "/.../" + formatSecretHint(raw)
}

// RemoteChannelRuntimeState 保存非敏感运行态游标与最近会话定位信息。
type RemoteChannelRuntimeState struct {
	WeixinUpdatesBuf    string            `json:"weixin_updates_buf,omitempty"`
	WeixinContextTokens map[string]string `json:"weixin_context_tokens,omitempty"`
	TelegramOffset      int64             `json:"telegram_offset,omitempty"`
	LastChatID          string            `json:"last_chat_id,omitempty"`
	LastChatType        string            `json:"last_chat_type,omitempty"`
	LastThreadID        string            `json:"last_thread_id,omitempty"`
	LastSessionWebhook  string            `json:"last_session_webhook,omitempty"`
	LastMessageID       string            `json:"last_message_id,omitempty"`
	LastMessageAt       int64             `json:"last_message_at,omitempty"`
}

// RemoteChannel 是持久化的远程通道实体。
type RemoteChannel struct {
	ID                 uuid.UUID
	WorkspaceID        uuid.UUID
	Name               string
	Platform           domain.ChannelPlatform
	Role               domain.ChannelRole
	Transport          domain.ChannelTransport
	Enabled            bool
	ProjectID          uuid.UUID
	TargetCardID       uuid.NullUUID
	ConversationCardID uuid.NullUUID
	PushFilter         domain.ChannelPushFilter
	MirrorWebMessages  bool
	GroupPolicy        domain.ChannelGroupPolicy
	AllowFrom          []string
	Config             RemoteChannelConfig
	SecretCiphertext   []byte
	SecretHints        map[string]string
	RuntimeStatus      domain.ChannelRuntimeStatus
	LastError          string
	LastHeartbeatAt    *time.Time
	LastInboundAt      *time.Time
	LastOutboundAt     *time.Time
	RuntimeState       RemoteChannelRuntimeState
	CreatedBySourceID  uuid.NullUUID
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// HasSecrets 返回该通道是否已在服务端加密保存凭据。
func (c RemoteChannel) HasSecrets() bool {
	return len(c.SecretCiphertext) > 0 && len(c.SecretHints) > 0
}

// HasOutboundTarget 判断通道是否已经具备可发送的目标会话（或 Webhook URL）。
// 双向通道刚启用但用户尚未发来首条消息且未手填 target_chat_id 时返回 false。
func (c RemoteChannel) HasOutboundTarget() bool {
	if c.Transport == domain.ChannelTransportWebhook {
		return true
	}
	if strings.TrimSpace(c.Config.TargetChatID) != "" {
		return true
	}
	if strings.TrimSpace(c.Config.WeixinUserID) != "" {
		return true
	}
	if strings.TrimSpace(c.RuntimeState.LastChatID) != "" {
		return true
	}
	if strings.TrimSpace(c.RuntimeState.LastSessionWebhook) != "" {
		return true
	}
	return false
}

// RemoteChannelInput 用于创建或更新远程通道。
type RemoteChannelInput struct {
	Name              string
	Platform          domain.ChannelPlatform
	Role              domain.ChannelRole
	Transport         domain.ChannelTransport
	Enabled           bool
	ProjectID         uuid.UUID
	TargetCardID      *uuid.UUID
	PushFilter        domain.ChannelPushFilter
	MirrorWebMessages bool
	GroupPolicy       domain.ChannelGroupPolicy
	AllowFrom         []string
	Config            RemoteChannelConfig
	Secrets           RemoteChannelSecrets
	ClearSecrets      bool
}

// ChannelInboundResult 描述一条入站 IM 消息的幂等处理结果。
type ChannelInboundResult struct {
	Reused  bool
	CardID  uuid.UUID
	EventID uuid.UUID
	TurnID  uuid.UUID
}

// RemoteChannelOutboundCursor 记录某通道在某卡片上的出站投递水位。
type RemoteChannelOutboundCursor struct {
	WorkspaceID       uuid.UUID
	ChannelID         uuid.UUID
	CardID            uuid.UUID
	LastMessageSeq    int64
	LastCommentSeq    int64
	LastCheckinSeq    int64
	LastStatusSeq     int64
	LastAttemptID     uuid.NullUUID
	LastAttemptStatus string
	LastApprovalID    uuid.NullUUID
	UpdatedAt         time.Time
}

// ConfigureChannelEncryption 配置远程通道敏感凭据的 AES-256-GCM 加密主密钥。
// 未单独配置时回退使用 Trigger / MCP / Model 加密密钥；全部缺失时拒绝加密保存，杜绝明文落库。
func (s *Store) ConfigureChannelEncryption(encodedKey string) error {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		s.channelCipher = nil
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
		return errors.New("CYCLEDOCK_CHANNEL_ENCRYPTION_KEY must encode exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create channel secret cipher: %w", err)
	}
	s.channelCipher, err = cipher.NewGCM(block)
	return err
}

func (s *Store) activeChannelCipher() cipher.AEAD {
	if s.channelCipher != nil {
		return s.channelCipher
	}
	if s.triggerCipher != nil {
		return s.triggerCipher
	}
	if s.mcpCipher != nil {
		return s.mcpCipher
	}
	return s.modelCipher
}

func (s *Store) encryptChannelSecrets(workspaceID, channelID uuid.UUID, secrets RemoteChannelSecrets) ([]byte, error) {
	if !secrets.HasAny() {
		return nil, nil
	}
	aead := s.activeChannelCipher()
	if aead == nil {
		return nil, errors.New("remote channel secret encryption key is not configured")
	}
	raw, err := json.Marshal(secrets)
	if err != nil {
		return nil, fmt.Errorf("encode remote channel secrets: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate remote channel secret nonce: %w", err)
	}
	aad := []byte("cycledock/remote_channel/" + workspaceID.String() + "/" + channelID.String())
	return aead.Seal(nonce, nonce, raw, aad), nil
}

// DecryptChannelSecrets 仅在服务端适配器建连/发送或更新合并时调用，绝不返回给前端。
func (s *Store) DecryptChannelSecrets(ch RemoteChannel) (RemoteChannelSecrets, error) {
	if len(ch.SecretCiphertext) == 0 {
		return RemoteChannelSecrets{}, nil
	}
	aead := s.activeChannelCipher()
	if aead == nil {
		return RemoteChannelSecrets{}, errors.New("remote channel secret encryption key is not configured")
	}
	nonceSize := aead.NonceSize()
	if len(ch.SecretCiphertext) < nonceSize+aead.Overhead() {
		return RemoteChannelSecrets{}, errors.New("invalid encrypted remote channel secret")
	}
	aad := []byte("cycledock/remote_channel/" + ch.WorkspaceID.String() + "/" + ch.ID.String())
	plain, err := aead.Open(nil, ch.SecretCiphertext[:nonceSize], ch.SecretCiphertext[nonceSize:], aad)
	if err != nil {
		return RemoteChannelSecrets{}, errors.New("remote channel secret decryption failed")
	}
	var out RemoteChannelSecrets
	if err := json.Unmarshal(plain, &out); err != nil {
		return RemoteChannelSecrets{}, fmt.Errorf("decode remote channel secrets: %w", err)
	}
	return out, nil
}

// DefaultTransportForChannel 根据平台与角色推导默认传输模式。
func DefaultTransportForChannel(platform domain.ChannelPlatform, role domain.ChannelRole, requested domain.ChannelTransport) (domain.ChannelTransport, error) {
	if requested == "" {
		switch platform {
		case domain.ChannelPlatformWeixin, domain.ChannelPlatformTelegram:
			return domain.ChannelTransportLongPoll, nil
		case domain.ChannelPlatformWeCom, domain.ChannelPlatformFeishu, domain.ChannelPlatformQQ:
			return domain.ChannelTransportWebSocket, nil
		case domain.ChannelPlatformDingTalk:
			return domain.ChannelTransportStream, nil
		default:
			return "", fmt.Errorf("%w: unsupported platform %q", domain.ErrConflict, platform)
		}
	}
	switch platform {
	case domain.ChannelPlatformWeixin, domain.ChannelPlatformTelegram:
		if requested != domain.ChannelTransportLongPoll {
			return "", fmt.Errorf("%w: platform %s requires transport=long_poll", domain.ErrConflict, platform)
		}
	case domain.ChannelPlatformWeCom, domain.ChannelPlatformFeishu:
		if requested != domain.ChannelTransportWebSocket && requested != domain.ChannelTransportWebhook {
			return "", fmt.Errorf("%w: platform %s supports websocket or webhook", domain.ErrConflict, platform)
		}
	case domain.ChannelPlatformDingTalk:
		if requested != domain.ChannelTransportStream && requested != domain.ChannelTransportWebhook {
			return "", fmt.Errorf("%w: platform dingtalk supports stream or webhook", domain.ErrConflict)
		}
	case domain.ChannelPlatformQQ:
		if requested != domain.ChannelTransportWebSocket {
			return "", fmt.Errorf("%w: platform qq requires transport=websocket", domain.ErrConflict)
		}
	default:
		return "", fmt.Errorf("%w: unsupported platform %q", domain.ErrConflict, platform)
	}
	if role == domain.ChannelRoleMainChat && requested == domain.ChannelTransportWebhook {
		return "", fmt.Errorf("%w: webhook transport is only allowed for scheduled_push", domain.ErrConflict)
	}
	return requested, nil
}

func normalizeRemoteChannelInput(input RemoteChannelInput) (RemoteChannelInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return input, fmt.Errorf("%w: channel name is required", domain.ErrConflict)
	}
	if len([]rune(input.Name)) > 120 {
		return input, fmt.Errorf("%w: channel name is too long", domain.ErrConflict)
	}
	if input.Role == "" {
		input.Role = domain.ChannelRoleMainChat
	}
	if input.Role != domain.ChannelRoleMainChat && input.Role != domain.ChannelRoleScheduledPush {
		return input, fmt.Errorf("%w: invalid channel role %q", domain.ErrConflict, input.Role)
	}
	transport, err := DefaultTransportForChannel(input.Platform, input.Role, input.Transport)
	if err != nil {
		return input, err
	}
	input.Transport = transport
	if input.ProjectID == uuid.Nil {
		return input, fmt.Errorf("%w: project_id is required", domain.ErrConflict)
	}
	if input.Role == domain.ChannelRoleMainChat && input.TargetCardID != nil && *input.TargetCardID != uuid.Nil {
		return input, fmt.Errorf("%w: target_card_id is only allowed for scheduled_push", domain.ErrConflict)
	}
	if input.PushFilter == "" {
		input.PushFilter = domain.ChannelPushFilterAll
	}
	if input.PushFilter != domain.ChannelPushFilterAll && input.PushFilter != domain.ChannelPushFilterWarningsOnly {
		return input, fmt.Errorf("%w: invalid push_filter %q", domain.ErrConflict, input.PushFilter)
	}
	if input.GroupPolicy == "" {
		input.GroupPolicy = domain.ChannelGroupPolicyMentionOnly
	}
	if input.GroupPolicy != domain.ChannelGroupPolicyMentionOnly &&
		input.GroupPolicy != domain.ChannelGroupPolicyAllowlist &&
		input.GroupPolicy != domain.ChannelGroupPolicyAll {
		return input, fmt.Errorf("%w: invalid group_policy %q", domain.ErrConflict, input.GroupPolicy)
	}
	// 清洗白名单：去空、去重，保持顺序。
	seen := make(map[string]struct{}, len(input.AllowFrom))
	cleanAllow := make([]string, 0, len(input.AllowFrom))
	for _, entry := range input.AllowFrom {
		v := strings.TrimSpace(entry)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		cleanAllow = append(cleanAllow, v)
	}
	input.AllowFrom = cleanAllow

	// 清洗非敏感配置字段
	input.Config.BaseURL = strings.TrimRight(strings.TrimSpace(input.Config.BaseURL), "/")
	input.Config.Domain = strings.ToLower(strings.TrimSpace(input.Config.Domain))
	if input.Platform == domain.ChannelPlatformFeishu && input.Config.Domain == "" {
		input.Config.Domain = "feishu"
	}
	if input.Platform == domain.ChannelPlatformFeishu && input.Config.Domain != "feishu" && input.Config.Domain != "lark" {
		return input, fmt.Errorf("%w: feishu domain must be feishu or lark", domain.ErrConflict)
	}
	input.Config.WSURL = strings.TrimSpace(input.Config.WSURL)
	input.Config.BotID = strings.TrimSpace(input.Config.BotID)
	input.Config.AppID = strings.TrimSpace(input.Config.AppID)
	input.Config.ClientID = strings.TrimSpace(input.Config.ClientID)
	input.Config.TargetChatID = strings.TrimSpace(input.Config.TargetChatID)
	input.Config.TargetChatType = strings.TrimSpace(input.Config.TargetChatType)
	input.Config.ProxyURL = strings.TrimSpace(input.Config.ProxyURL)
	input.Config.WeixinUserID = strings.TrimSpace(input.Config.WeixinUserID)
	if input.Config.ProxyURL != "" {
		parsed, err := url.Parse(input.Config.ProxyURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return input, fmt.Errorf("%w: invalid proxy_url", domain.ErrConflict)
		}
	}

	input.Secrets = RemoteChannelSecrets{
		BotToken:      strings.TrimSpace(input.Secrets.BotToken),
		Secret:        strings.TrimSpace(input.Secrets.Secret),
		AppSecret:     strings.TrimSpace(input.Secrets.AppSecret),
		ClientSecret:  strings.TrimSpace(input.Secrets.ClientSecret),
		WebhookURL:    strings.TrimSpace(input.Secrets.WebhookURL),
		WebhookSecret: strings.TrimSpace(input.Secrets.WebhookSecret),
	}
	if input.Secrets.WebhookURL != "" {
		parsed, err := url.Parse(input.Secrets.WebhookURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return input, fmt.Errorf("%w: invalid webhook_url", domain.ErrConflict)
		}
	}
	return input, nil
}

func mergeSecrets(existing, incoming RemoteChannelSecrets, clear bool) RemoteChannelSecrets {
	if clear {
		return incoming
	}
	out := existing
	if incoming.BotToken != "" {
		out.BotToken = incoming.BotToken
	}
	if incoming.Secret != "" {
		out.Secret = incoming.Secret
	}
	if incoming.AppSecret != "" {
		out.AppSecret = incoming.AppSecret
	}
	if incoming.ClientSecret != "" {
		out.ClientSecret = incoming.ClientSecret
	}
	if incoming.WebhookURL != "" {
		out.WebhookURL = incoming.WebhookURL
	}
	if incoming.WebhookSecret != "" {
		out.WebhookSecret = incoming.WebhookSecret
	}
	return out
}

func (s *Store) validateChannelTargetScope(ctx context.Context, workspaceID, projectID uuid.UUID, targetCardID *uuid.UUID) (uuid.NullUUID, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM projects WHERE workspace_id = $1 AND id = $2)
	`, workspaceID, projectID).Scan(&exists); err != nil {
		return uuid.NullUUID{}, err
	}
	if !exists {
		return uuid.NullUUID{}, domain.ErrNotFound
	}
	if targetCardID == nil || *targetCardID == uuid.Nil {
		return uuid.NullUUID{}, nil
	}
	var cardProjectID uuid.UUID
	var kind string
	err := s.pool.QueryRow(ctx, `
		SELECT project_id, kind
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, *targetCardID).Scan(&cardProjectID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.NullUUID{}, fmt.Errorf("%w: target ongoing card not found", domain.ErrNotFound)
	}
	if err != nil {
		return uuid.NullUUID{}, err
	}
	if cardProjectID != projectID {
		return uuid.NullUUID{}, fmt.Errorf("%w: target card does not belong to selected project", domain.ErrConflict)
	}
	if kind != string(domain.CardKindOngoing) {
		return uuid.NullUUID{}, fmt.Errorf("%w: scheduled_push target_card_id must be an ongoing card", domain.ErrConflict)
	}
	return uuid.NullUUID{UUID: *targetCardID, Valid: true}, nil
}

const remoteChannelSelectCols = `
	id, workspace_id, name, platform, role, transport, enabled,
	project_id, target_card_id, conversation_card_id, push_filter, mirror_web_messages,
	group_policy, allow_from, config_json, secret_ciphertext, secret_hints,
	runtime_status, last_error, last_heartbeat_at, last_inbound_at, last_outbound_at,
	runtime_state_json, created_by_source_id, created_at, updated_at
`

func scanRemoteChannel(row interface {
	Scan(dest ...any) error
}) (RemoteChannel, error) {
	var ch RemoteChannel
	var platform, role, transport, pushFilter, groupPolicy, runtimeStatus string
	var configBytes, hintsBytes, runtimeStateBytes []byte
	if err := row.Scan(
		&ch.ID, &ch.WorkspaceID, &ch.Name, &platform, &role, &transport, &ch.Enabled,
		&ch.ProjectID, &ch.TargetCardID, &ch.ConversationCardID, &pushFilter, &ch.MirrorWebMessages,
		&groupPolicy, &ch.AllowFrom, &configBytes, &ch.SecretCiphertext, &hintsBytes,
		&runtimeStatus, &ch.LastError, &ch.LastHeartbeatAt, &ch.LastInboundAt, &ch.LastOutboundAt,
		&runtimeStateBytes, &ch.CreatedBySourceID, &ch.CreatedAt, &ch.UpdatedAt,
	); err != nil {
		return RemoteChannel{}, err
	}
	ch.Platform = domain.ChannelPlatform(platform)
	ch.Role = domain.ChannelRole(role)
	ch.Transport = domain.ChannelTransport(transport)
	ch.PushFilter = domain.ChannelPushFilter(pushFilter)
	ch.GroupPolicy = domain.ChannelGroupPolicy(groupPolicy)
	ch.RuntimeStatus = domain.ChannelRuntimeStatus(runtimeStatus)
	if ch.AllowFrom == nil {
		ch.AllowFrom = []string{}
	}
	if len(configBytes) > 0 {
		_ = json.Unmarshal(configBytes, &ch.Config)
	}
	ch.SecretHints = map[string]string{}
	if len(hintsBytes) > 0 {
		_ = json.Unmarshal(hintsBytes, &ch.SecretHints)
	}
	if len(runtimeStateBytes) > 0 {
		_ = json.Unmarshal(runtimeStateBytes, &ch.RuntimeState)
	}
	return ch, nil
}

// CreateRemoteChannel 创建一条远程通道配置；敏感凭据强制加密落库。
func (s *Store) CreateRemoteChannel(
	ctx context.Context,
	workspaceID, createdBySourceID uuid.UUID,
	input RemoteChannelInput,
) (RemoteChannel, error) {
	normalized, err := normalizeRemoteChannelInput(input)
	if err != nil {
		return RemoteChannel{}, err
	}
	targetCard, err := s.validateChannelTargetScope(ctx, workspaceID, normalized.ProjectID, normalized.TargetCardID)
	if err != nil {
		return RemoteChannel{}, err
	}
	channelID := uuid.New()
	ciphertext, err := s.encryptChannelSecrets(workspaceID, channelID, normalized.Secrets)
	if err != nil {
		return RemoteChannel{}, err
	}
	configBytes, err := json.Marshal(normalized.Config)
	if err != nil {
		return RemoteChannel{}, err
	}
	hintsBytes, err := json.Marshal(normalized.Secrets.Hints())
	if err != nil {
		return RemoteChannel{}, err
	}
	var creator uuid.NullUUID
	if createdBySourceID != uuid.Nil {
		creator = uuid.NullUUID{UUID: createdBySourceID, Valid: true}
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO remote_channels (
			id, workspace_id, name, platform, role, transport, enabled,
			project_id, target_card_id, push_filter, mirror_web_messages,
			group_policy, allow_from, config_json, secret_ciphertext, secret_hints,
			created_by_source_id
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING `+remoteChannelSelectCols,
		channelID, workspaceID, normalized.Name, string(normalized.Platform), string(normalized.Role),
		string(normalized.Transport), normalized.Enabled, normalized.ProjectID, targetCard,
		string(normalized.PushFilter), normalized.MirrorWebMessages, string(normalized.GroupPolicy),
		normalized.AllowFrom, configBytes, ciphertext, hintsBytes, creator,
	)
	return scanRemoteChannel(row)
}

// ListRemoteChannels 列出工作区下全部远程通道。
func (s *Store) ListRemoteChannels(ctx context.Context, workspaceID uuid.UUID) ([]RemoteChannel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+remoteChannelSelectCols+`
		FROM remote_channels
		WHERE workspace_id = $1
		ORDER BY created_at DESC, id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RemoteChannel, 0)
	for rows.Next() {
		ch, err := scanRemoteChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// ListAllEnabledRemoteChannels 列出所有工作区已启用的远程通道，供后台 ChannelManager 启动与出站投递。
func (s *Store) ListAllEnabledRemoteChannels(ctx context.Context) ([]RemoteChannel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+remoteChannelSelectCols+`
		FROM remote_channels
		WHERE enabled = true
		ORDER BY workspace_id, created_at, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RemoteChannel, 0)
	for rows.Next() {
		ch, err := scanRemoteChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// GetRemoteChannel 按工作区和 ID 获取单个远程通道。
func (s *Store) GetRemoteChannel(ctx context.Context, workspaceID, channelID uuid.UUID) (RemoteChannel, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+remoteChannelSelectCols+`
		FROM remote_channels
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, channelID)
	ch, err := scanRemoteChannel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RemoteChannel{}, domain.ErrNotFound
	}
	return ch, err
}

// UpdateRemoteChannel 更新远程通道配置；未传新密钥字段时保留已有加密凭据。
func (s *Store) UpdateRemoteChannel(
	ctx context.Context,
	workspaceID, channelID uuid.UUID,
	input RemoteChannelInput,
) (RemoteChannel, error) {
	existing, err := s.GetRemoteChannel(ctx, workspaceID, channelID)
	if err != nil {
		return RemoteChannel{}, err
	}
	var oldEffectiveID uuid.NullUUID
	if oldRoute, routeErr := s.GetProjectPrimaryPushRoute(ctx, workspaceID, existing.ProjectID); routeErr == nil {
		oldEffectiveID = oldRoute.EffectiveChannelID
	}
	if input.Platform == "" {
		input.Platform = existing.Platform
	}
	if input.Platform != existing.Platform {
		return RemoteChannel{}, fmt.Errorf("%w: channel platform cannot be changed", domain.ErrConflict)
	}
	normalized, err := normalizeRemoteChannelInput(input)
	if err != nil {
		return RemoteChannel{}, err
	}
	targetCard, err := s.validateChannelTargetScope(ctx, workspaceID, normalized.ProjectID, normalized.TargetCardID)
	if err != nil {
		return RemoteChannel{}, err
	}

	var existingSecrets RemoteChannelSecrets
	if len(existing.SecretCiphertext) > 0 && !normalized.ClearSecrets {
		existingSecrets, err = s.DecryptChannelSecrets(existing)
		if err != nil {
			return RemoteChannel{}, err
		}
	}
	mergedSecrets := mergeSecrets(existingSecrets, normalized.Secrets, normalized.ClearSecrets)
	ciphertext, err := s.encryptChannelSecrets(workspaceID, channelID, mergedSecrets)
	if err != nil {
		return RemoteChannel{}, err
	}
	configBytes, err := json.Marshal(normalized.Config)
	if err != nil {
		return RemoteChannel{}, err
	}
	hintsBytes, err := json.Marshal(mergedSecrets.Hints())
	if err != nil {
		return RemoteChannel{}, err
	}

	row := s.pool.QueryRow(ctx, `
		UPDATE remote_channels
		SET name = $3,
		    role = $4,
		    transport = $5,
		    enabled = $6,
		    project_id = $7,
		    target_card_id = $8,
		    conversation_card_id = CASE WHEN project_id <> $7 OR $4 <> 'main_chat' THEN NULL ELSE conversation_card_id END,
		    push_filter = $9,
		    mirror_web_messages = $10,
		    group_policy = $11,
		    allow_from = $12,
		    config_json = $13,
		    secret_ciphertext = $14,
		    secret_hints = $15,
		    updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING `+remoteChannelSelectCols,
		workspaceID, channelID, normalized.Name, string(normalized.Role), string(normalized.Transport),
		normalized.Enabled, normalized.ProjectID, targetCard, string(normalized.PushFilter),
		normalized.MirrorWebMessages, string(normalized.GroupPolicy), normalized.AllowFrom,
		configBytes, ciphertext, hintsBytes,
	)
	updated, err := scanRemoteChannel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RemoteChannel{}, domain.ErrNotFound
	}
	if err != nil {
		return RemoteChannel{}, err
	}
	// 若通道切换为 scheduled_push 或迁移到其他项目，立即解除原项目对其的显式主推引用，
	// 保留原项目 primary_push_mode='channel' 使其安全静默，绝不退化为全量广播。
	if updated.Role != domain.ChannelRoleMainChat || updated.ProjectID != existing.ProjectID {
		_, _ = s.pool.Exec(ctx, `
			UPDATE projects
			SET primary_push_channel_id = NULL,
			    updated_at = now()
			WHERE workspace_id = $1 AND primary_push_channel_id = $2
		`, workspaceID, channelID)
	}
	s.alignEffectivePrimaryAfterChannelMutation(ctx, workspaceID, existing.ProjectID, oldEffectiveID)
	// 通道改名时同步已绑定专属讨论卡的标题，保持左侧讨论列表与通道名称一致。
	if updated.ConversationCardID.Valid {
		syncTitle := strings.TrimSpace(updated.Name)
		if syncTitle == "" {
			syncTitle = defaultPlatformTitle(updated.Platform)
		}
		syncDesc := defaultChannelConversationDescription(updated.Platform, syncTitle)
		if _, syncErr := s.pool.Exec(ctx, `
			UPDATE cards
			SET title = $3,
			    description = $4,
			    updated_at = now()
			WHERE workspace_id = $1 AND id = $2
		`, workspaceID, updated.ConversationCardID.UUID, syncTitle, syncDesc); syncErr == nil {
			s.notifyWorkspaceChanged(workspaceID)
		}
	}
	return updated, nil
}

// SetRemoteChannelEnabled 启停远程通道。
func (s *Store) SetRemoteChannelEnabled(ctx context.Context, workspaceID, channelID uuid.UUID, enabled bool) (RemoteChannel, error) {
	existing, err := s.GetRemoteChannel(ctx, workspaceID, channelID)
	if err != nil {
		return RemoteChannel{}, err
	}
	var oldEffectiveID uuid.NullUUID
	if oldRoute, routeErr := s.GetProjectPrimaryPushRoute(ctx, workspaceID, existing.ProjectID); routeErr == nil {
		oldEffectiveID = oldRoute.EffectiveChannelID
	}
	statusSQL := "runtime_status"
	if !enabled {
		statusSQL = "'disconnected'"
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE remote_channels
		SET enabled = $3,
		    runtime_status = `+statusSQL+`,
		    last_error = CASE WHEN $3 THEN last_error ELSE '' END,
		    updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING `+remoteChannelSelectCols,
		workspaceID, channelID, enabled,
	)
	ch, err := scanRemoteChannel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RemoteChannel{}, domain.ErrNotFound
	}
	if err == nil {
		s.alignEffectivePrimaryAfterChannelMutation(ctx, workspaceID, ch.ProjectID, oldEffectiveID)
	}
	return ch, err
}

// UpdateRemoteChannelSecretsAndConfig 原子更新通道凭据与非敏感配置（供微信扫码确认后落库）。
func (s *Store) UpdateRemoteChannelSecretsAndConfig(
	ctx context.Context,
	workspaceID, channelID uuid.UUID,
	secrets RemoteChannelSecrets,
	mutateConfig func(*RemoteChannelConfig),
) (RemoteChannel, error) {
	existing, err := s.GetRemoteChannel(ctx, workspaceID, channelID)
	if err != nil {
		return RemoteChannel{}, err
	}
	var currentSecrets RemoteChannelSecrets
	if len(existing.SecretCiphertext) > 0 {
		currentSecrets, err = s.DecryptChannelSecrets(existing)
		if err != nil {
			return RemoteChannel{}, err
		}
	}
	merged := mergeSecrets(currentSecrets, secrets, false)
	ciphertext, err := s.encryptChannelSecrets(workspaceID, channelID, merged)
	if err != nil {
		return RemoteChannel{}, err
	}
	cfg := existing.Config
	if mutateConfig != nil {
		mutateConfig(&cfg)
	}
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return RemoteChannel{}, err
	}
	hintsBytes, err := json.Marshal(merged.Hints())
	if err != nil {
		return RemoteChannel{}, err
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE remote_channels
		SET config_json = $3,
		    secret_ciphertext = $4,
		    secret_hints = $5,
		    last_error = '',
		    updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING `+remoteChannelSelectCols,
		workspaceID, channelID, configBytes, ciphertext, hintsBytes,
	)
	return scanRemoteChannel(row)
}

// DeleteRemoteChannel 删除远程通道。
func (s *Store) DeleteRemoteChannel(ctx context.Context, workspaceID, channelID uuid.UUID) error {
	existing, err := s.GetRemoteChannel(ctx, workspaceID, channelID)
	if err != nil {
		return err
	}
	var oldEffectiveID uuid.NullUUID
	if oldRoute, routeErr := s.GetProjectPrimaryPushRoute(ctx, workspaceID, existing.ProjectID); routeErr == nil {
		oldEffectiveID = oldRoute.EffectiveChannelID
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM remote_channels
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, channelID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	s.alignEffectivePrimaryAfterChannelMutation(ctx, workspaceID, existing.ProjectID, oldEffectiveID)
	return nil
}

// UpdateRemoteChannelRuntime 上报适配器实时运行态、心跳与最近错误。
func (s *Store) UpdateRemoteChannelRuntime(
	ctx context.Context,
	workspaceID, channelID uuid.UUID,
	status domain.ChannelRuntimeStatus,
	lastError string,
	heartbeat bool,
) error {
	lastError = strings.TrimSpace(lastError)
	if len([]rune(lastError)) > 600 {
		lastError = string([]rune(lastError)[:600])
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE remote_channels
		SET runtime_status = $3,
		    last_error = $4,
		    last_heartbeat_at = CASE WHEN $5 THEN now() ELSE last_heartbeat_at END
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, channelID, string(status), lastError, heartbeat)
	return err
}

// UpdateRemoteChannelRuntimeState 持久化通道非敏感运行态游标（如 get_updates_buf / telegram_offset / 最近会话目标）。
func (s *Store) UpdateRemoteChannelRuntimeState(
	ctx context.Context,
	workspaceID, channelID uuid.UUID,
	state RemoteChannelRuntimeState,
) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE remote_channels
		SET runtime_state_json = $3
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, channelID, raw)
	return err
}

// TouchRemoteChannelOutbound 更新最近出站投递时间与状态。
func (s *Store) TouchRemoteChannelOutbound(ctx context.Context, workspaceID, channelID uuid.UUID, lastError string) error {
	lastError = strings.TrimSpace(lastError)
	if len([]rune(lastError)) > 600 {
		lastError = string([]rune(lastError)[:600])
	}
	if lastError == "" {
		_, err := s.pool.Exec(ctx, `
			UPDATE remote_channels
			SET last_outbound_at = now(),
			    last_error = CASE WHEN transport = 'webhook' THEN '' ELSE last_error END,
			    runtime_status = CASE WHEN transport = 'webhook' THEN 'connected' ELSE runtime_status END
			WHERE workspace_id = $1 AND id = $2
		`, workspaceID, channelID)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE remote_channels
		SET last_error = $3,
		    runtime_status = CASE WHEN transport = 'webhook' THEN 'error' ELSE runtime_status END
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, channelID, lastError)
	return err
}

// EnsureChannelConversationCard 为双向远程通道懒创建并绑定专属普通讨论卡（绝不复用项目主聊天）。
// 若当前已绑定的讨论卡仍存在、归属同一项目且处于 active 状态则直接复用；
// 若已被删除或归档，则在事务行锁保护下原子创建一张新普通讨论卡并回写 remote_channels.conversation_card_id。
func (s *Store) EnsureChannelConversationCard(
	ctx context.Context,
	workspaceID, channelID, humanSourceID uuid.UUID,
) (db.Card, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Card{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		chName, chPlatform, chRole string
		projectID                  uuid.UUID
		convCardID                 uuid.NullUUID
	)
	err = tx.QueryRow(ctx, `
		SELECT name, platform, role, project_id, conversation_card_id
		FROM remote_channels
		WHERE workspace_id = $1 AND id = $2
		FOR UPDATE
	`, workspaceID, channelID).Scan(&chName, &chPlatform, &chRole, &projectID, &convCardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, false, domain.ErrNotFound
	}
	if err != nil {
		return db.Card{}, false, err
	}
	if chRole != string(domain.ChannelRoleMainChat) {
		return db.Card{}, false, fmt.Errorf("%w: only main_chat channels own a conversation card", domain.ErrConflict)
	}

	if convCardID.Valid {
		existing, ok, err := loadUsableChannelConversationCardTx(ctx, tx, workspaceID, projectID, convCardID.UUID)
		if err != nil {
			return db.Card{}, false, err
		}
		if ok {
			if err := tx.Commit(ctx); err != nil {
				return db.Card{}, false, err
			}
			return existing, false, nil
		}
	}

	title := strings.TrimSpace(chName)
	if title == "" {
		title = defaultPlatformTitle(domain.ChannelPlatform(chPlatform))
	}
	desc := defaultChannelConversationDescription(domain.ChannelPlatform(chPlatform), title)
	q := db.New(tx)
	card, err := q.CreateCard(ctx, db.CreateCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		Kind:                  string(domain.CardKindDiscussion),
		Title:                 title,
		Description:           desc,
		DiscussionState:       pgtype.Text{String: string(domain.DiscussionStateActive), Valid: true},
		RequiredConfirmations: 1,
		CreatedBySourceID:     humanSourceID,
	})
	if err != nil {
		return db.Card{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE remote_channels
		SET conversation_card_id = $3
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, channelID, card.ID); err != nil {
		return db.Card{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Card{}, false, err
	}
	s.notifyWorkspaceChanged(workspaceID)
	return card, true, nil
}

func loadUsableChannelConversationCardTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, projectID, cardID uuid.UUID,
) (db.Card, bool, error) {
	q := db.New(tx)
	card, err := q.GetCard(ctx, db.GetCardParams{
		WorkspaceID: workspaceID,
		ID:          cardID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, false, nil
	}
	if err != nil {
		return db.Card{}, false, err
	}
	if card.ProjectID != projectID || card.Kind != string(domain.CardKindDiscussion) {
		return db.Card{}, false, nil
	}
	if !card.DiscussionState.Valid || card.DiscussionState.String != string(domain.DiscussionStateActive) {
		return db.Card{}, false, nil
	}
	// 兜底防止误绑定到项目主聊天。
	var mainForProject uuid.NullUUID
	if err := tx.QueryRow(ctx, `
		SELECT main_discussion_id
		FROM projects
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, projectID).Scan(&mainForProject); err != nil {
		return db.Card{}, false, err
	}
	if mainForProject.Valid && mainForProject.UUID == card.ID {
		return db.Card{}, false, nil
	}
	return card, true, nil
}

func defaultPlatformTitle(platform domain.ChannelPlatform) string {
	switch platform {
	case domain.ChannelPlatformWeixin:
		return "微信"
	case domain.ChannelPlatformWeCom:
		return "企业微信"
	case domain.ChannelPlatformFeishu:
		return "飞书"
	case domain.ChannelPlatformDingTalk:
		return "钉钉"
	case domain.ChannelPlatformQQ:
		return "QQ"
	case domain.ChannelPlatformTelegram:
		return "Telegram"
	default:
		return "远程通道"
	}
}

// defaultChannelConversationDescription 生成通道专属讨论卡的系统托管描述，
// 同时向 Agent 明确该平台最适合的排版风格，减少外部 IM 出现乱码符号或复杂表格。
func defaultChannelConversationDescription(platform domain.ChannelPlatform, title string) string {
	var styleHint string
	switch platform {
	case domain.ChannelPlatformWeixin, domain.ChannelPlatformQQ:
		styleHint = "回复格式要求：该渠道仅展示纯文本，请直接用自然流畅的纯文本分段作答，不要使用 Markdown 标题、表格、代码块围栏或加粗星号，链接直接写纯 URL 或自然文字。"
	case domain.ChannelPlatformTelegram:
		styleHint = "回复格式要求：支持简洁 Markdown（加粗、斜体、行内代码、代码块与链接），避免宽表格，保持消息精炼易读。"
	case domain.ChannelPlatformFeishu:
		styleHint = "回复格式要求：消息会以飞书卡片 Markdown 展示，可适度使用加粗、列表、代码块与链接，保持段落清晰。"
	case domain.ChannelPlatformDingTalk, domain.ChannelPlatformWeCom:
		styleHint = "回复格式要求：支持轻量 Markdown（标题、加粗、列表、行内代码与链接），避免复杂表格和深层嵌套语法。"
	default:
		styleHint = "回复格式要求：请保持表达简洁清晰，避免复杂排版。"
	}
	return fmt.Sprintf(
		"远程通道「%s」的专属对话讨论卡（由系统托管，标题与描述随通道配置自动同步）。通过该通道发送的消息与子任务回调均在此卡片中独立沉淀，不会混入项目主聊天上下文。\n\n%s",
		title,
		styleHint,
	)
}

// RemoteChannelConversationBinding 描述某张讨论卡被远程通道托管的绑定元信息。
type RemoteChannelConversationBinding struct {
	ChannelID   uuid.UUID
	ChannelName string
	Platform    domain.ChannelPlatform
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func isRemoteChannelConversationCard(
	ctx context.Context,
	q queryRower,
	workspaceID, cardID uuid.UUID,
) (bool, error) {
	var exists bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM remote_channels
			WHERE workspace_id = $1 AND conversation_card_id = $2
		)
	`, workspaceID, cardID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// GetRemoteChannelForConversationCard 查询某张卡片是否正作为某个远程通道的专属讨论卡被系统托管。
func (s *Store) GetRemoteChannelForConversationCard(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
) (RemoteChannelConversationBinding, bool, error) {
	var b RemoteChannelConversationBinding
	var platformStr string
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, platform
		FROM remote_channels
		WHERE workspace_id = $1 AND conversation_card_id = $2
		LIMIT 1
	`, workspaceID, cardID).Scan(&b.ChannelID, &b.ChannelName, &platformStr)
	if errors.Is(err, pgx.ErrNoRows) {
		return RemoteChannelConversationBinding{}, false, nil
	}
	if err != nil {
		return RemoteChannelConversationBinding{}, false, err
	}
	b.Platform = domain.ChannelPlatform(platformStr)
	return b, true, nil
}

// ListRemoteChannelConversationBindings 批量返回工作区内全部“专属讨论卡 -> 通道绑定”映射，供列表接口 O(1) 装配。
func (s *Store) ListRemoteChannelConversationBindings(
	ctx context.Context,
	workspaceID uuid.UUID,
) (map[uuid.UUID]RemoteChannelConversationBinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT conversation_card_id, id, name, platform
		FROM remote_channels
		WHERE workspace_id = $1 AND conversation_card_id IS NOT NULL
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]RemoteChannelConversationBinding)
	for rows.Next() {
		var cardID uuid.UUID
		var b RemoteChannelConversationBinding
		var platformStr string
		if err := rows.Scan(&cardID, &b.ChannelID, &b.ChannelName, &platformStr); err != nil {
			return nil, err
		}
		b.Platform = domain.ChannelPlatform(platformStr)
		out[cardID] = b
	}
	return out, rows.Err()
}

// HasIngestedRemoteChannelMessage 只读查询外部消息是否已入库，供通道层在拉取大体积远端附件前提前短路去重。
func (s *Store) HasIngestedRemoteChannelMessage(
	ctx context.Context,
	workspaceID, channelID uuid.UUID,
	externalMessageID string,
) (ChannelInboundResult, bool, error) {
	externalMessageID = strings.TrimSpace(externalMessageID)
	if externalMessageID == "" {
		return ChannelInboundResult{}, false, nil
	}
	var existing CardEventTurnRef
	err := s.pool.QueryRow(ctx, `
		SELECT card_id, event_id, turn_id
		FROM remote_channel_inbound_dedup
		WHERE workspace_id = $1 AND channel_id = $2 AND external_message_id = $3
	`, workspaceID, channelID, externalMessageID).Scan(&existing.CardID, &existing.EventID, &existing.TurnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChannelInboundResult{}, false, nil
	}
	if err != nil {
		return ChannelInboundResult{}, false, err
	}
	return ChannelInboundResult{
		Reused:  true,
		CardID:  existing.CardID.UUID,
		EventID: existing.EventID.UUID,
		TurnID:  existing.TurnID.UUID,
	}, true, nil
}

// ResolveChannelHumanSource 返回通道入站消息与附件归属的人类 Source ID（优先使用通道创建者）。
func (s *Store) ResolveChannelHumanSource(
	ctx context.Context,
	workspaceID uuid.UUID,
	preferred uuid.NullUUID,
) (uuid.UUID, error) {
	return s.resolveChannelHumanSource(ctx, workspaceID, preferred)
}

// IngestRemoteChannelMessage 将已通过白名单与群策略校验的外部 IM 纯文本消息幂等写入该通道专属普通讨论卡。
func (s *Store) IngestRemoteChannelMessage(
	ctx context.Context,
	channel RemoteChannel,
	externalMessageID, senderID, chatID, content string,
) (ChannelInboundResult, error) {
	return s.IngestRemoteChannelMessageWithAttachments(ctx, channel, externalMessageID, senderID, chatID, content, nil)
}

// IngestRemoteChannelMessageWithAttachments 将已通过白名单与群策略校验的外部 IM 消息（含可选图片/文本附件）
// 幂等写入该通道专属普通讨论卡。
// 严格遵守：
// 1. 仅 role=main_chat 允许入站建 Turn；scheduled_push 绝不接收入站。
// 2. 通过 remote_channel_inbound_dedup 唯一索引防重连重复建 Turn。
// 3. 自动懒创建/复用通道专属普通讨论卡（绝不污染项目主聊天 main_discussion_id）；若专属卡已删或归档则自动重建。
// 4. 复用 SubmitSessionComment 写入 human comment（透传 AttachmentIDs，支持纯附件触发 Turn）+ human Turn。
func (s *Store) IngestRemoteChannelMessageWithAttachments(
	ctx context.Context,
	channel RemoteChannel,
	externalMessageID, senderID, chatID, content string,
	attachmentIDs []uuid.UUID,
) (ChannelInboundResult, error) {
	if channel.Role != domain.ChannelRoleMainChat {
		return ChannelInboundResult{}, fmt.Errorf("%w: only main_chat channels accept inbound messages", domain.ErrConflict)
	}
	externalMessageID = strings.TrimSpace(externalMessageID)
	content = strings.TrimSpace(content)
	if externalMessageID == "" || (content == "" && len(attachmentIDs) == 0) {
		return ChannelInboundResult{}, fmt.Errorf("%w: external_message_id and content or attachments are required", domain.ErrConflict)
	}

	// 第一步：原子占位去重记录，防止并发重连重复写入。
	dedupID := uuid.New()
	var insertedID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO remote_channel_inbound_dedup (
			id, workspace_id, channel_id, external_message_id, sender_id, chat_id
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id, channel_id, external_message_id) DO NOTHING
		RETURNING id
	`, dedupID, channel.WorkspaceID, channel.ID, externalMessageID, strings.TrimSpace(senderID), strings.TrimSpace(chatID)).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		// 已经处理过同一 external_message_id，查询已有映射直接幂等返回。
		var existing CardEventTurnRef
		_ = s.pool.QueryRow(ctx, `
			SELECT card_id, event_id, turn_id
			FROM remote_channel_inbound_dedup
			WHERE workspace_id = $1 AND channel_id = $2 AND external_message_id = $3
		`, channel.WorkspaceID, channel.ID, externalMessageID).Scan(&existing.CardID, &existing.EventID, &existing.TurnID)
		return ChannelInboundResult{
			Reused:  true,
			CardID:  existing.CardID.UUID,
			EventID: existing.EventID.UUID,
			TurnID:  existing.TurnID.UUID,
		}, nil
	}
	if err != nil {
		return ChannelInboundResult{}, err
	}

	// 若后续步骤失败则回滚去重占位，允许下次重试，避免丢消息。
	committed := false
	defer func() {
		if !committed {
			_, _ = s.pool.Exec(ctx, `DELETE FROM remote_channel_inbound_dedup WHERE id = $1`, dedupID)
		}
	}()

	humanSourceID, err := s.resolveChannelHumanSource(ctx, channel.WorkspaceID, channel.CreatedBySourceID)
	if err != nil {
		return ChannelInboundResult{}, err
	}
	convCard, _, err := s.EnsureChannelConversationCard(ctx, channel.WorkspaceID, channel.ID, humanSourceID)
	if err != nil {
		return ChannelInboundResult{}, fmt.Errorf("ensure channel conversation card: %w", err)
	}

	idemKey := fmt.Sprintf("remote-channel:%s:%s", channel.ID.String(), externalMessageID)
	submitted, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    channel.WorkspaceID,
		CardID:         convCard.ID,
		AuthorSourceID: humanSourceID,
		Content:        content,
		AttachmentIDs:  attachmentIDs,
		IdempotencyKey: idemKey,
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		return ChannelInboundResult{}, fmt.Errorf("submit channel human turn: %w", err)
	}

	if _, err := s.pool.Exec(ctx, `
		UPDATE remote_channel_inbound_dedup
		SET card_id = $2, event_id = $3, turn_id = $4
		WHERE id = $1
	`, dedupID, convCard.ID, submitted.EventID, submitted.TurnID); err != nil {
		return ChannelInboundResult{}, err
	}
	// 在 last_active 模式下，若本通道即将因本次入站消息接管项目主推位置，
	// 先将其非专属卡片游标水位对齐至当前时刻，避免切换主推瞬间补发历史积压。
	s.alignCursorsOnLastActiveTakeover(
		ctx,
		channel.WorkspaceID,
		channel.ProjectID,
		channel.ID,
		uuid.NullUUID{UUID: convCard.ID, Valid: true},
	)
	_, _ = s.pool.Exec(ctx, `
		UPDATE remote_channels
		SET last_inbound_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, channel.WorkspaceID, channel.ID)
	committed = true

	return ChannelInboundResult{
		Reused:  submitted.Reused,
		CardID:  convCard.ID,
		EventID: submitted.EventID,
		TurnID:  submitted.TurnID,
	}, nil
}

type CardEventTurnRef struct {
	CardID  uuid.NullUUID
	EventID uuid.NullUUID
	TurnID  uuid.NullUUID
}

func (s *Store) resolveChannelHumanSource(ctx context.Context, workspaceID uuid.UUID, preferred uuid.NullUUID) (uuid.UUID, error) {
	if preferred.Valid {
		var kind string
		err := s.pool.QueryRow(ctx, `
			SELECT kind FROM sources WHERE workspace_id = $1 AND id = $2
		`, workspaceID, preferred.UUID).Scan(&kind)
		if err == nil && kind == string(domain.SourceKindHuman) {
			return preferred.UUID, nil
		}
	}
	var sourceID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM sources
		WHERE workspace_id = $1 AND kind = 'human'
		ORDER BY created_at, id
		LIMIT 1
	`, workspaceID).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: workspace has no human source", domain.ErrConflict)
	}
	return sourceID, err
}

// EventOriginChannelID 查询某条 comment 事件是否源自某个远程通道（用于防回环）。
func (s *Store) EventOriginChannelID(ctx context.Context, workspaceID, eventID uuid.UUID) (uuid.UUID, bool, error) {
	var channelID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT channel_id
		FROM remote_channel_inbound_dedup
		WHERE workspace_id = $1 AND event_id = $2
		LIMIT 1
	`, workspaceID, eventID).Scan(&channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return channelID, true, nil
}

// AttemptTurnOrigin 描述某次 Attempt 对应的 Turn 来源及可选的入站远程通道 ID。
type AttemptTurnOrigin struct {
	Source          domain.TurnSource
	OriginChannelID uuid.UUID
	HasOrigin       bool
}

// AttemptOriginChannelID 查询某次 Attempt 对应的 Turn 是否源自远程通道。
func (s *Store) AttemptOriginChannelID(ctx context.Context, workspaceID, attemptID uuid.UUID) (uuid.UUID, bool, error) {
	info, ok, err := s.AttemptTurnOrigin(ctx, workspaceID, attemptID)
	if err != nil || !ok || !info.HasOrigin {
		return uuid.Nil, false, err
	}
	return info.OriginChannelID, true, nil
}

// AttemptTurnOrigin 查询某次 Attempt 对应的 Turn 来源（human/schedule/system/trigger）
// 及可选的入站远程通道 ID，供主聊天通道判断是否需要推送终态回复。
func (s *Store) AttemptTurnOrigin(ctx context.Context, workspaceID, attemptID uuid.UUID) (AttemptTurnOrigin, bool, error) {
	var source string
	var channelID uuid.NullUUID
	err := s.pool.QueryRow(ctx, `
		SELECT t.source, d.channel_id
		FROM turn_attempts a
		JOIN card_turns t
		  ON t.workspace_id = a.workspace_id AND t.id = a.turn_id
		LEFT JOIN remote_channel_inbound_dedup d
		  ON d.workspace_id = a.workspace_id AND d.turn_id = a.turn_id
		WHERE a.workspace_id = $1 AND a.id = $2
		LIMIT 1
	`, workspaceID, attemptID).Scan(&source, &channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttemptTurnOrigin{}, false, nil
	}
	if err != nil {
		return AttemptTurnOrigin{}, false, err
	}
	return AttemptTurnOrigin{
		Source:          domain.TurnSource(source),
		OriginChannelID: channelID.UUID,
		HasOrigin:       channelID.Valid,
	}, true, nil
}

// GetChannelOutboundCursor 读取某通道在某卡片上的出站投递水位。
func (s *Store) GetChannelOutboundCursor(
	ctx context.Context,
	workspaceID, channelID, cardID uuid.UUID,
) (RemoteChannelOutboundCursor, bool, error) {
	var c RemoteChannelOutboundCursor
	err := s.pool.QueryRow(ctx, `
		SELECT workspace_id, channel_id, card_id,
		       last_message_seq, last_comment_seq, last_checkin_seq, last_status_seq,
		       last_attempt_id, last_attempt_status, last_approval_id, updated_at
		FROM remote_channel_outbound_cursors
		WHERE workspace_id = $1 AND channel_id = $2 AND card_id = $3
	`, workspaceID, channelID, cardID).Scan(
		&c.WorkspaceID, &c.ChannelID, &c.CardID,
		&c.LastMessageSeq, &c.LastCommentSeq, &c.LastCheckinSeq, &c.LastStatusSeq,
		&c.LastAttemptID, &c.LastAttemptStatus, &c.LastApprovalID, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RemoteChannelOutboundCursor{}, false, nil
	}
	if err != nil {
		return RemoteChannelOutboundCursor{}, false, err
	}
	return c, true, nil
}

// UpsertChannelOutboundCursor 保存通道在某卡片上的最新出站投递水位。
func (s *Store) UpsertChannelOutboundCursor(ctx context.Context, c RemoteChannelOutboundCursor) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO remote_channel_outbound_cursors (
			workspace_id, channel_id, card_id,
			last_message_seq, last_comment_seq, last_checkin_seq, last_status_seq,
			last_attempt_id, last_attempt_status, last_approval_id, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now())
		ON CONFLICT (workspace_id, channel_id, card_id)
		DO UPDATE SET
			last_message_seq = EXCLUDED.last_message_seq,
			last_comment_seq = EXCLUDED.last_comment_seq,
			last_checkin_seq = EXCLUDED.last_checkin_seq,
			last_status_seq = EXCLUDED.last_status_seq,
			last_attempt_id = EXCLUDED.last_attempt_id,
			last_attempt_status = EXCLUDED.last_attempt_status,
			last_approval_id = EXCLUDED.last_approval_id,
			updated_at = now()
	`, c.WorkspaceID, c.ChannelID, c.CardID,
		c.LastMessageSeq, c.LastCommentSeq, c.LastCheckinSeq, c.LastStatusSeq,
		c.LastAttemptID, c.LastAttemptStatus, c.LastApprovalID)
	return err
}
