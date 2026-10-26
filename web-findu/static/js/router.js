// 页面路由管理
let currentPage = null;

/** chat、漏洞管理页在切换时保留当前 hash 上的查询串（如 ?conversation= / ?conversation_id=） */
function buildHashForPage(pageId) {
    if (pageId !== 'chat' && pageId !== 'vulnerabilities') {
        return pageId;
    }
    const full = window.location.hash.slice(1);
    const parts = full.split('?');
    const curPage = parts[0];
    const q = parts.length > 1 ? parts.slice(1).join('?') : '';
    if (curPage === pageId && q) {
        return pageId + '?' + q;
    }
    return pageId;
}

let chatConversationFromHashSeq = 0;

function cancelScheduledChatConversationFromHash() {
    chatConversationFromHashSeq++;
    setChatConversationRestorePending('', false);
}
window.cancelScheduledChatConversationFromHash = cancelScheduledChatConversationFromHash;

function setChatConversationRestorePending(conversationId, pending) {
    const container = document.querySelector('.chat-container');
    if (!container) return;
    const id = String(conversationId || '').trim();
    if (pending && id) {
        container.classList.add('is-conversation-restoring');
        container.dataset.restoringConversationId = id;
        container.setAttribute('aria-busy', 'true');
        return;
    }
    container.classList.remove('is-conversation-restoring');
    delete container.dataset.restoringConversationId;
    container.removeAttribute('aria-busy');
}

function finishChatConversationRestore(conversationId) {
    setChatConversationRestorePending(conversationId, false);
}
window.finishChatConversationRestore = finishChatConversationRestore;

function scheduleChatConversationFromHash(delayMs) {
    const hash = window.location.hash.slice(1);
    const hashParts = hash.split('?');
    if (hashParts[0] !== 'chat' || hashParts.length < 2) {
        return;
    }
    const params = new URLSearchParams(hashParts.slice(1).join('?'));
    const conversationId = params.get('conversation');
    const projectId = params.get('project');
    if (projectId && typeof setActiveProjectId === 'function') {
        setActiveProjectId(projectId);
        if (typeof refreshChatProjectSelector === 'function') {
            refreshChatProjectSelector();
        }
    }
    if (!conversationId) {
        return;
    }
    // 同一事件循环内先遮住默认新对话状态，避免网络请求返回前闪出“无项目”。
    setChatConversationRestorePending(conversationId, true);
    const token = ++chatConversationFromHashSeq;
    setTimeout(() => {
        if (token !== chatConversationFromHashSeq) {
            return;
        }
        if (typeof loadConversation === 'function') {
            loadConversation(conversationId);
        } else if (typeof window.loadConversation === 'function') {
            window.loadConversation(conversationId);
        } else {
            console.warn('loadConversation function not found');
        }
    }, delayMs);
}

/** 跳转到指定对话：单次切页 + 单次加载，避免 hashchange 与手动 load 重复触发导致闪烁 */
function navigateToConversation(conversationId) {
    const cid = String(conversationId || '').trim();
    if (!cid) return;
    const targetHash = 'chat?conversation=' + encodeURIComponent(cid);
    const alreadyOnChat = currentPage === 'chat';

    if (window.location.hash.slice(1) !== targetHash) {
        history.replaceState(null, '', '#' + targetHash);
    }

    if (!alreadyOnChat) {
        switchPage('chat');
    }

    if (typeof loadConversation === 'function') {
        void loadConversation(cid);
    } else if (typeof window.loadConversation === 'function') {
        void window.loadConversation(cid);
    }
}
window.navigateToConversation = navigateToConversation;

// 初始化路由
function initRouter() {
    // 从URL hash读取页面（如果有）
    const hash = window.location.hash.slice(1);
    if (hash) {
        const hashParts = hash.split('?');
        let pageId = hashParts[0];
        if (pageId === 'c2') pageId = 'c2-listeners';
        if (pageId && pageId.indexOf('console-') === 0 && window.switchConsolePage) {
            window.switchConsolePage(pageId);
            return;
        }
        if (pageId && ['dashboard', 'chat', 'hitl', 'tool-guard', 'asset-overview', 'asset-library', 'info-collect', 'projects', 'vulnerabilities', 'webshell', 'chat-files', 'mcp-monitor', 'mcp-management', 'knowledge-management', 'knowledge-retrieval-logs', 'roles-management', 'platform-rbac', 'workflows', 'skills-monitor', 'skill-candidates', 'skills-management', 'agents-management', 'settings', 'system-services', 'changelog', 'tasks', 'c2-listeners', 'c2-sessions', 'c2-tasks', 'c2-payloads', 'c2-events', 'c2-profiles'].includes(pageId)) {
            switchPage(pageId);
            if (pageId === 'chat') {
                scheduleChatConversationFromHash(0);
            }
            return;
        }
    }
    
    // 默认显示仪表盘
    switchPage('dashboard');
}

