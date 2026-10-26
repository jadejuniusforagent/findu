package handler

import (
	"net/http"
	"strings"
	"time"

	"finduai/internal/audit"
	"finduai/internal/config"
	"finduai/internal/security"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AuthHandler handles authentication-related endpoints.
type AuthHandler struct {
	manager    *security.AuthManager
	config     *config.Config
	configPath string
	logger     *zap.Logger
	audit      *audit.Service
}

// SetAudit wires platform audit logging.
func (h *AuthHandler) SetAudit(s *audit.Service) {
	h.audit = s
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(manager *security.AuthManager, cfg *config.Config, configPath string, logger *zap.Logger) *AuthHandler {
	return &AuthHandler{
		manager:    manager,
		config:     cfg,
		configPath: configPath,
		logger:     logger,
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password" binding:"required"`
}

type changePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// Login verifies password and returns a session token.
func (h *AuthHandler) Login(c *gin.Context) {
	// 账号已统一到控制台（findu 的 users + findu_token cookie）；findu 不再接受本地密码登录。
	c.JSON(http.StatusGone, gin.H{"error": "账号已统一到控制台，请从控制台登录", "login_url": "/login"})
	return
}

// Logout revokes the current session token.
func (h *AuthHandler) Logout(c *gin.Context) {
	token := c.GetString(security.ContextAuthTokenKey)
	if token == "" {
		authHeader := c.GetHeader("Authorization")
		if len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
			token = strings.TrimSpace(authHeader[7:])
		} else {
			token = strings.TrimSpace(authHeader)
		}
	}

	h.manager.RevokeToken(token)
	if h.audit != nil {
		h.audit.Record(c, audit.Entry{
			Category: "auth",
			Action:   "logout",
			Result:   "success",
			Message:  "退出登录",
		})
	}
	c.JSON(http.StatusOK, gin.H{"message": "已退出登录"})
}

// ChangePassword updates the login password.
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{"error": "密码统一在控制台管理", "manage_url": "/system/settings"})
	return
}

// Validate returns the current session status.
func (h *AuthHandler) Validate(c *gin.Context) {
	token := c.GetString(security.ContextAuthTokenKey)
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "会话无效"})
		return
	}

	session, ok := h.manager.ValidateToken(token)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "会话已过期"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":      session.Token,
		"expires_at": session.ExpiresAt.UTC().Format(time.RFC3339),
		"user": gin.H{
			"id":           session.UserID,
			"username":     session.Username,
			"display_name": session.DisplayName,
		},
		"roles":             session.Roles,
		"permissions":       permissionKeys(session.Permissions),
		"permission_scopes": session.PermissionScopes,
		"scope":             session.Scope,
	})
}

func permissionKeys(perms map[string]bool) []string {
	keys := make([]string, 0, len(perms))
	for key, ok := range perms {
		if ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// Exchange 用控制台的 findu_token cookie 换取 findu 会话。
// 账号统一到 findu 之后，这是进入 /findu/ 的唯一登录路径（前端启动时自动调用）。
func (h *AuthHandler) Exchange(c *gin.Context) {
	session, ok := security.SessionFromFinduCookie(c, h.manager)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录或控制台会话已过期", "login_url": "/login"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":             session.Token,
		"expires_at":        session.ExpiresAt.UTC().Format(time.RFC3339),
		"user":              gin.H{"id": session.UserID, "username": session.Username, "display_name": session.DisplayName},
		"roles":             session.Roles,
		"permissions":       permissionKeys(session.Permissions),
		"permission_scopes": session.PermissionScopes,
		"scope":             session.Scope,
	})
}
