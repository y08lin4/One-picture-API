package main

import (
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const (
	tagIndexFilename = "tags_index.json"
	maxTagCount      = 10
	hiddenPoolTag    = "hidden"
)

type TagIndex struct {
	mu               sync.RWMutex
	imageTags        map[string][]string
	tagImages        map[string]map[string]struct{}
	defaultPoolCache map[string][]string
	dirty            bool
}

type tagIndexFile struct {
	ImageTags map[string][]string `json:"image_tags"`
}

type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

func NewTagIndex() *TagIndex {
	return &TagIndex{
		imageTags:        make(map[string][]string),
		tagImages:        make(map[string]map[string]struct{}),
		defaultPoolCache: make(map[string][]string),
	}
}

func (ti *TagIndex) LoadFromFile(filename string) error {
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

	var f tagIndexFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}

	cleaned := make(map[string][]string)
	for rel, tags := range f.ImageTags {
		normRel, ok := normalizeRelImagePath(rel)
		if !ok {
			continue
		}
		normTags := normalizeTags(tags)
		if len(normTags) == 0 {
			continue
		}
		cleaned[normRel] = normTags
	}

	ti.mu.Lock()
	ti.imageTags = cleaned
	ti.rebuildTagImagesLocked()
	ti.defaultPoolCache = make(map[string][]string)
	ti.dirty = false
	ti.mu.Unlock()
	return nil
}

func (ti *TagIndex) SaveToFile(filename string) error {
	ti.mu.RLock()
	copyMap := make(map[string][]string, len(ti.imageTags))
	for k, v := range ti.imageTags {
		tmp := make([]string, len(v))
		copy(tmp, v)
		copyMap[k] = tmp
	}
	ti.mu.RUnlock()

	data, err := json.MarshalIndent(tagIndexFile{ImageTags: copyMap}, "", "  ")
	if err != nil {
		return err
	}
	tmpFile := filename + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpFile, filename); err != nil {
		_ = os.Remove(filename)
		if err2 := os.Rename(tmpFile, filename); err2 != nil {
			_ = os.Remove(tmpFile)
			return err2
		}
	}
	ti.mu.Lock()
	ti.dirty = false
	ti.mu.Unlock()
	return nil
}

func (ti *TagIndex) PruneMissing(baseDir string) {
	ti.mu.Lock()
	for rel := range ti.imageTags {
		abs := filepath.Join(baseDir, filepath.FromSlash(rel))
		if info, err := os.Stat(abs); err != nil || info.IsDir() {
			delete(ti.imageTags, rel)
		}
	}
	ti.rebuildTagImagesLocked()
	ti.defaultPoolCache = make(map[string][]string)
	ti.dirty = true
	ti.mu.Unlock()
}

func (ti *TagIndex) ReplaceTags(rel string, tags []string) {
	normRel, ok := normalizeRelImagePath(rel)
	if !ok {
		return
	}
	normTags := normalizeTags(tags)

	ti.mu.Lock()
	if len(normTags) == 0 {
		delete(ti.imageTags, normRel)
	} else {
		ti.imageTags[normRel] = normTags
	}
	ti.rebuildTagImagesLocked()
	ti.defaultPoolCache = make(map[string][]string)
	ti.dirty = true
	ti.mu.Unlock()
}

func (ti *TagIndex) AddTags(rel string, tags []string) {
	normRel, ok := normalizeRelImagePath(rel)
	if !ok {
		return
	}
	add := normalizeTags(tags)
	if len(add) == 0 {
		return
	}

	ti.mu.Lock()
	merged := make(map[string]struct{})
	for _, t := range ti.imageTags[normRel] {
		merged[t] = struct{}{}
	}
	for _, t := range add {
		merged[t] = struct{}{}
	}
	arr := make([]string, 0, len(merged))
	for t := range merged {
		arr = append(arr, t)
	}
	sort.Strings(arr)
	if len(arr) > maxTagCount {
		arr = arr[:maxTagCount]
	}
	ti.imageTags[normRel] = arr
	ti.rebuildTagImagesLocked()
	ti.defaultPoolCache = make(map[string][]string)
	ti.dirty = true
	ti.mu.Unlock()
}

func (ti *TagIndex) GetTags(rel string) []string {
	normRel, ok := normalizeRelImagePath(rel)
	if !ok {
		return nil
	}
	ti.mu.RLock()
	tags := ti.imageTags[normRel]
	res := make([]string, len(tags))
	copy(res, tags)
	ti.mu.RUnlock()
	return res
}

func (ti *TagIndex) GetImagesByTag(tag string) []string {
	norm := normalizeTag(tag)
	if norm == "" {
		return nil
	}
	ti.mu.RLock()
	set := ti.tagImages[norm]
	res := make([]string, 0, len(set))
	for rel := range set {
		res = append(res, rel)
	}
	ti.mu.RUnlock()
	sort.Strings(res)
	return res
}

