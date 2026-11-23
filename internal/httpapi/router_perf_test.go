package httpapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouterStaticCacheControlAndGzipCompression(t *testing.T) {
	webDir := t.TempDir()
	assetsDir := filepath.Join(webDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsContent := strings.Repeat("export const answer = 42;\n", 128)
	if err := os.WriteFile(filepath.Join(assetsDir, "index-hash123.js"), []byte(jsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>CycleDock</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "notifications-sw.js"), []byte("self.addEventListener('push', () => {});"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := NewRouter(nil, nil, nil, Config{WebDir: webDir})

	// 1. Vite 带哈希静态资源启用长期 immutable 强缓存与 Gzip 压缩
	assetReq := httptest.NewRequest(http.MethodGet, "/assets/index-hash123.js", nil)
	assetReq.Header.Set("Accept-Encoding", "gzip")
	assetRec := httptest.NewRecorder()
	handler.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("asset status=%d", assetRec.Code)
	}
	if got := assetRec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control=%q", got)
	}
	if got := assetRec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("expected gzip Content-Encoding, got %q", got)
	}
	gzReader, err := gzip.NewReader(assetRec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(gzReader)
	_ = gzReader.Close()
	if err != nil || string(decoded) != jsContent {
		t.Fatalf("decoded asset mismatch: err=%v len=%d", err, len(decoded))
	}

	// 2. SPA 回退的 index.html 与 Service Worker 保持 no-cache，确保发版后立即生效
	for _, p := range []string{"/", "/chat/demo-project/card-1", "/notifications-sw.js"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("path %s status=%d", p, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("path %s Cache-Control=%q, want no-cache", p, got)
		}
	}

	// 3. JSON 接口同样支持 Gzip
	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthReq.Header.Set("Accept-Encoding", "gzip")
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if got := healthRec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("healthz Content-Encoding=%q, want gzip", got)
	}
}

func TestRouterSecurityHeadersAllowHTTPSImages(t *testing.T) {
	handler := NewRouter(nil, nil, nil, Config{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status=%d", rec.Code)
	}

	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"img-src 'self' data: blob: https:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Fatalf("Content-Security-Policy=%q missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "img-src 'self' data: blob: https: http:") || strings.Contains(csp, "img-src *") {
		t.Fatalf("Content-Security-Policy=%q must not allow http: or wildcard images", csp)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy=%q, want no-referrer", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options=%q, want DENY", got)
	}
}
