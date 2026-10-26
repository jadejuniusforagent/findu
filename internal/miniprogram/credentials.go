// credentials.go 小程序凭证（AppID/AppSecret）池与有效性验证。
//
// 验证走微信官方 auth 接口（cgi-bin/token，client_credential 换 access_token），
// 属最小化有效性确认：换取到的 access_token 仅用于判定结论，随即丢弃，不落库不使用。
// 微信该接口的校验顺序是 appid 存在性 → IP 白名单 → secret 正确性，因此：
//   - 40013（invalid appid）→ AppID 不存在，invalid；
//   - 40164（IP 不在白名单）→ AppID 已确认真实存在，但 secret 未能核验 → 独立状态
//     ip_blocked（对渗透判断而言这是「凭证真实」的强信号，但不是 secret 有效的证明）；
//   - 40125/40001（secret 错误）→ invalid；
//   - 45009（接口日额度耗尽）等 → error，不下结论。
package miniprogram

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"finduai/internal/netproxy"
)

// 凭证状态（四档语义，与 cloudkeys/llmkeys 验证器统一；历史值 ip_blocked/error 由
// 迁移 v17 与启动归一分别映射为 restricted/unverifiable）。
const (
	CredStatusUnverified   = "unverified"
	CredStatusValid        = "valid"        // AppID + AppSecret 均验证有效
	CredStatusInvalid      = "invalid"      // AppID 不存在或 secret 错误
	CredStatusRestricted   = "restricted"   // AppID 真实存在，出口 IP 不在白名单，secret 未能核验
	CredStatusUnverifiable = "unverifiable" // 网络/额度等，不下结论
)

var appIDShape = regexp.MustCompile(`^wx[0-9a-f]{16}$`)

// Credential 小程序凭证池一条记录。Secret 明文仅服务端持有：API 只回显 Masked。
type Credential struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	AppID         string     `json:"app_id"`
	Secret        string     `json:"-"`
	MaskedSecret  string     `json:"masked_secret"`
	ProxyMode     string     `json:"proxy_mode"` // proxy(默认,走平台出口)/ direct(直连)
	ProjectID     string     `json:"project_id"` // 空=平台级（未绑定项目）
	Status        string     `json:"status"`
	StatusMessage string     `json:"status_message"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// AppSecretVerifyResult 一次微信凭证验证的结论。
type AppSecretVerifyResult struct {
	Valid   bool   `json:"valid"`
	Status  string `json:"status"`
	ErrCode int    `json:"err_code,omitempty"`
	Message string `json:"message"`
}

var errCredentialNotFound = errors.New("小程序凭证不存在")

// AddCredential 录入一对 AppID/AppSecret（同 AppID 去重，AppID 形态强校验 wx+16hex）。
// projectID 为会话绑定项目（空=平台级）。
func (s *Service) AddCredential(ctx context.Context, name, appID, secret, proxyMode, projectID string) (Credential, error) {
	name = strings.TrimSpace(name)
	appID = strings.ToLower(strings.TrimSpace(appID))
	secret = strings.TrimSpace(secret)
	proxyMode = netproxy.EffectiveEgressMode(proxyMode, "proxy")
	projectID = strings.TrimSpace(projectID)
	if !appIDShape.MatchString(appID) {
		return Credential{}, fmt.Errorf("AppID 形态不符（应为 wx + 16 位十六进制，如 wx1234abcdef567890）: %s", appID)
	}
	if secret == "" {
		return Credential{}, errors.New("AppSecret 不能为空")
	}
	if len(secret) < 16 || len(secret) > 64 {
		return Credential{}, fmt.Errorf("AppSecret 长度异常（通常为 32 位）: %d", len(secret))
	}
	existing, err := s.ListCredentials(ctx, "")
	if err != nil {
		return Credential{}, fmt.Errorf("读取凭证池失败（拒绝在状态不可知时写入）: %w", err)
	}
	for _, c := range existing {
		if c.AppID == appID {
			return Credential{}, fmt.Errorf("该 AppID 已在池中（%s），无需重复添加", c.Name)
		}
	}
	if name == "" {
		name = MaskSecret(secret)
	}
	id, err := randomCredentialID()
	if err != nil {
		return Credential{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO miniprogram_credentials (id,name,app_id,secret,proxy_mode,project_id,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())`, id, name, appID, secret, proxyMode, projectID)
	if err != nil {
		return Credential{}, err
	}
	return s.GetCredential(ctx, id)
}

