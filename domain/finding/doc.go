// Package finding 漏洞领域：漏洞表 CRUD、流量证据管线、复测、富化、
// 漏洞知识库（vuln_kb 1day 打击库）与漏洞情报（vuln_intel）。
//
// 分层约束：属于 domain，可 import finduai/core/*，禁止 import finduai/plugins/*。
// 校验：scripts/check-layering.sh（CI 闸门）。
//
// 迁移边界（Step 3 批 3 第七刀）：
//   - 列表/分组/统计/资产树（ListFindings* / FindingStats / buildFindingAssetTree 家族）、
//     DeleteFinding、RecordFindingTx、取证锁协议（LockFindingEvidenceTx /
//     LockTaskEvidenceTx）、EditFindingTraffic、setFindingCol 同步族、
//     AddFindingFollowUpIntent 留在 db 装配层——跨 findings×tasks×assets×companies×
//     exploration_*×task_archives（规范三-2）；
//   - 复测的 CreateFindingRetest（快照跨 findings×assets×tasks×conversations）
//     与 FinishFindingRetest（复测×漏洞×推送事件）留 db 装配层；
//   - 证据事务协议 WithEvidenceTx 落本包（advisory 锁 + BeginTx 是取证管线的
//     属主机制），db 侧保留同名方法转发（evidence 包零改动）。
package finding
