package miniprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanner_FindsSensitiveInfo(t *testing.T) {
	dir := t.TempDir()
	// 构造包含敏感信息的 JS 文件
	js := `const appid = "wx1234567890abcdef";
wx.request({ url: "https://api.example.com/login", method: "POST" });
const apiKey = "sk-test1234567890abcdef";
const password = "SuperSecret123";
wx.cloud.callFunction({ name: "getUserInfo" });
const internal = "http://192.168.1.100:8080/admin";
`
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte(js), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner()
	findings, err := scanner.ScanDirectory(dir)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}

	cats := map[string]int{}
	for _, f := range findings {
		cats[f.Category]++
	}

	// 应命中：API 接口 / 密钥 / AppID / 云函数 / 内网地址
	if cats[string(CategoryAPIEndpoint)] == 0 {
		t.Error("应命中 API 接口")
	}
	if cats[string(CategorySecret)] == 0 {
		t.Error("应命中硬编码密钥")
	}
	if cats[string(CategoryAppID)] == 0 {
		t.Error("应命中 AppID")
	}
	if cats[string(CategoryCloudEnv)] == 0 {
		t.Error("应命中云开发配置")
	}
	if cats[string(CategorySensitiveURL)] == 0 {
		t.Error("应命中内网地址")
	}

	// 校验行号与文件路径
	for _, f := range findings {
		if f.FilePath == "" || f.LineNumber <= 0 {
			t.Errorf("发现记录缺少定位信息: %+v", f)
		}
	}
}

func TestScanner_SkipsBinary(t *testing.T) {
	dir := t.TempDir()
	// 二进制文件（非文本扩展名）应跳过
	if err := os.WriteFile(filepath.Join(dir, "image.png"), []byte("wx.request({url:'http://x'})"), 0644); err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner()
	findings, err := scanner.ScanDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("非文本文件不应被扫描, got %d findings", len(findings))
	}
}

func TestScanner_CleansNonexistentDir(t *testing.T) {
	scanner := NewScanner()
	_, err := scanner.ScanDirectory(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("不存在的目录应返回错误")
	}
}

func TestScanner_LongMinifiedLine(t *testing.T) {
	dir := t.TempDir()
	// 压缩 JS:单行远超 64KB,内部含接口地址与密钥
	long := strings.Repeat("var a=1;", 20000) + `var u="https://log-game.qcplay.com/sync_json";var k="api_key":"sk-abcdef123456";`
	if err := os.WriteFile(filepath.Join(dir, "game.js"), []byte(long), 0644); err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner()
	findings, err := scanner.ScanDirectory(dir)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("超长行文件应能命中(旧实现会静默漏掉)")
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f.Content, "qcplay.com") {
			found = true
			if f.LineNumber != 1 {
				t.Errorf("超长单行文件行号应为 1,得到 %d", f.LineNumber)
			}
		}
	}
	if !found {
		t.Errorf("应命中外部接口地址: %+v", findings)
	}
}

func TestScanner_FiltersLibraryPEMTemplate(t *testing.T) {
	dir := t.TempDir()
	// jsencrypt 风格:模板拼接(不是真私钥)不应命中;真实 PEM 应命中
	lib := `t.prototype.getPrivateKey=function(){return"-----BEGIN RSA PRIVATE KEY-----\n"+t.wordwrap(this.getPrivateBaseKeyB64())+"\n-----END RSA PRIVATE KEY-----"}`
	real := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+/=\n-----END RSA PRIVATE KEY-----"
	if err := os.WriteFile(filepath.Join(dir, "lib.js"), []byte(lib), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.js"), []byte(real), 0644); err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner()
	findings, err := scanner.ScanDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	var libHit, realHit bool
	for _, f := range findings {
		if f.Category != string(CategorySecret) {
			continue
		}
		if strings.Contains(f.FilePath, "lib.js") {
			libHit = true
		}
		if strings.Contains(f.FilePath, "real.js") {
			realHit = true
		}
	}
	if libHit {
		t.Error("库模板字符串不应判为私钥泄露")
	}
	if !realHit {
		t.Error("真实 PEM 私钥应命中")
	}
}

func TestScanner_FiltersApiPathPassword(t *testing.T) {
	dir := t.TempDir()
	js := `const PASSWORD="passports/wSetPasswordApi";const password = "RealSecret123";`
	if err := os.WriteFile(filepath.Join(dir, "a.js"), []byte(js), 0644); err != nil {
		t.Fatal(err)
	}
	findings, err := NewScanner().ScanDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.Contains(f.Content, "passports/") {
			t.Errorf("接口路径常量不应判为密码: %s", f.Content)
		}
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f.Content, "RealSecret123") {
			found = true
		}
	}
	if !found {
		t.Error("真实硬编码密码应命中")
	}
}
