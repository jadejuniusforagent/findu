package miniprogram

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// InterfaceItem 接口工作台条目。
type InterfaceItem struct {
	ID         string   `json:"id"`
	Method     string   `json:"method"`
	URL        string   `json:"url"`   // 归一化 URL（保留稳定路径）
	RawURL     string   `json:"rawUrl"` // 首次出现的原始 URL
	Domain     string   `json:"domain"`
	SourceFile string   `json:"sourceFile"` // 首次引用文件
	RefCount   int      `json:"refCount"`   // 被引用的文件数（文件级去重）
	HasAuth    bool     `json:"hasAuth"`    // 调用是否带鉴权头
	IDOR       bool     `json:"idorCandidate"`   // 含动态参数/数字段，疑似越权(IDOR)
	Unauth     bool     `json:"unauthCandidate"` // 管理/内部/调试路径，疑似未授权
	Params     []string `json:"params"`
	Highlights []string `json:"highlights"`
}

// DomainStat 域名资产聚合。
type DomainStat struct {
	Domain string `json:"domain"`
	Count  int    `json:"count"`
}

// InterfaceWorkbench 接口工作台分析结果。
type InterfaceWorkbench struct {
	Interfaces []InterfaceItem `json:"interfaces"`
	Stats      map[string]int  `json:"stats"`
	Domains    []DomainStat    `json:"domains"`
}

var (
	reMethod = regexp.MustCompile(`method\s*:\s*["'](\w+)["']`)
	reAuthH  = regexp.MustCompile(`(?i)(authorization|bearer|x-?token|app-?token|wx-?token|session-?key)\s*:`)
	reData   = regexp.MustCompile(`data\s*:\s*\{([^}]*)\}`)
	reDataKey = regexp.MustCompile(`["']?(\w[\w]*)["']?\s*:`)
	reFetchMethod = regexp.MustCompile(`fetch\(\s*["'][^"']+["']\s*,\s*\{[^}]*?method\s*:\s*["'](\w+)["']`)
	// 全文件字面量 URL：wx.request 常为变量拼接(如 url:e)，真实接口写在封装层/配置的字面量里。
	reLiteralUrl = regexp.MustCompile(`(?:https?|wss?)://[a-zA-Z0-9][a-zA-Z0-9._-]*(?:\.[a-zA-Z]{2,})+(?::\d+)?(?:/[^\s"'\\<>()\[\]{}]*)?`)
	// 越权(IDOR)：路径数字段 /{xx} /:xx，或 query 里的 id 类参数。
	reIDORPath = regexp.MustCompile(`(?:/\d+(?:/|$)|/\{[^}]*\}|/:[^/]+)`)
	reIDORQry  = regexp.MustCompile(`(?i)(?:^|[?&])(?:id|uid|userId|user_id|orderId|order_id|goodsId|sn|order_no|account|mobile|phone)=`)
	// 未授权候选：管理/内部/调试/后台等路径。
	reUnauth   = regexp.MustCompile(`(?i)/(?:admin|debug|internal|console|manage|backend|test|staging|member|user/list|config|setting|operator|api/internal)`)
	reHostWork = regexp.MustCompile(`(?i)(?:https?|wss?)://([a-z0-9][a-z0-9._-]*(?::\d+)?)`)
)

