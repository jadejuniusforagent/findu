package server

// config_secret_bridge —— Step 6d Phase B3：config 域对外脱敏。
//
// 设计（用户定案「config 脱敏用环境变量方式，部署时可选交互式部署或直接修改
// 配置文件」）：
//
//	脱敏读 —— GET /api/config、GET /api/external-mcp* 响应做密钥叶子掩码：
//	          · 值本身是 ${VAR} / ${VAR:-default} 环境引用 → 原样透出（这就是
//	            期望的「环境变量式」展示）；
//	          · 其余非空密钥值（api_key/app_secret/password/token/secret 等
//	            键名，含 env/headers 映射内键名命中 KEY|TOKEN|SECRET|PASS）
//	            → 掩为 ********，原文按 JSON 路径存本进程注册表；
//	保真写 —— PUT /api/config、PUT /api/external-mcp/{name} 请求体里值为
//	          ******** 的密钥叶子按注册表原值回填（UI 永远先 GET 后 PUT，路径
//	          一致；注册表缺失时保留 ********，后续 apply/test 会以明文暴露
//	          缺口并由人工处理——已在 handoff 记录该边界）；
//	          UpdateConfig / ApplyConfig 内含 saveConfig 写盘，写盘后执行
//	          env 引用保全：对照写盘前快照，凡「pre 是 ${VAR} 引用且展开后
//	          等于 post 的明文值」处，把文件中的明文复原为 ${VAR} 引用 ——
//	          使「环境变量部署」模式在热修改后不把明文烧进配置文件；
//	交互部署 —— scripts/secret_wizard.sh 引导把交互录入的密钥写进 .env（compose
//	          传入），或直接编辑 config-findu.yaml（挂载自仓库根目录）。

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

const configMaskToken = "********"

// configSecretRegistry GET 时登记的原始密钥值（JSON 指针路径 → 原文）。仅本进程
// 内存不落盘；PUT 回填依据。
var configSecretRegistry sync.Map

// secretRefDisplay 展开值 → ${VAR} 展示串：GET 时对「值恰为某环境变量展开结果」
// 的密钥直接展示成 ${VAR} 引用（用户定案的「环境变量式」展示），原文仍入注册表
// 供 PUT 回填。源为 config-findu.yaml 的静态引用面（apply 后重建）。
var secretRefDisplay sync.Map

// rebuildSecretRefDisplay 扫描配置文件中的 ${VAR} 引用，登记 展开值→引用串。
func rebuildSecretRefDisplay(path string) {
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i]
				val := n.Content[i+1]
				if key.Kind == yaml.ScalarNode && secretKeyDeny[strings.ToLower(key.Value)] &&
					val.Kind == yaml.ScalarNode && isEnvRef(val.Value) {
					if ev := expandEnvRef(val.Value); ev != "" {
						secretRefDisplay.Store(ev, val.Value)
					}
				}
				walk(val)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c)
			}
		}
	}
	walk(&root)
}

var maskJSONPathTokenRe = regexp.MustCompile("^~")

var secretKeyDeny = map[string]bool{
	"api_key":       true,
	"apikey":        true,
	"app_secret":    true,
	"client_secret": true,
	"password":      true,
	"secret":        true,
	"token":         true,
	"access_token":  true,
	"refresh_token": true,
}

// envOrHeaderKeyDeny 命中 env/headers 映射键名的模式（ExternalMCP 配置节）。
var envOrHeaderKeyDeny = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|passphrase)`)

// envRefRe 匹配 ${VAR} / ${VAR:-default}（与 config 包 expandEnvVar 同语法）。
var envRefRe = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}$`)

// isEnvRef 判断字符串是否是环境变量引用（这类值本就是「脱敏形式」，透传不掩）。
func isEnvRef(s string) bool { return envRefRe.MatchString(s) }

