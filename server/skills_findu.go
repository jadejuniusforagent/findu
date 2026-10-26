package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	skill "finduai/skills"
)

// finduSkillRow 前端 /system/skills 需要的 findu 旧形状技能行（数据真源为本地
// skills 目录；version/progressive/tags/triggers 宿主模型无对应，填空/空数组）。
type finduSkillRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	DirName     string   `json:"dir_name,omitempty"`
	Description string   `json:"description,omitempty"`
	Path        string   `json:"path,omitempty"`
	FileCount   int      `json:"file_count,omitempty"`
	FileSize    int64    `json:"file_size,omitempty"`
	ScriptCount int      `json:"script_count,omitempty"`
	ModTime     string   `json:"mod_time,omitempty"`
	Version     string   `json:"version,omitempty"`
	Progressive bool     `json:"progressive"`
	Tags        []string `json:"tags,omitempty"`
	Triggers    []string `json:"triggers,omitempty"`
}

// listSkillsFinduNative serves GET /api/skills/findu：返回 findu 旧形状技能列表，
// 形状与 /findu/api/skills 对齐 {skills,total,limit,offset}。
func (s *Server) listSkillsFinduNative(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v >= 1 && v <= 2000 {
		limit = v
	}
	_ = os.MkdirAll(s.skillDir, 0o755)
	metaByDir := map[string]skill.Skill{}
	if reg, err := skill.LoadDir(s.skillDir); err == nil && reg != nil {
		for _, sk := range reg.List() {
			if sk.Dir != "" {
				metaByDir[filepath.Base(sk.Dir)] = sk
			}
		}
	}
	entries, err := os.ReadDir(s.skillDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := []finduSkillRow{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirName := e.Name()
		dir := filepath.Join(s.skillDir, dirName)
		files, _ := walkSkillFiles(dir)
		var totalSize int64
		scriptCount := 0
		var latest time.Time
		for _, f := range files {
			if st, err := os.Stat(f); err == nil {
				totalSize += st.Size()
				if st.ModTime().After(latest) {
					latest = st.ModTime()
				}
			}
			switch strings.ToLower(filepath.Ext(f)) {
			case ".sh", ".py", ".js", ".ts", ".go", ".pl", ".rb", ".lua":
				scriptCount++
			}
		}
		meta := metaByDir[dirName]
		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = dirName
		}
		var modTime string
		if !latest.IsZero() {
			modTime = latest.Format(time.RFC3339)
		}
		rows = append(rows, finduSkillRow{
			ID:          dirName,
			Name:        name,
			DirName:     dirName,
			Description: meta.Description,
			Path:        dir,
			FileCount:   len(files),
			FileSize:    totalSize,
			ScriptCount: scriptCount,
			ModTime:     modTime,
			Tags:        []string{},
			Triggers:    []string{},
		})
		if len(rows) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skills": rows, "total": len(rows), "limit": limit, "offset": 0,
	})
}