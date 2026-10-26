package miniprogram

import (
	"fmt"
	"strings"
)

// HandoffScope 支持的交接范围
const (
	ScopeReport   = "report"   // 整份报告（样本概要 + 全部敏感发现）
	ScopeFindings = "findings" // 敏感发现清单
	ScopeSource   = "source"   // 指定文件源码
	ScopeFindRefs = "findrefs" // 搜索命中上下文
)

// HandoffRole 交接会话落地的默认角色名，与 roles/小程序安全测试.yaml 的 name 一致。
// 🔴 2026-09-18 由「渗透测试」改为「小程序安全测试」：小程序交接的测试语境
// (敏感信息/硬编码凭据/JSBridge/开放数据域)与该角色提示词一一对应，固定落
// 「渗透测试」曾造成角色错配(其提示词还需提示用户再切角色)。前端交接弹窗
// 可选角色覆盖此默认；未知角色由发送链 resolveRoleToPersist 兜底。
const HandoffRole = "小程序安全测试"

// HandoffRequest 交接请求
type HandoffRequest struct {
	Scope    string `json:"scope"`
	SampleID string `json:"sample_id"`
	Role     string `json:"role"` // 可选,覆盖默认交接角色(见 HandoffRole)
	Target   struct {
		FilePath string `json:"file_path"` // scope=source
		Keyword  string `json:"keyword"`   // scope=findrefs
	} `json:"target"`
}

// HandoffResult 交接载荷
type HandoffResult struct {
	SampleID   string `json:"sample_id"`
	SampleName string `json:"sample_name"`
	Title      string `json:"title"`
	ProjectID  string `json:"project_id,omitempty"`
	Role       string `json:"role,omitempty"`
	Message    string `json:"message"`
	Summary    string `json:"summary"`
}

// BuildHandoff 组装交接消息
func (s *Service) BuildHandoff(sampleID string, req HandoffRequest) (*HandoffResult, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, fmt.Errorf("样本不存在: %w", err)
	}

	scope := req.Scope
	if scope == "" {
		scope = ScopeReport
	}

	// 获取发现
	findings, _ := s.ListFindings(sample.ID, "")
	// 获取文件列表
	files, _ := s.GetSourceFiles(sample.ID)

	var lines []string
	lines = append(lines, fmt.Sprintf("# 小程序样本交接：%s", sample.Name))
	lines = append(lines, "")
	lines = append(lines, "## 样本信息")
	lines = append(lines, fmt.Sprintf("- ID: %s", sample.ID))
	lines = append(lines, fmt.Sprintf("- 文件名: %s", sample.Name))
	shortSum := sample.SHA256
	if len(shortSum) > 16 {
		shortSum = shortSum[:16] + "..."
	}
	lines = append(lines, fmt.Sprintf("- SHA256: %s", shortSum))
	if sample.AppID != "" {
		lines = append(lines, fmt.Sprintf("- AppID: %s", sample.AppID))
	}
	lines = append(lines, fmt.Sprintf("- 文件数: %d", sample.FileCount))
	lines = append(lines, fmt.Sprintf("- 包大小: %d bytes", sample.Size))
	lines = append(lines, "")

	// 敏感发现摘要
	lines = append(lines, "## 敏感信息扫描结果")
	if len(findings) == 0 {
		lines = append(lines, "暂无敏感信息发现。")
	} else {
		// 按严重级别分组统计（固定顺序，避免 map 随机序）
		counts := map[string]int{}
		for _, f := range findings {
			counts[f.Severity]++
		}
		for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
			if cnt := counts[sev]; cnt > 0 {
				lines = append(lines, fmt.Sprintf("- %s: %d 项", sev, cnt))
			}
		}
		lines = append(lines, "")
		lines = append(lines, "详细发现列表：")
		// 单条上限 100 条，避免超长提示词
		limit := len(findings)
		if limit > 100 {
			limit = 100
		}
		for _, f := range findings[:limit] {
			lines = append(lines, fmt.Sprintf("- [%s] %s (L%d): %s", f.Severity, f.FilePath, f.LineNumber, f.Description))
		}
		if len(findings) > limit {
			lines = append(lines, fmt.Sprintf("... 等共 %d 条发现", len(findings)))
		}
	}
	lines = append(lines, "")

	// 源码文件列表（顶层目录）
	lines = append(lines, "## 源码文件概览")
	if len(files) == 0 {
		lines = append(lines, "暂无源码文件。")
	} else {
		// 只展示前 30 个文件
		limit := len(files)
		if limit > 30 {
			limit = 30
		}
		for _, f := range files[:limit] {
			lines = append(lines, fmt.Sprintf("- %s", f))
		}
		if len(files) > 30 {
			lines = append(lines, fmt.Sprintf("... 等共 %d 个文件", len(files)))
		}
	}
	lines = append(lines, "")

	// scope=source：附上指定文件内容，方便 Agent 直接分析
	if scope == ScopeSource && req.Target.FilePath != "" {
		if content, rerr := s.ReadSourceFile(sample.ID, req.Target.FilePath); rerr == nil {
			lines = append(lines, fmt.Sprintf("## 待分析文件：%s", req.Target.FilePath))
			lines = append(lines, "```")
			text := string(content)
			if len(text) > 30*1024 {
				text = text[:30*1024] + "\n...[截断]"
			}
			lines = append(lines, text)
			lines = append(lines, "```")
			lines = append(lines, "")
		}
	}

	// scope=findrefs：附上指定关键词的搜索结果
	if scope == ScopeFindRefs && req.Target.Keyword != "" {
		hits, _ := s.SearchWorkspace(sample, req.Target.Keyword, 50)
		lines = append(lines, fmt.Sprintf("## 关键词「%s」搜索命中（%d 条）", req.Target.Keyword, len(hits)))
		for _, h := range hits {
			lines = append(lines, fmt.Sprintf("- %s:%d  %s", h.File, h.Line, h.Text))
		}
		lines = append(lines, "")
	}

	// 建议测试方向
	lines = append(lines, "## 建议测试方向")
	lines = append(lines, "1. 检查 API 接口鉴权（未授权访问、越权）")
	lines = append(lines, "2. 检查云函数调用鉴权（cloud.callFunction 参数篡改）")
	lines = append(lines, "3. 检查跳转参数注入（navigateTo/web-view URL 注入）")
	lines = append(lines, "4. 检查硬编码密钥/凭据泄露")
	lines = append(lines, "5. 检查 WebView 组件安全（JSBridge 调用）")
	lines = append(lines, "6. 检查开放数据域权限")
	lines = append(lines, "")
	lines = append(lines, "请使用 miniprogram_source 和 miniprogram_findrefs 工具深入分析。")

	title := fmt.Sprintf("小程序渗透测试：%s", sample.Name)
	summary := fmt.Sprintf("样本 %s · %d 个发现 · %d 个文件", sample.Name, len(findings), len(files))

	handoffRole := HandoffRole
	if strings.TrimSpace(req.Role) != "" {
		handoffRole = strings.TrimSpace(req.Role)
	}
	return &HandoffResult{
		SampleID:   sample.ID,
		SampleName: sample.Name,
		Title:      title,
		ProjectID:  sample.ProjectID,
		Role:       handoffRole,
		Message:    strings.Join(lines, "\n"),
		Summary:    summary,
	}, nil
}
