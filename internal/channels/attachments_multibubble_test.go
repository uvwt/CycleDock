package channels

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func mustMakeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 32, G: 128, B: 224, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func encryptAES128ECBPKCS7(t *testing.T, plain, key []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes cipher: %v", err)
	}
	pad := aes.BlockSize - (len(plain) % aes.BlockSize)
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}
	return out
}

func encryptAES256CBCPKCS7(t *testing.T, plain, key []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes256 cipher: %v", err)
	}
	pad := aes.BlockSize - (len(plain) % aes.BlockSize)
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(out, padded)
	return out
}

func TestShouldDeliverChannelMessageBubble(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		isOwnConversation bool
		endTurn           bool
		msgType           string
		want              bool
	}{
		{
			name:              "own_conversation_allows_intermediate_bubble",
			isOwnConversation: true,
			endTurn:           false,
			msgType:           "text",
			want:              true,
		},
		{
			name:              "own_conversation_allows_final_bubble",
			isOwnConversation: true,
			endTurn:           true,
			msgType:           "text",
			want:              true,
		},
		{
			name:              "main_chat_mirror_filters_intermediate_bubble",
			isOwnConversation: false,
			endTurn:           false,
			msgType:           "text",
			want:              false,
		},
		{
			name:              "main_chat_mirror_allows_final_bubble",
			isOwnConversation: false,
			endTurn:           true,
			msgType:           "text",
			want:              true,
		},
		{
			name:              "main_chat_mirror_allows_choice_bubble",
			isOwnConversation: false,
			endTurn:           false,
			msgType:           "choice",
			want:              true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldDeliverChannelMessageBubble(tc.isOwnConversation, tc.endTurn, tc.msgType); got != tc.want {
				t.Fatalf("shouldDeliverChannelMessageBubble(%v, %v, %q)=%v, want %v",
					tc.isOwnConversation, tc.endTurn, tc.msgType, got, tc.want)
			}
		})
	}
}

func TestPolicyAllowsAttachmentOnlyInbound(t *testing.T) {
	t.Parallel()
	ch := store.RemoteChannel{
		Role:        domain.ChannelRoleMainChat,
		AllowFrom:   []string{"user-1"},
		GroupPolicy: domain.ChannelGroupPolicyMentionOnly,
	}
	if !PolicyAllows(ch, InboundMessage{
		SenderID: "user-1",
		ChatID:   "user-1",
		ChatType: "direct",
		Attachments: []InboundAttachment{
			{Kind: InboundAttachmentImage, DownloadURL: "https://example.com/a.png"},
		},
	}) {
		t.Fatal("expected image-only inbound message to pass PolicyAllows")
	}
	if !PolicyAllows(ch, InboundMessage{
		SenderID: "user-1",
		ChatID:   "user-1",
		ChatType: "direct",
		Attachments: []InboundAttachment{
			{Kind: InboundAttachmentAudio, Note: "时长 3 秒"},
		},
	}) {
		t.Fatal("expected voice-only inbound message to pass PolicyAllows")
	}
	if PolicyAllows(ch, InboundMessage{
		SenderID: "user-1",
		ChatID:   "user-1",
		ChatType: "direct",
	}) {
		t.Fatal("expected empty text + empty attachments to be rejected")
	}
}

