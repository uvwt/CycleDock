package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func makeAgentTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 40, G: 120, B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestBuildUserTurnContentPersistsReferenceOnlyNoInlineBytes(t *testing.T) {
	attID := uuid.New()
	content := buildUserTurnContent(store.AgentCardContext{
		TurnContent: "请看这张截图",
		TurnAttachments: []store.CommentAttachmentRef{
			{ID: attID, MimeType: "image/jpeg", Width: 800, Height: 600, SizeBytes: 12345},
		},
	})
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal genai.Content: %v", err)
	}
	rawStr := string(raw)
	if strings.Contains(rawStr, "inlineData") || strings.Contains(rawStr, "data:image/") {
		t.Fatalf("user turn content must never persist inline image bytes/base64, got %s", rawStr)
	}
	expectedURI := userAttachmentURI(attID)
	if !strings.Contains(rawStr, expectedURI) {
		t.Fatalf("expected FileData URI %q in serialized content, got %s", expectedURI, rawStr)
	}
}

func TestBuildUserTurnContentIncludesCardAttachmentsOnFirstTurnOnly(t *testing.T) {
	cardAttID := uuid.New()
	turnAttID := uuid.New()

	// 首轮（TurnSequence == 1）：同时携带卡片附带图片与当前消息图片，且仅存轻量 FileData URI。
	firstTurn := buildUserTurnContent(store.AgentCardContext{
		TurnSequence: 1,
		TurnContent:  "开始处理这个待办",
		CardAttachments: []store.CommentAttachmentRef{
			{ID: cardAttID, MimeType: "image/png", Width: 640, Height: 480, SizeBytes: 4096},
		},
		TurnAttachments: []store.CommentAttachmentRef{
			{ID: turnAttID, MimeType: "image/jpeg", Width: 800, Height: 600, SizeBytes: 8192},
		},
	})
	rawFirst, err := json.Marshal(firstTurn)
	if err != nil {
		t.Fatalf("marshal first turn: %v", err)
	}
	firstStr := string(rawFirst)
	if strings.Contains(firstStr, "inlineData") || strings.Contains(firstStr, "data:image/") {
		t.Fatalf("first turn must never persist inline image bytes/base64, got %s", firstStr)
	}
	if !strings.Contains(firstStr, userAttachmentURI(cardAttID)) {
		t.Fatalf("expected card attachment URI %q on first turn, got %s", userAttachmentURI(cardAttID), firstStr)
	}
	if !strings.Contains(firstStr, userAttachmentURI(turnAttID)) {
		t.Fatalf("expected turn attachment URI %q on first turn, got %s", userAttachmentURI(turnAttID), firstStr)
	}

	// 后续轮次（TurnSequence == 2）：不再重复注入卡片初始附件，避免会话重复膨胀。
	secondTurn := buildUserTurnContent(store.AgentCardContext{
		TurnSequence: 2,
		TurnContent:  "继续补充一下",
		CardAttachments: []store.CommentAttachmentRef{
			{ID: cardAttID, MimeType: "image/png", Width: 640, Height: 480, SizeBytes: 4096},
		},
	})
	rawSecond, err := json.Marshal(secondTurn)
	if err != nil {
		t.Fatalf("marshal second turn: %v", err)
	}
	if strings.Contains(string(rawSecond), userAttachmentURI(cardAttID)) {
		t.Fatalf("second turn should not duplicate card attachment URI, got %s", string(rawSecond))
	}
}

func TestAdkRequestHydratesUserAttachmentAndDegradesWhenExpiredOrOld(t *testing.T) {
	ctx := context.Background()
	// 复用隔离临时 schema 并执行完整迁移，避免依赖外部预迁移的 public schema。
	_, pool := newAgentIntegrationStore(t)

	wsID := uuid.New()
	srcID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, 'WS')`, wsID); err != nil {
		t.Fatalf("insert ws: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sources (id, workspace_id, kind, name) VALUES ($1, $2, 'human', 'H')`, srcID, wsID); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	svc, err := attachments.New(pool, attachments.Config{
		Dir:           t.TempDir(),
		TTL:           24 * time.Hour,
		MaxTotalBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}

	attActive, err := svc.Upload(ctx, attachments.UploadParams{
		WorkspaceID:        wsID,
		UploadedBySourceID: srcID,
		RawData:            makeAgentTestPNG(t, 64, 64),
	})
	if err != nil {
		t.Fatalf("upload active: %v", err)
	}
	attExpired, err := svc.Upload(ctx, attachments.UploadParams{
		WorkspaceID:        wsID,
		UploadedBySourceID: srcID,
		RawData:            makeAgentTestPNG(t, 48, 48),
	})
	if err != nil {
		t.Fatalf("upload expired: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workspace_attachments SET expires_at = now() - interval '1 hour' WHERE id = $1`, attExpired.ID); err != nil {
		t.Fatalf("expire attachment: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromParts([]*genai.Part{
				genai.NewPartFromText("旧图片和当前图片"),
				genai.NewPartFromURI(userAttachmentURI(attExpired.ID), attExpired.MimeType),
				genai.NewPartFromURI(userAttachmentURI(attActive.ID), attActive.MimeType),
			}, genai.RoleUser),
		},
	}

	messages, _, err := adkRequestToModelClientWithMedia(ctx, req, nil, svc, wsID, uuid.New())
	if err != nil {
		t.Fatalf("adkRequestToModelClientWithMedia: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	msg := messages[0]
	var textParts []string
	var imageCount int
	for _, part := range msg.Parts {
		if part.Type == modelclient.ContentPartImage {
			imageCount++
			if len(part.Data) == 0 {
				t.Fatal("expected hydrated image bytes")
			}
		} else {
			textParts = append(textParts, part.Text)
		}
	}
	if imageCount != 1 {
		t.Fatalf("expected 1 hydrated image (active only), got %d", imageCount)
	}
	joined := strings.Join(textParts, "\n")
	if !strings.Contains(joined, "已过期清理") {
		t.Fatalf("expected expired attachment to degrade to text notice, got %q", joined)
	}
}

func TestRouteModelDegradesVisionToTextOnUnsupportedModelError(t *testing.T) {
	calls := 0
	var secondRequestHasImage bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rawBody, _ := json.Marshal(body)
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"This model does not support image_url input"}}`))
			return
		}
		secondRequestHasImage = strings.Contains(string(rawBody), "image_url")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"已收到文字说明"}}]}`))
	}))
	defer server.Close()

	route := store.ModelRoute{
		ID:              uuid.New(),
		Enabled:         true,
		ProviderType:    "openai_compatible",
		ProviderBaseUrl: server.URL,
		Model:           "text-only-model",
	}
	llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{route})
	if err != nil {
		t.Fatalf("newADKRouteModel: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromParts([]*genai.Part{
				genai.NewPartFromText("请帮我看看"),
				genai.NewPartFromBytes(makeAgentTestPNG(t, 16, 16), "image/png"),
			}, genai.RoleUser),
		},
	}

	var gotText string
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("expected degraded retry to succeed, got err: %v", err)
		}
		if resp != nil && resp.Content != nil {
			gotText = textFromADKContent(resp.Content)
		}
	}
	if calls != 2 {
		t.Fatalf("expected 2 model calls (vision rejected -> text fallback), got %d", calls)
	}
	if secondRequestHasImage {
		t.Fatal("expected fallback request to strip image_url parts")
	}
	if !strings.Contains(gotText, "已收到文字说明") {
		t.Fatalf("unexpected fallback response: %q", gotText)
	}
}
