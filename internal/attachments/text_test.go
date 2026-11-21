package attachments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func setupAttachmentTestDB(t *testing.T) (*pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(adminPool.Close)

	schema := "att_txt_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
	})

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test db url: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	sqlDB := stdlib.OpenDB(*cfg.ConnConfig)
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect test schema pool: %v", err)
	}
	t.Cleanup(pool.Close)

	wsID := uuid.New()
	srcID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, 'WS1')`, wsID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sources (id, workspace_id, kind, name) VALUES ($1, $2, 'human', 'U1')`, srcID, wsID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	return pool, wsID, srcID
}

func TestIsAllowedTextFileNameAndValidation(t *testing.T) {
	allowed := []string{
		"server.log",
		"README.md",
		"main.go",
		"App.tsx",
		"config.yaml",
		"settings.yml",
		"cargo.toml",
		"app.ini",
		".env.example",
		"data.csv",
		"report.tsv",
		"schema.sql",
		"deploy.sh",
		"build.ps1",
		"script.py",
		"lib.rs",
		"Main.java",
		"util.cpp",
		"patch.diff",
		"fix.patch",
		"nginx.conf",
		"Dockerfile",
		"Makefile",
		"notes.TXT",
		"nested/dir/error.log",
	}
	for _, name := range allowed {
		if !IsAllowedTextFileName(name) {
			t.Errorf("expected %q to be allowed", name)
		}
	}

	rejected := []string{
		"",
		"report.pdf",
		"archive.zip",
		"backup.tar.gz",
		"video.mp4",
		"binary.exe",
		"photo.png",
		"icon.svg",
		"doc.docx",
		"unknown.bin",
	}
	for _, name := range rejected {
		if IsAllowedTextFileName(name) {
			t.Errorf("expected %q to be rejected", name)
		}
	}
}

func TestProcessTextFileGzipCompressionAndRejections(t *testing.T) {
	// 1. 合法日志文本：gzip 压缩落盘应显著小于原始大小，且可透明解压还原
	var sb strings.Builder
	for i := 1; i <= 400; i++ {
		fmt.Fprintf(&sb, "2026-11-21T10:00:%02dZ INFO worker=sync-01 event=batch_ok processed=%d latency_ms=12\n", i%60, i)
	}
	raw := []byte(sb.String())

	processed, err := ProcessTextFile("../logs/worker.log", raw, DefaultMaxTextUploadBytes)
	if err != nil {
		t.Fatalf("ProcessTextFile failed: %v", err)
	}
	if processed.FileName != "worker.log" {
		t.Fatalf("expected sanitized filename worker.log, got %q", processed.FileName)
	}
	if processed.StorageEncoding != EncodingGzip {
		t.Fatalf("expected gzip storage encoding, got %q", processed.StorageEncoding)
	}
	if processed.Ext != ".txt.gz" {
		t.Fatalf("expected .txt.gz ext, got %q", processed.Ext)
	}
	if processed.LineCount != 400 {
		t.Fatalf("expected 400 lines, got %d", processed.LineCount)
	}
	if processed.SizeBytes != int64(len(raw)) {
		t.Fatalf("expected size_bytes=%d, got %d", len(raw), processed.SizeBytes)
	}
	// 重复日志典型压缩率 > 80%
	if processed.StoredSizeBytes >= processed.SizeBytes/4 {
		t.Fatalf("expected gzip stored size (%d) to be < 25%% of raw size (%d)", processed.StoredSizeBytes, processed.SizeBytes)
	}

	decompressed, err := DecompressGzip(bytes.NewReader(processed.CompressedData), DefaultMaxTextUploadBytes)
	if err != nil {
		t.Fatalf("DecompressGzip failed: %v", err)
	}
	if !bytes.Equal(decompressed, raw) {
		t.Fatalf("decompressed bytes mismatch")
	}

	// 2. 拒绝 PDF（即使伪装为 .txt 或 .pdf）
	pdfBytes := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")
	if _, err := ProcessTextFile("spec.pdf", pdfBytes, DefaultMaxTextUploadBytes); !errors.Is(err, ErrUnsupportedTextFile) {
		t.Fatalf("expected PDF filename rejected with ErrUnsupportedTextFile, got %v", err)
	}
	if _, err := ProcessTextFile("fake.txt", pdfBytes, DefaultMaxTextUploadBytes); !errors.Is(err, ErrUnsupportedTextFile) {
		t.Fatalf("expected PDF content disguised as .txt rejected with ErrUnsupportedTextFile, got %v", err)
	}

	// 3. 拒绝含空字节（\x00）的二进制文件
	binBytes := []byte("hello\x00world\x01\x02")
	if _, err := ProcessTextFile("dump.log", binBytes, DefaultMaxTextUploadBytes); !errors.Is(err, ErrUnsupportedTextFile) {
		t.Fatalf("expected NUL-containing binary rejected, got %v", err)
	}

	// 4. 拒绝非法 UTF-8
	invalidUTF8 := []byte{0xff, 0xfe, 0xfd, 'a', 'b'}
	if _, err := ProcessTextFile("broken.txt", invalidUTF8, DefaultMaxTextUploadBytes); !errors.Is(err, ErrUnsupportedTextFile) {
		t.Fatalf("expected invalid UTF-8 rejected, got %v", err)
	}

	// 5. 拒绝超限文本
	if _, err := ProcessTextFile("huge.log", bytes.Repeat([]byte("a"), 1025), 1024); !errors.Is(err, ErrTextTooLarge) {
		t.Fatalf("expected ErrTextTooLarge, got %v", err)
	}

	// 6. 拒绝空文件
	if _, err := ProcessTextFile("empty.txt", nil, DefaultMaxTextUploadBytes); !errors.Is(err, ErrEmptyFile) {
		t.Fatalf("expected ErrEmptyFile, got %v", err)
	}
}

