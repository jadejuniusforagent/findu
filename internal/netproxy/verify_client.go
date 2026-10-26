// verify_client.go 验证类出网客户端：按出口模式构造 *http.Client。
//
//   - NormalizeEgressMode / EffectiveEgressMode 语义逐行保留；
//   - VerificationClient 保留 proxy/direct 两种模式；proxy 模式若平台出口代理
//     （SetDefaultEgressURL）未配置，回退进程默认 transport。完整多出口路由/故障切换
//     （egress Manager）不在本文件范围。
//
// 供凭证验证（miniprogram 登录态/云开发、cloudkeys、llmkeys）单次只读出网共用。
package netproxy

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	egressMu  sync.RWMutex
	egressURL string
)

// SetDefaultEgressURL 配置平台出口代理 URL（空串清除，回退进程默认出口）。
// 供宿主装配时注入；未调用时行为与「未配置平台代理」一致。
func SetDefaultEgressURL(u string) {
	egressMu.Lock()
	defer egressMu.Unlock()
	egressURL = strings.TrimSpace(u)
}

func activeEgressURL() string {
	egressMu.RLock()
	defer egressMu.RUnlock()
	return egressURL
}

// NormalizeEgressMode 归一化出口模式参数：proxy/direct 之外的值（含空）返回 ""，
// 表示"按记录保存的方式，缺省 proxy"。
func NormalizeEgressMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "proxy" || m == "direct" {
		return m
	}
	return ""
}

// EffectiveEgressMode 计算生效出口：请求级 override 优先，其次记录保存值，缺省 proxy。
func EffectiveEgressMode(override, stored string) string {
	if m := NormalizeEgressMode(override); m != "" {
		return m
	}
	if m := NormalizeEgressMode(stored); m != "" {
		return m
	}
	return "proxy"
}

// VerificationClient 按出口模式构造验证用客户端。超时由调用方指定。
func VerificationClient(mode string, timeout time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	switch EffectiveEgressMode(mode, "") {
	case "direct":
		// 显式直连：剥掉环境代理，避免 http_proxy 悄悄改道
		t.Proxy = nil
	default:
		// 平台出口代理优先；未配置时保留进程默认（环境代理或直连）
		if u := activeEgressURL(); u != "" {
			if pu, err := url.Parse(u); err == nil {
				t.Proxy = http.ProxyURL(pu)
			}
		}
	}
	return &http.Client{Transport: t, Timeout: timeout}
}
