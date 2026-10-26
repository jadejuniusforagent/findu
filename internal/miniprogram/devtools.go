// devtools.go 微信开发者工具 / PC 微信小程序模式的 DevTools 协议通道
// （盘点方案 P0-动态调试通道）。
//
// 走标准 CDP（Chrome DevTools Protocol）:微信开发者工具开启「服务端口」后、
// PC 微信小程序模式暴露的 inspectable 上下文,都提供 /json/list 目标清单与
// 每目标的 webSocketDebuggerUrl。本文件提供:
//  1. 目标枚举(ListDevToolsTargets)——找到小程序 service/content 上下文
//  2. 任意表达式求值(EvaluateOnTarget)——Runtime.evaluate,支持 await
//  3. 内置 hook 预设(HookPresets)——wx.request 录制 / storage 全量导出 /
//     页面栈快照,装一次,后续用读取表达式取数
//
// 这补上「只有重放、改不了运行时」的缺口:前端校验绕过、签名入参观察、
// 限购逻辑篡改都在运行时才可做。仅限已授权测试场景使用。
package miniprogram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// DevToolsTarget /json/list 返回的一个可调试目标。
type DevToolsTarget struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	WebSocketURL string `json:"webSocketDebuggerUrl,omitempty"`
	FavIconURL   string `json:"faviconUrl,omitempty"`
}

// ListDevToolsTargets 枚举调试端点的目标清单。
// 依次尝试 /json/list 与 /json（不同版本端点路径不同）。
func ListDevToolsTargets(ctx context.Context, client *http.Client, baseURL string) ([]DevToolsTarget, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base_url 为空")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var lastErr error
	for _, path := range []string{"/json/list", "/json"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		var targets []DevToolsTarget
		if err := json.Unmarshal(data, &targets); err != nil {
			lastErr = fmt.Errorf("%s 响应不是目标清单(HTTP %d)", path, resp.StatusCode)
			continue
		}
		return targets, nil
	}
	return nil, fmt.Errorf("枚举调试目标失败(端点不可达或服务端口未开启): %w", lastErr)
}

// ResolveTargetURL 从目标清单里挑出调试用 WebSocket URL:
// target 参数匹配 id / title / url 子串(大小写不敏感)。
// 空匹配仅在唯一目标时可用;多命中一律报错列出候选——避免 hook 错上下文。
func ResolveTargetURL(targets []DevToolsTarget, match string) (string, error) {
	match = strings.ToLower(strings.TrimSpace(match))
	var hits []DevToolsTarget
	for _, t := range targets {
		if t.WebSocketURL == "" {
			continue
		}
		if match == "" ||
			strings.Contains(strings.ToLower(t.ID), match) ||
			strings.Contains(strings.ToLower(t.Title), match) ||
			strings.Contains(strings.ToLower(t.URL), match) {
			hits = append(hits, t)
		}
	}
	if len(hits) == 0 {
		return "", fmt.Errorf("没有匹配的可调试目标(先 action=targets 看清单);match=%q", match)
	}
	if len(hits) > 1 {
		names := make([]string, 0, len(hits))
		for _, h := range hits {
			names = append(names, h.Title+"("+h.ID+")")
		}
		return "", fmt.Errorf("match=%q 命中 %d 个目标,请精确指定: %s", match, len(hits), strings.Join(names, " / "))
	}
	return hits[0].WebSocketURL, nil
}

// EvaluateOnTarget 对目标执行一段 JS(Runtime.evaluate,returnByValue+awaitPromise):
// 返回结果值的字符串形态;脚本抛错时返回异常摘要。
func EvaluateOnTarget(ctx context.Context, wsURL, expression string, timeoutSec int) (string, error) {
	if strings.TrimSpace(wsURL) == "" {
		return "", fmt.Errorf("ws url 为空")
	}
	if strings.TrimSpace(expression) == "" {
		return "", fmt.Errorf("expression 为空")
	}
	if timeoutSec < 1 {
		timeoutSec = 10
	}
	if timeoutSec > 120 {
		timeoutSec = 120
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec+5)*time.Second)
	defer cancel()

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return "", fmt.Errorf("连接调试目标失败: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(time.Duration(timeoutSec) * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	const callID = 14001 // 工具内单次调用,固定 id 足够
	call, _ := json.Marshal(map[string]interface{}{
		"id":     callID,
		"method": "Runtime.evaluate",
		"params": map[string]interface{}{
			"expression":    expression,
			"returnByValue": true,
			"awaitPromise":  true,
		},
	})
	if err := conn.WriteMessage(websocket.TextMessage, call); err != nil {
		return "", fmt.Errorf("发送 evaluate 失败: %w", err)
	}

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("等待结果超时或连接中断: %w", err)
		}
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				Result struct {
					Type  string          `json:"type"`
					Value json.RawMessage `json:"value"`
				} `json:"result"`
			} `json:"result"`
			Exception *struct {
				Text   string `json:"text"`
				Detail *struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil || msg.ID != callID {
			continue // 事件帧/其他 id,跳过
		}
		if msg.Exception != nil {
			detail := msg.Exception.Text
			if msg.Exception.Detail != nil && msg.Exception.Detail.Description != "" {
				detail = msg.Exception.Detail.Description
			}
			return "", fmt.Errorf("脚本执行异常: %s", clipDevTools(detail, 2000))
		}
		value := msg.Result.Result.Value
		if len(value) == 0 || string(value) == "null" || msg.Result.Result.Type == "undefined" {
			return "", nil
		}
		// 字符串值去掉引号原样返回;其他 JSON 值序列化保留
		var strVal string
		if json.Unmarshal(value, &strVal) == nil {
			return strVal, nil
		}
		return clipDevTools(string(value), 64*1024), nil
	}
}

