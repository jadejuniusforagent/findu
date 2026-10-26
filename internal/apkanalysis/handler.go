package apkanalysis

// HTTP handler —— 全部挂 protected 路由组(JWT + android:* 权限点)。

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"finduai/internal/routereg"
	"finduai/internal/security"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 挂载路由。权限点:android:read(读)/ android:execute(写与扫描)。
func (h *Handler) Register(rg gin.IRouter, mws ...gin.HandlerFunc) {
	g := rg.Group("/android", mws...)
	routereg.QueryPOST(g, "/samples", security.RequirePermission("android:read"), h.ListSamplesDispatch)
	g.POST("/samples/bind", security.RequirePermission("android:execute"), h.BindProject)
	g.DELETE("/samples/:id", security.RequirePermission("android:execute"), h.DeleteSample)
	g.POST("/upload", security.RequirePermission("android:execute"), h.Upload)
	g.POST("/auto-analyze", security.RequirePermission("android:execute"), h.AutoAnalyze)
	routereg.QueryPOST(g, "/auto-report", security.RequirePermission("android:read"), h.AutoReport)
	g.POST("/findrefs", security.RequirePermission("android:execute"), h.FindRefs)
	routereg.QueryPOST(g, "/tasks", security.RequirePermission("android:read"), h.ListTasks)
	routereg.QueryPOST(g, "/runtime", security.RequirePermission("android:read"), h.Runtime)
	routereg.QueryPOST(g, "/source", security.RequirePermission("android:read"), h.GetSource)
	routereg.QueryPOST(g, "/manifest", security.RequirePermission("android:read"), h.GetManifest)

	// ── 反编译后端管理(jadx 上传/路径/首选/验证/移除)──
	routereg.QueryPOST(g, "/backends", security.RequirePermission("android:read"), h.ListBackends)
	g.POST("/backends/path", security.RequirePermission("android:execute"), h.AddBackendByPath)
	g.POST("/backends/upload", security.RequirePermission("android:execute"), h.UploadBackend)
	g.PUT("/backends/:id/prefer", security.RequirePermission("android:execute"), h.SetPreferBackend)
	g.POST("/backends/:id/verify", security.RequirePermission("android:execute"), h.VerifyBackend)
	g.DELETE("/backends/:id", security.RequirePermission("android:execute"), h.RemoveBackend)

	// ── 工作台增量路由(GET /samples/:id 与 GET /tasks/:id 不与既有静态路由
	// 同方法冲突;POST 维持 body 传 sample_id,避开 gin static/param 混挂限制)──
	routereg.QueryPOST(g, "/samples/:id", security.RequirePermission("android:read"), h.GetSampleHTTP)
	routereg.QueryPOST(g, "/samples/:id/report", security.RequirePermission("android:read"), h.GetSampleReport)
	routereg.QueryPOST(g, "/samples/:id/tasks", security.RequirePermission("android:read"), h.ListSampleTasks)
	routereg.QueryPOST(g, "/samples/:id/test-items", security.RequirePermission("android:read"), h.ListTestItems)
	g.POST("/handoff", security.RequirePermission("android:execute"), h.Handoff)
	routereg.QueryPOST(g, "/tasks/:id", security.RequirePermission("android:read"), h.GetTaskHTTP)
	g.GET("/tasks/:id/events", security.RequirePermission("android:read"), h.TaskEvents)

	// ── 样本对比 ──
	routereg.QueryPOST(g, "/compare", security.RequirePermission("android:read"), h.CompareSamples)
	routereg.QueryPOST(g, "/samples/:id/versions", security.RequirePermission("android:read"), h.SamePackageVersions)

	// ── 代码工作台 ──
	routereg.QueryPOST(g, "/samples/:id/classes", security.RequirePermission("android:read"), h.ListClasses)
	routereg.QueryPOST(g, "/samples/:id/classes/:class/outline", security.RequirePermission("android:read"), h.GetClassOutline)
	routereg.QueryPOST(g, "/samples/:id/classes/:class/smali", security.RequirePermission("android:read"), h.GetClassSmali)
	routereg.QueryPOST(g, "/samples/:id/classes/:class/smali-standard", security.RequirePermission("android:read"), h.GetClassSmaliStandard)
}

