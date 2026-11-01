package webpush

import (
	"context"
	"crypto/elliptic"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

const defaultSendTimeout = 15 * time.Second

// ErrGone 表示浏览器已退订，调用方应删除这条订阅。
var ErrGone = errors.New("push subscription is gone")

// Sender 用 VAPID 把通知交给浏览器厂商的推送服务。
type Sender struct {
	publicKey  string
	privateKey string
	subject    string
	client     webpush.HTTPClient
}

// New 在密钥留空时返回 nil，页面打开时的通知仍然可用。
// subject 传给推送库前不能带 mailto: 前缀，库会自己补上。
func New(publicKey, privateKey, subject string) (*Sender, error) {
	publicKey = strings.TrimSpace(publicKey)
	privateKey = strings.TrimSpace(privateKey)
	subject = strings.TrimSpace(subject)
	if publicKey == "" && privateKey == "" {
		return nil, nil
	}
	if publicKey == "" || privateKey == "" {
		return nil, errors.New("CYCLEDOCK_VAPID_PUBLIC_KEY and CYCLEDOCK_VAPID_PRIVATE_KEY must both be set")
	}
	if err := validateKeys(publicKey, privateKey); err != nil {
		return nil, err
	}
	if subject == "" {
		return nil, errors.New("CYCLEDOCK_VAPID_SUBJECT or an https CYCLEDOCK_PUBLIC_URL is required")
	}
	normalizedSubject, err := normalizeSubject(subject)
	if err != nil {
		return nil, err
	}
	return &Sender{
		publicKey:  publicKey,
		privateKey: privateKey,
		subject:    normalizedSubject,
		client:     &http.Client{Timeout: defaultSendTimeout},
	}, nil
}

func normalizeSubject(subject string) (string, error) {
	lower := strings.ToLower(subject)
	if strings.HasPrefix(lower, "mailto:") {
		subject = strings.TrimSpace(subject[len("mailto:"):])
		lower = strings.ToLower(subject)
	}
	if strings.HasPrefix(lower, "https://") {
		normalized := "https://" + subject[len("https://"):]
		parsed, err := url.Parse(normalized)
		if err != nil || parsed.Host == "" {
			return "", errors.New("CYCLEDOCK_VAPID_SUBJECT must be an email or https URL")
		}
		return normalized, nil
	}
	if !strings.Contains(subject, "@") {
		return "", errors.New("CYCLEDOCK_VAPID_SUBJECT must be an email or https URL")
	}
	return subject, nil
}

func validateKeys(publicKey, privateKey string) error {
	private, err := base64.RawURLEncoding.DecodeString(privateKey)
	if err != nil || len(private) != 32 {
		return errors.New("CYCLEDOCK_VAPID_PRIVATE_KEY must be a 32-byte base64url P-256 private key")
	}
	public, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil || len(public) != 65 {
		return errors.New("CYCLEDOCK_VAPID_PUBLIC_KEY must be an uncompressed base64url P-256 public key")
	}
	x, y := elliptic.P256().ScalarBaseMult(private)
	if string(elliptic.Marshal(elliptic.P256(), x, y)) != string(public) {
		return errors.New("CYCLEDOCK_VAPID_PUBLIC_KEY does not match CYCLEDOCK_VAPID_PRIVATE_KEY")
	}
	return nil
}

func (s *Sender) PublicKey() string {
	if s == nil {
		return ""
	}
	return s.publicKey
}

func (s *Sender) Send(ctx context.Context, endpoint, p256dh, auth, topic string, payload []byte) error {
	if s == nil {
		return errors.New("push sender is not configured")
	}
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: defaultSendTimeout}
	}
	response, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: endpoint,
		Keys:     webpush.Keys{P256dh: p256dh, Auth: auth},
	}, &webpush.Options{
		HTTPClient:      client,
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		TTL:             int((12 * time.Hour).Seconds()),
		Urgency:         webpush.UrgencyHigh,
		Topic:           topic,
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	_, _ = io.Copy(io.Discard, response.Body)
	detail := compactBodyDetail(rawBody)
	if isPermanentSubscriptionGone(response.StatusCode, detail) {
		return ErrGone
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		host := endpointHost(endpoint)
		if detail != "" {
			return fmt.Errorf("push endpoint %s returned %d: %s", host, response.StatusCode, detail)
		}
		return fmt.Errorf("push endpoint %s returned %d", host, response.StatusCode)
	}
	return nil
}

func isPermanentSubscriptionGone(statusCode int, detail string) bool {
	if statusCode == http.StatusNotFound || statusCode == http.StatusGone {
		return true
	}
	if statusCode == http.StatusBadRequest && (strings.Contains(detail, "BadDeviceToken") || strings.Contains(detail, "Unregistered")) {
		return true
	}
	if statusCode == http.StatusForbidden && strings.Contains(detail, "VapidPkHashMismatch") {
		return true
	}
	return false
}

func compactBodyDetail(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func endpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return parsed.Host
}