func clipDevTools(s string, max int) string {
	if len(s) > max {
		return s[:max] + "\n...[截断]"
	}
	return s
}

// HookPresets 内置 hook 预设:install 是安装表达式,read 是取数表达式。
// 安装幂等(重复安装返回 already),数据挂在 globalThis.__ps_* 下。
type HookPreset struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Install     string `json:"-"`
	ReadExpr    string `json:"-"`
	Description string `json:"description"`
}

// hookWxRequestJS 拦截 wx.request:记录调用入参,complete 后补状态码与响应片段。
const hookWxRequestJS = `(() => {
  if (globalThis.__ps_hooked) return 'already';
  const orig = wx.request;
  globalThis.__ps_req_log = [];
  wx.request = function (opts) {
    const entry = { url: String(opts.url || ''), method: String(opts.method || 'GET'), header: opts.header, data: opts.data, ts: Date.now() };
    globalThis.__ps_req_log.push(entry);
    const done = opts.complete;
    opts.complete = function (res) {
      try {
        entry.status = res && res.statusCode;
        entry.resp = typeof (res && res.data) === 'string' ? res.data.slice(0, 2000) : JSON.stringify(res && res.data).slice(0, 2000);
      } catch (e) { entry.resp = String(e); }
      if (done) done(res);
    };
    return orig.call(wx, opts);
  };
  globalThis.__ps_hooked = true;
  return 'installed';
})()`

// hookStorageJS 一次性导出全部本地 storage(key/value 序列化截断)。
const hookStorageJS = `(() => {
  try {
    const keys = wx.getStorageInfoSync().keys || [];
    const out = keys.map(k => {
      let v; try { v = JSON.stringify(wx.getStorageSync(k)); } catch (e) { v = String(wx.getStorageSync(k)); }
      return { key: k, value: String(v).slice(0, 2000) };
    });
    return JSON.stringify(out);
  } catch (e) { return JSON.stringify({ error: String(e) }); }
})()`

// hookPageStackJS 当前页面栈与 query 参数(路由面快照)。
const hookPageStackJS = `(() => {
  try {
    const stack = getCurrentPages().map(p => ({ route: p.route, options: p.options }));
    return JSON.stringify(stack);
  } catch (e) { return JSON.stringify({ error: String(e) }); }
})()`

// HookPresets 预设清单(wx_request / storage / page_stack)。
func HookPresets() []HookPreset {
	return []HookPreset{
		{
			Name:        "wx_request",
			Title:       "拦截 wx.request(录制全部请求入参/状态码/响应片段)",
			Install:     hookWxRequestJS,
			ReadExpr:    "JSON.stringify(globalThis.__ps_req_log || [])",
			Description: "安装后回到小程序里操作触发请求;再 action=read 取录制结果;clear=true 清空续录",
		},
		{
			Name:        "storage",
			Title:       "一次性导出全部本地 Storage(登录态/缓存明文)",
			Install:     hookStorageJS,
			ReadExpr:    "",
			Description: "安装表达式直接返回全量 JSON,无需 read",
		},
		{
			Name:        "page_stack",
			Title:       "当前页面栈与 query 参数",
			Install:     hookPageStackJS,
			ReadExpr:    "",
			Description: "安装表达式直接返回页面栈 JSON;篡改 options 重入页面可测参数污染",
		},
	}
}

// HookPresetByName 取单个预设。
func HookPresetByName(name string) (HookPreset, bool) {
	for _, p := range HookPresets() {
		if p.Name == strings.TrimSpace(name) {
			return p, true
		}
	}
	return HookPreset{}, false
}

// DevToolsReadWxRequestLog 取 wx_request 录制数据的读取表达式(clear=true 时同时清空)。
func DevToolsReadWxRequestLog(clear bool) string {
	if clear {
		return `(() => { const d = JSON.stringify(globalThis.__ps_req_log || []); globalThis.__ps_req_log = []; return d; })()`
	}
	return `JSON.stringify(globalThis.__ps_req_log || [])`
}
