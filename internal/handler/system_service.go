package handler

import (
	"net/http"

	"finduai/internal/syssvc"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SystemServiceHandler 本机运行服务处理器（按模块统计 + 按需启停/开机自启）。
type SystemServiceHandler struct {
	registry *syssvc.Registry
	logger   *zap.Logger
}

// NewSystemServiceHandler 创建运行服务处理器。
func NewSystemServiceHandler(registry *syssvc.Registry, logger *zap.Logger) *SystemServiceHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &SystemServiceHandler{registry: registry, logger: logger}
}

// ListSystemServices 返回按模块分组的服务状态快照（含统计）。
// GET /api/system-services
func (h *SystemServiceHandler) ListSystemServices(c *gin.Context) {
	if !h.registry.Enabled() {
		c.JSON(http.StatusOK, gin.H{
			"enabled": false,
			"message": "未配置 system_services，请在配置文件中开启并登记模块/服务",
			"modules": []any{},
			"total":   0, "running": 0, "stopped": 0, "autostart_ok": 0,
		})
		return
	}
	snap := h.registry.Snapshot(c.Request.Context())
	c.JSON(http.StatusOK, snap)
}

// StartSystemService 启动指定服务。
// POST /api/system-services/:id/start
func (h *SystemServiceHandler) StartSystemService(c *gin.Context) {
	h.doAction(c, "start")
}

// StopSystemService 停止指定服务。
// POST /api/system-services/:id/stop
func (h *SystemServiceHandler) StopSystemService(c *gin.Context) {
	h.doAction(c, "stop")
}

// RestartSystemService 重启指定服务。
// POST /api/system-services/:id/restart
func (h *SystemServiceHandler) RestartSystemService(c *gin.Context) {
	h.doAction(c, "restart")
}

// SetSystemServiceAutostart 设置/取消开机自启（systemd enable/disable）。
// POST /api/system-services/:id/autostart  body: {"enable": true|false}
func (h *SystemServiceHandler) SetSystemServiceAutostart(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Enable *bool `json:"enable"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enable == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要 JSON 体 {\"enable\": true|false}"})
		return
	}
	if err := h.registry.SetAutostart(c.Request.Context(), id, *req.Enable); err != nil {
		h.logger.Warn("设置开机自启失败", zap.String("id", id), zap.Bool("enable", *req.Enable), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.respondStatus(c, id)
}

// GetSystemServiceStatus 返回单个服务最新状态。
// GET /api/system-services/:id
func (h *SystemServiceHandler) GetSystemServiceStatus(c *gin.Context) {
	h.respondStatus(c, c.Param("id"))
}

// doAction 执行 start/stop/restart 并回传最新状态。
func (h *SystemServiceHandler) doAction(c *gin.Context, action string) {
	id := c.Param("id")
	ctx := c.Request.Context()
	var err error
	switch action {
	case "start":
		err = h.registry.Start(ctx, id)
	case "stop":
		err = h.registry.Stop(ctx, id)
	case "restart":
		err = h.registry.Restart(ctx, id)
	}
	if err != nil {
		h.logger.Warn("服务操作失败", zap.String("id", id), zap.String("action", action), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "action": action, "id": id})
		return
	}
	h.logger.Info("服务操作成功", zap.String("id", id), zap.String("action", action))
	h.respondStatus(c, id)
}

// respondStatus 返回指定服务的最新探测状态。
func (h *SystemServiceHandler) respondStatus(c *gin.Context, id string) {
	st, err := h.registry.Status(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok", "service": st})
}
