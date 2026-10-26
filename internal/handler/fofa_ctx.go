package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"finduai/internal/database"
)

// fofa_ctx —— 空间测绘搜索引擎的「非 HTTP」内部调用路径。
// Agent/MCP 工具、后续定时采集 worker 与 Web Search 共用同一套 provider 逻辑，
// 区别仅在于错误以 error 返回而不是写入 gin.Context。

// SearchProviderCtx 以给定引擎执行一次查询，返回与 Web 端一致的响应结构。
// 每次实际调用（含未配置 key）回写源状态台账（凭据健康/用量，尽力而为）。
func (h *FofaHandler) SearchProviderCtx(ctx context.Context, provider string, req fofaSearchRequest) (*fofaSearchResponse, error) {
	provider = normalizeSpaceSearchProvider(provider)
	if provider == "" {
		return nil, fmt.Errorf("provider 不支持，可选：fofa、zoomeye、quake、shodan、hunter")
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return nil, fmt.Errorf("query 不能为空")
	}
	if req.Size <= 0 {
		req.Size = 100
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Size > 10000 {
		req.Size = 10000
	}
	if req.Fields == "" {
		req.Fields = defaultFieldsForProvider(provider)
	}
	apiKey := h.resolveAPIKey(provider)
	if apiKey == "" {
		h.recordSourceStatus(provider, "not_configured", "API Key 未配置")
		return nil, fmt.Errorf("%s 未配置 API Key（系统设置 → 资产管理，或环境变量）", providerDisplayName(provider))
	}
	var (
		resp *fofaSearchResponse
		err  error
	)
	switch provider {
	case "zoomeye":
		resp, err = h.zoomEyeSearchCtx(ctx, req, apiKey)
	case "quake":
		resp, err = h.quakeSearchCtx(ctx, req, apiKey)
	case "shodan":
		resp, err = h.shodanSearchCtx(ctx, req, apiKey)
	case "hunter":
		resp, err = h.hunterSearchCtx(ctx, req, apiKey)
	default:
		resp, err = h.fofaSearchCtx(ctx, req, apiKey)
	}
	if err != nil {
		status := "error"
		if isAuthError(err) {
			status = "auth_failed"
		}
		h.recordSourceStatus(provider, status, err.Error())
		return resp, err
	}
	h.recordSourceStatus(provider, "ok", "")
	_ = time.Now() // 保留计时埋点位置；耗时入 query_log 由调用方记录
	return resp, nil
}

// recordSourceStatus 回写源状态台账（回调由 app 装配层注入 DB 实现；
// 未注入或写失败静默——台账是尽力而为的观测面，不影响主流程）。
func (h *FofaHandler) recordSourceStatus(provider, status, errMsg string) {
	if h.sourceRecorder != nil {
		h.sourceRecorder(provider, status, errMsg)
	}
}

// SourceConfigured 引擎 key 是否已配置（采集调度降级跳过的判据）。
func (h *FofaHandler) SourceConfigured(provider string) bool {
	return strings.TrimSpace(h.resolveAPIKey(provider)) != ""
}

// SetSourceRecorder 注入源状态回写回调（source, status, errMsg）。
func (h *FofaHandler) SetSourceRecorder(fn func(provider, status, errMsg string)) {
	h.sourceRecorder = fn
}

// SearchEngineAssets 面向 Agent/MCP 的空间测绘查询：执行引擎查询并归一化为资产候选。
// 返回候选资产（未入库）、引擎原始行（样本展示用）与命中总数。
func (h *FofaHandler) SearchEngineAssets(ctx context.Context, engine, query string, size, page int) ([]*database.Asset, []map[string]interface{}, int, error) {
	resp, err := h.SearchProviderCtx(ctx, engine, fofaSearchRequest{Query: query, Size: size, Page: page})
	if err != nil {
		return nil, nil, 0, err
	}
	return assetsFromProviderRows(engine, resp.Query, resp.Results), resp.Results, resp.Total, nil
}

func (h *FofaHandler) fofaSearchCtx(ctx context.Context, req fofaSearchRequest, apiKey string) (*fofaSearchResponse, error) {
	baseURL := h.resolveBaseURL("fofa")
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("FOFA base_url 无效: %w", err)
	}
	qb64 := base64.StdEncoding.EncodeToString([]byte(req.Query))
	params := u.Query()
	params.Set("key", apiKey)
	params.Set("qbase64", qb64)
	params.Set("size", fmt.Sprintf("%d", req.Size))
	params.Set("page", fmt.Sprintf("%d", req.Page))
	params.Set("fields", strings.TrimSpace(req.Fields))
	if req.Full {
		params.Set("full", "true")
	} else {
		params.Set("full", "false")
	}
	u.RawQuery = params.Encode()

	var apiResp fofaAPIResponse
	if err := h.doJSONRequestCtx(ctx, http.MethodGet, u.String(), apiKey, "", nil, &apiResp, "FOFA"); err != nil {
		return nil, err
	}
	if apiResp.Error {
		msg := strings.TrimSpace(apiResp.ErrMsg)
		if msg == "" {
			msg = "FOFA 返回错误"
		}
		return nil, fmt.Errorf("%s", msg)
	}
	fields := splitAndCleanCSV(req.Fields)
	results := make([]map[string]interface{}, 0, len(apiResp.Results))
	for _, row := range apiResp.Results {
		item := make(map[string]interface{}, len(fields))
		for i, f := range fields {
			if i < len(row) {
				item[f] = row[i]
			} else {
				item[f] = nil
			}
		}
		results = append(results, item)
	}
	return &fofaSearchResponse{
		Provider:     "fofa",
		Query:        req.Query,
		Size:         apiResp.Size,
		Page:         apiResp.Page,
		Total:        apiResp.Total,
		Fields:       fields,
		ResultsCount: len(results),
		Results:      results,
	}, nil
}

