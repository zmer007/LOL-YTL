package main

// handler.go - HTTP 请求处理器

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// CacheProgress 缓存下载进度
type CacheProgress struct {
	Running bool   `json:"running"`
	Stage   string `json:"stage"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Message string `json:"message"`
}

// LibItem 图库中的一项
type LibItem struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category"` // 官方分类 (职业/大类/符文路径)
}

var (
	cacheMu       sync.Mutex
	cacheProgress CacheProgress
	cacheVersion  atomic.Value // string
)

func init() {
	cacheVersion.Store(DefaultVersion)
}

// ---------- 静态文件 ----------

// staticHandler 托管前端页面
func staticHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	// 安全: 防止路径穿越
	if strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(WebDir, path))
}

// ---------- 图库 API ----------

// libraryHandler GET /api/library 返回四类图片列表
func libraryHandler(w http.ResponseWriter, r *http.Request) {
	result := map[string][]LibItem{
		"champion": listType("champions"),
		"item":     listType("items"),
		"rune":     listType("runes"),
		"summoner": listType("summoners"),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// listType 列出某目录下的图片, 并附带官方分类
func listType(typeDir string) []LibItem {
	dir := filepath.Join(AssetsDir, typeDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []LibItem{}
	}
	// 加载分类元数据 (来自官方 Data Dragon, 缓存时保存)
	meta := loadMeta(typeDir)
	items := []LibItem{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isImage(name) {
			continue
		}
		cnName := strings.TrimSuffix(name, filepath.Ext(name))
		// URL 带版本号参数, 避免浏览器缓存旧图片
		ver := cacheVersion.Load().(string)
		items = append(items, LibItem{
			Name:     cnName,
			URL:      fmt.Sprintf("/assets/%s/%s?v=%s", typeDir, name, ver),
			Category: meta[cnName], // 空字符串表示无分类
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

// loadMeta 加载 assets/meta/{typeDir}.json 分类映射
func loadMeta(typeDir string) map[string]string {
	meta := map[string]string{}
	path := filepath.Join(MetaDir, typeDir+".json")
	f, err := os.Open(path)
	if err != nil {
		return meta
	}
	defer f.Close()
	json.NewDecoder(f).Decode(&meta)
	return meta
}

// isImage 判断是否为图片文件
func isImage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"
}

// libraryItemHandler DELETE /api/library/{type}/{name} 删除图片
func libraryItemHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/library/"), "/")
	if len(parts) != 2 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	dir, ok := TypeToDir[parts[0]]
	if !ok {
		http.Error(w, "unknown type", http.StatusBadRequest)
		return
	}
	name := parts[1]
	// 安全校验
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	path := findImageFile(filepath.Join(AssetsDir, dir), name)
	if path == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := os.Remove(path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// findImageFile 按名称查找图片 (尝试常见扩展名)
func findImageFile(dir, name string) string {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif"} {
		p := filepath.Join(dir, name+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ---------- 缓存状态 API ----------

// versionHandler GET /api/version
func versionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"version": cacheVersion.Load().(string)})
}

// cacheStatusHandler GET /api/cache/status
func cacheStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cacheMu.Lock()
	defer cacheMu.Unlock()
	json.NewEncoder(w).Encode(cacheProgress)
}

// cacheStartHandler POST /api/cache/start?version=xx
func cacheStartHandler(w http.ResponseWriter, r *http.Request) {
	cacheMu.Lock()
	running := cacheProgress.Running
	cacheMu.Unlock()
	if running {
		http.Error(w, "缓存正在进行中", http.StatusConflict)
		return
	}

	version := r.URL.Query().Get("version")
	if version == "" {
		version = cacheVersion.Load().(string)
	}
	cacheVersion.Store(version)

	go runCache(version)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

// cacheClearHandler POST /api/cache/clear - 删除所有缓存的图片资源
func cacheClearHandler(w http.ResponseWriter, r *http.Request) {
	cacheMu.Lock()
	running := cacheProgress.Running
	cacheMu.Unlock()
	if running {
		http.Error(w, "缓存正在进行中, 无法清空", http.StatusConflict)
		return
	}

	// 需要删除的图片目录和元数据目录
	dirs := []string{"champions", "items", "runes", "summoners", "meta"}
	deleted := 0
	for _, d := range dirs {
		p := filepath.Join(AssetsDir, d)
		if err := os.RemoveAll(p); err == nil {
			deleted++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"deleted": deleted,
		"message": fmt.Sprintf("已清空 %d 个目录", deleted),
	})
}

// ---------- 配置保存/加载 API ----------

// SavesDir 配置文件保存目录
const SavesDir = "saves"

func savesPath() string {
	return filepath.Join(AssetsDir, SavesDir)
}

// configSaveHandler POST /api/config/save?name=xxx
func configSaveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	// 安全校验
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(savesPath(), 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// 校验是合法 JSON
	var tmp interface{}
	if err := json.Unmarshal(body, &tmp); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	path := filepath.Join(savesPath(), name+".json")
	if err := os.WriteFile(path, body, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "name": name})
}

// configListHandler GET /api/config/list
func configListHandler(w http.ResponseWriter, r *http.Request) {
	type SaveInfo struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		ModTime string `json:"modTime"`
	}
	entries, err := os.ReadDir(savesPath())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]SaveInfo{})
		return
	}
	list := []SaveInfo{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, SaveInfo{
			Name:    strings.TrimSuffix(name, ".json"),
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ModTime > list[j].ModTime
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// configLoadHandler GET /api/config/load?name=xxx
func configLoadHandler(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	path := filepath.Join(savesPath(), name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// configDeleteHandler DELETE /api/config/delete?name=xxx
func configDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	path := filepath.Join(savesPath(), name+".json")
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := os.RemoveAll(path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
