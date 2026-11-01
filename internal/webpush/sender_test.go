package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lib "github.com/SherClockHolmes/webpush-go"
)

func TestSenderPostsEncryptedRequest(t *testing.T) {
	privateKey, publicKey, err := lib.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := New(publicKey, privateKey, "mailto:ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if sender.PublicKey() != publicKey {
		t.Fatalf("public key = %q", sender.PublicKey())
	}
	var authorization, encoding string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		encoding = r.Header.Get("Content-Encoding")
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	browserKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	// 测试服务器证书不是推送服务的证书。库使用默认客户端，会在 TLS 校验失败。
	// 这里只确认密钥材料能通过本地校验，真正的 HTTP 投递用库的错误区分。
	err = sender.Send(context.Background(), server.URL, base64.RawURLEncoding.EncodeToString(browserKey.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(auth), "cardtopic01234567890123456789012", []byte(`{"title":"卡片有更新"}`))
	if err == nil {
		t.Fatal("expected TLS failure against the test server")
	}
	if authorization != "" || encoding != "" {
		t.Fatalf("request reached the test server: auth=%q encoding=%q", authorization, encoding)
	}
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "tls") && !strings.Contains(err.Error(), "x509") {
		t.Fatalf("send error = %v", err)
	}

	// 使用信任测试证书的客户端验证完整的加密推送请求头以及 Apple/FCM 响应状态处理。
	sender.client = server.Client()
	err = sender.Send(context.Background(), server.URL+"/wpush/v2/secret-token", base64.RawURLEncoding.EncodeToString(browserKey.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(auth), "cardtopic01234567890123456789012", []byte(`{"title":"卡片有更新"}`))
	if err != nil {
		t.Fatalf("trusted send error = %v", err)
	}
	if !strings.HasPrefix(authorization, "vapid t=") || encoding != "aes128gcm" {
		t.Fatalf("unexpected headers: auth=%q encoding=%q", authorization, encoding)
	}
}

func TestSenderClassifiesEndpointErrorsWithoutLeakingToken(t *testing.T) {
	privateKey, publicKey, err := lib.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := New(publicKey, privateKey, "HTTPS://cycledock.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if sender.subject != "https://cycledock.example.com" {
		t.Fatalf("normalized subject = %q", sender.subject)
	}
	browserKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	p256dh := base64.RawURLEncoding.EncodeToString(browserKey.PublicKey().Bytes())
	authSecret := base64.RawURLEncoding.EncodeToString(auth)

	var status int
	var body string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	sender.client = server.Client()
	endpoint := server.URL + "/wpush/v2/private-capability-token"

	for _, tc := range []struct {
		status int
		body   string
		gone   bool
	}{
		{status: http.StatusGone, body: `{"reason":"Unregistered"}`, gone: true},
		{status: http.StatusBadRequest, body: `{"reason":"BadDeviceToken"}`, gone: true},
		{status: http.StatusForbidden, body: `{"reason":"VapidPkHashMismatch"}`, gone: true},
		{status: http.StatusForbidden, body: `{"reason":"BadJwtToken"}`, gone: false},
	} {
		status = tc.status
		body = tc.body
		err := sender.Send(context.Background(), endpoint, p256dh, authSecret, "cardtopic", []byte(`{"title":"test"}`))
		if tc.gone {
			if !errors.Is(err, ErrGone) {
				t.Fatalf("status %d (%s): got %v, want ErrGone", tc.status, tc.body, err)
			}
			continue
		}
		if err == nil || errors.Is(err, ErrGone) {
			t.Fatalf("status %d (%s): got %v, want diagnostic error", tc.status, tc.body, err)
		}
		if !strings.Contains(err.Error(), "BadJwtToken") {
			t.Fatalf("error missing response reason: %v", err)
		}
		if strings.Contains(err.Error(), "private-capability-token") {
			t.Fatalf("error leaked endpoint token: %v", err)
		}
	}
}

func TestNewRequiresAMatchingKeyPair(t *testing.T) {
	if sender, err := New("", "", ""); err != nil || sender != nil {
		t.Fatalf("empty config = %v, %v", sender, err)
	}
	privateKey, publicKey, err := lib.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(publicKey, "", "ops@example.com"); err == nil {
		t.Fatal("accepted a public key without a private key")
	}
	otherPrivate, _, err := lib.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(publicKey, otherPrivate, "ops@example.com"); err == nil {
		t.Fatal("accepted mismatched keys")
	}
	sender, err := New(publicKey, privateKey, "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if sender.subject != "ops@example.com" {
		t.Fatalf("subject = %q", sender.subject)
	}
}