// 切换页面
function switchPage(pageId) {
    const targetPage = document.getElementById(`page-${pageId}`);
    if (!targetPage) return;
    if (pageId !== 'chat') {
        setChatConversationRestorePending('', false);
        if (currentPage === 'chat' && typeof window.abandonChatConversationForPageNavigation === 'function') {
            window.abandonChatConversationForPageNavigation();
        }
    }

    // 导航点击会修改 hash，随后浏览器还会触发 hashchange。
    // 同一页面已经激活时不再重复初始化，避免接口重复请求和页面二次重绘。
    if (currentPage === pageId && targetPage.classList.contains('active')) {
        const currentHash = buildHashForPage(pageId);
        if (window.location.hash.slice(1) !== currentHash) {
            window.location.hash = currentHash;
        }
        return;
    }

    if (typeof window.syncC2NavOnceFromServer === 'function') {
        void window.syncC2NavOnceFromServer();
    }
    // 隐藏所有页面
    document.querySelectorAll('.page').forEach(page => {
        page.classList.remove('active');
    });
    
    // 显示目标页面
    targetPage.classList.add('active');
    currentPage = pageId;
        
    const newHash = buildHashForPage(pageId);
    if (window.location.hash.slice(1) !== newHash) {
        window.location.hash = newHash;
    }
        
    // 更新导航状态
    updateNavState(pageId);
        
    // 页面特定的初始化
    initPage(pageId);

    if (typeof applyRBACToUI === 'function') {
        applyRBACToUI(targetPage);
    }
}
window.switchPage = switchPage;

