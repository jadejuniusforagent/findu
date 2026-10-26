// 从实践项目提炼的 Skill（P0-2.2）
// 候选榜（任务结束自动蒸馏）→ LLM 生成 SKILL.md 草稿 → 人工审核通过后写入 skills_dir 生效。
// 字段：出处(sourceRef) / 能做什么(capability) / 当前完成度(completion) / 提炼优先级(priority) / 状态。

let skillCandidatesCache = [];

function _scEscape(v) {
    return String(v == null ? '' : v)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function _scSourceLabel(c) {
    if (c.sourceRef) return c.sourceRef;
    const parts = [];
    if (c.conversationId) parts.push('会话 ' + c.conversationId);
    if (c.projectId) parts.push('项目 ' + c.projectId);
    return parts.length ? parts.join(' / ') : '—';
}

function _scSourceKindLabel(s) {
    if (s === 'skill_miss') return '技能缺口';
    if (s === 'tool_failure') return '工具反复失败';
    return s || '—';
}

function _scStatusLabel(s) {
    switch (s) {
        case 'new': return '待生成草稿';
        case 'drafted': return '待人工审核';
        case 'approved': return '已通过并生效';
        case 'rejected': return '已驳回';
        default: return s || '—';
    }
}

function _scStatusColor(s) {
    switch (s) {
        case 'new': return '#8a8a8a';
        case 'drafted': return '#d08a00';
        case 'approved': return '#1a9e5a';
        case 'rejected': return '#c0392b';
        default: return '#8a8a8a';
    }
}

function _scPriorityColor(p) {
    if (p >= 70) return '#c0392b';
    if (p >= 40) return '#d08a00';
    return '#3a7ca5';
}

async function refreshSkillCandidates() {
    const box = document.getElementById('skill-candidates-list');
    if (!box) return;
    box.innerHTML = '<div class="loading-spinner">加载中...</div>';
    try {
        const resp = await apiFetch('/api/skills/candidates');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const data = await resp.json();
        skillCandidatesCache = Array.isArray(data.candidates) ? data.candidates : [];
        renderSkillCandidates();
    } catch (e) {
        box.innerHTML = '<div class="empty-state" style="padding:24px;color:#c0392b;">加载失败：' + _scEscape(e && e.message ? e.message : e) + '</div>';
    }
}

function renderSkillCandidates() {
    const box = document.getElementById('skill-candidates-list');
    if (!box) return;
    if (!skillCandidatesCache.length) {
        box.innerHTML = '<div class="empty-state" style="padding:24px;color:#8a8a8a;">暂无候选。跑几轮任务后，蒸馏出的「技能缺口 / 工具反复失败」会出现在这里。</div>';
        return;
    }
    const rows = skillCandidatesCache.map(function (c) {
        const hasDraft = !!c.hasDraft;
        let actions = '';
        if (!hasDraft) {
            actions += '<button class="btn-secondary" data-sc-action="draft" data-sc-id="' + _scEscape(c.id) + '">生成草稿</button> ';
        } else {
            actions += '<button class="btn-secondary" data-sc-action="view" data-sc-id="' + _scEscape(c.id) + '">查看草稿</button> ';
            actions += '<button class="btn-primary" data-sc-action="approve" data-sc-id="' + _scEscape(c.id) + '">通过</button> ';
        }
        if (c.status !== 'rejected') {
            actions += '<button class="btn-secondary" data-sc-action="reject" data-sc-id="' + _scEscape(c.id) + '">驳回</button>';
        }
        return '<tr>' +
            '<td style="white-space:nowrap;"><code>' + _scEscape(c.skillName) + '</code><br><span style="color:#8a8a8a;font-size:12px;">' + _scEscape(_scSourceKindLabel(c.source)) + '</span></td>' +
            '<td style="font-size:12px;">' + _scEscape(_scSourceLabel(c)) + '</td>' +
            '<td>' + (c.capability ? _scEscape(c.capability) : '<span style="color:#8a8a8a;">（未生成）</span>') + '</td>' +
            '<td style="text-align:center;">' + (hasDraft ? (_scEscape(c.completion) + '%') : '—') + '</td>' +
            '<td style="text-align:center;font-weight:600;color:' + _scPriorityColor(c.priority) + ';">' + _scEscape(c.priority) + '</td>' +
            '<td style="white-space:nowrap;color:' + _scStatusColor(c.status) + ';">' + _scEscape(_scStatusLabel(c.status)) + '</td>' +
            '<td style="white-space:nowrap;">' + actions + '</td>' +
            '</tr>';
    }).join('');
    box.innerHTML =
        '<div style="overflow-x:auto;">' +
        '<table class="data-table" style="width:100%;border-collapse:collapse;font-size:13px;">' +
        '<thead><tr style="text-align:left;border-bottom:2px solid var(--border-color,#ddd);">' +
        '<th style="padding:8px;">技能名</th><th style="padding:8px;">出处</th><th style="padding:8px;">能做什么</th>' +
        '<th style="padding:8px;">完成度</th><th style="padding:8px;">提炼优先级</th><th style="padding:8px;">状态</th><th style="padding:8px;">操作</th>' +
        '</tr></thead><tbody>' + rows + '</tbody></table></div>' +
        '<div id="skill-candidate-detail" style="margin-top:16px;"></div>';

    box.querySelectorAll('button[data-sc-action]').forEach(function (btn) {
        btn.addEventListener('click', function () {
            const id = btn.getAttribute('data-sc-id');
            const action = btn.getAttribute('data-sc-action');
            if (action === 'draft') generateSkillCandidateDraft(id);
            else if (action === 'view') viewSkillCandidateDraft(id);
            else if (action === 'approve') approveSkillCandidate(id);
            else if (action === 'reject') rejectSkillCandidate(id);
        });
    });
}

async function generateSkillCandidateDraft(id) {
    const box = document.getElementById('skill-candidates-list');
    if (box) box.insertAdjacentHTML('afterbegin', '<div id="sc-busy" style="padding:8px;color:#d08a00;">正在调用模型生成草稿…</div>');
    try {
        const resp = await apiFetch('/api/skills/candidates/' + encodeURIComponent(id) + '/draft', { method: 'POST' });
        const data = await resp.json().catch(function () { return {}; });
        if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
        if (data.message) { /* 提示可选 */ }
    } catch (e) {
        alert('生成草稿失败：' + (e && e.message ? e.message : e));
    } finally {
        const busy = document.getElementById('sc-busy');
        if (busy) busy.remove();
        refreshSkillCandidates();
    }
}

async function viewSkillCandidateDraft(id) {
    const detail = document.getElementById('skill-candidate-detail');
    if (!detail) return;
    detail.innerHTML = '<div class="loading-spinner">加载草稿…</div>';
    try {
        const resp = await apiFetch('/api/skills/candidates/' + encodeURIComponent(id));
        const data = await resp.json().catch(function () { return {}; });
        if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
        const c = data.candidate || {};
        detail.innerHTML =
            '<h3 style="margin:0 0 8px;">草稿：<code>' + _scEscape(c.skillName) + '</code>' +
            ' <span style="font-size:12px;color:#8a8a8a;">（模型 ' + _scEscape(c.draftModel || '—') + '）</span></h3>' +
            '<div style="font-size:13px;margin-bottom:8px;">' +
            '<b>能做什么：</b>' + _scEscape(c.capability || '—') + '<br>' +
            '<b>出处：</b>' + _scEscape(_scSourceLabel(c)) + '<br>' +
            '<b>完成度：</b>' + _scEscape(c.completion || 0) + '%　<b>提炼优先级：</b>' + _scEscape(c.priority || 0) +
            '</div>' +
            '<pre style="max-height:460px;overflow:auto;background:var(--code-bg,#f6f8fa);padding:12px;border-radius:6px;white-space:pre-wrap;">' +
            _scEscape(c.draftBody || '') + '</pre>';
    } catch (e) {
        detail.innerHTML = '<div style="color:#c0392b;">加载草稿失败：' + _scEscape(e && e.message ? e.message : e) + '</div>';
    }
}

async function approveSkillCandidate(id) {
    if (!confirm('通过后会把该 SKILL.md 写入 skills 目录，下次运行即生效。确认通过？')) return;
    try {
        const resp = await apiFetch('/api/skills/candidates/' + encodeURIComponent(id) + '/approve', { method: 'POST' });
        const data = await resp.json().catch(function () { return {}; });
        if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
        alert(data.message || '已通过');
    } catch (e) {
        alert('通过失败：' + (e && e.message ? e.message : e));
    } finally {
        refreshSkillCandidates();
    }
}

async function rejectSkillCandidate(id) {
    if (!confirm('确认驳回该候选？')) return;
    try {
        const resp = await apiFetch('/api/skills/candidates/' + encodeURIComponent(id) + '/reject', { method: 'POST' });
        const data = await resp.json().catch(function () { return {}; });
        if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
    } catch (e) {
        alert('驳回失败：' + (e && e.message ? e.message : e));
    } finally {
        refreshSkillCandidates();
    }
}

async function clearSkillCandidates() {
    if (!confirm('确认清空全部技能候选（含已生成草稿）？')) return;
    try {
        const resp = await apiFetch('/api/skills/candidates', { method: 'DELETE' });
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
    } catch (e) {
        alert('清空失败：' + (e && e.message ? e.message : e));
    } finally {
        refreshSkillCandidates();
    }
}

window.refreshSkillCandidates = refreshSkillCandidates;
window.generateSkillCandidateDraft = generateSkillCandidateDraft;
window.viewSkillCandidateDraft = viewSkillCandidateDraft;
window.approveSkillCandidate = approveSkillCandidate;
window.rejectSkillCandidate = rejectSkillCandidate;
window.clearSkillCandidates = clearSkillCandidates;
