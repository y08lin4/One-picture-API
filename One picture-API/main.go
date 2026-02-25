package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxUploadSize      = 10 << 20 // 10MB
	statsFlushInterval = 30 * time.Second
	tagsFlushInterval  = 2 * time.Second
	sessionTTL         = 24 * time.Hour
	sessionCookieName  = "opapi_session"
)

var (
	allowedImageExt = map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".webp": true,
		".gif":  true,
	}
	allowedImageMIME = map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
		"image/gif":  true,
	}
)

// ---------------- 图片缓存池 ----------------
type ImagePool struct {
	mu    sync.RWMutex
	files map[string][]string
}

func NewImagePool(folders ...string) *ImagePool {
	p := &ImagePool{files: make(map[string][]string)}
	for _, folder := range folders {
		if err := p.Refresh(folder); err != nil {
			log.Printf("图片目录预加载失败: %s, err=%v", folder, err)
		}
	}
	return p
}

func (p *ImagePool) Refresh(folder string) error {
	paths, err := filepath.Glob(filepath.Join(folder, "*"))
	if err != nil {
		return err
	}
	list := make([]string, 0, len(paths))
	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil || info.IsDir() {
			continue
		}
		list = append(list, path)
	}

	p.mu.Lock()
	p.files[folder] = list
	p.mu.Unlock()
	return nil
}

func (p *ImagePool) RandomFile(folder string) (string, error) {
	p.mu.RLock()
	list := p.files[folder]
	p.mu.RUnlock()
	if len(list) == 0 {
		return "", errors.New("no images")
	}
	return list[rand.Intn(len(list))], nil
}

func (p *ImagePool) AddFile(folder, file string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	list := p.files[folder]
	for _, f := range list {
		if f == file {
			return
		}
	}
	p.files[folder] = append(list, file)
}

func (p *ImagePool) ListFiles(folder string) []string {
	p.mu.RLock()
	list := p.files[folder]
	res := make([]string, len(list))
	copy(res, list)
	p.mu.RUnlock()
	return res
}

// ---------------- 会话管理 ----------------
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]time.Time
}

func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: make(map[string]time.Time)}
}

func (sm *SessionManager) Create() (string, error) {
	buf := make([]byte, 32)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	sid := hex.EncodeToString(buf)

	sm.mu.Lock()
	sm.sessions[sid] = time.Now().Add(sessionTTL)
	sm.mu.Unlock()
	return sid, nil
}

func (sm *SessionManager) Validate(sid string) bool {
	if strings.TrimSpace(sid) == "" {
		return false
	}

	sm.mu.RLock()
	expireAt, ok := sm.sessions[sid]
	sm.mu.RUnlock()
	if !ok {
		return false
	}

	if time.Now().After(expireAt) {
		sm.Delete(sid)
		return false
	}
	return true
}

func (sm *SessionManager) Delete(sid string) {
	sm.mu.Lock()
	delete(sm.sessions, sid)
	sm.mu.Unlock()
}

func (sm *SessionManager) CleanupExpired() {
	now := time.Now()
	sm.mu.Lock()
	for sid, exp := range sm.sessions {
		if now.After(exp) {
			delete(sm.sessions, sid)
		}
	}
	sm.mu.Unlock()
}

func getSessionID(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Println("JSON 响应写入失败:", err)
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message, detail string) {
	resp := map[string]any{
		"code":    code,
		"message": message,
	}
	if strings.TrimSpace(detail) != "" {
		resp["detail"] = detail
	}
	writeJSON(w, status, resp)
}

func requireLogin(sm *SessionManager, w http.ResponseWriter, r *http.Request) bool {
	sid := getSessionID(r)
	if !sm.Validate(sid) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "请先登录", "session invalid or expired")
		return false
	}
	return true
}

// ---------------- 安全静态文件 ----------------
func safeFileServer(prefix, folder string) http.Handler {
	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relPath := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
		fullPath := filepath.Join(folder, relPath)

		baseAbs, err := filepath.Abs(folder)
		if err != nil {
			http.Error(w, "Server Error", http.StatusInternalServerError)
			return
		}
		fileAbs, err := filepath.Abs(fullPath)
		if err != nil {
			http.Error(w, "Server Error", http.StatusInternalServerError)
			return
		}

		allowedPrefix := baseAbs + string(os.PathSeparator)
		if fileAbs != baseAbs && !strings.HasPrefix(fileAbs, allowedPrefix) {
			http.NotFound(w, r)
			return
		}

		info, err := os.Stat(fileAbs)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, fileAbs)
	}))
}

