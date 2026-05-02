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
	"strconv"
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
	debugErrors = false

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
	mimeCanonicalExt = map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
		"image/gif":  ".gif",
	}
	mimeAllowedExt = map[string]map[string]bool{
		"image/jpeg": {".jpg": true, ".jpeg": true},
		"image/png":  {".png": true},
		"image/webp": {".webp": true},
		"image/gif":  {".gif": true},
	}
)

func isAllowedImageExt(name string) bool {
	return allowedImageExt[strings.ToLower(filepath.Ext(name))]
}

func normalizeDetectedMIME(mime string) string {
	mime = strings.ToLower(mime)
	if idx := strings.Index(mime, ";"); idx > 0 {
		mime = mime[:idx]
	}
	return mime
}

func detectAllowedImageFile(filename string) bool {
	if !isAllowedImageExt(filename) {
		return false
	}

	f, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := f.Read(head)
	mime := normalizeDetectedMIME(http.DetectContentType(head[:n]))
	return extMatchesMIME(filepath.Ext(filename), mime)
}

func extMatchesMIME(ext, mime string) bool {
	ext = strings.ToLower(ext)
	allowed := mimeAllowedExt[mime]
	return allowed != nil && allowed[ext]
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func newImageFilename(ext string) (string, error) {
	token, err := randomHex(16)
	if err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + token + ext, nil
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

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
		if !detectAllowedImageFile(path) {
			log.Printf("跳过非图片文件: %s", path)
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
	if !detectAllowedImageFile(file) {
		return
	}
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

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
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
	if debugErrors && strings.TrimSpace(detail) != "" {
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
func safeFileServer(prefix, folder string, allow func(string, os.FileInfo) bool) http.Handler {
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

		fileReal, err := filepath.EvalSymlinks(fileAbs)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		baseReal, err := filepath.EvalSymlinks(baseAbs)
		if err != nil {
			http.Error(w, "Server Error", http.StatusInternalServerError)
			return
		}
		realAllowedPrefix := baseReal + string(os.PathSeparator)
		if fileReal != baseReal && !strings.HasPrefix(fileReal, realAllowedPrefix) {
			http.NotFound(w, r)
			return
		}
		if fileReal != fileAbs {
			fileAbs = fileReal
			info, err = os.Stat(fileAbs)
			if err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
		}
		if allow != nil && !allow(fileAbs, info) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, fileAbs)
	}))
}

func allowAnyFile(_ string, _ os.FileInfo) bool {
	return true
}

func allowImageFile(path string, _ os.FileInfo) bool {
	return detectAllowedImageFile(path)
}

// ---------------- Token 登录 ----------------
func addTokens(dst *[]string, tokens []string) {
	for _, t := range tokens {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			*dst = append(*dst, trimmed)
		}
	}
}

func splitTokenList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
}

func loadTokens(filename, envTokens string) (*TokenStore, error) {
	allTokens := make([]string, 0)

	var obj struct {
		Tokens []string `json:"tokens"`
	}

	if strings.TrimSpace(filename) != "" {
		data, err := os.ReadFile(filename)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) || strings.TrimSpace(envTokens) == "" {
				return nil, err
			}
		} else if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &obj); err != nil {
				return nil, err
			}
			addTokens(&allTokens, obj.Tokens)
		}
	}

	addTokens(&allTokens, splitTokenList(envTokens))
	store := NewTokenStore(allTokens)
	if store.Len() == 0 {
		return nil, errors.New("no login tokens configured; set OPAPI_TOKENS or create tokens.json from tokens.example.json")
	}
	return store, nil
}

