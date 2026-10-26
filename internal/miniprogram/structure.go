// structure.go 小程序包结构报告（盘点方案 P2-包内容覆盖）。
//
// wxapkg 解包后,插件/Worker/WASM/开放数据域等非常规内容与页面路由一样
// 都能从声明文件与产物形态判定。此前平台只看 app.json 的路由,这份报告把
// 「包里还有什么」一次讲清,并顺带提取 web-view 落地域名（内嵌 H5 → Web
// 角色交接的素材）。
package miniprogram

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// StructureReport 包结构报告。
type StructureReport struct {
	AppID string `json:"appid,omitempty"`
	// Pages 主包页面路由。
	Pages []string `json:"pages"`
	// Subpackages 分包(root + 页面数)。
	Subpackages []SubpackageInfo `json:"subpackages"`
	// Plugins 插件声明(provider+version,含分包内 plugins)。
	Plugins []PluginInfo `json:"plugins"`
	// Workers Worker 脚本目录(app.json workers 字段)。
	Workers string `json:"workers"`
	// WasmFiles WASM 产物(.wasm 文件,wxwebassembly 常在这里)。
	WasmFiles []string `json:"wasm_files"`
	// OpenDataContext 开放数据域声明(小游戏 game.json 等)。
	OpenDataContext string `json:"open_data_context"`
	// RequiredBackgroundModes 后台能力声明(requiredBackgroundModes)。
	RequiredBackgroundModes []string `json:"required_background_modes"`
	// WebViewHosts web-view 静态落地域名(内嵌 H5 交接素材;动态绑定标注 <dynamic>)。
	WebViewHosts []string `json:"webview_hosts"`
	// WebViewDynamic web-view 动态绑定 src 的页面(来源可污染面)。
	WebViewDynamic []string `json:"webview_dynamic_pages"`
	// AssetCounts 产物形态统计(文本外资产:图片/字体/音频/wasm 等)。
	AssetCounts map[string]int `json:"asset_counts"`
	// Notes 覆盖边界提示(平台不解的内容)。
	Notes []string `json:"notes"`
}

// SubpackageInfo 一个分包。
type SubpackageInfo struct {
	Root      string `json:"root"`
	PageCount int    `json:"page_count"`
}

// PluginInfo 一个插件声明。
type PluginInfo struct {
	Name     string `json:"name"`     // 声明键名
	Provider string `json:"provider"` // 插件 appid(wx 开头)
	Version  string `json:"version,omitempty"`
	From     string `json:"from"` // 声明所在文件(app.json 或分包 json)
}

// 结构声明里的插件条目形态。
type pluginDecl struct {
	Provider string `json:"provider"`
	Version  string `json:"version"`
}

var webViewSrcPattern = regexp.MustCompile(`<web-view[^>]*src\s*=\s*["']([^"']*)["']`)

// AnalyzeStructure 读取工作区的 json 声明与产物形态,产出结构报告。
// 只读不写;json 解析失败静默跳过(分包/多包结构下声明文件不齐全是常态)。
func AnalyzeStructure(workspace string) *StructureReport {
	report := &StructureReport{AssetCounts: map[string]int{}}
	pluginSeen := map[string]bool{}
	webViewHostSeen := map[string]bool{}
	webViewDynamicSeen := map[string]bool{}

	textExts := map[string]bool{
		".js": true, ".json": true, ".wxml": true, ".wxss": true,
		".ts": true, ".css": true, ".html": true, ".vue": true,
		".wasm": true, // 二进制,但单独归类
	}

	_ = filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := filepath.Base(path)
		ext := strings.ToLower(filepath.Ext(name))
		rel, _ := filepath.Rel(workspace, path)
		relSlash := filepath.ToSlash(rel)

		// 产物形态统计
		if !textExts[ext] {
			report.AssetCounts[extOrOther(ext)]++
		}
		if ext == ".wasm" {
			report.WasmFiles = append(report.WasmFiles, relSlash)
		}

		// 声明文件:app.json / 分包与页面 json / game.json
		if ext == ".json" && info.Size() < 1<<20 {
			if data, rerr := os.ReadFile(path); rerr == nil {
				var doc map[string]json.RawMessage
				if json.Unmarshal(data, &doc) == nil {
					applyStructureJSON(report, relSlash, name, doc, pluginSeen)
				}
			}
		}

		// web-view 提取(.wxml)
		if ext == ".wxml" && info.Size() < 1<<20 {
			if data, rerr := os.ReadFile(path); rerr == nil {
				for _, m := range webViewSrcPattern.FindAllStringSubmatch(string(data), -1) {
					recordWebViewSrc(report, relSlash, m[1], webViewHostSeen, webViewDynamicSeen)
				}
			}
		}
		return nil
	})

	sort.Strings(report.Pages)
	sort.Strings(report.WasmFiles)
	sort.Strings(report.WebViewHosts)
	sort.Strings(report.WebViewDynamic)
	if len(report.WasmFiles) == 0 && report.AssetCounts[".wasm"] == 0 {
		delete(report.AssetCounts, ".wasm")
	}

	// 覆盖边界提示
	if len(report.Plugins) > 0 {
		report.Notes = append(report.Notes, "插件代码在独立插件包内,本样本不含其源码;插件 JSBridge 面按角色/知识库方法论单独评估")
	}
	if len(report.WasmFiles) > 0 {
		report.Notes = append(report.Notes, "检测到 WASM 产物:加解密/风控逻辑常在 WASM 内,平台文本检索覆盖不到,需单独逆向")
	}
	if report.Workers != "" {
		report.Notes = append(report.Notes, "Worker 脚本目录 "+report.Workers+" 内 JS 已解包,findrefs 可直接检索(多线程逻辑常被忽略)")
	}
	return report
}

