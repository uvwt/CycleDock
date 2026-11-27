package attachments

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/testdb"
)

func makeTestPNG(t *testing.T, w, h int, alpha uint8, seed byte) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: byte((x*7 + int(seed)*13) % 256),
				G: byte((y*11 + int(seed)*5) % 256),
				B: byte(((x ^ y) + int(seed)*17) % 256),
				A: alpha,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestDetectImageFormatRejectsSVGAndUnknown(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if _, _, ok := DetectImageFormat(svg); ok {
		t.Fatal("expected SVG to be rejected")
	}
	html := []byte(`<!DOCTYPE html><html><body>hi</body></html>`)
	if _, _, ok := DetectImageFormat(html); ok {
		t.Fatal("expected HTML to be rejected")
	}
	xml := []byte(`<?xml version="1.0"?><svg></svg>`)
	if _, _, ok := DetectImageFormat(xml); ok {
		t.Fatal("expected XML/SVG to be rejected")
	}
	pngBytes := makeTestPNG(t, 16, 16, 255, 1)
	mimeType, ext, ok := DetectImageFormat(pngBytes)
	if !ok || mimeType != "image/png" || ext != ".png" {
		t.Fatalf("expected image/png .png, got ok=%v mime=%q ext=%q", ok, mimeType, ext)
	}
}

func TestProcessImageResizesAndCompressesLargeOpaquePNG(t *testing.T) {
	raw := makeTestPNG(t, 2400, 1200, 255, 3)
	processed, err := ProcessImage(raw, DefaultMaxDimension)
	if err != nil {
		t.Fatalf("ProcessImage failed: %v", err)
	}
	if processed.Width != 1600 || processed.Height != 800 {
		t.Fatalf("expected 1600x800, got %dx%d", processed.Width, processed.Height)
	}
	if processed.MimeType != "image/jpeg" || processed.Ext != ".jpg" {
		t.Fatalf("expected opaque PNG to be compressed as JPEG, got %s (%s)", processed.MimeType, processed.Ext)
	}
	if processed.SizeBytes <= 0 {
		t.Fatalf("expected non-empty compressed JPEG, got %d", processed.SizeBytes)
	}
	// 对比同尺寸照片/噪声图的 PNG 与 JPEG 体积：验证真实随机像素图压缩后显著小于原 PNG。
	noisy := image.NewNRGBA(image.Rect(0, 0, 1800, 900))
	var state uint32 = 0x12345678
	for y := 0; y < 900; y++ {
		for x := 0; x < 1800; x++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			noisy.SetNRGBA(x, y, color.NRGBA{
				R: byte(state),
				G: byte(state >> 8),
				B: byte(state >> 16),
				A: 255,
			})
		}
	}
	var noisyBuf bytes.Buffer
	if err := png.Encode(&noisyBuf, noisy); err != nil {
		t.Fatalf("encode noisy png: %v", err)
	}
	noisyProcessed, err := ProcessImage(noisyBuf.Bytes(), DefaultMaxDimension)
	if err != nil {
		t.Fatalf("ProcessImage noisy failed: %v", err)
	}
	if noisyProcessed.SizeBytes >= int64(noisyBuf.Len())/3 {
		t.Fatalf("expected noisy PNG (%d) compressed by >3x as JPEG, got %d", noisyBuf.Len(), noisyProcessed.SizeBytes)
	}
}

func TestProcessImagePreservesAlphaAsPNG(t *testing.T) {
	raw := makeTestPNG(t, 400, 200, 128, 7)
	processed, err := ProcessImage(raw, DefaultMaxDimension)
	if err != nil {
		t.Fatalf("ProcessImage failed: %v", err)
	}
	if processed.MimeType != "image/png" {
		t.Fatalf("expected transparent image to remain PNG, got %s", processed.MimeType)
	}
	if processed.Width != 400 || processed.Height != 200 {
		t.Fatalf("expected 400x200, got %dx%d", processed.Width, processed.Height)
	}
}

