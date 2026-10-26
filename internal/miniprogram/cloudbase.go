// cloudbase.go 云开发（CloudBase/TCB）资产盘点与测试项生成（盘点方案 P1-云开发专测第一步）。
//
// 小程序包内的云开发调用是静态可见的:env 标识、云函数名（callFunction）、
// 集合名（.collection）、云存储 fileID（cloud://）、匿名登录痕迹。本文件从解包
// 工作区提取这份资产清单,并为每个云函数/集合生成测试项（与扫描发现同一账本）,
// 把「认得出云开发」升级为「逐函数逐集合有验证任务」。
//
// 实际调用验证走既有通道:HTTP 触发路径用 http_probe 重放;TCB 凭据走
// cloud_key_verify 验真;小程序上下文外无法直接调用的函数在账本结论里标 na。
package miniprogram

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// CloudBaseInventory 云开发资产清单（按类别去重,含首个证据位置）。
type CloudBaseInventory struct {
	Envs        []CloudRef `json:"envs"`
	Functions   []CloudRef `json:"functions"`
	Collections []CloudRef `json:"collections"`
	FileIDs     []CloudRef `json:"file_ids"`
	Anonymous   []CloudRef `json:"anonymous_sign_in"`
}

// CloudRef 一个云开发资产引用。
type CloudRef struct {
	Value string `json:"value"` // env / 函数名 / 集合名 / fileID
	File  string `json:"file"`  // 首个命中文件（相对工作区）
	Line  int    `json:"line"`
}

var (
	cloudEnvPattern    = regexp.MustCompile(`wx\.cloud\.init\s*\(\s*\{[^}]*?env\s*:\s*["'` + "`" + `]([^"'` + "`" + `]+)["'` + "`" + `]`)
	cloudFnPattern     = regexp.MustCompile(`(?s)callFunction\s*\(\s*\{[^}]*?name\s*:\s*["'` + "`" + `]([^"'` + "`" + `]+)["'` + "`" + `]`)
	cloudCollPattern   = regexp.MustCompile(`\.collection\s*\(\s*["'` + "`" + `]([^"'` + "`" + `]+)["'` + "`" + `]`)
	cloudFileIDPattern = regexp.MustCompile(`cloud://[A-Za-z0-9._/-]+`)
	cloudAnonPattern   = regexp.MustCompile(`(?i)(tcb|cloud)[A-Za-z]*\.(?:auth|signIn)\w*\s*\(|signInWithEPhemeral|ANONYMOUS`)
)

// AnalyzeCloudBase 扫描工作区文本文件,提取云开发资产清单。
// 只读 .js/.json/.ts/.wxml 文本;单文件大小上限 2MB;命中按值去重保留首个位置。
func AnalyzeCloudBase(workspace string) (*CloudBaseInventory, error) {
	inv := &CloudBaseInventory{}
	seen := map[string]bool{}
	// add:按类别追加并去重(键=类别:值)
	add := func(class, value, file string, line int) {
		value = strings.TrimSpace(value)
		if value == "" || seen[class+":"+value] {
			return
		}
		seen[class+":"+value] = true
		ref := CloudRef{Value: value, File: filepath.ToSlash(file), Line: line}
		switch class {
		case "env":
			inv.Envs = append(inv.Envs, ref)
		case "fn":
			inv.Functions = append(inv.Functions, ref)
		case "coll":
			inv.Collections = append(inv.Collections, ref)
		case "file":
			inv.FileIDs = append(inv.FileIDs, ref)
		case "anon":
			inv.Anonymous = append(inv.Anonymous, ref)
		}
	}

	err := filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".js", ".json", ".ts", ".wxml":
		default:
			return nil
		}
		if info.Size() > 2*1024*1024 {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(workspace, path)
		text := string(data)
		lineAt := func(off int) int { return 1 + strings.Count(text[:off], "\n") }

		for _, m := range cloudEnvPattern.FindAllStringSubmatchIndex(text, -1) {
			add("env", text[m[2]:m[3]], rel, lineAt(m[2]))
		}
		for _, m := range cloudFnPattern.FindAllStringSubmatchIndex(text, -1) {
			add("fn", text[m[2]:m[3]], rel, lineAt(m[2]))
		}
		for _, m := range cloudCollPattern.FindAllStringSubmatchIndex(text, -1) {
			add("coll", text[m[2]:m[3]], rel, lineAt(m[2]))
		}
		for _, m := range cloudFileIDPattern.FindAllStringIndex(text, -1) {
			add("file", text[m[0]:m[1]], rel, lineAt(m[0]))
		}
		for _, m := range cloudAnonPattern.FindAllStringIndex(text, -1) {
			add("anon", clipContext(text, m[0]), rel, lineAt(m[0]))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortCloudRefs(inv.Envs)
	sortCloudRefs(inv.Functions)
	sortCloudRefs(inv.Collections)
	sortCloudRefs(inv.FileIDs)
	sortCloudRefs(inv.Anonymous)
	// 账本防爆量:单类超过 30 条截断(报告里保留全部,测试项只建前 30)
	return inv, nil
}

func sortCloudRefs(refs []CloudRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		return refs[i].Line < refs[j].Line
	})
}

