package channels

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

// 外部扫码绑定端点，抽取为包级变量以便测试替身替换，避免硬编码测试分支。
var (
	wecomQRBaseURL       = "https://work.weixin.qq.com"
	dingtalkRegBaseURL   = "https://oapi.dingtalk.com"
	feishuAccountsURL    = "https://accounts.feishu.cn"
	larkAccountsURL      = "https://accounts.larksuite.com"
	qqPortalBaseURL      = "https://q.qq.com"
	qqConnectPageURLTmpl = "https://q.qq.com/qqbot/openclaw/connect.html?task_id=%s&_wv=2"
)

const (
	qrSessionDefaultTTL = 10 * time.Minute
	qrSessionPruneAge   = 20 * time.Minute
	qrDefaultBrowserUA  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	qrQQUserAgent       = "Mozilla/5.0 (QQBot/1.0)"
)

// QRConnectStartInput 描述发起一次扫码连接所需的最小上下文。
// 传入 ChannelID 表示对已有通道重新扫码换绑；不传则按 Platform+ProjectID 在确认后自动新建通道。
type QRConnectStartInput struct {
	WorkspaceID uuid.UUID
	ChannelID   uuid.UUID
	Platform    domain.ChannelPlatform
	ProjectID   uuid.UUID
	Role        domain.ChannelRole
	Name        string
	Domain      string
	BaseURL     string
}