// Runtime 反编译后端 + 自动更新监控状态。
func (h *Handler) Runtime(c *gin.Context) {
	out := map[string]any{
		"decompiler_available": !errors.Is(h.svc.DecompilerAvailability(), ErrDecompilerUnavailable),
		"backends":             h.svc.Backends().List(c.Request.Context()),
		"preferred":            h.svc.Backends().Prefer(),
	}
	// ASC 自动更新器已下线（宿主侧 Python 依赖移除）：保留该字段兼容旧前端；
	// 反编译能力现由内置 Go 引擎（go-dad/smali）与上传的 jadx 等外部后端提供。
	out["updater"] = map[string]any{"enabled": false, "reason": "ASC 自动更新已下线，反编译由内置 Go 引擎提供"}
	c.JSON(http.StatusOK, out)
}

// ListBackends 后端列表(DB 记录,含验证状态)。
func (h *Handler) ListBackends(c *gin.Context) {
	records, err := h.svc.ListBackends(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if records == nil {
		records = []BackendRecord{}
	}
	c.JSON(http.StatusOK, gin.H{"backends": records})
}

// AddBackendByPath 指定本地路径接入 jadx 后端。
func (h *Handler) AddBackendByPath(c *gin.Context) {
	var req struct {
		Path     string `json:"path"`
		JavaPath string `json:"java_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path required"})
		return
	}
	rec, err := h.svc.AddJadxByPath(c.Request.Context(), req.Path, req.JavaPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"backend": rec})
}

// UploadBackend 上传 jadx 发行包(jar/zip)。multipart 字段: file / java_path。
func (h *Handler) UploadBackend(c *gin.Context) {
	javaPath := c.PostForm("java_path")
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}
	lower := strings.ToLower(file.Filename)
	if !strings.HasSuffix(lower, ".jar") && !strings.HasSuffix(lower, ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "仅支持 jar / zip 上传"})
		return
	}
	tmp, err := os.CreateTemp("", "backend_upload_*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := c.SaveUploadedFile(file, tmp.Name()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	rec, err := h.svc.UploadJadx(c.Request.Context(), tmp.Name(), file.Filename, javaPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"backend": rec})
}

// SetPreferBackend 设指定后端的 kind 为首选。
func (h *Handler) SetPreferBackend(c *gin.Context) {
	if err := h.svc.SetPreferredBackend(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "preferred": c.Param("id")})
}

// VerifyBackend 手动触发后端冒烟验证。
func (h *Handler) VerifyBackend(c *gin.Context) {
	rec, err := h.svc.VerifyBackend(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"backend": rec})
}

// RemoveBackend 移除后端。
func (h *Handler) RemoveBackend(c *gin.Context) {
	if err := h.svc.RemoveBackend(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListSamplesDispatch 兼容分派:带 page/page_size/keyword 走分页,否则旧行为(全量)。
func (h *Handler) ListSamplesDispatch(c *gin.Context) {
	if c.Query("page") != "" || c.Query("page_size") != "" || c.Query("keyword") != "" {
		h.ListSamplesPaged(c)
		return
	}
	h.ListSamples(c)
}

func (h *Handler) ListSamples(c *gin.Context) {
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

// ListSamplesPaged 工作台分页样本列表(page/page_size 缺省时回退全量旧行为)。
func (h *Handler) ListSamplesPaged(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
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

func (h *Handler) GetSampleHTTP(c *gin.Context) {
	sample, err := h.svc.GetSampleByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sample": sample})
}

// GetSampleReport 样本报告(?export=md 时返回 Markdown 下载)。
func (h *Handler) GetSampleReport(c *gin.Context) {
	st, err := h.svc.GetAutoReport(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if c.Query("export") == "md" {
		if st.Report == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "尚无分析报告"})
			return
		}
		name := strings.TrimSuffix(st.Report.SampleName, ".apk") + "-report.md"
		c.Header("Content-Disposition", `attachment; filename="`+url.PathEscape(name)+`"`)
		c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(ReportMarkdown(st.Report)))
		return
	}
	c.JSON(http.StatusOK, st)
}

// ListSampleTasks 样本维度任务分页。
func (h *Handler) ListSampleTasks(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))
	p, err := h.svc.ListTasksPaged(page, pageSize, c.Param("id"), c.Query("action"), c.Query("status"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p.Items == nil {
		p.Items = []*ScanTask{}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": p.Items, "total": p.Total, "page": p.Page, "page_size": p.PageSize})
}

// Handoff 扫描结果 → Agent 交接契约(scope + filter,提示词服务端组装)。
func (h *Handler) Handoff(c *gin.Context) {
	var req HandoffRequest
	req.Scope = c.Query("scope") // 兼容 query 传参
	if err := c.ShouldBindJSON(&req); err != nil {
		// body 为空时按 query 参数构建(scope 仅;filter 暂不支持 query 形态)
		req = HandoffRequest{Scope: c.Query("scope")}
	}
	sampleID := strings.TrimSpace(req.SampleID)
	if sampleID == "" {
		sampleID = strings.TrimSpace(c.Query("sample_id"))
	}
	if sampleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sample_id required"})
		return
	}
	res, err := h.svc.BuildHandoff(sampleID, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) GetTaskHTTP(c *gin.Context) {
	task, err := h.svc.GetTask(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, task)
}

// TaskEvents SSE 任务进度流:订阅任务状态/进度,终态后关闭。
// 鉴权走中间件(?token= 由 auth 中间件为 SSE 请求放行)。
func (h *Handler) TaskEvents(c *gin.Context) {
	taskID := c.Param("id")
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	deadline := time.After(15 * time.Minute)
	writeEvent := func(payload any) bool {
		b, err := json.Marshal(payload)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(c.Writer, "event: task\ndata: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-deadline:
			return
		case <-tick.C:
		}
		task, err := h.svc.GetTask(taskID)
		if err != nil {
			// 任务不存在:一次性终态事件后关闭
			writeEvent(gin.H{"task_id": taskID, "status": "error", "error": err.Error()})
			return
		}
		payload := gin.H{"task_id": task.ID, "status": task.Status, "progress": task.Progress}
		if task.Error != "" {
			payload["error"] = task.Error
		}
		if !writeEvent(payload) {
			return
		}
		if task.Status == "done" || task.Status == "error" {
			return
		}
	}
}

func (h *Handler) BindProject(c *gin.Context) {
	var req struct {
		SampleID  string `json:"sample_id"`
		ProjectID string `json:"project_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SampleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sample_id required"})
		return
	}
	if err := h.svc.BindProject(req.SampleID, req.ProjectID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) DeleteSample(c *gin.Context) {
	if err := h.svc.DeleteSample(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Upload 多部分上传 → 归档样本。与对话上传互通:也接受 JSON body 里的
// server_path(chat_uploads 下的绝对路径,由前端回传)。
func (h *Handler) Upload(c *gin.Context) {
	projectID := c.PostForm("project_id")
	source := c.PostForm("source")
	if source == "" {
		source = "gui_upload"
	}

	var srcPath, name string
	if serverPath := strings.TrimSpace(c.PostForm("server_path")); serverPath != "" {
		// chat_uploads 附件路径:防穿越校验
		if !IsSafeUploadPath(serverPath) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid server_path"})
			return
		}
		srcPath, name = serverPath, filepath.Base(serverPath)
	} else {
		file, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
			return
		}
		tmp, err := os.CreateTemp("", "apk_upload_*")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if err := c.SaveUploadedFile(file, tmp.Name()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		srcPath, name = tmp.Name(), file.Filename
	}

	if !strings.HasSuffix(strings.ToLower(name), ".apk") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only .apk accepted"})
		return
	}

	sample, err := h.svc.RegisterFromFile(c.Request.Context(), srcPath, name, projectID, source)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 上传即自动解包分析(后台任务,前端轮询 auto-report 取报告);
	// 显式传 auto_analyze=0 可跳过
	resp := gin.H{"sample": sample}
	if c.PostForm("auto_analyze") != "0" {
		if taskID, _, err := h.svc.StartAutoAnalyze(sample.ID); err == nil {
			resp["auto_analyze_task_id"] = taskID
		}
	}
	c.JSON(http.StatusOK, resp)
}

