package miniprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用无头环境直接验证:真实微信 4.x radium 目录里的 V1MMWX 包能否被平台解密+解包。
// 临时诊断用(验证后删除)。
func TestRadiumRealPackageDecryptAndParse(t *testing.T) {
	root := "/mnt/c/Users/c1ay/AppData/Roaming/Tencent/xwechat/radium/users"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skip("radium 目录不可达(非本机环境):", err)
	}
	verified := 0
	for _, acct := range entries {
		pkgRoot := filepath.Join(root, acct.Name(), "applet", "packages")
		apps, err := os.ReadDir(pkgRoot)
		if err != nil {
			continue
		}
		for _, app := range apps {
			appid := app.Name()
			if !strings.HasPrefix(appid, "wx") {
				continue
			}
			versions, _ := os.ReadDir(filepath.Join(pkgRoot, appid))
			for _, ver := range versions {
				main := filepath.Join(pkgRoot, appid, ver.Name(), "__APP__.wxapkg")
				data, err := os.ReadFile(main)
				if err != nil {
					continue
				}
				if !IsEncrypted(data) {
					t.Logf("%s %s: 未加密(跳过解密验证)", appid, ver.Name())
					continue
				}
				dec, err := DecryptWxapkg(data, appid)
				if err != nil {
					t.Errorf("%s/%s 解密失败: %v", appid, ver.Name(), err)
					continue
				}
				if dec[0] != 0xBE {
					t.Errorf("%s/%s 解密后魔数异常: %02x", appid, ver.Name(), dec[0])
					continue
				}
				pkg, err := ParseWxapkg(dec, "__APP__.wxapkg")
				if err != nil {
					t.Errorf("%s/%s 解析失败: %v", appid, ver.Name(), err)
					continue
				}
				t.Logf("%s %s: 解密+解析成功, %d 个文件", appid, ver.Name(), len(pkg.Files))
				verified++
				if verified >= 5 {
					return
				}
			}
		}
	}
	if verified == 0 {
		t.Skip("未验证到任何包")
	}
}
