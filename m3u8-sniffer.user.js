// ==UserScript==
// @name         M3U8 资源终极嗅探器
// @namespace    https://github.com/Orochi-Adde/m3u8-downloader
// @version      1.0.2
// @description  m3u8-downloader 专属解析适配，智能突破防爬限制，原生零损耗拉伸
// @author       Orochi-Adde
// @match        *://*/*
// @grant        GM_setClipboard
// @grant        GM_xmlhttpRequest
// @grant        GM_setValue
// @grant        GM_getValue
// @connect      *
// @run-at       document-start
// @license      MIT
// @homepageURL  https://github.com/Orochi-Adde/m3u8-downloader
// @supportURL   https://github.com/Orochi-Adde/m3u8-downloader/issues
// @downloadURL  https://raw.githubusercontent.com/Orochi-Adde/m3u8-downloader/main/m3u8-sniffer.user.js
// @updateURL    https://raw.githubusercontent.com/Orochi-Adde/m3u8-downloader/main/m3u8-sniffer.user.js
// @run-at       document-start
// ==UserScript/==

(function() {
    'use strict';

    const isTopWindow = window === window.top;
    const m3u8List = new Set();
    let uiContainer = null;
    let minimizedIcon = null;
    let tbodyElement = null;
    let domObserver = null;
    let isForceParseMode = false;
    let isUiMinimized = false;

    const COLORS = {
        main: '#27ae60', ad: '#e74c3c', host: '#aaaaaa', fileNormal: '#85c1e9', 
        fileMaster: '#f39c12', child: '#1abc9c', btnParse: '#8e44ad', warning: '#f1c40f', safe: '#2ecc71'
    };

    // ==========================================
    // 🌟 核心新增：底层网络 API 劫持 (Monkey Patching)
    // 解决延时加载、点击播放、广告后加载的核心武器
    // ==========================================
    function hijackNetwork() {
        // 1. 劫持 Fetch API
        const originalFetch = window.fetch;
        window.fetch = async function(...args) {
            try {
                const url = args[0] instanceof Request ? args[0].url : args[0];
                if (typeof url === 'string' && url.includes('.m3u8')) {
                    // 只要代码发起了 m3u8 请求，瞬间捕获！
                    processSniffedUrl(url, false); 
                }
            } catch (e) { console.error("Fetch Intercept Error", e); }
            return originalFetch.apply(this, args);
        };

        // 2. 劫持 XMLHttpRequest (XHR)
        const originalXhrOpen = XMLHttpRequest.prototype.open;
        XMLHttpRequest.prototype.open = function(method, url, ...rest) {
            try {
                if (typeof url === 'string' && url.includes('.m3u8')) {
                    // 兼容老式 Ajax 请求
                    processSniffedUrl(url, false);
                }
            } catch (e) { console.error("XHR Intercept Error", e); }
            return originalXhrOpen.call(this, method, url, ...rest);
        };
    }

    // 必须在 document-start 阶段立刻执行，抢在网页自身 JS 运行之前！
    hijackNetwork();

    // --- 读取本地持久化配置 ---
    let savedProxyEnable = false;
    let savedProxyUrl = 'socks5://127.0.0.1:10808';
    try {
        if (typeof GM_getValue !== 'undefined') {
            savedProxyEnable = GM_getValue('gemini_proxy_enable', false);
            savedProxyUrl = GM_getValue('gemini_proxy_url', 'socks5://127.0.0.1:10808');
        }
    } catch (e) {}

    function safeSaveConfig(key, value) {
        try { if (typeof GM_setValue !== 'undefined') GM_setValue(key, value); } catch (e) {}
    }

    function getAutoFilename() {
        try {
            let host = window.location.hostname.replace(/^www\./i, '');
            let paths = window.location.pathname.split('/').filter(p => p.trim() !== '');
            let lastDir = paths.length > 0 ? paths[paths.length - 1] : 'video';
            lastDir = decodeURIComponent(lastDir).replace(/[\\/:*?"<>|]/g, '_');
            return `[${host}] ${lastDir}`;
        } catch (e) {
            return `video_${Math.floor(Date.now()/1000)}`;
        }
    }

    function makeRequest(url, strategy) {
        return new Promise((resolve, reject) => {
            GM_xmlhttpRequest({ 
                method: "GET", url: url, headers: strategy.headers, anonymous: strategy.anonymous, 
                onload: resolve, onerror: reject 
            });
        });
    }

    function toggleUI(action) {
        if (!uiContainer || !minimizedIcon) return;
        
        if (action === 'show') {
            isUiMinimized = false;
            uiContainer.style.setProperty('display', 'flex', 'important');
            minimizedIcon.style.setProperty('display', 'none', 'important');
        } else if (action === 'minimize') {
            isUiMinimized = true;
            uiContainer.style.setProperty('display', 'none', 'important');
            minimizedIcon.style.setProperty('display', 'flex', 'important');
        } else if (action === 'close') {
            isUiMinimized = true;
            uiContainer.style.setProperty('display', 'none', 'important');
            minimizedIcon.style.setProperty('display', 'none', 'important');
        } else {
            if (uiContainer.style.display !== 'none') toggleUI('minimize'); else toggleUI('show');
        }
    }

    async function smartParseM3u8(url, parentTr, parseBtn, tdFile) {
        parseBtn.disabled = true;
        const baseHeaders = { 'Cache-Control': 'no-cache, no-store, must-revalidate' };
        const strategies = [
            { id: 'bare', name: '裸解析', anonymous: true, headers: { ...baseHeaders } },
            { id: 'ref', name: '加 Referer', anonymous: true, headers: { ...baseHeaders, 'Referer': window.location.href, 'Origin': window.location.origin } },
            { id: 'cookie', name: '加 Cookie', anonymous: false, headers: { ...baseHeaders, 'Referer': window.location.href, 'Origin': window.location.origin } },
            { id: 'headers', name: '全量 Headers', anonymous: false, headers: { ...baseHeaders, 'Referer': window.location.href, 'Origin': window.location.origin, 'User-Agent': navigator.userAgent, 'Accept': '*/*' } }
        ];

        let successRes = null, usedStrategy = null;
        for (let strategy of strategies) {
            parseBtn.innerText = `试[${strategy.id}]..`;
            parseBtn.style.background = '#34495e';
            try {
                const res = await makeRequest(url, strategy);
                if (res.status >= 200 && res.status < 300) { successRes = res; usedStrategy = strategy; break; }
            } catch (e) {}
        }

        if (successRes) {
            const lines = successRes.responseText.split('\n');
            const results = [];
            let isMaster = false, lastResolution = '默认画质';

            for (let i = 0; i < lines.length; i++) {
                const line = lines.length > i ? lines[i].trim() : '';
                if (line.startsWith('#EXT-X-STREAM-INF')) {
                    isMaster = true;
                    const resMatch = line.match(/RESOLUTION=(\d+x\d+)/);
                    lastResolution = resMatch ? resMatch[1] : '未知画质';
                } else if (!line.startsWith('#') && line.length > 0) {
                    // 🌟 修复核心：只要不是注释且不为空，即为有效的子 m3u8 链接（兼容带 query 参数的链接）
                    isMaster = true;
                    try { 
                        results.push({ url: new URL(line, url).href, res: lastResolution }); 
                        lastResolution = '默认画质'; 
                    } catch(e) {}
                }
            }

            let hintDiv = tdFile.querySelector('.gemini-hint');
            if (!hintDiv) {
                hintDiv = document.createElement('div');
                hintDiv.className = 'gemini-hint';
                hintDiv.style.cssText = `font-size: 10px; margin-top: 4px; border-top: 1px dashed #555; padding-top: 2px; line-height: 1.2; white-space: normal;`;
                tdFile.appendChild(hintDiv);
            }

            if (usedStrategy.id !== 'bare') {
                if (['ref', 'cookie', 'headers'].includes(usedStrategy.id)) document.getElementById('chk-ref').checked = true;
                if (['cookie', 'headers'].includes(usedStrategy.id)) document.getElementById('chk-cookie').checked = true;
                if (usedStrategy.id === 'headers') document.getElementById('chk-ua').checked = true;
                
                hintDiv.style.color = COLORS.warning;
                hintDiv.innerHTML = `🛡️ <b>防爬拦截</b>: 已验证必须挂载 <b>[${usedStrategy.name}]</b> 参数`;
            } else {
                document.getElementById('chk-ref').checked = false;
                document.getElementById('chk-cookie').checked = false;
                document.getElementById('chk-ua').checked = false;
                hintDiv.style.color = COLORS.safe;
                hintDiv.innerHTML = `✅ <b>纯净资源</b>: 无任何防盗链，可直接裸连下载`;
            }

            if (isMaster && results.length > 0) {
                parseBtn.innerText = '✔ 展开嵌套';
                parseBtn.style.background = COLORS.main;
                for (let i = results.length - 1; i >= 0; i--) {
                    if (!m3u8List.has(results[i].url)) {
                        m3u8List.add(results[i].url);
                        addRowToTable(results[i].url, false, { isChild: true, res: results[i].res, insertAfter: parentTr, inheritedStrategy: usedStrategy });
                    }
                }
            } else {
                parseBtn.innerText = '底层文件';
                parseBtn.style.background = '#7f8c8d';
            }
        } else {
            parseBtn.innerText = '❌ 防爬极严';
            parseBtn.style.background = COLORS.ad;
        }
        setTimeout(() => { parseBtn.innerText = '🔍 探测解析'; parseBtn.style.background = COLORS.btnParse; parseBtn.disabled = false; }, 3000);
    }

    function getTitleHTML(count) {
        return `
            🔍 M3U8 嗅探列表 (${count}) 
            <span style="font-size: 10px; background: #8e44ad; color: #fff; padding: 2px 6px; border-radius: 4px; margin-left: 8px; font-weight: normal; user-select: none;">m3u8-downloader专用解析</span>
            <span style="font-size: 10px; color: #7f8c8d; font-weight: normal; margin-left: 6px; user-select: none;">(Alt+M 显隐)</span>
        `;
    }

    function initUI() {
        if (!isTopWindow || uiContainer || !document.documentElement) return;
        
        minimizedIcon = document.createElement('div');
        minimizedIcon.style.cssText = `
            display: none !important; position: fixed !important; top: 15% !important; right: 20px !important; 
            z-index: 2147483647 !important; width: 44px; height: 44px; border-radius: 50%; 
            background: rgba(39, 174, 96, 0.85); backdrop-filter: blur(5px); color: white; 
            justify-content: center; align-items: center; cursor: pointer; font-size: 20px; 
            box-shadow: 0 4px 15px rgba(0,0,0,0.5); border: 2px solid #2ecc71; transition: transform 0.2s;
            user-select: none;
        `;
        minimizedIcon.innerHTML = '🔍';
        minimizedIcon.title = 'M3U8 嗅探器 (点击展开)';
        minimizedIcon.onmouseover = () => { minimizedIcon.style.transform = 'scale(1.1)'; };
        minimizedIcon.onmouseout = () => { minimizedIcon.style.transform = 'scale(1)'; };
        minimizedIcon.onclick = () => toggleUI('show');
        document.documentElement.appendChild(minimizedIcon);

        uiContainer = document.createElement('div');
        uiContainer.style.cssText = `
            position: fixed !important; top: 10% !important; right: 20px !important; z-index: 2147483647 !important; 
            background: rgba(18, 18, 18, 0.95) !important; color: #d4d4d4 !important; padding: 15px !important; 
            border-radius: 8px !important; box-shadow: 0 8px 24px rgba(0, 0, 0, 0.6) !important; 
            border: 1px solid #333 !important; 
            width: clamp(380px, 45vw, 620px); height: 450px; 
            min-width: 380px !important; min-height: 200px !important; 
            max-width: 95vw !important; max-height: 95vh !important; 
            resize: both !important; overflow: hidden !important; 
            font-family: Consolas, monospace !important; font-size: 12px !important; 
            flex-direction: column !important; gap: 10px !important; backdrop-filter: blur(10px);
            display: none !important;
        `;

        const header = document.createElement('div');
        header.style.cssText = 'display: flex; justify-content: space-between; align-items: center; cursor: move; padding-bottom: 8px; border-bottom: 1px solid rgba(255,255,255,0.1); flex-shrink: 0;';
        
        const title = document.createElement('span');
        title.id = 'm3u8-sniffer-title';
        title.innerHTML = getTitleHTML(0);
        title.style.cssText = 'font-weight: bold; font-size: 14px; color: #fff; display: flex; align-items: center;';

        const btnGroup = document.createElement('div');
        btnGroup.style.display = 'flex';
        btnGroup.style.alignItems = 'center';

        const advancedBtn = document.createElement('span');
        advancedBtn.innerText = '⚙️ 参数构造器';
        advancedBtn.style.cssText = 'cursor: pointer; color: #f1c40f; font-size: 12px; margin-right: 15px; font-weight: bold; border-bottom: 1px dashed #f1c40f; user-select: none;';
        
        const minBtn = document.createElement('span');
        minBtn.innerText = '➖';
        minBtn.title = '最小化为图标';
        minBtn.style.cssText = 'cursor: pointer; color: #f1c40f; font-size: 14px; font-weight: bold; padding: 0 8px; transition: 0.2s; user-select: none;';
        minBtn.onmouseover = () => { minBtn.style.color = '#fff'; };
        minBtn.onmouseout = () => { minBtn.style.color = '#f1c40f'; };
        minBtn.onclick = () => toggleUI('minimize');

        const closeBtn = document.createElement('span');
        closeBtn.innerText = '✖';
        closeBtn.title = '彻底隐藏 (Alt+M 可唤醒)';
        closeBtn.style.cssText = 'cursor: pointer; color: #e74c3c; font-size: 14px; font-weight: bold; padding: 0 5px; transition: 0.2s; user-select: none;';
        closeBtn.onmouseover = () => { closeBtn.style.color = '#ff7675'; };
        closeBtn.onmouseout = () => { closeBtn.style.color = '#e74c3c'; };
        closeBtn.onclick = () => toggleUI('close');

        btnGroup.appendChild(advancedBtn);
        btnGroup.appendChild(minBtn);
        btnGroup.appendChild(closeBtn);
        header.appendChild(title);
        header.appendChild(btnGroup);

        const advancedPanel = document.createElement('div');
        advancedPanel.style.cssText = 'display: none; background: rgba(0,0,0,0.5); padding: 10px; border-radius: 6px; border: 1px dashed #7f8c8d; flex-direction: column; gap: 8px; margin-top: -5px; flex-shrink: 0;';
        
        advancedPanel.innerHTML = `
            <div style="display: flex; justify-content: space-between; align-items: center; border-bottom: 1px dashed #555; padding-bottom: 6px; margin-bottom: 2px;">
                <span style="color: #bdc3c7; font-weight: bold;">【后端 CLI 参数构造器】</span>
                <a href="https://github.com/Orochi-Adde/m3u8-downloader" target="_blank" style="color: #3498db; font-size: 10px; text-decoration: none; user-select: none;">🚀 [Orochi-Adde/m3u8-downloader]</a>
            </div>
            
            <div style="display:flex; gap: 12px; flex-wrap: wrap; margin-top: 5px;">
                <label style="cursor:pointer;" title="使用 -H 添加 Referer 头"><input type="checkbox" id="chk-ref"> 携带 Referer (-H)</label>
                <label style="cursor:pointer;" title="使用 -c 添加 Cookie"><input type="checkbox" id="chk-cookie"> 携带 Cookie (-c)</label>
                <label style="cursor:pointer;" title="使用 -H 添加全量 Header 和 UA"><input type="checkbox" id="chk-ua"> 携带全量 Headers / UA (-H)</label>
            </div>
            
            <div style="display:flex; align-items:center; gap: 8px; margin-top: 8px; border-top: 1px dashed #555; padding-top: 8px;">
                <label style="cursor:pointer; color:#f1c40f;" title="勾选后，复制的参数会带上后面的代理地址">
                    <input type="checkbox" id="chk-proxy" ${savedProxyEnable ? 'checked' : ''}> 启用全局代理 (-p)
                </label>
                <input type="text" id="gemini-proxy-url" value="${savedProxyUrl}" title="跨网页自动保存的代理地址" style="background:#111; border:1px solid #555; color:#fff; padding:3px 6px; border-radius:3px; font-family:Consolas; width:200px; outline:none;">
                <span id="gemini-proxy-save-hint" style="color: #2ecc71; font-size: 10px; display: none;">✔ 已保存</span>
            </div>

            <div style="margin-top: 5px; border-top: 1px dashed #555; padding-top: 8px;">
                <label style="cursor:pointer; color:#e67e22;"><input type="checkbox" id="gemini-force-parse"> 强制显示所有底层链接的"探测"按钮</label>
            </div>
        `;

        advancedBtn.onclick = () => { advancedPanel.style.display = advancedPanel.style.display === 'none' ? 'flex' : 'none'; };
        
        advancedPanel.addEventListener('change', (e) => {
            if (e.target.id === 'gemini-force-parse') {
                isForceParseMode = e.target.checked;
                uiContainer.querySelectorAll('.gemini-hidden-parse').forEach(btn => btn.style.display = isForceParseMode ? 'inline-block' : 'none');
            }
            if (e.target.id === 'chk-proxy') {
                safeSaveConfig('gemini_proxy_enable', e.target.checked);
                showSaveHint();
            }
        });

        let typeTimer;
        advancedPanel.addEventListener('input', (e) => {
            if (e.target.id === 'gemini-proxy-url') {
                clearTimeout(typeTimer);
                typeTimer = setTimeout(() => {
                    safeSaveConfig('gemini_proxy_url', e.target.value.trim());
                    showSaveHint();
                }, 500);
            }
        });

        function showSaveHint() {
            const hint = document.getElementById('gemini-proxy-save-hint');
            if (hint) {
                hint.style.display = 'inline-block';
                setTimeout(() => { hint.style.display = 'none'; }, 1500);
            }
        }

        const tableContainer = document.createElement('div');
        tableContainer.style.cssText = 'flex: 1; overflow-y: auto; min-height: 0; padding-right: 5px; margin-bottom: 4px;';

        const table = document.createElement('table');
        table.style.cssText = 'width: 100%; border-collapse: collapse; table-layout: fixed; text-align: left;';

        const thead = document.createElement('thead');
        thead.innerHTML = `
            <tr style="border-top: 2px solid ${COLORS.main}; border-bottom: 1px solid ${COLORS.main}; color: #fff;">
                <th style="padding: 6px 4px; width: 25%; position: sticky; top: 0; background: #121212; z-index: 1;">来源</th>
                <th style="padding: 6px 4px; width: 50%; position: sticky; top: 0; background: #121212; z-index: 1;">文件信息</th>
                <th style="padding: 6px 4px; width: 25%; text-align: right; position: sticky; top: 0; background: #121212; z-index: 1;">操作</th>
            </tr>
        `;

        tbodyElement = document.createElement('tbody');
        tbodyElement.style.cssText = `border-bottom: 2px solid ${COLORS.main};`;

        table.appendChild(thead);
        table.appendChild(tbodyElement);
        tableContainer.appendChild(table);

        uiContainer.appendChild(header);
        uiContainer.appendChild(advancedPanel);
        uiContainer.appendChild(tableContainer);
        document.documentElement.appendChild(uiContainer);

        let isDragging = false, currentX, currentY, initialX, initialY, xOffset = 0, yOffset = 0;
        header.addEventListener('mousedown', e => { initialX = e.clientX - xOffset; initialY = e.clientY - yOffset; if (e.target === header || e.target === title) isDragging = true; });
        document.addEventListener('mouseup', () => { initialX = currentX; initialY = currentY; isDragging = false; });
        document.addEventListener('mousemove', e => { if (isDragging) { e.preventDefault(); currentX = e.clientX - initialX; currentY = e.clientY - initialY; xOffset = currentX; yOffset = currentY; uiContainer.style.transform = `translate3d(${currentX}px, ${currentY}px, 0)`; } });
    }

    function addRowToTable(url, isAd, options = {}) {
        if (!tbodyElement) return;
        const { isChild = false, res = '', insertAfter = null, inheritedStrategy = null } = options;

        const tr = document.createElement('tr');
        tr.style.cssText = 'border-bottom: 1px dashed #333; transition: background 0.2s;';
        tr.onmouseover = () => { tr.style.background = 'rgba(255,255,255,0.05)'; };
        tr.onmouseout = () => { tr.style.background = 'transparent'; };

        const tdSource = document.createElement('td');
        tdSource.style.cssText = 'padding: 8px 4px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; vertical-align: top;';
        if (isChild) {
            tdSource.innerHTML = `<span style="color:${COLORS.child}; padding-left: 8px;">└─ 画质[${res}]</span>`;
        } else {
            let host = '未知';
            try { host = new URL(url).hostname; } catch(e){}
            const type = isAd ? `<span style="color:${COLORS.ad}">[广告]</span>` : `<span style="color:${COLORS.main}">[主站]</span>`;
            tdSource.innerHTML = `${type} <br><span style="color:${COLORS.host}; font-size:10px;">${host}</span>`;
        }

        const tdFile = document.createElement('td');
        tdFile.style.cssText = 'padding: 8px 4px; vertical-align: top; word-break: break-all;';
        tdFile.title = url; 
        
        let filename = 'video.m3u8';
        try { filename = new URL(url).pathname.split('/').pop() || 'video.m3u8'; } catch(e){}
        
        let isProbableMaster = false;
        let fileHtml = '';
        if (!isChild) {
            if (/(playlist|master|index)\.m3u8/i.test(url)) {
                isProbableMaster = true;
                fileHtml = `<span style="color:${COLORS.fileMaster}; font-weight:bold;">${filename}</span>`;
            } else {
                fileHtml = `<span style="color:${COLORS.fileNormal};">${filename}</span>`;
            }
        } else {
            fileHtml = `<span style="color:${COLORS.child};">${filename}</span>`;
        }
        
        const fileContentDiv = document.createElement('div');
        fileContentDiv.innerHTML = fileHtml;
        tdFile.appendChild(fileContentDiv);

        if (inheritedStrategy && inheritedStrategy.id !== 'bare') {
            const hintDiv = document.createElement('div');
            hintDiv.className = 'gemini-hint';
            hintDiv.style.cssText = `font-size: 10px; margin-top: 4px; border-top: 1px dashed #555; padding-top: 2px; line-height: 1.2; white-space: normal; color: ${COLORS.child};`;
            hintDiv.innerHTML = `🔄 默认沿用 <b>[${inheritedStrategy.name}]</b> 防爬参数`;
            tdFile.appendChild(hintDiv);
        }

        const tdAction = document.createElement('td');
        tdAction.style.cssText = 'padding: 6px 4px; text-align: right; white-space: nowrap; vertical-align: top;';
        
        const copyBtn = document.createElement('button');
        copyBtn.innerText = '复制参数';
        copyBtn.style.cssText = `background: ${COLORS.main}; color: white; border: none; padding: 4px 6px; border-radius: 4px; cursor: pointer; font-size: 11px; font-weight: bold; transition: background 0.2s; display: block; margin-bottom: 4px; width: 100%;`;
        
        copyBtn.onclick = () => {
            const outName = isChild ? `${getAutoFilename()}_${res}` : getAutoFilename();
            let cmdStr = ` -u "${url}" -n 32`;
            
            if (document.getElementById('chk-ref').checked) { cmdStr += ` -H "Referer: ${window.location.href}"`; }
            if (document.getElementById('chk-cookie').checked && document.cookie) { cmdStr += ` -c "${document.cookie}"`; }
            if (document.getElementById('chk-ua').checked) { cmdStr += ` -H "User-Agent: ${navigator.userAgent}"`; }
            
            if (document.getElementById('chk-proxy').checked) { 
                const proxyUrl = document.getElementById('gemini-proxy-url').value.trim();
                if (proxyUrl) { cmdStr += ` -p "${proxyUrl}"`; }
            }

            cmdStr += ` -o "${outName}"`;

            GM_setClipboard(cmdStr);
            const originTxt = copyBtn.innerText;
            copyBtn.innerText = '✔ 已复制'; copyBtn.style.background = '#e67e22';
            setTimeout(() => { copyBtn.innerText = originTxt; copyBtn.style.background = COLORS.main; }, 1500);
        };

        const parseBtn = document.createElement('button');
        parseBtn.innerText = '🔍 探测解析';
        if (isProbableMaster) {
            parseBtn.style.cssText = `background: ${COLORS.btnParse}; color: white; border: none; padding: 4px 6px; border-radius: 4px; cursor: pointer; font-size: 11px; font-weight: bold; width: 100%;`;
        } else {
            parseBtn.className = 'gemini-hidden-parse';
            parseBtn.style.cssText = `display: ${isForceParseMode ? 'inline-block' : 'none'}; background: ${COLORS.btnParse}; color: white; border: none; padding: 4px 6px; border-radius: 4px; cursor: pointer; font-size: 11px; font-weight: bold; width: 100%;`;
        }
        
        parseBtn.onclick = () => smartParseM3u8(url, tr, parseBtn, tdFile);

        tdAction.appendChild(copyBtn);
        tdAction.appendChild(parseBtn); 

        tr.appendChild(tdSource);
        tr.appendChild(tdFile);
        tr.appendChild(tdAction);
        
        if (insertAfter && insertAfter.parentNode) { insertAfter.after(tr); } else { tbodyElement.appendChild(tr); }
    }

    function processSniffedUrl(url, isAd = false) {
        if (m3u8List.has(url)) return;
        if (isTopWindow) {
            m3u8List.add(url);
            
            if (!uiContainer) {
                initUI();
                toggleUI('show');
            } else if (!isUiMinimized) {
                uiContainer.style.setProperty('display', 'flex', 'important');
            }

            addRowToTable(url, isAd);
            
            document.getElementById('m3u8-sniffer-title').innerHTML = getTitleHTML(m3u8List.size);
            if (minimizedIcon) minimizedIcon.title = `已抓取 ${m3u8List.size} 个链接 (点击展开)`;

            if (domObserver) { domObserver.disconnect(); domObserver = null; }
        } else {
            window.top.postMessage({ type: 'GEMINI_M3U8_SNIFFED', url: url, isAd: true }, '*');
        }
    }

    if (isTopWindow) {
        window.addEventListener('message', (event) => { if (event.data && event.data.type === 'GEMINI_M3U8_SNIFFED') { processSniffedUrl(event.data.url, event.data.isAd); }});
        
        document.addEventListener('keydown', (e) => { 
            if (e.altKey && e.key.toLowerCase() === 'm') { 
                if (!uiContainer) {
                    initUI(); 
                    toggleUI('show');
                } else {
                    toggleUI();
                }
            }
        });
    }

    function startPerformanceObserver() {
        const entries = performance.getEntriesByType('resource');
        entries.forEach(entry => { if (entry.name && entry.name.includes('.m3u8')) processSniffedUrl(entry.name); });
        if (typeof PerformanceObserver !== 'undefined') {
            const observer = new PerformanceObserver((list) => { for (const entry of list.getEntries()) { if (entry.name && entry.name.includes('.m3u8')) processSniffedUrl(entry.name); } });
            observer.observe({ entryTypes: ['resource'] });
        }
    }

    function startDOMObserver() {
        domObserver = new MutationObserver((mutations) => {
            for (let mutation of mutations) {
                if (mutation.addedNodes) { mutation.addedNodes.forEach(node => { if ((node.tagName === 'VIDEO' || node.tagName === 'SOURCE') && node.src && node.src.includes('.m3u8')) processSniffedUrl(node.src); }); }
                if (mutation.type === 'attributes') { const node = mutation.target; if ((node.tagName === 'VIDEO' || node.tagName === 'SOURCE') && node.src && node.src.includes('.m3u8')) processSniffedUrl(node.src); }
            }
        });
        const observeConfig = { childList: true, subtree: true, attributes: true, attributeFilter: ['src', 'data-src'] };
        if (document.body) { domObserver.observe(document.body, observeConfig); } else { window.addEventListener('DOMContentLoaded', () => domObserver.observe(document.body, observeConfig)); }
    }

    let scanTimeout = null;
    function debouncedDeepScanHTML() {
        if (scanTimeout) clearTimeout(scanTimeout);
        scanTimeout = setTimeout(() => {
            const htmlStr = document.documentElement.innerHTML;
            const regex = /(https?:\/\/[a-zA-Z0-9_./-]+\.m3u8[a-zA-Z0-9_./?=A-Z-]*)/ig;
            const matches = htmlStr.match(regex);
            if (matches) { matches.forEach(url => processSniffedUrl(url.replace(/\\/g, ''))); }
        }, 500); 
    }

    window.addEventListener('load', () => {
        startPerformanceObserver();
        startDOMObserver();
        debouncedDeepScanHTML();
    });

})();
