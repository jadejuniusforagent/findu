package apkanalysis

// testitems — 扫描发现 → Agent 测试项(委派账本)。
// 自动分析报告生成后,把可行动的发现/攻击面展开为测试项;Agent 经
// android_test_items 拉取任务、android_test_item_update 回写状态与结论,
// 概览页读回显示 —— 形成 扫描定靶 → Agent 验证 → 结论回流 的闭环。

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// 测试项状态:pending → in_progress → confirmed / clean / na。
const (
	ItemPending    = "pending"
	ItemInProgress = "in_progress"
	ItemConfirmed  = "confirmed" // 风险确认
	ItemClean      = "clean"     // 排除
	ItemNA         = "na"        // 不适用
)

// TestItem 单个测试项。
type TestItem struct {
	ID             string    `json:"id"`
	SampleID       string    `json:"sample_id"`
	Source         string    `json:"source"`      // 来源标识(发现名/组件名/scheme),幂等键
	SourceType     string    `json:"source_type"` // finding | exported_component | deeplink | app_flag
	Category       string    `json:"category"`
	Severity       string    `json:"severity"`
	Title          string    `json:"title"`
	Target         string    `json:"target"` // 类名/组件名/scheme
	Instruction    string    `json:"instruction"`
	Status         string    `json:"status"`
	Conclusion     string    `json:"conclusion,omitempty"`
	Evidence       string    `json:"evidence,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// findingInstruction 发现类别 → 验证指令。
func findingInstruction(f AutoFinding) string {
	switch f.Category {
	case "加固":
		return "评估该加固/壳特征的影响:静态分析可行性、建议的脱壳方案(frida-dexdump/FART/BlackDex),给出是否继续静态深挖的结论"
	case "硬编码凭据":
		return "用 android_source 反编译证据行所在类,确认该凭据形态的真伪与用途;真凭据评估影响面(仅最小化验证,禁止访问业务资源/调用收费接口)"
	case "敏感串":
		return "反编译证据行定位的类,判断命中串是否为真实凭据(而非字段名/日志文案),给出结论与证据"
	case "动态加载":
		return "定位 DexClassLoader 的加载源路径,判断该路径是否外部可写(插件/热修劫持风险),给出证据"
	case "WebView":
		return "反编译注册 JS 桥的类,列出 @JavascriptInterface 暴露方法与参数校验情况,评估可达的敏感动作"
	case "加密":
		return "反编译证据行所在类,识别算法选择与密钥管理(硬编码密钥/ECB/固定IV),给出弱点结论"
	default:
		return "反编译证据行所在类,结合上下文评估该发现的真实风险,输出 结论/证据/影响"
	}
}

// itemSeverity 发现为零命中项不生成;info 级发现价值低,过滤。
func generatableFinding(f AutoFinding) bool {
	return f.Hits > 0 && f.Severity != "info"
}

// RegenerateTestItems 依据最新报告重建测试项(幂等:以 (sample_id, source)
// 为键,已存在的项保留状态;报告新出现项追加)。
func (s *Service) RegenerateTestItems(sampleID string) (added int, err error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return 0, err
	}
	st, err := s.GetAutoReport(sample.ID)
	if err != nil {
		return 0, err
	}
	if st.Report == nil {
		return 0, fmt.Errorf("尚无分析报告")
	}
	report := st.Report

	wants := buildTestItemDrafts(sample, report)
	existing := map[string]bool{}
	rows, err := s.db.Query(`SELECT source FROM apk_test_items WHERE sample_id = $1`, sample.ID)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var src string
		if rows.Scan(&src) == nil {
			existing[src] = true
		}
	}
	rows.Close()

	for _, d := range wants {
		if existing[d.Source] {
			continue
		}
		_, err := s.db.Exec(`INSERT INTO apk_test_items
			(id, sample_id, source, source_type, category, severity, title, target, instruction, status, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`,
			uuid.NewString(), sample.ID, d.Source, d.SourceType, d.Category, d.Severity,
			d.Title, d.Target, d.Instruction, ItemPending, time.Now())
		if err != nil {
			return added, err
		}
		added++
	}
	if added > 0 {
		s.log.Info("android test items generated", zap.String("sample", sample.Name), zap.Int("added", added))
	}
	return added, nil
}

// draft 测试项草稿。
type draft struct {
	Source      string
	SourceType  string
	Category    string
	Severity    string
	Title       string
	Target      string
	Instruction string
}

const (
	maxComponentItems = 30
	maxDeeplinkItems  = 10
)

// buildTestItemDrafts 报告 → 测试项草稿(发现 + 攻击面)。
func buildTestItemDrafts(sample *Sample, report *AutoReport) []draft {
	out := []draft{}
	for _, f := range report.Findings {
		if !generatableFinding(f) {
			continue
		}
		target := ""
		if len(f.Rows) > 0 {
			target = dotClassName(f.Rows[0].Class)
		}
		out = append(out, draft{
			Source:      "finding:" + f.Name,
			SourceType:  "finding",
			Category:    f.Category,
			Severity:    f.Severity,
			Title:       f.Name + fmt.Sprintf("(命中 %d 处)", f.Hits),
			Target:      target,
			Instruction: findingInstruction(f),
		})
	}
	// 攻击面:显式/隐式导出组件
	if sec := report.ManifestSecurity; sec != nil {
		n := 0
		for _, mAny := range []any{sec["explicit_exported"], sec["implicitly_exported"]} {
			m, ok := mAny.(map[string]any)
			if !ok {
				continue
			}
			for kind, listAny := range m {
				list, ok := listAny.([]any)
				if !ok {
					continue
				}
				for _, nameAny := range list {
					if n >= maxComponentItems {
						return out
					}
					name := fmt.Sprintf("%v", nameAny)
					out = append(out, draft{
						Source:      "component:" + name,
						SourceType:  "exported_component",
						Category:    "组件攻击面",
						Severity:    "high",
						Title:       fmt.Sprintf("导出组件 %s(%s)", name, kind),
						Target:      name,
						Instruction: "用 android_manifest 核对该组件 intent-filter/permission 保护,android_source 反编译入口类审计对外部 Intent 的信任程度,评估可否被任意 App 拉起/绑定/注入,给出复现路径",
					})
					n++
				}
			}
		}
		if schemes, ok := sec["deeplink_schemes"].([]any); ok {
			for i, schAny := range schemes {
				if i >= maxDeeplinkItems {
					break
				}
				sch := fmt.Sprintf("%v", schAny)
				out = append(out, draft{
					Source:      "deeplink:" + sch,
					SourceType:  "deeplink",
					Category:    "deeplink",
					Severity:    "medium",
					Title:       "deeplink scheme: " + sch + "://",
					Target:      sch + "://",
					Instruction: "静态构造该 scheme 的测试 URI(找带参数的处理 Activity,关注 token/支付参数),审计参数校验与敏感动作,给出可利用场景",
				})
			}
		}
		if flags, ok := sec["app_flags"].(map[string]any); ok {
			for flag := range flags {
				out = append(out, draft{
					Source:      "flag:" + flag,
					SourceType:  "app_flag",
					Category:    "配置风险",
					Severity:    "medium",
					Title:       "manifest 危险标志: " + flag,
					Target:      flag,
					Instruction: "说明该标志(debuggable/allowBackup/usesClefindutTraffic)在本应用的实际影响与利用路径",
				})
			}
		}
	}
	return out
}

// ────────────────────────── 查询 / 回写 ──────────────────────────

// ListTestItems 样本测试项(status 空 = 全部)。
func (s *Service) ListTestItems(sampleID, status string) ([]*TestItem, error) {
	where := `WHERE sample_id = $1`
	args := []any{sampleID}
	if status != "" {
		where += ` AND status = $2`
		args = append(args, status)
	}
	rows, err := s.db.Query(`SELECT id, sample_id, source, source_type, category, severity, title,
		target, instruction, status, COALESCE(conclusion,''), COALESCE(evidence,''), COALESCE(conversation_id,''),
		created_at, updated_at FROM apk_test_items `+where+` ORDER BY created_at ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TestItem{}
	for rows.Next() {
		it := &TestItem{}
		if err := rows.Scan(&it.ID, &it.SampleID, &it.Source, &it.SourceType, &it.Category, &it.Severity,
			&it.Title, &it.Target, &it.Instruction, &it.Status, &it.Conclusion, &it.Evidence,
			&it.ConversationID, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateTestItem Agent 回写测试项状态与结论。
func (s *Service) UpdateTestItem(itemID, status, conclusion, evidence, conversationID string) (*TestItem, error) {
	switch status {
	case ItemPending, ItemInProgress, ItemConfirmed, ItemClean, ItemNA:
	default:
		return nil, fmt.Errorf("invalid status: %s", status)
	}
	res, err := s.db.Exec(`UPDATE apk_test_items
		SET status = $2, conclusion = NULLIF($3,'')::text, evidence = NULLIF($4,'')::text,
		    conversation_id = NULLIF($5,'')::text, updated_at = $6
		WHERE id = $1`, itemID, status, conclusion, evidence, conversationID, time.Now())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errors.New("test item not found")
	}
	it := &TestItem{}
	err = s.db.QueryRow(`SELECT id, sample_id, source, source_type, category, severity, title,
		target, instruction, status, COALESCE(conclusion,''), COALESCE(evidence,''), COALESCE(conversation_id,''),
		created_at, updated_at FROM apk_test_items WHERE id = $1`, itemID).
		Scan(&it.ID, &it.SampleID, &it.Source, &it.SourceType, &it.Category, &it.Severity,
			&it.Title, &it.Target, &it.Instruction, &it.Status, &it.Conclusion, &it.Evidence,
			&it.ConversationID, &it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("test item not found")
	}
	return it, err
}
