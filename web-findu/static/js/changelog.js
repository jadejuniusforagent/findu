// 版本记录（变更日志）页面
//
// 数据源：GET /api/changelog（后端只读解析 docs/CHANGELOG.md）。
// 因数据源是随代码走的 Markdown，改/加记录后**无需重启**，点「刷新」即可见。

let changelogLoaded = false;

/** 变更类型 -> 标签样式类 */
const CHANGELOG_TAG_CLASS = {
    '新增': 'is-new',
    '优化': 'is-improve',
    '修复': 'is-fix',
    '决策': 'is-decision',
    '风险': 'is-risk',
    '评估': 'is-review',
    '未决': 'is-pending',
};

/** 极简行内 markdown：`code` -> <code>，其余按文本转义 */
function changelogInline(text) {
    const esc = (typeof escapeHtml === 'function')
        ? escapeHtml
        : (s) => String(s == null ? '' : s)
            .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    return esc(text).replace(/`([^`]+)`/g, '<code>$1</code>');
}

function renderChangelogGroups(groups) {
    if (!Array.isArray(groups) || groups.length === 0) {
        return '<div class="changelog-empty">暂无变更记录</div>';
    }
    const parts = [];
    groups.forEach((day) => {
        const entries = Array.isArray(day.entries) ? day.entries : [];
        const entryHtml = entries.map((entry) => {
            const items = (Array.isArray(entry.items) ? entry.items : []).map((it) => {
                const cls = CHANGELOG_TAG_CLASS[it.type] || 'is-other';
                const tag = it.type
                    ? `<span class="changelog-tag ${cls}">${changelogInline(it.type)}</span>`
                    : '';
                return `<li class="changelog-item">${tag}<span class="changelog-text">${changelogInline(it.text)}</span></li>`;
            }).join('');
            return `<article class="changelog-entry">
                <h3 class="changelog-entry-title">${changelogInline(entry.title)}</h3>
                <ul class="changelog-items">${items}</ul>
            </article>`;
        }).join('');
        parts.push(`<section class="changelog-day">
            <div class="changelog-day-head">
                <span class="changelog-date">${changelogInline(day.date)}</span>
                <span class="changelog-count">${entries.length} 次变更</span>
            </div>
            ${entryHtml}
        </section>`);
    });
    return parts.join('');
}

/**
 * 加载并渲染版本记录。
 * @param {boolean} [force] 为 true 时忽略缓存重新拉取（「刷新」按钮用）。
 */
async function loadChangelog(force) {
    const box = document.getElementById('changelog-list');
    if (!box) return;
    if (changelogLoaded && !force) return;

    const meta = document.getElementById('changelog-meta');
    box.innerHTML = '<div class="changelog-empty">加载中…</div>';

    let data;
    try {
        const resp = await apiFetch('/api/changelog');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        data = await resp.json();
    } catch (e) {
        box.innerHTML = `<div class="changelog-empty">加载失败：${changelogInline(String((e && e.message) || e))}</div>`;
        return;
    }

    if (!data || data.available === false) {
        box.innerHTML = `<div class="changelog-empty">${changelogInline((data && data.message) || '变更记录不可用')}</div>`;
        if (meta) meta.textContent = '';
        return;
    }

    box.innerHTML = renderChangelogGroups(data.groups);
    changelogLoaded = true;
    if (meta) {
        const bits = [];
        if (data.updatedAt) bits.push(`更新于 ${data.updatedAt}`);
        if (typeof data.entryCount === 'number') bits.push(`${data.entryCount} 条变更`);
        meta.textContent = bits.join(' · ');
    }
}

window.loadChangelog = loadChangelog;
