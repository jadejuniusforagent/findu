package miniprogram

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildTestWxapkg 构造一个最小合法 wxapkg（真实布局，大端序）：
// header(14B: 0xBE + info1(4,恒 0) + 索引区长度(4) + 数据区长度(4) + 0xED)
// + 文件数(4,索引区首字段) + 索引项 + 数据区
func buildTestWxapkg(files map[string][]byte) []byte {
	// 按名称排序保证确定性
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	// 简单排序
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}

	// 先计算索引区大小(含 4 字节文件数)
	indexSize := 4
	type entry struct {
		name   string
		offset uint32
		size   uint32
	}
	entries := make([]entry, 0, len(names))
	for _, name := range names {
		indexSize += 4 + len(name) + 8
	}

	dataStart := uint32(14 + indexSize)
	var dataBuf bytes.Buffer
	for _, name := range names {
		content := files[name]
		entries = append(entries, entry{name: name, offset: dataStart + uint32(dataBuf.Len()), size: uint32(len(content))})
		dataBuf.Write(content)
	}

	var buf bytes.Buffer
	// header: firstMark(1) + info1(4)=0 + 索引区长度(4) + 数据区长度(4) + lastMark(1) = 14B
	buf.WriteByte(0xBE)
	_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // info1
	_ = binary.Write(&buf, binary.BigEndian, uint32(indexSize))
	_ = binary.Write(&buf, binary.BigEndian, uint32(dataBuf.Len()))
	buf.WriteByte(0xED)
	// 索引区首字段:文件数
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(entries)))
	// 索引项（大端）
	for _, e := range entries {
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(e.name)))
		buf.WriteString(e.name)
		_ = binary.Write(&buf, binary.BigEndian, e.offset)
		_ = binary.Write(&buf, binary.BigEndian, e.size)
	}
	// 数据区
	buf.Write(dataBuf.Bytes())
	return buf.Bytes()
}

func TestParseWxapkg_Valid(t *testing.T) {
	files := map[string][]byte{
		"app.json":       []byte(`{"appid":"wx1234567890abcdef"}`),
		"pages/index.js": []byte(`Page({data:{url:"https://api.example.com"}})`),
	}
	raw := buildTestWxapkg(files)

	pkg, err := ParseWxapkg(raw, "test.wxapkg")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(pkg.Files) != 2 {
		t.Fatalf("文件数 = %d, want 2", len(pkg.Files))
	}

	// 解包并校验内容
	dest := t.TempDir()
	if err := ExtractFiles(pkg, dest); err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "app.json"))
	if err != nil {
		t.Fatalf("读取 app.json 失败: %v", err)
	}
	if string(got) != string(files["app.json"]) {
		t.Errorf("app.json 内容不一致: %q", got)
	}
}

func TestParseWxapkg_Encrypted(t *testing.T) {
	raw := append([]byte("V1MMWX"), make([]byte, 32)...)
	_, err := ParseWxapkg(raw, "enc.wxapkg")
	if err == nil {
		t.Fatal("加密包应返回错误")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("加密")) {
		t.Errorf("错误信息应提示加密: %v", err)
	}
}

func TestParseWxapkg_InvalidMagic(t *testing.T) {
	raw := make([]byte, 64)
	copy(raw, []byte{0x01, 0x02, 0x03, 0x04})
	if _, err := ParseWxapkg(raw, "bad.wxapkg"); err == nil {
		t.Fatal("非法魔数应返回错误")
	}
}

func TestParseWxapkg_TooSmall(t *testing.T) {
	if _, err := ParseWxapkg([]byte{0xBE, 0x00}, "tiny.wxapkg"); err == nil {
		t.Fatal("过小文件应返回错误")
	}
}

func TestExtractFiles_PathTraversal(t *testing.T) {
	// 构造含 ../ 穿越路径的包
	raw := buildTestWxapkg(map[string][]byte{
		"../evil.sh": []byte("bad"),
		"ok.txt":     []byte("good"),
	})
	pkg, err := ParseWxapkg(raw, "trav.wxapkg")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	dest := t.TempDir()
	if err := ExtractFiles(pkg, dest); err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	// 穿越文件必须被跳过
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil.sh")); err == nil {
		t.Fatal("路径穿越文件不应被写出")
	}
	if _, err := os.Stat(filepath.Join(dest, "ok.txt")); err != nil {
		t.Fatalf("正常文件应被写出: %v", err)
	}
}

func TestIsEncrypted(t *testing.T) {
	if !IsEncrypted([]byte("V1MMWXxxxxx")) {
		t.Error("V1MMWX 前缀应判定为加密")
	}
	if IsEncrypted([]byte{0xBE, 0x00, 0x00, 0x00, 0x00, 0x00}) {
		t.Error("明文包不应判定为加密")
	}
}

// buildLegacyWxapkg 构造旧假设布局(count 在偏移 9-12、条目从 14 开始)的包,验证兼容分支
func buildLegacyWxapkg(files map[string][]byte) []byte {
	var entries bytes.Buffer
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	indexSize := 0
	offsets := map[string]uint32{}
	dataStart := uint32(14)
	for _, n := range names {
		indexSize += 4 + len(n) + 8
	}
	dataStart = uint32(14 + indexSize)
	var dataBuf bytes.Buffer
	for _, n := range names {
		offsets[n] = dataStart + uint32(dataBuf.Len())
		dataBuf.Write(files[n])
	}
	for _, n := range names {
		_ = binary.Write(&entries, binary.BigEndian, uint32(len(n)))
		entries.WriteString(n)
		_ = binary.Write(&entries, binary.BigEndian, offsets[n])
		_ = binary.Write(&entries, binary.BigEndian, uint32(len(files[n])))
	}
	var buf bytes.Buffer
	buf.WriteByte(0xBE)
	_ = binary.Write(&buf, binary.BigEndian, uint32(indexSize))
	_ = binary.Write(&buf, binary.BigEndian, uint32(dataBuf.Len()))
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(names))) // count 在 9-12
	buf.WriteByte(0xED)
	buf.Write(entries.Bytes())
	buf.Write(dataBuf.Bytes())
	return buf.Bytes()
}

func TestParseWxapkg_LegacyHeaderLayout(t *testing.T) {
	raw := buildLegacyWxapkg(map[string][]byte{"a.js": []byte("x")})
	pkg, err := ParseWxapkg(raw, "legacy.wxapkg")
	if err != nil {
		t.Fatalf("旧布局包应能兼容解析: %v", err)
	}
	if len(pkg.Files) != 1 || pkg.Files[0].Name != "a.js" {
		t.Fatalf("解析结果异常: %+v", pkg.Files)
	}
}