// expandEnvRef 镜像 config 包 expandEnvVar 的单引用展开（供写盘保全比对）。
func expandEnvRef(s string) string {
	m := envRefRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	val := os.Getenv(m[1])
	if val == "" && len(m) == 3 {
		val = m[2]
	}
	return val
}

// walkJSONMask 深度遍历，掩码密钥叶子并登记原文；返回是否有变更。
func walkJSONMask(root any) bool {
	type frame struct {
		v    any
		path string
	}
	changed := false
	stack := []frame{{root, ""}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch tv := f.v.(type) {
		case map[string]any:
			for k, child := range tv {
				childPath := f.path + "/" + strings.ReplaceAll(k, "~", "~0")
				if secretKeyDeny[strings.ToLower(k)] {
					if s, ok := child.(string); ok && s != "" && !isEnvRef(s) && s != configMaskToken {
						configSecretRegistry.Store(childPath, s)
						if disp, ok2 := secretRefDisplay.Load(s); ok2 {
							if ds, ok3 := disp.(string); ok3 {
								tv[k] = ds
								changed = true
								continue
							}
						}
						tv[k] = configMaskToken
						changed = true
					}
					continue
				}
				if strings.EqualFold(k, "env") || strings.EqualFold(k, "headers") {
					if m2, ok := child.(map[string]any); ok {
						for ek, ev := range m2 {
							if es, ok := ev.(string); ok && es != "" &&
								envOrHeaderKeyDeny.MatchString(ek) && !isEnvRef(es) && es != configMaskToken {
								configSecretRegistry.Store(f.path+"/"+ek, es)
								m2[ek] = configMaskToken
								changed = true
							}
						}
					}
					continue
				}
				stack = append(stack, frame{child, childPath})
			}
		case []any:
			for i, child := range tv {
				stack = append(stack, frame{child, f.path + "/" + strconv.Itoa(i)})
			}
		}
	}
	return changed
}

// maskConfigResponseBytes 对 GET 响应体执行掩码；不可解析时原样返回。
func maskConfigResponseBytes(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw
	}
	if !walkJSONMask(doc) {
		return raw
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

// walkJSONRestore 深度遍历请求体，把值为 ******** 的密钥叶子按注册表回填；
// 返回是否有回填动作。
func walkJSONRestore(root any) bool {
	type frame struct {
		v    any
		path string
	}
	changed := false
	stack := []frame{{root, ""}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch tv := f.v.(type) {
		case map[string]any:
			for k, child := range tv {
				childPath := f.path + "/" + strings.ReplaceAll(k, "~", "~0")
				if secretKeyDeny[strings.ToLower(k)] {
					if s, ok := child.(string); ok && s == configMaskToken {
						if origAny, ok2 := configSecretRegistry.Load(childPath); ok2 {
							if orig, ok3 := origAny.(string); ok3 {
								tv[k] = orig
								changed = true
							}
						}
					}
					continue
				}
				if strings.EqualFold(k, "env") || strings.EqualFold(k, "headers") {
					if m2, ok := child.(map[string]any); ok {
						for ek, ev := range m2 {
							if es, ok := ev.(string); ok && es == configMaskToken {
								if origAny, ok2 := configSecretRegistry.Load(f.path + "/" + ek); ok2 {
									if orig, ok3 := origAny.(string); ok3 {
										m2[ek] = orig
										changed = true
									}
								}
							}
						}
					}
					continue
				}
				stack = append(stack, frame{child, childPath})
			}
		case []any:
			for i, child := range tv {
				stack = append(stack, frame{child, f.path + "/" + strconv.Itoa(i)})
			}
		}
	}
	return changed
}

// restoreConfigRequestBytes 对 PUT 请求体执行注册表回填；不可解析时原样返回。
func restoreConfigRequestBytes(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw
	}
	walkJSONRestore(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

// captureRespWriter 捕获 handler 响应体用于脱敏后处理。
type captureRespWriter struct {
	gin.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (w *captureRespWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureRespWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }

func (w *captureRespWriter) WriteString(s string) (int, error) { return w.buf.WriteString(s) }

// ginWrapCapture 包装 handler：捕获响应体 → post 处理后写出（含请求体 pre 变换）。 :
func ginWrapCapture(h gin.HandlerFunc, post func([]byte) []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		orig := c.Writer
		cw := &captureRespWriter{ResponseWriter: orig}
		c.Writer = cw
		h(c)
		if post == nil {
			return
		}
		out := post(cw.buf.Bytes())
		if len(out) == 0 {
			return
		}
		cw.ResponseWriter.Header().Set("Content-Length", strconv.Itoa(len(out)))
		_, _ = cw.ResponseWriter.Write(out)
	}
}

// ginWrapBodyTransform 包装 handler：请求体经 transform 后回填，handler 完成后
// 依据 pre 快照对配置文件做 ${VAR} 引用保全。
func ginWrapBodyTransform(h gin.HandlerFunc, cfgPath func() string,
	transform func([]byte) []byte, preserveAfter bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			raw = nil
		}
		if transform != nil && len(raw) > 0 {
			transformed := transform(raw)
			c.Request.Body = io.NopCloser(bytes.NewReader(transformed))
			c.Request.ContentLength = int64(len(transformed))
		} else {
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		}
		pre := []byte(nil)
		if preserveAfter && cfgPath != nil {
			if p := cfgPath(); p != "" {
				pre, _ = os.ReadFile(p)
			}
		}
		h(c)
		if preserveAfter && cfgPath != nil && len(pre) > 0 {
			if p := cfgPath(); p != "" {
				preserveEnvRefsInFile(p, pre)
			}
		}
	}
}

