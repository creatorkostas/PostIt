        // ─── Runtime Detection & Backend Abstraction ──────────────────────────
        // Auto-detect Wails vs web-browser environment.  In Wails mode the
        // bound Go methods are called directly via window.go.main.App.*; in
        // web mode the same operations go through fetch('/api/...').
        const _isWails = typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App && true;

        /**
         * callApi – unified backend call that works in both Wails and web mode.
         *
         * @param {string}  endpoint  – e.g. "/api/requests"
         * @param {string}  [method]  – "GET" (default) or "POST"
         * @param {object}  [body]    – JS object to send (ignored for GET)
         * @returns {Promise<any>}    – parsed response (same shape in both modes)
         */
        async function callApi(endpoint, method, body) {
            if (_isWails) {
                return await wailsCall(endpoint, method || 'GET', body);
            }
            const opts = { method: method || 'GET', headers: { 'Content-Type': 'application/json' } };
            if (body && method !== 'GET') opts.body = JSON.stringify(body);
            const resp = await fetch(endpoint, opts);
            if (!resp.ok) {
                const errText = await resp.text().catch(() => resp.statusText);
                throw new Error(errText || `HTTP ${resp.status}`);
            }
            const text = await resp.text();
            return text ? JSON.parse(text) : null;
        }

        // Endpoint → Wails method-name mapping.  The key is "METHOD /path".
        const _wailsRouting = {
            'GET /api/requests':             'GetRequests',
            'POST /api/requests/new':        'NewRequest',
            'POST /api/requests/duplicate':  'DuplicateRequest',
            'POST /api/requests/update':     'UpdateRequest',
            'POST /api/requests/delete':     'DeleteRequest',
            'POST /api/requests/reorder':    'ReorderRequests',
            'GET /api/variables':            'GetVariables',
            'POST /api/variables':           'SaveVariables',
            'GET /api/history':              'GetHistory',
            'POST /api/history/clear':       'ClearHistory',
            'POST /api/history/delete':      null, // handled specially
            'POST /api/send':                'SendRequest',
            'POST /api/hammer':              'HammerRequest',
            'POST /api/sql':                 'SQLRequest',
            'POST /api/schema/save':         'SaveSchema',
            'POST /api/mock/save':           'SaveMockResponse',
            'POST /api/mock/delete':         'DeleteMock',
            'GET /api/mock/stats':           'GetMockStats',
            'POST /api/fuzz':                'FuzzRequest',
            'POST /api/runner/run':          'RunRunner',
            'POST /api/graphql/introspection':'GraphQLIntrospection',
            'POST /api/import/curl':         'ImportCurl',
            'POST /api/import/openapi':      'ImportOpenAPI',
            'POST /api/import/postman':      'ImportPostman',
            'GET /api/workflows':            'GetWorkflows',
            'POST /api/workflows':           'SaveWorkflows',
            'POST /api/workflows/run':       'RunWorkflow',
            'GET /api/environments':         'GetEnvironments',
            'POST /api/environments':        'SaveEnvironments',
            'GET /api/environments/active':  'GetActiveEnv',
            'POST /api/environments/active': 'SetActiveEnv',
            'POST /api/vault/unlock':        'UnlockVault',
            'POST /api/vault/encrypt':       'VaultEncrypt',
            'GET /api/vault/status':         'GetVaultStatus',
            'POST /api/ws/connect':          'WSConnect',
            'POST /api/ws/send':             'WSSend',
            'GET /api/ws/messages':          'WSGetMessages',
            'POST /api/ws/close':            'WSClose',
            'POST /api/proxy/start':         'StartProxy',
            'POST /api/proxy/stop':          'StopProxy',
            'GET /api/proxy/status':         'GetProxyStatus',
            'GET /api/export':               'ExportPostman',
            'POST /api/import/curl':         'ImportCurl',
        };

        /** Dispatch a call to the Wails runtime. */
        async function wailsCall(endpoint, method, body) {
            const key = method + ' ' + endpoint;
            const fnName = _wailsRouting[key];

            // Special cases that need custom argument shaping
            if (key === 'POST /api/history/delete') {
                // Web sends {timestamp: ts} but Wails expects raw ts value
                return await window.go.main.App.DeleteHistory(body.timestamp);
            }
            if (key === 'GET /api/export') {
                return await window.go.main.App.ExportPostman(window._exportPath || "");
            }

            if (!fnName) {
                throw new Error(`[Wails] No mapping for ${key}`);
            }
            const fn = window.go.main.App[fnName];
            if (!fn) {
                throw new Error(`[Wails] Method "${fnName}" not found on App`);
            }
            if (body) return await fn(body);
            return await fn();
        }

        let flatRequests = [];
        let currentRequest = null;
        let responseCache = {}; 
        let baselineCache = {};
        let menuTarget = null;
        let activeSidebar = 'collections';
        let workflows = [];
        let currentWorkflow = null;

        // Toast System
        function showToast(message, type = 'info') {
            const container = document.getElementById('toast-container');
            const toast = document.createElement('div');
            toast.className = 'toast';
            let icon = 'info';
            if (type === 'success') icon = 'check-circle';
            if (type === 'error') icon = 'alert-circle';
            
            toast.innerHTML = `<i data-lucide="${icon}" style="width: 16px; height: 16px;"></i> <span>${message}</span>`;
            container.appendChild(toast);
            lucide.createIcons();

            setTimeout(() => {
                toast.style.opacity = '0';
                toast.style.transform = 'translateX(100%)';
                setTimeout(() => toast.remove(), 300);
            }, 3000);
        }

        let lastFilteredResult = null;

        function copyResponseToClipboard() {
            let text = "";
            const filterVal = document.getElementById('json-filter')?.value;
            
            if (filterVal && lastFilteredResult !== null) {
                text = JSON.stringify(lastFilteredResult, null, 4);
            } else if (currentRequest && responseCache[currentRequest.path]) {
                text = responseCache[currentRequest.path].body;
            } else {
                text = document.getElementById('response-body').textContent;
            }
            
            navigator.clipboard.writeText(text).then(() => {
                showToast('Copied to clipboard!', 'success');
            }).catch(err => {
                showToast('Failed to copy', 'error');
            });
        }

        function expandAllJson() {
            document.querySelectorAll('#response-body details').forEach(el => el.open = true);
        }

        function collapseAllJson() {
            document.querySelectorAll('#response-body details').forEach(el => el.open = false);
        }

        function copyJsonPath(path) {
            navigator.clipboard.writeText(path).then(() => {
                showToast(`Path copied: ${path}`, 'success');
            });
        }

        function syntaxHighlight(json) {
            if (!json) return "";
            let str = typeof json != 'string' ? JSON.stringify(json, undefined, 4) : json;
            str = str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
            return str.replace(/("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)/g, function (match) {
                var cls = 'json-number';
                if (/^"/.test(match)) {
                    if (/:$/.test(match)) cls = 'json-key';
                    else cls = 'json-string';
                } else if (/true|false/.test(match)) cls = 'json-boolean';
                else if (/null/.test(match)) cls = 'json-null';
                return '<span class="' + cls + '">' + match + '</span>';
            });
        }

        function escapeHtml(text) {
            if (typeof text !== 'string') return text;
            return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
        }

        function renderJsonCollapsible(data, path = '') {
            if (data === null) return '<span class="json-null">null</span>';
            const type = typeof data;
            if (type === 'number') return `<span class="json-number">${data}</span>`;
            if (type === 'boolean') return `<span class="json-boolean">${data}</span>`;
            if (type === 'string') return `<span class="json-string">"${escapeHtml(data)}"</span>`;
            
            if (Array.isArray(data)) {
                if (data.length === 0) return '<span class="json-bracket">[]</span>';
                let html = '<details open class="json-details">';
                html += `<summary class="json-summary"><span class="json-bracket">[</span><span class="json-summary-preview"> ${data.length} items </span></summary>`;
                html += '<div class="json-collapsible-content">';
                html += data.map((item, i) => {
                    const currentPath = `${path}[${i}]`;
                    return `<div class="json-line">${renderJsonCollapsible(item, currentPath)}${i < data.length - 1 ? '<span class="json-comma">,</span>' : ''}</div>`;
                }).join('');
                html += '</div><span class="json-bracket">]</span></details>';
                return html;
            }
            
            if (type === 'object') {
                const keys = Object.keys(data);
                if (keys.length === 0) return '<span class="json-bracket">{}</span>';
                let html = '<details open class="json-details">';
                html += `<summary class="json-summary"><span class="json-bracket">{</span><span class="json-summary-preview"> ${keys.length} keys </span></summary>`;
                html += '<div class="json-collapsible-content">';
                html += keys.map((key, i) => {
                    const currentPath = path ? `${path}.${key}` : key;
                    return `<div class="json-line"><span class="json-key" onclick="copyJsonPath('${currentPath}')" title="Click to copy path: ${currentPath}">"${escapeHtml(key)}"</span>: ${renderJsonCollapsible(data[key], currentPath)}${i < keys.length - 1 ? '<span class="json-comma">,</span>' : ''}</div>`;
                }).join('');
                html += '</div><span class="json-bracket">}</span></details>';
                return html;
            }
            return escapeHtml(String(data));
        }

        function updateRequestHighlight() {
            const textarea = document.getElementById('req-body-input');
            const highlight = document.getElementById('req-body-highlight');
            const val = textarea.value;
            try {
                if (val.trim().startsWith('{') || val.trim().startsWith('[')) { highlight.innerHTML = syntaxHighlight(val) + "\n"; } 
                else { highlight.textContent = val + "\n"; }
            } catch (e) { highlight.textContent = val + "\n"; }
        }

        function syncEditorScroll() {
            const textarea = document.getElementById('req-body-input');
            const highlight = document.getElementById('req-body-highlight');
            highlight.scrollTop = textarea.scrollTop;
            highlight.scrollLeft = textarea.scrollLeft;
        }

        document.getElementById('req-body-input').addEventListener('keydown', function(e) {
            if (e.key === 'Tab') {
                e.preventDefault();
                const start = this.selectionStart; const end = this.selectionEnd;
                this.value = this.value.substring(0, start) + "    " + this.value.substring(end);
                this.selectionStart = this.selectionEnd = start + 4;
                updateRequestHighlight();
            }
        });

        function decodeJWT(token) {
            try {
                const parts = token.split('.');
                if (parts.length !== 3) return null;
                const header = JSON.parse(atob(parts[0].replace(/-/g, '+').replace(/_/g, '/')));
                const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')));
                return { header, payload };
            } catch (e) { return null; }
        }

        function renderJWT(body, headers) {
            const container = document.getElementById('jwt-container');
            container.innerHTML = '';
            let tokens = [];
            const jwtRegex = /[a-zA-Z0-9\-_]+\.[a-zA-Z0-9\-_]+\.[a-zA-Z0-9\-_]+/g;
            const bodyMatches = body.match(jwtRegex) || [];
            tokens = [...new Set(bodyMatches)];
            Object.values(headers).forEach(val => {
                const hMatches = val.join(' ').match(jwtRegex) || [];
                tokens = [...new Set([...tokens, ...hMatches])];
            });
            const validTokens = tokens.map(t => ({ raw: t, decoded: decodeJWT(t) })).filter(t => t.decoded !== null);
            if (validTokens.length === 0) {
                container.innerHTML = '<div style="color: var(--text-secondary); font-style: italic; font-size: 12px;">No valid JWT found in response.</div>';
                return;
            }
            validTokens.forEach((token, index) => {
                const div = document.createElement('div');
                div.innerHTML = `
                    <div style="margin-bottom: 32px;">
                        <div style="font-size: 11px; color: var(--accent); font-weight: 800; margin-bottom: 12px;">TOKEN #${index + 1}</div>
                        <div class="jwt-section"><div class="jwt-label">Header</div><pre class="jwt-box">${syntaxHighlight(token.decoded.header)}</pre></div>
                        <div class="jwt-section"><div class="jwt-label">Payload</div><pre class="jwt-box">${syntaxHighlight(token.decoded.payload)}</pre></div>
                    </div>
                `;
                container.appendChild(div);
            });
        }

        function beautifyRequestJson() {
            const el = document.getElementById('req-body-input');
            try { const obj = JSON.parse(el.value); el.value = JSON.stringify(obj, null, 4); updateRequestHighlight(); } catch (e) { showToast("Invalid JSON", "error"); }
        }

        function beautifyResponseJson() {
            if (!currentRequest) return;
            const cached = responseCache[currentRequest.path];
            if (!cached || !cached.body) return;
            const el = document.getElementById('response-body');
            try { 
                const obj = JSON.parse(cached.body); 
                el.innerHTML = renderJsonCollapsible(obj); 
            } catch (e) { showToast("Invalid JSON", "error"); }
        }

        async function init() {
            try {
                const data = await callApi('/api/requests');
                flatRequests = data.flat;
                const treeContainer = document.getElementById('collection-tree');
                treeContainer.innerHTML = '';
                if (data.collection && data.collection.item) renderTree(treeContainer, data.collection.item, "");
                
                const visited = new Set();
                const mark = (items, prefix) => {
                    if (!items) return;
                    items.forEach(item => {
                        const path = prefix ? `${prefix} > ${item.name}` : item.name;
                        if (item.request) visited.add(path);
                        if (item.item) mark(item.item, path);
                    });
                };
                mark(data.collection.item, "");
                const custom = flatRequests.filter(r => !visited.has(r.path));
                if (custom.length > 0) {
                    const folder = document.createElement('div');
                    folder.className = 'folder'; folder.innerHTML = `<i data-lucide="chevron-right" class="chevron"></i> <i data-lucide="folder" class="folder-icon"></i> <span>Custom Requests</span>`;
                    const content = document.createElement('div');
                    content.className = 'folder-content';
                    folder.onclick = () => { folder.classList.toggle('expanded'); content.classList.toggle('show'); };
                    custom.forEach(req => content.appendChild(createRequestNode(req.path, req.request.method, req.path.split(' > ').pop())));
                    treeContainer.appendChild(folder); treeContainer.appendChild(content);
                }
                lucide.createIcons();
            } catch (e) { console.error(e); }
        }

        function switchSidebar(tab) {
            activeSidebar = tab;
            document.querySelectorAll('#sidebar-tabs .tab').forEach(t => t.classList.toggle('active', t.id === `tab-${tab}`));
            
            document.getElementById('collection-tree').style.display = (tab === 'collections' || tab === 'workflows') ? 'block' : 'none';
            document.getElementById('history-list').style.display = tab === 'history' ? 'block' : 'none';
            document.getElementById('history-actions').style.display = tab === 'history' ? 'block' : 'none';
            document.getElementById('ws-container').style.display = tab === 'ws' ? 'flex' : 'none';
            document.getElementById('mock-dashboard').style.display = tab === 'mock' ? 'block' : 'none';
            
            document.getElementById('sidebar-title').textContent = tab.toUpperCase();
            
            document.getElementById('workflow-view').style.display = tab === 'workflows' ? 'flex' : 'none';
            document.getElementById('editor-wrapper').style.display = (tab === 'workflows' || tab === 'ws' || tab === 'mock') ? 'none' : 'flex';
            document.getElementById('resizer-response').style.display = (tab === 'workflows' || tab === 'ws' || tab === 'mock') ? 'none' : 'block';
            document.getElementById('response-container').style.display = (tab === 'workflows' || tab === 'ws' || tab === 'mock') ? 'none' : 'flex';

            if (tab === 'history') renderHistory();
            if (tab === 'workflows') renderWorkflows();
            if (tab === 'collections') init();
            if (tab === 'mock') renderMockStats();
            if (tab === 'ws') renderWS();
            lucide.createIcons();
        }

        // New Feature Functions
        function handleImportFile(input, targetId) {
            const file = input.files[0];
            if (!file) return;
            const reader = new FileReader();
            reader.onload = (e) => {
                document.getElementById(targetId).value = e.target.result;
            };
            reader.readAsText(file);
        }

        function switchImportTab(tab) {
            document.getElementById('tab-import-curl').classList.toggle('active', tab === 'curl');
            document.getElementById('tab-import-openapi').classList.toggle('active', tab === 'openapi');
            document.getElementById('tab-import-postman').classList.toggle('active', tab === 'postman');
            document.getElementById('import-curl-pane').style.display = tab === 'curl' ? 'block' : 'none';
            document.getElementById('import-openapi-pane').style.display = tab === 'openapi' ? 'block' : 'none';
            document.getElementById('import-postman-pane').style.display = tab === 'postman' ? 'block' : 'none';
        }

        async function importOpenAPI() {
            const spec = document.getElementById('openapi-input').value;
            try {
                const data = await callApi('/api/import/openapi', 'POST', { json: spec });
                showToast(`Imported ${data.count} requests!`, "success");
                closeModal('import-modal');
                init();
            } catch (e) { showToast("Import failed: " + e.message, "error"); }
        }

        async function importPostman() {
            const json = document.getElementById('postman-input').value;
            try {
                const data = await callApi('/api/import/postman', 'POST', { json });
                showToast(`Imported ${data.count} requests!`, "success");
                closeModal('import-modal');
                init();
            } catch (e) { showToast("Import failed: " + e.message, "error"); }
        }

        async function exportHAR() {
            if (_isWails) {
                // In Wails mode, fetch history data and trigger a save dialog
                try {
                    const history = await callApi('/api/history');
                    const blob = new Blob([JSON.stringify(history, null, 2)], { type: 'application/json' });
                    const url = URL.createObjectURL(blob);
                    const a = document.createElement('a');
                    a.href = url; a.download = 'history.json'; a.click();
                    URL.revokeObjectURL(url);
                } catch (e) { showToast("Export failed: " + e.message, "error"); }
                return;
            }
            window.location.href = '/api/history/export';
        }

        async function renderMockStats() {
            const list = document.getElementById('mock-stats-list');
            list.innerHTML = 'Loading...';
            try {
                const stats = await callApi('/api/mock/stats');
                list.innerHTML = '';
                Object.entries(stats).forEach(([key, stat]) => {
                    const div = document.createElement('div');
                    div.style = 'background: var(--bg-input); padding: 12px; border-radius: 8px; margin-bottom: 8px; border: 1px solid var(--border-color);';
                    div.innerHTML = `
                        <div style="font-weight: 600; color: var(--text-main); font-size: 12px; margin-bottom: 4px;">${key}</div>
                        <div style="display:flex; justify-content: space-between; font-size: 11px;">
                            <span style="color: var(--accent);">Hits: ${stat.hits}</span>
                            <span style="color: var(--text-secondary);">Last: ${new Date(stat.lastAccess).toLocaleTimeString()}</span>
                        </div>
                    `;
                    list.appendChild(div);
                });
                if (Object.keys(stats).length === 0) list.innerHTML = '<div style="color:var(--text-secondary); font-style:italic;">No mock activity recorded.</div>';
            } catch (e) { list.innerHTML = 'Error loading stats'; }
        }

        let wsPoll = null;
        function renderWS() {
            const area = document.getElementById('ws-message-area');
            area.style.display = 'flex';
            if (!wsPoll) wsPoll = setInterval(updateWSLog, 2000);
            updateWSLog();
        }

        async function connectWS() {
            const url = document.getElementById('ws-url').value;
            try { await callApi('/api/ws/connect', 'POST', { url });
                document.getElementById('ws-connect-btn').style.display = 'none';
                document.getElementById('ws-disconnect-btn').style.display = 'inline-block';
                showToast("WebSocket Connected", "success");
            } catch (e) { showToast("Connection Failed: " + e.message, "error"); }
        }

        async function sendWS() {
            const msg = document.getElementById('ws-input').value;
            try { await callApi('/api/ws/send', 'POST', { message: msg }); document.getElementById('ws-input').value = ''; updateWSLog(); } catch (e) { showToast("Send failed: " + e.message, "error"); }
        }

        async function updateWSLog() {
            if (activeSidebar !== 'ws') return;
            const msgs = await callApi('/api/ws/messages');
            const log = document.getElementById('ws-log');
            log.innerHTML = msgs.map(m => {
                let color = m.type === 'sent' ? '#4ade80' : (m.type === 'received' ? '#60a5fa' : '#f87171');
                return `<div style="margin-bottom:4px;"><span style="color:var(--text-secondary)">[${new Date(m.timestamp).toLocaleTimeString()}]</span> <span style="color:${color}; font-weight:700;">${m.type.toUpperCase()}:</span> ${m.content}</div>`;
            }).join('');
            log.scrollTop = log.scrollHeight;
        }

        async function closeWS() {
            try { await callApi('/api/ws/close', 'POST'); } catch (e) { /* ignore */ }
            document.getElementById('ws-connect-btn').style.display = 'inline-block';
            document.getElementById('ws-disconnect-btn').style.display = 'none';
        }

        let historyCache = [];

        async function renderHistory() {
            const list = document.getElementById('history-list');
            list.innerHTML = '<div style="padding: 24px; color: var(--text-secondary); text-align: center;">Loading history...</div>';
            try {
                const history = await callApi('/api/history');
                historyCache = history;
                list.innerHTML = history.length === 0 ? '<div style="padding: 24px; color: var(--text-secondary); font-style: italic; text-align: center;">No history.</div>' : '';
                history.slice().reverse().forEach((item, index) => {
                    const realIndex = history.length - 1 - index;
                    const div = document.createElement('div');
                    div.className = 'request-item history-item';
                    let color = item.statusCode >= 400 || item.statusCode === 0 ? 'var(--method-delete)' : (item.statusCode >= 300 ? 'var(--method-post)' : 'var(--method-get)');
                    div.innerHTML = `
                        <div class="history-meta"><span class="method-tag method-${item.method}">${item.method}</span><span style="color: ${color}; font-weight: 700;">${item.statusCode || 'ERR'}</span></div>
                        <div class="history-path">${item.path}</div>
                        <div class="history-url">${item.url}</div>
                        <div class="history-footer">${new Date(item.timestamp).toLocaleTimeString()} • ${item.duration}ms</div>
                        <button class="sidebar-action-btn" style="position: absolute; right: 12px; top: 12px; padding: 4px;" onclick="event.stopPropagation(); deleteHistoryItem('${item.timestamp}')"><i data-lucide="x" style="width: 12px; height: 12px;"></i></button>
                    `;
                    div.onclick = () => selectHistoryItem(realIndex);
                    list.appendChild(div);
                });
                lucide.createIcons();
            } catch (e) { list.innerHTML = '<div style="color:var(--method-delete); padding: 24px;">Error loading history</div>'; }
        }

        function selectHistoryItem(index) {
            const item = historyCache[index];
            if (!item) return;
            
            // First, try to find and select the request in the tree/editor
            selectRequest(item.path);
            
            // Then override the response with the saved one
            const rBody = document.getElementById('response-body');
            const rHeaders = document.getElementById('resp-pane-headers');
            const meta = document.getElementById('response-meta');
            
            let color = (item.statusCode >= 400 || item.statusCode === 0) ? 'var(--method-delete)' : 'var(--method-get)';
            let mHtmlText = `<span style="color:${color};font-weight:700">${item.statusCode} ${item.statusText}</span> &bull; ${item.duration}ms (from history)`;
            
            let body = item.responseBody || '';
            const h = item.responseHeaders || {};
            const ct = (h['Content-Type'] || h['content-type'] || [])[0] || '';
            let isJson = ct.includes('json');
            
            let displayBody = body;
            if (isJson && body) {
                try {
                    displayBody = JSON.parse(body);
                } catch (e) {}
            }

            rHeaders.innerHTML = Object.entries(h).map(([k, v]) => `<div><span style="color:var(--accent); font-weight:600;">${k}:</span> ${v.join(', ')}</div>`).join('');
            meta.innerHTML = `<div style="display:flex;gap:12px;align-items:center"><span>${mHtmlText}</span><button class="sidebar-action-btn" onclick="setBaseline()">Baseline</button><button class="sidebar-action-btn" onclick="saveAsMock()">Mock</button></div>`;
            rBody.innerHTML = isJson ? renderJsonCollapsible(displayBody) : body;
            renderJWT(body, h);
            
            // Update cache for this path so it stays if we switch tabs
            responseCache[item.path] = { 
                body, 
                headers: rHeaders.innerHTML, 
                meta: mHtmlText, 
                isJson, 
                rawHeaders: h 
            };
            
            lucide.createIcons();
        }

        async function deleteHistoryItem(ts) { 
            try {
                await callApi('/api/history/delete', 'POST', { timestamp: ts });
                showToast("History item deleted", "info");
                renderHistory(); 
            } catch (e) { showToast("Failed to delete: " + e.message, "error"); }
        }
        async function clearHistory() { 
            if (!confirm("Clear history?")) return; 
            try {
                await callApi('/api/history/clear', 'POST');
                showToast("History cleared", "success");
                renderHistory(); 
            } catch (e) { showToast("Failed to clear: " + e.message, "error"); }
        }

        async function renderWorkflows() {
            const list = document.getElementById('collection-tree');
            if (activeSidebar !== 'workflows') return;
            list.innerHTML = '<div style="padding: 16px;"><button class="btn-primary" style="width:100%" onclick="createNewWorkflow()"><i data-lucide="plus"></i> New Workflow</button></div>';
            try {
                workflows = await callApi('/api/workflows') || [];
                workflows.forEach(w => {
                    const div = document.createElement('div');
                    div.className = 'folder';
                    div.style.paddingLeft = '16px';
                    div.innerHTML = `<i data-lucide="git-branch" class="folder-icon"></i> <span>${w.name}</span>`;
                    div.onclick = () => selectWorkflow(w);
                    list.appendChild(div);
                });
                lucide.createIcons();
            } catch (e) { console.error(e); }
        }

        function createNewWorkflow() {
            const name = prompt("Workflow Name:", "New Workflow");
            if (!name) return;
            const newW = { id: Date.now().toString(), name, nodes: [], edges: [] };
            workflows.push(newW);
            saveWorkflows();
            renderWorkflows();
            selectWorkflow(newW);
        }

        function selectWorkflow(w) {
            currentWorkflow = w;
            document.getElementById('workflow-title').textContent = w.name;
            renderCanvas();
        }

        function renderCanvas() {
            if (!currentWorkflow) return;
            const nodeG = document.getElementById('workflow-nodes');
            const edgeG = document.getElementById('workflow-edges');
            nodeG.innerHTML = '';
            edgeG.innerHTML = '';

            currentWorkflow.nodes.forEach(node => {
                const g = document.createElementNS("http://www.w3.org/2000/svg", "g");
                g.setAttribute("transform", `translate(${node.x}, ${node.y})`);
                g.style.cursor = "move";

                const rect = document.createElementNS("http://www.w3.org/2000/svg", "rect");
                rect.setAttribute("width", "180"); rect.setAttribute("height", "50");
                rect.setAttribute("rx", "8"); rect.setAttribute("fill", "var(--bg-sidebar)");
                rect.setAttribute("stroke", "var(--border-color)");
                
                const text = document.createElementNS("http://www.w3.org/2000/svg", "text");
                text.setAttribute("x", "10"); text.setAttribute("y", "30");
                text.setAttribute("fill", "white"); text.setAttribute("font-size", "12");
                text.textContent = node.requestPath.split(' > ').pop();

                g.appendChild(rect); g.appendChild(text); nodeG.appendChild(g);
                
                g.onmousedown = (e) => {
                    e.stopPropagation();
                    let startX = e.clientX; let startY = e.clientY;
                    const onMove = (me) => {
                        node.x += (me.clientX - startX);
                        node.y += (me.clientY - startY);
                        startX = me.clientX; startY = me.clientY;
                        renderCanvas();
                    };
                    const onUp = () => { document.removeEventListener('mousemove', onMove); document.removeEventListener('mouseup', onUp); };
                    document.addEventListener('mousemove', onMove);
                    document.addEventListener('mouseup', onUp);
                };
            });
            
            currentWorkflow.edges.forEach(edge => {
                const from = currentWorkflow.nodes.find(n => n.id === edge.fromNode);
                const to = currentWorkflow.nodes.find(n => n.id === edge.toNode);
                if (from && to) {
                    const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
                    line.setAttribute("x1", from.x + 180); line.setAttribute("y1", from.y + 25);
                    line.setAttribute("x2", to.x); line.setAttribute("y2", to.y + 25);
                    line.setAttribute("stroke", "var(--text-secondary)"); line.setAttribute("stroke-width", "2");
                    line.setAttribute("marker-end", "url(#arrowhead)");
                    edgeG.appendChild(line);
                }
            });
        }

        function addNode(type) {
            if (!currentWorkflow) return showToast("Select a workflow first", "error");
            if (type === 'request') {
                const select = document.getElementById('workflow-request-select');
                select.innerHTML = '';
                flatRequests.forEach(req => { const opt = document.createElement('option'); opt.value = req.path; opt.textContent = req.path; select.appendChild(opt); });
                document.getElementById('workflow-node-add-btn').onclick = () => {
                    const node = { id: Date.now().toString(), type: 'request', requestPath: select.value, x: 100, y: 100, extracts: [] };
                    pushNode(node); closeModal('workflow-node-modal');
                };
                document.getElementById('workflow-node-modal').classList.add('show');
            } else if (type === 'wait') {
                document.getElementById('workflow-wait-add-btn').onclick = () => {
                    const node = { id: Date.now().toString(), type: 'wait', waitTime: parseInt(document.getElementById('workflow-wait-time').value), x: 100, y: 100 };
                    pushNode(node); closeModal('workflow-wait-modal');
                };
                document.getElementById('workflow-wait-modal').classList.add('show');
            } else if (type === 'condition') {
                document.getElementById('workflow-cond-add-btn').onclick = () => {
                    const node = { id: Date.now().toString(), type: 'condition', condition: document.getElementById('workflow-cond-expr').value, x: 100, y: 100 };
                    pushNode(node); closeModal('workflow-cond-modal');
                };
                document.getElementById('workflow-cond-modal').classList.add('show');
            } else if (type === 'loop') {
                document.getElementById('workflow-loop-add-btn').onclick = () => {
                    const node = { id: Date.now().toString(), type: 'loop', loopPath: document.getElementById('workflow-loop-path').value, maxIterations: parseInt(document.getElementById('workflow-loop-max').value), x: 100, y: 100 };
                    pushNode(node); closeModal('workflow-loop-modal');
                };
                document.getElementById('workflow-loop-modal').classList.add('show');
            } else if (type === 'script') {
                document.getElementById('workflow-script-add-btn').onclick = () => {
                    const node = { id: Date.now().toString(), type: 'script', script: document.getElementById('workflow-script-code').value, x: 100, y: 100 };
                    pushNode(node); closeModal('workflow-script-modal');
                };
                document.getElementById('workflow-script-modal').classList.add('show');
            } else if (type === 'input') {
                document.getElementById('workflow-input-add-btn').onclick = () => {
                    const node = { id: Date.now().toString(), type: 'input', variableName: document.getElementById('workflow-input-var').value, x: 100, y: 100 };
                    pushNode(node); closeModal('workflow-input-modal');
                };
                document.getElementById('workflow-input-modal').classList.add('show');
            }
        }

        function pushNode(node) {
            if (currentWorkflow.nodes.length > 0) {
                const last = currentWorkflow.nodes[currentWorkflow.nodes.length - 1];
                currentWorkflow.edges.push({ fromNode: last.id, toNode: node.id, type: 'default' });
            }
            currentWorkflow.nodes.push(node);
            renderCanvas();
        }

        function renderCanvas() {
            if (!currentWorkflow) return;
            const nodeG = document.getElementById('workflow-nodes');
            const edgeG = document.getElementById('workflow-edges');
            nodeG.innerHTML = ''; edgeG.innerHTML = '';

            currentWorkflow.nodes.forEach(node => {
                const g = document.createElementNS("http://www.w3.org/2000/svg", "g");
                g.setAttribute("transform", `translate(${node.x}, ${node.y})`);
                g.style.cursor = "move";

                const rect = document.createElementNS("http://www.w3.org/2000/svg", "rect");
                rect.setAttribute("width", "180"); rect.setAttribute("height", "60");
                rect.setAttribute("rx", "8");
                let color = "var(--bg-sidebar)";
                if (node.type === 'wait') color = "#1e293b";
                if (node.type === 'condition') color = "#312e81";
                if (node.type === 'script') color = "#064e3b";
                if (node.type === 'input') color = "#78350f";
                rect.setAttribute("fill", color); rect.setAttribute("stroke", "var(--border-color)");
                
                const typeText = document.createElementNS("http://www.w3.org/2000/svg", "text");
                typeText.setAttribute("x", "10"); typeText.setAttribute("y", "20");
                typeText.setAttribute("fill", "var(--text-secondary)"); typeText.setAttribute("font-size", "10");
                typeText.setAttribute("font-weight", "800");
                typeText.textContent = node.type.toUpperCase();

                const mainText = document.createElementNS("http://www.w3.org/2000/svg", "text");
                mainText.setAttribute("x", "10"); mainText.setAttribute("y", "40");
                mainText.setAttribute("fill", "white"); mainText.setAttribute("font-size", "12");
                let content = "";
                if (node.type === 'request') content = node.requestPath.split(' > ').pop();
                else if (node.type === 'wait') content = node.waitTime + "ms";
                else if (node.type === 'condition') content = node.condition;
                else if (node.type === 'script') content = node.script.substring(0, 20) + "...";
                else if (node.type === 'input') content = "Wait for: " + node.variableName;
                mainText.textContent = content.length > 20 ? content.substring(0, 17) + "..." : content;

                g.appendChild(rect); g.appendChild(typeText); g.appendChild(mainText);
                
                g.onmousedown = (e) => {
                    e.stopPropagation();
                    let startX = e.clientX; let startY = e.clientY;
                    const onMove = (me) => {
                        node.x += (me.clientX - startX); node.y += (me.clientY - startY);
                        startX = me.clientX; startY = me.clientY; renderCanvas();
                    };
                    const onUp = () => { document.removeEventListener('mousemove', onMove); document.removeEventListener('mouseup', onUp); };
                    document.addEventListener('mousemove', onMove); document.addEventListener('mouseup', onUp);
                };

                g.ondblclick = () => editNode(node);
                nodeG.appendChild(g);
            });
            
            currentWorkflow.edges.forEach(edge => {
                const from = currentWorkflow.nodes.find(n => n.id === edge.fromNode);
                const to = currentWorkflow.nodes.find(n => n.id === edge.toNode);
                if (from && to) {
                    const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
                    line.setAttribute("x1", from.x + 180); line.setAttribute("y1", from.y + 30);
                    line.setAttribute("x2", to.x); line.setAttribute("y2", to.y + 30);
                    line.setAttribute("stroke", edge.type === 'failure' ? "var(--method-delete)" : "var(--text-secondary)");
                    line.setAttribute("stroke-width", "2"); line.setAttribute("marker-end", "url(#arrowhead)");
                    edgeG.appendChild(line);
                }
            });
        }

        function editNode(node) {
            const fields = document.getElementById('node-edit-fields');
            fields.innerHTML = `
                <div class="form-group"><label>X Position</label><input type="number" id="edit-node-x" value="${node.x}"></div>
                <div class="form-group"><label>Y Position</label><input type="number" id="edit-node-y" value="${node.y}"></div>
            `;
            if (node.type === 'request') {
                fields.innerHTML += `<div class="form-group"><label>Extracts (Source Path : Target Var)</label><div id="node-extracts-list"></div><button class="sidebar-action-btn" onclick="addNodeExtractRow()">+ Add Extract</button></div>`;
                const list = fields.querySelector('#node-extracts-list');
                (node.extracts || []).forEach(ex => list.appendChild(createExtractRow(ex.sourcePath, ex.targetVar)));
            } else if (node.type === 'wait') {
                fields.innerHTML += `<div class="form-group"><label>Wait Time (ms)</label><input type="number" id="edit-node-wait" value="${node.waitTime}"></div>`;
            } else if (node.type === 'condition') {
                fields.innerHTML += `<div class="form-group"><label>Condition (GJSON)</label><input type="text" id="edit-node-cond" value="${node.condition}"></div>`;
            }

            document.getElementById('node-save-btn').onclick = () => {
                node.x = parseInt(document.getElementById('edit-node-x').value);
                node.y = parseInt(document.getElementById('edit-node-y').value);
                if (node.type === 'request') {
                    node.extracts = Array.from(fields.querySelectorAll('.header-row')).map(row => ({ sourcePath: row.querySelector('.ex-source').value, targetVar: row.querySelector('.ex-target').value })).filter(ex => ex.sourcePath !== '');
                } else if (node.type === 'wait') {
                    node.waitTime = parseInt(document.getElementById('edit-node-wait').value);
                } else if (node.type === 'condition') {
                    node.condition = document.getElementById('edit-node-cond').value;
                }
                renderCanvas(); closeModal('node-edit-modal');
            };
            document.getElementById('node-edit-modal').classList.add('show');
        }

        function createExtractRow(s, t) {
            const row = document.createElement('div'); row.className = 'header-row';
            row.innerHTML = `<input type="text" class="ex-source" value="${s}" placeholder="data.id"><input type="text" class="ex-target" value="${t}" placeholder="user_id"><button class="sidebar-action-btn" onclick="this.parentElement.remove()">✕</button>`;
            return row;
        }
        function addNodeExtractRow() { document.getElementById('node-extracts-list').appendChild(createExtractRow('', '')); }

        // Remove old simple click handler
        document.getElementById('workflow-svg').onclick = null;

        async function saveWorkflows() { try { await callApi('/api/workflows', 'POST', workflows); } catch (e) { showToast("Save failed: " + e.message, "error"); } }
        function saveWorkflow() { saveWorkflows(); showToast("Workflow saved!", "success"); }

        async function runWorkflow() {
            if (!currentWorkflow) return;
            const logsCont = document.getElementById('workflow-logs-content');
            logsCont.innerHTML = '<div style="color:var(--accent)">Running workflow...</div>';
            try {
                const logs = await callApi('/api/workflows/run', 'POST', currentWorkflow);
                logsCont.innerHTML = '';
                logs.forEach(log => {
                    const div = document.createElement('div');
                    div.style.marginBottom = '8px';
                    const node = currentWorkflow.nodes.find(n => n.id === log.nodeId);
                    const name = node ? node.requestPath : 'Unknown';
                    const statusColor = log.statusCode >= 200 && log.statusCode < 300 ? 'var(--method-get)' : 'var(--method-delete)';
                    div.innerHTML = `[ <span style="color:${statusColor}">${log.statusCode || 'ERR'}</span> ] ${name} ${log.error ? `- <span style="color:var(--method-delete)">${log.error}</span>` : ''}`;
                    logsCont.appendChild(div);
                });
            } catch (e) { logsCont.innerHTML = `<div style="color:var(--method-delete)">Error: ${e.message}</div>`; }
        }

        let hammerChart = null;

        async function runHammer() {
            if (!currentRequest) return showToast("Select a request first", "error");
            const workers = document.getElementById('hammer-workers').value;
            const duration = document.getElementById('hammer-duration').value;
            const resultsDiv = document.getElementById('hammer-results');
            const dashboard = document.getElementById('hammer-dashboard');
            const chartCont = document.getElementById('hammer-chart-container');
            
            resultsDiv.textContent = "Hammering in progress...";
            dashboard.style.display = 'grid';
            chartCont.style.display = 'block';

            if (hammerChart) hammerChart.destroy();
            const ctx = document.getElementById('hammer-chart').getContext('2d');
            hammerChart = new Chart(ctx, {
                type: 'line',
                data: { labels: [], datasets: [{ label: 'RPS', data: [], borderColor: '#4ade80', tension: 0.4 }, { label: 'Avg Latency (ms)', data: [], borderColor: '#60a5fa', tension: 0.4 }] },
                options: { responsive: true, maintainAspectRatio: false, scales: { y: { beginAtZero: true, grid: { color: '#26262a' } }, x: { grid: { display: false } } }, plugins: { legend: { labels: { color: '#a1a1aa', font: { size: 10 } } } } }
            });

            try {
                const data = await callApi('/api/hammer', 'POST', { path: currentRequest.path, workers: parseInt(workers), duration: parseInt(duration) });
                
                document.getElementById('h-total').textContent = data.TotalRequests;
                document.getElementById('h-rps').textContent = data.RPS.toFixed(1);
                document.getElementById('h-avg').textContent = (data.AverageLatency / 1000000).toFixed(1) + 'ms';
                document.getElementById('h-p99').textContent = (data.P99Latency / 1000000).toFixed(1) + 'ms';

                hammerChart.data.labels = Array.from({length: 10}, (_, i) => i + 1);
                hammerChart.data.datasets[0].data = Array(10).fill(data.RPS);
                hammerChart.data.datasets[1].data = Array(10).fill(data.AverageLatency / 1000000);
                hammerChart.update();

                resultsDiv.innerHTML = `Total Requests: ${data.TotalRequests}\nSuccess: ${data.SuccessCount}\nFailure: ${data.FailureCount}\nAvg Latency: ${(data.AverageLatency / 1000000).toFixed(2)}ms\nP95 Latency: ${(data.P95Latency / 1000000).toFixed(2)}ms\nP99 Latency: ${(data.P99Latency / 1000000).toFixed(2)}ms\nRPS: ${data.RPS.toFixed(2)}\nStatus Codes: ${JSON.stringify(data.StatusCodes, null, 2)}`;
            } catch (e) { resultsDiv.textContent = "Error: " + e.message; }
        }

        async function runSQL() {
            const driver = document.getElementById('sql-driver').value;
            const dbPath = document.getElementById('sql-db-path').value;
            const query = document.getElementById('sql-query').value;
            const targetVar = document.getElementById('sql-target-var').value;
            const targetCol = document.getElementById('sql-target-col').value;
            const resultsDiv = document.getElementById('sql-results');
            resultsDiv.textContent = "Executing...";
            try {
                const data = await callApi('/api/sql', 'POST', { driver: driver, connStr: dbPath, query: query, targetVar, targetCol });
                let html = '<table style="width:100%; border-collapse:collapse; font-size:12px;"><thead><tr style="border-bottom:1px solid var(--border-color);">';
                data.columns.forEach(c => html += `<th style="padding:8px; text-align:left; color:var(--accent);">${c}</th>`);
                html += '</tr></thead><tbody>';
                (data.rows || []).forEach(row => {
                    html += '<tr style="border-bottom:1px solid var(--border-color);">';
                    row.forEach(cell => html += `<td style="padding:8px; color:var(--text-secondary);">${cell}</td>`);
                    html += '</tr>';
                });
                html += '</tbody></table>';
                resultsDiv.innerHTML = html;
            } catch (e) { resultsDiv.textContent = "Error: " + e.message; }
        }

        function filterSidebar(query) {
            query = query.toLowerCase();
            document.querySelectorAll('.request-item').forEach(item => {
                const path = (item.dataset.path || "").toLowerCase();
                item.style.display = path.includes(query) ? 'flex' : 'none';
            });
        }

        function createRequestNode(path, method, name) {
            const div = document.createElement('div');
            div.className = 'request-item'; div.dataset.path = path;
            div.innerHTML = `
                <span class="method-tag method-${method}">${method}</span>
                <span style="flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">${name}</span>
                <button class="zap-btn" onclick="event.stopPropagation(); openAndSend('${path}')" title="Open and Send">
                    <i data-lucide="zap" style="width: 14px; height: 14px;"></i>
                </button>
            `;
            div.onclick = (e) => { e.stopPropagation(); selectRequest(path); };
            div.oncontextmenu = (e) => { e.preventDefault(); e.stopPropagation(); selectRequest(path); showContextMenu(e.pageX, e.pageY, path); };
            return div;
        }

        async function openAndSend(path) {
            selectRequest(path);
            await sendRequest();
        }

        function renderTree(container, items, prefix) {
            if (!items) return;
            items.forEach(item => {
                const path = prefix ? `${prefix} > ${item.name}` : item.name;
                if (item.item && item.item.length > 0) {
                    const f = document.createElement('div'); f.className = 'folder'; f.textContent = item.name;
                    f.oncontextmenu = (e) => { e.preventDefault(); e.stopPropagation(); showContextMenu(e.pageX, e.pageY, path); };
                    const c = document.createElement('div'); c.className = 'folder-content';
                    f.onclick = (e) => { e.stopPropagation(); f.classList.toggle('expanded'); c.classList.toggle('show'); };
                    container.appendChild(f); container.appendChild(c); renderTree(c, item.item, path);
                } else if (item.request) { container.appendChild(createRequestNode(path, item.request.method, item.name)); }
            });
        }

        function selectRequest(path) {
            currentRequest = flatRequests.find(r => r.path === path); if (!currentRequest) return;
            document.querySelectorAll('.request-item').forEach(el => el.classList.toggle('active', el.dataset.path === path));
            document.getElementById('req-method-display').textContent = currentRequest.request.method;
            document.getElementById('req-url-input').value = currentRequest.request.url.raw;
            const mode = (currentRequest.request.body && currentRequest.request.body.mode) || 'raw'; 
            switchBodyMode(mode);
            const radio = document.querySelector(`input[name="body-mode"][value="${mode}"]`);
            if (radio) radio.checked = true;
            document.getElementById('req-body-input').value = currentRequest.request.body ? (currentRequest.request.body.raw || '') : '';
            updateRequestHighlight();
            
            document.getElementById('sql-driver').value = currentRequest.sql_driver || "sqlite";
            document.getElementById('sql-db-path').value = currentRequest.db_path || "";
            document.getElementById('sql-query').value = currentRequest.sql_query || "";
            document.getElementById('sql-target-var').value = currentRequest.sql_target_var || "";
            document.getElementById('sql-target-col').value = currentRequest.sql_target_col || "";
            
            const urlEncList = document.getElementById('req-urlencoded-list'); urlEncList.innerHTML = '';
            (currentRequest.request.body ? currentRequest.request.body.urlencoded || [] : []).forEach(u => {
                const d = document.createElement('div'); d.className = 'header-row'; d.innerHTML = `<input type="text" class="urlencoded-key" value="${u.key}"><input type="text" class="urlencoded-value" value="${u.value}"><button class="sidebar-action-btn" onclick="this.parentElement.remove()">✕</button>`;
                urlEncList.appendChild(d);
            });
            const headList = document.getElementById('req-headers-list'); headList.innerHTML = '';
            (currentRequest.request.header || []).forEach(h => {
                const d = document.createElement('div'); d.className = 'header-row'; d.innerHTML = `<input type="text" class="header-key" value="${h.key}"><input type="text" class="header-value" value="${h.value}"><button class="sidebar-action-btn" onclick="this.parentElement.remove()">✕</button>`;
                headList.appendChild(d);
            });
const pre = (currentRequest.events || []).find(e => e.listen === 'prerequest'); const test = (currentRequest.events || []).find(e => e.listen === 'test');
document.getElementById('req-prerequest-input').value = pre ? pre.script.exec.join('\n') : '';
document.getElementById('req-tests-input').value = test ? test.script.exec.join('\n') : '';
document.getElementById('req-note-input').value = currentRequest.note || '';
            
            const mocksList = document.getElementById('mocks-list'); mocksList.innerHTML = currentRequest.responses && currentRequest.responses.length ? '' : '<div style="padding:16px; color:var(--text-secondary); font-style:italic; font-size:12px;">No mocks.</div>';
            (currentRequest.responses || []).forEach(m => {
                const d = document.createElement('div'); d.style = 'border:1px solid var(--border-color); padding:12px; margin-bottom:12px; border-radius:8px; background:var(--bg-sidebar); position:relative;';
                d.innerHTML = `
                    <div style="display:flex; justify-content:space-between; align-items:flex-start;">
                        <div>
                            <div style="font-weight:700; color:var(--accent);">${m.name}</div>
                            <div style="font-size:11px; color:var(--method-get);">${m.code} ${m.status}</div>
                        </div>
                        <button class="sidebar-action-btn" onclick="deleteMock('${m.name}')" title="Delete Mock"><i data-lucide="trash-2" style="width:14px;"></i></button>
                    </div>
                    ${m.condition ? `<div style="font-size:10px; color:#f59e0b; margin-top:4px;"><b>Condition:</b> ${m.condition}</div>` : ''}
                    ${m.delay ? `<div style="font-size:10px; color:#60a5fa; margin-top:2px;"><b>Delay:</b> ${m.delay}ms</div>` : ''}
                    <div style="font-size:10px; color:var(--text-secondary); opacity:0.6; margin-top:6px; font-family:monospace; white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">${m.body.substring(0,100)}...</div>
                `;
                mocksList.appendChild(d);
            });
            
            const cached = responseCache[path];
            if (cached) { 
                document.getElementById('response-body').innerHTML = cached.isJson ? syntaxHighlight(cached.body) : cached.body;
                document.getElementById('resp-pane-headers').innerHTML = cached.headers; 
                document.getElementById('response-meta').innerHTML = `<div style="display:flex;gap:12px;align-items:center"><span>${cached.meta}</span><button class="sidebar-action-btn" onclick="setBaseline()">Baseline</button><button class="sidebar-action-btn" onclick="saveAsMock()">Mock</button></div>`;
                renderJWT(cached.body, cached.rawHeaders || {});
            } else { document.getElementById('response-body').textContent = '// Ready'; document.getElementById('resp-pane-headers').innerHTML = ''; document.getElementById('response-meta').textContent = ''; }
            lucide.createIcons();
        }

