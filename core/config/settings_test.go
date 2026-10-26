package config

import "testing"

func TestLoadSettingsVulnKBDir(t *testing.T) {
	t.Setenv("FINDU_VULNKB_DIR", "  /srv/vulnkb  ")
	if got := LoadSettings().VulnKBDir; got != "/srv/vulnkb" {
		t.Fatalf("去空白: got %q", got)
	}

	t.Setenv("FINDU_VULNKB_DIR", "/other")
	if got := LoadSettings().VulnKBDir; got != "/other" {
		t.Fatalf("普通取值: got %q", got)
	}

	t.Setenv("FINDU_VULNKB_DIR", "")
	if got := LoadSettings().VulnKBDir; got != "" {
		t.Fatalf("均未设置应为空: got %q", got)
	}
}

func TestLoadSettingsSideQuestionMaxTokens(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"abc", 0},
		{"255", 0},
		{"256", 256},
		{"4096", 4096},
		{"32768", 32768},
		{"32769", 0},
		{"-1", 0},
	}
	for _, c := range cases {
		t.Setenv("FINDU_BTW_MAX_OUTPUT_TOKENS", c.raw)
		if got := LoadSettings().SideQuestionMaxTokens; got != c.want {
			t.Fatalf("raw=%q got %d want %d", c.raw, got, c.want)
		}
	}
}

func TestLoadSettingsLLMSection(t *testing.T) {
	for _, k := range []string{
		"FINDU_LLM_PROVIDER", "FINDU_LLM_MODEL", "FINDU_LLM_BASE_URL",
		"FINDU_LLM_PROXY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "FINDU_LLM_STREAM",
	} {
		t.Setenv(k, "")
	}

	llm := LoadSettings().LLM
	if llm.Provider != "" || llm.Model != "" || llm.BaseURL != "" || llm.Proxy != "" ||
		llm.AnthropicKey != "" || llm.OpenAIKey != "" {
		t.Fatalf("全空时应为零值: %+v", llm)
	}
	if !llm.Stream {
		t.Fatal("未设置 FINDU_LLM_STREAM 时 Stream 必须默认 true")
	}

	t.Setenv("FINDU_LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "sk-oai")
	t.Setenv("ANTHROPIC_API_KEY", "sk-anth")
	t.Setenv("FINDU_LLM_PROXY", "  socks5://127.0.0.1:7897  ")
	llm = LoadSettings().LLM
	if llm.Provider != "openai" || llm.OpenAIKey != "sk-oai" || llm.AnthropicKey != "sk-anth" {
		t.Fatalf("provider/key 解析错误: %+v", llm)
	}
	if llm.Proxy != "socks5://127.0.0.1:7897" {
		t.Fatalf("Proxy 应去首尾空白: %q", llm.Proxy)
	}

	for _, off := range []string{"0", "false", "off", "no", "OFF"} {
		t.Setenv("FINDU_LLM_STREAM", off)
		if LoadSettings().LLM.Stream {
			t.Fatalf("FINDU_LLM_STREAM=%q 应关闭流式", off)
		}
	}
	t.Setenv("FINDU_LLM_STREAM", "1")
	if !LoadSettings().LLM.Stream {
		t.Fatal("FINDU_LLM_STREAM=1 应保持流式")
	}
}

func TestLoadSettingsFinduSwitches(t *testing.T) {
	// findu 子系统开关
	t.Setenv("FINDU_ENABLE", "1")
	if !LoadSettings().FinduEnable {
		t.Fatal("FINDU_ENABLE=1 应开启")
	}
	t.Setenv("FINDU_ENABLE", "0")
	if LoadSettings().FinduEnable {
		t.Fatal("FINDU_ENABLE=0 应关闭")
	}

	// 跳过机器人只认 1（与原语义一致，true 不算）
	t.Setenv("FINDU_SKIP_ROBOTS", "true")
	if LoadSettings().FinduSkipRobots {
		t.Fatal("FINDU_SKIP_ROBOTS=true 不应视为开启（只认 1）")
	}
	t.Setenv("FINDU_SKIP_ROBOTS", "1")
	if !LoadSettings().FinduSkipRobots {
		t.Fatal("FINDU_SKIP_ROBOTS=1 应开启")
	}

	// 其余字符串项去空白
	t.Setenv("FINDU_KEY_DIR", "  /keys  ")
	t.Setenv("FINDU_CONFIG", " /etc/findu.yaml ")
	t.Setenv("FINDU_DATA_DIR", " /data-findu ")
	t.Setenv("FINDU_DB_DSN", " postgres://x@y/z ")
	t.Setenv("FINDU_WEB_DIR", " web-findu ")
	st := LoadSettings()
	if st.KeyDir != "/keys" || st.FinduConfig != "/etc/findu.yaml" || st.FinduDataDir != "/data-findu" ||
		st.FinduDBDSN != "postgres://x@y/z" || st.FinduWebDir != "web-findu" {
		t.Fatalf("字符串项去空白失败: %+v", st)
	}

	// 通知逃生口
	t.Setenv("FINDU_NOTIFY_ALLOW_LOCAL", "1")
	if !LoadSettings().NotifyAllowLocal {
		t.Fatal("FINDU_NOTIFY_ALLOW_LOCAL=1 应允许")
	}
	t.Setenv("FINDU_NOTIFY_ALLOW_LOCAL", "")
	if LoadSettings().NotifyAllowLocal {
		t.Fatal("未设置时默认不允许")
	}
}
