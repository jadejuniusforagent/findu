package finding

// 流量证据管线（finding_traffic_bindings / traffic_evidence_snapshots /
// findings.evidence_version）的存储层。
//
// 迁移边界（Step 3 批 3 第七刀）：
//   - WithEvidenceTx（advisory 全局锁 + BeginTx）落本包，db 侧保留同名方法转发，
//     evidence 包与 agent 调用点零改动；
//   - LockTaskEvidenceTx / LockFindingEvidenceTx / RecordFindingTx /
//     EditFindingTraffic / SetFindingReportVersionByNodeID 留 db 装配层——
//     取证锁协议跨 task_archives/tasks 状态机（规范三-2）。

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"finduai/core/store"
)

var (
	ErrEvidenceConflict = errors.New("流量证据已变更，请刷新后重试")
	ErrFindingNotFound  = errors.New("漏洞不存在")
	ErrEvidenceNotFound = errors.New("流量证据不存在")
)

// This lock covers the evidence filesystem as well as its SQL references. All
// processes sharing the database use it, including readers, exports and GC.
//
// 导出为 FindingEvidenceLockKey：跨进程协议所需的常量值必须稳定可见（测试、
// 诊断脚本能对上号），装配层经 db 别名取用。
const FindingEvidenceLockKey int64 = 7337741004

const findingEvidenceLockKey = FindingEvidenceLockKey

// WithEvidenceTx executes fn within a transaction that holds the evidence
// advisory lock (pg_advisory_xact_lock), serializing writers of the evidence
// filesystem and its SQL references across processes.
func WithEvidenceTx(ctx context.Context, b store.ContextBeginner, fn func(*sql.Tx) error) error {
	tx, err := b.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, findingEvidenceLockKey); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type TrafficRef struct {
	TrafficID string `json:"traffic_id"`
	Role      string `json:"role"`
	Note      string `json:"note"`
}

func NormalizeTrafficRefs(refs []TrafficRef) ([]TrafficRef, error) {
	out := make([]TrafficRef, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		ref.TrafficID = strings.TrimSpace(ref.TrafficID)
		if ref.TrafficID == "" {
			return nil, errors.New("traffic_id 不能为空")
		}
		if ref.Role == "" {
			ref.Role = "supporting"
		}
		if !ValidTrafficRole(ref.Role) {
			return nil, fmt.Errorf("无效的流量用途 %q", ref.Role)
		}
		if !seen[ref.TrafficID] {
			out = append(out, ref)
			seen[ref.TrafficID] = true
		}
	}
	return out, nil
}

func ValidTrafficRole(role string) bool {
	return role == "baseline" || role == "proof" || role == "verification" || role == "supporting"
}

type TrafficEvidenceSnapshot struct {
	ID              string    `json:"id"`
	SourceTrafficID string    `json:"source_traffic_id"`
	CapturedAt      int64     `json:"captured_at"`
	URL             string    `json:"url"`
	Method          string    `json:"method"`
	Status          int       `json:"status"`
	ContentType     string    `json:"content_type"`
	ReqHead         string    `json:"req_head,omitempty"`
	RespHead        string    `json:"resp_head,omitempty"`
	ReqHash         string    `json:"req_hash"`
	RespHash        string    `json:"resp_hash"`
	ReqLen          int64     `json:"req_len"`
	RespLen         int64     `json:"resp_len"`
	CreatedAt       time.Time `json:"created_at"`
}

type FindingTrafficBinding struct {
	ID         int64                   `json:"id,string"`
	FindingID  int64                   `json:"finding_id,string"`
	SnapshotID string                  `json:"snapshot_id"`
	Role       string                  `json:"role"`
	Note       string                  `json:"note"`
	Position   int                     `json:"position"`
	CreatedAt  time.Time               `json:"created_at"`
	Snapshot   TrafficEvidenceSnapshot `json:"snapshot"`
}

type FindingTraffic struct {
	FindingID     int64                   `json:"finding_id,string"`
	Version       int64                   `json:"version"`
	ReportVersion int64                   `json:"report_version"`
	Bindings      []FindingTrafficBinding `json:"bindings"`
}

type PreparedTrafficEvidence struct {
	Ref      TrafficRef
	Snapshot TrafficEvidenceSnapshot
}