func (ti *TagIndex) HasTag(rel, tag string) bool {
	normRel, ok := normalizeRelImagePath(rel)
	if !ok {
		return false
	}
	normTag := normalizeTag(tag)
	if normTag == "" {
		return false
	}
	ti.mu.RLock()
	tags := ti.imageTags[normRel]
	for _, t := range tags {
		if t == normTag {
			ti.mu.RUnlock()
			return true
		}
	}
	ti.mu.RUnlock()
	return false
}

func (ti *TagIndex) ListTags() []TagCount {
	ti.mu.RLock()
	list := make([]TagCount, 0, len(ti.tagImages))
	for t, set := range ti.tagImages {
		list = append(list, TagCount{Tag: t, Count: len(set)})
	}
	ti.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count == list[j].Count {
			return list[i].Tag < list[j].Tag
		}
		return list[i].Count > list[j].Count
	})
	return list
}

func (ti *TagIndex) rebuildTagImagesLocked() {
	ti.tagImages = make(map[string]map[string]struct{})
	for rel, tags := range ti.imageTags {
		for _, t := range tags {
			set := ti.tagImages[t]
			if set == nil {
				set = make(map[string]struct{})
				ti.tagImages[t] = set
			}
			set[rel] = struct{}{}
		}
	}
}

func normalizeTag(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			return r
		}
		return -1
	}, raw)
	if len(mapped) > 32 {
		mapped = mapped[:32]
	}
	return strings.Trim(mapped, "-_")
}

func normalizeTags(input []string) []string {
	uniq := make(map[string]struct{})
	for _, t := range input {
		n := normalizeTag(t)
		if n == "" {
			continue
		}
		uniq[n] = struct{}{}
	}
	res := make([]string, 0, len(uniq))
	for t := range uniq {
		res = append(res, t)
	}
	sort.Strings(res)
	if len(res) > maxTagCount {
		res = res[:maxTagCount]
	}
	return res
}

func parseTagsInput(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	return normalizeTags(parts)
}

func normalizeRelImagePath(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	raw = strings.TrimPrefix(raw, "/")
	raw = strings.TrimPrefix(raw, "images/")
	if raw == "" {
		return "", false
	}
	clean := path.Clean("/" + raw)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	if !(strings.HasPrefix(clean, "web/") || strings.HasPrefix(clean, "m/")) {
		return "", false
	}
	return clean, true
}

func (ti *TagIndex) InvalidateDefaultPoolCache() {
	ti.mu.Lock()
	ti.defaultPoolCache = make(map[string][]string)
	ti.dirty = true
	ti.mu.Unlock()
}

func (ti *TagIndex) IsDirty() bool {
	ti.mu.RLock()
	defer ti.mu.RUnlock()
	return ti.dirty
}

func (ti *TagIndex) getOrBuildDefaultPool(folder string, pool *ImagePool, excludeTag string) []string {
	ti.mu.RLock()
	cached, ok := ti.defaultPoolCache[folder]
	if ok {
		res := make([]string, len(cached))
		copy(res, cached)
		ti.mu.RUnlock()
		return res
	}
	ti.mu.RUnlock()

	files := pool.ListFiles(folder)
	if len(files) == 0 {
		return nil
	}
	candidates := make([]string, 0, len(files))
	for _, full := range files {
		rel, err := filepath.Rel("images", full)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if ti.HasTag(rel, excludeTag) {
			continue
		}
		candidates = append(candidates, full)
	}
	if len(candidates) == 0 {
		ti.mu.Lock()
		ti.defaultPoolCache[folder] = []string{}
		ti.mu.Unlock()
		return nil
	}

	ti.mu.Lock()
	ti.defaultPoolCache[folder] = append([]string(nil), candidates...)
	ti.mu.Unlock()
	return candidates
}

func pickTaggedImage(folder, tag string, ti *TagIndex) (string, error) {
	normTag := normalizeTag(tag)
	if normTag == "" {
		return "", errors.New("empty tag")
	}
	folderPrefix := strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(folder, "images")), "/") + "/"
	candidates := make([]string, 0)
	for _, rel := range ti.GetImagesByTag(normTag) {
		if strings.HasPrefix(rel, folderPrefix) {
			full := filepath.Join("images", filepath.FromSlash(rel))
			if info, err := os.Stat(full); err == nil && !info.IsDir() {
				candidates = append(candidates, full)
			}
		}
	}
	if len(candidates) == 0 {
		return "", errors.New("no tagged images")
	}
	return candidates[rand.Intn(len(candidates))], nil
}