// ---------------- Token 登录 ----------------
func loadTokens(filename string) (map[string]bool, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var obj struct {
		Tokens []string `json:"tokens"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	tmap := make(map[string]bool)
	for _, t := range obj.Tokens {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			tmap[trimmed] = true
		}
	}
	return tmap, nil
}

func loginHandler(tokens map[string]bool, sm *SessionManager) http.HandlerFunc {
	type loginReq struct {
		Token string `json:"token"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect POST")
			return
		}

		var req loginReq
		if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "请求参数错误", err.Error())
				return
			}
		} else {
			req.Token = r.FormValue("token")
		}

		token := strings.TrimSpace(req.Token)
		if !tokens[token] {
			writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token 无效", "")
			return
		}

		sid, err := sm.Create()
		if err != nil {
			log.Println("创建会话失败:", err)
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    sid,
			Path:     "/",
			HttpOnly: true,
			MaxAge:   int(sessionTTL.Seconds()),
			SameSite: http.SameSiteLaxMode,
		})

		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "loggedIn": true})
	}
}

func logoutHandler(sm *SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect POST")
			return
		}
		sid := getSessionID(r)
		if sid != "" {
			sm.Delete(sid)
		}
		clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "loggedIn": false})
	}
}

func authStatusHandler(sm *SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		loggedIn := sm.Validate(getSessionID(r))
		writeJSON(w, http.StatusOK, map[string]any{"loggedIn": loggedIn})
	}
}

// ---------------- 上传 ----------------
func uploadHandler(counter *Counter, pool *ImagePool, sm *SessionManager, tags *TagIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect POST")
			return
		}
		if !requireLogin(sm, w, r) {
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+(1<<20))
		if err := r.ParseMultipartForm(maxUploadSize + (1 << 20)); err != nil {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "文件过大", err.Error())
			return
		}

		category := r.FormValue("category")
		if category != "m" {
			category = "web"
		}
		dir := filepath.Join("images", category)
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Println("创建上传目录失败:", err)
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "缺少上传文件", err.Error())
			return
		}
		defer file.Close()

		safeName := filepath.Base(header.Filename)
		if safeName == "." || safeName == string(os.PathSeparator) || strings.TrimSpace(safeName) == "" {
			writeAPIError(w, http.StatusBadRequest, "INVALID_FILENAME", "文件名无效", "")
			return
		}

		ext := strings.ToLower(filepath.Ext(safeName))
		if !allowedImageExt[ext] {
			writeAPIError(w, http.StatusBadRequest, "UNSUPPORTED_EXTENSION", "文件扩展名不支持", ext)
			return
		}

		head := make([]byte, 512)
		n, _ := file.Read(head)
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}
		mime := strings.ToLower(http.DetectContentType(head[:n]))
		if idx := strings.Index(mime, ";"); idx > 0 {
			mime = mime[:idx]
		}
		if !allowedImageMIME[mime] {
			writeAPIError(w, http.StatusBadRequest, "UNSUPPORTED_FILE_TYPE", "文件类型不支持", mime)
			return
		}

		if header.Size > maxUploadSize {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "文件过大", "")
			return
		}

		dstPath := filepath.Join(dir, safeName)
		if _, err := os.Stat(dstPath); err == nil {
			writeAPIError(w, http.StatusConflict, "FILE_EXISTS", "文件已存在", safeName)
			return
		} else if !os.IsNotExist(err) {
			log.Println("检查文件是否存在失败:", err)
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}

		dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}
		defer dst.Close()

		written, err := io.Copy(dst, file)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}
		if written > maxUploadSize {
			_ = os.Remove(dstPath)
			writeAPIError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "文件过大", "")
			return
		}

		rel, err := filepath.Rel("images", dstPath)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}
		rel = filepath.ToSlash(rel)
		tagList := parseTagsInput(r.FormValue("tags"))
		if len(tagList) > 0 {
			tags.ReplaceTags(rel, tagList)
		}

		counter.Add("upload")
		pool.AddFile(dir, dstPath)
		tags.InvalidateDefaultPoolCache()

		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "path": dstPath, "tags": tagList})
	}
}

// ---------------- 统计 ----------------
type StatItem struct {
	Today int `json:"today"`
	Total int `json:"total"`
}

