package miniprogram

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// wxapkg 二进制格式（微信小程序包，经真实包实测确认）:
//
//	偏移 0      1 字节   起始标记 0xBE
//	偏移 1-4    4 字节   info1（大端，恒为 0）
//	偏移 5-8    4 字节   索引区长度（大端，含 4 字节文件数）
//	偏移 9-12   4 字节   数据区长度（大端）
//	偏移 13     1 字节   结束标记 0xED
//	偏移 14-17  4 字节   文件数量（大端）—— 索引区第一个字段
//	偏移 18+    文件条目区，每条目:
//	             4 字节 文件名长度（大端）
//	             N 字节 文件名（UTF-8）
//	             4 字节 数据偏移（大端，相对文件起始的绝对偏移）
//	             4 字节 数据大小（大端）
//
// 加密包（PC 微信 4.x）：以 "V1MMWX" 开头，解密方案（需 appid）:
//
//	前 1024 字节: AES-256-CBC，key = PBKDF2-HMAC-SHA1(appid, "saltiest", 1000, 32)，IV = "the iv: 16 bytes"
//	其余字节:     与 appid 倒数第 2 个字符逐字节 XOR
//	明文 = 前 1023 字节（第 1024 字节为占位）|| 解出的尾部
const (
	wxapkgFirstMark = 0xBE
	wxapkgLastMark  = 0xED
	wxapkgHeaderLen = 14
	wxapkgIndexMin  = 18 // 头部 + 文件数字段
)

var wxapkgEncPrefix = []byte("V1MMWX")

// WxapkgFile 单个文件条目
type WxapkgFile struct {
	Name   string `json:"name"`
	Offset uint32 `json:"offset"`
	Size   uint32 `json:"size"`
}

// WxapkgPackage 解析后的 wxapkg 包结构
type WxapkgPackage struct {
	FileName  string       `json:"file_name"`
	Encrypted bool         `json:"encrypted"`
	Files     []WxapkgFile `json:"files"`
	Data      []byte       `json:"-"`
}

// IsEncrypted 检测 wxapkg 数据是否为加密格式（V1MMWX 前缀）
func IsEncrypted(data []byte) bool {
	if len(data) < len(wxapkgEncPrefix) {
		return false
	}
	return string(data[:len(wxapkgEncPrefix)]) == string(wxapkgEncPrefix)
}

// ParseWxapkg 解析 wxapkg 二进制格式，返回文件索引列表。
// 加密包返回明确错误提示（需带 appid 解密，见 DecryptWxapkg）。
func ParseWxapkg(data []byte, fileName string) (*WxapkgPackage, error) {
	if len(data) < wxapkgIndexMin {
		return nil, fmt.Errorf("文件过小，不是有效的 wxapkg")
	}

	if IsEncrypted(data) {
		return nil, fmt.Errorf("此 wxapkg 为加密格式（V1MMWX，PC 微信 4.x）。上传时填写该小程序的 AppID 即可自动解密（AppID 就是包所在文件夹名，如 wx1234567890abcdef）")
	}

	// 头部标记校验：首字节 0xBE、第 14 字节 0xED
	if data[0] != wxapkgFirstMark {
		return nil, fmt.Errorf("wxapkg 起始标记校验失败（0x%02X != 0xBE），不是有效的 wxapkg 文件", data[0])
	}
	if data[wxapkgHeaderLen-1] != wxapkgLastMark {
		return nil, fmt.Errorf("wxapkg 结束标记校验失败（0x%02X != 0xED），文件可能不完整", data[wxapkgHeaderLen-1])
	}

	// 标准布局：文件数量在偏移 14-17（索引区首字段），条目从 18 开始。
	// 索引用 5-8 的索引区长度做强校验：走完条目应恰好落在 14+indexLen。
	indexLen := binary.BigEndian.Uint32(data[5:9])
	fileCount := binary.BigEndian.Uint32(data[14:18])
	if fileCount > 0 && fileCount <= 100000 {
		pkg, indexEnd, err := parseWxapkgEntries(data, fileName, fileCount, wxapkgIndexMin)
		if err == nil {
			// indexLen 合理时必须吻合（0 视为未提供）
			if indexLen == 0 || uint64(wxapkgHeaderLen)+uint64(indexLen) == uint64(indexEnd) {
				return pkg, nil
			}
		}
	}

	// 兼容旧布局假设（文件数在 9-12、条目从 14 开始）的包
	if legacy := binary.BigEndian.Uint32(data[9:13]); legacy > 0 && legacy <= 100000 {
		if pkg, _, err := parseWxapkgEntries(data, fileName, legacy, wxapkgHeaderLen); err == nil {
			return pkg, nil
		}
	}

	if fileCount == 0 || fileCount > 100000 {
		return nil, fmt.Errorf("文件数量异常（%d），不是有效的 wxapkg", fileCount)
	}
	return nil, fmt.Errorf("wxapkg 索引解析失败：条目数与索引区长度（%d）不一致，文件可能损坏", indexLen)
}

