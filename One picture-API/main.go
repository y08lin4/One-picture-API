package main

import (
	"encoding/json"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ---------------- 随机图片 ----------------
func getRandomFile(folder string) string {
	files, _ := filepath.Glob(folder + "/*")
	if len(files) == 0 {
		return ""
	}
	rand.Seed(time.Now().UnixNano())
	file := files[rand.Intn(len(files))]
	_, filename := filepath.Split(file)
	return filename
}

func randomImageJSONHandler(folder, urlPrefix string, counter *Counter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := getRandomFile(folder)
		if filename == "" {
			http.Error(w, "No images", 404)
			return
		}
		counter.Add(urlPrefix)
		resp := map[string]string{"url": urlPrefix + "/" + filename}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		log.Println("JSON API:", filename)
	}
}

func randomImageRedirectHandler(folder string, key string, counter *Counter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		files, _ := filepath.Glob(folder + "/*")
		if len(files) == 0 {
			http.Error(w, "No images", 404)
			return
		}
		rand.Seed(time.Now().UnixNano())
		file := files[rand.Intn(len(files))]
		counter.Add(key)
		log.Println("Redirect API:", file)
		http.ServeFile(w, r, file)
	}
}

// ---------------- 安全静态文件 ----------------
func safeFileServer(prefix, folder string) http.Handler {
	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(folder, r.URL.Path)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
	}))
}

// ---------------- Token 上传 ----------------
func loadTokens(filename string) map[string]bool {
	data, err := os.ReadFile(filename)
	if err != nil {
		log.Println("加载 tokens.json 失败:", err)
		return nil
	}
	var obj struct {
		Tokens []string `json:"tokens"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		log.Println("解析 tokens.json 失败:", err)
		return nil
	}
	tmap := make(map[string]bool)
	for _, t := range obj.Tokens {
		tmap[t] = true
	}
	return tmap
}

func uploadHandler(tokens map[string]bool, counter *Counter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", 405)
			return
		}
		token := r.FormValue("token")
		if !tokens[token] {
			http.Error(w, "Unauthorized", 401)
			return
		}

		category := r.FormValue("category")
		if category != "m" {
			category = "web"
		}
		dir := filepath.Join("images", category)
		os.MkdirAll(dir, 0755)

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "Bad Request", 400)
			return
		}
		defer file.Close()

		dstPath := filepath.Join(dir, header.Filename)
		dst, err := os.Create(dstPath)
		if err != nil {
			http.Error(w, "Server Error", 500)
			return
		}
		defer dst.Close()

		_, err = io.Copy(dst, file)
		if err != nil {
			http.Error(w, "Server Error", 500)
			return
		}

		counter.Add("upload")
		resp := map[string]string{"status": "ok", "path": dstPath}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		log.Println("上传成功:", dstPath)
	}
}

// ---------------- 统计 ----------------
type StatItem struct {
	Today int `json:"today"`
	Total int `json:"total"`
}

type Counter struct {
	mu    sync.Mutex
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
	c.mu.Lock()
	defer c.mu.Unlock()
	copy := make(map[string]StatItem)
	for k, v := range c.stats {
		copy[k] = *v
	}
	return copy
}

func (c *Counter) ResetToday() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.stats {
		v.Today = 0
	}
}

func (c *Counter) SaveToFile(filename string) {
	data, _ := json.MarshalIndent(c.GetStats(), "", "  ")
	os.WriteFile(filename, data, 0644)
}

// ---------------- main ----------------
func main() {
	rand.Seed(time.Now().UnixNano())
	counter := NewCounter()
	tokens := loadTokens("tokens.json")

	mux := http.NewServeMux()

	// 根路径跳转首页
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/public/index.html", http.StatusFound)
	})

	// 随机图 API
	mux.HandleFunc("/api/web", randomImageRedirectHandler("images/web", "redirect_web", counter))
	mux.HandleFunc("/api/m", randomImageRedirectHandler("images/m", "redirect_m", counter))
	mux.HandleFunc("/api/web/json", randomImageJSONHandler("images/web", "/api/web", counter))
	mux.HandleFunc("/api/m/json", randomImageJSONHandler("images/m", "/api/m", counter))

	// 上传 API
	mux.HandleFunc("/api/upload", uploadHandler(tokens, counter))

	// 统计接口
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(counter.GetStats())
	})

	// 静态文件
	mux.Handle("/public/", safeFileServer("/public/", "public"))

	// 404
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// 定期写入 stats.json（每分钟写一次）
	go func() {
		for {
			time.Sleep(time.Minute)
			counter.SaveToFile("stats.json")
		}
	}()

	// 每天零点重置今日统计（北京时间 UTC+8）
	go func() {
		for {
			now := time.Now().UTC().Add(8 * time.Hour)
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Add(-8 * time.Hour)
			time.Sleep(time.Until(next))
			counter.ResetToday()
			log.Println("今日统计已重置")
		}
	}()

	log.Println("Server started at http://localhost:8080")
	http.ListenAndServe(":8080", handler)
}