func TestQQMultiBubbleMsgSeqAndPassiveQuotaFallback(t *testing.T) {
	t.Parallel()

	type capturedReq struct {
		Content string `json:"content"`
		MsgID   string `json:"msg_id"`
		MsgSeq  int    `json:"msg_seq"`
	}
	var (
		mu       sync.Mutex
		requests []capturedReq
		passiveN int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "qq-tok-1",
				"expires_in":   "7200",
			})
		case "/v2/users/openid-1/messages":
			raw, _ := io.ReadAll(r.Body)
			var req capturedReq
			_ = json.Unmarshal(raw, &req)
			mu.Lock()
			requests = append(requests, req)
			if req.MsgID != "" {
				passiveN++
				if passiveN > 5 {
					mu.Unlock()
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"code":40054005,"message":"passive reply limit reached"}`))
					return
				}
			}
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	now := time.Now().UTC()
	ch := store.RemoteChannel{
		Platform: domain.ChannelPlatformQQ,
		Config: store.RemoteChannelConfig{
			BaseURL:      srv.URL,
			AppID:        "102999",
			TargetChatID: "openid-1",
		},
		RuntimeState: store.RemoteChannelRuntimeState{
			LastChatID:    "openid-1",
			LastChatType:  "direct",
			LastMessageID: "passive-msg-1",
			LastMessageAt: now.Unix(),
		},
	}
	adapter := NewQQAdapter(ch, store.RemoteChannelSecrets{Secret: "sec"}, srv.Client())
	stateCleared := false
	adapter.mu.Lock()
	adapter.saveState = func(_ context.Context, mutate func(*store.RemoteChannelRuntimeState)) error {
		st := store.RemoteChannelRuntimeState{
			LastMessageID: "passive-msg-1",
			LastMessageAt: now.Unix(),
		}
		mutate(&st)
		if st.LastMessageID == "" && st.LastMessageAt == 0 {
			stateCleared = true
		}
		return nil
	}
	adapter.mu.Unlock()

	for i := 1; i <= 7; i++ {
		if err := adapter.Send(context.Background(), OutboundMessage{
			ChatID:   "openid-1",
			ChatType: "direct",
			Text:     "连续气泡 #" + string(rune('0'+i)),
		}); err != nil {
			t.Fatalf("bubble %d send failed: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	// 前 5 条被动成功，第 6 条先尝试被动失败再自动降级主动成功（2 次请求），第 7 条直接走主动（1 次请求） => 共 8 次请求
	if len(requests) != 8 {
		t.Fatalf("expected 8 requests (5 passive + 1 failed passive + 2 proactive), got %d: %+v", len(requests), requests)
	}
	prevSeq := 0
	for i := 0; i < 6; i++ {
		if requests[i].MsgID != "passive-msg-1" {
			t.Fatalf("request %d expected passive msg_id, got %+v", i, requests[i])
		}
		if requests[i].MsgSeq <= prevSeq {
			t.Fatalf("expected strictly increasing msg_seq at %d: prev=%d curr=%d", i, prevSeq, requests[i].MsgSeq)
		}
		prevSeq = requests[i].MsgSeq
	}
	if requests[6].MsgID != "" || requests[7].MsgID != "" {
		t.Fatalf("expected proactive fallback without msg_id for requests 6 & 7, got %+v %+v", requests[6], requests[7])
	}
	if !stateCleared {
		t.Fatal("expected expired/capped passive msg_id to be cleared via saveState")
	}
}

func TestQQHandleDispatchMessageAttachments(t *testing.T) {
	t.Parallel()
	ch := store.RemoteChannel{
		Platform: domain.ChannelPlatformQQ,
		Config:   store.RemoteChannelConfig{AppID: "102999"},
	}
	adapter := NewQQAdapter(ch, store.RemoteChannelSecrets{AppSecret: "sec"}, nil)

	var got []InboundMessage
	rt := AdapterRuntime{
		OnInbound: func(_ context.Context, msg InboundMessage) error {
			got = append(got, msg)
			return nil
		},
	}

	// 1. 纯图片 + 语音（无文字正文）
	raw := json.RawMessage(`{
		"id": "qq-msg-att-1",
		"content": "",
		"author": {"user_openid": "u-open-1"},
		"attachments": [
			{"content_type": "image/png", "filename": "shot.png", "url": "//multimedia.nt.qq.com.cn/shot.png", "size": 1234},
			{"content_type": "voice", "filename": "record.silk", "url": "https://multimedia.nt.qq.com.cn/record.silk", "size": 2048}
		]
	}`)
	adapter.handleDispatchMessage(context.Background(), "C2C_MESSAGE_CREATE", raw, rt)

	if len(got) != 1 {
		t.Fatalf("expected 1 inbound message, got %d", len(got))
	}
	if got[0].ExternalMessageID != "qq-msg-att-1" || got[0].Content != "" {
		t.Fatalf("unexpected inbound meta: %+v", got[0])
	}
	if len(got[0].Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %+v", got[0].Attachments)
	}
	if got[0].Attachments[0].Kind != InboundAttachmentImage || got[0].Attachments[0].DownloadURL != "//multimedia.nt.qq.com.cn/shot.png" {
		t.Fatalf("unexpected image attachment: %+v", got[0].Attachments[0])
	}
	if got[0].Attachments[1].Kind != InboundAttachmentAudio || got[0].Attachments[1].DownloadURL != "" {
		t.Fatalf("expected audio attachment without eager download url, got %+v", got[0].Attachments[1])
	}
}

func TestTelegramPollOncePhotoDocumentAndVoice(t *testing.T) {
	t.Parallel()
	pngBytes := mustMakeTestPNG(t)
	logBytes := []byte("2026-04-20 ERROR something failed\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 1001,
						"message": map[string]any{
							"message_id": 51,
							"date":       1710000000,
							"caption":    "帮我看下这张图",
							"from":       map[string]any{"id": 9001, "is_bot": false},
							"chat":       map[string]any{"id": 9001, "type": "private"},
							"photo": []map[string]any{
								{"file_id": "small-id", "width": 90, "height": 90, "file_size": 100},
								{"file_id": "large-id", "width": 800, "height": 600, "file_size": len(pngBytes)},
							},
						},
					},
					{
						"update_id": 1002,
						"message": map[string]any{
							"message_id": 52,
							"date":       1710000001,
							"from":       map[string]any{"id": 9001, "is_bot": false},
							"chat":       map[string]any{"id": 9001, "type": "private"},
							"document": map[string]any{
								"file_id":   "doc-log-1",
								"file_name": "app.log",
								"mime_type": "text/plain",
								"file_size": len(logBytes),
							},
						},
					},
					{
						"update_id": 1003,
						"message": map[string]any{
							"message_id": 53,
							"date":       1710000002,
							"from":       map[string]any{"id": 9001, "is_bot": false},
							"chat":       map[string]any{"id": 9001, "type": "private"},
							"voice": map[string]any{
								"file_id":   "voice-1",
								"duration":  7,
								"mime_type": "audio/ogg",
							},
						},
					},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			var req struct {
				FileID string `json:"file_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			switch req.FileID {
			case "large-id":
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_path": "photos/pic.png", "file_size": len(pngBytes)}})
			case "doc-log-1":
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_path": "docs/app.log", "file_size": len(logBytes)}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "not found"})
			}
		case strings.HasSuffix(r.URL.Path, "/photos/pic.png"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		case strings.HasSuffix(r.URL.Path, "/docs/app.log"):
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(logBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	adapter, err := NewTelegramAdapter(
		store.RemoteChannel{
			Platform: domain.ChannelPlatformTelegram,
			Config:   store.RemoteChannelConfig{BaseURL: srv.URL},
		},
		store.RemoteChannelSecrets{BotToken: "12345:secret-token"},
		srv.Client(),
	)
	if err != nil {
		t.Fatalf("new telegram adapter: %v", err)
	}

	var got []InboundMessage
	if err := adapter.pollOnce(context.Background(), AdapterRuntime{
		OnInbound: func(_ context.Context, msg InboundMessage) error {
			got = append(got, msg)
			return nil
		},
	}, "mybot"); err != nil {
		t.Fatalf("telegram pollOnce failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 inbound messages (photo, doc, voice), got %d", len(got))
	}
	if got[0].Content != "帮我看下这张图" || len(got[0].Attachments) != 1 || !bytes.Equal(got[0].Attachments[0].RawData, pngBytes) {
		t.Fatalf("unexpected photo inbound: %+v", got[0])
	}
	if got[1].Content != "" || len(got[1].Attachments) != 1 || string(got[1].Attachments[0].RawData) != string(logBytes) {
		t.Fatalf("unexpected document inbound: %+v", got[1])
	}
	if got[2].Content != "" || len(got[2].Attachments) != 1 || got[2].Attachments[0].Kind != InboundAttachmentAudio || !strings.Contains(got[2].Attachments[0].Note, "7") {
		t.Fatalf("unexpected voice inbound: %+v", got[2])
	}
}

func TestWeixinExtractAttachmentsEncryptedImageAndVoiceFallback(t *testing.T) {
	t.Parallel()
	pngBytes := mustMakeTestPNG(t)
	aesKey := []byte("0123456789abcdef")
	cipherBytes := encryptAES128ECBPKCS7(t, pngBytes, aesKey)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(cipherBytes)
	}))
	defer srv.Close()

	adapter := NewWeixinAdapter(
		store.RemoteChannel{Platform: domain.ChannelPlatformWeixin},
		store.RemoteChannelSecrets{},
		srv.Client(),
	)
	items := []weixinMsgItem{
		{
			Type: weixinItemImage,
			ImageItem: weixinImageItem{
				FileName: "wx.png",
				Media: &weixinMediaRef{
					FullURL: srv.URL + "/enc.bin",
					AESKey:  base64.StdEncoding.EncodeToString(aesKey),
				},
			},
		},
		{
			Type:      weixinItemVoice,
			VoiceItem: weixinVoiceItem{Playtime: 4200},
		},
	}
	atts := adapter.extractWeixinAttachments(context.Background(), items)
	if len(atts) != 2 {
		t.Fatalf("expected 2 weixin attachments, got %+v", atts)
	}
	if atts[0].Kind != InboundAttachmentImage || !bytes.Equal(atts[0].RawData, pngBytes) {
		t.Fatalf("expected decrypted png bytes, got len=%d note=%q", len(atts[0].RawData), atts[0].Note)
	}
	if atts[1].Kind != InboundAttachmentAudio || !strings.Contains(atts[1].Note, "4") {
		t.Fatalf("expected voice fallback note, got %+v", atts[1])
	}
}

