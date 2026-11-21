package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/CycleDock/internal/attachments"
)

func TestHTTPTextAttachmentsUploadDownloadTodoAndCrossWorkspaceIsolation(t *testing.T) {
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

	wsID := identity.Workspace.ID
	uploadPath := "/api/v1/workspaces/" + wsID + "/attachments"

	otherWS, err := dataStore.CreateWorkspace(context.Background(), "Other Text WS")
	if err != nil {
		t.Fatalf("create other ws: %v", err)
	}

	projectsRec := doJSON(t, handler, http.MethodGet, "/api/v1/projects", nil, cookie, nil)
	var projects []projectResponse
	decodeRecorder(t, projectsRec, &projects)
	if len(projects) == 0 {
		t.Fatalf("expected default project")
	}
	projectID := projects[0].ID
	discResp := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+projectID+"/discussions", map[string]any{}, cookie, nil)
	if discResp.Code != http.StatusCreated {
		t.Fatalf("create discussion: %d %s", discResp.Code, discResp.Body.String())
	}
	var discussionCard cardResponse
	decodeRecorder(t, discResp, &discussionCard)
	discussionCardID := discussionCard.ID

	// 1. 拒绝 PDF 与二进制文件
	pdfRR := doMultipartUpload(t, handler, uploadPath, "file", "report.pdf", []byte("%PDF-1.7\nhello\n"), cookie)
	if pdfRR.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for PDF upload, got %d: %s", pdfRR.Code, pdfRR.Body.String())
	}
	binRR := doMultipartUpload(t, handler, uploadPath, "file", "crash.log", []byte("ok\x00binary"), cookie)
	if binRR.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for binary-with-NUL upload, got %d: %s", binRR.Code, binRR.Body.String())
	}

	// 2. 上传纯文本 HTML 文件，确认服务端以 gzip 落盘，下载时透明解压且强制 text/plain + nosniff 防 XSS
	htmlPayload := []byte("<html><body><script>alert('xss')</script>\nline 2\nline 3\n</body></html>\n")
	upRR := doMultipartUpload(t, handler, uploadPath, "file", "debug.html", htmlPayload, cookie)
	if upRR.Code != http.StatusCreated {
		t.Fatalf("expected 201 for text upload, got %d: %s", upRR.Code, upRR.Body.String())
	}
	var uploaded attachments.Attachment
	decodeRecorder(t, upRR, &uploaded)
	if uploaded.Kind != attachments.KindText || uploaded.FileName != "debug.html" || uploaded.StorageEncoding != attachments.EncodingGzip {
		t.Fatalf("unexpected uploaded metadata: %+v", uploaded)
	}
	if uploaded.SizeBytes != int64(len(htmlPayload)) || uploaded.LineCount != 4 {
		t.Fatalf("unexpected size/lines: size=%d lines=%d", uploaded.SizeBytes, uploaded.LineCount)
	}

	// 下载预览（inline）
	getReq := httptest.NewRequest(http.MethodGet, uploaded.URL, nil)
	getReq.AddCookie(cookie)
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on download, got %d: %s", getRR.Code, getRR.Body.String())
	}
	if ct := getRR.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("expected text/plain content-type for XSS safety, got %q", ct)
	}
	if nosniff := getRR.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options: nosniff, got %q", nosniff)
	}
	if gotBytes, _ := io.ReadAll(getRR.Body); !bytes.Equal(gotBytes, htmlPayload) {
		t.Fatalf("downloaded text mismatch: got %q", string(gotBytes))
	}

	// 下载模式（download=1）带 attachment disposition
	dlReq := httptest.NewRequest(http.MethodGet, uploaded.URL+"?download=1", nil)
	dlReq.AddCookie(cookie)
	dlRR := httptest.NewRecorder()
	handler.ServeHTTP(dlRR, dlReq)
	if dlRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on download=1, got %d", dlRR.Code)
	}
	if cd := dlRR.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "debug.html") {
		t.Fatalf("expected attachment Content-Disposition with filename, got %q", cd)
	}

	// 3. 跨 Workspace 隔离：不能用 otherWS 路由读取 wsID 的附件
	crossRR := doJSON(t, handler, http.MethodGet, "/api/v1/workspaces/"+otherWS.ID.String()+"/attachments/"+uploaded.ID.String(), nil, cookie, nil)
	if crossRR.Code != http.StatusForbidden && crossRR.Code != http.StatusNotFound {
		t.Fatalf("expected 403/404 on cross-workspace attachment read, got %d", crossRR.Code)
	}

	// 4. 在聊天消息中发送文本附件
	msgRR := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+discussionCardID+"/comments", map[string]any{
		"content":        "请帮我排查日志",
		"attachment_ids": []string{uploaded.ID.String()},
	}, cookie, map[string]string{"Idempotency-Key": "text-att-comment-1"})
	if msgRR.Code != http.StatusCreated {
		t.Fatalf("expected 201 on comment with text attachment, got %d: %s", msgRR.Code, msgRR.Body.String())
	}

	// 5. 新建待办携带纯文本代码附件
	codePayload := []byte("package demo\nfunc Sum(a, b int) int { return a + b }\n")
	codeUpRR := doMultipartUpload(t, handler, uploadPath, "file", "sum.go", codePayload, cookie)
	if codeUpRR.Code != http.StatusCreated {
		t.Fatalf("expected 201 on code upload, got %d: %s", codeUpRR.Code, codeUpRR.Body.String())
	}
	var codeAtt attachments.Attachment
	decodeRecorder(t, codeUpRR, &codeAtt)

	todoRR := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+projectID+"/todos", map[string]any{
		"title":          "审查 sum.go",
		"description":    "查看附件代码",
		"attachment_ids": []string{codeAtt.ID.String()},
	}, cookie, nil)
	if todoRR.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating todo with text attachment, got %d: %s", todoRR.Code, todoRR.Body.String())
	}
	var createdTodo cardResponse
	decodeRecorder(t, todoRR, &createdTodo)
	if len(createdTodo.Attachments) != 1 || createdTodo.Attachments[0].Kind != attachments.KindText || createdTodo.Attachments[0].FileName != "sum.go" {
		t.Fatalf("unexpected todo attachments: %+v", createdTodo.Attachments)
	}

	// 6. 过期清理后，下载返回 410 Gone，且卡片详情时间线中状态变为 purged
	if _, err := pool.Exec(context.Background(), `UPDATE workspace_attachments SET expires_at = now() - interval '1 hour' WHERE id = $1`, uploaded.ID); err != nil {
		t.Fatalf("expire uploaded: %v", err)
	}
	if _, err := attSvc.RunCleanup(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}

	goneRR := doJSON(t, handler, http.MethodGet, uploaded.URL, nil, cookie, nil)
	if goneRR.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone for purged text attachment, got %d: %s", goneRR.Code, goneRR.Body.String())
	}

	detailRR := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+discussionCardID, nil, cookie, nil)
	if detailRR.Code != http.StatusOK {
		t.Fatalf("expected 200 getting card detail, got %d: %s", detailRR.Code, detailRR.Body.String())
	}
	if !strings.Contains(detailRR.Body.String(), `"status":"purged"`) {
		t.Fatalf("expected timeline attachment status=purged after cleanup, got: %s", detailRR.Body.String())
	}

	todoDetailRR := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+createdTodo.ID, nil, cookie, nil)
	if todoDetailRR.Code != http.StatusOK {
		t.Fatalf("expected 200 getting todo detail, got %d: %s", todoDetailRR.Code, todoDetailRR.Body.String())
	}
	bodyStr := todoDetailRR.Body.String()
	if !strings.Contains(bodyStr, `"kind":"text"`) || !strings.Contains(bodyStr, `"file_name":"sum.go"`) {
		t.Fatalf("expected todo card_attachments with text metadata, got: %s", bodyStr)
	}
}
