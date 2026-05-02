package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenStoreAndLoginRateLimit(t *testing.T) {
	store := NewTokenStore([]string{"secret-token-12345678901234567890"})
	limiter := NewLoginRateLimiter(2, time.Minute, time.Minute)
	sm := NewSessionManager()
	handler := loginHandler(store, sm, false, limiter, nil, false)

	body := `{"token":"wrong"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusUnauthorized {
		t.Fatalf("first bad login status=%d, want %d", rr1.Code, http.StatusUnauthorized)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second bad login status=%d, want %d", rr2.Code, http.StatusTooManyRequests)
	}
}

func TestRequireWriteOriginRejectsCrossSite(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/upload", nil)
	req.Host = "api.example.com"
	req.Header.Set("Origin", "https://evil.example")

	rr := httptest.NewRecorder()
	if requireWriteOrigin(rr, req, []string{"https://admin.example.com"}) {
		t.Fatal("cross-site origin should be rejected")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestPublicCORSAndMethodGuard(t *testing.T) {
	handler := withPublicCORS(withMethods(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, http.MethodGet, http.MethodHead), []string{"*"})

	optionsReq := httptest.NewRequest(http.MethodOptions, "/api/web/json", nil)
	optionsReq.Header.Set("Origin", "https://example.com")
	optionsRR := httptest.NewRecorder()
	handler.ServeHTTP(optionsRR, optionsReq)
	if optionsRR.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status=%d, want %d", optionsRR.Code, http.StatusNoContent)
	}
	if got := optionsRR.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin=%q, want *", got)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/web/json", nil)
	postRR := httptest.NewRecorder()
	handler.ServeHTTP(postRR, postReq)
	if postRR.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d, want %d", postRR.Code, http.StatusMethodNotAllowed)
	}
}

func TestDetectAllowedImageFileRejectsFakeImage(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake.jpg")
	if err := os.WriteFile(fake, []byte("not an image"), 0644); err != nil {
		t.Fatal(err)
	}
	if detectAllowedImageFile(fake) {
		t.Fatal("fake jpg should not pass image detection")
	}
}

func TestUploadGeneratesServerSideFilename(t *testing.T) {
	root := t.TempDir()
	imageBase := filepath.Join(root, "images")
	webDir := filepath.Join(imageBase, "web")
	mobileDir := filepath.Join(imageBase, "m")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mobileDir, 0755); err != nil {
		t.Fatal(err)
	}

	counter := NewCounter()
	pool := NewImagePool(webDir, mobileDir)
	sessions := NewSessionManager()
	tags := NewTagIndex()
	sid, err := sessions.Create()
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("category", "web"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "../../evil.gif")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})

	rr := httptest.NewRecorder()
	uploadHandler(counter, pool, sessions, tags, imageBase, nil, 0).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.Path, "web/") || !strings.HasSuffix(resp.Path, ".gif") {
		t.Fatalf("unexpected generated path: %q", resp.Path)
	}
	if strings.Contains(resp.Path, "evil") || strings.Contains(resp.Path, "..") {
		t.Fatalf("path should not include client filename: %q", resp.Path)
	}
	if _, err := os.Stat(filepath.Join(imageBase, filepath.FromSlash(resp.Path))); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
}

func TestAdminDeleteImageRemovesFilePoolAndTags(t *testing.T) {
	root := t.TempDir()
	imageBase := filepath.Join(root, "images")
	webDir := filepath.Join(imageBase, "web")
	mobileDir := filepath.Join(imageBase, "m")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mobileDir, 0755); err != nil {
		t.Fatal(err)
	}

	fullPath := filepath.Join(webDir, "delete-me.gif")
	if err := os.WriteFile(fullPath, []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"), 0644); err != nil {
		t.Fatal(err)
	}

	pool := NewImagePool(webDir, mobileDir)
	tags := NewTagIndex()
	tags.ReplaceTags("web/delete-me.gif", []string{"old"})
	sm := NewSessionManager()
	sid, err := sm.Create()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/image/delete", strings.NewReader(`{"path":"web/delete-me.gif"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sid})
	rr := httptest.NewRecorder()

	adminDeleteImageHandler(sm, pool, tags, imageBase, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Fatalf("file should be removed, stat err=%v", err)
	}
	if got := tags.GetTags("web/delete-me.gif"); len(got) != 0 {
		t.Fatalf("tags should be removed, got %v", got)
	}
	if files := pool.ListFiles(webDir); len(files) != 0 {
		t.Fatalf("pool should be empty after delete, got %v", files)
	}
}

func TestHealthAndImageStats(t *testing.T) {
	root := t.TempDir()
	imageBase := filepath.Join(root, "images")
	if err := os.MkdirAll(filepath.Join(imageBase, "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imageBase, "web", "a.gif"), []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := imageStats(imageBase, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if stats["imageCount"] != 1 {
		t.Fatalf("imageCount=%v, want 1", stats["imageCount"])
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	healthHandler(NewCounter(), imageBase).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("health status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected health body: %s", rr.Body.String())
	}
}
