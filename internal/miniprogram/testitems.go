package miniprogram

// testitems — 扫描发现 → Agent 测试项(委派账本),消费面与 apkanalysis 对齐。
// 敏感扫描完成后把命中的发现类别展开为测试项;Agent 经
// miniprogram_test_items 拉取任务、miniprogram_test_item_update 回写状态与结论,
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
	Source         string    `json:"source"`      // 来源标识(幂等键,如 finding:api_endpoint)
	SourceType     string    `json:"source_type"` // finding(发现类别)
	Category       string    `json:"category"`
	Severity       string    `json:"severity"`
	Title          string    `json:"title"`
	Target         string    `json:"target"` // 证据所在文件(首个命中)
	Instruction    string    `json:"instruction"`
	Status         string    `json:"status"`
	Conclusion     string    `json:"conclusion,omitempty"`
	Evidence       string    `json:"evidence,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// categoryLabel 发现类别 → 中文名(前端筛选器同源,保持一致)。
func categoryLabel(category string) string {
	labels := map[string]string{
		"api_endpoint":  "API 接口",
		"secret":        "密钥",
		"appid":         "AppID",
		"cloud_env":     "云环境",
		"sensitive_url": "敏感 URL",
	}
	return labels[category]
}

// categoryInstruction 发现类别 → 验证指令。
func categoryInstruction(category string) string {
	switch category {
	case "api_endpoint":
		return "结合页面代码确认该 API 端点的用途与鉴权方式(是否带签名/token);对未鉴权端点做最小化重放验证未授权访问与越权风险,给出结论与证据"
	case "secret":
		return "定位密钥所在文件与使用点(源码内全文检索引用),判断是真实凭据还是占位/测试值;真凭据评估影响面(仅最小化验证,禁止调用收费/业务接口)"
	case "appid":
		return "核对该 AppID 暴露位置与用途,评估被冒用风险(分享链接伪造/接口拼凑),给出结论与证据"
	case "cloud_env":
		return "确认云开发环境 ID 的暴露面,静态检索云调用点,评估未授权访问云资源(数据库/存储/函数)的可行性,给出验证思路"
	case "sensitive_url":
		return "核验该 URL 的归属与暴露面(仅最小化访问确认存活),判断是否内部系统/管理后台/测试环境,给出结论与证据"
	default:
		return "结合解包源码上下文评估该发现的真实风险,输出 结论/证据/影响"
	}
}

// itemSeverity 同类别里最严重的非 info 级别;全 info 不生成(价值低,对齐安卓)。
func itemSeverity(items []*Finding) (severity string, count int) {
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}
	worst := ""
	for _, f := range items {
		if _, known := rank[f.Severity]; !known {
			f.Severity = "info"
		}
		if f.Severity == "info" {
			continue
		}
		count++
		if worst == "" || rank[f.Severity] < rank[worst] {
			worst = f.Severity
		}
	}
	return worst, count
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

// buildTestItemDrafts 发现列表(已按类别分组)→ 测试项草稿。
func buildTestItemDrafts(byCategory map[string][]*Finding) []draft {
	out := []draft{}
	for category, items := range byCategory {
		severity, count := itemSeverity(items)
		if count == 0 {
			continue
		}
		target := ""
		if len(items) > 0 {
			target = items[0].FilePath
		}
		label := categoryLabel(category)
		if label == "" {
			label = category
		}
		out = append(out, draft{
			Source:      "finding:" + category,
			SourceType:  "finding",
			Category:    category,
			Severity:    severity,
			Title:       fmt.Sprintf("%s泄漏(命中 %d 处)", label, count),
			Target:      target,
			Instruction: categoryInstruction(category),
		})
	}
	return out
}

// RegenerateTestItems 依据最新扫描发现重建测试项(幂等:以 (sample_id, source)
// 为键,已存在的项保留状态;扫描新出现类别追加)。RunScan 完成后自动调用。
func (s *Service) RegenerateTestItems(sampleID string) (added int, err error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return 0, err
	}
	findings, err := s.ListFindings(sample.ID, "")
	if err != nil {
		return 0, err
	}
	byCategory := map[string][]*Finding{}
	for _, f := range findings {
		byCategory[f.Category] = append(byCategory[f.Category], f)
	}
	wants := buildTestItemDrafts(byCategory)

	existing := map[string]bool{}
	rows, err := s.db.Query(`SELECT source FROM miniprogram_test_items WHERE sample_id = $1`, sample.ID)
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
		_, err := s.db.Exec(`INSERT INTO miniprogram_test_items
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
		s.log.Info("miniprogram test items generated", zap.String("sample", sample.Name), zap.Int("added", added))
	}
	return added, nil
}

// ────────────────────────── 查询 / 回写 ──────────────────────────

const testItemColumns = `id, sample_id, source, source_type, category, severity, title,
	target, instruction, status, COALESCE(conclusion,''), COALESCE(evidence,''), COALESCE(conversation_id,''),
	created_at, updated_at`

func scanTestItem(it *TestItem, row interface{ Scan(dest ...any) error }) error {
	return row.Scan(&it.ID, &it.SampleID, &it.Source, &it.SourceType, &it.Category, &it.Severity,
		&it.Title, &it.Target, &it.Instruction, &it.Status, &it.Conclusion, &it.Evidence,
		&it.ConversationID, &it.CreatedAt, &it.UpdatedAt)
}

// ListTestItems 样本测试项(status 空 = 全部)。
func (s *Service) ListTestItems(sampleID, status string) ([]*TestItem, error) {
	where := `WHERE sample_id = $1`
	args := []any{sampleID}
	if status != "" {
		where += ` AND status = $2`
		args = append(args, status)
	}
	rows, err := s.db.Query(`SELECT `+testItemColumns+` FROM miniprogram_test_items `+where+` ORDER BY created_at ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TestItem{}
	for rows.Next() {
		it := &TestItem{}
		if err := scanTestItem(it, rows); err != nil {
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
	res, err := s.db.Exec(`UPDATE miniprogram_test_items
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
	err = scanTestItem(it, s.db.QueryRow(`SELECT `+testItemColumns+` FROM miniprogram_test_items WHERE id = $1`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("test item not found")
	}
	return it, err
}
