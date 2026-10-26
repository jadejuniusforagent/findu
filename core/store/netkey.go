package store

import (
	"net"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// 域名/IP 归一化工具（原 db/nkey.go 的根键两件上提）。
//
// 上提原因：DomainKey / RootDomain 是纯文本函数、零 IO，但资产、企业、任务三个域
// 的存储层都要用（资产 upsert 的去重键、company_scope 归属匹配、task scope 解析）。
// 放 core/store 与 UTF8Clean 同口径：跨域共用的落库工具层，不依赖任何域模型。

// DomainKey 归一化 FQDN：小写、去首尾空白、去尾点。
func DomainKey(fqdn string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(fqdn)), ".")
}

// DefaultPort returns the explicit port, falling back to the scheme's well-known
// default (http 80 / https 443 / 其他 scheme 0)。
func DefaultPort(scheme, p string) int {
	if p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	switch scheme {
	case "https":
		return 443
	case "http":
		return 80
	default:
		return 0
	}
}

// RootDomain returns the registrable domain (eTLD+1) for a host and whether the
// host itself IS that apex (§3.1). Edge cases (§3.1 边界处理): an IP literal or a
// host publicsuffix can't classify (localhost / internal / non-ICANN TLD) is
// returned unchanged as its own root with isApex=true — best-effort, never treated
// as a subdomain.
func RootDomain(host string) (root string, isApex bool) {
	h := DomainKey(host)
	if h == "" || net.ParseIP(h) != nil {
		return h, true
	}
	etld1, err := publicsuffix.EffectiveTLDPlusOne(h)
	if err != nil || etld1 == "" {
		return h, true
	}
	return etld1, h == etld1
}
