package miniprogram

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pkcs7Pad 测试辅助:与微信 sensitiveData 相同的 PKCS#7 填充。
func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// encryptUserData 测试辅助:按微信规范构造 encryptedData+iv。
func encryptUserData(t *testing.T, sessionKey []byte, plaintext map[string]interface{}) (string, string) {
	t.Helper()
	raw, err := json.Marshal(plaintext)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("rand iv: %v", err)
	}
	block, err := aes.NewCipher(sessionKey)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	padded := pkcs7Pad(raw, aes.BlockSize)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), base64.StdEncoding.EncodeToString(iv)
}

func TestDecryptUserDataRoundTrip(t *testing.T) {
	sessionKey := make([]byte, 16)
	if _, err := rand.Read(sessionKey); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	plain := map[string]interface{}{
		"phoneNumber":     "13800000000",
		"purePhoneNumber": "13800000000",
		"countryCode":     "86",
		"watermark":       map[string]interface{}{"appid": "wx1234abcdef567890", "timestamp": 1726732800},
	}
	encB64, ivB64 := encryptUserData(t, sessionKey, plain)

	res, err := DecryptUserData(
		base64.StdEncoding.EncodeToString(sessionKey), encB64, ivB64, "wx1234abcdef567890")
	if err != nil {
		t.Fatalf("DecryptUserData: %v", err)
	}
	if res.WatermarkAppID != "wx1234abcdef567890" {
		t.Fatalf("水印 appid = %q, 期望 wx1234abcdef567890", res.WatermarkAppID)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(res.Data, &got); err != nil {
		t.Fatalf("解密明文不是合法 JSON: %v", err)
	}
	if got["phoneNumber"] != "13800000000" {
		t.Fatalf("phoneNumber = %v, 期望 13800000000", got["phoneNumber"])
	}
}

func TestDecryptUserDataWatermarkMismatch(t *testing.T) {
	sessionKey := make([]byte, 16)
	encB64, ivB64 := encryptUserData(t, sessionKey, map[string]interface{}{
		"watermark": map[string]interface{}{"appid": "wxaaaaaacdef567890"},
	})
	_, err := DecryptUserData(
		base64.StdEncoding.EncodeToString(sessionKey), encB64, ivB64, "wxbbbbbdef56789012")
	if err == nil || !strings.Contains(err.Error(), "水印") {
		t.Fatalf("期望水印不一致报错,得到: %v", err)
	}
}

func TestDecryptUserDataBadInputs(t *testing.T) {
	sessionKey := make([]byte, 16)
	encB64, ivB64 := encryptUserData(t, sessionKey, map[string]interface{}{"a": 1})
	goodKeyB64 := base64.StdEncoding.EncodeToString(sessionKey)

	cases := []struct {
		name    string
		sk, enc string
		iv      string
		want    string
	}{
		{"session_key 非 base64", "!!!", encB64, ivB64, "base64"},
		{"session_key 长度异常", base64.StdEncoding.EncodeToString(make([]byte, 15)), encB64, ivB64, "长度异常"},
		{"encrypted_data 非 base64", goodKeyB64, "@@@", ivB64, "base64"},
		{"iv 长度异常", goodKeyB64, encB64, base64.StdEncoding.EncodeToString([]byte("short")), "iv"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecryptUserData(c.sk, c.enc, c.iv, "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("期望含 %q 的错误,得到: %v", c.want, err)
			}
		})
	}
	// 密文不配对:PKCS#7 去填充应报错而非 panic（密钥不能与加密用 key 相同）
	wrongKey := make([]byte, 16)
	for i := range wrongKey {
		wrongKey[i] = 0xA5
	}
	if _, err := DecryptUserData(
		base64.StdEncoding.EncodeToString(wrongKey), encB64, ivB64, ""); err == nil {
		t.Fatalf("错误 session_key 应解密失败")
	}
}

func TestJsCode2SessionClassification(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus string
		wantOpenID string
	}{
		{"成功", `{"openid":"oABC","session_key":"iWGx","unionid":"u1"}`, JsStatusValid, "oABC"},
		{"code 无效=凭据已验真", `{"errcode":40029,"errmsg":"invalid code"}`, JsStatusCodeInvalid, ""},
		{"secret 错误", `{"errcode":40125,"errmsg":"invalid appsecret"}`, JsStatusInvalid, ""},
		{"appid 不存在", `{"errcode":40013,"errmsg":"invalid appid"}`, JsStatusInvalid, ""},
		{"IP 受限(异常路径)", `{"errcode":40164,"errmsg":"invalid ip"}`, JsStatusRestricted, ""},
		{"频率限制", `{"errcode":45011,"errmsg":"rate limit"}`, JsStatusUnverifiable, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/sns/jscode2session") {
					t.Errorf("请求路径异常: %s", r.URL.Path)
				}
				q := r.URL.Query()
				if q.Get("appid") != "wx1234abcdef567890" || q.Get("js_code") != "the-code" {
					t.Errorf("请求参数缺失: %v", q)
				}
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			res := jsCode2SessionAt(t.Context(), srv.Client(), srv.URL, "wx1234abcdef567890", "the-secret", "the-code")
			if res.Status != c.wantStatus {
				t.Fatalf("status = %q(%s), 期望 %q", res.Status, res.Message, c.wantStatus)
			}
			if res.OpenID != c.wantOpenID {
				t.Fatalf("openid = %q, 期望 %q", res.OpenID, c.wantOpenID)
			}
			if c.wantStatus == JsStatusValid && res.SessionKey == "" {
				t.Fatalf("成功结论必须带 session_key")
			}
		})
	}
}
