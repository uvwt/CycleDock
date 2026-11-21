package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/channels"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestQRChannelValidation_Unit(t *testing.T) {
	if got := defaultQRChannelName(domain.ChannelPlatformWeCom, domain.ChannelRoleMainChat); got != "企业微信" {
		t.Fatalf("unexpected default name: %q", got)
	}
	if got := defaultQRChannelName(domain.ChannelPlatformFeishu, domain.ChannelRoleScheduledPush); got != "飞书 定时推送" {
		t.Fatalf("unexpected default scheduled name: %q", got)
	}

	// 验证 buildQRCredentials 映射五平台 secrets 与非敏感 config
	feishuSecrets, feishuMutate := buildQRCredentials(&channels.QRConnectSession{
		Platform:  domain.ChannelPlatformFeishu,
		Domain:    "lark",
		AppID:     "cli_123",
		AppSecret: "sec_456",
		OpenID:    "ou_789",
	})
	if feishuSecrets.AppSecret != "sec_456" {
		t.Fatalf("expected feishu app secret mapped, got %+v", feishuSecrets)
	}
	var fsCfg store.RemoteChannelConfig
	feishuMutate(&fsCfg)
	if fsCfg.AppID != "cli_123" || fsCfg.Domain != "lark" || fsCfg.TargetChatID != "ou_789" || fsCfg.TargetChatType != "direct" {
		t.Fatalf("unexpected feishu config: %+v", fsCfg)
	}

	qqSecrets, qqMutate := buildQRCredentials(&channels.QRConnectSession{
		Platform: domain.ChannelPlatformQQ,
		AppID:    "102030",
		Secret:   "qq-sec",
	})
	if qqSecrets.Secret != "qq-sec" {
		t.Fatalf("expected qq secret mapped, got %+v", qqSecrets)
	}
	var qqCfg store.RemoteChannelConfig
	qqMutate(&qqCfg)
	if qqCfg.AppID != "102030" {
		t.Fatalf("unexpected qq config: %+v", qqCfg)
	}
}

func TestStartChannelQRConnect_RejectsInvalidRequestsWithoutDB(t *testing.T) {
	api := &API{}
	wsID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remote-channels/qr/start", bytes.NewBufferString(`{"platform":"weixin"}`))
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, auth.Identity{WorkspaceID: wsID}))
	rec := httptest.NewRecorder()
	api.startChannelQRConnect(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "project_id") {
		t.Fatalf("expected 400 project_id required, got %d %s", rec.Code, rec.Body.String())
	}

	var errResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
}
