package miniprogram

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildInterfaceWorkbench(t *testing.T) {
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

	// 带鉴权头 + 显式 method + data 参数的登录态接口。
	write("pages/user.js", `
wx.request({
  url: "https://api.acme-shop.cn/user/list",
  method: "POST",
  data: { page: 1, size: 20 },
  header: { Authorization: "Bearer " + getApp().token }
});
`)

	// 动态数字段的订单接口 → 越权候选，且未带鉴权头。
	write("pages/order.js", `
wx.request({ url: "https://api.acme-shop.cn/order/" + orderId, method: "GET" });
fetch("https://api.acme-shop.cn/order/" + orderId);
`)

	// 管理后台配置接口 → 未授权候选。
	write("pages/admin.js", `
fetch("https://api.acme-shop.cn/admin/config?env=prod");
`)

	// 动态拼接 URL → 仅保留域名级，不算越权。
	write("pages/dyn.js", `
wx.request({ url: "https://cdn.acme-shop.cn/" + path + "/file", method: "POST" });
`)

	res := BuildInterfaceWorkbench(dir)
	if res == nil {
		t.Fatal("res nil")
	}

	find := func(suffix string) *InterfaceItem {
		for i := range res.Interfaces {
			if pathSuffix(res.Interfaces[i].URL, suffix) {
				return &res.Interfaces[i]
			}
		}
		return nil
	}

	if it := find("user/list"); it == nil {
		t.Fatalf("缺少 user/list 接口，实际: %+v", res.Interfaces)
	} else {
		if it.Method != "POST" {
			t.Errorf("user/list 期望 method POST，得到 %s", it.Method)
		}
		if !it.HasAuth {
			t.Error("user/list 期望 HasAuth=true（带 Authorization）")
		}
		if !containsStr(it.Params, "page") {
			t.Errorf("user/list 期望含 data 参数 page，得到 %v", it.Params)
		}
		if it.IDOR {
			t.Error("user/list 不应判定为 IDOR")
		}
	}

	if it := find("order"); it == nil {
		t.Fatalf("缺少 order 接口，实际: %+v", res.Interfaces)
	} else if !it.IDOR {
		t.Errorf("order 接口（数字段）期望 IDOR=true，得到 IDOR=%v", it.IDOR)
	}

	if it := find("admin/config"); it == nil {
		t.Fatalf("缺少 admin/config 接口")
	} else if !it.Unauth {
		t.Errorf("admin/config 期望 Unauth=true，得到 Unauth=%v", it.Unauth)
	}

	if res.Stats["interfaces"] < 3 {
		t.Errorf("期望 >=3 个接口，得到 %d: %v", res.Stats["interfaces"], res.Stats)
	}
	if res.Stats["idor"] < 1 {
		t.Errorf("期望 >=1 个越权候选，得到 %d", res.Stats["idor"])
	}
	if res.Stats["unauth"] < 1 {
		t.Errorf("期望 >=1 个未授权候选，得到 %d", res.Stats["unauth"])
	}
	if len(res.Domains) < 2 {
		t.Errorf("期望 >=2 个域名（api/cdn），得到 %v", res.Domains)
	}
}

func pathSuffix(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}