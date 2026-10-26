package app

// postQueryAdapter —— 接口 POST 化的参数适配中间件（挂 protected 组链首）。
// 背景：GET query 会原样落进访问日志/代理/浏览器历史，资产、IP、目标域名等
// 敏感参数随之暴露。业务查询接口已全部由 GET 改为 POST 注册；前端查询统一走
// POST + JSON body + 标记头 X-Api-Query-Post，本中间件把这类请求的 body 扁平
// 字段注入 URL query → 原 GET handler 的 c.Query / ShouldBindQuery 读取逻辑
// 零改动。miniprogram/apkanalysis 的查询路由（routereg.QueryPOST 注册）依赖
// 此中间件读取参数。
//
// 安全边界：
//   - 白名单硬门禁：只对 routereg.QueryPOST 注册过的查询路由注入；
//   - 仅处理带 X-Api-Query-Post 标记头的请求；
//   - 仅接受 application/json 且 body ≤ 1MB；
//   - 仅注入一层扁平标量参数，嵌套对象与 null 跳过；
//   - 跨站伪造不成立：跨站无法发送 application/json 请求体（fetch JSON 触发
//     CORS 预检）。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"finduai/internal/routereg"

	"github.com/gin-gonic/gin"
)

// QueryPostMarkerHeader 标记「POST 承载查询参数」的请求；与前端 apiPostQuery 对应。
const QueryPostMarkerHeader = "X-Api-Query-Post"

const maxPostQueryBodyBytes = 1 << 20

func postQueryAdapter() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost &&
			routereg.IsQueryRoute(c.FullPath()) &&
			c.GetHeader(QueryPostMarkerHeader) != "" &&
			strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
			if raw, ok := readBodyCapped(c.Request); ok {
				if query, err := flattenJSONToQuery(raw); err == nil {
					mergeQuery(c.Request.URL, query)
					// 纯 POST 化：method 保持 POST 不再改写。读语义（RBAC 读权限
					// 映射）由 security 包经同一张 QueryPOST 白名单查表归一。
				}
				// body 回填：误用到会绑定 body 的接口上时，绑定依然成立
				c.Request.Body = io.NopCloser(bytes.NewReader(raw))
				c.Request.ContentLength = int64(len(raw))
			}
		}
		c.Next()
	}
}

// PostQueryAdapter 暴露 protected 组的「POST 承载查询参数」适配器，供宿主
// 运行时收口桥（Phase R2b）挂同一链：Auth → QueryPOST 适配 → RBAC。
func PostQueryAdapter() gin.HandlerFunc {
	return postQueryAdapter()
}

func readBodyCapped(r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxPostQueryBodyBytes+1))
	if err != nil || len(raw) > maxPostQueryBodyBytes {
		return nil, false
	}
	return raw, true
}

// mergeQuery 把 body 解析出的参数并入 URL query（同名键以 body 为准）。
func mergeQuery(u *url.URL, query url.Values) {
	if len(query) == 0 {
		return
	}
	existing := u.Query()
	for key, values := range query {
		existing[key] = values
	}
	u.RawQuery = existing.Encode()
}

// flattenJSONToQuery 把一层扁平 JSON 对象编码为 url.Values。
// json.Number 原样保留字面量；数组展开为重复键；null 与嵌套对象跳过。
func flattenJSONToQuery(body []byte) (url.Values, error) {
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	values := make(url.Values)
	for key, val := range obj {
		switch typed := val.(type) {
		case nil:
			// 跳过：查询参数无 null 语义
		case string:
			values[key] = []string{typed}
		case bool:
			values[key] = []string{strconv.FormatBool(typed)}
		case json.Number:
			values[key] = []string{typed.String()}
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if s, ok := scalarToString(item); ok {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				values[key] = out
			}
		default:
			// 嵌套对象跳过：查询 handler 只消费标量
		}
	}
	return values, nil
}

func scalarToString(v any) (string, bool) {
	switch typed := v.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case json.Number:
		return typed.String(), true
	default:
		return "", false
	}
}
