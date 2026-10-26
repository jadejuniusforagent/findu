/**
 * 运行服务（WSL / 宿主机）页面
 * 按模块分组统计本机服务状态，并支持按需 启动/停止/重启/设置开机自启。
 * 数据来源：GET /api/system-services
 * 操作：POST /api/system-services/:id/{start|stop|restart|autostart}
 */
(function () {
    'use strict';

    var SS_POLL_MS = 10000;
    var ssPollTimer = null;
    var ssAutoRefresh = true;
    var ssBusy = {};

    function ssT(key, fallback) {
        try {
            if (typeof window.t === 'function') {
                var v = window.t('systemServices.' + key);
                if (v && v !== 'systemServices.' + key) return v;
            }
        } catch (e) { /* ignore */ }
        return fallback;
    }

    function esc(s) {
        if (s === null || s === undefined) return '';
        return String(s).replace(/[&<>"']/g, function (c) {
            return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
        });
    }

    function ssApi(url, opts) {
        if (typeof apiFetch === 'function') return apiFetch(url, opts);
        return fetch(url, opts || {});
    }

    function badge(text, cls) {
        return '<span class="ss-badge ' + (cls || '') + '">' + esc(text) + '</span>';
    }

    function renderStats(snap) {
        var el = document.getElementById('ss-stats');
        if (!el) return;
        var missingAutostart = 0;
        (snap.modules || []).forEach(function (m) {
            (m.services || []).forEach(function (s) {
                if (s.autostart_supported && s.expected_autostart && !s.autostart_enabled) missingAutostart++;
            });
        });
        el.innerHTML =
            '<div class="ss-stat"><div class="ss-stat-num">' + (snap.total || 0) + '</div><div class="ss-stat-label">' + ssT('statTotal', '服务总数') + '</div></div>' +
            '<div class="ss-stat ss-stat-ok"><div class="ss-stat-num">' + (snap.running || 0) + '</div><div class="ss-stat-label">' + ssT('statRunning', '运行中') + '</div></div>' +
            '<div class="ss-stat ss-stat-bad"><div class="ss-stat-num">' + (snap.stopped || 0) + '</div><div class="ss-stat-label">' + ssT('statStopped', '已停止') + '</div></div>' +
            '<div class="ss-stat ss-stat-warn"><div class="ss-stat-num">' + (snap.autostart_ok || 0) + '</div><div class="ss-stat-label">' + ssT('statAutostart', '已开机自启') + '</div></div>' +
            '<div class="ss-stat ss-stat-warn"><div class="ss-stat-num">' + missingAutostart + '</div><div class="ss-stat-label">' + ssT('statMissingAutostart', '期望自启未启用') + '</div></div>';
    }

    function serviceButtons(s) {
        var id = esc(s.id);
        var btns = [];
        if (s.running) {
            btns.push('<button class="ss-btn" onclick="ssAct(\'' + id + '\',\'stop\')">' + ssT('btnStop', '停止') + '</button>');
            btns.push('<button class="ss-btn" onclick="ssAct(\'' + id + '\',\'restart\')">' + ssT('btnRestart', '重启') + '</button>');
        } else {
            if (s.start_supported) {
                btns.push('<button class="ss-btn ss-btn-primary" onclick="ssAct(\'' + id + '\',\'start\')">' + ssT('btnStart', '启动') + '</button>');
            } else {
                btns.push('<button class="ss-btn" disabled title="' + ssT('noStartCmd', '未配置启动命令') + '">' + ssT('btnStart', '启动') + '</button>');
            }
        }
        if (s.autostart_supported) {
            if (s.autostart_enabled) {
                btns.push('<button class="ss-btn" onclick="ssAutostart(\'' + id + '\',false)">' + ssT('btnDisableAutostart', '关闭自启') + '</button>');
            } else {
                btns.push('<button class="ss-btn" onclick="ssAutostart(\'' + id + '\',true)">' + ssT('btnEnableAutostart', '设置开机自启') + '</button>');
            }
        } else if (s.suggested_unit) {
            btns.push('<button class="ss-btn" onclick="ssShowUnit(\'' + id + '\')">' + ssT('btnViewUnit', '自启配置') + '</button>');
        }
        return btns.join('');
    }

    function renderService(s) {
        var running = !!s.running;
        var dotCls = running ? 'ss-dot-ok' : 'ss-dot-bad';
        var stateTxt = running ? ssT('stateRunning', '运行中') : ssT('stateStopped', '已停止');
        var stateCls = running ? 'ss-badge-ok' : 'ss-badge-bad';
        var warn = '';
        if (s.autostart_supported && s.expected_autostart && !s.autostart_enabled) {
            warn = ' ' + badge(ssT('warnNoAutostart', '期望自启未启用'), 'ss-badge-warn');
        }
        if (s.optional) {
            warn += ' ' + badge(ssT('optional', '可选'), 'ss-badge-muted');
        }
        var error = s.error ? '<div class="ss-svc-err">' + esc(s.error) + '</div>' : '';
        return '' +
            '<div class="ss-svc ' + (ssBusy[s.id] ? 'ss-svc-busy' : '') + '">' +
                '<div class="ss-svc-main">' +
                    '<div class="ss-svc-title"><span class="ss-dot ' + dotCls + '"></span>' +
                        '<span class="ss-svc-name">' + esc(s.name || s.id) + '</span>' +
                        '<span class="ss-kind">' + esc(s.kind) + '</span>' +
                        badge(stateTxt, stateCls) + warn +
                    '</div>' +
                    (s.description ? '<div class="ss-svc-desc">' + esc(s.description) + '</div>' : '') +
                    (s.detail ? '<div class="ss-svc-detail">' + esc(s.detail) + '</div>' : '') +
                    error +
                '</div>' +
                '<div class="ss-svc-actions">' + serviceButtons(s) + '</div>' +
            '</div>';
    }

    function renderModules(snap) {
        var el = document.getElementById('ss-modules');
        if (!el) return;
        if (!snap.enabled) {
            el.innerHTML = '<div class="ss-empty">' + esc(snap.message || ssT('disabled', '未启用 system_services 配置')) + '</div>';
            return;
        }
        if (!snap.modules || !snap.modules.length) {
            el.innerHTML = '<div class="ss-empty">' + ssT('empty', '暂无服务配置') + '</div>';
            return;
        }
        var html = '';
        snap.modules.forEach(function (m) {
            var running = m.running || 0;
            var total = m.total || 0;
            html += '<div class="ss-module">' +
                '<div class="ss-module-head">' +
                    '<div class="ss-module-title">' + esc(m.name || m.id) +
                        (m.description ? '<span class="ss-module-desc">' + esc(m.description) + '</span>' : '') +
                    '</div>' +
                    '<div class="ss-module-badges">' +
                        badge(ssT('runningOf', '运行') + ' ' + running + '/' + total, running === total ? 'ss-badge-ok' : 'ss-badge-warn') +
                    '</div>' +
                '</div>' +
                '<div class="ss-services">';
            (m.services || []).forEach(function (s) { html += renderService(s); });
            html += '</div></div>';
        });
        el.innerHTML = html;
    }

    function setUpdated(snap) {
        var el = document.getElementById('ss-updated');
        if (!el) return;
        var ts = snap && snap.generated_at ? new Date(snap.generated_at) : new Date();
        el.textContent = ssT('updatedAt', '更新于') + ' ' + ts.toLocaleTimeString();
    }

    window.ssRefresh = function () {
        return ssLoad();
    };

    async function ssLoad() {
        var el = document.getElementById('ss-modules');
        try {
            var resp = await ssApi('/api/system-services');
            var snap = await resp.json();
            renderStats(snap);
            renderModules(snap);
            setUpdated(snap);
        } catch (e) {
            if (el) el.innerHTML = '<div class="ss-empty ss-err">' + esc(ssT('loadFailed', '加载失败：') + e) + '</div>';
        }
        return Promise.resolve();
    }

    function toast(msg, isErr) {
        if (typeof showNotification === 'function') { showNotification(msg, isErr ? 'error' : 'success'); return; }
        var el = document.getElementById('ss-toast');
        if (!el) return;
        el.textContent = msg;
        el.className = 'ss-toast ' + (isErr ? 'ss-toast-err' : 'ss-toast-ok') + ' ss-toast-show';
        setTimeout(function () { el.className = 'ss-toast'; }, 3000);
    }

    async function ssPost(url, body) {
        var opts = { method: 'POST' };
        if (body) {
            opts.headers = { 'Content-Type': 'application/json' };
            opts.body = JSON.stringify(body);
        }
        var resp = await ssApi(url, opts);
        var data = null;
        try { data = await resp.json(); } catch (e) { /* ignore */ }
        if (!resp.ok) throw new Error((data && data.error) || ('HTTP ' + resp.status));
        return data;
    }

    window.ssAct = async function (id, action) {
        if (ssBusy[id]) return;
        ssBusy[id] = true;
        try {
            await ssPost('/api/system-services/' + encodeURIComponent(id) + '/' + action, null);
            toast(ssT('actionOk', '操作成功') + ': ' + action + ' ' + id, false);
        } catch (e) {
            toast(id + ' ' + action + ' ' + ssT('failed', '失败') + ': ' + e.message, true);
        } finally {
            ssBusy[id] = false;
            ssLoad();
        }
    };

    window.ssAutostart = async function (id, enable) {
        if (ssBusy[id]) return;
        ssBusy[id] = true;
        try {
            await ssPost('/api/system-services/' + encodeURIComponent(id) + '/autostart', { enable: !!enable });
            toast(id + ' ' + (enable ? ssT('autostartOn', '已设置开机自启') : ssT('autostartOff', '已关闭开机自启')), false);
        } catch (e) {
            toast(id + ' ' + ssT('failed', '失败') + ': ' + e.message, true);
        } finally {
            ssBusy[id] = false;
            ssLoad();
        }
    };

    window.ssShowUnit = async function (id) {
        try {
            var resp = await ssApi('/api/system-services/' + encodeURIComponent(id));
            var data = await resp.json();
            var unit = data && data.service && data.service.suggested_unit;
            if (!unit) { toast(ssT('noUnit', '暂无自启配置建议'), true); return; }
            if (window.prompt) {
                window.prompt(ssT('unitHint', '可将以下内容保存为 /etc/systemd/system/' + id + '.service，再执行 systemctl enable --now ' + id), unit);
            }
        } catch (e) {
            toast(ssT('failed', '失败') + ': ' + e.message, true);
        }
    };

    function ssStopPoll() {
        if (ssPollTimer) { clearInterval(ssPollTimer); ssPollTimer = null; }
    }

    function ssStartPoll() {
        ssStopPoll();
        if (ssAutoRefresh) {
            ssPollTimer = setInterval(ssLoad, SS_POLL_MS);
        }
    }

    window.initSystemServicesPage = function () {
        ssStopPoll();
        ssLoad().then(ssStartPoll);
        var auto = document.getElementById('ss-auto');
        if (auto) {
            auto.checked = ssAutoRefresh;
            auto.onchange = function () {
                ssAutoRefresh = !!auto.checked;
                ssStartPoll();
            };
        }
    };

    // 离开页面时停止轮询
    window.addEventListener('hashchange', function () {
        if ((location.hash || '').indexOf('system-services') === -1) ssStopPoll();
    });
})();
