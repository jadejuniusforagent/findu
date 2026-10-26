package miniprogram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// 目标枚举:/json/list 可用 / /json 回退 / 双双失效报错。
func TestListDevToolsTargets(t *testing.T) {
	serve := func(path string, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		}))
	}
	list := `[{"id":"svc1","title":"小程序 service","type":"service","url":"wx1234567890abcdef/","webSocketDebuggerUrl":"ws://127.0.0.1:9420/devtools/page/svc1"}]`

	s1 := serve("/json/list", list)
	defer s1.Close()
	targets, err := ListDevToolsTargets(t.Context(), s1.Client(), s1.URL)
	if err != nil || len(targets) != 1 || targets[0].ID != "svc1" {
		t.Fatalf("list 路径枚举失败: %v %+v", err, targets)
	}

	s2 := serve("/json", list)
	defer s2.Close()
	targets, err = ListDevToolsTargets(t.Context(), s2.Client(), s2.URL)
	if err != nil || len(targets) != 1 {
		t.Fatalf("/json 回退枚举失败: %v", err)
	}

	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer s3.Close()
	if _, err := ListDevToolsTargets(t.Context(), s3.Client(), s3.URL); err == nil {
		t.Fatal("非 JSON 响应应报错")
	}
}

func TestResolveTargetURL(t *testing.T) {
	targets := []DevToolsTarget{
		{ID: "svc1", Title: "小程序 service", URL: "pages/index/index", WebSocketURL: "ws://x/1"},
		{ID: "svc2", Title: "页面 pages/order", URL: "pages/order/order", WebSocketURL: "ws://x/2"},
	}
	if u, err := ResolveTargetURL(targets, "order"); err != nil || u != "ws://x/2" {
		t.Fatalf("子串匹配失败: %v %q", err, u)
	}
	// 空匹配在多目标时同样必须拒绝(避免 hook 错上下文)
	if _, err := ResolveTargetURL(targets, ""); err == nil || !strings.Contains(err.Error(), "精确指定") {
		t.Fatalf("空匹配多目标应报错: %v", err)
	}
	single := targets[:1]
	if u, err := ResolveTargetURL(single, ""); err != nil || u != "ws://x/1" {
		t.Fatalf("空匹配唯一目标应直接可用: %v %q", err, u)
	}
	if _, err := ResolveTargetURL(targets, "不存在"); err == nil {
		t.Fatal("无命中应报错")
	}
	// 多命中必须拒绝(hook 错上下文是不可接受事故)
	if _, err := ResolveTargetURL(targets, "pages"); err == nil || !strings.Contains(err.Error(), "精确指定") {
		t.Fatalf("多命中应报错: %v", err)
	}
}

// fakeCDPServer 模拟 CDP 目标:upgrade 后对 Runtime.evaluate 回显固定 value。
func fakeCDPServer(t *testing.T, handler func(msg map[string]interface{}) interface{}) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]interface{}
			if json.Unmarshal(raw, &msg) != nil {
				continue
			}
			value := handler(msg)
			resp, _ := json.Marshal(map[string]interface{}{
				"id":     msg["id"],
				"result": map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": value}},
			})
			if conn.WriteMessage(websocket.TextMessage, resp) != nil {
				return
			}
		}
	}))
}

func TestEvaluateOnTarget(t *testing.T) {
	srv := fakeCDPServer(t, func(msg map[string]interface{}) interface{} {
		if msg["method"] != "Runtime.evaluate" {
			return "wrong-method"
		}
		params, _ := msg["params"].(map[string]interface{})
		if params == nil || params["expression"] == "" {
			return "no-expression"
		}
		return "hook-installed"
	})
	defer srv.Close()
	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://")

	got, err := EvaluateOnTarget(t.Context(), wsURL, `(() => 'x')()`, 5)
	if err != nil || got != "hook-installed" {
		t.Fatalf("evaluate 失败: %v %q", err, got)
	}
	if _, err := EvaluateOnTarget(t.Context(), wsURL, "", 5); err == nil {
		t.Fatal("空表达式应报错")
	}
	if _, err := EvaluateOnTarget(t.Context(), "", "1+1", 5); err == nil {
		t.Fatal("空 ws url 应报错")
	}
}

// 内置预设完备性:三个预设都有安装表达式,取数表达式约定成立。
func TestHookPresets(t *testing.T) {
	presets := HookPresets()
	if len(presets) != 3 {
		t.Fatalf("预设数 = %d", len(presets))
	}
	byName := map[string]HookPreset{}
	for _, p := range presets {
		if strings.TrimSpace(p.Install) == "" {
			t.Fatalf("预设 %s 缺安装表达式", p.Name)
		}
		byName[p.Name] = p
	}
	for _, want := range []string{"wx_request", "storage", "page_stack"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("缺预设 %s", want)
		}
	}
	if !strings.Contains(byName["wx_request"].Install, "wx.request") {
		t.Fatal("wx_request 预设应拦截 wx.request")
	}
	if !strings.Contains(byName["wx_request"].ReadExpr, "__ps_req_log") {
		t.Fatal("wx_request 读取表达式应指向录制缓冲")
	}
	if !strings.Contains(DevToolsReadWxRequestLog(true), "= []") {
		t.Fatal("clear 读取表达式应清空缓冲")
	}
	if _, ok := HookPresetByName("不存在"); ok {
		t.Fatal("未知预设应返回 false")
	}
}
