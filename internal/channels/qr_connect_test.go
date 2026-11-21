package channels

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestQRConnectStore_WeCom_StartPollScannedConfirmAndExpired(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ai/qc/generate":
			if !strings.Contains(r.Header.Get("User-Agent"), "Mozilla/5.0") {
				t.Fatalf("expected browser User-Agent, got %q", r.Header.Get("User-Agent"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errcode": 0,
				"data": map[string]any{
					"scode":    "wecom-scode-1",
					"auth_url": "https://work.weixin.qq.com/ai/auth?scode=wecom-scode-1",
				},
			})
		case "/ai/qc/query_result":
			if r.URL.Query().Get("scode") != "wecom-scode-1" {
				t.Fatalf("unexpected scode query: %s", r.URL.RawQuery)
			}
			n := hits.Add(1)
			switch n {
			case 1:
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": "wait"}})
			case 2:
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": "scaned"}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{
						"status": "success",
						"bot_info": map[string]any{
							"botid":  "wecom-bot-888",
							"secret": "wecom-secret-xyz",
						},
					},
				})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	orig := wecomQRBaseURL
	wecomQRBaseURL = srv.URL
	defer func() { wecomQRBaseURL = orig }()

	store := NewQRConnectStore(srv.Client(), nil)
	wsID := uuid.New()
	projID := uuid.New()
	sess, err := store.Start(context.Background(), QRConnectStartInput{
		WorkspaceID: wsID,
		Platform:    domain.ChannelPlatformWeCom,
		ProjectID:   projID,
	})
	if err != nil {
		t.Fatalf("start wecom qr: %v", err)
	}
	if sess.Status != "wait" || !strings.Contains(sess.QRCodeContent, "wecom-scode-1") {
		t.Fatalf("unexpected start session: %+v", sess)
	}

	s1, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s1.Status != "wait" {
		t.Fatalf("expected wait on poll 1, got %+v err=%v", s1, err)
	}
	s2, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s2.Status != "scanned" {
		t.Fatalf("expected scanned on poll 2, got %+v err=%v", s2, err)
	}
	s3, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s3.Status != "confirmed" {
		t.Fatalf("expected confirmed on poll 3, got %+v err=%v", s3, err)
	}
	if s3.BotID != "wecom-bot-888" || s3.Secret != "wecom-secret-xyz" {
		t.Fatalf("expected credentials on server session, got %+v", s3)
	}
	// 确认敏感凭据不会序列化返回给前端 JSON。
	encoded, _ := json.Marshal(s3)
	if strings.Contains(string(encoded), "wecom-secret-xyz") {
		t.Fatalf("secret leaked into JSON: %s", string(encoded))
	}
}

func TestQRConnectStore_DingTalk_FlowAndErrors(t *testing.T) {
	var pollStep atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/registration/init":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["source"] != "DING_DWS_CLAW" {
				t.Fatalf("unexpected init source: %+v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "nonce": "nonce-123"})
		case "/app/registration/begin":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["nonce"] != "nonce-123" {
				t.Fatalf("unexpected begin nonce: %+v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errcode":                   0,
				"device_code":               "dt-dev-1",
				"verification_uri_complete": "https://dingtalk.test/auth?code=dt-dev-1",
				"expires_in":                600,
				"interval":                  3,
			})
		case "/app/registration/poll":
			n := pollStep.Add(1)
			if n == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "status": "WAITING"})
				return
			}
			if n == 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"errcode":       0,
					"status":        "SUCCESS",
					"client_id":     "ding-app-key",
					"client_secret": "ding-app-secret",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "status": "EXPIRED"})
		}
	}))
	defer srv.Close()

	orig := dingtalkRegBaseURL
	dingtalkRegBaseURL = srv.URL
	defer func() { dingtalkRegBaseURL = orig }()

	store := NewQRConnectStore(srv.Client(), nil)
	wsID := uuid.New()
	projID := uuid.New()

	sess, err := store.Start(context.Background(), QRConnectStartInput{
		WorkspaceID: wsID,
		Platform:    domain.ChannelPlatformDingTalk,
		ProjectID:   projID,
	})
	if err != nil {
		t.Fatalf("start dingtalk qr: %v", err)
	}
	if sess.IntervalMs != 3000 || sess.QRCodeContent != "https://dingtalk.test/auth?code=dt-dev-1" {
		t.Fatalf("unexpected dingtalk session: %+v", sess)
	}

	s1, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s1.Status != "wait" {
		t.Fatalf("expected wait, got %+v err=%v", s1, err)
	}
	s2, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s2.Status != "confirmed" || s2.ClientID != "ding-app-key" || s2.ClientSecret != "ding-app-secret" {
		t.Fatalf("expected confirmed with credentials, got %+v err=%v", s2, err)
	}
	encoded, _ := json.Marshal(s2)
	if strings.Contains(string(encoded), "ding-app-secret") {
		t.Fatalf("dingtalk client_secret leaked into JSON: %s", string(encoded))
	}

	// 验证 EXPIRED 终态
	sess2, err := store.Start(context.Background(), QRConnectStartInput{
		WorkspaceID: wsID,
		Platform:    domain.ChannelPlatformDingTalk,
		ProjectID:   projID,
	})
	if err != nil {
		t.Fatalf("start dingtalk qr 2: %v", err)
	}
	sExp, err := store.Poll(context.Background(), wsID, sess2.SessionID, "")
	if err != nil || sExp.Status != "expired" {
		t.Fatalf("expected expired, got %+v err=%v", sExp, err)
	}
}