// preserveEnvRefsInFile 写盘保全：pre 为 ${VAR} 引用 && expand(pre) == post 明文
// 时，把文件中的明文复原为 ${VAR} 引用。未变更时不写盘。
func preserveEnvRefsInFile(path string, pre []byte) {
	if path == "" || len(pre) == 0 {
		return
	}
	post, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var preNode, postNode yaml.Node
	_ = yaml.Unmarshal(pre, &preNode)
	_ = yaml.Unmarshal(post, &postNode)
	patchEnvRefsNode(&postNode, &preNode)
	out, err := yaml.Marshal(&postNode)
	if err != nil {
		return
	}
	if bytes.Equal(post, out) {
		return
	}
	_ = os.WriteFile(path, out, 0644)
}

func patchEnvRefsNode(post, pre *yaml.Node) {
	if post == nil || pre == nil {
		return
	}
	if post.Kind != pre.Kind {
		return
	}
	switch post.Kind {
	case yaml.DocumentNode:
		if len(post.Content) > 0 && len(pre.Content) > 0 {
			patchEnvRefsNode(post.Content[0], pre.Content[0])
		}
	case yaml.MappingNode:
		if len(post.Content) != len(pre.Content) {
			return
		}
		for i := 0; i+1 < len(post.Content) && i+1 < len(pre.Content); i += 2 {
			patchEnvRefsNode(post.Content[i+1], pre.Content[i+1])
		}
	case yaml.SequenceNode:
		if len(post.Content) != len(pre.Content) {
			return
		}
		for i := range post.Content {
			patchEnvRefsNode(post.Content[i], pre.Content[i])
		}
	case yaml.ScalarNode:
		if isEnvRef(pre.Value) && post.Value != "" && post.Value != pre.Value &&
			expandEnvRef(pre.Value) == post.Value {
			post.Value = pre.Value
			post.Tag = "!!str"
			post.Style = pre.Style
			return
		}
		// 掩字 heal：regisdustry miss 时写盘会把 ******** 烧进文件——回填写盘
		// 前快照对应叶子的原值（含空串），保证落盘内容永远是真实值/空值。
		if post.Value == configMaskToken && pre.Value != configMaskToken && (pre.Tag == "" || pre.Tag == "!!str") {
			post.Value = pre.Value
			post.Tag = "!!str"
			post.Style = pre.Style
		}
	}
}
