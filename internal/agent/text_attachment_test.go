package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/testdb"
)

func setupAgentTextAttachmentEnv(t *testing.T) (*sqldb.DB, *attachments.Service, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.OpenDefault(t)

	ws1 := uuid.New()
	ws2 := uuid.New()
	src1 := uuid.New()
	src2 := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, 'WS1'), ($2, 'WS2')`, ws1, ws2); err != nil {
		t.Fatalf("insert workspaces: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sources (id, workspace_id, kind, name) VALUES ($1, $2, 'human', 'U1'), ($3, $4, 'human', 'U2')`, src1, ws1, src2, ws2); err != nil {
		t.Fatalf("insert sources: %v", err)
	}

	svc, err := attachments.New(pool, attachments.Config{
		Dir:                t.TempDir(),
		TTL:                24 * time.Hour,
		MaxTotalBytes:      16 << 20,
		MaxTextUploadBytes: 2 << 20,
	})
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}
	return pool, svc, ws1, src1, ws2, src2
}

func TestHydrateTextAttachmentsSmallInlineLargeSummaryAndExpired(t *testing.T) {
	pool, svc, ws1, src1, ws2, _ := setupAgentTextAttachmentEnv(t)
	ctx := context.Background()

	// 1. 小文本（<= 8 KiB 且 <= 200 行）直接完整附带进上下文，带 untrusted_data 边界
	smallContent := "package main\nfunc main() {\n\tprintln(\"hello cycledock\")\n}\n"
	smallAtt, err := svc.Upload(ctx, attachments.UploadParams{
		WorkspaceID:        ws1,
		UploadedBySourceID: src1,
		FileName:           "main.go",
		RawData:            []byte(smallContent),
	})
	if err != nil {
		t.Fatalf("upload small text: %v", err)
	}

	// 2. 大文本（> 8 KiB 且 > 200 行）只注入摘要 + 前几行预览 + cycledock_read_attachment 指引
	var largeBuilder strings.Builder
	for i := 1; i <= 320; i++ {
		fmt.Fprintf(&largeBuilder, "line-%03d: diagnostic payload entry with repeated text to exceed 8KiB threshold\n", i)
	}
	largeContent := largeBuilder.String()
	largeAtt, err := svc.Upload(ctx, attachments.UploadParams{
		WorkspaceID:        ws1,
		UploadedBySourceID: src1,
		FileName:           "server.log",
		RawData:            []byte(largeContent),
	})
	if err != nil {
		t.Fatalf("upload large text: %v", err)
	}

	// 3. 过期文本附件应返回明确提示且不报错中断
	expiredAtt, err := svc.Upload(ctx, attachments.UploadParams{
		WorkspaceID:        ws1,
		UploadedBySourceID: src1,
		FileName:           "old.log",
		RawData:            []byte("stale log line\n"),
	})
	if err != nil {
		t.Fatalf("upload expired text: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workspace_attachments SET expires_at = now() - interval '1 hour' WHERE id = $1`, expiredAtt.ID); err != nil {
		t.Fatalf("expire text attachment: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromParts([]*genai.Part{
				genai.NewPartFromText("请查看这三个文本附件"),
				genai.NewPartFromURI(userAttachmentURI(smallAtt.ID), attachments.TextMimeType),
				genai.NewPartFromURI(userAttachmentURI(largeAtt.ID), attachments.TextMimeType),
				genai.NewPartFromURI(userAttachmentURI(expiredAtt.ID), attachments.TextMimeType),
			}, genai.RoleUser),
		},
	}

	messages, _, err := adkRequestToModelClientWithMedia(ctx, req, nil, svc, ws1, uuid.New())
	if err != nil {
		t.Fatalf("adkRequestToModelClientWithMedia: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messagesContainImages(messages) {
		t.Fatalf("text attachments must not be treated as multimodal images")
	}

	joined := messages[0].Content
	if !strings.Contains(joined, "<user_text_attachment ") || !strings.Contains(joined, `untrusted_data="true"`) || !strings.Contains(joined, `println("hello cycledock")`) {
		t.Fatalf("expected small text attachment to be inlined with untrusted boundary, got:\n%s", joined)
	}
	if !strings.Contains(joined, "<user_text_attachment_summary ") || !strings.Contains(joined, "cycledock_read_attachment") || !strings.Contains(joined, largeAtt.ID.String()) {
		t.Fatalf("expected large text attachment to be summarized with tool hint, got:\n%s", joined)
	}
	if strings.Contains(joined, "line-320:") {
		t.Fatalf("large text attachment tail should NOT be inlined into initial context")
	}
	if !strings.Contains(joined, "已过期") || !strings.Contains(joined, "old.log") {
		t.Fatalf("expected expired text attachment notice, got:\n%s", joined)
	}

	// 4. 验证 cycledock_read_attachment 分段（offset/max_chars + 行范围）读取与跨 Workspace 隔离
	rt := attemptToolRuntime{
		context: store.AgentCardContext{
			WorkspaceID: ws1,
		},
		attachments: svc,
	}

	// 按行范围读取后半段
	lineRes, err := rt.readAttachmentTool(nil, map[string]any{
		"attachment_id": largeAtt.ID.String(),
		"start_line":    315,
		"end_line":      320,
	})
	if err != nil {
		t.Fatalf("readAttachmentTool(lines): %v", err)
	}
	lineContent, _ := lineRes["content"].(string)
	if !strings.Contains(lineContent, "line-315:") || !strings.Contains(lineContent, "line-320:") {
		t.Fatalf("unexpected line slice content: %q", lineContent)
	}
	if hasMore, _ := lineRes["has_more"].(bool); hasMore {
		t.Fatalf("expected has_more=false at end of file")
	}

	// 按字符 offset 分页读取
	chunkRes, err := rt.readAttachmentTool(nil, map[string]any{
		"attachment_id": largeAtt.ID.String(),
		"offset":        0,
		"max_chars":     120,
	})
	if err != nil {
		t.Fatalf("readAttachmentTool(chunk): %v", err)
	}
	if nextOffset, _ := chunkRes["next_offset"].(int); nextOffset != 120 {
		t.Fatalf("expected next_offset=120, got %v", chunkRes["next_offset"])
	}
	if hasMore, _ := chunkRes["has_more"].(bool); !hasMore {
		t.Fatalf("expected has_more=true for first chunk")
	}

	// 读取过期附件返回清晰提示，不返回错误中断 Turn
	expRes, err := rt.readAttachmentTool(nil, map[string]any{
		"attachment_id": expiredAtt.ID.String(),
	})
	if err != nil {
		t.Fatalf("readAttachmentTool(expired) should not hard-error: %v", err)
	}
	if expiredFlag, _ := expRes["expired"].(bool); !expiredFlag {
		t.Fatalf("expected expired=true, got %+v", expRes)
	}

	// 跨 Workspace 隔离：ws2 无法读取 ws1 的附件
	otherRT := attemptToolRuntime{
		context: store.AgentCardContext{
			WorkspaceID: ws2,
		},
		attachments: svc,
	}
	crossRes, err := otherRT.readAttachmentTool(nil, map[string]any{
		"attachment_id": largeAtt.ID.String(),
	})
	if err != nil {
		t.Fatalf("cross-workspace readAttachmentTool unexpected error: %v", err)
	}
	if _, hasContent := crossRes["content"]; hasContent {
		t.Fatalf("cross-workspace read must not return attachment content: %+v", crossRes)
	}
}