func TestFeishuDingTalkWeComMediaParsing(t *testing.T) {
	t.Parallel()
	pngBytes := mustMakeTestPNG(t)

	// 1. 飞书图片 + 语音
	feishuSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "fs-tok", "expire": 7200})
		case strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/messages/om_1/resources/img_key_1"):
			if r.Header.Get("Authorization") != "Bearer fs-tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer feishuSrv.Close()

	fsAdapter := NewFeishuAdapter(
		store.RemoteChannel{Platform: domain.ChannelPlatformFeishu, Config: store.RemoteChannelConfig{BaseURL: feishuSrv.URL, AppID: "cli_1"}},
		store.RemoteChannelSecrets{AppSecret: "sec_1"},
		feishuSrv.Client(),
	)
	text, atts := fsAdapter.extractFeishuContentAndAttachments(context.Background(), "om_1", "image", `{"image_key":"img_key_1"}`)
	if text != "" || len(atts) != 1 || !bytes.Equal(atts[0].RawData, pngBytes) {
		t.Fatalf("unexpected feishu image parse: text=%q atts=%+v", text, atts)
	}
	_, voiceAtts := fsAdapter.extractFeishuContentAndAttachments(context.Background(), "om_2", "audio", `{"file_key":"file_audio","duration":3100}`)
	if len(voiceAtts) != 1 || voiceAtts[0].Kind != InboundAttachmentAudio || !strings.Contains(voiceAtts[0].Note, "3") {
		t.Fatalf("unexpected feishu audio parse: %+v", voiceAtts)
	}

	// 2. 钉钉图片 + 带转写语音
	var dtSrvURL string
	dtSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "dt-tok", "expireIn": 7200})
		case "/v1.0/robot/messageFiles/download":
			_ = json.NewEncoder(w).Encode(map[string]any{"downloadUrl": dtSrvURL + "/file.png"})
		case "/file.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer dtSrv.Close()
	dtSrvURL = dtSrv.URL

	dtAdapter := NewDingTalkAdapter(
		store.RemoteChannel{Platform: domain.ChannelPlatformDingTalk, Config: store.RemoteChannelConfig{BaseURL: dtSrv.URL, AppID: "dt_app"}},
		store.RemoteChannelSecrets{AppSecret: "dt_sec"},
		dtSrv.Client(),
	)
	dtAtt := dtAdapter.fetchDingTalkAttachment(context.Background(), InboundAttachmentImage, "dl-code-1", "dt_app", "image.png")
	if !bytes.Equal(dtAtt.RawData, pngBytes) {
		t.Fatalf("expected dingtalk downloaded png bytes, got len=%d note=%q", len(dtAtt.RawData), dtAtt.Note)
	}

	// 3. 企业微信加密图片解密（AES-256-CBC）
	wecomKey := []byte("0123456789abcdef0123456789abcdef")
	wecomCipher := encryptAES256CBCPKCS7(t, pngBytes, wecomKey)
	wecomSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(wecomCipher)
	}))
	defer wecomSrv.Close()

	wcAdapter := NewWeComAdapter(
		store.RemoteChannel{Platform: domain.ChannelPlatformWeCom},
		store.RemoteChannelSecrets{},
		wecomSrv.Client(),
	)
	wcAtt := wcAdapter.fetchWeComAttachment(context.Background(), InboundAttachmentImage, wecomSrv.URL+"/media", base64.StdEncoding.EncodeToString(wecomKey), "image.png")
	if !bytes.Equal(wcAtt.RawData, pngBytes) {
		t.Fatalf("expected wecom decrypted png bytes, got len=%d note=%q", len(wcAtt.RawData), wcAtt.Note)
	}
}

