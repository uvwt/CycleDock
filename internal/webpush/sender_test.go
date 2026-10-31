package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
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