async function saveCurrentRequest() {
  if (!currentRequest) return;
  const urlencoded = Array.from(document.querySelectorAll('#req-urlencoded-list .header-row')).map(row => ({ key: row.querySelector('.urlencoded-key').value, value: row.querySelector('.urlencoded-value').value })).filter(u => u.key !== '');
  const headers = Array.from(document.querySelectorAll('#req-headers-list .header-row')).map(row => ({ key: row.querySelector('.header-key').value, value: row.querySelector('.header-value').value })).filter(h => h.key !== '');
  const schema = currentRequest.schema || '';
  try {
    const data = await callApi('/api/requests/update', 'POST', {
      oldPath: currentRequest.path,
      newPath: currentRequest.path,
      method: document.getElementById('req-method-display').textContent,
      url: document.getElementById('req-url-input').value,
      bodyMode: document.querySelector('input[name="body-mode"]:checked').value,
      bodyRaw: document.getElementById('req-body-input').value,
      urlencoded,
      headers,
      preRequestScript: document.getElementById('req-prerequest-input').value,
      testScript: document.getElementById('req-tests-input').value,
      sqlQuery: document.getElementById('sql-query').value,
      dbPath: document.getElementById('sql-db-path').value,
      sqlDriver: document.getElementById('sql-driver').value,
      sqlTargetVar: document.getElementById('sql-target-var').value,
      sqlTargetCol: document.getElementById('sql-target-col').value,
      note: document.getElementById('req-note-input').value,
      schema: schema
    });
    const idx = flatRequests.findIndex(r => r.path === currentRequest.path);
    if (idx !== -1) { flatRequests[idx] = data; currentRequest = data; }
    showToast("Saved!", "success");
  } catch (e) {
    showToast("Save failed: " + e.message, "error");
  }
}

        async function sendRequest() {
            if (!currentRequest) return;
            const rBody = document.getElementById('response-body'); const rHeaders = document.getElementById('resp-pane-headers'); const meta = document.getElementById('response-meta');
            rBody.textContent = '// Sending...';
            const start = Date.now();
            
            let bodyRaw = document.getElementById('req-body-input').value;
            let mode = document.querySelector('input[name="body-mode"]:checked').value;
            if (mode === 'graphql') {
                const q = document.getElementById('req-body-graphql-query').value;
                const v = document.getElementById('req-body-graphql-vars').value;
                try {
                    bodyRaw = JSON.stringify({ query: q, variables: v ? JSON.parse(v) : {} });
                } catch(e) { showToast("Invalid GraphQL Variables JSON", "error"); return; }
            }

            try {
                const data = await callApi('/api/send', 'POST', { path: currentRequest.path, bodyMode: mode, bodyRaw: bodyRaw, urlencoded: Array.from(document.querySelectorAll('#req-urlencoded-list .header-row')).map(row => ({ key: row.querySelector('.urlencoded-key').value, value: row.querySelector('.urlencoded-value').value })).filter(u => u.key !== '') });
                const duration = Date.now() - start;
                let color = (data.statusCode >= 400 || data.statusCode === 0) ? 'var(--method-delete)' : 'var(--method-get)';
                let mHtmlText = `<span style="color:${color};font-weight:700">${data.statusCode} ${data.statusText}</span> &bull; ${duration}ms`;
                let body = data.body || ''; const h = data.headers || {}; const ct = (h['Content-Type'] || h['content-type'] || [])[0] || '';
                let isJson = ct.includes('json');
                
                let displayBody = body;
                if (isJson && body) {
                    try {
                        const parsed = JSON.parse(body);
                        displayBody = parsed;
                        body = JSON.stringify(parsed, null, 4);
                    } catch (e) {}
                }

                rHeaders.innerHTML = Object.entries(h).map(([k, v]) => `<div><span style="color:var(--accent); font-weight:600;">${k}:</span> ${v.join(', ')}</div>`).join('');
                responseCache[currentRequest.path] = { body, headers: rHeaders.innerHTML, meta: mHtmlText, isJson, rawHeaders: h };
                meta.innerHTML = `<div style="display:flex;gap:12px;align-items:center"><span>${mHtmlText}</span><button class="sidebar-action-btn" onclick="setBaseline()">Baseline</button><button class="sidebar-action-btn" onclick="saveAsMock()">Mock</button></div>`;
                rBody.innerHTML = isJson ? renderJsonCollapsible(displayBody) : body;
                renderJWT(body, h);
                if (activeSidebar === 'history') renderHistory();
                lucide.createIcons();
                
                if (data.statusCode >= 200 && data.statusCode < 300) {
                    showToast(`Request successful (${data.statusCode})`, "success");
                } else {
                    showToast(`Request finished with status ${data.statusCode}`, "info");
                }
            } catch (e) { 
                rBody.textContent = '// Error: ' + e.message; 
                showToast("Request failed: " + e.message, "error");
            }
        }

        function switchTab(tab) {
            document.querySelectorAll('.tabs-container .tab').forEach(t => t.classList.toggle('active', t.textContent.toLowerCase() === tab.toLowerCase()));
            document.querySelectorAll('.pane').forEach(p => { if (p.id.startsWith('pane-')) p.classList.toggle('active', p.id === `pane-${tab.toLowerCase()}`); });
        }
        function switchResponseTab(tab) {
            document.querySelectorAll('#response-header .tab').forEach(t => t.classList.toggle('active', t.textContent.toLowerCase() === tab.toLowerCase()));
            document.querySelectorAll('#response-container .pane').forEach(p => { if(p.id.startsWith('resp-pane-')) p.classList.toggle('active', p.id === `resp-pane-${tab.toLowerCase()}`); });
            if (tab === 'schema') loadSchemaEditor();
        }
        function switchBodyMode(mode) { 
            document.getElementById('body-raw-container').style.display = mode === 'raw' ? 'block' : 'none'; 
            document.getElementById('body-urlencoded-container').style.display = mode === 'urlencoded' ? 'flex' : 'none'; 
        }

        function setBaseline() { if (!currentRequest) return; const cached = responseCache[currentRequest.path]; if (!cached) return showToast("Run request first", "error"); baselineCache[currentRequest.path] = { ...cached }; showToast("Baseline set!", "success"); }
        function renderDiff() {
            if (!currentRequest) return; const current = responseCache[currentRequest.path]; const baseline = baselineCache[currentRequest.path];
            const bPre = document.getElementById('diff-baseline'); const cPre = document.getElementById('diff-current');
            if (!baseline) { bPre.textContent = "// No baseline."; cPre.textContent = current ? current.body : ""; return; }
            bPre.textContent = baseline.body; cPre.textContent = current ? current.body : "";
        }

        function showCodeModal() { if (!currentRequest) return showToast("Select request", "error"); document.getElementById('code-modal').classList.add('show'); generateSnippet(document.getElementById('snippet-lang').value); }
        function generateSnippet(lang) {
            const req = currentRequest.request; const body = req.body ? (req.body.raw || '') : '';
            let code = "";
            if (lang === 'javascript') code = `fetch("${req.url.raw}", {\n  method: "${req.method}",\n  headers: ${JSON.stringify(req.header || {}, null, 2)}${body ? `,\n  body: JSON.stringify(${body})` : ''}\n}).then(r => r.json()).then(console.log);`;
            else if (lang === 'go') code = `package main\nimport ("net/http"; "io/ioutil"; "strings")\nfunc main() {\n  url := "${req.url.raw}"\n  payload := strings.NewReader(\`${body}\`)\n  req, _ := http.NewRequest("${req.method}", url, payload)\n  res, _ := http.DefaultClient.Do(req)\n  defer res.Body.Close()\n  b, _ := ioutil.ReadAll(res.Body)\n  println(string(b))\n}`;
            else if (lang === 'python') code = `import requests\nurl = "${req.url.raw}"\nres = requests.request("${req.method}", url, headers=${JSON.stringify(req.header || {})}, data="${body}")\nprint(res.text)`;
            document.getElementById('snippet-output').value = code;
        }
        function copySnippet() { const el = document.getElementById('snippet-output'); el.select(); document.execCommand('copy'); showToast("Copied!", "success"); }

        async function saveAsMock() {
            if (!currentRequest) return; const cached = responseCache[currentRequest.path]; if (!cached) return;
            const name = prompt("Mock Name:", "Default Mock"); if (!name) return;
            const condition = prompt("Condition (Optional, GJSON path):", "");
            const delayStr = prompt("Delay in ms (Optional):", "0");
            const delay = parseInt(delayStr) || 0;
            const meta = document.getElementById('response-meta').innerText || ""; const match = meta.match(/(\d+) (.*)/);
            const code = match ? parseInt(match[1]) : 200; const status = match ? match[2].split('\n')[0].split(' •')[0] : "OK";
            const hRows = document.querySelectorAll('#resp-pane-headers div');
            const h = Array.from(hRows).map(r => { const t = r.innerText; const i = t.indexOf(':'); return { key: t.substring(0, i).trim(), value: t.substring(i+1).trim() }; });
            try { await callApi('/api/mock/save', 'POST', { path: currentRequest.path, name, code, status, body: cached.body, headers: h, condition, delay }); showToast("Mock saved!", "success"); await init(); selectRequest(currentRequest.path); } catch (e) { showToast("Save failed: " + e.message, "error"); }
        }

        async function deleteMock(mockName) {
            if (!confirm(`Delete mock "${mockName}"?`)) return;
            try { await callApi('/api/mock/delete', 'POST', { path: currentRequest.path, mockName }); showToast("Mock deleted!", "success"); await init(); selectRequest(currentRequest.path); } catch (e) { showToast("Delete failed: " + e.message, "error"); }
        }

        // ─── Response Schema Feature ─────────────────────────────────────────────

        async function saveResponseSchema() {
            if (!currentRequest) return showToast("Select a request first", "error");
            const cached = responseCache[currentRequest.path];
            if (!cached || !cached.body) return showToast("Send a request first", "error");
            
            let bodyStr = cached.body;
            if (typeof bodyStr !== 'string') {
                try { bodyStr = JSON.stringify(bodyStr); } catch (e) { bodyStr = String(bodyStr); }
            }
            
            try {
                const parsed = JSON.parse(bodyStr);
                const schema = generateSchemaFromJSON(parsed);
                const schemaStr = JSON.stringify(schema, null, 4);
                
                await callApi('/api/schema/save', 'POST', { requestPath: currentRequest.path, schema: schemaStr });
                showToast("Response schema saved!", "success");
                
                const idx = flatRequests.findIndex(r => r.path === currentRequest.path);
                if (idx !== -1) flatRequests[idx].schema = schemaStr;
                currentRequest.schema = schemaStr;
            } catch (e) {
                showToast("Failed to generate schema: " + e.message, "error");
            }
        }

        function generateSchemaFromJSON(data) {
            if (data === null) return { type: "null" };
            const type = typeof data;
            
            if (type === "string") return { type: "string" };
            if (type === "boolean") return { type: "boolean" };
            if (type === "number") {
                if (Number.isInteger(data)) return { type: "integer" };
                return { type: "number" };
            }
            
            if (Array.isArray(data)) {
                const schema = { type: "array" };
                if (data.length > 0) {
                    schema.items = generateSchemaFromJSON(data[0]);
                    if (data.length > 1) {
                        const itemSchemas = data.slice(1).map(item => generateSchemaFromJSON(item));
                        schema.items = mergeSchemas([schema.items, ...itemSchemas]);
                    }
                }
                return schema;
            }
            
            if (type === "object") {
                const schema = { type: "object", properties: {}, required: [] };
                for (const key of Object.keys(data)) {
                    schema.properties[key] = generateSchemaFromJSON(data[key]);
                    schema.required.push(key);
                }
                return schema;
            }
            
            return { type: "string" };
        }

        function mergeSchemas(schemas) {
            if (schemas.length === 0) return { type: "string" };
            
            const types = new Set(schemas.map(s => s.type));
            if (types.size === 1) {
                const base = JSON.parse(JSON.stringify(schemas[0]));
                if (base.properties) {
                    const allKeys = new Set();
                    schemas.forEach(s => { if (s.properties) Object.keys(s.properties).forEach(k => allKeys.add(k)); });
                    for (const key of allKeys) {
                        const keySchemas = schemas.filter(s => s.properties && s.properties[key]).map(s => s.properties[key]);
                        if (keySchemas.length > 0) {
                            base.properties[key] = mergeSchemas(keySchemas);
                        }
                    }
                }
                return base;
            }
            
            return { type: "string" };
        }

        function loadSchemaEditor() {
            const editor = document.getElementById('schema-editor');
            if (!editor) return;
            
            if (!currentRequest) {
                editor.value = '// Select a request to view its response schema.';
                return;
            }
            
            const schema = currentRequest.schema || flatRequests.find(r => r.path === currentRequest.path)?.schema;
            if (schema) {
                try {
                    const parsed = JSON.parse(schema);
                    editor.value = JSON.stringify(parsed, null, 4);
                } catch (e) {
                    editor.value = schema;
                }
            } else {
                editor.value = '// No schema saved. Send a request and click "Save Schema" in the response toolbar to generate one.';
            }
        }

        async function saveSchemaFromEditor() {
            if (!currentRequest) return showToast("Select a request first", "error");
            const editor = document.getElementById('schema-editor');
            if (!editor) return;
            
            const schemaStr = editor.value.trim();
            if (!schemaStr || schemaStr.startsWith('//')) {
                return showToast("No schema to save", "error");
            }
            
            try {
                JSON.parse(schemaStr);
            } catch (e) {
                return showToast("Invalid JSON: " + e.message, "error");
            }
            
            try {
                await callApi('/api/schema/save', 'POST', { requestPath: currentRequest.path, schema: schemaStr });
                showToast("Schema saved!", "success");
                
                const idx = flatRequests.findIndex(r => r.path === currentRequest.path);
                if (idx !== -1) flatRequests[idx].schema = schemaStr;
                currentRequest.schema = schemaStr;
            } catch (e) {
                showToast("Failed to save schema: " + e.message, "error");
            }
        }

        function formatSchemaEditor() {
            const editor = document.getElementById('schema-editor');
            if (!editor) return;
            try {
                const parsed = JSON.parse(editor.value);
                editor.value = JSON.stringify(parsed, null, 4);
            } catch (e) {
                showToast("Invalid JSON", "error");
            }
        }

        function showDuplicateModal() { document.getElementById('duplicate-path').value = menuTarget + " Copy"; document.getElementById('duplicate-modal').classList.add('show'); }
        function showEditModal() { const req = flatRequests.find(r => r.path === menuTarget); if(!req) return; document.getElementById('edit-path').value = req.path; document.getElementById('edit-method').value = req.request.method; document.getElementById('edit-url').value = req.request.url.raw; document.getElementById('edit-modal').classList.add('show'); }
        
        async function saveEditRequest() {
            const newPath = document.getElementById('edit-path').value; const req = flatRequests.find(r => r.path === menuTarget);
            const pre = (req.events || []).find(e => e.listen === 'prerequest'); const test = (req.events || []).find(e => e.listen === 'test');
            try { await callApi('/api/requests/update', 'POST', { 
                    oldPath: menuTarget, 
                    newPath: newPath, 
                    method: document.getElementById('edit-method').value, 
                    url: document.getElementById('edit-url').value, 
                    bodyMode: (req.request.body && req.request.body.mode) || 'raw', 
                    bodyRaw: document.getElementById('req-body-input').value, 
                    urlencoded: req.request.body ? (req.request.body.urlencoded || []) : [], 
                    headers: req.request.header || [], 
preRequestScript: pre ? pre.script.exec.join('\n') : '',
testScript: test ? test.script.exec.join('\n') : '',
sqlQuery: req.sql_query || '',
dbPath: req.db_path || '',
sqlDriver: req.sql_driver || 'sqlite',
sqlTargetVar: req.sql_target_var || '',
sqlTargetCol: req.sql_target_col || '',
note: req.note || '',
schema: req.schema || ''
}); 
                showToast("Request updated!", "success");
                closeModal('edit-modal'); 
                await init(); 
                selectRequest(newPath); 
            } catch (e) {
                showToast("Failed to update request: " + e.message, "error");
            }
        }

        async function saveDuplicateRequest() { 
            try { await callApi('/api/requests/duplicate', 'POST', { path: menuTarget, newPath: document.getElementById('duplicate-path').value }); 
                showToast("Request duplicated!", "success");
                closeModal('duplicate-modal'); 
                init(); 
            } catch (e) {
                showToast("Failed to duplicate request: " + e.message, "error");
            }
        }
        function showNewModalFromContext() { document.getElementById('new-path').value = menuTarget + " > "; showNewModal(); }

        function exportPostman() {
            if (_isWails) {
                // Wails mode: call ExportPostman which opens a native file dialog
                const path = menuTarget || "";
                window.go.main.App.ExportPostman(path).then(() => {
                    showToast("Collection exported!", "success");
                }).catch(e => {
                    showToast("Export failed: " + e.message, "error");
                });
                return;
            }
            // Web mode: trigger download via browser
            if (!menuTarget) {
                window.location.href = '/api/export';
                return;
            }
            window.location.href = `/api/export?path=${encodeURIComponent(menuTarget)}`;
            showToast("Exporting Postman collection...", "info");
        }

        async function deleteCurrentItem() {
            if (!confirm(`Delete "${menuTarget}"?`)) return;
            try { await callApi('/api/requests/delete', 'POST', { path: menuTarget });
                showToast("Item deleted", "success");
                await init(); 
                if (currentRequest && currentRequest.path === menuTarget) currentRequest = null; 
            } catch (e) {
                showToast("Failed to delete item: " + e.message, "error");
            }
        }

        async function moveSelected(dir) {
            const idx = flatRequests.findIndex(r => r.path === menuTarget); if (idx === -1) return;
            let tIdx = dir === 'up' ? idx - 1 : idx + 1;
            if (tIdx < 0 || tIdx >= flatRequests.length) return;
            try { await callApi('/api/requests/reorder', 'POST', { path1: menuTarget, path2: flatRequests[tIdx].path }); await init(); selectRequest(menuTarget); } catch (e) { showToast("Reorder failed: " + e.message, "error"); }
        }

        async function showVariablesModal() {
            const vars = await callApi('/api/variables');
            const list = document.getElementById('variables-list'); list.innerHTML = '';
            Object.entries(vars).forEach(([k,v]) => {
                const div = document.createElement('div'); div.className = 'header-row';
                div.innerHTML = `<input type="text" class="var-key" value="${k}"><input type="text" class="var-value" value="${v}"><button class="sidebar-action-btn" onclick="this.parentElement.remove()">✕</button>`;
                list.appendChild(div);
            });
            document.getElementById('variables-modal').classList.add('show');
        }

        function addVariableRow() {
            const div = document.createElement('div'); div.className = 'header-row';
            div.innerHTML = `<input type="text" class="var-key"><input type="text" class="var-value"><button class="sidebar-action-btn" onclick="this.parentElement.remove()">✕</button>`;
            document.getElementById('variables-list').appendChild(div);
        }

        async function saveVariables() {
            const vars = {};
            document.querySelectorAll('#variables-list .header-row').forEach(row => { const k = row.querySelector('.var-key').value; if(k) vars[k] = row.querySelector('.var-value').value; });
            try {
                await callApi('/api/variables', 'POST', vars);
                showToast("Variables saved!", "success");
                closeModal('variables-modal');
            } catch (e) {
                showToast("Failed to save variables: " + e.message, "error");
            }
        }

        function showContextMenu(x, y, path) {
            const menu = document.getElementById('context-menu');
            menu.style.left = x + 'px'; menu.style.top = y + 'px'; menu.style.display = 'flex';
            menuTarget = path;
            document.addEventListener('click', () => menu.style.display = 'none', { once: true });
        }
        function closeModal(id) { document.getElementById(id).classList.remove('show'); }
        function showNewModal() { document.getElementById('new-modal').classList.add('show'); }
        
        async function saveNewRequest() {
            const path = document.getElementById('new-path').value;
            try { await callApi('/api/requests/new', 'POST', { 
                    path, 
                    method: document.getElementById('new-method').value, 
                    url: document.getElementById('new-url').value, 
                    bodyMode: 'raw', 
                    bodyRaw: '', 
                    urlencoded: [], 
                    headers: [], 
                    preRequestScript: '', 
                    testScript: '',
                    sqlQuery: '',
                    dbPath: '',
                    sqlDriver: 'sqlite',
                    sqlTargetVar: '',
                    sqlTargetCol: '',
                    schema: ''
                });
                showToast("Request created!", "success");
                closeModal('new-modal'); 
                init(); 
            } catch (e) {
                showToast("Failed to create request: " + e.message, "error");
            }
        }

        async function fetchActiveEnv() {
            const data = await callApi('/api/environments/active');
            document.getElementById('env-active-select').value = data.activeEnvId || data.id || '';
        }

        async function fetchEnvironments() {
            const envs = await callApi('/api/environments') || [];
            renderActiveEnvSelect(envs);
            return envs;
        }

        function renderActiveEnvSelect(envs) {
            const select = document.getElementById('env-active-select');
            const current = select.value;
            select.innerHTML = '<option value="">No Environment</option>';
            envs.forEach(e => {
                const opt = document.createElement('option');
                opt.value = e.id; opt.textContent = e.name;
                select.appendChild(opt);
            });
            select.value = current;
        }

        async function switchActiveEnv(id) {
            try { await callApi('/api/environments/active', 'POST', { id }); } catch (e) { showToast("Switch failed: " + e.message, "error"); }
            showToast("Environment switched", "info");
        }

        async function unlockVault() {
            const password = document.getElementById('vault-password').value;
            try { await callApi('/api/vault/unlock', 'POST', { password }); closeModal('vault-modal'); showToast("Vault Unlocked!", "success"); showEnvironmentsModal(); }
            catch (e) { showToast("Invalid Password: " + e.message, "error"); }
        }

        async function getVaultStatus() {
            const data = await callApi('/api/vault/status');
            return data.unlocked;
        }

        async function showEnvironmentsModal() {
            const envs = await fetchEnvironments();
            const list = document.getElementById('env-list');
            list.innerHTML = '';
            
            const unlocked = await getVaultStatus();
            const header = document.querySelector('#environments-modal .modal-title');
            header.innerHTML = `<i data-lucide="layers"></i> Manage Environments <button class="sidebar-action-btn" onclick="document.getElementById('vault-modal').classList.add('show')">${unlocked ? '<i data-lucide="unlock"></i>' : '<i data-lucide="lock"></i>'}</button>`;

            envs.forEach(env => renderEnvEditor(env, unlocked));
            document.getElementById('environments-modal').classList.add('show');
            lucide.createIcons();
        }

        function renderEnvEditor(env, unlocked) {
            const container = document.getElementById('env-list');
            const div = document.createElement('div');
            div.className = 'env-container';
            div.style = 'border: 1px solid var(--border-color); padding: 16px; border-radius: 8px; margin-bottom: 16px; background: var(--bg-app);';
            div.dataset.id = env.id;
            
            div.innerHTML = `
                <div style="display:flex; gap:12px; margin-bottom:12px;">
                    <input type="text" class="env-name" value="${env.name}" style="font-weight:700; color:var(--accent);">
                    <button class="sidebar-action-btn" style="color:var(--method-delete)" onclick="this.parentElement.parentElement.remove()">Delete Env</button>
                </div>
                <div class="env-vars-list"></div>
                <button class="sidebar-action-btn" style="margin-top:8px;" onclick="addEnvVarRow(this.previousElementSibling)">+ Add Variable</button>
            `;
            
            const varList = div.querySelector('.env-vars-list');
            Object.entries(env.variables || {}).forEach(([k,v]) => {
                varList.appendChild(createEnvVarRow(k, v, false));
            });
            Object.entries(env.secret_vars || {}).forEach(([k,v]) => {
                varList.appendChild(createEnvVarRow(k, v, true));
            });
            
            container.appendChild(div);
        }

        function createEnvVarRow(k, v, isSecret) {
            const row = document.createElement('div');
            row.className = 'header-row env-var-row';
            row.dataset.isSecret = isSecret;
            const displayVal = isSecret ? '••••••••' : v;
            row.innerHTML = `
                <input type="text" class="env-var-key" value="${k}" placeholder="Key">
                <input type="text" class="env-var-value" value="${displayVal}" placeholder="Value" ${isSecret ? 'readonly' : ''}>
                <button class="sidebar-action-btn btn-lock" onclick="toggleSecretRow(this)">${isSecret ? '🔓' : '🔒'}</button>
                <button class="sidebar-action-btn" onclick="this.parentElement.remove()">✕</button>
            `;
            return row;
        }

        function toggleSecretRow(btn) {
            const row = btn.parentElement;
            const isSecret = row.dataset.isSecret === 'true';
            if (!isSecret) {
                if (confirm("Mark as secret? It will be encrypted on save.")) {
                    row.dataset.isSecret = 'true';
                    btn.textContent = '🔓';
                    row.querySelector('.env-var-value').type = 'password';
                }
            } else {
                showToast("Decryption requires full save or API usage.", "info");
            }
        }

        async function saveAllEnvironments() {
            const envs = [];
            const containers = document.querySelectorAll('#env-list .env-container');
            
            for (const envDiv of containers) {
                const env = {
                    id: envDiv.dataset.id,
                    name: envDiv.querySelector('.env-name').value,
                    variables: {},
                    secret_vars: {}
                };
                
                const rows = envDiv.querySelectorAll('.env-var-row');
                for (const row of rows) {
                    const k = row.querySelector('.env-var-key').value;
                    const v = row.querySelector('.env-var-value').value;
                    if (!k) continue;

                    if (row.dataset.isSecret === 'true') {
                        // If it's still dots, it's an existing secret we don't change
                        if (v === '••••••••') {
                            // Find old secret value (need to keep it)
                            // For simplicity in this demo, we assume we have the full env list cached or just don't overwrite if unchanged
                            // In a real app, we'd fetch the existing env first.
                            const existingEnv = workflows.find(e => e.id === env.id); // Placeholder logic
                            // Let's just send the raw value if it's not dots
                        }
                        
                        if (v !== '••••••••') {
                            const data = await callApi('/api/vault/encrypt', 'POST', { plaintext: v });
                            env.secret_vars[k] = data.ciphertext || data.encrypted;
                        }
                    } else {
                        env.variables[k] = v;
                    }
                }
                envs.push(env);
            }
            
            try { await callApi('/api/environments', 'POST', envs); showToast("All environments saved!", "success"); } catch (e) { showToast("Save failed: " + e.message, "error"); }
        }

        document.addEventListener('keydown', (e) => {
            if (e.ctrlKey && e.key.toLowerCase() === 's') {
                e.preventDefault();
                saveCurrentRequest();
                return;
            }
            if (e.ctrlKey && e.key.toLowerCase() === 'r') {
                e.preventDefault();
                sendRequest();
                return;
            }
            if (e.ctrlKey && e.key === 'Enter') {
                e.preventDefault();
                sendRequest();
                return;
            }
            if (e.ctrlKey && e.key.toLowerCase() === 'q') {
                e.preventDefault();
                switchTab('sql');
                return;
            }
            if (e.ctrlKey && e.key.toLowerCase() === 'h') {
                e.preventDefault();
                switchTab('hammer');
                return;
            }
            if (e.ctrlKey && e.key.toLowerCase() === 'n') {
                e.preventDefault();
                showNewModal();
                return;
            }
            if (e.ctrlKey && e.shiftKey && e.key.toLowerCase() === 'p') {
                e.preventDefault();
                const modal = document.getElementById('command-palette-modal');
                modal.classList.add('show');
                const input = document.getElementById('command-palette-input');
                input.value = '';
                input.focus();
                renderCommandResults('');
                return;
            }
            if (e.ctrlKey && e.key === 'p' && !e.shiftKey) {
                e.preventDefault();
                const modal = document.getElementById('search-modal');
                modal.classList.add('show');
                const input = document.getElementById('global-search-input');
                input.value = '';
                input.focus();
                renderSearchResults('');
            }
            if (e.ctrlKey && e.key.toLowerCase() === 'l') {
                e.preventDefault();
                document.getElementById('response-body').textContent = '// Ready';
                document.getElementById('resp-pane-headers').innerHTML = '';
                document.getElementById('response-meta').textContent = '';
                document.querySelectorAll('.request-item').forEach(el => el.classList.remove('active'));
                lastFilteredResult = null;
                showToast("View cleared", "info");
                return;
            }
            if (e.altKey && e.shiftKey && (e.key === '+' || e.key === '=')) {
                e.preventDefault();
                expandAllJson();
                return;
            }
            if (e.altKey && e.shiftKey && (e.key === '-' || e.key === '_')) {
                e.preventDefault();
                collapseAllJson();
                return;
            }
            if (e.ctrlKey && e.key.toLowerCase() === 'f') {
                const filter = document.getElementById('json-filter');
                if (filter && document.activeElement !== filter) {
                    e.preventDefault();
                    filter.focus();
                    filter.select();
                }
            }
            if (e.key === 'Escape') {
                closeModal('search-modal');
                closeModal('command-palette-modal');
                closeModal('new-modal');
                closeModal('duplicate-modal');
                closeModal('edit-modal');
                closeModal('variables-modal');
                closeModal('code-modal');
                closeModal('workflow-node-modal');
                closeModal('environments-modal');
            }
        });

        const commands = [
            { name: "Format JSON Body", action: beautifyRequestJson },
            { name: "Clear History", action: clearHistory },
            { name: "Switch to Collections Tab", action: () => switchSidebar('collections') },
            { name: "Switch to History Tab", action: () => switchSidebar('history') },
            { name: "Switch to Workflows Tab", action: () => switchSidebar('workflows') },
            { name: "Switch to WebSockets Tab", action: () => switchSidebar('ws') },
            { name: "Switch to Mock Stats Tab", action: () => switchSidebar('mock') },
            { name: "Manage Environments", action: showEnvironmentsModal },
            { name: "Manage Global Variables", action: showVariablesModal },
            { name: "Toggle Proxy", action: toggleProxy },
            { name: "Export HAR", action: exportHAR },
            { name: "New Request", action: showNewModal }
        ];

        document.getElementById('command-palette-input').oninput = (e) => {
            renderCommandResults(e.target.value);
        };

        function renderCommandResults(query) {
            const results = document.getElementById('command-palette-results');
            results.innerHTML = '';
            const q = query.toLowerCase();
            const filtered = commands.filter(c => c.name.toLowerCase().includes(q));
            
            filtered.forEach(c => {
                const div = document.createElement('div');
                div.className = 'request-item';
                div.innerHTML = `<i data-lucide="terminal" style="width:14px; margin-right:8px;"></i> <span>${c.name}</span>`;
                div.onclick = () => {
                    c.action();
                    closeModal('command-palette-modal');
                };
                results.appendChild(div);
            });
            lucide.createIcons();
            if (filtered.length === 0) {
                results.innerHTML = '<div style="padding: 12px; color: var(--text-secondary); font-size: 13px;">No commands found.</div>';
            }
        }

        document.getElementById('global-search-input').oninput = (e) => {
            renderSearchResults(e.target.value);
        };

        function renderSearchResults(query) {
            const results = document.getElementById('global-search-results');
            results.innerHTML = '';
            const q = query.toLowerCase();
            const filtered = flatRequests.filter(r => r.path.toLowerCase().includes(q));
            
            filtered.slice(0, 10).forEach(r => {
                const div = createRequestNode(r.path, r.request.method, r.path);
                const originalClick = div.onclick;
                div.onclick = (e) => { 
                    if (e.target.closest('.zap-btn')) return;
                    originalClick(e); 
                    closeModal('search-modal'); 
                };
                results.appendChild(div);
            });
            if (filtered.length === 0) {
                results.innerHTML = '<div style="padding: 12px; color: var(--text-secondary); font-size: 13px;">No results found.</div>';
            }
        }

        function applyJsonFilter(query) {
            if (!currentRequest) return;
            const cached = responseCache[currentRequest.path];
            if (!cached || !cached.isJson) return;
            
            const rBody = document.getElementById('response-body');
            if (!query) {
                rBody.innerHTML = syntaxHighlight(cached.body);
                return;
            }

            try {
                let data = JSON.parse(cached.body);
                
                // Parse the query: handle multiple exclusions, main query, and pipes
                // We split by space but respect brackets and braces
                const parts = query.match(/(?:[^\s[\]{}]+|\[[^\]]*\]|\{[^}]*\})+/g) || [];
                
                let exclusions = [];
                let remaining = [];
                
                parts.forEach(p => {
                    if (p.startsWith('-')) exclusions.push(p.substring(1));
                    else remaining.push(p);
                });

                // 1. Apply Exclusions first on a clone of the data
                if (exclusions.length > 0) {
                    data = JSON.parse(JSON.stringify(data));
                    exclusions.forEach(path => excludePath(data, path));
                }

                // If only exclusions were provided, show the result
                if (remaining.length === 0) {
                    rBody.innerHTML = syntaxHighlight(data);
                    return;
                }

                // Join the rest back to handle piping correctly
                const restQuery = remaining.join(' ');
                
                // 2. Split Path and Pipes
                const pipeParts = restQuery.split('|').map(s => s.trim());
                let mainQuery = pipeParts.shift();
                
                // 3. Resolve Path (including Deep Search **)
                let result = resolveAdvancedPath(data, mainQuery);

                // 4. Apply Pipes
                pipeParts.forEach(pipe => {
                    if (pipe) result = applyPipe(result, pipe);
                });

                rBody.innerHTML = (result !== undefined && result !== null) ? syntaxHighlight(result) : '<div style="color:var(--method-delete)">No matches found</div>';
            } catch (e) {
                rBody.textContent = "Filter Error: " + e.message;
            }
        }

        function resolveAdvancedPath(data, query) {
            if (!query || query === '.') return data;
            
            // Handle Deep Search prefix **.key
            if (query.startsWith('**.')) {
                const targetKey = query.substring(3);
                return deepFindAll(data, targetKey);
            }

            let current = data;
            // Split by dots, but ignore dots inside brackets [] or braces {}
            const segments = query.match(/(?:[^{}.[\]]+|\{[^}]*\}|\[[^\]]*\])+/g) || [];

            for (let seg of segments) {
                if (current === undefined || current === null) return undefined;

                // Check for Predicate [key=val] or [key>val]
                if (seg.includes('[')) {
                    const match = seg.match(/([^\[]+)\[(.*)\]/);
                    if (match) {
                        const key = match[1];
                        const predicate = match[2];
                        current = current[key];
                        if (Array.isArray(current)) {
                            current = applyPredicate(current, predicate);
                        }
                        continue;
                    }
                }

                // Check for Projection {a, b}
                if (seg.includes('{')) {
                    const match = seg.match(/([^{]+)\{(.*)\}/);
                    if (match) {
                        const key = match[1];
                        const fields = match[2].split(',').map(f => f.trim());
                        current = current[key];
                        current = applyProjection(current, fields);
                        continue;
                    } else if (seg.startsWith('{')) {
                        // Root projection
                        const fields = seg.substring(1, seg.length - 1).split(',').map(f => f.trim());
                        current = applyProjection(current, fields);
                        continue;
                    }
                }

                current = current[seg];
            }
            return current;
        }

        function applyPredicate(arr, predicate) {
            const operators = ['>=', '<=', '!=', '=', '>', '<'];
            let op = operators.find(o => predicate.includes(o));
            if (!op) return arr;

            const [key, val] = predicate.split(op).map(s => s.trim().replace(/['"]/g, ''));
            
            return arr.filter(item => {
                if (!item || typeof item !== 'object') return false;
                const itemVal = item[key];
                
                // Compare
                switch(op) {
                    case '=':  return String(itemVal) == val;
                    case '!=': return String(itemVal) != val;
                    case '>':  return Number(itemVal) > Number(val);
                    case '<':  return Number(itemVal) < Number(val);
                    case '>=': return Number(itemVal) >= Number(val);
                    case '<=': return Number(itemVal) <= Number(val);
                }
                return false;
            });
        }

        function applyProjection(data, fields) {
            const project = (obj) => {
                if (!obj || typeof obj !== 'object') return obj;
                let res = {};
                fields.forEach(f => { if (obj.hasOwnProperty(f)) res[f] = obj[f]; });
                return res;
            };

            if (Array.isArray(data)) return data.map(project);
            return project(data);
        }

        function applyPipe(data, pipeStr) {
            const [name, ...args] = pipeStr.split(':');
            const arg = args.join(':');

            switch(name.toLowerCase()) {
                case 'count': 
                    return Array.isArray(data) ? data.length : (typeof data === 'object' ? Object.keys(data).length : 0);
                case 'first':
                    return Array.isArray(data) ? data[0] : data;
                case 'last':
                    return Array.isArray(data) ? data[data.length - 1] : data;
                case 'flatten':
                    return flattenObject(data);
                case 'sort':
                    if (!Array.isArray(data)) return data;
                    return [...data].sort((a, b) => {
                        const valA = arg ? a[arg] : a;
                        const valB = arg ? b[arg] : b;
                        return (valA > valB) ? 1 : -1;
                    });
                case 'keys':
                    return (data && typeof data === 'object') ? Object.keys(data) : [];
                default:
                    return data;
            }
        }

        function flattenObject(obj, prefix = '') {
            let res = {};
            for (let k in obj) {
                const propName = prefix ? prefix + '.' + k : k;
                if (typeof obj[k] === 'object' && obj[k] !== null && !Array.isArray(obj[k])) {
                    Object.assign(res, flattenObject(obj[k], propName));
                } else {
                    res[propName] = obj[k];
                }
            }
            return res;
        }

        function deepFindAll(obj, targetKey, res = []) {
            if (!obj || typeof obj !== 'object') return res;
            if (obj.hasOwnProperty(targetKey)) res.push(obj[targetKey]);
            
            for (let k in obj) {
                if (typeof obj[k] === 'object') deepFindAll(obj[k], targetKey, res);
            }
            return res;
        }

        function deepExclude(obj, targetKey) {
            if (!obj || typeof obj !== 'object') return;
            
            if (Array.isArray(obj)) {
                obj.forEach(item => deepExclude(item, targetKey));
                return;
            }

            if (obj.hasOwnProperty(targetKey)) {
                delete obj[targetKey];
            }

            for (let k in obj) {
                if (typeof obj[k] === 'object') {
                    deepExclude(obj[k], targetKey);
                }
            }
        }

        function excludePath(obj, path) {
            if (path.startsWith('**.')) {
                const targetKey = path.substring(3);
                deepExclude(obj, targetKey);
                return;
            }
            const parts = path.split('.');
            const last = parts.pop();
            let current = obj;
            for (const p of parts) {
                if (current && current.hasOwnProperty(p)) current = current[p];
                else return;
            }
            if (current && typeof current === 'object') {
                if (Array.isArray(current)) {
                    current.forEach(item => { if (item && typeof item === 'object') delete item[last]; });
                } else {
                    delete current[last];
                }
            }
        }

        function showImportModal() { document.getElementById('import-modal').classList.add('show'); }
        async function importCurl() {
            const curl = document.getElementById('curl-input').value;
            try { await callApi('/api/import/curl', 'POST', { curl }); closeModal('import-modal'); await init(); showToast("Imported!", "success"); } catch (e) { showToast("Import failed: " + e.message, "error"); }
        }

        let proxyInterval = null;

        async function toggleProxy() {
            const btn = document.getElementById('proxy-btn');
            const isRunning = btn.textContent.includes('Stop');
            
            if (!isRunning) {
                const port = prompt("Enter Proxy Port:", "8081");
                if (!port) return;
                try { await callApi('/api/proxy/start', 'POST', { port: parseInt(port) });
                    btn.innerHTML = '<i data-lucide="radio"></i> Stop Proxy';
                    btn.style.color = 'var(--method-delete)';
                    proxyInterval = setInterval(init, 5000);
                    showToast(`Proxy started on port ${port}.`, "success");
                    lucide.createIcons();
                } catch (e) { showToast("Failed to start proxy: " + e.message, "error"); }
            } else {
                try { await callApi('/api/proxy/stop', 'POST');
                    btn.innerHTML = '<i data-lucide="radio"></i> Start Proxy';
                    btn.style.color = 'var(--text-secondary)';
                    if (proxyInterval) clearInterval(proxyInterval);
                    lucide.createIcons();
                } catch (e) { showToast("Failed to stop proxy: " + e.message, "error"); }
            }
        }

        async function runFuzzer() {
            if (!currentRequest) return showToast("Select a request first", "error");
            const tbody = document.getElementById('fuzz-results-body');
            tbody.innerHTML = '<tr><td colspan="4" style="text-align:center; padding:20px; color:var(--accent);">Scanning injection points...</td></tr>';
            
            try {
                const results = await callApi('/api/fuzz', 'POST', { path: currentRequest.path });
                tbody.innerHTML = '';
                results.forEach(res => {
                    const tr = document.createElement('tr');
                    tr.style.borderBottom = '1px solid var(--border-color)';
                    let statusColor = res.statusCode >= 500 ? 'var(--method-delete)' : (res.statusCode >= 400 ? 'var(--method-post)' : 'var(--method-get)');
                    if (res.error) statusColor = 'var(--method-delete)';
                    
                    tr.innerHTML = `
                        <td style="padding:8px; color:var(--accent); font-weight:600;">${res.field}</td>
                        <td style="padding:8px; font-family:monospace; opacity:0.8;">${res.payload}</td>
                        <td style="padding:8px; color:${statusColor}; font-weight:700;">${res.statusCode || 'ERR'}</td>
                        <td style="padding:8px; color:var(--text-secondary);">${res.responseTime}ms</td>
                    `;
                    tbody.appendChild(tr);
                });
                if (results.length === 0) tbody.innerHTML = '<tr><td colspan="4" style="text-align:center; padding:20px;">No injectable parameters found.</td></tr>';
            } catch (e) { showToast("Scan failed: " + e.message, "error"); }
        }

        async function visualizeLastWorkflow() {
            const logsCont = document.getElementById('workflow-logs-content');
            const lines = Array.from(logsCont.children);
            if (lines.length === 0) return showToast("No workflow execution logs found", "error");

            let mermaidCode = "sequenceDiagram\n    autonumber\n    participant Workflow\n    participant PostIt\n    participant API\n";
            lines.forEach(line => {
                const text = line.innerText;
                const match = text.match(/\[ (.*) \] (.*)/);
                if (match) {
                    const status = match[1];
                    const name = match[2].split(' - ')[0];
                    mermaidCode += `    Workflow->>PostIt: Step: ${name}\n`;
                    mermaidCode += `    PostIt->>API: Execute ${name}\n`;
                    mermaidCode += `    API-->>PostIt: ${status}\n`;
                    mermaidCode += `    PostIt-->>Workflow: Next Step\n`;
                }
            });

            renderDiagram(mermaidCode);
        }

        async function visualizeHistory() {
            const history = await callApi('/api/history');
            if (history.length === 0) return showToast("History is empty", "error");
            
            let mermaidCode = "sequenceDiagram\n    autonumber\n    participant User\n    participant PostIt\n    participant API\n";
            history.forEach(item => {
                const name = item.path.split(' > ').pop();
                mermaidCode += `    User->>PostIt: Send ${name}\n`;
                mermaidCode += `    PostIt->>API: ${item.method} ${item.url}\n`;
                mermaidCode += `    API-->>PostIt: ${item.statusCode} (${item.duration}ms)\n`;
                mermaidCode += `    PostIt-->>User: Show Response\n`;
            });

            renderDiagram(mermaidCode);
        }

        async function renderDiagram(code) {
            const container = document.getElementById('mermaid-container');
            container.innerHTML = `<div class="mermaid">${code}</div>`;
            document.getElementById('architecture-modal').classList.add('show');
            await mermaid.run();
        }

        function exportDiagramSVG() {
            const svg = document.querySelector('#mermaid-container svg');
            if (!svg) return;
            const svgData = new XMLSerializer().serializeToString(svg);
            const blob = new Blob([svgData], { type: 'image/svg+xml;charset=utf-8' });
            const url = URL.createObjectURL(blob);
            const link = document.createElement('a');
            link.href = url;
            link.download = 'architecture.svg';
            link.click();
        }

        async function runDataIteration() {
            if (!currentRequest) return showToast("Select a request first", "error");
            const fileInput = document.getElementById('runner-file-input');
            if (!fileInput.files.length) return showToast("Select a data file first", "error");
            
            const file = fileInput.files[0];
            const text = await file.text();
            let data = [];

            try {
                if (file.name.endsWith('.json')) {
                    data = JSON.parse(text);
                } else if (file.name.endsWith('.csv')) {
                    data = parseCSV(text);
                } else {
                    return showToast("Unsupported file format (use .csv or .json)", "error");
                }
            } catch (e) { return showToast("Failed to parse file: " + e.message, "error"); }

            if (!Array.isArray(data) || data.length === 0) return showToast("File is empty or not an array", "error");

            const tbody = document.getElementById('runner-results-body');
            tbody.innerHTML = '<tr><td colspan="3" style="text-align:center; padding:20px; color:var(--accent);">Running iterations...</td></tr>';

            try {
                const results = await callApi('/api/runner/run', 'POST', { path: currentRequest.path, data: data });
                tbody.innerHTML = '';
                results.forEach(res => {
                    const tr = document.createElement('tr');
                    tr.style.borderBottom = '1px solid var(--border-color)';
                    let statusColor = res.statusCode >= 400 ? 'var(--method-delete)' : 'var(--method-get)';
                    tr.innerHTML = `
                        <td style="padding:8px; color:var(--text-secondary);">${res.iteration}</td>
                        <td style="padding:8px; color:${statusColor}; font-weight:700;">${res.statusCode} ${res.statusText}</td>
                        <td style="padding:8px; color:var(--text-secondary);">${res.duration}ms</td>
                    `;
                    tbody.appendChild(tr);
                });
                showToast(`Finished ${results.length} iterations!`, "success");
            } catch (e) { showToast("Run failed: " + e.message, "error"); }
        }

        function parseCSV(text) {
            const lines = text.split('\n').map(l => l.trim()).filter(l => l !== '');
            if (lines.length < 2) return [];
            const headers = lines[0].split(',').map(h => h.trim());
            return lines.slice(1).map(line => {
                const values = line.split(',').map(v => v.trim());
                const obj = {};
                headers.forEach((h, i) => obj[h] = values[i] || "");
                return obj;
            });
        }

        async function fetchGraphQLSchema() {
            const url = document.getElementById('req-url-input').value;
            if (!url) return showToast("Enter URL first", "error");
            const browser = document.getElementById('graphql-schema-browser');
            browser.innerHTML = '<div style="color:var(--accent)">Introspecting...</div>';
            
            try {
                const data = await callApi('/api/graphql/introspection', 'POST', { url });
                renderGraphQLSchema(data.data.__schema);
                showToast("Schema loaded!", "success");
            } catch (e) { 
                browser.innerHTML = `<div style="color:var(--method-delete)">Failed: ${e.message}</div>`;
                showToast("Introspection failed", "error"); 
            }
        }

        function renderGraphQLSchema(schema) {
            const browser = document.getElementById('graphql-schema-browser');
            browser.innerHTML = '';
            if (!schema || !schema.types) return;

            const types = schema.types.filter(t => !t.name.startsWith('__') && t.kind !== 'SCALAR');
            types.forEach(t => {
                const div = document.createElement('div');
                div.style = 'margin-bottom: 8px; border-bottom: 1px solid var(--border-color); padding-bottom: 4px;';
                div.innerHTML = `
                    <div style="font-weight:700; color:var(--accent);">${t.name} <span style="font-weight:400; font-size:9px; color:var(--text-secondary); opacity:0.6;">(${t.kind})</span></div>
                    <div style="font-size:10px; color:var(--text-secondary); margin-top:2px;">${t.description || ''}</div>
                `;
                browser.appendChild(div);
            });
        }

        async function updateProxyStatus() {
            const data = await callApi('/api/proxy/status');
            const btn = document.getElementById('proxy-btn');
            if (data.running) {
                btn.innerHTML = '<i data-lucide="radio"></i> Stop Proxy';
                btn.style.color = 'var(--method-delete)';
            } else {
                btn.innerHTML = '<i data-lucide="radio"></i> Start Proxy';
                btn.style.color = 'var(--text-secondary)';
            }
            lucide.createIcons();
        }

        init();
        fetchActiveEnv();
        fetchEnvironments();

        const sidebarResizer = document.getElementById('resizer-sidebar');
        const responseResizer = document.getElementById('resizer-response');
        const sidebar = document.getElementById('sidebar');
        const responseContainer = document.getElementById('response-container');
        let isResizingSidebar = false; let isResizingResponse = false;
        sidebarResizer.onmousedown = () => isResizingSidebar = true;
        responseResizer.onmousedown = () => isResizingResponse = true;
        document.onmousemove = (e) => {
            if (isResizingSidebar) { sidebar.style.width = Math.max(200, Math.min(600, e.clientX)) + 'px'; }
            if (isResizingResponse) { responseContainer.style.height = Math.max(100, Math.min(window.innerHeight - 200, window.innerHeight - e.clientY)) + 'px'; }
        };
        document.onmouseup = () => { isResizingSidebar = false; isResizingResponse = false; };