func TestQRConnectStore_FeishuAndLark_PendingHTTP400AndRewrite(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("action") {
		case "init":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"supported_auth_methods": []string{"client_secret"},
			})
		case "begin":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "fs-dev-1",
				"verification_uri_complete": "https://open.feishu.cn/page/launcher?user_code=ABC",
				"interval":                  4,
				"expires_in":                300,
			})
		case "poll":
			n := hits.Add(1)
			if n == 1 {
				// 官方 pending 时可能返回 HTTP 400 + JSON
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": "authorization_pending",
				})
				return
			}
			if n == 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"client_id":     "cli_feishu_999",
					"client_secret": "sec_feishu_999",
					"user_info": map[string]any{
						"open_id":      "ou_owner_1",
						"tenant_brand": "lark",
					},
				})
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "access_denied"})
		}
	}))
	defer srv.Close()

	origFS, origLark := feishuAccountsURL, larkAccountsURL
	feishuAccountsURL, larkAccountsURL = srv.URL, srv.URL
	defer func() {
		feishuAccountsURL = origFS
		larkAccountsURL = origLark
	}()

	store := NewQRConnectStore(srv.Client(), nil)
	wsID := uuid.New()
	projID := uuid.New()

	sess, err := store.Start(context.Background(), QRConnectStartInput{
		WorkspaceID: wsID,
		Platform:    domain.ChannelPlatformFeishu,
		ProjectID:   projID,
		Domain:      "feishu",
	})
	if err != nil {
		t.Fatalf("start feishu qr: %v", err)
	}
	// 验证 /page/launcher -> /page/cli 改写
	if sess.QRCodeContent != "https://open.feishu.cn/page/cli?user_code=ABC" {
		t.Fatalf("expected /page/cli rewrite, got %s", sess.QRCodeContent)
	}

	s1, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s1.Status != "wait" {
		t.Fatalf("expected wait on HTTP 400 authorization_pending, got %+v err=%v", s1, err)
	}
	s2, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s2.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %+v err=%v", s2, err)
	}
	if s2.AppID != "cli_feishu_999" || s2.AppSecret != "sec_feishu_999" || s2.OpenID != "ou_owner_1" || s2.Domain != "lark" {
		t.Fatalf("unexpected confirmed feishu session: %+v", s2)
	}
	encoded, _ := json.Marshal(s2)
	if strings.Contains(string(encoded), "sec_feishu_999") {
		t.Fatalf("feishu app_secret leaked into JSON: %s", string(encoded))
	}
}

func TestQRConnectStore_QQ_DecryptAndCancel(t *testing.T) {
	var capturedKey string
	var pollCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "QQBot") {
			t.Fatalf("expected QQBot User-Agent, got %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/lite/create_bind_task":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			capturedKey = body["key"]
			_ = json.NewEncoder(w).Encode(map[string]any{
				"retcode": 0,
				"data":    map[string]any{"task_id": "qq-task-42"},
			})
		case "/lite/poll_bind_result":
			if pollCount.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"retcode": 0,
					"data":    map[string]any{"status": 1},
				})
				return
			}
			// 用服务端返回的 32 字节 key + 12 字节随机 nonce 做 AES-GCM 加密，验证客户端解密逻辑。
			keyBytes, err := base64.StdEncoding.DecodeString(capturedKey)
			if err != nil {
				t.Fatalf("decode captured key: %v", err)
			}
			block, _ := aes.NewCipher(keyBytes)
			aead, _ := cipher.NewGCM(block)
			nonce := make([]byte, 12)
			_, _ = rand.Read(nonce)
			sealed := aead.Seal(nonce, nonce, []byte("qq-bot-decrypted-secret"), nil)

			_ = json.NewEncoder(w).Encode(map[string]any{
				"retcode": 0,
				"data": map[string]any{
					"status":             2,
					"bot_appid":          "10203040",
					"bot_encrypt_secret": base64.StdEncoding.EncodeToString(sealed),
				},
			})
		}
	}))
	defer srv.Close()

	origBase, origPage := qqPortalBaseURL, qqConnectPageURLTmpl
	qqPortalBaseURL = srv.URL
	qqConnectPageURLTmpl = srv.URL + "/qqbot/openclaw/connect.html?task_id=%s&_wv=2"
	defer func() {
		qqPortalBaseURL = origBase
		qqConnectPageURLTmpl = origPage
	}()

	store := NewQRConnectStore(srv.Client(), nil)
	wsID := uuid.New()
	projID := uuid.New()

	sess, err := store.Start(context.Background(), QRConnectStartInput{
		WorkspaceID: wsID,
		Platform:    domain.ChannelPlatformQQ,
		ProjectID:   projID,
	})
	if err != nil {
		t.Fatalf("start qq qr: %v", err)
	}
	if !strings.Contains(sess.QRCodeContent, "task_id=qq-task-42") {
		t.Fatalf("unexpected qq qr url: %s", sess.QRCodeContent)
	}

	s1, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s1.Status != "wait" {
		t.Fatalf("expected wait, got %+v err=%v", s1, err)
	}
	s2, err := store.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil || s2.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %+v err=%v", s2, err)
	}
	if s2.AppID != "10203040" || s2.Secret != "qq-bot-decrypted-secret" {
		t.Fatalf("expected decrypted qq credentials, got %+v", s2)
	}
	encoded, _ := json.Marshal(s2)
	if strings.Contains(string(encoded), "qq-bot-decrypted-secret") {
		t.Fatalf("qq secret leaked into JSON: %s", string(encoded))
	}

	// 验证取消会话
	sess3, err := store.Start(context.Background(), QRConnectStartInput{
		WorkspaceID: wsID,
		Platform:    domain.ChannelPlatformQQ,
		ProjectID:   projID,
	})
	if err != nil {
		t.Fatalf("start qq qr 2: %v", err)
	}
	cancelled, err := store.Cancel(wsID, sess3.SessionID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("expected cancelled, got %+v err=%v", cancelled, err)
	}
}