// Normalize makes the snapshot's text columns safe for PostgreSQL. URL and the
// head blocks come straight off the wire, so a target answering with a non-UTF-8
// header (a GBK `Content-Disposition: filename=…`, a NUL byte) would otherwise
// abort the INSERT and roll back the whole finding — losing a confirmed finding
// over a malformed response header. Applied before hashing so the ID always
// matches the bytes that actually land in the table.
func (s TrafficEvidenceSnapshot) Normalize() TrafficEvidenceSnapshot {
	s.SourceTrafficID = store.UTF8Clean(s.SourceTrafficID)
	s.URL = store.UTF8Clean(s.URL)
	s.Method = store.UTF8Clean(s.Method)
	s.ContentType = store.UTF8Clean(s.ContentType)
	s.ReqHead = store.UTF8Clean(s.ReqHead)
	s.RespHead = store.UTF8Clean(s.RespHead)
	return s
}

func TrafficSnapshotID(snapshot TrafficEvidenceSnapshot) string {
	snapshot = snapshot.Normalize()
	snapshot.ID = ""
	snapshot.CreatedAt = time.Time{}
	raw, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func InsertEvidenceSnapshotTx(tx *sql.Tx, s TrafficEvidenceSnapshot) error {
	if s.ID != TrafficSnapshotID(s) {
		return errors.New("证据快照元数据哈希不匹配")
	}
	// The ID was computed over the normalized form; store those same bytes.
	id := s.ID
	s = s.Normalize()
	s.ID = id
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	_, err := tx.Exec(`INSERT INTO traffic_evidence_snapshots
(id,source_traffic_id,captured_at,url,method,status,content_type,req_head,resp_head,req_hash,resp_hash,req_len,resp_len,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(id) DO NOTHING`,
		s.ID, s.SourceTrafficID, s.CapturedAt, s.URL, s.Method, s.Status, s.ContentType, s.ReqHead, s.RespHead, s.ReqHash, s.RespHash, s.ReqLen, s.RespLen, s.CreatedAt)
	return err
}

func AddFindingTrafficTx(tx *sql.Tx, findingID int64, prepared []PreparedTrafficEvidence) error {
	var position int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(position)+1,0) FROM finding_traffic_bindings WHERE finding_id=$1`, findingID).Scan(&position); err != nil {
		return err
	}
	changed := false
	for _, item := range prepared {
		if err := InsertEvidenceSnapshotTx(tx, item.Snapshot); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE traffic_evidence_snapshots SET unreferenced_at=NULL WHERE id=$1`, item.Snapshot.ID); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO finding_traffic_bindings(finding_id,snapshot_id,role,note,position)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(finding_id,snapshot_id) DO NOTHING`, findingID, item.Snapshot.ID, item.Ref.Role, item.Ref.Note, position)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			changed = true
			position++
		}
	}
	if changed {
		return bumpEvidenceVersionTx(tx, findingID)
	}
	return nil
}

func bumpEvidenceVersionTx(tx *sql.Tx, findingID int64) error {
	_, err := tx.Exec(`UPDATE findings SET evidence_version=evidence_version+1 WHERE id=$1`, findingID)
	return err
}

// BumpEvidenceVersionTx 导出版本自增，供装配层（EditFindingTraffic）在取证锁下
// 复用同一写入。
func BumpEvidenceVersionTx(tx *sql.Tx, findingID int64) error {
	return bumpEvidenceVersionTx(tx, findingID)
}

func FindingTrafficTx(tx *sql.Tx, findingID int64) (*FindingTraffic, error) {
	out := &FindingTraffic{FindingID: findingID, Bindings: []FindingTrafficBinding{}}
	if err := tx.QueryRow(`SELECT evidence_version,report_evidence_version FROM findings WHERE id=$1`, findingID).Scan(&out.Version, &out.ReportVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFindingNotFound
		}
		return nil, err
	}
	rows, err := tx.Query(`SELECT b.id,b.finding_id,b.snapshot_id,b.role,b.note,b.position,b.created_at,to_jsonb(s)
FROM finding_traffic_bindings b JOIN traffic_evidence_snapshots s ON s.id=b.snapshot_id WHERE b.finding_id=$1 ORDER BY b.position,b.id`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b FindingTrafficBinding
		var raw []byte
		if err := rows.Scan(&b.ID, &b.FindingID, &b.SnapshotID, &b.Role, &b.Note, &b.Position, &b.CreatedAt, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &b.Snapshot); err != nil {
			return nil, err
		}
		out.Bindings = append(out.Bindings, b)
	}
	return out, rows.Err()
}

func GetFindingTraffic(ctx context.Context, b store.ContextBeginner, findingID int64) (out *FindingTraffic, err error) {
	err = WithEvidenceTx(ctx, b, func(tx *sql.Tx) error { var e error; out, e = FindingTrafficTx(tx, findingID); return e })
	return
}