// BuildInterfaceWorkbench 遍历解包工作区，汇总结构化接口清单并标记越权/未授权候选。
// 只读不写，复用 topology 的调用正则。与拓扑图互补：拓扑讲关系，工作台讲可操作性。
func BuildInterfaceWorkbench(workspace string) *InterfaceWorkbench {
	res := &InterfaceWorkbench{Stats: map[string]int{}}
	if workspace == "" {
		return res
	}

	type key struct{ domain, path string }
	items := map[key]*InterfaceItem{}
	sources := map[key]map[string]bool{}
	order := []key{}
	uniq := func(k key) *InterfaceItem {
		if it, ok := items[k]; ok {
			return it
		}
		it := &InterfaceItem{ID: "api:" + k.domain + k.path, Domain: k.domain, URL: k.domain + k.path, Params: []string{}}
		items[k] = it
		sources[k] = map[string]bool{}
		order = append(order, k)
		return it
	}

	// 提取并记入某个接口，返回条目以便继续补充 method/鉴权等。
	record := func(raw, srcRel string) *InterfaceItem {
		domain, path, query := workURL(raw)
		if domain == "" {
			return nil
		}
		if ignoreDomain(domain) {
			return nil
		}
		k := key{domain, path}
		it := uniq(k)
		if it.RawURL == "" {
			it.RawURL = strings.TrimSpace(raw)
		}
		if it.SourceFile == "" {
			it.SourceFile = srcRel
		}
		if !sources[k][srcRel] {
			sources[k][srcRel] = true
			it.RefCount++
		}
		// 越权/未授权候选（路径级判断；URL 字面量以 / 结尾＝动态拼接路径，通常后续拼 id）。
		rawTrim := strings.TrimSpace(strings.Trim(raw, `"'`))
		if reIDORPath.MatchString(path) || reIDORQry.MatchString(query) || (path != "" && strings.HasSuffix(rawTrim, "/")) {
			if !it.IDOR {
				it.IDOR = true
			}
		}
		if reUnauth.MatchString(path) {
			if !it.Unauth {
				it.Unauth = true
			}
		}
		// query 参数。
		for _, kv := range strings.Split(query, "&") {
			if eq := strings.IndexByte(kv, '='); eq > 0 {
				p := kv[:eq]
				if !containsStr(it.Params, p) {
					it.Params = append(it.Params, p)
				}
			}
		}
		return it
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

		// wx.request / uploadFile / downloadFile。
		for _, m := range reApiCall.FindAllStringSubmatch(src, -1) {
			body := m[1]
			var urlStr string
			for _, u := range reUrlArg.FindAllStringSubmatch(body, -1) {
				urlStr = u[1]
				break
			}
			if urlStr == "" {
				continue
			}
			it := record(urlStr, relSlash)
			if it == nil {
				continue
			}
			// method：uploadFile 固定 POST，其余读显式 method。
			if strings.Contains(m[0], "uploadFile") {
				it.Method = "POST"
			} else if mm := reMethod.FindStringSubmatch(body); len(mm) == 2 {
				it.Method = strings.ToUpper(mm[1])
			} else if strings.Contains(m[0], "downloadFile") {
				it.Method = "GET"
			} else {
				it.Method = "POST"
			}
			if reAuthH.MatchString(body) {
				it.HasAuth = true
			}
			if dm := reData.FindStringSubmatch(body); len(dm) == 2 {
				for _, dk := range reDataKey.FindAllStringSubmatch(dm[1], -1) {
					p := dk[1]
					if p == "" || containsStr(it.Params, p) {
						continue
					}
					it.Params = append(it.Params, p)
				}
			}
		}

		// fetch（默认 GET）。
		for _, m := range reFetch.FindAllStringSubmatch(src, -1) {
			it := record(m[1], relSlash)
			if it != nil {
				if mm := reFetchMethod.FindStringSubmatch(src); len(mm) == 2 {
					it.Method = strings.ToUpper(mm[1])
				} else if it.Method == "" {
					it.Method = "GET"
				}
			}
		}
		// 全文件字面量 URL（封装层/配置里的接口，wx.request 多为变量拼接）。
		for _, m := range reLiteralUrl.FindAllString(src, 300) {
			if skipStaticURL(m) {
				continue
			}
			it := record(m, relSlash)
			if it != nil {
				if it.Method == "" {
					it.Method = "?"
				}
				if !it.HasAuth {
					it.Highlights = append(it.Highlights, "字面量接口地址（method 未知，建议抓包确认）")
				}
			}
		}
		return nil
	})

	// 组装 highlights + 排序（越权/未授权优先，其次热度）。
	stats := map[string]int{"interfaces": 0, "idor": 0, "unauth": 0, "domains": 0}
	domains := map[string]int{}
	out := make([]InterfaceItem, 0, len(order))
	for _, k := range order {
		it := items[k]
		if it.Method == "" {
			it.Method = "GET"
		}
		if it.IDOR {
			it.Highlights = append(it.Highlights, "含动态参数/数字段，疑似越权(IDOR)")
			stats["idor"]++
		}
		if it.Unauth {
			it.Highlights = append(it.Highlights, "管理/内部/调试路径，疑似未授权访问")
			stats["unauth"]++
		}
		if !it.HasAuth && (it.IDOR || it.Unauth) {
			it.Highlights = append(it.Highlights, "调用未携带鉴权头")
		}
		domains[it.Domain]++
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i], out[j]
		if ai.IDOR != aj.IDOR {
			return ai.IDOR
		}
		if ai.Unauth != aj.Unauth {
			return ai.Unauth
		}
		if ai.RefCount != aj.RefCount {
			return ai.RefCount > aj.RefCount
		}
		return ai.URL < aj.URL
	})

	stats["interfaces"] = len(out)
	stats["domains"] = len(domains)
	domStats := make([]DomainStat, 0, len(domains))
	for d, c := range domains {
		domStats = append(domStats, DomainStat{Domain: d, Count: c})
	}
	sort.Slice(domStats, func(i, j int) bool { return domStats[i].Count > domStats[j].Count })
	res.Interfaces = out
	res.Stats = stats
	res.Domains = domStats
	return res
}

