// topology.go 小程序调用链/接口拓扑分析。
//
// 对反编译解包后的源码做静态再处理：解析页面跳转边、接口(URL)调用、云函数
// 调用，把「页面/文件 → 目标」的关系汇总成一张可读的架构拓扑图，辅助看清
// 整个项目的操作面（哪些接口/云函数是测试点）与页面流转。
//
// 采用与 scanner/structure 一致的正则提取方式（不引入 JS AST），覆盖压缩成
// 单行的产物；动态拼接的 URL 只收敛到域名级，避免拓扑被变量噪声淹没。
package miniprogram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TopologyNode 拓扑节点。
type TopologyNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Type: page | file | api | cloudfn
	Type string `json:"type"`
	// File 来源文件（page/file 节点的自身路径，api/cloudfn 的首次出现文件）。
	File string `json:"file,omitempty"`
	// RefCount 被引用/调用次数（操作点热度：越高越关键）。
	RefCount int `json:"refCount"`
	// Detail 附加信息（api 的 url 等）。
	Detail map[string]string `json:"detail,omitempty"`
}

// TopologyEdge 拓扑边。
type TopologyEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	// Type: navigate(页面跳转) | api_call(接口调用) | cloudfn(云函数调用)
	Type string `json:"type"`
	// Label 目标描述（跳转页面 / 接口 URL / 云函数名）。
	Label string `json:"label,omitempty"`
	// File 触发该边所在文件。
	File string `json:"file,omitempty"`
}

// TopologyResult 拓扑分析结果。
type TopologyResult struct {
	Nodes []TopologyNode `json:"nodes"`
	Edges []TopologyEdge `json:"edges"`
	Stats map[string]int `json:"stats"`
}

var (
	reNavigate  = regexp.MustCompile(`wx\.(?:navigateTo|redirectTo|switchTab|reLaunch)\(([^)]*)\)`)
	reUrlArg    = regexp.MustCompile(`url\s*:\s*["']([^"']+)["']`)
	reApiCall   = regexp.MustCompile(`wx\.(?:request|uploadFile|downloadFile)\(([^)]*)\)`)
	reFetch     = regexp.MustCompile(`\bfetch\(\s*["']([^"']+)["']`)
	reCloudFn   = regexp.MustCompile(`wx\.cloud\.callFunction\(\s*\{[^}]*name\s*:\s*["']([^"']+)["']`)
	rePageMaker = regexp.MustCompile(`\b(?:Page|Component)\s*\(`)
)