// 更新导航状态
function updateNavState(pageId) {
    // 移除所有活动状态
    document.querySelectorAll('.nav-item').forEach(item => {
        item.classList.remove('active');
        item.classList.remove('expanded');
    });
    
    document.querySelectorAll('.nav-submenu-item').forEach(item => {
        item.classList.remove('active');
    });
    
    // 设置活动状态
    if (pageId === 'hitl' || pageId === 'tool-guard') {
        const securityItem = document.querySelector('.nav-item[data-page="security"]');
        if (securityItem) {
            securityItem.classList.add('active');
            securityItem.classList.add('expanded');
        }
        const submenuItem = document.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) submenuItem.classList.add('active');
    } else if (pageId === 'asset-overview' || pageId === 'asset-library' || pageId === 'info-collect') {
        const assetItem = document.querySelector('.nav-item[data-page="assets"]');
        if (assetItem) {
            assetItem.classList.add('active');
            assetItem.classList.add('expanded');
        }
        const submenuItem = document.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) submenuItem.classList.add('active');
    } else if (pageId === 'mcp-monitor' || pageId === 'mcp-management') {
        // MCP子菜单项
        const mcpItem = document.querySelector('.nav-item[data-page="mcp"]');
        if (mcpItem) {
            mcpItem.classList.add('active');
            // 展开MCP子菜单
            mcpItem.classList.add('expanded');
        }
        
        const submenuItem = document.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) {
            submenuItem.classList.add('active');
        }
    } else if (pageId === 'knowledge-management' || pageId === 'knowledge-retrieval-logs') {
        // 知识子菜单项
        const knowledgeItem = document.querySelector('.nav-item[data-page="knowledge"]');
        if (knowledgeItem) {
            knowledgeItem.classList.add('active');
            // 展开知识子菜单
            knowledgeItem.classList.add('expanded');
        }
        
        const submenuItem = document.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) {
            submenuItem.classList.add('active');
        }
    } else if (pageId === 'skills-monitor' || pageId === 'skills-management' || pageId === 'skill-candidates') {
        // Skills子菜单项
        const skillsItem = document.querySelector('.nav-item[data-page="skills"]');
        if (skillsItem) {
            skillsItem.classList.add('active');
            // 展开Skills子菜单
            skillsItem.classList.add('expanded');
        }
        
        const submenuItem = document.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) {
            submenuItem.classList.add('active');
        }
    } else if (pageId === 'agents-management') {
        const agentsItem = document.querySelector('.nav-item[data-page="agents"]');
        if (agentsItem) {
            agentsItem.classList.add('active');
            agentsItem.classList.add('expanded');
        }
        const submenuItem = document.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) {
            submenuItem.classList.add('active');
        }
    } else if (pageId.startsWith('c2') || pageId === 'c2-listeners' || pageId === 'c2-sessions' || pageId === 'c2-tasks' || pageId === 'c2-payloads' || pageId === 'c2-events' || pageId === 'c2-profiles') {
        // C2 子菜单项
        const c2Item = document.querySelector('.nav-item[data-page="c2"]');
        if (c2Item) {
            c2Item.classList.add('active');
            c2Item.classList.add('expanded');
        }
        const submenuItem = document.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) {
            submenuItem.classList.add('active');
        }
    } else if (pageId === 'roles-management') {
        // 角色子菜单项
        const rolesItem = document.querySelector('.nav-item[data-page="roles"]');
        if (rolesItem) {
            rolesItem.classList.add('active');
            // 展开角色子菜单
            rolesItem.classList.add('expanded');
        }
        
        const submenuItem = document.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) {
            submenuItem.classList.add('active');
        }
    } else if (pageId.indexOf('console-') === 0) {
        // 控制台内嵌页：高亮子项，同时点亮并展开它所属的分组
        var submenuItem = document.querySelector('.nav-submenu-item[data-page="' + pageId + '"]');
        if (submenuItem) {
            submenuItem.classList.add('active');
            var groupItem = submenuItem.closest('.nav-item');
            if (groupItem) {
                groupItem.classList.add('active');
                groupItem.classList.add('expanded');
            }
        }
    } else {
        // 主菜单项
        const navItem = document.querySelector(`.nav-item[data-page="${pageId}"]`);
        if (navItem) {
            navItem.classList.add('active');
        }
    }
}

/** 读取侧栏子菜单项（仅 .nav-submenu 内，避免误匹配） */
function getNavSubmenuItems(navItem) {
    if (!navItem) return [];
    const submenu = navItem.querySelector('.nav-submenu');
    if (!submenu) return [];
    return Array.from(submenu.querySelectorAll('.nav-submenu-item'));
}

// 切换子菜单
function toggleSubmenu(menuId) {
    const sidebar = document.getElementById('main-sidebar');
    const navItem = document.querySelector(`.nav-item[data-page="${menuId}"]`);
    
    if (!navItem) return;
    
    const collapsed = sidebar && sidebar.classList.contains('collapsed');

    // 检查侧边栏是否折叠
    if (collapsed) {
        // 折叠状态下显示弹出菜单
        showSubmenuPopup(navItem, menuId);
        return;
    }

    // 展开状态下切换子菜单，并滚入视口以便看到子项
    const willExpand = !navItem.classList.contains('expanded');
    navItem.classList.toggle('expanded');
    if (willExpand) {
        requestAnimationFrame(() => {
            navItem.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
            const items = getNavSubmenuItems(navItem);
            const last = items[items.length - 1];
            if (last) {
                last.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
            }
        });
    }
}
window.toggleSubmenu = toggleSubmenu;

