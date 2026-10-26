package server

// Step 6d Phase B2：roles 原生 handler（只读 + 写收口 409）。
// 行为与 findu internal/handler/role.go 对齐：读真源 agents(kind='role')；
// create/update/delete 一律 409 指向控制台 /system/agents（角色已统一管理）。

import (
	"net/http"
	"strings"

	"finduai/db"
)

// listRolesNative serves GET /api/roles。
func (s *Server) listRolesNative(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	rs := s.m.pg.Roles()
	roles, err := rs.ListRolesNative()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if roles == nil {
		roles = []*db.RoleDTO{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles, "source": "db"})
}

// getRoleNative serves GET /api/roles/{name}。
func (s *Server) getRoleNative(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		writeErr(w, http.StatusBadRequest, "角色名称不能为空")
		return
	}
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	role, ok, err := s.m.pg.Roles().GetRoleNative(name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "角色不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": role, "source": "db"})
}

// rolesWriteLocked 返回 findu 同款 409（写入口收口到控制台）。
func rolesWriteLocked(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":      "角色已统一到控制台管理，此处只读",
		"manage_url": "/system/agents",
	})
}

// createRoleNative serves POST /api/roles（409 收口）。
func (s *Server) createRoleNative(w http.ResponseWriter, r *http.Request) {
	rolesWriteLocked(w)
}

// updateRoleNative serves PUT /api/roles/{name}（409 收口）。
func (s *Server) updateRoleNative(w http.ResponseWriter, r *http.Request) {
	rolesWriteLocked(w)
}

// deleteRoleNative serves DELETE /api/roles/{name}（409 收口）。
func (s *Server) deleteRoleNative(w http.ResponseWriter, r *http.Request) {
	rolesWriteLocked(w)
}
