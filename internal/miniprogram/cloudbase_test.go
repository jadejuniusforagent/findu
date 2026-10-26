package miniprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 云开发资产提取:env/函数/集合/fileID/匿名痕迹全命中,且按值去重。
func TestAnalyzeCloudBase(t *testing.T) {
	ws := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(ws, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.js", `
wx.cloud.init({ env: 'prod-env-8g123' });
wx.cloud.init({ env: 'prod-env-8g123' });
db.collection('orders').where({userId: 1}).get();
db.collection('users').get();
`)
	write("pages/order/order.js", `
wx.cloud.callFunction({ name: 'createOrder', data: {amount: 1} }).then();
cloud.callFunction({
  name: 'queryOrder',
  data: {}
});
const anon = tcb.auth.signInAnonymously();
`)
	write("pages/share/share.js", `const fid = 'cloud://prod-env-8g123.6pro-id/share.png';`)

	inv, err := AnalyzeCloudBase(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Envs) != 1 || inv.Envs[0].Value != "prod-env-8g123" {
		t.Fatalf("env 提取异常: %+v", inv.Envs)
	}
	if len(inv.Functions) != 2 {
		t.Fatalf("云函数应命中 2 个(createOrder/queryOrder),得到 %+v", inv.Functions)
	}
	names := map[string]bool{}
	for _, f := range inv.Functions {
		names[f.Value] = true
	}
	if !names["createOrder"] || !names["queryOrder"] {
		t.Fatalf("函数名缺失: %+v", names)
	}
	if len(inv.Collections) != 2 {
		t.Fatalf("集合应命中 2 个,得到 %+v", inv.Collections)
	}
	if len(inv.FileIDs) != 1 || !strings.HasPrefix(inv.FileIDs[0].Value, "cloud://prod-env-8g123") {
		t.Fatalf("fileID 提取异常: %+v", inv.FileIDs)
	}
	if len(inv.Anonymous) != 1 {
		t.Fatalf("匿名登录痕迹应命中 1 处,得到 %+v", inv.Anonymous)
	}
}

func TestAnalyzeCloudBaseEmpty(t *testing.T) {
	inv, err := AnalyzeCloudBase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Functions)+len(inv.Envs) != 0 {
		t.Fatalf("空目录应得空清单: %+v", inv)
	}
	drafts := buildCloudBaseDrafts(inv)
	if len(drafts) != 0 {
		t.Fatalf("空清单不应生成测试项: %+v", drafts)
	}
}

func TestBuildCloudBaseDrafts(t *testing.T) {
	inv := &CloudBaseInventory{
		Envs:      []CloudRef{{Value: "env1", File: "app.js", Line: 1}},
		Functions: []CloudRef{{Value: "createOrder", File: "a.js", Line: 2}, {Value: "queryOrder", File: "b.js", Line: 3}},
		FileIDs:   []CloudRef{{Value: "cloud://x/y", File: "c.js", Line: 4}},
	}
	drafts := buildCloudBaseDrafts(inv)
	sources := map[string]bool{}
	for _, d := range drafts {
		sources[d.Source] = true
	}
	// 函数逐个一项;fileID 有一项;env 有但无匿名痕迹 → 无 anonymous 项
	if !sources["cloudbase:function:createOrder"] || !sources["cloudbase:function:queryOrder"] {
		t.Fatalf("函数测试项缺失: %+v", sources)
	}
	if !sources["cloudbase:storage"] {
		t.Fatalf("云存储测试项缺失: %+v", sources)
	}
	if sources["cloudbase:anonymous"] {
		t.Fatalf("无匿名痕迹不应生成 anonymous 项")
	}
}
