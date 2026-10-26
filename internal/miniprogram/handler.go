package miniprogram

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"finduai/internal/audit"
	"finduai/internal/routereg"
	"finduai/internal/security"

	"github.com/gin-gonic/gin"
)

// Handler 小程序分析 HTTP handler
type Handler struct {
	svc        *Service
	audit      *audit.Service
	credClient *http.Client
}

// NewHandler 创建 handler
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, credClient: &http.Client{Timeout: 20 * time.Second}}
}

// Register 挂载路由到 /miniprogram 组
func (h *Handler) Register(rg gin.IRouter, mws ...gin.HandlerFunc) {
	g := rg.Group("/miniprogram", mws...)
	g.POST("/upload", security.RequirePermission("miniprogram:execute"), h.Upload)
	routereg.QueryPOST(g, "/samples", security.RequirePermission("miniprogram:read"), h.ListSamples)
	routereg.QueryPOST(g, "/samples/:id", security.RequirePermission("miniprogram:read"), h.GetSample)
	g.DELETE("/samples/:id", security.RequirePermission("miniprogram:execute"), h.DeleteSample)
	g.POST("/samples/bind", security.RequirePermission("miniprogram:execute"), h.BindProject)
	routereg.QueryPOST(g, "/samples/:id/source", security.RequirePermission("miniprogram:read"), h.GetSourceTree)
	g.GET("/samples/:id/file", security.RequirePermission("miniprogram:read"), h.GetSourceFile)
	routereg.QueryPOST(g, "/samples/:id/topology", security.RequirePermission("miniprogram:read"), h.GetTopology)
	routereg.QueryPOST(g, "/samples/:id/interface-workbench", security.RequirePermission("miniprogram:read"), h.GetInterfaceWorkbench)
	routereg.QueryPOST(g, "/samples/:id/findings", security.RequirePermission("miniprogram:read"), h.ListFindings)
	routereg.QueryPOST(g, "/samples/:id/test-items", security.RequirePermission("miniprogram:read"), h.ListTestItems)
	routereg.QueryPOST(g, "/tasks", security.RequirePermission("miniprogram:read"), h.ListTasks)
	routereg.QueryPOST(g, "/tasks/:id", security.RequirePermission("miniprogram:read"), h.GetTaskHTTP)
	g.POST("/samples/:id/scan", security.RequirePermission("miniprogram:execute"), h.Scan)
	g.POST("/samples/:id/appid", security.RequirePermission("miniprogram:execute"), h.SetSampleAppID)
	g.POST("/handoff", security.RequirePermission("miniprogram:execute"), h.Handoff)
	h.registerCredentialRoutes(rg)
}