// ListCredentials 列凭证池。projectID 非空时只返回该项目的凭证，空串返回全部（含平台级）。
func (s *Service) ListCredentials(ctx context.Context, projectID string) ([]Credential, error) {
	query := `SELECT id,name,app_id,secret,proxy_mode,project_id,status,COALESCE(status_message,''),last_checked_at,created_at
		FROM miniprogram_credentials`
	args := []any{}
	if strings.TrimSpace(projectID) != "" {
		query += ` WHERE project_id=$1`
		args = append(args, strings.TrimSpace(projectID))
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) GetCredential(ctx context.Context, id string) (Credential, error) {
	c, err := scanCredential(s.db.QueryRowContext(ctx, `SELECT id,name,app_id,secret,proxy_mode,project_id,status,COALESCE(status_message,''),last_checked_at,created_at
		FROM miniprogram_credentials WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, errCredentialNotFound
	}
	return c, err
}

func scanCredential(row interface{ Scan(...any) error }) (Credential, error) {
	var c Credential
	var checkedAt sql.NullTime
	if err := row.Scan(&c.ID, &c.Name, &c.AppID, &c.Secret, &c.ProxyMode, &c.ProjectID, &c.Status, &c.StatusMessage, &checkedAt, &c.CreatedAt); err != nil {
		return Credential{}, err
	}
	c.ProxyMode = netproxy.EffectiveEgressMode(c.ProxyMode, "proxy")
	c.MaskedSecret = MaskSecret(c.Secret)
	c.ProjectID = strings.TrimSpace(c.ProjectID)
	if checkedAt.Valid {
		c.LastCheckedAt = &checkedAt.Time
	}
	return c, nil
}

func (s *Service) DeleteCredential(ctx context.Context, id string) error {
	r, err := s.db.ExecContext(ctx, `DELETE FROM miniprogram_credentials WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return errCredentialNotFound
	}
	return nil
}

// RecordCredentialVerification 落一次验证结论。
func (s *Service) RecordCredentialVerification(ctx context.Context, id string, result AppSecretVerifyResult) error {
	message := result.Message
	if len(message) > 500 {
		message = message[:500]
	}
	_, err := s.db.ExecContext(ctx, `UPDATE miniprogram_credentials
		SET status=$2, status_message=$3, last_checked_at=now() WHERE id=$1`,
		id, result.Status, message)
	return err
}

// VerifyCredential 池内单条凭证出网验证并回填结论。
func (s *Service) VerifyCredential(ctx context.Context, client *http.Client, id string) (Credential, AppSecretVerifyResult, error) {
	c, err := s.GetCredential(ctx, id)
	if err != nil {
		return Credential{}, AppSecretVerifyResult{}, err
	}
	result := VerifyAppSecret(ctx, client, c.AppID, c.Secret)
	if err := s.RecordCredentialVerification(ctx, id, result); err != nil {
		return c, result, err
	}
	updated, err := s.GetCredential(ctx, id)
	if err != nil {
		return c, result, nil
	}
	return updated, result, nil
}

// VerifyAppSecret 用微信 cgi-bin/token 最小化验证一对 AppID/AppSecret。
// 成功响应里的 access_token 只参与结论判定，不记录。
func VerifyAppSecret(ctx context.Context, client *http.Client, appID, secret string) AppSecretVerifyResult {
	return verifyAppSecretAt(ctx, client, "https://api.weixin.qq.com", appID, secret)
}

func verifyAppSecretAt(ctx context.Context, client *http.Client, base, appID, secret string) AppSecretVerifyResult {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	invalidate := func(valid bool, status, message string) AppSecretVerifyResult {
		if message == "" {
			message = map[string]string{
				CredStatusValid:        "凭证有效（access_token 换取成功）",
				CredStatusInvalid:      "凭证无效",
				CredStatusRestricted:   "",
				CredStatusUnverifiable: "验证失败",
			}[status]
		}
		return AppSecretVerifyResult{Valid: valid, Status: status, Message: message}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	api := base + "/cgi-bin/token?grant_type=client_credential&appid=" +
		url.QueryEscape(appID) + "&secret=" + url.QueryEscape(secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return invalidate(false, CredStatusUnverifiable, err.Error())
	}
	resp, err := client.Do(req)
	if err != nil {
		return invalidate(false, CredStatusUnverifiable, "请求微信接口失败: "+err.Error())
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return invalidate(false, CredStatusUnverifiable, "读取微信响应失败: "+err.Error())
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return invalidate(false, CredStatusUnverifiable, fmt.Sprintf("解析微信响应失败 (HTTP %d)", resp.StatusCode))
	}
	if payload.AccessToken != "" {
		return invalidate(true, CredStatusValid, "")
	}
	return classifyWeChatError(payload.ErrCode, payload.ErrMsg)
}

// classifyWeChatError 按微信校验顺序归一错误码结论（见文件头注释）。
func classifyWeChatError(errCode int, errMsg string) AppSecretVerifyResult {
	res := AppSecretVerifyResult{Valid: false, ErrCode: errCode, Message: errMsg}
	switch errCode {
	case 40013:
		res.Status = CredStatusInvalid
		res.Message = "AppID 不存在（" + errMsg + "）"
	case 40125, 40001:
		res.Status = CredStatusInvalid
		res.Message = "AppSecret 错误（" + errMsg + "）"
	case 40164:
		res.Status = CredStatusRestricted
		res.Message = "AppID 真实存在；平台出口 IP 不在该小程序 IP 白名单，AppSecret 未能核验（" + errMsg + "）"
	case 45009:
		res.Status = CredStatusUnverifiable
		res.Message = "微信接口日额度耗尽，暂无法下结论（" + errMsg + "）"
	default:
		res.Status = CredStatusUnverifiable
		res.Message = "微信返回未分类错误（" + errMsg + "）"
	}
	return res
}

func MaskSecret(secret string) string {
	if len(secret) <= 8 {
		return "****"
	}
	return secret[:4] + "…" + secret[len(secret)-4:]
}

func randomCredentialID() (string, error) {
	const hexDigits = "0123456789abcdef"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 32)
	for i, v := range b {
		out[i*2] = hexDigits[v>>4]
		out[i*2+1] = hexDigits[v&0x0f]
	}
	return string(out), nil
}
