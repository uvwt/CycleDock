package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/store"
)

func makeHTTPTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: byte(x % 256), G: byte(y % 256), B: 180, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func newHTTPIntegrationWithAttachments(t *testing.T) (http.Handler, *pgxpool.Pool, *store.Store, *attachments.Service) {
	t.Helper()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	adminPool, err := pgxpool.New(context.Background(), baseURL)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}
	t.Cleanup(adminPool.Close)

	schema := "http_att_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
	})

	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	scopedURL := parsedURL.String()

	migrationDB, err := sql.Open("pgx", scopedURL)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	t.Cleanup(func() { _ = migrationDB.Close() })
	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(migrationDB, migrations); err != nil {
		t.Fatalf("migrate integration schema: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), scopedURL)
	if err != nil {
		t.Fatalf("connect integration schema: %v", err)
	}
	t.Cleanup(pool.Close)

	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	dataStore := store.New(pool)
	if err := dataStore.ConfigureMCPEncryption(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))); err != nil {
		t.Fatalf("configure MCP encryption: %v", err)
	}
	if err := dataStore.ConfigureModelEncryption(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))); err != nil {
		t.Fatalf("configure model encryption: %v", err)
	}
	attSvc, err := attachments.New(pool, attachments.Config{
		Dir:           t.TempDir(),
		TTL:           24 * time.Hour,
		MaxTotalBytes: 2 << 20,
	})
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}
	return NewRouter(pool, dataStore, authService, Config{Attachments: attSvc}), pool, dataStore, attSvc
}