func (h *FofaHandler) zoomEyeSearchCtx(ctx context.Context, req fofaSearchRequest, apiKey string) (*fofaSearchResponse, error) {
	baseURL := h.resolveBaseURL("zoomeye")
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("ZoomEye base_url 无效: %w", err)
	}
	body := map[string]interface{}{
		"qbase64":  base64.StdEncoding.EncodeToString([]byte(req.Query)),
		"page":     req.Page,
		"pagesize": req.Size,
	}
	if fields := strings.TrimSpace(req.Fields); fields != "" {
		body["fields"] = fields
	}
	var apiResp spaceSearchEnvelope
	if err := h.doJSONRequestCtx(ctx, http.MethodPost, u.String(), apiKey, "API-KEY", body, &apiResp, "ZoomEye"); err != nil {
		return nil, err
	}
	rows, err := decodeSpaceSearchRows(apiResp.Data)
	if err != nil {
		return nil, fmt.Errorf("解析 ZoomEye 响应失败: %w", err)
	}
	if zoomEyeRequestFailed(apiResp.Code, apiResp.Message) {
		msg := firstNonEmptySpaceSearchValue(apiResp.Message, messageFromRawObject(apiResp.Data), "ZoomEye 返回错误")
		return nil, fmt.Errorf("%s", msg)
	}
	fields := splitAndCleanCSV(req.Fields)
	return &fofaSearchResponse{
		Provider:     "zoomeye",
		Query:        firstNonEmptySpaceSearchValue(apiResp.Query, req.Query),
		Size:         firstPositive(apiResp.PageSize, req.Size),
		Page:         firstPositive(apiResp.Page, req.Page),
		Total:        apiResp.Total,
		Fields:       fields,
		ResultsCount: len(rows),
		Results:      projectRows(rows, fields),
	}, nil
}

func (h *FofaHandler) quakeSearchCtx(ctx context.Context, req fofaSearchRequest, apiKey string) (*fofaSearchResponse, error) {
	baseURL := h.resolveBaseURL("quake")
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("Quake base_url 无效: %w", err)
	}
	fields := splitAndCleanCSV(req.Fields)
	body := map[string]interface{}{
		"query":  req.Query,
		"size":   req.Size,
		"start":  (req.Page - 1) * req.Size,
		"latest": req.Full,
	}
	if len(fields) > 0 {
		body["include"] = fields
	}
	var apiResp spaceSearchEnvelope
	if err := h.doJSONRequestCtx(ctx, http.MethodPost, u.String(), apiKey, "X-QuakeToken", body, &apiResp, "Quake"); err != nil {
		return nil, err
	}
	rows, err := decodeSpaceSearchRows(apiResp.Data)
	if err != nil {
		return nil, fmt.Errorf("解析 Quake 响应失败: %w", err)
	}
	if !isZeroSpaceSearchCode(apiResp.Code) {
		msg := firstNonEmptySpaceSearchValue(apiResp.Message, messageFromRawObject(apiResp.Data), "Quake 返回错误")
		return nil, fmt.Errorf("%s", msg)
	}
	total := firstPositive(apiResp.TotalCount, apiResp.Meta.Pagination.Total)
	return &fofaSearchResponse{
		Provider:     "quake",
		Query:        req.Query,
		Size:         req.Size,
		Page:         req.Page,
		Total:        total,
		Fields:       fields,
		ResultsCount: len(rows),
		Results:      projectRows(rows, fields),
	}, nil
}

