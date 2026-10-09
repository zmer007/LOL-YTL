package main

// cache.go - 从 Data Dragon 下载游戏图片资源

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
	"time"
)

// setProgress 更新缓存进度
func setProgress(stage string, done, total int, msg string) {
	cacheMu.Lock()
	cacheProgress = CacheProgress{
		Running: true, Stage: stage, Done: done, Total: total, Message: msg,
	}
	cacheMu.Unlock()
}

// runCache 执行完整缓存流程: 英雄 → 装备 → 符文 → 召唤师技能
func runCache(version string) {
	setProgress("init", 0, 0, "开始下载...")
	base := fmt.Sprintf("%s/%s", DDragonBase, version)

	// 1. 英雄
	if err := cacheChampions(base); err != nil {
		setProgress("error", 0, 0, "英雄下载失败: "+err.Error())
		return
	}

	// 2. 装备
	if err := cacheItems(base); err != nil {
		setProgress("error", 0, 0, "装备下载失败: "+err.Error())
		return
	}

	// 3. 符文
	if err := cacheRunes(base); err != nil {
		setProgress("error", 0, 0, "符文下载失败: "+err.Error())
		return
	}

	// 4. 召唤师技能
	if err := cacheSummoners(base); err != nil {
		setProgress("error", 0, 0, "召唤师技能下载失败: "+err.Error())
		return
	}

	cacheMu.Lock()
	cacheProgress = CacheProgress{Running: false, Stage: "done", Message: "缓存完成!"}
	cacheMu.Unlock()
}

// cacheChampions 下载所有英雄头像
func cacheChampions(base string) error {
	setProgress("champion", 0, 0, "获取英雄列表...")
	raw, err := fetchJSON(base + "/data/zh_CN/champion.json")
	if err != nil {
		return err
	}
	root, _ := raw.(map[string]interface{})
	data, _ := root["data"].(map[string]interface{})

	names := make([]string, 0, len(data))
	cnNames := map[string]string{}
	for k, v := range data {
		names = append(names, k)
		if vm, ok := v.(map[string]interface{}); ok {
			if n, ok := vm["name"].(string); ok {
				cnNames[k] = n
			}
		}
	}
	sort.Strings(names)

	downloadImages("champions", names, cnNames,
		func(name string) string { return fmt.Sprintf("%s/img/champion/%s.png", base, name) })
	return nil
}

// cacheItems 下载所有装备图标
func cacheItems(base string) error {
	setProgress("item", 0, 0, "获取装备列表...")
	raw, err := fetchJSON(base + "/data/zh_CN/item.json")
	if err != nil {
		return err
	}
	root, _ := raw.(map[string]interface{})
	data, _ := root["data"].(map[string]interface{})

	var ids []string
	cnNames := map[string]string{}
	for k, v := range data {
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		img, ok := vm["image"].(map[string]interface{})
		if !ok {
			continue
		}
		full, _ := img["full"].(string)
		if full == "" {
			continue
		}
		ids = append(ids, k)
		if n, ok := vm["name"].(string); ok {
			cnNames[k] = n
		}
	}
	sort.Strings(ids)

	downloadImages("items", ids, cnNames,
		func(id string) string { return fmt.Sprintf("%s/img/item/%s.png", base, id) })
	return nil
}

// cacheRunes 下载所有符文图标
func cacheRunes(base string) error {
	setProgress("rune", 0, 0, "获取符文列表...")
	raw, err := fetchJSON(base + "/data/zh_CN/runesReforged.json")
	if err != nil {
		return err
	}
	trees, _ := raw.([]interface{})

	var paths []string
	cnNames := map[string]string{}
	for _, t := range trees {
		tm, _ := t.(map[string]interface{})
		slots, _ := tm["slots"].([]interface{})
		for _, s := range slots {
			sm, _ := s.(map[string]interface{})
			runes, _ := sm["runes"].([]interface{})
			for _, ru := range runes {
				rum, _ := ru.(map[string]interface{})
				icon, ok := rum["icon"].(string)
				if !ok || icon == "" {
					continue
				}
				paths = append(paths, icon)
				if n, ok := rum["name"].(string); ok {
					cnNames[icon] = n
				}
			}
		}
	}

	downloadImages("runes", paths, cnNames,
		func(path string) string {
			return fmt.Sprintf("%s/img/%s", DDragonBase, strings.TrimPrefix(path, "/"))
		})
	return nil
}

// cacheSummoners 下载所有召唤师技能图标
func cacheSummoners(base string) error {
	setProgress("summoner", 0, 0, "获取召唤师技能列表...")
	raw, err := fetchJSON(base + "/data/zh_CN/summoner.json")
	if err != nil {
		return err
	}
	root, _ := raw.(map[string]interface{})
	data, _ := root["data"].(map[string]interface{})

	var keys []string
	cnNames := map[string]string{}
	for k, v := range data {
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		img, ok := vm["image"].(map[string]interface{})
		if !ok {
			continue
		}
		full, _ := img["full"].(string)
		if full == "" {
			continue
		}
		keys = append(keys, k)
		if n, ok := vm["name"].(string); ok {
			cnNames[k] = n
		}
	}
	sort.Strings(keys)

	downloadImages("summoners", keys, cnNames,
		func(key string) string { return fmt.Sprintf("%s/img/spell/%s.png", base, key) })
	return nil
}

// downloadImages 并发下载图片到指定目录
func downloadImages(typeDir string, keys []string, cnNames map[string]string, urlFn func(string) string) {
	total := len(keys)
	if total == 0 {
		return
	}
	sem := make(chan struct{}, DownloadConcurrency)
	var wg sync.WaitGroup
	var done int32

	for _, key := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func(k string) {
			defer wg.Done()
			defer func() { <-sem }()

			name := cnNames[k]
			if name == "" {
				name = k
			}
			safeName := sanitizeFilename(name)
			dest := filepath.Join(AssetsDir, typeDir, safeName+".png")

			// 已存在则跳过
			if _, err := os.Stat(dest); err == nil {
				atomic.AddInt32(&done, 1)
				d := int(atomic.LoadInt32(&done))
				setProgress(typeDir, d, total, fmt.Sprintf("下载%s %d/%d", typeDir, d, total))
				return
			}

			resp, err := http.Get(urlFn(k))
			if err != nil {
				atomic.AddInt32(&done, 1)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				atomic.AddInt32(&done, 1)
				return
			}
			f, err := os.Create(dest)
			if err != nil {
				atomic.AddInt32(&done, 1)
				return
			}
			io.Copy(f, resp.Body)
			f.Close()

			atomic.AddInt32(&done, 1)
			d := int(atomic.LoadInt32(&done))
			if d%5 == 0 || d == total {
				setProgress(typeDir, d, total, fmt.Sprintf("下载%s %d/%d", typeDir, d, total))
			}
		}(key)
	}
	wg.Wait()
}

// sanitizeFilename 清理文件名中的非法字符
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", " ", "_",
	)
	return replacer.Replace(name)
}

// fetchJSON GET 请求并解析 JSON
func fetchJSON(url string) (interface{}, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var data interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return data, nil
}
