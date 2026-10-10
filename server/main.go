package main

// main.go - 程序入口与路由注册

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	// 确保 assets 子目录存在
	for _, dir := range AssetSubdirs {
		os.MkdirAll(filepath.Join(AssetsDir, dir), 0755)
	}
	// 确保元数据目录存在
	os.MkdirAll(MetaDir, 0755)

	mux := http.NewServeMux()

	// 静态资源
	mux.HandleFunc("/", staticHandler)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(AssetsDir))))

	// 图库 API
	mux.HandleFunc("/api/library", libraryHandler)
	mux.HandleFunc("/api/library/", libraryItemHandler)

	// 缓存 API
	mux.HandleFunc("/api/cache/start", cacheStartHandler)
	mux.HandleFunc("/api/cache/status", cacheStatusHandler)
	mux.HandleFunc("/api/cache/clear", cacheClearHandler)
	mux.HandleFunc("/api/version", versionHandler)

	// 配置保存/加载 API
	mux.HandleFunc("/api/config/save", configSaveHandler)
	mux.HandleFunc("/api/config/list", configListHandler)
	mux.HandleFunc("/api/config/load", configLoadHandler)
	mux.HandleFunc("/api/config/delete", configDeleteHandler)

	fmt.Printf("服务器启动: http://localhost%s\n", ListenAddr)
	fmt.Printf("前端目录: %s/  图片目录: %s/\n", WebDir, AssetsDir)
	if err := http.ListenAndServe(ListenAddr, mux); err != nil {
		fmt.Println("启动失败:", err)
		os.Exit(1)
	}
}