func TestPrepareInboundAttachments_FallbackNotesAndLimit(t *testing.T) {
	t.Parallel()
	mgr := &Manager{}
	ch := store.RemoteChannel{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
	}
	msg := InboundMessage{
		Content: "",
		Attachments: []InboundAttachment{
			{Kind: InboundAttachmentAudio, Transcript: "明天九点提醒我开会"},
			{Kind: InboundAttachmentAudio, Note: "时长 4 秒"},
			{Kind: InboundAttachmentVideo, FileName: "demo.mp4", Note: "时长 12 秒"},
			{Kind: InboundAttachmentFile, FileName: "archive.zip", MimeType: "application/zip", DownloadURL: "https://example.com/archive.zip"},
			{Kind: InboundAttachmentImage, FileName: "pic.png", DownloadURL: "https://example.com/pic.png"},
		},
	}
	content, attIDs := mgr.prepareInboundAttachments(context.Background(), ch, msg, slog.Default())
	if len(attIDs) != 0 {
		t.Fatalf("expected 0 uploaded attachments when attachments service is nil, got %v", attIDs)
	}
	for _, want := range []string{
		"[语音转写] 明天九点提醒我开会",
		"收到语音消息",
		"时长 4 秒",
		"收到视频消息（demo.mp4）",
		"收到文件（archive.zip）",
		"收到图片（pic.png）",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected prepared content to contain %q, got:\n%s", want, content)
		}
	}
}

func TestDownloadInboundAttachmentGuards(t *testing.T) {
	t.Parallel()
	if _, _, err := downloadInboundAttachment(context.Background(), nil, "file:///etc/passwd", nil, 1024); err == nil {
		t.Fatal("expected file:// scheme to be rejected")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 2048))
	}))
	defer srv.Close()
	if _, _, err := downloadInboundAttachment(context.Background(), srv.Client(), srv.URL, nil, 512); err == nil {
		t.Fatal("expected oversized download to be rejected")
	}
}
