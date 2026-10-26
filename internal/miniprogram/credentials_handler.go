package miniprogram

// credentials_handler.go 小程序凭证(AppID/AppSecret)池 HTTP 接口。
// 读 = miniprogram:read,增删/验证 = miniprogram:execute(RBAC 前缀映射复用 /miniprogram)。

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"finduai/internal/audit"
	"finduai/internal/netproxy"
	"finduai/internal/security"

	"github.com/gin-gonic/gin"
)

// SetAudit 注入审计服务（setupMiniprogramAnalysis 装配时调用）。
func (h *Handler) SetAudit(s *audit.Service) { h.audit = s }

// clientFor 按出口模式构造验证客户端:?proxy= 请求级覆盖优先,其次凭证保存值,缺省 proxy。
func (h *Handler) clientFor(override, stored string) *http.Client {
	mode := netproxy.EffectiveEgressMode(override, stored)
	if mode == "proxy" && h.credClient != nil {
		return h.credClient
	}
	return netproxy.VerificationClient(mode, 20*time.Second)
}

// proxyModeOverride 请求级 ?proxy=proxy|direct 覆盖。
func proxyModeOverride(c *gin.Context) string {
	return netproxy.NormalizeEgressMode(c.Query("proxy"))
}

func (h *Handler) registerCredentialRoutes(rg gin.IRouter) {
	g := rg.Group("/miniprogram")
	g.GET("/credentials", security.RequirePermission("miniprogram:read"), h.ListCredentials)
	g.POST("/credentials", security.RequirePermission("miniprogram:execute"), h.AddCredential)
	g.DELETE("/credentials/:id", security.RequirePermission("miniprogram:execute"), h.DeleteCredential)
	g.POST("/credentials/:id/verify", security.RequirePermission("miniprogram:execute"), h.VerifyCredential)
	// 免入库快速验证一对 AppID/AppSecret（扫描发现手工核验用），结论只透传不存储
	g.POST("/credentials/verify-adhoc", security.RequirePermission("miniprogram:execute"), h.VerifyCredentialAdhoc)
}

// ListCredentials GET /api/miniprogram/credentials —— ?project_id= 按项目过滤（缺省全部）。
func (h *Handler) ListCredentials(c *gin.Context) {
	items, err := h.svc.ListCredentials(c.Request.Context(), c.Query("project_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

// AddCredential POST /api/miniprogram/credentials {name, app_id, secret, project_id?, auto_verify}
func (h *Handler) AddCredential(c *gin.Context) {
	var req struct {
		Name       string `json:"name"`
		AppID      string `json:"app_id"`
		Secret     string `json:"secret"`
		ProxyMode  string `json:"proxy_mode"`
		ProjectID  string `json:"project_id"`
		AutoVerify bool   `json:"auto_verify"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cred, err := h.svc.AddCredential(c.Request.Context(), req.Name, req.AppID, req.Secret, req.ProxyMode, req.ProjectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.audit != nil {
		h.audit.RecordOK(c, "miniprogram_credential", "add", "录入小程序凭证 "+cred.AppID, "miniprogram_credential", cred.ID, nil)
	}
	if req.AutoVerify {
		cred, _, _ = h.svc.VerifyCredential(c.Request.Context(), h.clientFor("", cred.ProxyMode), cred.ID)
	}
	c.JSON(http.StatusOK, gin.H{"credential": cred})
}

// DeleteCredential DELETE /api/miniprogram/credentials/:id
func (h *Handler) DeleteCredential(c *gin.Context) {
	if err := h.svc.DeleteCredential(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, errCredentialNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "小程序凭证不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.audit != nil {
		h.audit.RecordOK(c, "miniprogram_credential", "delete", "删除小程序凭证", "miniprogram_credential", c.Param("id"), nil)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// VerifyCredential POST /api/miniprogram/credentials/:id/verify —— ?proxy=proxy|direct 可单次覆盖。
func (h *Handler) VerifyCredential(c *gin.Context) {
	cred0, gerr := h.svc.GetCredential(c.Request.Context(), c.Param("id"))
	if gerr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "小程序凭证不存在"})
		return
	}
	cred, result, err := h.svc.VerifyCredential(c.Request.Context(), h.clientFor(proxyModeOverride(c), cred0.ProxyMode), c.Param("id"))
	if errors.Is(err, errCredentialNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "小程序凭证不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.audit != nil {
		h.audit.RecordOK(c, "miniprogram_credential", "verify", "验证小程序凭证 "+cred.AppID+"（结论 "+cred.Status+"）", "miniprogram_credential", cred.ID, nil)
	}
	c.JSON(http.StatusOK, gin.H{"credential": cred, "result": result})
}

// VerifyCredentialAdhoc POST /api/miniprogram/credentials/verify-adhoc {app_id, secret}
func (h *Handler) VerifyCredentialAdhoc(c *gin.Context) {
	var req struct {
		AppID  string `json:"app_id"`
		Secret string `json:"secret"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.AppID = strings.TrimSpace(req.AppID)
	if req.AppID == "" || req.Secret == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_id 与 secret 必填"})
		return
	}
	result := VerifyAppSecret(c.Request.Context(), h.clientFor(proxyModeOverride(c), ""), req.AppID, req.Secret)
	if h.audit != nil {
		h.audit.RecordOK(c, "miniprogram_credential", "verify_adhoc", "快速验证小程序凭证（免入库，结论 "+result.Status+"）", "miniprogram_credential", "", nil)
	}
	c.JSON(http.StatusOK, gin.H{"result": result})
}
