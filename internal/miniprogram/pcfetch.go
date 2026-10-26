// pcfetch.go PC 微信小程序包直取（盘点方案 P2-取包渠道,成本最低优先项）。
//
// 微信 PC 端会把打开过的小程序包缓存在本地,两种已知布局:
//   - 微信 4.x radium 新架构: <用户目录>/AppData/Roaming/Tencent/xwechat/radium/
//     users/<账号>/applet/packages/<appid>/<版本>/__APP__.wxapkg + _subpackages_*.wxapkg（V1MMWX 加密）
//   - 微信 3.x 旧架构: <用户目录>/Documents/WeChat Files/Applet/<appid>/<hash>/*.wxapkg
//
// WSL 部署下 Windows 用户目录经 /mnt/<盘>/Users/... 直接可达,无需 root 即可取包
// （AppID 就是包所在目录名,正好满足 V1MMWX 解密入参）。扫描只读;注册（解密/解包/
// 敏感扫描）由 app 层调 Service.RegisterFromFiles 完成。
package miniprogram

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PCFetchMaxFiles 单次 harvest 拉取注册的包数上限（防止把整个微信缓存全部灌进工作区）。
const PCFetchMaxFiles = 200

// pcPackagePatterns 已知的小程序包缓存目录布局（相对用户主目录）。
// 目录倒数第二段须形如 appid（wx+16hex）才视为包目录。
var pcPackagePatterns = []string{
	// 微信 4.x radium:users/<账号>/applet/packages/<appid>/<版本>/
	filepath.Join("AppData", "Roaming", "Tencent", "xwechat", "radium", "users", "*", "applet", "packages", "*", "*"),
	// 微信 3.x:Documents/WeChat Files/Applet/<appid>/<hash>/
	filepath.Join("Documents", "WeChat Files", "Applet", "*", "*"),
}

// PCPackageFile 一个 wxapkg 缓存文件。
type PCPackageFile struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"mod_time"`
	Encrypted bool      `json:"encrypted"`
}

// PCAppPackage 一个 appid 名下的全部缓存包（多版本/多账号平铺列出）。
type PCAppPackage struct {
	AppID string          `json:"appid"`
	Files []PCPackageFile `json:"files"`
}

// PCDefaultScanRoots 默认扫描根:WSL 挂载盘的 Windows 用户目录。
// 可用环境变量 MINIPROGRAM_PC_SCAN_ROOTS（os.PathListSeparator 分隔）追加或
// 由工具入参 root 覆盖;根下按 pcPackagePatterns 匹配,不存在静默跳过。
func PCDefaultScanRoots() []string {
	var roots []string
	if env := strings.TrimSpace(os.Getenv("MINIPROGRAM_PC_SCAN_ROOTS")); env != "" {
		roots = append(roots, splitNonEmpty(env, os.PathListSeparator)...)
	}
	// WSL: /mnt/<盘>/Users/*（含大小写变体盘符;非 WSL 环境该 glob 落空即跳过）
	for _, mount := range globSorted("/mnt/*") {
		roots = append(roots, globSorted(filepath.Join(mount, "Users", "*"))...)
	}
	return dedup(roots)
}

// ScanPCPackages 扫描各根下的小程序包缓存,按 appid 聚合返回（文件按修改时间倒序）。
// 单包只读前 6 字节判加密标记;目录不可达一律跳过——扫描结果为空不是错误。
func ScanPCPackages(roots []string) []PCAppPackage {
	groups := map[string][]PCPackageFile{}
	scanned := 0
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		for _, pattern := range pcPackagePatterns {
			if scanned >= PCFetchMaxFiles*10 {
				break
			}
			for _, dir := range globSorted(filepath.Join(root, pattern)) {
				if scanned >= PCFetchMaxFiles*10 {
					break
				}
				appid := filepath.Base(filepath.Dir(dir))
				if !appIDShape.MatchString(appid) {
					continue
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if e.IsDir() || scanned >= PCFetchMaxFiles*10 {
						continue
					}
					if !strings.EqualFold(filepath.Ext(e.Name()), ".wxapkg") {
						continue
					}
					full := filepath.Join(dir, e.Name())
					info, err := e.Info()
					if err != nil {
						continue
					}
					scanned++
					groups[appid] = append(groups[appid], PCPackageFile{
						Path:      full,
						Name:      e.Name(),
						Size:      info.Size(),
						ModTime:   info.ModTime(),
						Encrypted: fileLooksEncrypted(full),
					})
				}
			}
		}
	}
	out := make([]PCAppPackage, 0, len(groups))
	for appid, files := range groups {
		sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
		out = append(out, PCAppPackage{AppID: appid, Files: files})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	return out
}

// fileLooksEncrypted 读文件前 6 字节判 V1MMWX 加密标记（读失败按明文处理,不阻断扫描）。
func fileLooksEncrypted(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, len(wxapkgEncPrefix))
	n, err := f.Read(head)
	if err != nil || n < len(head) {
		return false
	}
	return IsEncrypted(head)
}

func globSorted(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

func splitNonEmpty(s string, sep rune) []string {
	var out []string
	for _, part := range strings.Split(s, string(sep)) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