// parseWxapkgEntries 从 entryStart 起连续解析 fileCount 个条目，返回包与索引结束偏移
func parseWxapkgEntries(data []byte, fileName string, fileCount uint32, entryStart uint32) (*WxapkgPackage, uint32, error) {
	pkg := &WxapkgPackage{FileName: fileName}
	offset := entryStart
	total := uint32(len(data))

	for i := uint32(0); i < fileCount; i++ {
		// 文件名长度
		if offset+4 > total {
			return nil, 0, fmt.Errorf("解析第 %d 个文件条目时数据不足", i)
		}
		nameLen := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4

		if nameLen == 0 || nameLen > 4096 || offset+nameLen > total {
			return nil, 0, fmt.Errorf("第 %d 个文件名长度异常（%d bytes）", i, nameLen)
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen

		// 数据偏移 + 数据大小
		if offset+8 > total {
			return nil, 0, fmt.Errorf("解析第 %d 个文件偏移时数据不足", i)
		}
		fileOffset := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4
		fileSize := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4

		// 数据范围必须落在文件内
		if uint64(fileOffset)+uint64(fileSize) > uint64(total) {
			return nil, 0, fmt.Errorf("文件 %q 数据越界（offset=%d, size=%d, total=%d）", name, fileOffset, fileSize, total)
		}

		pkg.Files = append(pkg.Files, WxapkgFile{Name: name, Offset: fileOffset, Size: fileSize})
	}

	pkg.Data = data
	return pkg, offset, nil
}

// DecryptWxapkg 解密 PC 微信 4.x 的 V1MMWX 加密包（需要该小程序的 appid）:
//
//	前 1024 字节: AES-256-CBC(key = PBKDF2-HMAC-SHA1(appid, "saltiest", 1000, 32), IV = "the iv: 16 bytes")
//	其余字节:     与 appid 倒数第 2 个字符逐字节 XOR
//	明文 = 前 1023 字节 || 解出的尾部（文件中第 1024 字节是占位,不属于明文）
func DecryptWxapkg(data []byte, appid string) ([]byte, error) {
	appid = strings.TrimSpace(appid)
	if appid == "" {
		return nil, fmt.Errorf("加密包需要 AppID 才能解密（AppID 是包所在文件夹名,如 wx1234567890abcdef）")
	}
	if !IsEncrypted(data) {
		return data, nil
	}
	const headLen = 1024
	if len(data) < 6+headLen {
		return nil, fmt.Errorf("加密包数据不完整（%d 字节）", len(data))
	}

	key := pbkdf2.Key([]byte(appid), []byte("saltiest"), 1000, 32, sha1.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	head := make([]byte, headLen)
	cipher.NewCBCDecrypter(block, []byte("the iv: 16 bytes")).CryptBlocks(head, data[6:6+headLen])

	xorKey := byte(0x66)
	if len(appid) >= 2 {
		xorKey = appid[len(appid)-2]
	}
	tail := make([]byte, len(data)-6-headLen)
	for i, b := range data[6+headLen:] {
		tail[i] = b ^ xorKey
	}

	out := make([]byte, 0, len(head)-1+len(tail))
	out = append(out, head[:headLen-1]...)
	out = append(out, tail...)
	return out, nil
}

// ExtractFiles 将 wxapkg 中的文件解压到目标目录
func ExtractFiles(pkg *WxapkgPackage, destDir string) error {
	_, err := ExtractFilesWithReport(pkg, destDir)
	return err
}

// ExtractFilesWithReport 解压并返回被覆盖的文件名(多分包合并时同名路径后包覆盖先包)
func ExtractFilesWithReport(pkg *WxapkgPackage, destDir string) ([]string, error) {
	if pkg == nil || pkg.Data == nil {
		return nil, fmt.Errorf("无效的 wxapkg 包")
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("创建输出目录失败: %w", err)
	}

	var overwritten []string

	for _, f := range pkg.Files {
		// 安全性：防止路径穿越。包内路径通常是 "/app.js" 这类绝对形式，
		// 去掉前导 / 后落到工作目录内；"../" 等穿越路径仍被跳过。
		cleanName := filepath.Clean(strings.TrimLeft(strings.ReplaceAll(f.Name, "\\", "/"), "/"))
		if cleanName == "." || cleanName == ".." ||
			strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/../") ||
			filepath.IsAbs(cleanName) {
			continue
		}

		outPath := filepath.Join(destDir, cleanName)
		outDir := filepath.Dir(outPath)
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return nil, fmt.Errorf("创建目录 %s 失败: %w", outDir, err)
		}

		end := uint64(f.Offset) + uint64(f.Size)
		if end > uint64(len(pkg.Data)) {
			return nil, fmt.Errorf("文件 %s 的数据范围越界（offset=%d, size=%d, total=%d）", f.Name, f.Offset, f.Size, len(pkg.Data))
		}

		fileData := pkg.Data[f.Offset:end]
		if _, err := os.Stat(outPath); err == nil {
			overwritten = append(overwritten, cleanName)
		}
		if err := os.WriteFile(outPath, fileData, 0644); err != nil {
			return nil, fmt.Errorf("写入文件 %s 失败: %w", f.Name, err)
		}
	}

	return overwritten, nil
}