// 显示子菜单弹出框
function showSubmenuPopup(navItem, menuId) {
    const existingPopup = document.querySelector('.submenu-popup');
    if (existingPopup) {
        const sameMenu = existingPopup.dataset.menuId === menuId;
        existingPopup.remove();
        // 再次点击同一项：仅关闭；点击另一项：继续打开新菜单
        if (sameMenu) {
            return;
        }
    }

    const navItemContent = navItem.querySelector('.nav-item-content');
    const submenu = navItem.querySelector('.nav-submenu');
    
    if (!submenu) return;
    
    // 获取菜单位置
    const rect = navItemContent.getBoundingClientRect();
    
    // 创建弹出菜单
    const popup = document.createElement('div');
    popup.className = 'submenu-popup';
    popup.dataset.menuId = menuId;
    popup.style.position = 'fixed';
    popup.style.left = (rect.right + 8) + 'px';
    popup.style.top = rect.top + 'px';
    popup.style.zIndex = '1000';
    
    // 复制子菜单项到弹出菜单
    const submenuItems = submenu.querySelectorAll('.nav-submenu-item');
    submenuItems.forEach(item => {
        if (item.hidden || (typeof permissionAllowedForElement === 'function' && !permissionAllowedForElement(item))) return;
        const popupItem = document.createElement('div');
        popupItem.className = 'submenu-popup-item';
        popupItem.textContent = item.textContent.trim();
        
        // 检查是否是当前激活的页面
        const pageId = item.getAttribute('data-page');
        if (pageId && document.querySelector(`.nav-submenu-item[data-page="${pageId}"].active`)) {
            popupItem.classList.add('active');
        }
        
        popupItem.onclick = function(e) {
            e.stopPropagation();
            e.preventDefault();
            
            // 获取页面ID并切换
            const pageId = item.getAttribute('data-page');
            if (pageId) {
                switchPage(pageId);
            }
            
            // 关闭弹出菜单
            popup.remove();
            document.removeEventListener('click', closePopup);
        };
        popup.appendChild(popupItem);
    });
    
    document.body.appendChild(popup);
    
    // 点击外部关闭弹出菜单
    const closePopup = function(e) {
        if (!popup.contains(e.target) && !navItem.contains(e.target)) {
            popup.remove();
            document.removeEventListener('click', closePopup);
        }
    };
    
    // 延迟添加事件监听，避免立即触发
    setTimeout(() => {
        document.addEventListener('click', closePopup);
    }, 0);
}

