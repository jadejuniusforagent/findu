// loginchain.go 小程序登录态与敏感数据链（盘点方案 P0-2 的工具化）。
//
// 覆盖微信小程序最高频高危的两步:
//  1. sns/jscode2session: AppID+AppSecret+js_code → openid+session_key。
//     实测（2026-09-19）该接口不受 cgi-bin 的 IP 白名单约束——同一对凭据在
//     /cgi-bin/token 报 40164 时仍能在此正常校验。因此它同时是「凭据有效性
//     旁路验证」通道: 40125/40013 直接判定 secret/appid 错误; 40029 说明
//     appid+secret 已通过微信校验（仅 js_code 不可用）。
//  2. encryptedData + iv + session_key → AES-128-CBC 解密，还原手机号/unionId
//     等敏感字段，并按 watermark.appid 校验数据归属。
//
// 边界: 本文件只做「凭据确认」与「授权测试范围内的数据解密」。伪造 openid 建立
// 会话、批量拉取他人数据等主动利用不在工具面内，必须由 Agent 在授权边界内用
// http_session/http_probe 显式执行并遵守 ROE。
package miniprogram

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// JsCode2SessionResult jscode2session 一次调用的结论。
type JsCode2SessionResult struct {
	Valid      bool   `json:"valid"`
	Status     string `json:"status"` // valid / code_invalid / invalid / restricted / unverifiable
	OpenID     string `json:"openid,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	UnionID    string `json:"unionid,omitempty"`
	ErrCode    int    `json:"err_code,omitempty"`
	Message    string `json:"message"`
}

// jscode2session 的状态值（与凭证池四档语义并存,code_invalid 是本接口特有的
// 「凭据已验真、code 不可用」结论,对渗透判断而言等价于凭据有效）。
const (
	JsStatusValid        = "valid"
	JsStatusCodeInvalid  = "code_invalid"
	JsStatusInvalid      = "invalid"
	JsStatusRestricted   = "restricted"
	JsStatusUnverifiable = "unverifiable"
)

// JsCode2Session 用微信 sns/jscode2session 换取登录态（openid+session_key）。
// jsCode 来自小程序端 wx.login,一次性且约 5 分钟有效,需从抓包或运行时获取。
func JsCode2Session(ctx context.Context, client *http.Client, appID, secret, jsCode string) JsCode2SessionResult {
	return jsCode2SessionAt(ctx, client, "https://api.weixin.qq.com", appID, secret, jsCode)
}

func jsCode2SessionAt(ctx context.Context, client *http.Client, base, appID, secret, jsCode string) JsCode2SessionResult {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	invalidate := func(valid bool, status, message string) JsCode2SessionResult {
		return JsCode2SessionResult{Valid: valid, Status: status, Message: message}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	api := base + "/sns/jscode2session?appid=" + url.QueryEscape(appID) +
		"&secret=" + url.QueryEscape(secret) +
		"&js_code=" + url.QueryEscape(jsCode) +
		"&grant_type=authorization_code"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return invalidate(false, JsStatusUnverifiable, err.Error())
	}
	resp, err := client.Do(req)
	if err != nil {
		return invalidate(false, JsStatusUnverifiable, "请求微信接口失败: "+err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return invalidate(false, JsStatusUnverifiable, "读取微信响应失败: "+err.Error())
	}
	var payload struct {
		OpenID     string `json:"openid"`
		SessionKey string `json:"session_key"`
		UnionID    string `json:"unionid"`
		ErrCode    int    `json:"errcode"`
		ErrMsg     string `json:"errmsg"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return invalidate(false, JsStatusUnverifiable, fmt.Sprintf("解析微信响应失败 (HTTP %d)", resp.StatusCode))
	}
	if payload.OpenID != "" && payload.SessionKey != "" {
		return JsCode2SessionResult{
			Valid:      true,
			Status:     JsStatusValid,
			OpenID:     payload.OpenID,
			SessionKey: payload.SessionKey,
			UnionID:    payload.UnionID,
			Message:    "登录态换取成功（openid+session_key）",
		}
	}
	return classifyJsCode2SessionError(payload.ErrCode, payload.ErrMsg)
}