func pickRandomFromPoolExcludingTag(folder, excludeTag string, pool *ImagePool, ti *TagIndex) (string, error) {
	candidates := ti.getOrBuildDefaultPool(folder, pool, excludeTag)
	if len(candidates) == 0 {
		return "", errors.New("no images")
	}
	return candidates[rand.Intn(len(candidates))], nil
}

func randomImageJSONWithTagHandler(folder, key string, counter *Counter, pool *ImagePool, ti *TagIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag := r.URL.Query().Get("tag")
		var (
			file string
			err  error
		)
		if normalizeTag(tag) == "" {
			file, err = pickRandomFromPoolExcludingTag(folder, hiddenPoolTag, pool, ti)
		} else {
			file, err = pickTaggedImage(folder, tag, ti)
		}
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "NO_IMAGES", "没有可用图片", err.Error())
			return
		}
		rel, err := filepath.Rel("images", file)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}
		if normalizeTag(tag) == "" {
			counter.Add(key)
		} else {
			counter.Add(key + "_tag")
		}
		resp := map[string]any{"url": "/images/" + strings.TrimPrefix(filepath.ToSlash(rel), "/")}
		if normalizeTag(tag) != "" {
			resp["tag"] = normalizeTag(tag)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func randomImageRedirectWithTagHandler(folder, key string, counter *Counter, pool *ImagePool, ti *TagIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag := r.URL.Query().Get("tag")
		var (
			file string
			err  error
		)
		if normalizeTag(tag) == "" {
			file, err = pickRandomFromPoolExcludingTag(folder, hiddenPoolTag, pool, ti)
		} else {
			file, err = pickTaggedImage(folder, tag, ti)
		}
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "NO_IMAGES", "没有可用图片", err.Error())
			return
		}
		rel, err := filepath.Rel("images", file)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误", err.Error())
			return
		}
		if normalizeTag(tag) == "" {
			counter.Add(key)
		} else {
			counter.Add(key + "_tag")
		}
		http.Redirect(w, r, "/images/"+strings.TrimPrefix(filepath.ToSlash(rel), "/"), http.StatusFound)
	}
}

func adminTagsHandler(sm *SessionManager, ti *TagIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect GET")
			return
		}
		if !requireLogin(sm, w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": ti.ListTags()})
	}
}

func adminImagesHandler(sm *SessionManager, pool *ImagePool, ti *TagIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect GET")
			return
		}
		if !requireLogin(sm, w, r) {
			return
		}

		category := strings.TrimSpace(r.URL.Query().Get("category"))
		tag := normalizeTag(r.URL.Query().Get("tag"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		if pageSize <= 0 {
			pageSize = 100
		}
		if pageSize > 300 {
			pageSize = 300
		}

		collect := make([]string, 0)
		if tag != "" {
			for _, rel := range ti.GetImagesByTag(tag) {
				if category == "web" && !strings.HasPrefix(rel, "web/") {
					continue
				}
				if category == "m" && !strings.HasPrefix(rel, "m/") {
					continue
				}
				collect = append(collect, rel)
			}
		} else {
			if category == "" || category == "web" {
				for _, f := range pool.ListFiles("images/web") {
					rel, err := filepath.Rel("images", f)
					if err == nil {
						collect = append(collect, filepath.ToSlash(rel))
					}
				}
			}
			if category == "" || category == "m" {
				for _, f := range pool.ListFiles("images/m") {
					rel, err := filepath.Rel("images", f)
					if err == nil {
						collect = append(collect, filepath.ToSlash(rel))
					}
				}
			}
		}

		sort.Strings(collect)
		total := len(collect)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}

		items := make([]map[string]any, 0, end-start)
		for _, rel := range collect[start:end] {
			items = append(items, map[string]any{
				"path": rel,
				"tags": ti.GetTags(rel),
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"items":    items,
			"page":     page,
			"pageSize": pageSize,
			"total":    total,
		})
	}
}

func adminSetImageTagsHandler(sm *SessionManager, ti *TagIndex) http.HandlerFunc {
	type reqBody struct {
		Path string   `json:"path"`
		Tags []string `json:"tags"`
		Mode string   `json:"mode"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许", "expect POST")
			return
		}
		if !requireLogin(sm, w, r) {
			return
		}

		var req reqBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "请求参数错误", err.Error())
			return
		}

		rel, ok := normalizeRelImagePath(req.Path)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, "INVALID_PATH", "图片路径无效", req.Path)
			return
		}
		if info, err := os.Stat(filepath.Join("images", filepath.FromSlash(rel))); err != nil || info.IsDir() {
			writeAPIError(w, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在", rel)
			return
		}

		if strings.EqualFold(req.Mode, "append") {
			ti.AddTags(rel, req.Tags)
		} else {
			ti.ReplaceTags(rel, req.Tags)
		}
		ti.InvalidateDefaultPoolCache()

		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"path":   rel,
			"tags":   ti.GetTags(rel),
		})
	}
}