func TestServiceUploadDedupAndCleanupTTLandCapacity(t *testing.T) {
	testdb.ForEach(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()

		wsID := uuid.New()
		wsOtherID := uuid.New()
		srcID := uuid.New()
		srcOtherID := uuid.New()
		if _, err := db.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, 'WS1'), ($2, 'WS2')`, wsID, wsOtherID); err != nil {
			t.Fatalf("insert workspaces: %v", err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO sources (id, workspace_id, kind, name) VALUES ($1, $2, 'human', 'U1'), ($3, $4, 'human', 'U2')`, srcID, wsID, srcOtherID, wsOtherID); err != nil {
			t.Fatalf("insert sources: %v", err)
		}

		uploadDir := t.TempDir()
		svc, err := New(db, Config{
			Dir:           uploadDir,
			TTL:           24 * time.Hour,
			MaxTotalBytes: 1 << 20, // 1 MiB
		})
		if err != nil {
			t.Fatalf("New service: %v", err)
		}

		img1 := makeTestPNG(t, 200, 150, 255, 11)
		att1, err := svc.Upload(ctx, UploadParams{
			WorkspaceID:        wsID,
			UploadedBySourceID: srcID,
			RawData:            img1,
		})
		if err != nil {
			t.Fatalf("Upload 1: %v", err)
		}
		// 同工作区相同内容应复用到同一个物理文件。
		att1Dup, err := svc.Upload(ctx, UploadParams{
			WorkspaceID:        wsID,
			UploadedBySourceID: srcID,
			RawData:            img1,
		})
		if err != nil {
			t.Fatalf("Upload 1 dup: %v", err)
		}
		if att1.RelativePath != att1Dup.RelativePath {
			t.Fatalf("expected same workspace dedup to reuse relative path: %q vs %q", att1.RelativePath, att1Dup.RelativePath)
		}
		// 跨工作区访问必须拒绝（NotFound）。
		if _, err := svc.Open(ctx, wsOtherID, att1.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected cross-workspace Open to return ErrNotFound, got %v", err)
		}

		// 验证 TTL 过期清理：仅让 att1 过期，由于 att1Dup 仍引用物理文件，磁盘文件应保留；
		// 两条都过期后磁盘文件应被物理删除，且 Open 返回 ErrExpired。
		pastHour := time.Now().UTC().Add(-time.Hour)
		if _, err := db.Exec(ctx, `UPDATE workspace_attachments SET expires_at = $2 WHERE id = $1`, att1.ID, pastHour); err != nil {
			t.Fatalf("expire att1: %v", err)
		}
		stats, err := svc.RunCleanup(ctx, time.Now().UTC())
		if err != nil {
			t.Fatalf("cleanup 1: %v", err)
		}
		if stats.ExpiredRows != 1 || stats.DeletedFiles != 0 {
			t.Fatalf("expected 1 expired row and 0 deleted files (still referenced), got %+v", stats)
		}
		if _, err := svc.Open(ctx, wsID, att1.ID); !errors.Is(err, ErrExpired) {
			t.Fatalf("expected expired att1 Open to return ErrExpired, got %v", err)
		}
		if opened, err := svc.Open(ctx, wsID, att1Dup.ID); err != nil {
			t.Fatalf("expected att1Dup still readable, got %v", err)
		} else {
			_ = opened.File.Close()
		}

		if _, err := db.Exec(ctx, `UPDATE workspace_attachments SET expires_at = $2 WHERE id = $1`, att1Dup.ID, pastHour); err != nil {
			t.Fatalf("expire att1Dup: %v", err)
		}
		stats, err = svc.RunCleanup(ctx, time.Now().UTC())
		if err != nil {
			t.Fatalf("cleanup 2: %v", err)
		}
		if stats.ExpiredRows != 1 || stats.DeletedFiles != 1 {
			t.Fatalf("expected 1 expired row and 1 deleted file, got %+v", stats)
		}
		if _, statErr := os.Stat(filepath.Join(uploadDir, filepath.FromSlash(att1.RelativePath))); !os.IsNotExist(statErr) {
			t.Fatalf("expected physical file removed after all refs expired, stat err=%v", statErr)
		}

		// 验证总容量上限 LRU 淘汰：上传两张不同图片，把容量上限设为单张略大但小于两张之和。
		imgA := makeTestPNG(t, 500, 500, 255, 21)
		imgB := makeTestPNG(t, 500, 500, 255, 42)
		attA, err := svc.Upload(ctx, UploadParams{WorkspaceID: wsID, UploadedBySourceID: srcID, RawData: imgA})
		if err != nil {
			t.Fatalf("upload A: %v", err)
		}
		// 确保 A 的 created_at 明显早于 B。
		if _, err := db.Exec(ctx, `UPDATE workspace_attachments SET created_at = $2 WHERE id = $1`, attA.ID, time.Now().UTC().Add(-10*time.Minute)); err != nil {
			t.Fatalf("backdate A: %v", err)
		}
		attB, err := svc.Upload(ctx, UploadParams{WorkspaceID: wsID, UploadedBySourceID: srcID, RawData: imgB})
		if err != nil {
			t.Fatalf("upload B: %v", err)
		}

		svc.maxTotalBytes = max(attA.SizeBytes, attB.SizeBytes) + 10
		stats, err = svc.RunCleanup(ctx, time.Now().UTC())
		if err != nil {
			t.Fatalf("capacity cleanup: %v", err)
		}
		if stats.EvictedRows != 1 || stats.DeletedFiles != 1 {
			t.Fatalf("expected oldest attachment evicted, got %+v", stats)
		}
		if _, err := svc.Open(ctx, wsID, attA.ID); !errors.Is(err, ErrExpired) {
			t.Fatalf("expected oldest attA to be evicted (ErrExpired), got %v", err)
		}
		if opened, err := svc.Open(ctx, wsID, attB.ID); err != nil {
			t.Fatalf("expected newest attB to remain available, got %v", err)
		} else {
			_ = opened.File.Close()
		}
	})
}
