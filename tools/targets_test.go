package tools

import (
	"encoding/json"
	"testing"
)

func TestParseTarget(t *testing.T) {
	cases := []struct {
		raw   string
		want  TargetKind
		ok    bool
		host  string
	}{
		{"https://api.example.com/x?a=1", TargetKindURL, true, "api.example.com"},
		{"http://10.1.2.3:3000/admin", TargetKindURL, true, "10.1.2.3"},
		{"10.1.2.3:3000", TargetKindDomain, true, "10.1.2.3"},
		{"10.1.2.3", TargetKindIP, true, "10.1.2.3"},
		{"fd00::1", TargetKindIP, true, "fd00::1"},
		{"10.0.0.0/24", TargetKindCIDR, true, "10.0.0.0/24"},
		{"sub.example.com", TargetKindDomain, true, "sub.example.com"},
		{"localhost", TargetKindDomain, true, "localhost"},
		{"example.com:8080", TargetKindDomain, true, "example.com"},
		// 非目标形态按契约拒绝——fail closed 交给显式声明路径，兜底扫描不得误判。
		{"", "", false, ""},
		{"-v", "", false, ""},
		{"/etc/passwd", "", false, ""},
		{"--proxy=http://x", "", false, ""},
	}
	for _, c := range cases {
		got, ok := ParseTarget(c.raw)
		if ok != c.ok || (ok && (got.Kind != c.want || got.Host() != c.host)) {
			t.Errorf("ParseTarget(%q) = (%v,%q,%v) want (%v,%q,%v)", c.raw, got.Kind, got.Host(), ok, c.want, c.host, c.ok)
		}
	}
}

func TestExtractTargetsFromInput(t *testing.T) {
	wrapper := func(js string) json.RawMessage { return json.RawMessage(js) }
	in := wrapper(`{"target":"example.com","args":{"targets":["a.com","10.0.0.1/24","oops"]}}`)
	decls := []TargetDecl{{Field: "target", Kind: "domain"}, {Field: "args.targets", Kind: ""}}
	// 显式声明路径解析失败必须上抛（fail closed），而不是静默跳过。
	if _, err := ExtractTargetsFromInput(in, decls); err == nil {
		t.Fatal("声明字段内的不可解析值必须报错（fail closed）")
	}
	in2 := wrapper(`{"target":"example.com","args":{"targets":["a.com","10.0.0.1/24"]}}`)
	got, err := ExtractTargetsFromInput(in2, decls)
	if err != nil {
		t.Fatalf("ExtractTargetsFromInput: %v", err)
	}
	if len(got) != 3 || got[1].Host() != "a.com" || got[2].Kind != TargetKindCIDR {
		t.Fatalf("got %+v", got)
	}
	// 缺省字段（可选参数未传）= 空结果，不报错。
	if out, err := ExtractTargetsFromInput(wrapper(`{}`), decls); err != nil || len(out) != 0 {
		t.Fatalf("absent fields must yield empty, got %v err=%v", out, err)
	}
}

func TestExtractTargetsFromCommonFields(t *testing.T) {
	in := json.RawMessage(`{"target":"example.com","paths":["/a","/b"],"host":"host.local:22"}`)
	got, err := ExtractTargetsFromCommonFields(in)
	if err != nil {
		t.Fatalf("ExtractTargetsFromCommonFields: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 targets, got %+v", got)
	}
	m := map[string]bool{}
	for _, tg := range got {
		m[tg.Host()] = true
	}
	if m["example.com"] != true || m["host.local"] != true {
		t.Fatalf("want example.com + host.local, got %+v", got)
	}
}