// SetSampleAppID POST /samples/:id/appid —— 为待解密的挂起样本补填 AppID,触发解密解包与重扫
func (h *Handler) SetSampleAppID(c *gin.Context) {
	var req struct {
		AppID string `json:"appid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sample, err := h.svc.FillAppID(c.Param("id"), req.AppID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sample": sample})
}

// Upload 上传 wxapkg 文件。支持一次多个包（PC 微信 4.x 常拆成 __APP__ + _subpackages_* 分包），
// 合并解包为一个样本；加密包（V1MMWX）需同时提交 appid 表单字段用于解密。
func (h *Handler) Upload(c *gin.Context) {
	projectID := c.PostForm("project_id")
	appid := strings.TrimSpace(c.PostForm("appid"))
	source := c.PostForm("source")
	if source == "" {
		source = "gui_upload"
	}

	form, err := c.MultipartForm()
	if err != nil || form == nil || len(form.File["file"]) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}

	var parts []UploadPart
	var cleanup []string
	defer func() {
		for _, p := range cleanup {
			if err := os.Remove(p); err != nil {
				log.Printf("[miniprogram] 清理上传临时文件失败 %s: %v", p, err)
			}
		}
	}()
	for _, file := range form.File["file"] {
		if !strings.HasSuffix(strings.ToLower(file.Filename), ".wxapkg") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "only .wxapkg accepted: " + file.Filename})
			return
		}
		tmp, err := os.CreateTemp("", "miniprogram_upload_*")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		tmp.Close()
		cleanup = append(cleanup, tmp.Name())
		if err := c.SaveUploadedFile(file, tmp.Name()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		parts = append(parts, UploadPart{Path: tmp.Name(), Name: file.Filename})
	}

	// 获取 owner（与平台会话一致：security.CurrentSession）
	ownerID := ""
	if session, ok := security.CurrentSession(c); ok {
		ownerID = session.UserID
	}

	sample, err := h.svc.RegisterFromFiles(parts, projectID, ownerID, source, appid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sample": sample})
}

// ListSamples 样本列表。兼容分派:带 page/page_size/keyword 走分页,否则旧行为(全量)。
func (h *Handler) ListSamples(c *gin.Context) {
	if c.Query("page") != "" || c.Query("page_size") != "" || c.Query("keyword") != "" {
		h.ListSamplesPaged(c)
		return
	}
	samples, err := h.svc.ListSamples(c.Query("project_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if samples == nil {
		samples = []*Sample{}
	}
	c.JSON(http.StatusOK, gin.H{"samples": samples})
}

// ListSamplesPaged 样本库分页(page/page_size 缺省回退全量旧行为;keyword 匹配名称/appid/sha256)。
func (h *Handler) ListSamplesPaged(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "12"))
	p, err := h.svc.ListSamplesPaged(page, pageSize, c.Query("keyword"), c.Query("project_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p.Items == nil {
		p.Items = []*Sample{}
	}
	c.JSON(http.StatusOK, gin.H{
		"samples":   p.Items,
		"total":     p.Total,
		"page":      p.Page,
		"page_size": p.PageSize,
	})
}

// GetSample 样本详情
func (h *Handler) GetSample(c *gin.Context) {
	id := c.Param("id")
	sample, err := h.svc.GetSample(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sample not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sample": sample})
}

// DeleteSample 删除样本
func (h *Handler) DeleteSample(c *gin.Context) {
	id := c.Param("id")
	if err := h.svc.DeleteSample(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// BindProject 绑定/解绑样本项目（project_id 空 = 解绑）。
func (h *Handler) BindProject(c *gin.Context) {
	var req struct {
		SampleID  string `json:"sample_id"`
		ProjectID string `json:"project_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.SampleID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sample_id required"})
		return
	}
	if err := h.svc.BindProject(strings.TrimSpace(req.SampleID), strings.TrimSpace(req.ProjectID), ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GetSourceTree 源码文件树
func (h *Handler) GetSourceTree(c *gin.Context) {
	id := c.Param("id")
	files, err := h.svc.GetSourceFiles(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sample not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

// GetTopology 样本调用链/接口拓扑分析
func (h *Handler) GetTopology(c *gin.Context) {
	id := c.Param("id")
	res, err := h.svc.AnalyzeTopology(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sample not found"})
		return
	}
	c.JSON(http.StatusOK, res)
}

// GetInterfaceWorkbench 样本接口工作台（接口清单 + 越权/未授权候选标注）。
func (h *Handler) GetInterfaceWorkbench(c *gin.Context) {
	id := c.Param("id")
	res, err := h.svc.AnalyzeInterfaceWorkbench(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sample not found"})
		return
	}
	c.JSON(http.StatusOK, res)
}

// GetSourceFile 读取源码文件内容（?path= 相对路径，query 传参避免路径编码问题）
func (h *Handler) GetSourceFile(c *gin.Context) {
	id := c.Param("id")
	filePath := strings.TrimPrefix(c.Query("path"), "/")

	data, err := h.svc.ReadSourceFile(id, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	// 根据扩展名设置 Content-Type
	ext := strings.ToLower(filepath.Ext(filePath))
	ct := "text/plain"
	switch ext {
	case ".js", ".json":
		ct = "application/javascript"
	case ".wxml":
		ct = "text/html"
	case ".wxss", ".css":
		ct = "text/css"
	case ".ts":
		ct = "application/typescript"
	}
	c.Data(http.StatusOK, ct, data)
}

// ListFindings 敏感信息发现列表
func (h *Handler) ListFindings(c *gin.Context) {
	id := c.Param("id")
	category := c.Query("category")
	findings, err := h.svc.ListFindings(id, category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"findings": findings})
}

// ListTestItems 样本测试项(概览页测试状态数据源;回写走 Agent MCP 工具)。
func (h *Handler) ListTestItems(c *gin.Context) {
	items, err := h.svc.ListTestItems(c.Param("id"), c.Query("status"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// Scan 触发重新扫描(落一条 scan 分析记录)
func (h *Handler) Scan(c *gin.Context) {
	id := c.Param("id")
	sample, err := h.svc.GetSample(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sample not found"})
		return
	}
	if err := h.svc.ScanWithTask(sample); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListTasks 分析记录分页(sample_id/action/status 过滤)。
func (h *Handler) ListTasks(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))
	p, err := h.svc.ListTasksPaged(page, pageSize, c.Query("sample_id"), c.Query("action"), c.Query("status"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p.Items == nil {
		p.Items = []*ScanTask{}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": p.Items, "total": p.Total, "page": p.Page, "page_size": p.PageSize})
}

// GetTaskHTTP 分析记录详情(查询/结果 JSON)。
func (h *Handler) GetTaskHTTP(c *gin.Context) {
	d, err := h.svc.GetTask(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

// Handoff 交接给 Agent
func (h *Handler) Handoff(c *gin.Context) {
	var req HandoffRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if req.SampleID == "" {
		req.SampleID = c.Query("sample_id")
	}
	if req.Scope == "" {
		req.Scope = c.Query("scope")
	}

	result, err := h.svc.BuildHandoff(req.SampleID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