type Counter struct {
	mu    sync.RWMutex
	stats map[string]*StatItem
}

func NewCounter() *Counter {
	return &Counter{stats: make(map[string]*StatItem)}
}

func (c *Counter) Add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.stats[key]; !ok {
		c.stats[key] = &StatItem{}
	}
	c.stats[key].Today++
	c.stats[key].Total++
}

func (c *Counter) GetStats() map[string]StatItem {
	c.mu.RLock()
	defer c.mu.RUnlock()
	copyStats := make(map[string]StatItem)
	for k, v := range c.stats {
		copyStats[k] = *v
	}
	return copyStats
}

func (c *Counter) ResetToday() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.stats {
		v.Today = 0
	}
}

func (c *Counter) SaveToFile(filename string) error {
	data, err := json.MarshalIndent(c.GetStats(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// ---------------- main ----------------
func main() {
	rand.Seed(time.Now().UnixNano())
	counter := NewCounter()
	pool := NewImagePool("images/web", "images/m")
	tags := NewTagIndex()
	if err := tags.LoadFromFile(tagIndexFilename); err != nil {
		log.Printf("加载 %s 失败: %v", tagIndexFilename, err)
	}
	tags.PruneMissing("images")
	sessions := NewSessionManager()
	tokens, err := loadTokens("tokens.json")
	if err != nil {
		log.Fatalf("加载 tokens.json 失败: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/public/index.html", http.StatusFound)
	})

	mux.HandleFunc("/api/web", randomImageRedirectWithTagHandler("images/web", "redirect_web", counter, pool, tags))
	mux.HandleFunc("/api/m", randomImageRedirectWithTagHandler("images/m", "redirect_m", counter, pool, tags))
	mux.HandleFunc("/api/web/json", randomImageJSONWithTagHandler("images/web", "json_web", counter, pool, tags))
	mux.HandleFunc("/api/m/json", randomImageJSONWithTagHandler("images/m", "json_m", counter, pool, tags))

	mux.HandleFunc("/api/login", loginHandler(tokens, sessions))
	mux.HandleFunc("/api/logout", logoutHandler(sessions))
	mux.HandleFunc("/api/auth/status", authStatusHandler(sessions))
	mux.HandleFunc("/api/upload", uploadHandler(counter, pool, sessions, tags))
	mux.HandleFunc("/api/admin/tags", adminTagsHandler(sessions, tags))
	mux.HandleFunc("/api/admin/images", adminImagesHandler(sessions, pool, tags))
	mux.HandleFunc("/api/admin/image/tags", adminSetImageTagsHandler(sessions, tags))

	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, counter.GetStats())
	})

	mux.Handle("/public/", safeFileServer("/public/", "public"))
	imageStaticHandler := safeFileServer("/images/", "images")
	mux.Handle("/images/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		imageStaticHandler.ServeHTTP(w, r)
	}))

	server := &http.Server{Addr: ":8080", Handler: mux}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		ticker := time.NewTicker(statsFlushInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := counter.SaveToFile("stats.json"); err != nil {
				log.Println("写入 stats.json 失败:", err)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(tagsFlushInterval)
		defer ticker.Stop()
		for range ticker.C {
			if !tags.IsDirty() {
				continue
			}
			if err := tags.SaveToFile(tagIndexFilename); err != nil {
				log.Printf("写入 %s 失败: %v", tagIndexFilename, err)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			sessions.CleanupExpired()
		}
	}()

	go func() {
		for {
			now := time.Now().UTC().Add(8 * time.Hour)
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Add(-8 * time.Hour)
			time.Sleep(time.Until(next))
			counter.ResetToday()
			if err := counter.SaveToFile("stats.json"); err != nil {
				log.Println("重置后写入 stats.json 失败:", err)
			}
			log.Println("今日统计已重置")
		}
	}()

	go func() {
		<-stop
		log.Println("收到退出信号，正在保存统计并关闭服务...")
		if err := counter.SaveToFile("stats.json"); err != nil {
			log.Println("退出前写入 stats.json 失败:", err)
		}
		if err := tags.SaveToFile(tagIndexFilename); err != nil {
			log.Printf("退出前写入 %s 失败: %v", tagIndexFilename, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Println("服务关闭失败:", err)
		}
	}()

	log.Println("Server started at http://localhost:8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("服务启动失败: %v", err)
	}
}