// 初始化页面
async function initPage(pageId) {
    // 等待 i18n 就绪，避免快速刷新时翻译函数未初始化导致页面显示原始占位符 key
    if (window.i18nReady) await window.i18nReady;
    if (typeof stopExternalMcpPoll === 'function') {
        stopExternalMcpPoll();
    }
    switch(pageId) {
        case 'dashboard':
            if (typeof refreshDashboard === 'function') {
                refreshDashboard();
            }
            break;
        case 'chat':
            // 恢复对话列表折叠状态（从其他页返回时保持用户选择）
            initConversationSidebarState();
            if (typeof prefetchProjectsForChat === 'function') {
                prefetchProjectsForChat();
            }
            if (typeof refreshChatProjectSelector === 'function') {
                refreshChatProjectSelector();
            }
            break;
        case 'tool-guard':
            if (typeof loadToolGuardConfig === 'function') loadToolGuardConfig();
            break;
        case 'changelog':
            if (typeof loadChangelog === 'function') loadChangelog(false);
            break;
        case 'hitl':
            if (typeof refreshHitlActivePanel === 'function') {
                refreshHitlActivePanel();
            } else if (typeof refreshHitlPending === 'function') {
                refreshHitlPending();
            }
            break;
        case 'info-collect':
            // 信息收集页面
            if (typeof initInfoCollectPage === 'function') {
                initInfoCollectPage();
            }
            break;
        case 'asset-overview':
            if (typeof loadAssetOverview === 'function') loadAssetOverview();
            break;
        case 'asset-library':
            if (typeof loadAssets === 'function') loadAssets();
            break;
        case 'tasks':
            // 初始化任务管理页面
            if (typeof initTasksPage === 'function') {
                initTasksPage();
            }
            break;
        case 'mcp-monitor':
            // 初始化监控面板
            if (typeof refreshMonitorPanel === 'function') {
                refreshMonitorPanel();
            }
            if (typeof startMonitorPoll === 'function') {
                startMonitorPoll();
            }
            break;
        case 'mcp-management':
            // 初始化MCP管理
            const startLoadMcpTools = () => {
                // 加载工具列表（MCP工具配置已移到MCP管理页面）
                // 使用异步加载，避免阻塞页面渲染
                if (typeof loadToolsList === 'function') {
                    // 确保工具分页设置已初始化
                    if (typeof getToolsPageSize === 'function' && typeof toolsPagination !== 'undefined') {
                        toolsPagination.pageSize = getToolsPageSize();
                    }
                    // 延迟加载，让页面先渲染
                    setTimeout(() => {
                        loadToolsList(1, '').catch(err => {
                            console.error('加载工具列表失败:', err);
                        });
                    }, 100);
                }
            };
            const afterMcpConfigReady = () => {
                startLoadMcpTools();
                if (typeof loadExternalMCPs === 'function') {
                    loadExternalMCPs().catch(err => {
                        console.warn('加载外部MCP列表失败:', err);
                    });
                }
                if (typeof startExternalMcpPoll === 'function') {
                    startExternalMcpPoll();
                }
            };
            // 先拉取配置（含 tool_search 常驻列表），再加载工具与外部 MCP。
            // 仅有 mcp:read 的用户无需拉全量 /api/config（需 config:read）。
            const canLoadFullConfig = typeof hasPermission !== 'function' || hasPermission('config:read');
            if (typeof loadConfig === 'function' && canLoadFullConfig) {
                loadConfig(false, { silent: true })
                    .catch(err => {
                        console.warn('加载配置失败（将继续加载 MCP 列表）:', err);
                    })
                    .finally(afterMcpConfigReady);
            } else {
                afterMcpConfigReady();
            }
            break;
        case 'projects':
            if (typeof initProjectsPage === 'function') {
                initProjectsPage();
            }
            break;
        case 'vulnerabilities':
            // 初始化漏洞管理页面
            if (typeof initVulnerabilityPage === 'function') {
                initVulnerabilityPage();
            }
            break;
        case 'webshell':
            // 初始化 WebShell 管理页面
            if (typeof initWebshellPage === 'function') {
                initWebshellPage();
            }
            break;
        case 'chat-files':
            if (typeof initChatFilesPage === 'function') {
                initChatFilesPage();
            }
            break;
        case 'system-services':
            // 初始化运行服务页面
            if (typeof initSystemServicesPage === 'function') {
                initSystemServicesPage();
            }
            break;
        case 'settings':
            // 初始化设置页面（不需要加载工具列表）
            if (typeof loadConfig === 'function') {
                loadConfig(false);
            }
            break;
        case 'roles-management':
            // 初始化角色管理页面
            // 重置搜索UI（变量会在下次搜索时自动更新）
            const rolesSearchInput = document.getElementById('roles-search');
            if (rolesSearchInput) {
                rolesSearchInput.value = '';
            }
            const rolesSearchClear = document.getElementById('roles-search-clear');
            if (rolesSearchClear) {
                rolesSearchClear.style.display = 'none';
            }
            if (typeof loadRoles === 'function') {
                loadRoles().then(() => {
                    if (typeof renderRolesList === 'function') {
                        renderRolesList();
                    }
                });
            }
            break;
        case 'platform-rbac':
            if (typeof initPlatformRbacPage === 'function') {
                initPlatformRbacPage();
            }
            break;
        case 'workflows':
            if (typeof refreshWorkflows === 'function') {
                refreshWorkflows();
            }
            break;
        case 'skill-candidates':
            // 从实践项目提炼的 Skill（P0-2.2）
            if (typeof refreshSkillCandidates === 'function') {
                refreshSkillCandidates();
            }
            break;
        case 'skills-monitor':
            // 初始化Skills状态监控页面
            if (typeof loadSkillsMonitor === 'function') {
                loadSkillsMonitor();
            }
            break;
        case 'skills-management':
            // 初始化Skills管理页面
            // 重置搜索UI（变量会在下次搜索时自动更新）
            const skillsSearchInput = document.getElementById('skills-search');
            if (skillsSearchInput) {
                skillsSearchInput.value = '';
            }
            const skillsSearchClear = document.getElementById('skills-search-clear');
            if (skillsSearchClear) {
                skillsSearchClear.style.display = 'none';
            }
            if (typeof initSkillsPagination === 'function') {
                initSkillsPagination();
            }
            if (typeof loadSkills === 'function') {
                loadSkills();
            }
            break;
        case 'agents-management':
            if (typeof loadMarkdownAgents === 'function') {
                loadMarkdownAgents();
            }
            break;
        case 'c2-listeners':
        case 'c2-sessions':
        case 'c2-tasks':
        case 'c2-payloads':
        case 'c2-events':
        case 'c2-profiles':
            window.currentPageId = pageId;
            if (window.C2 && typeof window.C2.init === 'function') {
                window.C2.init();
            }
            break;
    }
    
    // 清理其他页面的定时器
    if (pageId !== 'tasks' && typeof cleanupTasksPage === 'function') {
        cleanupTasksPage();
    }
}