// classifyJsCode2SessionError 按微信校验顺序归一错误码结论（见文件头注释）。
// 关键差异: 40029（code 无效）说明 appid+secret 已通过校验——这是不受 IP 白名单
// 约束的凭据旁路验证结论,独立成 code_invalid 状态。
func classifyJsCode2SessionError(errCode int, errMsg string) JsCode2SessionResult {
	res := JsCode2SessionResult{Valid: false, ErrCode: errCode, Message: errMsg}
	switch errCode {
	case 40029:
		res.Status = JsStatusCodeInvalid
		res.Message = "appid+secret 已通过微信校验,但 js_code 无效/已使用/过期（" + errMsg + "）——凭据真实性已旁路确认"
	case 40013:
		res.Status = JsStatusInvalid
		res.Message = "AppID 不存在（" + errMsg + "）"
	case 40125, 40001, 41004:
		res.Status = JsStatusInvalid
		res.Message = "AppSecret 错误（" + errMsg + "）"
	case 40164:
		res.Status = JsStatusRestricted
		res.Message = "出口 IP 被拒（该接口实测通常不受 IP 白名单约束,出现此码说明目标配置了强制校验）: " + errMsg
	case 45011:
		res.Status = JsStatusUnverifiable
		res.Message = "微信接口频率限制,稍后重试（" + errMsg + "）"
	case 40226:
		res.Status = JsStatusUnverifiable
		res.Message = "微信判定高风险用户,拒绝换取（" + errMsg + "）"
	default:
		res.Status = JsStatusUnverifiable
		res.Message = "微信返回未分类错误（" + errMsg + "）"
	}
	return res
}

// DecryptedUserData encryptedData 解密结果。
type DecryptedUserData struct {
	// WatermarkAppID 明文内微信自带水印里的 appid(数据归属的小程序)。
	WatermarkAppID string          `json:"watermark_appid"`
	Data           json.RawMessage `json:"data"`
}

// DecryptUserData 解密小程序 sensitiveData（wx.getUserInfo/getPhoneNumber 等
// 返回的 encryptedData 字段）。
//
// 算法: session_key/encryptedData/iv 均为标准 base64;AES-128-CBC(key=session_key
// 16 字节,iv=16 字节)解密后 PKCS#7 去填充,明文为 JSON,内含
// watermark.appid 标识数据归属小程序。wantAppID 非空时校验水印一致性——
// 不一致说明 session_key 与数据不配对（跨 appid 混用或凭据错误）。
func DecryptUserData(sessionKeyB64, encryptedDataB64, ivB64, wantAppID string) (*DecryptedUserData, error) {
	sessionKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sessionKeyB64))
	if err != nil {
		return nil, fmt.Errorf("session_key 不是合法 base64: %w", err)
	}
	if len(sessionKey) != 16 {
		return nil, fmt.Errorf("session_key 长度异常: %d 字节（应为 16 字节 AES-128 密钥）", len(sessionKey))
	}
	encrypted, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encryptedDataB64))
	if err != nil {
		return nil, fmt.Errorf("encrypted_data 不是合法 base64: %w", err)
	}
	iv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ivB64))
	if err != nil {
		return nil, fmt.Errorf("iv 不是合法 base64: %w", err)
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("iv 长度异常: %d 字节（应为 16 字节）", len(iv))
	}
	if len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("encrypted_data 长度异常: %d 字节（应为 16 的倍数）", len(encrypted))
	}

	block, err := aes.NewCipher(sessionKey)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, encrypted)

	unpadded, err := pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return nil, fmt.Errorf("去填充失败,session_key 与 encrypted_data 不配对: %w", err)
	}

	var doc struct {
		Watermark struct {
			AppID     string `json:"appid"`
			Timestamp int64  `json:"timestamp"`
		} `json:"watermark"`
	}
	if err := json.Unmarshal(unpadded, &doc); err != nil {
		return nil, fmt.Errorf("解密结果不是合法 JSON（%v）,session_key 可能不配对", err)
	}
	watermarkAppID := strings.TrimSpace(doc.Watermark.AppID)
	if wantAppID != "" && watermarkAppID != "" && watermarkAppID != strings.TrimSpace(wantAppID) {
		return nil, fmt.Errorf("数据水印校验失败: 明文归属 %s,与传入 appid %s 不一致（session_key 跨小程序混用）", watermarkAppID, wantAppID)
	}
	return &DecryptedUserData{WatermarkAppID: watermarkAppID, Data: json.RawMessage(unpadded)}, nil
}

// pkcs7Unpad 去除 PKCS#7 填充（微信 sensitiveData 使用标准 PKCS#7,块大小 16）。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("数据长度 %d 不是块大小 %d 的倍数", len(data), blockSize)
	}
	pad := int(data[len(data)-1])
	if pad < 1 || pad > blockSize {
		return nil, fmt.Errorf("填充字节的非法值 %d", pad)
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("填充内容不一致")
		}
	}
	return data[:len(data)-pad], nil
}