// QRConnectSession 表示一次进行中的五平台扫码绑定会话。
// 严禁将 Secret/BotToken 等敏感凭据直接返回给前端（json:"-"）。
type QRConnectSession struct {
	SessionID     string                 `json:"session_id"`
	WorkspaceID   uuid.UUID              `json:"-"`
	ChannelID     uuid.UUID              `json:"channel_id,omitempty"`
	Platform      domain.ChannelPlatform `json:"platform"`
	ProjectID     uuid.UUID              `json:"project_id,omitempty"`
	Role          domain.ChannelRole     `json:"role,omitempty"`
	Name          string                 `json:"name,omitempty"`
	Domain        string                 `json:"domain,omitempty"`
	BaseURL       string                 `json:"base_url,omitempty"`
	QRCodeContent string                 `json:"qrcode_content"`
	Status        string                 `json:"status"` // wait / scanned / confirmed / expired / error / cancelled
	IntervalMs    int                    `json:"interval_ms"`
	Message       string                 `json:"message,omitempty"`
	ExpiresAt     time.Time              `json:"expires_at"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`

	// 仅服务端内部状态与解密后暂存凭据，绝不序列化到响应体。
	Committed       bool   `json:"-"`
	WeixinSessionID string `json:"-"`
	SCode           string `json:"-"`
	DeviceCode      string `json:"-"`
	TaskID          string `json:"-"`
	AESKeyBase64    string `json:"-"`

	BotID        string `json:"bot_id,omitempty"`
	AppID        string `json:"app_id,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	WeixinUserID string `json:"weixin_user_id,omitempty"`
	OpenID       string `json:"-"`

	BotToken     string `json:"-"`
	Secret       string `json:"-"`
	AppSecret    string `json:"-"`
	ClientSecret string `json:"-"`
}

// QRConnectStore 管理五平台（微信、企业微信、钉钉、飞书/Lark、QQ）扫码绑定会话。
type QRConnectStore struct {
	mu         sync.Mutex
	sessions   map[string]*QRConnectSession
	httpClient *http.Client
	weixinAuth *WeixinLoginStore
}

func NewQRConnectStore(client *http.Client, weixinAuth *WeixinLoginStore) *QRConnectStore {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if weixinAuth == nil {
		weixinAuth = NewWeixinLoginStore(nil, defaultWeixinBaseURL)
	}
	return &QRConnectStore{
		sessions:   make(map[string]*QRConnectSession),
		httpClient: client,
		weixinAuth: weixinAuth,
	}
}

func (s *QRConnectStore) WeixinStore() *WeixinLoginStore {
	return s.weixinAuth
}

// Start 为指定平台发起一次扫码绑定会话。
func (s *QRConnectStore) Start(ctx context.Context, input QRConnectStartInput) (*QRConnectSession, error) {
	if input.WorkspaceID == uuid.Nil {
		return nil, fmt.Errorf("%w: workspace_id is required", domain.ErrConflict)
	}
	role := input.Role
	if role == "" {
		role = domain.ChannelRoleMainChat
	}
	if role != domain.ChannelRoleMainChat && role != domain.ChannelRoleScheduledPush {
		return nil, fmt.Errorf("%w: invalid channel role %q", domain.ErrConflict, role)
	}
	now := time.Now().UTC()
	sess := &QRConnectSession{
		SessionID:   uuid.NewString(),
		WorkspaceID: input.WorkspaceID,
		ChannelID:   input.ChannelID,
		Platform:    input.Platform,
		ProjectID:   input.ProjectID,
		Role:        role,
		Name:        strings.TrimSpace(input.Name),
		Domain:      normalizeFeishuDomain(input.Domain),
		BaseURL:     strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"),
		Status:      "wait",
		IntervalMs:  2000,
		ExpiresAt:   now.Add(qrSessionDefaultTTL),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	switch input.Platform {
	case domain.ChannelPlatformWeixin:
		wxSess, err := s.weixinAuth.Start(ctx, input.WorkspaceID, input.ChannelID, sess.BaseURL)
		if err != nil {
			return nil, err
		}
		sess.WeixinSessionID = wxSess.SessionID
		sess.QRCodeContent = wxSess.QRCodeContent
		sess.BaseURL = wxSess.BaseURL
		sess.ExpiresAt = wxSess.ExpiresAt
		sess.Message = wxSess.Message

	case domain.ChannelPlatformWeCom:
		scode, authURL, err := s.startWeCom(ctx)
		if err != nil {
			return nil, err
		}
		sess.SCode = scode
		sess.QRCodeContent = authURL
		sess.Message = "请使用企业微信扫描二维码确认创建机器人"

	case domain.ChannelPlatformDingTalk:
		deviceCode, qrURL, intervalSec, expiresInSec, err := s.startDingTalk(ctx)
		if err != nil {
			return nil, err
		}
		sess.DeviceCode = deviceCode
		sess.QRCodeContent = qrURL
		sess.IntervalMs = maxInt(2000, intervalSec*1000)
		if expiresInSec > 0 {
			sess.ExpiresAt = now.Add(time.Duration(expiresInSec) * time.Second)
		}
		sess.Message = "请使用钉钉扫描二维码并授权创建机器人"

	case domain.ChannelPlatformFeishu:
		deviceCode, qrURL, intervalSec, expiresInSec, err := s.startFeishu(ctx, sess.Domain)
		if err != nil {
			return nil, err
		}
		sess.DeviceCode = deviceCode
		sess.QRCodeContent = qrURL
		sess.IntervalMs = maxInt(2000, intervalSec*1000)
		if expiresInSec > 0 {
			sess.ExpiresAt = now.Add(time.Duration(expiresInSec) * time.Second)
		}
		if sess.Domain == "lark" {
			sess.Message = "请使用 Lark 扫描二维码并确认授权"
		} else {
			sess.Message = "请使用飞书扫描二维码并确认授权"
		}

	case domain.ChannelPlatformQQ:
		taskID, aesKey, qrURL, err := s.startQQ(ctx)
		if err != nil {
			return nil, err
		}
		sess.TaskID = taskID
		sess.AESKeyBase64 = aesKey
		sess.QRCodeContent = qrURL
		sess.Message = "请使用手机 QQ 扫描二维码绑定机器人"

	default:
		return nil, fmt.Errorf("%w: platform %q does not support QR connect", domain.ErrConflict, input.Platform)
	}

	s.mu.Lock()
	s.pruneLocked(now)
	s.sessions[sess.SessionID] = sess
	copySess := *sess
	s.mu.Unlock()
	return &copySess, nil
}

// Poll 推进一次对应平台的扫码状态轮询。
func (s *QRConnectStore) Poll(ctx context.Context, workspaceID uuid.UUID, sessionID, verifyCode string) (*QRConnectSession, error) {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if !ok || sess.WorkspaceID != workspaceID {
		s.mu.Unlock()
		return nil, domain.ErrNotFound
	}
	if sess.Status == "confirmed" || sess.Status == "expired" || sess.Status == "cancelled" || sess.Status == "error" {
		copySess := *sess
		s.mu.Unlock()
		return &copySess, nil
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		sess.Status = "expired"
		sess.Message = "二维码已过期，请重新生成"
		sess.UpdatedAt = time.Now().UTC()
		copySess := *sess
		s.mu.Unlock()
		return &copySess, nil
	}
	snapshot := *sess
	s.mu.Unlock()

	switch snapshot.Platform {
	case domain.ChannelPlatformWeixin:
		wxSess, err := s.weixinAuth.Poll(ctx, workspaceID, snapshot.WeixinSessionID, verifyCode)
		if err != nil {
			return nil, err
		}
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = wxSess.Status
			cur.Message = wxSess.Message
			cur.BaseURL = wxSess.BaseURL
			cur.BotID = wxSess.BotID
			cur.WeixinUserID = wxSess.WeixinUserID
			cur.BotToken = wxSess.BotToken
			cur.UpdatedAt = time.Now().UTC()
		})

	case domain.ChannelPlatformWeCom:
		return s.pollWeCom(ctx, sessionID, snapshot.SCode)

	case domain.ChannelPlatformDingTalk:
		return s.pollDingTalk(ctx, sessionID, snapshot.DeviceCode)

	case domain.ChannelPlatformFeishu:
		return s.pollFeishu(ctx, sessionID, snapshot.Domain, snapshot.DeviceCode)

	case domain.ChannelPlatformQQ:
		return s.pollQQ(ctx, sessionID, snapshot.TaskID, snapshot.AESKeyBase64)

	default:
		return nil, fmt.Errorf("%w: unsupported platform %q", domain.ErrConflict, snapshot.Platform)
	}
}

// MarkCommitted 在 HTTP 层成功落库后原子标记会话已提交，防止前端重复轮询多次创建通道。
func (s *QRConnectStore) MarkCommitted(sessionID string, channelID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[sessionID]; ok {
		sess.Committed = true
		if channelID != uuid.Nil {
			sess.ChannelID = channelID
		}
		sess.UpdatedAt = time.Now().UTC()
	}
}

// Cancel 取消并清理扫码会话。
func (s *QRConnectStore) Cancel(workspaceID uuid.UUID, sessionID string) (*QRConnectSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok || sess.WorkspaceID != workspaceID {
		return nil, domain.ErrNotFound
	}
	if sess.Status != "confirmed" {
		sess.Status = "cancelled"
		sess.Message = "已取消扫码绑定"
		sess.UpdatedAt = time.Now().UTC()
	}
	copySess := *sess
	delete(s.sessions, sessionID)
	return &copySess, nil
}

func (s *QRConnectStore) updateSession(sessionID string, fn func(*QRConnectSession)) (*QRConnectSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if sess.Status == "cancelled" {
		copySess := *sess
		return &copySess, nil
	}
	fn(sess)
	copySess := *sess
	return &copySess, nil
}

func (s *QRConnectStore) pruneLocked(now time.Time) {
	for id, sess := range s.sessions {
		if now.Sub(sess.UpdatedAt) > qrSessionPruneAge {
			delete(s.sessions, id)
		}
	}
}

// --- 企业微信 AI Bot 一键扫码 ---

func (s *QRConnectStore) startWeCom(ctx context.Context) (string, string, error) {
	endpoint := strings.TrimRight(wecomQRBaseURL, "/") + "/ai/qc/generate"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	// 企业微信官网接口会对缺失或异常 User-Agent 返回空响应，需携带常规浏览器 UA。
	req.Header.Set("User-Agent", qrDefaultBrowserUA)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("请求企业微信二维码失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("读取企业微信二维码响应失败: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("企业微信二维码 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", "", errors.New("企业微信二维码接口返回空响应")
	}
	var payload struct {
		Data struct {
			SCode   string `json:"scode"`
			AuthURL string `json:"auth_url"`
		} `json:"data"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", fmt.Errorf("解析企业微信二维码响应失败: %w", err)
	}
	if payload.ErrCode != 0 {
		return "", "", fmt.Errorf("企业微信二维码返回错误 (%d): %s", payload.ErrCode, payload.ErrMsg)
	}
	scode := strings.TrimSpace(payload.Data.SCode)
	authURL := strings.TrimSpace(payload.Data.AuthURL)
	if scode == "" || authURL == "" {
		return "", "", errors.New("企业微信二维码接口未返回有效的 scode 或 auth_url")
	}
	return scode, authURL, nil
}