func (h *FofaHandler) shodanSearchCtx(ctx context.Context, req fofaSearchRequest, apiKey string) (*fofaSearchResponse, error) {
	baseURL := strings.TrimRight(h.resolveBaseURL("shodan"), "/") + "/shodan/host/search"
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("Shodan base_url 无效: %w", err)
	}
	var apiResp spaceSearchEnvelope
	targetSize := req.Size
	if targetSize <= 0 {
		targetSize = 100
	}
	if targetSize > 1000 {
		targetSize = 1000
	}
	page := req.Page
	matches := make([]map[string]interface{}, 0, targetSize)
	pagesNeeded := (targetSize + 99) / 100
	for i := 0; i < pagesNeeded; i++ {
		pageURL := *u
		params := pageURL.Query()
		params.Set("key", apiKey)
		params.Set("query", req.Query)
		params.Set("page", fmt.Sprintf("%d", page+i))
		params.Set("minify", "false")
		if fields := strings.TrimSpace(req.Fields); fields != "" {
			params.Set("fields", fields)
		}
		pageURL.RawQuery = params.Encode()
		apiResp = spaceSearchEnvelope{}
		if err := h.doJSONRequestCtx(ctx, http.MethodGet, pageURL.String(), "", "", nil, &apiResp, "Shodan"); err != nil {
			return nil, err
		}
		if errMsg := firstNonEmptySpaceSearchValue(apiResp.Error, apiResp.Message); errMsg != "" {
			return nil, fmt.Errorf("%s", errMsg)
		}
		pageMatches, err := decodeSpaceSearchRows(apiResp.Matches)
		if err != nil {
			return nil, fmt.Errorf("解析 Shodan 响应失败: %w", err)
		}
		if len(pageMatches) == 0 {
			break
		}
		matches = append(matches, pageMatches...)
		if len(matches) >= targetSize {
			matches = matches[:targetSize]
			break
		}
	}
	fields := splitAndCleanCSV(req.Fields)
	return &fofaSearchResponse{
		Provider:     "shodan",
		Query:        req.Query,
		Size:         targetSize,
		Page:         page,
		Total:        apiResp.Total,
		Fields:       fields,
		ResultsCount: len(matches),
		Results:      projectRows(matches, fields),
	}, nil
}

// hunterSearchCtx 奇安信鹰图 openApi/search 适配：
// GET {base}?api-key=&search=<urlsafe-base64(query)>&page=&page_size=&is_web_search=3
// 响应 {"code":20000,"data":{"total":N,"arr":[...]}}；行字段 url/ip/port/domain/
// web_title/number(ICP 备案号)/company 等，不 projection 原样透出。
func (h *FofaHandler) hunterSearchCtx(ctx context.Context, req fofaSearchRequest, apiKey string) (*fofaSearchResponse, error) {
	baseURL := strings.TrimRight(h.resolveBaseURL("hunter"), "/")
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("Hunter base_url 无效: %w", err)
	}
	targetSize := req.Size
	if targetSize <= 0 {
		targetSize = 100
	}
	if targetSize > 500 {
		targetSize = 500
	}
	page := req.Page
	matches := make([]map[string]interface{}, 0, targetSize)
	total := 0
	// page_size 上限 100：请求较大 size 时按页循环（与 Shodan 适配器同策略）
	for i := 0; i*100 < targetSize; i++ {
		pageURL := *u
		params := pageURL.Query()
		params.Set("api-key", apiKey)
		params.Set("search", base64.RawURLEncoding.EncodeToString([]byte(req.Query)))
		params.Set("page", fmt.Sprintf("%d", page+i))
		params.Set("page_size", "100")
		params.Set("is_web_search", "3") // 1=web 2=非web 3=全部
		pageURL.RawQuery = params.Encode()
		var apiResp struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Total int                      `json:"total"`
				Arr   []map[string]interface{} `json:"arr"`
			} `json:"data"`
		}
		if err := h.doJSONRequestCtx(ctx, http.MethodGet, pageURL.String(), "", "", nil, &apiResp, "Hunter"); err != nil {
			return nil, err
		}
		// 20000=成功；其余（积分不足/参数错误/无权限）透出 msg
		if apiResp.Code != 20000 {
			msg := strings.TrimSpace(apiResp.Msg)
			if msg == "" {
				msg = fmt.Sprintf("Hunter 返回错误码 %d", apiResp.Code)
			}
			return nil, fmt.Errorf("%s", msg)
		}
		total = apiResp.Data.Total
		if len(apiResp.Data.Arr) == 0 {
			break
		}
		matches = append(matches, apiResp.Data.Arr...)
		if len(matches) >= targetSize {
			matches = matches[:targetSize]
			break
		}
	}
	return &fofaSearchResponse{
		Provider:     "hunter",
		Query:        req.Query,
		Size:         targetSize,
		Page:         page,
		Total:        total,
		ResultsCount: len(matches),
		Results:      matches,
	}, nil
}

