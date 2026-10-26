package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"finduai/domain/asset"
)

// AssetPatch is a partial update for the shared assets table.
type AssetPatch struct {
	Host              *string  `json:"host"`
	IP                *string  `json:"ip"`
	Port              *int     `json:"port"`
	Domain            *string  `json:"domain"`
	Protocol          *string  `json:"protocol"`
	Title             *string  `json:"title"`
	Server            *string  `json:"server"`
	Country           *string  `json:"country"`
	Province          *string  `json:"province"`
	City              *string  `json:"city"`
	ResponsiblePerson *string  `json:"responsible_person"`
	Department        *string  `json:"department"`
	BusinessSystem    *string  `json:"business_system"`
	Environment       *string  `json:"environment"`
	Criticality       *string  `json:"criticality"`
	Source            *string  `json:"source"`
	SourceQuery       *string  `json:"source_query"`
	Status            *string  `json:"status"`
	RiskLevel         *string  `json:"risk_level"`
	RiskScore         *int     `json:"risk_score"`
	ProjectID         *string  `json:"project_id"`
	Tags              []string `json:"tags"`
	AddTags           []string `json:"add_tags"`
	RemoveTags        []string `json:"remove_tags"`
}

func (s *AssetStore) GetByID(id string) (*asset.Asset, error) {
	rows, err := s.db.Query(asset.AssetSelectCols+` WHERE id=$1`, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := asset.ScanAssets(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, sql.ErrNoRows
	}
	return list[0], nil
}

func (s *AssetStore) getAssetTags(id string) []string {
	var raw string
	if err := s.db.QueryRow(`SELECT COALESCE(tags_json,'[]') FROM assets WHERE id=$1`, id).Scan(&raw); err != nil {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}

func (s *AssetStore) UpdateByID(id string, patch AssetPatch) error {
	if len(patch.AddTags) > 0 || len(patch.RemoveTags) > 0 {
		tags := map[string]bool{}
		for _, tag := range s.getAssetTags(id) {
			tags[tag] = true
		}
		for _, tag := range patch.AddTags {
			if strings.TrimSpace(tag) != "" {
				tags[strings.TrimSpace(tag)] = true
			}
		}
		for _, tag := range patch.RemoveTags {
			delete(tags, strings.TrimSpace(tag))
		}
		merged := make([]string, 0, len(tags))
		for tag := range tags {
			merged = append(merged, tag)
		}
		patch.Tags = merged
	}
	sets := []string{}
	args := []any{strings.TrimSpace(id)}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if patch.Host != nil {
		add("host", *patch.Host)
	}
	if patch.IP != nil {
		add("ip", *patch.IP)
	}
	if patch.Port != nil {
		add("port", *patch.Port)
	}
	if patch.Domain != nil {
		add("domain", *patch.Domain)
	}
	if patch.Protocol != nil {
		add("protocol", *patch.Protocol)
	}
	if patch.Title != nil {
		add("title", *patch.Title)
	}
	if patch.Server != nil {
		add("server", *patch.Server)
	}
	if patch.Country != nil {
		add("country", *patch.Country)
	}
	if patch.Province != nil {
		add("province", *patch.Province)
	}
	if patch.City != nil {
		add("city", *patch.City)
	}
	if patch.ResponsiblePerson != nil {
		add("responsible_person", *patch.ResponsiblePerson)
	}
	if patch.Department != nil {
		add("department", *patch.Department)
	}
	if patch.BusinessSystem != nil {
		add("business_system", *patch.BusinessSystem)
	}
	if patch.Environment != nil {
		add("environment", *patch.Environment)
	}
	if patch.Criticality != nil {
		add("criticality", *patch.Criticality)
	}
	if patch.Source != nil {
		add("source", *patch.Source)
	}
	if patch.SourceQuery != nil {
		add("source_query", *patch.SourceQuery)
	}
	if patch.Status != nil {
		add("status", *patch.Status)
	}
	if patch.RiskLevel != nil {
		add("risk_level", *patch.RiskLevel)
	}
	if patch.RiskScore != nil {
		add("risk_score", *patch.RiskScore)
	}
	if patch.ProjectID != nil {
		if strings.TrimSpace(*patch.ProjectID) == "" {
			add("project_id", nil)
		} else {
			add("project_id", strings.TrimSpace(*patch.ProjectID))
		}
	}
	if patch.Tags != nil {
		b, _ := json.Marshal(patch.Tags)
		add("tags_json", string(b))
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at=now()")
	res, err := s.db.Exec(`UPDATE assets SET `+strings.Join(sets, ", ")+` WHERE id=$1`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// BulkUpdate applies the same patch to a set of assets. Tag add/remove is merged
// per asset because tags_json is a JSON array.
func (s *AssetStore) BulkUpdate(ids []string, patch AssetPatch) (int64, error) {
	var updated int64
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := s.GetByID(id); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return updated, err
		}
		one := patch
		if len(patch.AddTags) > 0 || len(patch.RemoveTags) > 0 {
			tags := map[string]bool{}
			for _, tag := range s.getAssetTags(id) {
				tags[tag] = true
			}
			for _, tag := range patch.AddTags {
				if strings.TrimSpace(tag) != "" {
					tags[strings.TrimSpace(tag)] = true
				}
			}
			for _, tag := range patch.RemoveTags {
				delete(tags, strings.TrimSpace(tag))
			}
			merged := make([]string, 0, len(tags))
			for tag := range tags {
				merged = append(merged, tag)
			}
			one.Tags = merged
		}
		if err := s.UpdateByID(id, one); err == nil {
			updated++
		} else if err != sql.ErrNoRows {
			return updated, err
		}
	}
	return updated, nil
}
