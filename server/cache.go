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
	meta := map[string]string{} // 中文名 → 职业分类 (取第一个 tag)
	for k, v := range data {
		names = append(names, k)
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if n, ok := vm["name"].(string); ok {
			cnNames[k] = n
			// 取第一个职业 tag 作为主分类
			if tags, ok := vm["tags"].([]interface{}); ok && len(tags) > 0 {
				if tag, ok := tags[0].(string); ok {
					meta[n] = tag
				}
			}
		}
	}
	sort.Strings(names)

	downloadImages("champions", names, cnNames,
		func(name string) string { return fmt.Sprintf("%s/img/champion/%s.png", base, name) })

	// 保存分类元数据
	saveMeta("champions", meta)
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
	meta := map[string]string{} // 中文名 → 分类
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
			// 将官方 tags 映射为中文大类 (按优先级)
			meta[n] = mapItemTags(vm["tags"])
		}
	}
	sort.Strings(ids)

	downloadImages("items", ids, cnNames,
		func(id string) string { return fmt.Sprintf("%s/img/item/%s.png", base, id) })

	// 保存分类元数据
	saveMeta("items", meta)
	return nil
}

// mapItemTags 将装备官方 tags 映射为中文大类 (按优先级匹配)
func mapItemTags(tags interface{}) string {
	tagList, ok := tags.([]interface{})
	if !ok {
		return "其他"
	}
	has := func(t string) bool {
		for _, x := range tagList {
			if s, ok := x.(string); ok && s == t {
				return true
			}
		}
		return false
	}
	// 优先级: 攻击 > 法术 > 防御 > 移动 > 辅助
	switch {
	case has("Damage") || has("ArmorPenetration") || has("CriticalStrike") ||
		has("AttackSpeed") || has("LifeSteal") || has("OnHit"):
		return "攻击"
	case has("SpellDamage") || has("MagicPenetration") || has("Mana") ||
		has("ManaRegen") || has("SpellVamp"):
		return "法术"
	case has("Health") || has("Armor") || has("SpellBlock") ||
		has("MagicResist") || has("HealthRegen") || has("Tenacity"):
		return "防御"
	case has("Boots") || has("NonbootsMovement") || has("Movement"):
		return "移动"
	default:
		return "辅助"
	}
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
	meta := map[string]string{} // 中文名 → 符文路径
	for _, t := range trees {
		tm, _ := t.(map[string]interface{})
		pathName, _ := tm["name"].(string) // 路径中文名 (精密/主宰/巫术/坚决/启迪)
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
					meta[n] = pathName
				}
			}
		}
	}

	downloadImages("runes", paths, cnNames,
		func(path string) string {
			return fmt.Sprintf("%s/img/%s", DDragonBase, strings.TrimPrefix(path, "/"))
		})

	// 属性碎片 (Stat Shards) - Data Dragon 不提供, 从 CommunityDragon 获取
	cacheStatShards(meta)

	// 保存分类元数据
	saveMeta("runes", meta)
	return nil
}

// cacheStatShards 从 CommunityDragon 获取属性碎片并下载图标
func cacheStatShards(meta map[string]string) {
	setProgress("rune", 0, 0, "获取属性碎片...")
	raw, err := fetchJSON(CDragonBase + "/perks.json")
	if err != nil {
		return
	}
	perks, ok := raw.([]interface{})
	if !ok {
		return
	}
	var paths []string
	cnNames := map[string]string{}
	for _, p := range perks {
		pm, _ := p.(map[string]interface{})
		iconPath, _ := pm["iconPath"].(string)
		if !strings.Contains(iconPath, "StatMods") {
			continue
		}
		name, _ := pm["name"].(string)
		if name == "" {
			continue
		}
		// iconPath 形如 /lol-game-data/assets/v1/perk-images/StatMods/StatModsArmorIcon.png
		// Data Dragon 对应路径: perk-images/StatMods/StatModsArmorIcon.png
		idx := strings.Index(iconPath, "perk-images/")
		if idx < 0 {
			continue
		}
		relPath := iconPath[idx:]
		paths = append(paths, relPath)
		cnNames[relPath] = name
		meta[name] = "属性碎片"
	}
	downloadImages("runes", paths, cnNames,
		func(path string) string {
			return fmt.Sprintf("%s/img/%s", DDragonBase, path)
		})
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

// saveMeta 保存分类元数据到 assets/meta/{type}.json
// meta: 中文名 → 分类 (职业/大类/符文路径)
func saveMeta(typeDir string, meta map[string]string) {
	os.MkdirAll(MetaDir, 0755)
	path := filepath.Join(MetaDir, typeDir+".json")
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	json.NewEncoder(f).Encode(meta)
}