/* ===== 控制台页面：在当前外壳内以 iframe 渲染 =====
 * 背景：侧栏「控制台」分组原先直接 window.location.href 跳转，会把 findu 外壳整页换成
 * 控制台的 Next 应用（导航栏、主题、侧栏全部重建）。这里改为按需创建一个内嵌 iframe
 * 页，点击只在内容区换页，外壳与导航保持不动；?embed=1 让控制台只渲染内容、不渲染
 * 它自己的侧栏与顶栏。
 */
var CONSOLE_PAGE_URLS = {
    'console-vuln-kb':     '/function/vuln-kb',
    'console-traffic':     '/function/traffic',
    'console-reports':     '/system/reports',
    'console-agents':      '/system/agents',
    'console-tools':       '/system/tools',
    'console-llm-records': '/function/llm-records',
    'console-rbac':        '/system/rbac',
    'console-logs':        '/system/logs',
    'console-notify':      '/system/notify',
    'console-changelog':   '/system/changelog'
};

// ensureConsolePage 幂等创建 page-<id> 容器（懒加载：只有第一次切过去才请求 iframe）。
function ensureConsolePage(pageId) {
    var url = CONSOLE_PAGE_URLS[pageId];
    if (!url) return null;
    var existing = document.getElementById('page-' + pageId);
    if (existing) return existing;
    var area = document.querySelector('.content-area');
    if (!area) return null;
    var el = document.createElement('div');
    el.className = 'page console-embed-page';
    el.id = 'page-' + pageId;
    var frame = document.createElement('iframe');
    frame.className = 'console-embed-frame';
    frame.setAttribute('src', url + '?embed=1');
    frame.setAttribute('title', pageId);
    el.appendChild(frame);
    area.appendChild(el);
    return el;
}

function switchConsolePage(pageId) {
    if (!CONSOLE_PAGE_URLS[pageId]) return;
    if (!ensureConsolePage(pageId)) return;
    switchPage(pageId);
}
window.switchConsolePage = switchConsolePage;

// 页面加载完成后初始化路由
document.addEventListener('DOMContentLoaded', function() {
    initRouter();
    document.documentElement.classList.remove('initial-route-pending');
    initSidebarState();
    
    // 监听hash变化
    window.addEventListener('hashchange', function() {
        const hash = window.location.hash.slice(1);
        // 处理带参数的hash（如 chat?conversation=xxx）
        const hashParts = hash.split('?');
        let pageId = hashParts[0];
        
        if (pageId === 'c2') pageId = 'c2-listeners';
        if (pageId && pageId.indexOf('console-') === 0 && window.switchConsolePage) {
            window.switchConsolePage(pageId);
            return;
        }
        if (pageId && ['dashboard', 'chat', 'hitl', 'tool-guard', 'asset-overview', 'asset-library', 'info-collect', 'projects', 'tasks', 'workflows', 'vulnerabilities', 'webshell', 'chat-files', 'mcp-monitor', 'mcp-management', 'knowledge-management', 'knowledge-retrieval-logs', 'roles-management', 'platform-rbac', 'skills-monitor', 'skill-candidates', 'skills-management', 'agents-management', 'settings', 'system-services', 'c2-listeners', 'c2-sessions', 'c2-tasks', 'c2-payloads', 'c2-events', 'c2-profiles'].includes(pageId)) {
            switchPage(pageId);
            if (pageId === 'chat') {
                scheduleChatConversationFromHash(0);
            }
        }
    });
});

// 切换侧边栏折叠/展开
function toggleSidebar() {
    const sidebar = document.getElementById('main-sidebar');
    if (sidebar) {
        sidebar.classList.toggle('collapsed');
        // 保存折叠状态到localStorage
        const isCollapsed = sidebar.classList.contains('collapsed');
        localStorage.setItem('sidebarCollapsed', isCollapsed ? 'true' : 'false');
    }
}
window.toggleSidebar = toggleSidebar;

// 初始化侧边栏状态
function initSidebarState() {
    const sidebar = document.getElementById('main-sidebar');
    if (sidebar) {
        const savedState = localStorage.getItem('sidebarCollapsed');
        if (savedState === 'true') {
            sidebar.classList.add('collapsed');
        }
    }
    initConversationSidebarState();
}