// AnalyzeTopology 对解包工作区做静态调用链分析，返回拓扑图数据。只读不写。
func AnalyzeTopology(workspace string) *TopologyResult {
	res := &TopologyResult{Stats: map[string]int{}}
	if workspace == "" {
		return res
	}
	nodes := map[string]*TopologyNode{}
	var edges []TopologyEdge

	// 预扫描真实存在的 .js/.ts 路径。反编译常把页面合并进 app-service.js / 分包 chunk，
	// app.json/navigateTo 推断的页面 .js 可能不存在；仅当真实存在时才允许前端跳转源码。
	jsPaths := map[string]bool{}
	_ = filepath.Walk(workspace, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".js" || ext == ".ts" {
			rel, _ := filepath.Rel(workspace, p)
			jsPaths[filepath.ToSlash(rel)] = true
		}
		return nil
	})

	ensure := func(id, label, typ, file string, detail map[string]string) *TopologyNode {
		n, ok := nodes[id]
		if !ok {
			n = &TopologyNode{ID: id, Label: label, Type: typ, File: file, RefCount: 0, Detail: detail}
			nodes[id] = n
			res.Stats[typ]++
		}
		return n
	}
	// 记录一次「来源文件 → 目标」的引用（提升目标热度）。
	ref := func(targetID, file string) {
		if n, ok := nodes[targetID]; ok {
			n.RefCount++
			if n.File == "" {
				n.File = file
			}
		}
	}

	// 主包页面声明（app.json pages）。
	pages := map[string]bool{}
	if data, err := os.ReadFile(filepath.Join(workspace, "app.json")); err == nil {
		var doc struct {
			Pages []string `json:"pages"`
		}
		if json.Unmarshal(data, &doc) == nil {
			for _, p := range doc.Pages {
				p = strings.TrimPrefix(strings.TrimSpace(p), "/")
				if p == "" {
					continue
				}
				pages[p] = true
				pf := ""
				if jsPaths[p+".js"] {
					pf = p + ".js"
				}
				ensure("page:"+p, p, "page", pf, nil)
			}
		}
	}

	_ = filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Size() > 16<<20 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".js" && ext != ".ts" {
			return nil
		}
		rel, _ := filepath.Rel(workspace, path)
		relSlash := filepath.ToSlash(rel)
		if strings.HasPrefix(filepath.Base(relSlash), ".") || strings.Contains(relSlash, "node_modules") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := string(data)

		// 来源节点：页面 js 归到页面节点，其余作为文件模块节点。
		pagePath := strings.TrimSuffix(relSlash, filepath.Ext(relSlash))
		srcID, srcType := "file:"+relSlash, "file"
		if pages[pagePath] || rePageMaker.MatchString(src) {
			ensure("page:"+pagePath, pagePath, "page", relSlash, nil)
			srcID, srcType = "page:"+pagePath, "page"
		}
		ensure(srcID, srcType, "", relSlash, nil)

		// 页面跳转边。
		for _, m := range reNavigate.FindAllStringSubmatch(src, -1) {
			for _, u := range reUrlArg.FindAllStringSubmatch(m[1], -1) {
				target := normalizePagePath(u[1])
				if target == "" {
					continue
				}
				pf := ""
				if jsPaths[target+".js"] {
					pf = target + ".js"
				}
				ensure("page:"+target, target, "page", pf, nil)
				edges = append(edges, TopologyEdge{Source: srcID, Target: "page:" + target, Type: "navigate", Label: target, File: relSlash})
				ref("page:"+target, relSlash)
			}
		}

		// 接口调用边（wx.request/uploadFile/downloadFile + fetch）。
		apiSeen := map[string]bool{}
		collectAPI := func(raw string) {
			label, norm := normalizeAPI(raw)
			if norm == "" || apiSeen[norm] {
				return
			}
			apiSeen[norm] = true
			ensure("api:"+norm, label, "api", relSlash, map[string]string{"url": strings.TrimSpace(raw)})
			edges = append(edges, TopologyEdge{Source: srcID, Target: "api:" + norm, Type: "api_call", Label: label, File: relSlash})
			ref("api:"+norm, relSlash)
		}
		for _, m := range reApiCall.FindAllStringSubmatch(src, -1) {
			for _, u := range reUrlArg.FindAllStringSubmatch(m[1], -1) {
				collectAPI(u[1])
			}
		}
		for _, m := range reFetch.FindAllStringSubmatch(src, -1) {
			collectAPI(m[1])
		}

		// 云函数调用边。
		cfSeen := map[string]bool{}
		for _, m := range reCloudFn.FindAllStringSubmatch(src, -1) {
			name := strings.TrimSpace(m[1])
			if name == "" || cfSeen[name] {
				continue
			}
			cfSeen[name] = true
			ensure("cloudfn:"+name, name, "cloudfn", relSlash, nil)
			edges = append(edges, TopologyEdge{Source: srcID, Target: "cloudfn:" + name, Type: "cloudfn", Label: name, File: relSlash})
			ref("cloudfn:"+name, relSlash)
		}
		return nil
	})

	for _, n := range nodes {
		if n.Label == "" {
			n.Label = n.ID
		}
	}
	res.Nodes = make([]TopologyNode, 0, len(nodes))
	for _, n := range nodes {
		res.Nodes = append(res.Nodes, *n)
	}
	res.Edges = edges
	return res
}

// normalizePagePath 把跳转 url 归一到页面路径（去 query/扩展名/前导斜杠；相对/非页面目标返回空）。
func normalizePagePath(url string) string {
	u := strings.TrimSpace(url)
	if u == "" || strings.HasPrefix(u, "../") || strings.HasPrefix(u, "./") {
		return ""
	}
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.TrimPrefix(u, "/")
	u = strings.TrimSuffix(u, ".js")
	u = strings.TrimSuffix(u, ".json")
	u = strings.Trim(u, "/")
	if u == "" {
		return ""
	}
	return u
}

// normalizeAPI 把接口 URL 归一到 (label, norm)。动态拼接的 URL 收敛到域名级。
func normalizeAPI(raw string) (label, norm string) {
	u := strings.TrimSpace(strings.Trim(raw, `"'`))
	if u == "" {
		return "", ""
	}
	host := extractHost(u)
	if host == "" {
		return "", ""
	}
	// 取 host 后的路径，去掉 scheme://host 前缀。
	rest := u
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[i:]
	} else {
		rest = ""
	}
	// 动态拼接（模板/变量/引号）→ 只保留 host，避免噪声节点。
	if strings.ContainsAny(rest, "${}{}") || strings.ContainsAny(rest, `"'`) || strings.Contains(rest, "+") {
		rest = ""
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.Trim(rest, "/")
	path := rest
	// 归一 path：去尾部的纯数字/动态段，保留稳定前缀（两段以内）。
	if path != "" {
		segs := strings.Split(path, "/")
		var keep []string
		for _, s := range segs {
			if s == "" || isDynamicSeg(s) {
				break
			}
			keep = append(keep, s)
			if len(keep) >= 2 {
				break
			}
		}
		path = strings.Join(keep, "/")
	}
	label = host
	if path != "" {
		label = host + "/" + path
	}
	return label, host + "/" + path
}

// isDynamicSeg 判断路径段是否含动态内容（数字 ID / 变量占位）。
func isDynamicSeg(s string) bool {
	if s == "" {
		return true
	}
	digits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			digits = false
			break
		}
	}
	return digits
}