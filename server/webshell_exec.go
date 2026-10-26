package server

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// webshellSupportedEncodings 允许的响应编码（小写，空串代表 auto）。
var webshellSupportedEncodings = map[string]struct{}{
	"":         {},
	"auto":     {},
	"utf-8":    {},
	"utf8":     {},
	"gbk":      {},
	"gb18030":  {},
}

func normalizeWebshellEncoding(enc string) string {
	enc = strings.ToLower(strings.TrimSpace(enc))
	if _, ok := webshellSupportedEncodings[enc]; !ok {
		return "auto"
	}
	if enc == "" {
		return "auto"
	}
	if enc == "utf8" {
		return "utf-8"
	}
	return enc
}

// decodeWebshellOutput 把 WebShell 返回的字节按指定编码转换为合法 UTF-8。
func decodeWebshellOutput(raw []byte, encoding string) string {
	if len(raw) == 0 {
		return ""
	}
	switch normalizeWebshellEncoding(encoding) {
	case "utf-8":
		return string(raw)
	case "gbk":
		if out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), raw); err == nil {
			return string(out)
		}
		return string(raw)
	case "gb18030":
		if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw); err == nil {
			return string(out)
		}
		return string(raw)
	default: // auto
		if utf8.Valid(raw) {
			return string(raw)
		}
		if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw); err == nil {
			return string(out)
		}
		return string(raw)
	}
}

// webshellHTTPClient 与 findu 的 WebShellHandler 客户端一致：30s 超时 + 跳过 TLS 校验
// （WebShell 场景常见自签证书或 IP 访问）。
var webshellHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DisableKeepAlives: false,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // webshell proxy
	},
}

// execWebshellRequest 与 findu ExecRequest 对齐的宿主请求体。
type execWebshellRequest struct {
	URL          string `json:"url"`
	Password     string `json:"password"`
	Type         string `json:"type"`
	Method       string `json:"method"`
	CmdParam     string `json:"cmd_param"`
	Encoding     string `json:"encoding"`
	ConnectionID string `json:"connection_id"`
	Command      string `json:"command"`
}

// execWebshellResponse 与 findu ExecResponse 对齐。
type execWebshellResponse struct {
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	HTTPCode int    `json:"http_code,omitempty"`
}

// webshellExecParams 构建 pass + cmd 表单参数（cmd 参数名可配置）。
func webshellExecParams(shellType, password, cmdParam, command string) url.Values {
	shellType = strings.ToLower(strings.TrimSpace(shellType))
	if shellType == "" {
		shellType = "php"
	}
	if strings.TrimSpace(cmdParam) == "" {
		cmdParam = "cmd"
	}
	form := url.Values{}
	form.Set("pass", password)
	form.Set(cmdParam, command)
	return form
}

func webshellBuildExecBody(shellType, password, cmdParam, command string) []byte {
	return []byte(webshellExecParams(shellType, password, cmdParam, command).Encode())
}

func webshellBuildExecURL(baseURL, shellType, password, cmdParam, command string) string {
	form := webshellExecParams(shellType, password, cmdParam, command)
	if parsed, err := url.Parse(baseURL); err == nil {
		parsed.RawQuery = form.Encode()
		return parsed.String()
	}
	return baseURL + "?" + form.Encode()
}

// execWebshellNative serves POST /api/webshell/exec：
// findu WebShellHandler.Exec 的宿主原生实现（HTTP 在线执行，无 findu 运行态依赖）。
func (s *Server) execWebshellNative(w http.ResponseWriter, r *http.Request) {
	var req execWebshellRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	req.Command = strings.TrimSpace(req.Command)
	if req.URL == "" || req.Command == "" {
		writeErr(w, http.StatusBadRequest, "url and command are required")
		return
	}
	// 已保存连接：DB 凭据为准，禁止调用方配对任意凭据/URL。
	if cid := strings.TrimSpace(req.ConnectionID); cid != "" {
		ws := s.webshellsNative()
		if ws == nil {
			writeErr(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		conn, err := ws.Get(cid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "connection not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if conn == nil {
			writeErr(w, http.StatusNotFound, "connection not found")
			return
		}
		req.URL, req.Password, req.Type = conn.URL, conn.Password, conn.Type
		req.Method, req.CmdParam, req.Encoding = conn.Method, conn.CmdParam, conn.Encoding
	}

	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeErr(w, http.StatusBadRequest, "invalid url: only http(s) allowed")
		return
	}

	useGET := strings.ToUpper(strings.TrimSpace(req.Method)) == "GET"
	cmdParam := strings.TrimSpace(req.CmdParam)
	if cmdParam == "" {
		cmdParam = "cmd"
	}
	var httpReq *http.Request
	if useGET {
		targetURL := webshellBuildExecURL(req.URL, req.Type, req.Password, cmdParam, req.Command)
		httpReq, err = http.NewRequest(http.MethodGet, targetURL, nil)
	} else {
		body := webshellBuildExecBody(req.Type, req.Password, cmdParam, req.Command)
		httpReq, err = http.NewRequest(http.MethodPost, req.URL, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, execWebshellResponse{OK: false, Error: err.Error()})
		return
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FindUAI-WebShell/1.0)")

	resp, err := webshellHTTPClient.Do(httpReq)
	if err != nil {
		writeJSON(w, http.StatusOK, execWebshellResponse{OK: false, Error: err.Error()})
		return
	}
	defer resp.Body.Close()

	out, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		writeJSON(w, http.StatusOK, execWebshellResponse{OK: false, Error: readErr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, execWebshellResponse{
		OK:       resp.StatusCode == http.StatusOK,
		Output:   decodeWebshellOutput(out, req.Encoding),
		HTTPCode: resp.StatusCode,
	})
}

// getWebshellStateNative serves GET /api/webshell/connections/{id}/state：
// findu WebShellHandler.GetConnectionState 的宿主原生实现（纯 DB，无 findu 运行态依赖）。
func (s *Server) getWebshellStateNative(w http.ResponseWriter, r *http.Request) {
	ws := s.webshellsNative()
	if ws == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	conn, err := ws.Get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "connection not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if conn == nil {
		writeErr(w, http.StatusNotFound, "connection not found")
		return
	}
	stateJSON, err := ws.GetState(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var state any
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		state = map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": state})
}

// saveWebshellStateNative serves PUT /api/webshell/connections/{id}/state：
// findu WebShellHandler.SaveConnectionState 的宿主原生实现（纯 DB upsert）。
func (s *Server) saveWebshellStateNative(w http.ResponseWriter, r *http.Request) {
	ws := s.webshellsNative()
	if ws == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	conn, err := ws.Get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "connection not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if conn == nil {
		writeErr(w, http.StatusNotFound, "connection not found")
		return
	}
	var req struct {
		State json.RawMessage `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	raw := req.State
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > 2*1024*1024 {
		writeErr(w, http.StatusBadRequest, "state payload too large (max 2MB)")
		return
	}
	var anyJSON any
	if err := json.Unmarshal(raw, &anyJSON); err != nil {
		writeErr(w, http.StatusBadRequest, "state must be valid json")
		return
	}
	if err := ws.UpsertState(id, string(raw)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}