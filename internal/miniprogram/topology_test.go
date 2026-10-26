package miniprogram

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeTopology(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("app.js", `
wx.cloud.callFunction({ name: "login" });
`)

	write("pages/index.js", `
Page({
  onLoad() {
    wx.navigateTo({ url: "/pages/detail?id=1" });
    wx.request({ url: "https://api.example.com/user/list" });
    wx.request({ url: "https://api.example.com/user/list?page=2" });
  }
});
`)

	write("pages/detail.js", `
Page({
  onLoad() {
    wx.redirectTo({ url: "/pages/index" });
    fetch("https://api.example.com/order/detail");
  }
});
`)

	res := AnalyzeTopology(dir)
	if res == nil {
		t.Fatal("res is nil")
	}

	nodeIDs := map[string]bool{}
	var pages, apis, cloudfns int
	for _, n := range res.Nodes {
		nodeIDs[n.ID] = true
		switch n.Type {
		case "page":
			pages++
		case "api":
			apis++
		case "cloudfn":
			cloudfns++
		}
	}

	if pages < 2 {
		t.Errorf("期望 >=2 个 page 节点，得到 %d", pages)
	}
	// 接口 URL 归一化后应收敛为 2 个（/user/list、/order/detail）。
	if apis < 2 {
		t.Errorf("期望 >=2 个 api 节点（URL 归一化收敛），得到 %d", apis)
	}
	if cloudfns < 1 {
		t.Errorf("期望 >=1 个 cloudfn 节点，得到 %d", cloudfns)
	}

	// 引用热度：/user/list 至少被 1 个文件引用（同文件重复调用按文件级去重）。
	for _, n := range res.Nodes {
		if n.Type == "api" && n.Detail != nil && n.Detail["url"] == "https://api.example.com/user/list" {
			if n.RefCount < 1 {
				t.Errorf("期望 /user/list 的 RefCount>=1，得到 %d", n.RefCount)
			}
		}
	}

	// 边：cloudfn(1) + navigate(2) + api_call(2)。
	if len(res.Edges) < 5 {
		t.Errorf("期望 >=5 条边（1 云函数 + 2 跳转 + 2 接口），得到 %d", len(res.Edges))
	}
	if res.Stats["page"] < 2 || res.Stats["api"] < 2 || res.Stats["cloudfn"] < 1 {
		t.Errorf("stats 异常: %v", res.Stats)
	}

	if !nodeIDs["page:pages/detail"] {
		t.Error("期望存在页面节点 page:pages/detail")
	}
}