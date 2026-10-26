package miniprogram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func weChatStub(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "grant_type=client_credential") {
			t.Errorf("grant_type 不符: %s", r.URL.RawQuery)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// TestVerifyAppSecret_Outcomes 微信各错误码 → 状态归一。
func TestVerifyAppSecret_Outcomes(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus string
		wantValid  bool
	}{
		{"有效凭证", `{"access_token":"TKN","expires_in":7200}`, CredStatusValid, true},
		{"appid不存在", `{"errcode":40013,"errmsg":"invalid appid"}`, CredStatusInvalid, false},
		{"secret错误", `{"errcode":40125,"errmsg":"invalid appsecret"}`, CredStatusInvalid, false},
		{"IP白名单", `{"errcode":40164,"errmsg":"invalid ip 1.2.3.4 ipv6 ::, not in whitelist"}`, CredStatusRestricted, false},
		{"额度耗尽", `{"errcode":45009,"errmsg":"reach max api daily quota"}`, CredStatusUnverifiable, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := weChatStub(t, tc.body, http.StatusOK)
			defer srv.Close()
			res := verifyAppSecretAt(context.Background(), srv.Client(), srv.URL, "wx1234abcdef567890", "secret")
			if res.Status != tc.wantStatus || res.Valid != tc.wantValid {
				t.Fatalf("got %+v, want status=%s valid=%v", res, tc.wantStatus, tc.wantValid)
			}
		})
	}
}

// TestVerifyAppSecret_IPBlockedMeansAppIDKnown 40164 是「AppID 真实存在」的强信号,
// 提示信息必须带出这层语义,避免被误读为纯失败。
func TestVerifyAppSecret_IPBlockedMeansAppIDKnown(t *testing.T) {
	srv := weChatStub(t, `{"errcode":40164,"errmsg":"invalid ip 1.2.3.4, not in whitelist"}`, http.StatusOK)
	defer srv.Close()
	res := verifyAppSecretAt(context.Background(), srv.Client(), srv.URL, "wx1234abcdef567890", "secret")
	if !strings.Contains(res.Message, "AppID 真实存在") {
		t.Fatalf("40164 提示未带出 AppID 已确认语义: %s", res.Message)
	}
}

// TestVerifyAppSecret_TokenNotLeaked 成功响应的 access_token 不得出现在结论里（不落库不透出）。
func TestVerifyAppSecret_TokenNotLeaked(t *testing.T) {
	srv := weChatStub(t, `{"access_token":"SUPER_SECRET_TOKEN_VALUE","expires_in":7200}`, http.StatusOK)
	defer srv.Close()
	res := verifyAppSecretAt(context.Background(), srv.Client(), srv.URL, "wx1234abcdef567890", "secret")
	if strings.Contains(res.Message, "SUPER_SECRET") {
		t.Fatalf("结论信息泄露 access_token: %s", res.Message)
	}
}

func TestMaskSecret(t *testing.T) {
	if got := MaskSecret("abcdefghijklmnopqrstuvwxyz123456"); got != "abcd…3456" {
		t.Fatalf("MaskSecret 不符: %s", got)
	}
	if got := MaskSecret("short"); got != "****" {
		t.Fatalf("短值应全掩码: %s", got)
	}
}

func TestAppIDShape(t *testing.T) {
	if !appIDShape.MatchString("wx1234abcdef567890") {
		t.Fatal("标准 AppID 应通过")
	}
	for _, bad := range []string{"wx123", "wx1234abcdef56789g", "wx", "appid"} {
		if appIDShape.MatchString(bad) {
			t.Fatalf("%q 不应通过形态校验", bad)
		}
	}
}

func TestClassifyWeChatError(t *testing.T) {
	if res := classifyWeChatError(40001, "invalid credential"); res.Status != CredStatusInvalid {
		t.Fatalf("40001 应判 invalid: %+v", res)
	}
	if res := classifyWeChatError(-1, "system busy"); res.Status != CredStatusUnverifiable {
		t.Fatalf("未知错误码应判 error: %+v", res)
	}
}
