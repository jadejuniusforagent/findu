package apkanalysis

// extract_apis —— API 提取专项:从样本(dex/assets/resources.arsc)中提取
// URL 端点、域名资产与 deeplink scheme,产出可直接喂给 http_probe/http_session
// 的目标清单。加固样本的明文字符串会被壳加密吸收,本工具在脱壳产物上效果最佳。

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// urlRe 覆盖 http/https 绝对地址(含路径与 query;尾部标点在清洗阶段剥掉)。
var urlRe = regexp.MustCompile(`https?://[A-Za-z0-9._~:/?#@!$&'()*+,;=%\[\]-]{4,}`)

// noiseHosts 文档/协议名空间等噪声域名(命中即丢弃)。
var noiseHostSuffixes = []string{
	"schemas.android.com", "www.w3.org", "w3.org", "xml.apache.org", "apache.org",
	"purl.org", "json-schema.org", "xmlpull.org", "ns.adobe.com", "xmlsoap.org",
	"npes.org", "schemas.microsoft.com", "apple.com", "itunes.apple.com",
	"javax.xml", "java.sun.com", "glassfish.org", "jcp.org", "hibernate.org",
	"wellformedweb.org", "eclipse.org", "sourceforge.net", "codehaus.org",
	"sqlite.org", "openssl.org", "unicode.org", "ietf.org", "rfc-editor.org",
	"gnu.org", "kernel.org", "freebsd.org", "openssl.cn", "w3c.org",
	"google.com/ads", "doubleclick.net", "example.invalid", "dom4j.org",
}

// ExtractAPIResult API 提取结果。
type ExtractAPIResult struct {
	SampleName   string   `json:"sample_name"`
	Endpoints    []string `json:"endpoints"`           // 带路径的完整 URL(按 host+path 去重)
	Hosts        []string `json:"hosts"`               // 域名资产(去重,含 API base 域)
	Deeplinks    []string `json:"deeplinks,omitempty"` // manifest 中的自定义 scheme(deeplink 面)
	ScannedFiles int      `json:"scanned_files"`
	Truncated    bool     `json:"truncated"`
	TotalURLs    int      `json:"total_urls"`
}

// ExtractAPIs 提取样本内的 API 端点与域名。加固样本请先脱壳并把产物收编为新
// 样本后再调用(明文字符串才会进入 dex 池)。
func (s *Service) ExtractAPIs(ctx context.Context, sampleID string, limit int) (*ExtractAPIResult, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	zr, err := zip.OpenReader(sample.Workspace)
	if err != nil {
		return nil, fmt.Errorf("打开样本失败: %w", err)
	}
	defer zr.Close()

	endpoints := map[string]bool{}
	hosts := map[string]bool{}
	total := 0
	scanned := 0
	truncated := false
	const maxScanBytes = 512 << 20 // 单样本扫描总量上限 512MB,防超大包拖死

	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".dex") &&
			!strings.HasSuffix(strings.ToLower(f.Name), ".arsc") &&
			!strings.HasSuffix(strings.ToLower(f.Name), ".json") &&
			!strings.HasSuffix(strings.ToLower(f.Name), ".xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			continue
		}
		scanned++
		if total > 20000 { // 原始命中过多时停止扫描(截断标记)
			truncated = true
			break
		}
		if scanned > 800 {
			truncated = true
			break
		}
		for _, m := range urlRe.FindAllString(string(data), -1) {
			u := cleanURL(m)
			if u == "" {
				continue
			}
			parsed, err := url.Parse(u)
			if err != nil || parsed.Host == "" {
				continue
			}
			total++
			if isNoiseHost(parsed.Hostname()) {
				continue
			}
			hosts[strings.ToLower(parsed.Hostname())] = true
			if len(parsed.Path) > 1 && endpointsMapCap(endpoints, limit) {
				endpoints[strings.TrimSuffix(u, "/")] = true
			}
		}
	}

	// deeplink scheme 从已解析的 manifest 画像带出(如有)
	deeplinks := []string{}
	if profile, err := s.ManifestSecurityProfile(ctx, sampleID); err == nil {
		if list, ok := profile["deeplinks"].([]string); ok {
			deeplinks = list
		} else if raw, ok := profile["deeplinks"]; ok {
			if arr, ok := raw.([]interface{}); ok {
				for _, v := range arr {
					deeplinks = append(deeplinks, fmt.Sprint(v))
				}
			}
		}
	}

	res := &ExtractAPIResult{
		SampleName:   sample.Name,
		ScannedFiles: scanned,
		Truncated:    truncated,
		TotalURLs:    total,
		Deeplinks:    deeplinks,
	}
	for u := range endpoints {
		res.Endpoints = append(res.Endpoints, u)
	}
	for h := range hosts {
		res.Hosts = append(res.Hosts, h)
	}
	sort.Strings(res.Endpoints)
	sort.Strings(res.Hosts)
	sort.Strings(res.Deeplinks)
	if len(res.Endpoints) > limit {
		res.Endpoints = res.Endpoints[:limit]
		res.Truncated = true
	}
	if len(res.Hosts) > limit {
		res.Hosts = res.Hosts[:limit]
		res.Truncated = true
	}
	if res.Endpoints == nil {
		res.Endpoints = []string{}
	}
	if res.Hosts == nil {
		res.Hosts = []string{}
	}
	return res, nil
}

func endpointsMapCap(m map[string]bool, limit int) bool { return len(m) < limit*3 }

func cleanURL(raw string) string {
	u := strings.TrimRight(raw, ".,;:!?)\"'”]")
	// 砍掉明显被后续字母黏连的 TLD 后缀
	if i := strings.Index(u, "}"); i > 0 {
		u = u[:i]
	}
	return u
}

func isNoiseHost(host string) bool {
	h := strings.ToLower(host)
	for _, s := range noiseHostSuffixes {
		if h == s || strings.HasSuffix(h, "."+s) || strings.Contains(h, s) {
			return true
		}
	}
	return false
}