// workURL 解析出 (domain, path, query)；path 保留动态段、去 query；动态拼接无法解析时返回空 domain。
func workURL(raw string) (domain, path, query string) {
	u := strings.TrimSpace(strings.Trim(raw, `"'`))
	if u == "" {
		return "", "", ""
	}
	hm := reHostWork.FindStringSubmatch(u)
	if len(hm) != 2 {
		return "", "", ""
	}
	domain = hm[1]
	rest := u
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[i:]
	} else {
		rest = ""
	}
	// 动态拼接（模板/变量/引号/加号）→ 无法得到稳定路径，仅保留域名级。
	if strings.ContainsAny(rest, "${}{}") || strings.Contains(rest, "+") || strings.Contains(rest, `"`) {
		path = ""
		query = ""
		return domain, path, query
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		q := rest[i:]
		if strings.HasPrefix(q, "?") {
			if j := strings.IndexByte(q, '#'); j >= 0 {
				q = q[:j]
			}
			query = strings.TrimPrefix(q, "?")
		}
		rest = rest[:i]
	}
	rest = strings.Trim(rest, "/")
	return domain, rest, query
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// skipStaticURL 判断 URL 是否为静态资源（图片/字体/音视频/脚本等），这类不应列入接口。
func skipStaticURL(u string) bool {
	p := u
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	lower := strings.ToLower(p)
	for _, ext := range []string{
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".bmp",
		".css", ".woff", ".woff2", ".ttf", ".otf", ".eot",
		".mp3", ".mp4", ".mov", ".avi", ".webm", ".m4a", ".wav", ".flac", ".ogg",
		".zip", ".apk", ".ipa", ".wasm", ".pdf", ".map", ".js",
	} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// ignoreDomain 第三方库/文档/示例域名（含子域），非业务接口，跳过以免污染清单。
func ignoreDomain(d string) bool {
	d = strings.ToLower(strings.TrimSpace(d))
	for _, bad := range []string{
		"github.com", "githubusercontent.com", "gitlab.com", "bitbucket.org",
		"npmjs.com", "registry.npmjs.org", "unpkg.com", "jsdelivr.net",
		"momentjs.com", "marked.js.org", "js.org", "stackoverflow.com",
		"developer.mozilla.org", "w3.org", "ecma-international.org",
		"example.com", "example.org", "docs.rs", "reactjs.org", "vuejs.org",
		"babeljs.io", "webpack.js.org", "typescriptlang.org", "nodejs.org",
	} {
		if d == bad || strings.HasSuffix(d, "."+bad) {
			return true
		}
	}
	return false
}