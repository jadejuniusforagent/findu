package database

import (
	"database/sql"
	"time"

	"go.uber.org/zap"
)

// SkillStats Skills统计信息
type SkillStats struct {
	SkillName    string
	TotalCalls   int
	SuccessCalls int
	FailedCalls  int
	LastCallTime *time.Time
	// MissCalls / LastMissTime：缺口遥测（P2）。
	// 模型调用 skill 工具但该技能不存在（或 SKILL.md 解析失败被跳过）时记一次 miss，
	// 用来产出「应该有但还没有」的技能清单，而不是算成一次失败调用。
	MissCalls    int
	LastMissTime *time.Time
}

// SaveSkillStats 保存Skills统计信息
func (db *DB) SaveSkillStats(skillName string, stats *SkillStats) error {
	var lastCallTime sql.NullTime
	if stats.LastCallTime != nil {
		lastCallTime = sql.NullTime{Time: *stats.LastCallTime, Valid: true}
	}
	var lastMissTime sql.NullTime
	if stats.LastMissTime != nil {
		lastMissTime = sql.NullTime{Time: *stats.LastMissTime, Valid: true}
	}

	query := `
		INSERT OR REPLACE INTO skill_stats 
		(skill_name, total_calls, success_calls, failed_calls, last_call_time, miss_calls, last_miss_time, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := db.Exec(query,
		skillName,
		stats.TotalCalls,
		stats.SuccessCalls,
		stats.FailedCalls,
		lastCallTime,
		stats.MissCalls,
		lastMissTime,
		time.Now(),
	)

	if err != nil {
		db.logger.Error("保存Skills统计信息失败", zap.Error(err), zap.String("skillName", skillName))
		return err
	}

	return nil
}

// LoadSkillStats 加载所有Skills统计信息
func (db *DB) LoadSkillStats() (map[string]*SkillStats, error) {
	query := `
		SELECT skill_name, total_calls, success_calls, failed_calls, last_call_time, miss_calls, last_miss_time
		FROM skill_stats
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]*SkillStats)
	for rows.Next() {
		var stat SkillStats
		var lastCallTime sql.NullTime
		var lastMissTime sql.NullTime

		err := rows.Scan(
			&stat.SkillName,
			&stat.TotalCalls,
			&stat.SuccessCalls,
			&stat.FailedCalls,
			&lastCallTime,
			&stat.MissCalls,
			&lastMissTime,
		)
		if err != nil {
			db.logger.Warn("加载Skills统计信息失败", zap.Error(err))
			continue
		}
		if lastCallTime.Valid {
			stat.LastCallTime = &lastCallTime.Time
		}
		if lastMissTime.Valid {
			stat.LastMissTime = &lastMissTime.Time
		}

		stats[stat.SkillName] = &stat
	}

	return stats, nil
}

// LoadSkillMisses 加载「缺口榜」：只反查过、但当前不存在的技能（miss_calls > 0），按缺口次数降序。
func (db *DB) LoadSkillMisses() ([]*SkillStats, error) {
	query := `
		SELECT skill_name, total_calls, success_calls, failed_calls, last_call_time, miss_calls, last_miss_time
		FROM skill_stats
		WHERE miss_calls > 0
		ORDER BY miss_calls DESC, last_miss_time DESC
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SkillStats
	for rows.Next() {
		var stat SkillStats
		var lastCallTime sql.NullTime
		var lastMissTime sql.NullTime
		if err := rows.Scan(
			&stat.SkillName,
			&stat.TotalCalls,
			&stat.SuccessCalls,
			&stat.FailedCalls,
			&lastCallTime,
			&stat.MissCalls,
			&lastMissTime,
		); err != nil {
			db.logger.Warn("加载Skills缺口榜失败", zap.Error(err))
			continue
		}
		if lastCallTime.Valid {
			stat.LastCallTime = &lastCallTime.Time
		}
		if lastMissTime.Valid {
			stat.LastMissTime = &lastMissTime.Time
		}
		out = append(out, &stat)
	}

	return out, nil
}

// UpdateSkillMiss 记录一次「技能缺失」（累加模式）；技能存在时不回退已有 miss 计数。
func (db *DB) UpdateSkillMiss(skillName string, at time.Time) error {
	query := `
		INSERT INTO skill_stats (skill_name, total_calls, success_calls, failed_calls, miss_calls, last_miss_time, updated_at)
		VALUES (?, 0, 0, 0, 1, ?, ?)
		ON CONFLICT(skill_name) DO UPDATE SET
			miss_calls = miss_calls + 1,
			last_miss_time = ?,
			updated_at = ?
	`

	_, err := db.Exec(query, skillName, at, time.Now(), at, time.Now())
	if err != nil {
		db.logger.Error("记录Skills缺口失败", zap.Error(err), zap.String("skillName", skillName))
		return err
	}

	return nil
}

// UpdateSkillStats 更新Skills统计信息（累加模式）
func (db *DB) UpdateSkillStats(skillName string, totalCalls, successCalls, failedCalls int, lastCallTime *time.Time) error {
	var lastCallTimeSQL sql.NullTime
	if lastCallTime != nil {
		lastCallTimeSQL = sql.NullTime{Time: *lastCallTime, Valid: true}
	}

	query := `
		INSERT INTO skill_stats (skill_name, total_calls, success_calls, failed_calls, last_call_time, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(skill_name) DO UPDATE SET
			total_calls = total_calls + ?,
			success_calls = success_calls + ?,
			failed_calls = failed_calls + ?,
			last_call_time = COALESCE(?, last_call_time),
			updated_at = ?
	`

	_, err := db.Exec(query,
		skillName, totalCalls, successCalls, failedCalls, lastCallTimeSQL, time.Now(),
		totalCalls, successCalls, failedCalls, lastCallTimeSQL, time.Now(),
	)

	if err != nil {
		db.logger.Error("更新Skills统计信息失败", zap.Error(err), zap.String("skillName", skillName))
		return err
	}

	return nil
}

// ClearSkillMisses 清空缺口榜：纯缺口行删除，混合行只清零 miss 计数。
func (db *DB) ClearSkillMisses() error {
	if _, err := db.Exec(`DELETE FROM skill_stats WHERE miss_calls > 0 AND total_calls = 0`); err != nil {
		db.logger.Error("清空Skills缺口榜失败", zap.Error(err))
		return err
	}
	if _, err := db.Exec(`UPDATE skill_stats SET miss_calls = 0, last_miss_time = NULL WHERE miss_calls > 0`); err != nil {
		db.logger.Error("清零Skills缺口计数失败", zap.Error(err))
		return err
	}
	db.logger.Info("已清空Skills缺口榜")
	return nil
}

// ClearSkillStats 清空所有Skills统计信息
func (db *DB) ClearSkillStats() error {
	query := `DELETE FROM skill_stats`
	_, err := db.Exec(query)
	if err != nil {
		db.logger.Error("清空Skills统计信息失败", zap.Error(err))
		return err
	}
	db.logger.Info("已清空所有Skills统计信息")
	return nil
}

// ClearSkillStatsByName 清空指定skill的统计信息
func (db *DB) ClearSkillStatsByName(skillName string) error {
	query := `DELETE FROM skill_stats WHERE skill_name = ?`
	_, err := db.Exec(query, skillName)
	if err != nil {
		db.logger.Error("清空指定skill统计信息失败", zap.Error(err), zap.String("skillName", skillName))
		return err
	}
	db.logger.Info("已清空指定skill统计信息", zap.String("skillName", skillName))
	return nil
}
