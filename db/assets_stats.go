package db

import "time"

// AssetStats returns the dashboard-style asset overview used by /api/assets/stats.
// The shared assets table is wide (findu-shaped); legacy NULL columns are coalesced.
func (s *AssetStore) Stats(days int) (map[string]any, error) {
	if days != 7 && days != 30 && days != 90 {
		days = 30
	}
	stats := map[string]any{}
	var total, ips, domains, ports, recent int
	if err := s.db.QueryRow(`SELECT count(*),
		count(DISTINCT NULLIF(ip,'')), count(DISTINCT NULLIF(domain,'')),
		count(DISTINCT CASE WHEN port>0 THEN port::text END),
		COALESCE(sum(CASE WHEN last_seen_at >= now()-interval '7 days' THEN 1 ELSE 0 END),0)
		FROM assets`).Scan(&total, &ips, &domains, &ports, &recent); err != nil {
		return nil, err
	}
	stats["total"], stats["ips"], stats["domains"], stats["ports"], stats["recent"] = total, ips, domains, ports, recent
	stats["period_days"] = days

	protocols := []map[string]any{}
	rows, err := s.db.Query(`SELECT COALESCE(NULLIF(protocol,''),'unknown'), count(*)
		FROM assets GROUP BY 1 ORDER BY count(*) DESC LIMIT 8`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			rows.Close()
			return nil, err
		}
		protocols = append(protocols, map[string]any{"name": name, "count": count})
	}
	rows.Close()
	stats["protocols"] = protocols

	coverage := map[string]any{}
	var scanned, scanned7, scanned30, neverScanned, stale, active int
	if err := s.db.QueryRow(`SELECT
		COALESCE(sum(CASE WHEN last_scan_at IS NOT NULL THEN 1 ELSE 0 END),0),
		COALESCE(sum(CASE WHEN last_scan_at >= now()-interval '7 days' THEN 1 ELSE 0 END),0),
		COALESCE(sum(CASE WHEN last_scan_at >= now()-interval '30 days' THEN 1 ELSE 0 END),0),
		COALESCE(sum(CASE WHEN last_scan_at IS NULL THEN 1 ELSE 0 END),0),
		COALESCE(sum(CASE WHEN last_scan_at IS NOT NULL AND last_scan_at < now()-interval '30 days' THEN 1 ELSE 0 END),0),
		COALESCE(sum(CASE WHEN status='active' THEN 1 ELSE 0 END),0)
		FROM assets`).Scan(&scanned, &scanned7, &scanned30, &neverScanned, &stale, &active); err != nil {
		return nil, err
	}
	coverage["scanned"], coverage["scanned_7d"], coverage["scanned_30d"] = scanned, scanned7, scanned30
	coverage["never_scanned"], coverage["stale"], coverage["active"] = neverScanned, stale, active
	if total > 0 {
		coverage["rate"] = int(float64(scanned) / float64(total) * 100)
		coverage["recent_rate"] = int(float64(scanned30) / float64(total) * 100)
	} else {
		coverage["rate"], coverage["recent_rate"] = 0, 0
	}
	stats["coverage"] = coverage

	assetDaily := map[string]map[string]int{}
	rows, err = s.db.Query(`SELECT to_char(date(first_seen_at), 'YYYY-MM-DD'), count(*)
		FROM assets WHERE first_seen_at >= now()-($1::int - 1) * interval '1 day'
		GROUP BY 1`, days)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day string
		var added int
		if err := rows.Scan(&day, &added); err != nil {
			rows.Close()
			return nil, err
		}
		assetDaily[day] = map[string]int{"added": added, "inactive": 0}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT to_char(date(updated_at), 'YYYY-MM-DD'), count(*)
		FROM assets WHERE status='inactive' AND updated_at >= now()-($1::int - 1) * interval '1 day'
		GROUP BY 1`, days)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day string
		var inactive int
		if err := rows.Scan(&day, &inactive); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := assetDaily[day]; !ok {
			assetDaily[day] = map[string]int{"added": 0, "inactive": 0}
		}
		assetDaily[day]["inactive"] = inactive
	}
	rows.Close()

	riskDaily := map[string]map[string]int{}
	rows, err = s.db.Query(`SELECT to_char(date(created_at), 'YYYY-MM-DD'), count(*),
		COALESCE(sum(CASE WHEN lower(severity) IN ('critical','high') THEN 1 ELSE 0 END),0)
		FROM vulnerabilities WHERE created_at >= now()-($1::int - 1) * interval '1 day'
		GROUP BY 1`, days)
	if err == nil {
		for rows.Next() {
			var day string
			var discovered, high int
			if err := rows.Scan(&day, &discovered, &high); err != nil {
				rows.Close()
				return nil, err
			}
			riskDaily[day] = map[string]int{"discovered": discovered, "high_risk": high}
		}
		rows.Close()
	}

	assetTrend := make([]map[string]any, 0, days)
	riskTrend := make([]map[string]any, 0, days)
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i).Format("2006-01-02")
		ap := map[string]any{"date": day, "added": 0, "inactive": 0}
		if v, ok := assetDaily[day]; ok {
			ap["added"], ap["inactive"] = v["added"], v["inactive"]
		}
		assetTrend = append(assetTrend, ap)
		rp := map[string]any{"date": day, "discovered": 0, "high_risk": 0}
		if v, ok := riskDaily[day]; ok {
			rp["discovered"], rp["high_risk"] = v["discovered"], v["high_risk"]
		}
		riskTrend = append(riskTrend, rp)
	}
	stats["asset_trend"], stats["risk_trend"] = assetTrend, riskTrend
	return stats, nil
}