// doJSONRequestCtx doJSONRequest 的无 gin 版本：错误以 error 返回。
func (h *FofaHandler) doJSONRequestCtx(ctx context.Context, method, endpoint, apiKey, headerName string, body interface{}, out interface{}, label string) error {
	var reqBody *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("创建请求失败: %w", err)
		}
		reqBody = strings.NewReader(string(b))
	} else {
		reqBody = strings.NewReader("")
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	httpReq.Header.Set("User-Agent", "FindUAI/1.7.4")
	httpReq.Header.Set("Accept", "application/json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if headerName != "" && apiKey != "" {
		httpReq.Header.Set(headerName, apiKey)
	}
	resp, err := h.client.Do(httpReq)
	if err != nil {
		_, _, timeout := safeFofaRequestError(err)
		if timeout {
			return fmt.Errorf("%s 请求超时（60 秒）", label)
		}
		return fmt.Errorf("无法连接 %s 服务，请检查服务器网络或代理配置", label)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("读取 %s 响应失败: %w", label, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s", extractRemoteAPIError(respBody, resp.StatusCode, label))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		if msg := extractRemoteAPIError(respBody, resp.StatusCode, label); strings.Contains(msg, "网页") {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("解析 %s 响应失败: %w", label, err)
	}
	return nil
}

// isAuthError 识别引擎凭据失效类错误（用于台账 status=auth_failed 判定）。
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, k := range []string{"api key", "apikey", "401", "unauthorized", "无效", "未授权", "无权限", "权限"} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// ── 引擎结果 → 资产归一化 ──

func rowStringValue(row map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		v := valueByPath(row, k)
		if v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		default:
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func rowIntValue(row map[string]interface{}, keys ...string) int {
	for _, k := range keys {
		v := valueByPath(row, k)
		if v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return int(t)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n
			}
		}
	}
	return 0
}

func rowFirstOfList(row map[string]interface{}, key string) string {
	v := valueByPath(row, key)
	switch t := v.(type) {
	case []interface{}:
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	case string:
		parts := strings.Split(strings.Trim(t, ","), ",")
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				return s
			}
		}
	}
	return ""
}

// rowJoinedValue 与 rowStringValue 同语义，但字段值为数组时把字符串项拼接返回
// （ZoomEye 的 title/app 会以 list 形态返回，直接 fmt.Sprint 会得到 "[a b]" 垃圾值）。
// map 形态（org 等嵌套对象）不拼、返回空。
func rowJoinedValue(row map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		v := valueByPath(row, k)
		if v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		case []interface{}:
			parts := make([]string, 0, len(t))
			for _, item := range t {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					parts = append(parts, strings.TrimSpace(s))
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, ", ")
			}
		}
	}
	return ""
}

var htmlTitlePattern = regexp.MustCompile(`(?is)<title[^>]*>\s*(.*?)\s*</title>`)

func htmlTitleFromBody(body string) string {
	if body == "" {
		return ""
	}
	if len(body) > 8192 {
		body = body[:8192]
	}
	m := htmlTitlePattern.FindStringSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	title := strings.TrimSpace(m[1])
	if len([]rune(title)) > 256 {
		title = string([]rune(title)[:256])
	}
	return title
}