func (s *QRConnectStore) pollWeCom(ctx context.Context, sessionID, scode string) (*QRConnectSession, error) {
	endpoint := strings.TrimRight(wecomQRBaseURL, "/") + "/ai/qc/query_result?scode=" + url.QueryEscape(scode)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", qrDefaultBrowserUA)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		// 瞬时网络抖动保持等待重试，不立即判死。
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "wait"
			cur.Message = "等待企业微信扫码确认"
			cur.UpdatedAt = time.Now().UTC()
		})
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 || len(bytes.TrimSpace(raw)) == 0 {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "wait"
			cur.Message = "等待企业微信扫码确认"
			cur.UpdatedAt = time.Now().UTC()
		})
	}
	var payload struct {
		Data struct {
			Status  string `json:"status"`
			BotInfo struct {
				BotID  string `json:"botid"`
				Secret string `json:"secret"`
			} `json:"bot_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "wait"
			cur.Message = "等待企业微信扫码确认"
			cur.UpdatedAt = time.Now().UTC()
		})
	}
	return s.updateSession(sessionID, func(cur *QRConnectSession) {
		cur.UpdatedAt = time.Now().UTC()
		switch strings.ToLower(strings.TrimSpace(payload.Data.Status)) {
		case "success":
			botID := strings.TrimSpace(payload.Data.BotInfo.BotID)
			secret := strings.TrimSpace(payload.Data.BotInfo.Secret)
			if botID == "" || secret == "" {
				cur.Status = "error"
				cur.Message = "企业微信确认成功但未返回完整的 BotID 或 Secret"
				return
			}
			cur.Status = "confirmed"
			cur.BotID = botID
			cur.Secret = secret
			cur.Message = "企业微信扫码绑定成功，已自动加密保存并连接"
		case "scaned", "scanned":
			cur.Status = "scanned"
			cur.Message = "已扫码，请在企业微信客户端确认授权"
		case "expired":
			cur.Status = "expired"
			cur.Message = "企业微信二维码已过期，请重新生成"
		case "fail", "failed":
			cur.Status = "error"
			cur.Message = "企业微信扫码授权失败或被拒绝"
		default:
			cur.Status = "wait"
			cur.Message = "请使用企业微信扫描二维码确认创建机器人"
		}
	})
}

// --- 钉钉官方设备码注册 ---

func (s *QRConnectStore) startDingTalk(ctx context.Context) (deviceCode, qrURL string, intervalSec, expiresInSec int, err error) {
	base := strings.TrimRight(dingtalkRegBaseURL, "/")
	var initResp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Nonce   string `json:"nonce"`
	}
	if err := s.postJSON(ctx, base+"/app/registration/init", map[string]string{
		"source": "DING_DWS_CLAW",
	}, &initResp); err != nil {
		return "", "", 0, 0, fmt.Errorf("钉钉初始化注册失败: %w", err)
	}
	if initResp.ErrCode != 0 || strings.TrimSpace(initResp.Nonce) == "" {
		return "", "", 0, 0, fmt.Errorf("钉钉初始化注册失败 (%d): %s", initResp.ErrCode, initResp.ErrMsg)
	}

	var beginResp struct {
		ErrCode                 int    `json:"errcode"`
		ErrMsg                  string `json:"errmsg"`
		DeviceCode              string `json:"device_code"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := s.postJSON(ctx, base+"/app/registration/begin", map[string]string{
		"nonce": strings.TrimSpace(initResp.Nonce),
	}, &beginResp); err != nil {
		return "", "", 0, 0, fmt.Errorf("钉钉获取设备码失败: %w", err)
	}
	if beginResp.ErrCode != 0 {
		return "", "", 0, 0, fmt.Errorf("钉钉获取设备码失败 (%d): %s", beginResp.ErrCode, beginResp.ErrMsg)
	}
	deviceCode = strings.TrimSpace(beginResp.DeviceCode)
	qrURL = strings.TrimSpace(beginResp.VerificationURIComplete)
	if deviceCode == "" || qrURL == "" {
		return "", "", 0, 0, errors.New("钉钉未返回有效的 device_code 或二维码链接")
	}
	return deviceCode, qrURL, beginResp.Interval, beginResp.ExpiresIn, nil
}

func (s *QRConnectStore) pollDingTalk(ctx context.Context, sessionID, deviceCode string) (*QRConnectSession, error) {
	base := strings.TrimRight(dingtalkRegBaseURL, "/")
	var pollResp struct {
		ErrCode      int    `json:"errcode"`
		ErrMsg       string `json:"errmsg"`
		Status       string `json:"status"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		FailReason   string `json:"fail_reason"`
	}
	if err := s.postJSON(ctx, base+"/app/registration/poll", map[string]string{
		"device_code": deviceCode,
	}, &pollResp); err != nil {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "wait"
			cur.Message = "等待钉钉扫码确认"
			cur.UpdatedAt = time.Now().UTC()
		})
	}
	if pollResp.ErrCode != 0 {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "error"
			cur.Message = fmt.Sprintf("钉钉授权异常 (%d): %s", pollResp.ErrCode, pollResp.ErrMsg)
			cur.UpdatedAt = time.Now().UTC()
		})
	}
	return s.updateSession(sessionID, func(cur *QRConnectSession) {
		cur.UpdatedAt = time.Now().UTC()
		switch strings.ToUpper(strings.TrimSpace(pollResp.Status)) {
		case "SUCCESS":
			cid := strings.TrimSpace(pollResp.ClientID)
			csec := strings.TrimSpace(pollResp.ClientSecret)
			if cid == "" || csec == "" {
				cur.Status = "error"
				cur.Message = "钉钉授权成功但未返回完整的 client_id 或 client_secret"
				return
			}
			cur.Status = "confirmed"
			cur.ClientID = cid
			cur.ClientSecret = csec
			cur.Message = "钉钉扫码绑定成功，已自动加密保存并连接"
		case "EXPIRED":
			cur.Status = "expired"
			cur.Message = "钉钉二维码已过期，请重新生成"
		case "FAIL":
			cur.Status = "error"
			reason := strings.TrimSpace(pollResp.FailReason)
			if reason == "" {
				reason = "钉钉授权失败或已拒绝"
			}
			cur.Message = reason
		default:
			cur.Status = "wait"
			cur.Message = "请使用钉钉扫描二维码并授权创建机器人"
		}
	})
}

// --- 飞书 / Lark 官方设备码注册 ---

func normalizeFeishuDomain(domain string) string {
	if strings.EqualFold(strings.TrimSpace(domain), "lark") {
		return "lark"
	}
	return "feishu"
}

func feishuAccountsBase(domain string) string {
	if normalizeFeishuDomain(domain) == "lark" {
		return strings.TrimRight(larkAccountsURL, "/")
	}
	return strings.TrimRight(feishuAccountsURL, "/")
}

func (s *QRConnectStore) postFeishuRegistration(ctx context.Context, domain string, form url.Values) (map[string]any, error) {
	endpoint := feishuAccountsBase(domain) + "/oauth/v1/app/registration"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	// 飞书官方在 authorization_pending 时会返回 HTTP 400 + JSON，必须优先解析 JSON body。
	var parsed map[string]any
	if jsonErr := json.Unmarshal(raw, &parsed); jsonErr == nil && parsed != nil {
		return parsed, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("feishu registration HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return map[string]any{}, nil
}

func (s *QRConnectStore) startFeishu(ctx context.Context, domain string) (deviceCode, qrURL string, intervalSec, expiresInSec int, err error) {
	initForm := url.Values{}
	initForm.Set("action", "init")
	initRes, err := s.postFeishuRegistration(ctx, domain, initForm)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("飞书初始化注册失败: %w", err)
	}
	supportsSecret := false
	if methodsRaw, ok := initRes["supported_auth_methods"].([]any); ok {
		for _, item := range methodsRaw {
			if strings.EqualFold(fmt.Sprint(item), "client_secret") {
				supportsSecret = true
				break
			}
		}
	}
	if !supportsSecret {
		return "", "", 0, 0, errors.New("飞书注册接口未声明支持 client_secret 授权方式")
	}

	beginForm := url.Values{}
	beginForm.Set("action", "begin")
	beginForm.Set("archetype", "PersonalAgent")
	beginForm.Set("auth_method", "client_secret")
	beginForm.Set("request_user_info", "open_id")
	beginRes, err := s.postFeishuRegistration(ctx, domain, beginForm)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("飞书获取设备码失败: %w", err)
	}
	deviceCode = strings.TrimSpace(stringField(beginRes, "device_code"))
	qrURL = strings.TrimSpace(stringField(beginRes, "verification_uri_complete"))
	if deviceCode == "" || qrURL == "" {
		return "", "", 0, 0, errors.New("飞书注册接口未返回有效的 device_code 或二维码链接")
	}
	// 与官方 PersonalAgent 一致：若为 /page/launcher 落地页则重写为 /page/cli。
	if parsed, parseErr := url.Parse(qrURL); parseErr == nil &&
		parsed.Scheme == "https" &&
		(parsed.Host == "open.feishu.cn" || parsed.Host == "open.larksuite.com") &&
		parsed.Path == "/page/launcher" {
		parsed.Path = "/page/cli"
		qrURL = parsed.String()
	}
	intervalSec = intField(beginRes, "interval", 5)
	expiresInSec = intField(beginRes, "expires_in", intField(beginRes, "expire_in", 600))
	return deviceCode, qrURL, intervalSec, expiresInSec, nil
}

func (s *QRConnectStore) pollFeishu(ctx context.Context, sessionID, domain, deviceCode string) (*QRConnectSession, error) {
	pollForm := url.Values{}
	pollForm.Set("action", "poll")
	pollForm.Set("device_code", deviceCode)
	pollForm.Set("tp", "ob_app")
	res, err := s.postFeishuRegistration(ctx, domain, pollForm)
	if err != nil {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "wait"
			cur.Message = "等待飞书/Lark 扫码确认"
			cur.UpdatedAt = time.Now().UTC()
		})
	}

	nextDomain := normalizeFeishuDomain(domain)
	var openID string
	if userInfo, ok := res["user_info"].(map[string]any); ok {
		if strings.EqualFold(stringField(userInfo, "tenant_brand"), "lark") {
			nextDomain = "lark"
		}
		openID = strings.TrimSpace(stringField(userInfo, "open_id"))
	}

	clientID := strings.TrimSpace(stringField(res, "client_id"))
	clientSecret := strings.TrimSpace(stringField(res, "client_secret"))
	if clientID != "" && clientSecret != "" {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "confirmed"
			cur.Domain = nextDomain
			cur.AppID = clientID
			cur.AppSecret = clientSecret
			cur.OpenID = openID
			cur.Message = "飞书扫码绑定成功，已自动加密保存并连接"
			cur.UpdatedAt = time.Now().UTC()
		})
	}

	errCode := strings.TrimSpace(stringField(res, "error"))
	if errCode == "expired_token" {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "expired"
			cur.Domain = nextDomain
			cur.Message = "飞书二维码已过期，请重新生成"
			cur.UpdatedAt = time.Now().UTC()
		})
	}
	if errCode == "access_denied" {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "error"
			cur.Domain = nextDomain
			cur.Message = "飞书扫码授权被拒绝"
			cur.UpdatedAt = time.Now().UTC()
		})
	}

	return s.updateSession(sessionID, func(cur *QRConnectSession) {
		cur.Domain = nextDomain
		cur.Status = "wait"
		cur.Message = "等待飞书/Lark 扫码确认"
		cur.UpdatedAt = time.Now().UTC()
	})
}

// --- QQ 机器人 q.qq.com 官方扫码绑定 ---

func (s *QRConnectStore) startQQ(ctx context.Context) (taskID, aesKeyBase64, qrURL string, err error) {
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		return "", "", "", err
	}
	aesKeyBase64 = base64.StdEncoding.EncodeToString(rawKey)

	data, err := s.postQQPortal(ctx, "/lite/create_bind_task", map[string]any{
		"key": aesKeyBase64,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("QQ 创建扫码任务失败: %w", err)
	}
	taskID = strings.TrimSpace(stringField(data, "task_id"))
	if taskID == "" {
		return "", "", "", errors.New("QQ 未返回有效的 task_id")
	}
	qrURL = fmt.Sprintf(qqConnectPageURLTmpl, url.QueryEscape(taskID))
	return taskID, aesKeyBase64, qrURL, nil
}

func (s *QRConnectStore) pollQQ(ctx context.Context, sessionID, taskID, aesKeyBase64 string) (*QRConnectSession, error) {
	data, err := s.postQQPortal(ctx, "/lite/poll_bind_result", map[string]any{
		"task_id": taskID,
	})
	if err != nil {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "wait"
			cur.Message = "等待手机 QQ 扫码确认"
			cur.UpdatedAt = time.Now().UTC()
		})
	}

	statusCode := intField(data, "status", 0)
	if statusCode == 3 {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "expired"
			cur.Message = "QQ 二维码已过期，请重新生成"
			cur.UpdatedAt = time.Now().UTC()
		})
	}
	if statusCode != 2 {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "wait"
			cur.Message = "等待手机 QQ 扫码确认"
			cur.UpdatedAt = time.Now().UTC()
		})
	}

	appID := strings.TrimSpace(stringField(data, "bot_appid"))
	encryptedSecret := strings.TrimSpace(stringField(data, "bot_encrypt_secret"))
	secret, decErr := decryptQQPortalSecret(encryptedSecret, aesKeyBase64)
	if decErr != nil {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "error"
			cur.Message = fmt.Sprintf("QQ 密钥解密失败: %v", decErr)
			cur.UpdatedAt = time.Now().UTC()
		})
	}
	if appID == "" || strings.TrimSpace(secret) == "" {
		return s.updateSession(sessionID, func(cur *QRConnectSession) {
			cur.Status = "error"
			cur.Message = "QQ 扫码确认成功但未返回完整的 AppID 或 Secret"
			cur.UpdatedAt = time.Now().UTC()
		})
	}

	return s.updateSession(sessionID, func(cur *QRConnectSession) {
		cur.Status = "confirmed"
		cur.AppID = appID
		cur.Secret = strings.TrimSpace(secret)
		cur.Message = "QQ 机器人扫码绑定成功，已自动加密保存并连接"
		cur.UpdatedAt = time.Now().UTC()
	})
}

func (s *QRConnectStore) postQQPortal(ctx context.Context, path string, body map[string]any) (map[string]any, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(qqPortalBaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// q.qq.com 在缺少浏览器风格 UA 时会返回 JS 反爬页面而非 JSON。
	req.Header.Set("User-Agent", qrQQUserAgent)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("QQ portal HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var payload struct {
		RetCode int            `json:"retcode"`
		Msg     string         `json:"msg"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload.RetCode != 0 {
		msg := strings.TrimSpace(payload.Msg)
		if msg == "" {
			msg = fmt.Sprintf("retcode=%d", payload.RetCode)
		}
		return nil, errors.New(msg)
	}
	if payload.Data == nil {
		return map[string]any{}, nil
	}
	return payload.Data, nil
}

// decryptQQPortalSecret 复刻 nanobot/channels/qq/connect.py 的 AES-256-GCM 解密：
// base64(encrypted) 前 12 字节为 nonce，后续为密文 + GCM tag，密钥为 base64(key)。
func decryptQQPortalSecret(encrypted, keyBase64 string) (string, error) {
	keyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyBase64))
	if err != nil {
		return "", fmt.Errorf("decode key: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(raw) <= 12 {
		return "", errors.New("ciphertext too short")
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := aead.Open(nil, raw[:12], raw[12:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *QRConnectStore) postJSON(ctx context.Context, endpoint string, body any, out any) error {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprint(v)
}

func intField(m map[string]any, key string, fallback int) int {
	v, ok := m[key]
	if !ok || v == nil {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		if parsed, err := n.Int64(); err == nil {
			return int(parsed)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return parsed
		}
	}
	return fallback
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