func doMultipartUpload(t *testing.T, handler http.Handler, path, fieldName, fileName string, content []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAttachmentsHTTPFlowAndGoneDegradation(t *testing.T) {
	handler, pool, dataStore, attSvc := newHTTPIntegrationWithAttachments(t)

	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var identity identityResponse
	decodeRecorder(t, login, &identity)

	uploadPath := "/api/v1/workspaces/" + identity.Workspace.ID + "/attachments"

	// 1. 未登录不能上传。
	unauth := doMultipartUpload(t, handler, uploadPath, "file", "a.png", makeHTTPTestPNG(t, 64, 64), nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated upload, got %d", unauth.Code)
	}

	// 2. 拒绝 SVG 等可执行格式。
	svgResp := doMultipartUpload(t, handler, uploadPath, "file", "xss.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), cookie)
	if svgResp.Code != http.StatusBadRequest && svgResp.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 400/415 for SVG upload, got %d body=%s", svgResp.Code, svgResp.Body.String())
	}

	// 3. 正常上传 PNG 并校验压缩响应与读取头。
	upResp := doMultipartUpload(t, handler, uploadPath, "file", "shot.png", makeHTTPTestPNG(t, 200, 100), cookie)
	if upResp.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid upload, got %d body=%s", upResp.Code, upResp.Body.String())
	}
	var uploaded attachments.Attachment
	decodeRecorder(t, upResp, &uploaded)
	if uploaded.ID == uuid.Nil || uploaded.Width != 200 || uploaded.Height != 100 || !strings.HasPrefix(uploaded.URL, "/api/v1/workspaces/") {
		t.Fatalf("unexpected upload metadata: %+v", uploaded)
	}

	// 4. 流式读取图片并校验私有缓存与 ETag/304。
	getResp := doJSON(t, handler, http.MethodGet, uploaded.URL, nil, cookie, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("expected 200 for attachment read, got %d body=%s", getResp.Code, getResp.Body.String())
	}
	if cc := getResp.Header().Get("Cache-Control"); !strings.Contains(cc, "private") || !strings.Contains(cc, "max-age=86400") {
		t.Fatalf("unexpected Cache-Control: %q", cc)
	}
	etag := getResp.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header")
	}
	notMod := doJSON(t, handler, http.MethodGet, uploaded.URL, nil, cookie, map[string]string{"If-None-Match": etag})
	if notMod.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for matching ETag, got %d", notMod.Code)
	}

	// 5. 跨工作区访问被拒绝（403/404）。
	otherWS, err := dataStore.CreateWorkspace(context.Background(), "Other WS")
	if err != nil {
		t.Fatalf("create other ws: %v", err)
	}
	crossResp := doJSON(t, handler, http.MethodGet, "/api/v1/workspaces/"+otherWS.ID.String()+"/attachments/"+uploaded.ID.String(), nil, cookie, nil)
	if crossResp.Code != http.StatusForbidden && crossResp.Code != http.StatusNotFound {
		t.Fatalf("expected 403/404 for cross-workspace read, got %d", crossResp.Code)
	}

	// 6. 纯图片无文字评论发送成功，Timeline 返回轻引用且不含 base64。
	projResp := doJSON(t, handler, http.MethodPost, "/api/v1/projects", map[string]any{"name": "ImgProj"}, cookie, nil)
	if projResp.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", projResp.Code, projResp.Body.String())
	}
	var proj projectResponse
	decodeRecorder(t, projResp, &proj)

	discResp := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+proj.ID+"/discussions", map[string]any{}, cookie, nil)
	if discResp.Code != http.StatusCreated {
		t.Fatalf("create discussion: %d %s", discResp.Code, discResp.Body.String())
	}
	var card cardResponse
	decodeRecorder(t, discResp, &card)

	commentResp := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID+"/comments", map[string]any{
		"content":        "",
		"attachment_ids": []string{uploaded.ID.String()},
		"dispatch_mode":  "enqueue",
	}, cookie, map[string]string{"Idempotency-Key": "img-only-comment"})
	if commentResp.Code != http.StatusCreated {
		t.Fatalf("image-only comment: %d %s", commentResp.Code, commentResp.Body.String())
	}

	detailResp := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("get card detail: %d %s", detailResp.Code, detailResp.Body.String())
	}
	var detail cardDetailResponse
	decodeRecorder(t, detailResp, &detail)
	if len(detail.Timeline) != 1 {
		t.Fatalf("expected 1 timeline comment, got %d", len(detail.Timeline))
	}
	var payload store.CommentMessagePayload
	if err := json.Unmarshal(detail.Timeline[0].Message, &payload); err != nil {
		t.Fatalf("unmarshal comment payload: %v", err)
	}
	if len(payload.Attachments) != 1 || payload.Attachments[0].ID != uploaded.ID {
		t.Fatalf("expected 1 attachment ref matching %s in timeline message, got %+v", uploaded.ID, payload.Attachments)
	}

	// 校验数据库 events.message 不含 base64。
	var rawMessage string
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(message::text, '') FROM events WHERE card_id = $1 AND event_type = 'comment' LIMIT 1`, uuid.MustParse(card.ID)).Scan(&rawMessage); err != nil {
		t.Fatalf("query event message: %v", err)
	}
	if strings.Contains(rawMessage, "data:image/") || len(rawMessage) > 1024 {
		t.Fatalf("events.message should only store lightweight reference, got len=%d content=%s", len(rawMessage), rawMessage)
	}

	// 7. 过期清理后访问返回 410 Gone。
	if _, err := pool.Exec(context.Background(), `UPDATE workspace_attachments SET expires_at = now() - interval '1 hour' WHERE id = $1`, uploaded.ID); err != nil {
		t.Fatalf("expire attachment: %v", err)
	}
	if _, err := attSvc.RunCleanup(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	goneResp := doJSON(t, handler, http.MethodGet, uploaded.URL, nil, cookie, nil)
	if goneResp.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone for purged attachment, got %d body=%s", goneResp.Code, goneResp.Body.String())
	}
}

func TestCreateTodoWithImageAttachmentsEndToEnd(t *testing.T) {
	handler, pool, _, attSvc := newHTTPIntegrationWithAttachments(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)

	projectsRec := doJSON(t, handler, http.MethodGet, "/api/v1/projects", nil, cookie, nil)
	var projects []projectResponse
	decodeRecorder(t, projectsRec, &projects)
	if len(projects) == 0 {
		t.Fatalf("expected default project")
	}
	projectID := projects[0].ID

	uploadImage := func(w, h int) attachments.Attachment {
		t.Helper()
		rec := doMultipartUpload(
			t, handler,
			"/api/v1/workspaces/"+me.Workspace.ID+"/attachments",
			"file", "todo.png", makeHTTPTestPNG(t, w, h), cookie,
		)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload status = %d body = %s", rec.Code, rec.Body.String())
		}
		var uploaded attachments.Attachment
		decodeRecorder(t, rec, &uploaded)
		return uploaded
	}

	// 1. 先预上传两张图片（无 card_id），再携带 attachment_ids 创建待办。
	first := uploadImage(120, 80)
	second := uploadImage(90, 90)

	createRec := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+projectID+"/todos", map[string]any{
		"title":          "带参考图的待办",
		"description":    "请按截图修复布局问题",
		"attachment_ids": []string{first.ID.String(), second.ID.String()},
	}, cookie, nil)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create todo status = %d body = %s", createRec.Code, createRec.Body.String())
	}
	var created cardResponse
	decodeRecorder(t, createRec, &created)
	if created.AttachmentCount == nil || *created.AttachmentCount != 2 {
		t.Fatalf("expected attachment_count=2 on created todo, got %#v", created.AttachmentCount)
	}
	if len(created.Attachments) != 2 || created.Attachments[0].ID != first.ID || created.Attachments[1].ID != second.ID {
		t.Fatalf("expected ordered todo attachments [%s, %s], got %#v", first.ID, second.ID, created.Attachments)
	}
	if created.Attachments[0].Status != "active" {
		t.Fatalf("expected active attachment status, got %q", created.Attachments[0].Status)
	}

	// 2. GET /cards/{cardID} 稳定返回附件元数据。
	detailRec := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+created.ID, nil, cookie, nil)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("get card status = %d body = %s", detailRec.Code, detailRec.Body.String())
	}
	var detail cardDetailResponse
	decodeRecorder(t, detailRec, &detail)
	if len(detail.Card.Attachments) != 2 || detail.Card.Attachments[0].URL != first.URL {
		t.Fatalf("expected 2 card attachments on GET /cards/{id}, got %#v", detail.Card.Attachments)
	}

	// 3. 项目卡片列表摘要返回 attachment_count=2。
	listRec := doJSON(t, handler, http.MethodGet, "/api/v1/projects/"+projectID+"/cards", nil, cookie, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list cards status = %d body = %s", listRec.Code, listRec.Body.String())
	}
	var list []cardResponse
	decodeRecorder(t, listRec, &list)
	found := false
	for _, item := range list {
		if item.ID == created.ID {
			found = true
			if item.AttachmentCount == nil || *item.AttachmentCount != 2 {
				t.Fatalf("expected attachment_count=2 in project cards summary, got %#v", item.AttachmentCount)
			}
		}
	}
	if !found {
		t.Fatalf("created todo %s not found in project cards list", created.ID)
	}

	// 4. 校验禁止跨卡片重复绑定已被绑定到其他待办的图片。
	reuseRec := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+projectID+"/todos", map[string]any{
		"title":          "重复绑定应失败",
		"attachment_ids": []string{first.ID.String()},
	}, cookie, nil)
	if reuseRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when reusing bound attachment on another todo, got %d body=%s", reuseRec.Code, reuseRec.Body.String())
	}

	// 5. 超过 8 张附件应返回 400 Bad Request。
	tooMany := make([]string, 0, attachments.MaxAttachmentsPerTurn+1)
	for i := 0; i < attachments.MaxAttachmentsPerTurn+1; i++ {
		tooMany = append(tooMany, uuid.NewString())
	}
	tooManyRec := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+projectID+"/todos", map[string]any{
		"title":          "超限测试",
		"attachment_ids": tooMany,
	}, cookie, nil)
	if tooManyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when attachment_ids exceeds limit, got %d body=%s", tooManyRec.Code, tooManyRec.Body.String())
	}

	// 6. 过期并清理后，GET /cards/{cardID} 中对应附件状态切换为 purged，且下载返回 410 Gone。
	if _, err := pool.Exec(context.Background(), `UPDATE workspace_attachments SET expires_at = now() - interval '1 hour' WHERE id = $1`, first.ID); err != nil {
		t.Fatalf("expire first attachment: %v", err)
	}
	if _, err := attSvc.RunCleanup(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	detailAfterPurgeRec := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+created.ID, nil, cookie, nil)
	if detailAfterPurgeRec.Code != http.StatusOK {
		t.Fatalf("get card after purge status = %d body = %s", detailAfterPurgeRec.Code, detailAfterPurgeRec.Body.String())
	}
	var detailAfterPurge cardDetailResponse
	decodeRecorder(t, detailAfterPurgeRec, &detailAfterPurge)
	if len(detailAfterPurge.Card.Attachments) != 2 || detailAfterPurge.Card.Attachments[0].Status != "purged" || detailAfterPurge.Card.Attachments[1].Status != "active" {
		t.Fatalf("expected first=purged second=active, got %#v", detailAfterPurge.Card.Attachments)
	}
	goneRec := doJSON(t, handler, http.MethodGet, first.URL, nil, cookie, nil)
	if goneRec.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone for purged todo attachment, got %d", goneRec.Code)
	}

	// 7. 删除带有附件的卡片应正常返回 204，且卡片不再存在、保留的 workspace_attachments.card_id 被置空。
	deleteRec := doJSON(t, handler, http.MethodDelete, "/api/v1/cards/"+created.ID, nil, cookie, nil)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete todo with attachments status = %d body = %s", deleteRec.Code, deleteRec.Body.String())
	}
	afterDeleteRec := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+created.ID, nil, cookie, nil)
	if afterDeleteRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after deleting card with attachments, got %d body = %s", afterDeleteRec.Code, afterDeleteRec.Body.String())
	}
}