// cleanAssetProtocol 归一化引擎返回的协议字段，避免整批入库被单条脏值拖垮：
//   - 小写、去空格；quake 的 service.name 形如 "http/ssl"，取 "/" 前段 → "http"
//   - 其余非法字符（空格/斜杠外的符号等）一律置空（协议为可选字段，置空可入库）
func cleanAssetProtocol(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return ""
	}
	if idx := strings.IndexByte(v, '/'); idx >= 0 {
		v = v[:idx]
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '.' || c == '-' {
			continue
		}
		return ""
	}
	if v == "" || v[0] < 'a' || v[0] > 'z' {
		return ""
	}
	if len(v) > 32 {
		return ""
	}
	return v
}

func urlHostOrEmpty(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
}

// assetsFromProviderRows 引擎行 → Asset 结构（source 标记引擎名）。
func assetsFromProviderRows(provider, query string, rows []map[string]interface{}) []*database.Asset {
	source := providerDisplayName(provider)
	assets := make([]*database.Asset, 0, len(rows))
	for _, row := range rows {
		asset := &database.Asset{Source: source, SourceQuery: query}
		switch provider {
		case "zoomeye":
			asset.IP = rowStringValue(row, "ip")
			asset.Port = rowIntValue(row, "port")
			asset.Domain = rowStringValue(row, "domain", "hostname")
			asset.Title = rowJoinedValue(row, "title", "portinfo.title")
			asset.Protocol = cleanAssetProtocol(rowStringValue(row, "service"))
			asset.Server = rowJoinedValue(row, "app", "portinfo.app")
			asset.Country = rowStringValue(row, "country")
			asset.City = rowStringValue(row, "city")
		case "quake":
			asset.IP = rowStringValue(row, "ip")
			asset.Port = rowIntValue(row, "port")
			asset.Domain = rowStringValue(row, "service.http.host", "hostname", "domain")
			asset.Title = rowStringValue(row, "service.http.title")
			if asset.Title == "" {
				asset.Title = htmlTitleFromBody(rowStringValue(row, "service.response"))
			}
			asset.Protocol = cleanAssetProtocol(rowStringValue(row, "service.name"))
			asset.Server = rowStringValue(row, "service.http.server")
			asset.Country = rowStringValue(row, "location.country_cn", "country_cn")
			asset.Province = rowStringValue(row, "location.province_cn", "province_cn")
			asset.City = rowStringValue(row, "location.city_cn", "city_cn")
		case "shodan":
			asset.IP = rowStringValue(row, "ip_str", "ip")
			asset.Port = rowIntValue(row, "port")
			asset.Domain = rowFirstOfList(row, "hostnames")
			if asset.Domain == "" {
				asset.Domain = rowFirstOfList(row, "domains")
			}
			asset.Title = rowStringValue(row, "http.title")
			asset.Protocol = cleanAssetProtocol(rowStringValue(row, "transport"))
			asset.Server = rowStringValue(row, "product")
			asset.Country = rowStringValue(row, "location.country_name", "location.country_code", "country")
			asset.City = rowStringValue(row, "location.city")
		case "hunter":
			asset.IP = rowStringValue(row, "ip")
			asset.Port = rowIntValue(row, "port")
			asset.Domain = rowStringValue(row, "domain")
			if asset.Domain == "" {
				if u := rowStringValue(row, "url"); u != "" {
					asset.Domain = urlHostOrEmpty(u)
				}
			}
			asset.Title = rowStringValue(row, "web_title")
			asset.Protocol = cleanAssetProtocol(rowStringValue(row, "protocol"))
		default:
			asset.Host = rowStringValue(row, "host")
			asset.IP = rowStringValue(row, "ip")
			asset.Port = rowIntValue(row, "port")
			asset.Domain = rowStringValue(row, "domain")
			asset.Title = rowStringValue(row, "title")
			asset.Protocol = cleanAssetProtocol(rowStringValue(row, "protocol"))
			asset.Server = rowStringValue(row, "server")
			asset.Country = rowStringValue(row, "country")
			asset.Province = rowStringValue(row, "province")
			asset.City = rowStringValue(row, "city")
		}
		if asset.Host == "" {
			if asset.Domain != "" {
				asset.Host = asset.Domain
			} else if asset.IP != "" {
				asset.Host = asset.IP
			}
		}
		if asset.Host == "" && asset.IP == "" && asset.Domain == "" {
			continue
		}
		assets = append(assets, asset)
	}
	return assets
}