// clipContext 匿名登录命中取上下文片段(值列展示用)。
func clipContext(text string, off int) string {
	start := off
	for start > 0 && text[start-1] != '\n' && off-start < 40 {
		start--
	}
	end := off
	for end < len(text) && text[end] != '\n' && end-off < 60 {
		end++
	}
	return strings.TrimSpace(text[start:end])
}

// cloudbaseItemDraft 云开发测试项草稿(source 幂等键,与扫描发现账本同一张表)。
type cloudbaseItemDraft struct {
	Source      string
	Category    string
	Severity    string
	Title       string
	Target      string
	Instruction string
}

// cloudbaseItemCap 单类测试项上限(防账本爆炸;清单本身在工具输出里全量返回)。
const cloudbaseItemCap = 30

// buildCloudBaseDrafts 资产清单 → 测试项草稿。
func buildCloudBaseDrafts(inv *CloudBaseInventory) []cloudbaseItemDraft {
	out := []cloudbaseItemDraft{}
	for i, fn := range inv.Functions {
		if i >= cloudbaseItemCap {
			break
		}
		out = append(out, cloudbaseItemDraft{
			Source:      "cloudbase:function:" + fn.Value,
			Category:    "cloudbase_function",
			Severity:    "high",
			Title:       fmt.Sprintf("云函数未授权直调验证:%s", fn.Value),
			Target:      fn.File,
			Instruction: "云函数 " + fn.Value + " 的验证链:① 检查调用点传参是否携带身份(把 userId/openId 当参数=越权直通车);② 若存在 HTTP 触发路径用 http_probe 无凭据重放测未授权;③ 云函数内部是否用 wxContext.OPENID 做归属校验;结论按 未授权可调/需小程序上下文/不可达 三态回写",
		})
	}
	for i, coll := range inv.Collections {
		if i >= cloudbaseItemCap {
			break
		}
		out = append(out, cloudbaseItemDraft{
			Source:      "cloudbase:collection:" + coll.Value,
			Category:    "cloudbase_collection",
			Severity:    "high",
			Title:       fmt.Sprintf("云数据库集合权限验证:%s", coll.Value),
			Target:      coll.File,
			Instruction: "集合 " + coll.Value + " 的权限模型验证:确认预设档位(所有用户可读/仅创建者可读写/所有用户可读写)或自定义安全规则是否对每条读写下做了 _openid 归属校验;A/B 双身份用 B 的身份读/写 A 创建的文档,最小化验证后回写",
		})
	}
	if len(inv.FileIDs) > 0 {
		out = append(out, cloudbaseItemDraft{
			Source:      "cloudbase:storage",
			Category:    "cloudbase_storage",
			Severity:    "medium",
			Title:       fmt.Sprintf("云存储未授权访问验证(fileID 命中 %d 处)", len(inv.FileIDs)),
			Target:      inv.FileIDs[0].File,
			Instruction: "从 fileID 规律(env.bucket/路径)判断能否伪造遍历他人文件;getTempFileURL 换临时链接是否校验归属;最小化验证后回写",
		})
	}
	if len(inv.Anonymous) > 0 && len(inv.Envs) > 0 {
		out = append(out, cloudbaseItemDraft{
			Source:      "cloudbase:anonymous",
			Category:    "cloudbase_anonymous",
			Severity:    "medium",
			Title:       "云开发匿名登录暴露面验证",
			Target:      inv.Anonymous[0].File,
			Instruction: "包内存在匿名登录痕迹且有 env 标识:确认目标云环境是否开启匿名登录;开启则平台外可构造匿名凭据访问云资源(等价未授权),用 cloud_key_verify 验真手头云凭据后评估;结论回写",
		})
	}
	return out
}

// EnsureCloudBaseTestItems 云开发资产 → 测试项(幂等:(sample_id,source) 为键)。
// 返回新增条数;清单为空时零新增不报错。
func (s *Service) EnsureCloudBaseTestItems(sampleID string) (int, *CloudBaseInventory, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return 0, nil, err
	}
	inv, err := AnalyzeCloudBase(sample.Workspace)
	if err != nil {
		return 0, nil, err
	}
	drafts := buildCloudBaseDrafts(inv)
	if len(drafts) == 0 {
		return 0, inv, nil
	}
	existing := map[string]bool{}
	rows, err := s.db.Query(`SELECT source FROM miniprogram_test_items WHERE sample_id = $1`, sample.ID)
	if err != nil {
		return 0, inv, err
	}
	for rows.Next() {
		var src string
		if rows.Scan(&src) == nil {
			existing[src] = true
		}
	}
	_ = rows.Close()

	added := 0
	for _, d := range drafts {
		if existing[d.Source] {
			continue
		}
		if _, err := s.db.Exec(`INSERT INTO miniprogram_test_items
			(id, sample_id, source, source_type, category, severity, title, target, instruction, status, created_at, updated_at)
			VALUES ($1,$2,$3,'cloudbase',$4,$5,$6,$7,$8,$9,now(),now())`,
			uuid.NewString(), sample.ID, d.Source, d.Category, d.Severity, d.Title, d.Target, d.Instruction, ItemPending); err != nil {
			return added, inv, err
		}
		added++
	}
	return added, inv, nil
}