func loginHandler(tokens *TokenStore, sm *SessionManager, cookieSecure bool, limiter *LoginRateLimiter, trustedOrigins []string, trustProxy bool) http.HandlerFunc {
	type loginReq struct {
		Token string `json:"token"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect POST")
			return
		}
		if !requireWriteOrigin(w, r, trustedOrigins) {
			return
		}

		clientKey := clientIP(r, trustProxy)
		if ok, retryAfter := limiter.Allow(clientKey); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			writeAPIError(w, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "登录尝试过于频繁，请稍后再试", retryAfter.String())
			return
		}

		var req loginReq
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "请求参数错误", err.Error())
				return
			}
		} else {
			req.Token = r.FormValue("token")
		}

		token := strings.TrimSpace(req.Token)
		if !tokens.Valid(token) {
			if ok, retryAfter := limiter.Failure(clientKey); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
				writeAPIError(w, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "登录尝试过于频繁，请稍后再试", retryAfter.String())
				return
			}
			writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token 无效", "")
			return
		}
		limiter.Success(clientKey)

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
			Secure:   cookieSecure,
		})

		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "loggedIn": true})
	}
}

func logoutHandler(sm *SessionManager, cookieSecure bool, trustedOrigins []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect POST")
			return
		}
		if !requireWriteOrigin(w, r, trustedOrigins) {
			return
		}
		sid := getSessionID(r)
		if sid != "" {
			sm.Delete(sid)
		}
		clearSessionCookie(w, cookieSecure)
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
func uploadHandler(counter *Counter, pool *ImagePool, sm *SessionManager, tags *TagIndex, imageBaseDir string, trustedOrigins []string, maxStorageBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect POST")
			return
		}
		if !requireWriteOrigin(w, r, trustedOrigins) {
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
		dir := categoryFolder(imageBaseDir, category)
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

		if !isAllowedImageExt(safeName) {
			writeAPIError(w, http.StatusBadRequest, "UNSUPPORTED_EXTENSION", "文件扩展名不支持", filepath.Ext(safeName))
			return
		}

		head := make([]byte, 512)
		n, _ := file.Read(head)
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}
		mime := normalizeDetectedMIME(http.DetectContentType(head[:n]))
		if !allowedImageMIME[mime] {
			writeAPIError(w, http.StatusBadRequest, "UNSUPPORTED_FILE_TYPE", "文件类型不支持", mime)
			return
		}
		if !extMatchesMIME(filepath.Ext(safeName), mime) {
			writeAPIError(w, http.StatusBadRequest, "EXTENSION_MISMATCH", "文件扩展名与图片内容不匹配", filepath.Ext(safeName)+" vs "+mime)
			return
		}

		if header.Size > maxUploadSize {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "文件过大", "")
			return
		}
		if maxStorageBytes > 0 {
			used, err := directorySize(imageBaseDir)
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
				return
			}
			if used+header.Size > maxStorageBytes {
				writeAPIError(w, http.StatusInsufficientStorage, "STORAGE_LIMIT_EXCEEDED", "图片存储空间已达到上限", "")
				return
			}
		}

		var (
			dstPath string
			dst     *os.File
		)
		for i := 0; i < 8; i++ {
			filename, err := newImageFilename(mimeCanonicalExt[mime])
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
				return
			}
			dstPath = filepath.Join(dir, filename)
			dst, err = os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
			if err == nil {
				break
			}
			if !os.IsExist(err) {
				writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
				return
			}
		}
		if dst == nil {
			writeAPIError(w, http.StatusConflict, "FILE_EXISTS", "无法生成唯一文件名", "")
			return
		}
		defer dst.Close()

		written, err := io.Copy(dst, file)
		if err != nil {
			_ = dst.Close()
			_ = os.Remove(dstPath)
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}
		if written > maxUploadSize {
			_ = os.Remove(dstPath)
			writeAPIError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "文件过大", "")
			return
		}

		rel, err := filepath.Rel(imageBaseDir, dstPath)
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

		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "path": rel, "url": "/images/" + rel, "tags": tagList})
	}
}

// ---------------- 统计 ----------------
type StatItem struct {
	Today int `json:"today"`
	Total int `json:"total"`
}

type Counter struct {
	mu     sync.RWMutex
	saveMu sync.Mutex
	stats  map[string]*StatItem
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
	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	data, err := json.MarshalIndent(c.GetStats(), "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filename, data, 0644)
}

func (c *Counter) LoadFromFile(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	var disk map[string]StatItem
	if err := json.Unmarshal(data, &disk); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range disk {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if v.Today < 0 {
			v.Today = 0
		}
		if v.Total < 0 {
			v.Total = 0
		}
		c.stats[key] = &StatItem{Today: v.Today, Total: v.Total}
	}
	return nil
}

// ---------------- main ----------------
func main() {
	rand.Seed(time.Now().UnixNano())
	cfg := LoadConfig()
	debugErrors = cfg.DebugErrors

	counter := NewCounter()
	if err := counter.LoadFromFile(cfg.StatsFile); err != nil {
		log.Printf("加载 %s 失败: %v", cfg.StatsFile, err)
	}

	webFolder := categoryFolder(cfg.ImageBaseDir, "web")
	mobileFolder := categoryFolder(cfg.ImageBaseDir, "m")
	pool := NewImagePool(webFolder, mobileFolder)
	tags := NewTagIndex()
	if err := tags.LoadFromFile(cfg.TagsFile); err != nil {
		log.Printf("加载 %s 失败: %v", cfg.TagsFile, err)
	}
	tags.PruneMissing(cfg.ImageBaseDir)
	sessions := NewSessionManager()
	loginLimiter := NewLoginRateLimiter(cfg.LoginMaxFails, cfg.LoginWindow, cfg.LoginBlock)
	tokens, err := loadTokens(cfg.TokensFile, os.Getenv("OPAPI_TOKENS"))
	if err != nil {
		log.Fatalf("加载登录 token 失败: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", withMethods(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/public/index.html", http.StatusFound)
	}, http.MethodGet, http.MethodHead))

	mux.HandleFunc("/api/web", withMethods(randomImageRedirectWithTagHandler(webFolder, "web", cfg.ImageBaseDir, "redirect_web", counter, pool, tags), http.MethodGet, http.MethodHead))
	mux.HandleFunc("/api/m", withMethods(randomImageRedirectWithTagHandler(mobileFolder, "m", cfg.ImageBaseDir, "redirect_m", counter, pool, tags), http.MethodGet, http.MethodHead))
	mux.HandleFunc("/api/web/json", withPublicCORS(withMethods(randomImageJSONWithTagHandler(webFolder, "web", cfg.ImageBaseDir, "json_web", counter, pool, tags), http.MethodGet, http.MethodHead), cfg.PublicCORS))
	mux.HandleFunc("/api/m/json", withPublicCORS(withMethods(randomImageJSONWithTagHandler(mobileFolder, "m", cfg.ImageBaseDir, "json_m", counter, pool, tags), http.MethodGet, http.MethodHead), cfg.PublicCORS))

	mux.HandleFunc("/api/login", loginHandler(tokens, sessions, cfg.CookieSecure, loginLimiter, cfg.TrustedOrigins, cfg.TrustProxy))
	mux.HandleFunc("/api/logout", logoutHandler(sessions, cfg.CookieSecure, cfg.TrustedOrigins))
	mux.HandleFunc("/api/auth/status", authStatusHandler(sessions))
	mux.HandleFunc("/api/upload", uploadHandler(counter, pool, sessions, tags, cfg.ImageBaseDir, cfg.TrustedOrigins, cfg.MaxStorageBytes))
	mux.HandleFunc("/api/admin/tags", adminTagsHandler(sessions, tags))
	mux.HandleFunc("/api/admin/images", adminImagesHandler(sessions, pool, tags, cfg.ImageBaseDir))
	mux.HandleFunc("/api/admin/image/tags", adminSetImageTagsHandler(sessions, tags, cfg.ImageBaseDir, cfg.TrustedOrigins))

	mux.HandleFunc("/api/stats", withPublicCORS(withMethods(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, counter.GetStats())
	}, http.MethodGet, http.MethodHead), cfg.PublicCORS))

	mux.Handle("/public/", withMethodsHandler(safeFileServer("/public/", cfg.PublicDir, allowAnyFile), http.MethodGet, http.MethodHead))
	imageStaticHandler := safeFileServer("/images/", cfg.ImageBaseDir, allowImageFile)
	mux.Handle("/images/", withMethodsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		imageStaticHandler.ServeHTTP(w, r)
	}), http.MethodGet, http.MethodHead))

	server := &http.Server{
		Addr: cfg.Addr,
		Handler: chain(
			mux,
			securityHeaders,
			func(next http.Handler) http.Handler {
				if !cfg.AccessLog {
					return next
				}
				return accessLog(next, cfg.TrustProxy)
			},
		),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		ticker := time.NewTicker(statsFlushInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := counter.SaveToFile(cfg.StatsFile); err != nil {
				log.Printf("写入 %s 失败: %v", cfg.StatsFile, err)
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
			if err := tags.SaveToFile(cfg.TagsFile); err != nil {
				log.Printf("写入 %s 失败: %v", cfg.TagsFile, err)
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
			if err := counter.SaveToFile(cfg.StatsFile); err != nil {
				log.Printf("重置后写入 %s 失败: %v", cfg.StatsFile, err)
			}
			log.Println("今日统计已重置")
		}
	}()

	go func() {
		<-stop
		log.Println("收到退出信号，正在保存统计并关闭服务...")
		if err := counter.SaveToFile(cfg.StatsFile); err != nil {
			log.Printf("退出前写入 %s 失败: %v", cfg.StatsFile, err)
		}
		if err := tags.SaveToFile(cfg.TagsFile); err != nil {
			log.Printf("退出前写入 %s 失败: %v", cfg.TagsFile, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Println("服务关闭失败:", err)
		}
	}()

	log.Printf("Server started at %s", displayListenAddr(cfg.Addr))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("服务启动失败: %v", err)
	}
}