// applyStructureJSON 从单个 json 声明里抽取结构信息。
func applyStructureJSON(report *StructureReport, relSlash, name string, doc map[string]json.RawMessage, pluginSeen map[string]bool) {
	if name == "app.json" {
		if report.AppID == "" {
			var appid string
			decodeInto(doc["appid"], &appid)
			report.AppID = strings.TrimSpace(appid)
		}
		decodeInto(doc["pages"], &report.Pages)
		decodeInto(doc["requiredBackgroundModes"], &report.RequiredBackgroundModes)
		if raw, ok := doc["workers"]; ok {
			var workers string
			if json.Unmarshal(raw, &workers) == nil {
				report.Workers = workers
			}
		}
		if _, ok := doc["subpackages"]; ok {
			var subs []struct {
				Root  string   `json:"root"`
				Pages []string `json:"pages"`
			}
			decodeInto(doc["subpackages"], &subs)
			for _, sub := range subs {
				report.Subpackages = append(report.Subpackages, SubpackageInfo{Root: sub.Root, PageCount: len(sub.Pages)})
			}
		}
	}
	// 开放数据域:app.json 无此字段,小游戏 game.json(或其他配置 json)里声明
	if report.OpenDataContext == "" {
		if raw, ok := doc["openDataContext"]; ok {
			var ctx string
			if json.Unmarshal(raw, &ctx) == nil {
				report.OpenDataContext = strings.TrimSpace(ctx)
			}
		}
	}
	// 插件声明:app.json 与分包 json 都可能有 plugins
	if raw, ok := doc["plugins"]; ok {
		var plugins map[string]pluginDecl
		if json.Unmarshal(raw, &plugins) == nil {
			for pName, p := range plugins {
				key := pName + "@" + p.Provider
				if pluginSeen[key] {
					continue
				}
				pluginSeen[key] = true
				report.Plugins = append(report.Plugins, PluginInfo{Name: pName, Provider: p.Provider, Version: p.Version, From: relSlash})
			}
		}
	}
}

// recordWebViewSrc 归类一个 web-view src(静态域名 / 动态绑定)。
func recordWebViewSrc(report *StructureReport, page, src string, hostSeen, dynamicSeen map[string]bool) {
	src = strings.TrimSpace(src)
	if src == "" {
		return
	}
	if strings.Contains(src, "{{") {
		if !dynamicSeen[page] {
			dynamicSeen[page] = true
			report.WebViewDynamic = append(report.WebViewDynamic, page)
		}
		return
	}
	host := extractHost(src)
	if host == "" || hostSeen[host] {
		return
	}
	hostSeen[host] = true
	report.WebViewHosts = append(report.WebViewHosts, host)
}

func extractHost(src string) string {
	s := src
	// 协议相对形态(//host/path)与显式 scheme 都取 host;纯相对路径无域名
	if strings.HasPrefix(s, "//") {
		s = s[2:]
	} else if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else {
		return ""
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func extOrOther(ext string) string {
	if ext == "" {
		return "(无扩展名)"
	}
	return ext
}

// decodeInto 解码失败仅记日志（纯解析管线，无 logger 注入点，字段按零值缺失处理）。
func decodeInto[T any](raw json.RawMessage, dst *T) {
	if raw != nil {
		if err := json.Unmarshal(raw, dst); err != nil {
			log.Printf("[miniprogram] 解析结构化声明失败(按缺失处理): %v", err)
		}
	}
}