// AutoAnalyze 手动触发自动解包分析(幂等:同样本已有 running 任务则复用)。
func (h *Handler) AutoAnalyze(c *gin.Context) {
	var req struct {
		SampleID string `json:"sample_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SampleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sample_id required"})
		return
	}
	taskID, started, err := h.svc.StartAutoAnalyze(req.SampleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": taskID, "status": "running", "started": started})
}

// AutoReport 轮询自动分析状态/报告(none=从未分析)。
func (h *Handler) AutoReport(c *gin.Context) {
	sampleID := c.Query("sample_id")
	if sampleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sample_id required"})
		return
	}
	st, err := h.svc.GetAutoReport(sampleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

// IsSafeUploadPath 校验绝对路径落在对话附件目录（chat_uploads）下(与
// internal/handler/agent.go 的 validateChatAttachmentServerPath 同语义)。
// HTTP 上传的 server_path 参数用它防止任意文件收编；Agent 工具收编走
// ResolveIngestFile（额外允许会话工作区与会话产物，见 ingest.go）。
func IsSafeUploadPath(p string) bool {
	if strings.TrimSpace(p) == "" || !filepath.IsAbs(p) {
		return false
	}
	root, err := security.ChatUploadsRootDir()
	if err != nil {
		return false
	}
	return security.PathWithin(root, filepath.Clean(p))
}

func (h *Handler) FindRefs(c *gin.Context) {
	var req FindRefsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.svc.FindRefs(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) ListTasks(c *gin.Context) {
	tasks, err := h.svc.ListTasks(c.Query("sample_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tasks == nil {
		tasks = []*ScanTask{}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func (h *Handler) GetSource(c *gin.Context) {
	dexName, source, err := h.svc.GetSource(c.Request.Context(), c.Query("apk"), c.Query("class"))
	switch {
	case errors.Is(err, ErrDecompilerUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	case err != nil:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"dex": dexName, "class": c.Query("class"), "source": source})
}

func (h *Handler) GetManifest(c *gin.Context) {
	xml, err := h.svc.Manifest(c.Request.Context(), c.Query("apk"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/xml", []byte(xml))
}

// ListTestItems 样本测试项(概览页测试状态列数据源)。
func (h *Handler) ListTestItems(c *gin.Context) {
	items, err := h.svc.ListTestItems(c.Param("id"), c.Query("status"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// CompareSamples 两样本最新报告对比(GET query: base/target)。
func (h *Handler) CompareSamples(c *gin.Context) {
	res, err := h.svc.CompareSamples(c.Query("base"), c.Query("target"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// SamePackageVersions 同包名其他样本(对比候选)。
func (h *Handler) SamePackageVersions(c *gin.Context) {
	samples, err := h.svc.SamePackageSamples(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"samples": samples})
}

// ListClasses 类浏览(tree/list,prefix 关键词过滤)。
func (h *Handler) ListClasses(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "500"))
	offset := (page - 1) * pageSize
	listing, err := h.svc.ListClasses(c.Param("id"), c.Query("prefix"), c.Query("keyword"), offset, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, listing)
}

// GetClassOutline 类的方法/字段大纲。
func (h *Handler) GetClassOutline(c *gin.Context) {
	outline, err := h.svc.GetClassOutline(c.Param("id"), c.Param("class"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, outline)
}

// GetClassSmali 类的 smali 字节码视图(反编译链兜底)。
func (h *Handler) GetClassSmali(c *gin.Context) {
	text, err := h.svc.GetClassSmali(c.Param("id"), c.Param("class"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"class": c.Param("class"), "smali": text})
}

// GetClassSmaliStandard 标准 smali 输出(可被 smali.jar 汇编)。
func (h *Handler) GetClassSmaliStandard(c *gin.Context) {
	text, err := h.svc.GetClassSmaliStandard(c.Param("id"), c.Param("class"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"class": c.Param("class"), "smali": text, "format": "standard"})
}