// 切换对话页左侧列表折叠/展开
function toggleConversationSidebar() {
    const sidebar = document.getElementById('conversation-sidebar');
    if (sidebar) {
        sidebar.classList.toggle('collapsed');
        const isCollapsed = sidebar.classList.contains('collapsed');
        localStorage.setItem('conversationSidebarCollapsed', isCollapsed ? 'true' : 'false');
    }
}
window.toggleConversationSidebar = toggleConversationSidebar;

// 恢复对话列表折叠状态（进入对话页时生效）
function initConversationSidebarState() {
    const sidebar = document.getElementById('conversation-sidebar');
    if (sidebar) {
        const savedState = localStorage.getItem('conversationSidebarCollapsed');
        if (savedState === 'true') {
            sidebar.classList.add('collapsed');
        } else {
            sidebar.classList.remove('collapsed');
        }
    }
}

// 导出函数供其他脚本使用（与上方尽早绑定保持一致，便于外部脚本探测）
window.currentPage = function() { return currentPage; };

/* ===== 控制台能力分组（幂等兜底） ===== */
/*
 * 背景：控制台的 10 项独占能力在侧边栏曾以平铺 nav-item 呈现（data-page="ext-*"）。
 * 模板已改为 3 个可折叠分组；但 gin 的 LoadHTMLGlob 只在进程启动时解析模板，
 * 所以尚未重启的实例仍然吐旧标记。此处在运行时把旧平铺项就地升级成分组，
 * 已分组时（无 ext-* 项）直接返回，重复执行无副作用。
 */
