package main

// config.go - 全局配置常量

const (
	// AssetsDir 图片缓存根目录
	AssetsDir = "assets"
	// MetaDir 分类元数据目录 (name → category 映射, 来自官方 Data Dragon)
	MetaDir = "assets/meta"
	// WebDir 前端静态文件目录
	WebDir = "web"
	// DDragonBase Riot 官方 Data Dragon CDN 地址
	DDragonBase = "https://ddragon.leagueoflegends.com/cdn"
	// CDragonBase CommunityDragon CDN (提供 Data Dragon 缺失的属性碎片等数据)
	CDragonBase = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/zh_cn/v1"
	// DefaultVersion 默认游戏版本
	DefaultVersion = "16.20.1"
	// ListenAddr 监听地址
	ListenAddr = ":8765"
	// DownloadConcurrency 下载并发数
	DownloadConcurrency = 6
)

// 图片子目录名
var AssetSubdirs = []string{"champions", "items", "runes", "summoners"}

// 类型到目录的映射 (API 用单数, 目录用复数)
var TypeToDir = map[string]string{
	"champion": "champions",
	"item":     "items",
	"rune":     "runes",
	"summoner": "summoners",
}
