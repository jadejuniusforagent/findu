package apkanalysis

// ingest.go —— 样本收编的路径解析与格式校验（纯函数，便于单测）。
//
// 背景：Agent 自取的包（exec 里下载、split 合并、重打包产物、从设备拉取）落在
// 会话工作区，此前 android_analyze 只接受对话附件路径（chat_uploads），导致这些包
// 收编不进来、安卓分析页看不到。此处把「允许收编的根目录」显式化：调用方（app 层）
// 按当前会话的项目边界给出根目录清单，本包只做词法判定与格式校验，不感知 ctx。
//
// 边界定位与 security.WorkspaceCrossProjectGuard 同级：Abs+Clean+前缀比对，
// 不解析符号链接——防误操作与跨项目串写，不是对抗性沙箱。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"finduai/internal/security"
)

// IngestRoot 允许收编的根目录：Dir 绝对路径，Source 写库来源标记，Label 报错信息里的可读名。
type IngestRoot struct {
	Dir    string
	Source string
	Label  string
}

// ResolveIngestFile 把 Agent 传入的样本路径解析为可收编的绝对路径。
//   - raw 支持绝对路径，也支持相对路径（按 relativeBases 顺序解析，通常依次是
//     会话工作区、进程 cwd——Agent 的 cwd 就是会话工作区）；
//   - 解析结果必须落在某个 root 内，否则报错并列出允许目录；
//   - 必须是常规文件且通过 ZIP 魔数校验（APK/XAPK/APKS 均为 ZIP 容器）。
//
// 返回的 source 为命中根目录标记（chat / workspace / conversation_artifact 等）。
func ResolveIngestFile(raw string, relativeBases []string, roots []IngestRoot) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf("路径为空")
	}

	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("路径非法: %v", err)
	}
	if !filepath.IsAbs(trimmed) {
		candidates := make([]string, 0, len(relativeBases))
		for _, base := range relativeBases {
			base = strings.TrimSpace(base)
			if base == "" {
				continue
			}
			candidate, cerr := filepath.Abs(filepath.Join(base, trimmed))
			if cerr != nil {
				continue
			}
			candidates = append(candidates, candidate)
			if filepathIsRegularFile(candidate) {
				abs = candidate
				break
			}
		}
		if len(candidates) == 0 {
			return "", "", fmt.Errorf("相对路径 %q 无法解析：当前会话没有可用工作区，请传绝对路径", raw)
		}
		if !filepathIsRegularFile(abs) {
			return "", "", fmt.Errorf("文件不存在或不是常规文件: %s（相对路径按会话工作区解析，已尝试: %s）",
				raw, strings.Join(candidates, ", "))
		}
	}

	matched, ok := matchIngestRoot(abs, roots)
	if !ok {
		return "", "", fmt.Errorf("路径不在可收编目录内: %s；允许的目录: %s", abs, ingestRootLabels(roots))
	}
	if !filepathIsRegularFile(abs) {
		return "", "", fmt.Errorf("文件不存在或不是常规文件: %s", abs)
	}
	if !LooksLikeZipFile(abs) {
		return "", "", fmt.Errorf("文件不是 APK/ZIP 格式（zip 魔数校验失败）: %s", abs)
	}
	return abs, matched.Source, nil
}

// matchIngestRoot 返回包含 abs 的根目录（同层多命中取最长的那个，避免 /a 覆盖 /a/b）。
func matchIngestRoot(abs string, roots []IngestRoot) (IngestRoot, bool) {
	best := IngestRoot{}
	found := false
	for _, r := range roots {
		if strings.TrimSpace(r.Dir) == "" || !security.PathWithin(r.Dir, abs) {
			continue
		}
		if !found || len(r.Dir) > len(best.Dir) {
			best, found = r, true
		}
	}
	return best, found
}

func ingestRootLabels(roots []IngestRoot) string {
	labels := make([]string, 0, len(roots))
	for _, r := range roots {
		label := strings.TrimSpace(r.Label)
		if label == "" {
			label = r.Dir
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, "；")
}

// LooksLikeZipFile 校验文件头是 ZIP 魔数（PK\x03\x04 / 空归档 PK\x05\x06）。
// 不解析包结构——只挡目录、文本与半截下载文件，真正的解包校验由分析阶段负责。
func LooksLikeZipFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	n, err := f.Read(head)
	if err != nil || n < 4 {
		return false
	}
	if head[0] != 'P' || head[1] != 'K' {
		return false
	}
	return (head[2] == 0x03 && head[3] == 0x04) || (head[2] == 0x05 && head[3] == 0x06)
}

func filepathIsRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
