(function () {
    'use strict';

    // 引擎任务面板：合并后 findu 渗透引擎的任务（/api/tasks）此前在界面上没有入口，
    // 仪表盘「运行中对话」也只统计 findu 的 agent-loop 任务。这里补一个只读视图，
    // 复用 findu 任务管理页的表格样式，作为该页的第二个 tab。

    var STATE = { tasks: null, active: '', loading: false, error: '', timer: null };

    var STATUS_META = {
        running:  { label: '运行中', cls: 'is-running' },
        created:  { label: '待运行', cls: 'is-pending' },
        queued:   { label: '排队中', cls: 'is-pending' },
        paused:   { label: '已暂停', cls: 'is-warning' },
        done:     { label: '已完成', cls: 'is-success' },
        failed:   { label: '失败',   cls: 'is-danger' },
        timeout:  { label: '超时',   cls: 'is-danger' }
    };

    function isRunning(t) {
        if (!t) return false;
        if (t.status === 'running') return true;
        return Number(t.in_flight || 0) > 0;
    }

    function statusMeta(t) {
        return STATUS_META[t && t.status] || { label: (t && t.status) || '未知', cls: 'is-pending' };
    }

    function fmtNum(n) {
        var v = Number(n || 0);
        if (!isFinite(v)) return '0';
        return v.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
    }

    function fmtTokensCompact(n) {
        var v = Number(n || 0);
        if (!isFinite(v) || v <= 0) return '0';
        if (v >= 1000000) return (v / 1000000).toFixed(1).replace(/\.0$/, '') + 'M';
        if (v >= 1000) return (v / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
        return String(Math.round(v));
    }

    function fmtTime(unix) {
        var v = Number(unix || 0);
        if (!isFinite(v) || v <= 0) return '—';
        var d = new Date(v * 1000);
        var p = function (x) { return (x < 10 ? '0' : '') + x; };
        return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) +
            ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
    }

    function findingCount(t) {
        if (!t || !t.findings) return 0;
        var f = t.findings;
        if (Array.isArray(f)) return f.length;
        if (typeof f === 'number') return f;
        return Number(f.critical || 0) + Number(f.high || 0) + Number(f.medium || 0) +
            Number(f.low || 0) + Number(f.info || 0);
    }

    function tokenTotal(t) {
        if (!t || !t.tokens) return 0;
        var k = t.tokens;
        if (typeof k === 'number') return k;
        return Number(k.input_tokens || k.input || 0) + Number(k.output_tokens || k.output || 0) +
            Number(k.total_tokens || k.total || 0);
    }

    function cell(text, cls) {
        var td = document.createElement('td');
        if (cls) td.className = cls;
        td.textContent = (text === null || text === undefined || text === '') ? '—' : String(text);
        return td;
    }

    function renderTable() {
        var tbody = document.getElementById('engine-tasks-tbody');
        if (!tbody) return;
        tbody.innerHTML = '';

        if (STATE.loading) {
            var trL = document.createElement('tr');
            var tdL = cell('加载中…', 'engine-tasks-empty');
            tdL.colSpan = 8;
            trL.appendChild(tdL);
            tbody.appendChild(trL);
            return;
        }
        if (STATE.error) {
            var trE = document.createElement('tr');
            var tdE = cell('加载失败：' + STATE.error, 'engine-tasks-empty');
            tdE.colSpan = 8;
            trE.appendChild(tdE);
            tbody.appendChild(trE);
            return;
        }

        var rows = (STATE.tasks || []).slice();
        var q = (document.getElementById('engine-tasks-search') || {}).value || '';
        q = String(q).trim().toLowerCase();
        var st = (document.getElementById('engine-tasks-status-filter') || {}).value || '';

        rows = rows.filter(function (t) {
            if (st === 'running' && !isRunning(t)) return false;
            if (st === 'done' && t.status !== 'done') return false;
            if (st === 'idle' && (isRunning(t) || t.status === 'done')) return false;
            if (!q) return true;
            var hay = [t.id, t.name, t.description, t.goal, String(t.exploration_id)].join(' ').toLowerCase();
            return hay.indexOf(q) >= 0;
        });

        rows.sort(function (a, b) {
            var ra = isRunning(a) ? 0 : 1, rb = isRunning(b) ? 0 : 1;
            if (ra !== rb) return ra - rb;
            return Number(b.created_unix || 0) - Number(a.created_unix || 0);
        });

        if (!rows.length) {
            var tr0 = document.createElement('tr');
            var td0 = cell('没有匹配的引擎任务', 'engine-tasks-empty');
            td0.colSpan = 8;
            tr0.appendChild(td0);
            tbody.appendChild(tr0);
            return;
        }

        rows.forEach(function (t) {
            var tr = document.createElement('tr');
            if (isRunning(t)) tr.className = 'engine-task-row is-running';

            tr.appendChild(cell('#' + t.id, 'engine-task-id'));

            var tdMain = document.createElement('td');
            var title = document.createElement('div');
            title.className = 'engine-task-title';
            title.textContent = (t.name && String(t.name).trim()) || (t.description ? String(t.description).slice(0, 60) : ('任务 #' + t.id));
            var sub = document.createElement('div');
            sub.className = 'engine-task-sub';
            sub.textContent = t.goal || t.description || '';
            tdMain.appendChild(title);
            tdMain.appendChild(sub);
            tr.appendChild(tdMain);

            var meta = statusMeta(t);
            var tdSt = document.createElement('td');
            var badge = document.createElement('span');
            badge.className = 'engine-task-badge ' + meta.cls;
            badge.textContent = meta.label;
            tdSt.appendChild(badge);
            tr.appendChild(tdSt);

            var goalsTotal = Number(t.goals_total || 0);
            var goalsMet = Number(t.goals_met || 0);
            tr.appendChild(cell(goalsTotal > 0 ? (goalsMet + ' / ' + goalsTotal) : '—', 'engine-task-num'));
            tr.appendChild(cell(fmtNum(findingCount(t)), 'engine-task-num'));
            tr.appendChild(cell(fmtTokensCompact(tokenTotal(t)), 'engine-task-num'));
            tr.appendChild(cell(fmtTime(t.created_unix || t.created_at), 'engine-task-time'));
            tr.appendChild(cell(fmtTime(t.last_activity_unix), 'engine-task-time'));

            tbody.appendChild(tr);
        });
    }

    function renderSummary() {
        var el = document.getElementById('engine-tasks-summary');
        if (!el) return;
        var all = STATE.tasks || [];
        var running = all.filter(isRunning).length;
        var done = all.filter(function (t) { return t.status === 'done'; }).length;
        var findings = all.reduce(function (acc, t) { return acc + findingCount(t); }, 0);
        var tokens = all.reduce(function (acc, t) { return acc + tokenTotal(t); }, 0);
        el.textContent = '共 ' + all.length + ' 个任务 · 运行中 ' + running + ' · 已完成 ' + done +
            ' · 发现 ' + fmtNum(findings) + ' · Token ' + fmtTokensCompact(tokens);
    }

    function refreshEngineTasks() {
        var tbody = document.getElementById('engine-tasks-tbody');
        if (!tbody) return Promise.resolve();
        if (typeof apiFetch !== 'function') {
            STATE.error = '接口封装未加载';
            renderTable();
            return Promise.resolve();
        }
        STATE.loading = true;
        STATE.error = '';
        renderTable();
        return apiFetch('/api/tasks')
            .then(function (r) { return r && r.ok ? r.json() : Promise.reject(new Error('HTTP ' + (r ? r.status : '?'))); })
            .then(function (d) {
                STATE.tasks = (d && d.tasks) || [];
                STATE.active = (d && d.active) || '';
                STATE.loading = false;
                renderTable();
                renderSummary();
            })
            .catch(function (e) {
                STATE.loading = false;
                STATE.error = (e && e.message) || '未知错误';
                renderTable();
                renderSummary();
            });
    }

    function switchTasksView(view) {
        var batch = document.getElementById('batch-queues-section');
        var engine = document.getElementById('tasks-engine-panel');
        var tabBatch = document.getElementById('tasks-tab-batch');
        var tabEngine = document.getElementById('tasks-tab-engine');
        if (!engine) return;
        var isEngine = view === 'engine';
        if (engine) engine.style.display = isEngine ? '' : 'none';
        if (tabBatch) tabBatch.classList.toggle('is-active', !isEngine);
        if (tabEngine) tabEngine.classList.toggle('is-active', isEngine);
        if (isEngine) {
            if (batch) batch.style.display = 'none';
            refreshEngineTasks();
            startEngineTasksPoll();
        } else {
            stopEngineTasksPoll();
            if (typeof refreshBatchQueues === 'function') refreshBatchQueues();
        }
    }

    function startEngineTasksPoll() {
        stopEngineTasksPoll();
        STATE.timer = setInterval(function () {
            if (document.visibilityState === 'hidden') return;
            var engine = document.getElementById('tasks-engine-panel');
            if (!engine || engine.style.display === 'none') return;
            refreshEngineTasks();
        }, 30000);
    }

    function stopEngineTasksPoll() {
        if (STATE.timer) { clearInterval(STATE.timer); STATE.timer = null; }
    }

    // 供仪表盘合并统计使用：引擎里正在跑的任务数。
    function engineTasksRunningCount() {
        if (!STATE.tasks) return null;
        return STATE.tasks.filter(isRunning).length;
    }

    window.__engineTasksFilter = renderTable;
    window.switchTasksView = switchTasksView;
    window.refreshEngineTasks = refreshEngineTasks;
    window.engineTasksRunningCount = engineTasksRunningCount;
})();