func TestServiceUploadTextGzipDedupAndCleanup(t *testing.T) {
	pool, wsID, sourceID := setupAttachmentTestDB(t)
	uploadDir := t.TempDir()

	svc, err := New(pool, Config{
		Dir:                uploadDir,
		TTL:                24 * time.Hour,
		MaxTotalBytes:      4096, // 仅按 gzip 物理落盘字节数控制配额
		MaxTextUploadBytes: 2 << 20,
	})
	if err != nil {
		t.Fatalf("New service: %v", err)
	}

	ctx := context.Background()
	var sb strings.Builder
	for i := 1; i <= 250; i++ {
		fmt.Fprintf(&sb, "line %03d: repeated application log payload for gzip compression ratio verification\n", i)
	}
	rawText := []byte(sb.String())

	// 1. 上传纯文本日志，校验磁盘上是 gzip 压缩文件，而 ReadBytes 返回解压后的原文
	first, err := svc.Upload(ctx, UploadParams{
		WorkspaceID:        wsID,
		UploadedBySourceID: sourceID,
		FileName:           "app.log",
		RawData:            rawText,
	})
	if err != nil {
		t.Fatalf("Upload text failed: %v", err)
	}
	if first.Kind != KindText || first.StorageEncoding != EncodingGzip {
		t.Fatalf("unexpected kind=%q encoding=%q", first.Kind, first.StorageEncoding)
	}
	if first.FileName != "app.log" || first.LineCount != 250 {
		t.Fatalf("unexpected file_name=%q line_count=%d", first.FileName, first.LineCount)
	}
	if first.SizeBytes != int64(len(rawText)) {
		t.Fatalf("expected size_bytes=%d, got %d", len(rawText), first.SizeBytes)
	}
	if first.StoredSizeBytes <= 0 || first.StoredSizeBytes >= first.SizeBytes/4 {
		t.Fatalf("expected compressed stored_size_bytes (%d) << size_bytes (%d)", first.StoredSizeBytes, first.SizeBytes)
	}

	diskPath := filepath.Join(uploadDir, filepath.FromSlash(first.RelativePath))
	diskInfo, err := os.Stat(diskPath)
	if err != nil {
		t.Fatalf("stat gzip disk file: %v", err)
	}
	if diskInfo.Size() != first.StoredSizeBytes {
		t.Fatalf("disk size %d != stored_size_bytes %d", diskInfo.Size(), first.StoredSizeBytes)
	}

	readAtt, readBytes, err := svc.ReadBytes(ctx, wsID, first.ID)
	if err != nil {
		t.Fatalf("ReadBytes failed: %v", err)
	}
	if readAtt.FileName != "app.log" || !bytes.Equal(readBytes, rawText) {
		t.Fatalf("ReadBytes content mismatch")
	}

	// 2. 同工作区重复上传同内容文本应复用同一物理文件
	second, err := svc.Upload(ctx, UploadParams{
		WorkspaceID:        wsID,
		UploadedBySourceID: sourceID,
		FileName:           "copy.log",
		RawData:            rawText,
	})
	if err != nil {
		t.Fatalf("second Upload failed: %v", err)
	}
	if second.RelativePath != first.RelativePath {
		t.Fatalf("expected dedup relative_path %q, got %q", first.RelativePath, second.RelativePath)
	}
	if second.FileName != "copy.log" {
		t.Fatalf("expected second record to keep its own filename copy.log, got %q", second.FileName)
	}

	// 3. 仅第一条过期被清理时，物理文件仍被 second 引用，不可删除
	_, err = pool.Exec(ctx, `UPDATE workspace_attachments SET expires_at = $2 WHERE id = $1`, first.ID, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatalf("expire first: %v", err)
	}
	if _, err := svc.RunCleanup(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("RunCleanup(first): %v", err)
	}
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatalf("expected shared gzip file to remain while second is active: %v", err)
	}
	if _, _, err := svc.ReadBytes(ctx, wsID, first.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected first to return ErrExpired, got %v", err)
	}
	if _, data2, err := svc.ReadBytes(ctx, wsID, second.ID); err != nil || !bytes.Equal(data2, rawText) {
		t.Fatalf("expected second still readable, err=%v", err)
	}

	// 4. 第二条也过期后，物理文件被彻底删除
	_, err = pool.Exec(ctx, `UPDATE workspace_attachments SET expires_at = $2 WHERE id = $1`, second.ID, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatalf("expire second: %v", err)
	}
	if _, err := svc.RunCleanup(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("RunCleanup(second): %v", err)
	}
	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		t.Fatalf("expected shared gzip file removed after all refs expired, stat err=%v", err)
	}
}