(function () {
    var CONSOLE_GROUPS = [
        {
            id: 'console-delivery',
            key: 'consoleGroups.delivery',
            fallback: '作业与交付',
            icon: '<circle cx="12" cy="12" r="9"></circle><circle cx="12" cy="12" r="3"></circle><path d="M12 1v4M12 19v4M1 12h4M19 12h4"></path>',
            items: [
                { page: 'console-vuln-kb',   key: 'nav.consoleVulnKb',   fallback: '漏洞库',    url: '/function/vuln-kb' },
                { page: 'console-traffic',   key: 'nav.consoleTraffic',  fallback: '流量录制',  url: '/function/traffic' },
                { page: 'console-reports',   key: 'nav.consoleReports',  fallback: '报告',      url: '/system/reports' }
            ]
        },
        {
            id: 'console-agent',
            key: 'consoleGroups.agent',
            fallback: '智能体与工具',
            icon: '<rect x="5" y="5" width="14" height="14" rx="2"></rect><rect x="9" y="9" width="6" height="6"></rect><path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3"></path>',
            items: [
                { page: 'console-agents',      key: 'nav.consoleAgents',      fallback: '角色与 Agent', url: '/system/agents' },
                { page: 'console-tools',       key: 'nav.consoleTools',       fallback: '工具',         url: '/system/tools' },
                { page: 'console-llm-records', key: 'nav.consoleLlmRecords',  fallback: '模型录制',     url: '/function/llm-records' }
            ]
        },
        {
            id: 'console-governance',
            key: 'consoleGroups.governance',
            fallback: '平台治理',
            icon: '<path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3"></path><path d="M1 14h6M9 8h6M17 16h6"></path>',
            items: [
                { page: 'console-rbac',      key: 'nav.consoleRbac',      fallback: '权限与角色', url: '/system/rbac' },
                { page: 'console-logs',      key: 'nav.consoleLogs',      fallback: '日志',       url: '/system/logs' },
                { page: 'console-notify',    key: 'nav.consoleNotify',    fallback: '通知推送',   url: '/system/notify' },
                { page: 'console-changelog', key: 'nav.consoleChangelog', fallback: '版本记录',   url: '/system/changelog' }
            ]
        }
    ];

    var SVG_NS = 'http://www.w3.org/2000/svg';

    function makeIcon(pathsHtml) {
        var svg = document.createElementNS(SVG_NS, 'svg');
        svg.setAttribute('width', '20');
        svg.setAttribute('height', '20');
        svg.setAttribute('viewBox', '0 0 24 24');
        svg.setAttribute('fill', 'none');
        svg.setAttribute('stroke', 'currentColor');
        svg.setAttribute('stroke-width', '2');
        svg.setAttribute('stroke-linecap', 'round');
        svg.setAttribute('stroke-linejoin', 'round');
        svg.setAttribute('aria-hidden', 'true');
        svg.innerHTML = pathsHtml;
        return svg;
    }

    function makeArrow() {
        var svg = document.createElementNS(SVG_NS, 'svg');
        svg.setAttribute('class', 'submenu-arrow');
        svg.setAttribute('width', '16');
        svg.setAttribute('height', '16');
        svg.setAttribute('viewBox', '0 0 24 24');
        svg.setAttribute('fill', 'none');
        var path = document.createElementNS(SVG_NS, 'path');
        path.setAttribute('d', 'm9 18 6-6-6-6');
        path.setAttribute('stroke', 'currentColor');
        path.setAttribute('stroke-width', '2');
        path.setAttribute('stroke-linecap', 'round');
        path.setAttribute('stroke-linejoin', 'round');
        svg.appendChild(path);
        return svg;
    }

    function buildGroup(g) {
        var navItem = document.createElement('div');
        navItem.className = 'nav-item nav-item-has-submenu';
        navItem.setAttribute('data-page', g.id);

        var content = document.createElement('div');
        content.className = 'nav-item-content';
        content.setAttribute('data-title', g.fallback);
        content.setAttribute('data-i18n', g.key);
        content.setAttribute('data-i18n-attr', 'data-title');
        content.setAttribute('data-i18n-skip-text', 'true');
        content.appendChild(makeIcon(g.icon));

        var label = document.createElement('span');
        label.setAttribute('data-i18n', g.key);
        label.textContent = g.fallback;
        content.appendChild(label);
        content.appendChild(makeArrow());

        content.addEventListener('click', function () {
            if (window.toggleSubmenu) window.toggleSubmenu(g.id);
        });

        var submenu = document.createElement('div');
        submenu.className = 'nav-submenu';
        g.items.forEach(function (it) {
            var d = document.createElement('div');
            d.className = 'nav-submenu-item';
            d.setAttribute('data-page', it.page);
            d.setAttribute('data-i18n', it.key);
            d.textContent = it.fallback;
            d.addEventListener('click', function () { window.switchConsolePage(it.page); });
            submenu.appendChild(d);
        });

        navItem.appendChild(content);
        navItem.appendChild(submenu);
        return navItem;
    }

    function upgradeConsoleNav() {
        var nav = document.querySelector('.main-sidebar-nav');
        if (!nav) return;
        var flat = nav.querySelectorAll('.nav-item[data-page^="ext-"]');
        if (!flat.length) return;                       // 已是分组形态，直接退出

        // 找到「控制台能力」标签；找不到就顺延到平台管理之前
        var anchorLabel = null;
        var labels = nav.querySelectorAll('.nav-section-label');
        for (var i = 0; i < labels.length; i++) {
            var sp = labels[i].querySelector('span');
            var txt = sp ? (sp.textContent || '').trim() : '';
            if (txt === '控制台能力' || txt === '控制台' || txt === 'Console') {
                anchorLabel = labels[i];
                // 与改版后的模板对齐：标签统一为「控制台」并接入 i18n
                if (sp) {
                    sp.setAttribute('data-i18n', 'navGroups.console');
                    sp.textContent = '控制台';
                }
                break;
            }
        }

        var refNode = anchorLabel || (flat.length ? flat[0] : null);
        if (!refNode) return;

        var parent = refNode.parentNode;
        var insertAfter = refNode;
        CONSOLE_GROUPS.forEach(function (g) {
            var el = buildGroup(g);
            parent.insertBefore(el, insertAfter.nextSibling);
            insertAfter = el;
        });

        // 移除旧平铺项
        flat.forEach(function (el) { el.parentNode && el.parentNode.removeChild(el); });

        // 新节点补翻译
        try {
            if (window.i18nReady && window.applyTranslations) {
                window.i18nReady.then(function () { window.applyTranslations(parent); });
            } else if (window.applyTranslations) {
                window.applyTranslations(parent);
            }
        } catch (e) { /* 语言包未就绪时保留中文兜底文案 */ }
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', upgradeConsoleNav);
    } else {
        upgradeConsoleNav();
    }
    window.upgradeConsoleNav = upgradeConsoleNav;
})();

