package miniprogram

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"os"
	"testing"

	"golang.org/x/crypto/pbkdf2"
)

// encryptV1MMWX 按 PC 微信 4.x 方案加密一个明文 wxapkg（用于解密回归测试）:
// 前 1024 字节 AES-256-CBC；其余(明文 1023 起)与 appid 倒数第 2 个字符 XOR。
func encryptV1MMWX(plain []byte, appid string) []byte {
	if len(plain) < 1024 {
		panic("plain too short")
	}
	key := pbkdf2.Key([]byte(appid), []byte("saltiest"), 1000, 32, sha1.New)
	block, _ := aes.NewCipher(key)
	head := make([]byte, 1024)
	cipher.NewCBCEncrypter(block, []byte("the iv: 16 bytes")).CryptBlocks(head, plain[:1024])

	xorKey := appid[len(appid)-2]
	tail := make([]byte, len(plain)-1023)
	for i, b := range plain[1023:] {
		tail[i] = b ^ xorKey
	}
	out := append([]byte("V1MMWX"), head...)
	return append(out, tail...)
}

func TestDecryptV1MMWXRoundTrip(t *testing.T) {
	const appid = "wx1234567890abcdef"
	plain := buildTestWxapkg(map[string][]byte{
		"/app.js": []byte("Page({ onLoad() { console.log('hello') } })"),
	})
	// 补齐到 >=1024 便于加密
	for len(plain) < 2048 {
		plain = append(plain, ' ')
	}
	enc := encryptV1MMWX(plain, appid)

	if !IsEncrypted(enc) {
		t.Fatal("加密结果应以 V1MMWX 开头")
	}
	// 未给 appid 应报错
	if _, err := DecryptWxapkg(enc, ""); err == nil {
		t.Error("缺少 appid 时应报错")
	}
	dec, err := DecryptWxapkg(enc, appid)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("解密结果与明文不一致: len %d vs %d", len(dec), len(plain))
	}
	if _, err := ParseWxapkg(dec, "t.wxapkg"); err != nil {
		t.Fatalf("解密后解析失败: %v", err)
	}
	// 明文包原样返回
	same, err := DecryptWxapkg(plain, appid)
	if err != nil || !bytes.Equal(same, plain) {
		t.Errorf("明文包应原样返回")
	}
	// 加密包直接解析应给出带 AppID 的提示
	if _, err := ParseWxapkg(enc, "t.wxapkg"); err == nil {
		t.Error("加密包直接解析应报错")
	}
}

// TestDecryptRealPackage 用真实 PC 微信 4.x 包做回归(文件不存在则跳过)。
// 用法: WXAPKG_REAL=/path/__APP__.wxapkg WXAPKG_APPID=wx... go test ./internal/miniprogram/ -run Real
func TestDecryptRealPackage(t *testing.T) {
	path := os.Getenv("WXAPKG_REAL")
	appid := os.Getenv("WXAPKG_APPID")
	if path == "" || appid == "" {
		t.Skip("未设置 WXAPKG_REAL / WXAPKG_APPID,跳过真实包回归")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	if !IsEncrypted(data) {
		t.Skip("不是加密包,跳过")
	}
	dec, err := DecryptWxapkg(data, appid)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	pkg, err := ParseWxapkg(dec, path)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(pkg.Files) == 0 {
		t.Fatal("未解析出文件")
	}
	t.Logf("解密解析成功: %d 个文件,首个=%s", len(pkg.Files), pkg.Files[0].Name)
}
