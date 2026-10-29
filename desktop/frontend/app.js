// Global State
let currentTab = 'dashboard';
let apiConfig = null;
let wsClient = null;
let latestPorts = null;
const localClientId = 'bp_client_' + Math.random().toString(36).substring(2, 10);
let saveConfigTimer = null;

// DOM Elements
const tabButtons = document.querySelectorAll('.nav-item');
const panels = document.querySelectorAll('.tab-panel');
const tabTitle = document.getElementById('tab-title');
const tabSubtitle = document.getElementById('tab-subtitle');
const statusText = document.getElementById('status-text');
const statusPulse = document.getElementById('status-pulse');
const shutdownBtn = document.getElementById('btn-shutdown');

// Tab Configuration Details
const tabDetails = {
    dashboard: { title: "Gösterge Paneli", subtitle: "Bağlantı durumunu izleyin ve telefonunuzu eşleştirin." },
    streamdeck: { title: "Stream Deck", subtitle: "Telefondaki buton ızgarasını özelleştirin." },
    monitor: { title: "Sanal 2. Monitör", subtitle: "Sanal ekran sürücüsünü yönetin ve yapılandırın." },
    webcam: { title: "Kamera Akışı", subtitle: "Telefonun kamera görüntüsünü izleyin ve yönlendirin." },
    files: { title: "Dosya Transferi", subtitle: "Bilgisayar ile telefon arasında dosya alışverişi yapın." },
    settings: { title: "Ayarlar", subtitle: "Uygulama tercihleri, sistem bilgileri ve güncelleme yönetimi." }
};

// Auth helpers
function getAuthPassword() {
    return localStorage.getItem("bigpocket_password") || "";
}

function setAuthPassword(pw) {
    localStorage.setItem("bigpocket_password", pw);
}

// Custom fetch wrapper that adds auth header and client ID
function authFetch(url, options = {}) {
    const isLocal = window.location.hostname === 'localhost' || 
                    window.location.hostname === '127.0.0.1' || 
                    window.location.hostname === '';
    const password = getAuthPassword();
    options.headers = options.headers || {};
    options.headers['X-Client-Id'] = localClientId;
    if (!(options.body instanceof FormData)) {
        if (!options.headers['Content-Type']) {
            options.headers['Content-Type'] = 'application/json';
        }
    }
    if (password) {
        options.headers['X-Password'] = password;
    }

    return fetch(url, options).then(res => {
        if (res.status === 401 && !isLocal) {
            showPasswordPrompt();
            throw new Error("Unauthorized");
        }
        return res;
    });
}

function showPasswordPrompt() {
    const isLocal = window.location.hostname === 'localhost' || 
                    window.location.hostname === '127.0.0.1' || 
                    window.location.hostname === '';
    if (isLocal) {
        // PC local access never prompts for a password
        return;
    }
    const modal = document.getElementById('password-prompt-modal');
    if (modal) modal.classList.add('active');
    const errEl = document.getElementById('prompt-error-msg');
    if (errEl) errEl.style.display = 'none';
}

function setupPasswordPromptHandlers() {
    const modal = document.getElementById('password-prompt-modal');
    const submitBtn = document.getElementById('btn-submit-prompt-password');
    const input = document.getElementById('prompt-password-input');

    submitBtn.addEventListener('click', () => {
        const pw = input.value;
        // Try to validate by fetching config with this password
        fetch('/config', {
            headers: { 'X-Password': pw }
        })
        .then(res => {
            if (res.status === 401) {
                document.getElementById('prompt-error-msg').style.display = 'block';
            } else {
                setAuthPassword(pw);
                modal.classList.remove('active');
                // Reload data and reconnect WebSocket
                fetchConfig();
                fetchFiles();
                checkDriverStatus();
                fetchSystemInfo();
                if (wsClient) {
                    wsClient.close();
                }
            }
        })
        .catch(err => {
            console.error("Auth validation failed", err);
        });
    });

    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
            submitBtn.click();
        }
    });
}

function setupDashboardPassword() {
    const savePwBtn = document.getElementById('btn-save-password');
    const passwordInput = document.getElementById('settings-password');

    // Populate current password in input
    passwordInput.value = getAuthPassword();

    savePwBtn.addEventListener('click', () => {
        const newPassword = passwordInput.value;
        if (apiConfig) {
            apiConfig.password = newPassword;
            authFetch('/config', {
                method: 'POST',
                body: JSON.stringify(apiConfig)
            })
            .then(res => res.json())
            .then(data => {
                if (data.success) {
                    setAuthPassword(newPassword);
                    alert("Erişim şifresi başarıyla güncellendi!");
                    fetchSystemInfo(); // Refresh pairing QR code with new password
                    updateMcpCardUI();
                }
            })
            .catch(err => {
                console.error("Failed to update password", err);
            });
        } else {
            alert("Sistem yapılandırması yüklenemedi. Lütfen sayfayı yenileyin.");
        }
    });
}

// ----------------- MCP / Integration Helpers -----------------
function buildMcpBaseUrl() {
    const host = window.location.hostname || '127.0.0.1';
    const proto = window.location.protocol === 'https:' ? 'https' : 'http';
    // The desktop server always exposes its HTTP/MCP API on port 8085.
    return `${proto}://${host}:8085`;
}

function updateMcpCardUI() {
    const base = buildMcpBaseUrl();
    const password = getAuthPassword();

    // 1) HTTP / SSE transport config (Cursor, Windsurf, Claude Code & other remote-capable clients)
    const httpServer = { url: `${base}/mcp` };
    if (password) {
        httpServer.headers = { "X-Password": password };
    }
    const httpConfig = JSON.stringify({
        mcpServers: { "bigpocket-streamdeck": httpServer }
    }, null, 2);

    const sseEl = document.getElementById('mcp-sse-json-config');
    if (sseEl) sseEl.textContent = httpConfig;

    // 2) Stdio transport config (Claude Desktop, Cursor local process)
    const stdioServer = {
        command: "C:\\Users\\ygt\\Desktop\\projeler\\bigpocket-main\\mcp\\bigpocket-streamdeck-mcp.exe",
        args: ["--url", base]
    };
    stdioServer.env = { BIGPOCKET_API_KEY: password || "" };
    const stdioConfig = JSON.stringify({
        mcpServers: { "bigpocket-streamdeck": stdioServer }
    }, null, 2);

    const stdioEl = document.getElementById('mcp-stdio-json-config');
    if (stdioEl) stdioEl.textContent = stdioConfig;
}

function copyTextToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
        return navigator.clipboard.writeText(text).then(() => true).catch(() => fallbackCopyText(text));
    }
    return Promise.resolve(fallbackCopyText(text));
}

function fallbackCopyText(text) {
    try {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.top = '-9999px';
        ta.setAttribute('readonly', '');
        document.body.appendChild(ta);
        ta.select();
        const ok = document.execCommand('copy');
        document.body.removeChild(ta);
        return ok;
    } catch (_) {
        return false;
    }
}

function setupMcpIntegration() {
    // Protocol switcher tabs
    const tabButtons = document.querySelectorAll('.mcp-tab-btn');
    tabButtons.forEach(btn => {
        btn.addEventListener('click', () => {
            const target = btn.getAttribute('data-mcptab');
            tabButtons.forEach(b => {
                const isActive = b === btn;
                b.classList.toggle('active', isActive);
                b.style.background = isActive ? 'var(--bg-surface-elevated)' : 'transparent';
                b.style.border = isActive ? '1px solid var(--border-color)' : '1px solid transparent';
                b.style.color = isActive ? 'var(--text-main)' : 'var(--text-muted)';
            });
            document.querySelectorAll('.mcp-tab-content').forEach(content => {
                content.style.display = content.id === `mcp-tab-content-${target}` ? 'block' : 'none';
            });
        });
    });

    // Copy buttons (block copy + inline copy)
    const flashLabel = (btn, ok) => {
        if (!btn.dataset.originalLabel) btn.dataset.originalLabel = btn.textContent;
        btn.textContent = ok ? 'Kopyalandı' : 'Kopyalanamadı';
        setTimeout(() => { btn.textContent = btn.dataset.originalLabel; }, 1400);
    };

    document.querySelectorAll('.btn-copy-code[data-target]').forEach(btn => {
        btn.addEventListener('click', () => {
            const el = document.getElementById(btn.getAttribute('data-target'));
            copyTextToClipboard(el ? el.textContent : '').then(ok => flashLabel(btn, ok));
        });
    });

    document.querySelectorAll('[data-copy]').forEach(btn => {
        btn.addEventListener('click', () => {
            copyTextToClipboard(btn.getAttribute('data-copy') || '').then(ok => flashLabel(btn, ok));
        });
    });

    // Windows autostart toggle (GET/POST /api/autostart)
    const autostartChk = document.getElementById('chk-autostart');
    if (autostartChk) {
        authFetch('/api/autostart')
            .then(res => res.json())
            .then(data => {
                const enabled = (data && (data.enabled === true || data.autostart === true));
                autostartChk.checked = !!enabled;
            })
            .catch(err => console.warn("Autostart durumu okunamadı", err));

        autostartChk.addEventListener('change', () => {
            const enable = autostartChk.checked;
            autostartChk.disabled = true;
            authFetch('/api/autostart', {
                method: 'POST',
                body: JSON.stringify({ enable: enable })
            })
            .then(res => res.json())
            .then(data => {
                if (data && data.success === false) {
                    autostartChk.checked = !enable;
                    alert(data.error || 'Otomatik başlatma ayarı değiştirilemedi.');
                }
            })
            .catch(err => {
                autostartChk.checked = !enable;
                alert('Otomatik başlatma ayarı değiştirilemedi: ' + err);
            })
            .finally(() => { autostartChk.disabled = false; });
        });
    }

    updateMcpCardUI();
}

function fetchConfig() {
    authFetch('/config')
        .then(res => res.json())
        .then(data => {
            if (data.success && data.config) {
                apiConfig = data.config;
                if (!apiConfig.stream_deck_rows) apiConfig.stream_deck_rows = 2;
                if (!apiConfig.stream_deck_cols) apiConfig.stream_deck_cols = 4;
                if (!apiConfig.stream_deck_pages || apiConfig.stream_deck_pages.length === 0) {
                    apiConfig.stream_deck_pages = [
                        { id: 0, title: "Ana Sayfa" },
                        { id: 1, title: "Medya & Ses" },
                        { id: 2, title: "Sistem & PC" }
                    ];
                }
                if (apiConfig.active_page === undefined) {
                    apiConfig.active_page = 0;
                }
                syncMatrixControlsUI();
                renderPageTabs();
                renderStreamDeckButtons();
                const betaToggle = document.getElementById('settings-beta-channel-toggle');
                if (betaToggle) betaToggle.checked = !!apiConfig.beta_channel;
            }
        })
        .catch(err => {
            console.error("Error fetching config:", err);
        });
}

// ============================================================
// Mikro-Etkileşimler: radyal spotlight takibi + monokrom tooltip
// (Yalnızca transform/opacity yazar; okuma+yazma rAF ile senkronlanır.)
// ============================================================
function setupCardSpotlight() {
    if (window.matchMedia('(hover: none), (pointer: coarse)').matches) return;
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;

    let frame = 0;
    let pendingEvent = null;

    const apply = () => {
        frame = 0;
        const e = pendingEvent;
        if (!e) return;
        const el = e.target instanceof Element ? e.target.closest('.card, .deck-btn') : null;
        if (!el) return;
        const rect = el.getBoundingClientRect();
        el.style.setProperty('--spot-x', (e.clientX - rect.left) + 'px');
        el.style.setProperty('--spot-y', (e.clientY - rect.top) + 'px');
    };

    document.addEventListener('pointermove', (e) => {
        pendingEvent = e;
        if (!frame) frame = requestAnimationFrame(apply);
    }, { passive: true });
}

function setupTooltips() {
    if (window.matchMedia('(hover: none), (pointer: coarse)').matches) return;

    const tip = document.createElement('div');
    tip.className = 'bp-tooltip';
    tip.setAttribute('role', 'tooltip');
    document.body.appendChild(tip);

    let active = null;
    let timer = 0;

    const hide = () => {
        clearTimeout(timer);
        timer = 0;
        tip.classList.remove('is-visible');
        active = null;
    };

    const place = (el) => {
        const text = el.getAttribute('data-tooltip');
        if (!text) { hide(); return; }
        tip.textContent = text;
        const rect = el.getBoundingClientRect();
        const tw = tip.offsetWidth;
        const th = tip.offsetHeight;
        let x = rect.left + rect.width / 2 - tw / 2;
        let y = rect.top - th - 10;
        if (y < 8) y = rect.bottom + 10;                       // üstte yer yoksa alta in
        x = Math.max(8, Math.min(x, window.innerWidth - tw - 8));
        tip.style.left = x + 'px';
        tip.style.top = y + 'px';
        tip.classList.add('is-visible');
    };

    document.addEventListener('pointerover', (e) => {
        const el = e.target instanceof Element ? e.target.closest('[data-tooltip]') : null;
        if (!el || el === active) return;
        clearTimeout(timer);
        active = el;
        timer = window.setTimeout(() => {
            timer = 0;
            if (active === el && el.isConnected) place(el);
        }, 200);
    });

    document.addEventListener('pointerout', (e) => {
        if (!active) return;
        const el = e.target instanceof Element ? e.target.closest('[data-tooltip]') : null;
        if (el !== active) return;
        const to = e.relatedTarget;
        if (to instanceof Node && active.contains(to)) return;
        hide();
    });

    document.addEventListener('pointerdown', hide, true);
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape') hide(); });
    window.addEventListener('scroll', hide, true);
    window.addEventListener('blur', hide);
}

// Initialize App
document.addEventListener('DOMContentLoaded', () => {
    checkMobileMode();
    setupTabNavigation();
    setupCardSpotlight();
    setupTooltips();
    setupDragAndDrop();
    setupPCClipboardSync();
    setupStreamDeck();
    initDeckStage();
    setupMonitorManager();
    setupMicDriverManager();
    setupWebcam();
    setupShutdown();
    setupPasswordPromptHandlers();
    setupDashboardPassword();
    setupMcpIntegration();
    setupIPSelectHandler();
    setupUsbBridge();
    
    // Fetch system info and configurations
    fetchSystemInfo();
    fetchConfig();
    fetchFiles();
    checkMicDriverStatus();
    syncPomodoroState();
    connectWebSocket();
});

function setupUsbBridge() {
    const adbBtn = document.getElementById('btn-setup-adb-reverse');
    if (adbBtn) {
        adbBtn.addEventListener('click', () => {
            adbBtn.textContent = "Kuruluyor...";
            adbBtn.disabled = true;
            authFetch('/api/usb/forward', { method: 'POST' })
                .then(r => r.json())
                .then(data => {
                    alert(data.message || (data.success ? "1ms USB Köprüsü Başarıyla Kuruldu!" : "ADB hatası"));
                    adbBtn.textContent = "1ms ADB Başlat";
                    adbBtn.disabled = false;
                })
                .catch(err => {
                    alert("USB köprüsü kurulamadı: " + err);
                    adbBtn.textContent = "1ms ADB Başlat";
                    adbBtn.disabled = false;
                });
        });
    }
}

// Setup Shutdown
function setupShutdown() {
    shutdownBtn.addEventListener('click', () => {
        if (confirm("BigPocket arka plan sunucusunu kapatmak istediğinize emin misiniz?")) {
            authFetch('/shutdown')
                .then(res => res.json())
                .then(data => {
                    alert("Sunucu kapatıldı. Tarayıcı penceresini kapatabilirsiniz.");
                    window.close();
                })
                .catch(err => {
                    // Fail gracefully as server shuts down immediately
                    alert("Kapatma komutu gönderildi.");
                });
        }
    });
}

// Fetch System Info
function fetchSystemInfo() {
    fetch('/system_info')
        .then(res => res.json())
        .then(info => {
            if (info.success) {
                latestPorts = info.ports;
                const ipSelect = document.getElementById('pc-ip-select');
                if (ipSelect) {
                    const currentSelected = ipSelect.value || info.ip;
                    ipSelect.innerHTML = '';
                    const ips = info.ips || [info.ip];
                    const uniqueIps = Array.from(new Set(ips));
                    
                    uniqueIps.forEach(ip => {
                        const opt = document.createElement('option');
                        opt.value = ip;
                        opt.style.backgroundColor = '#18181b';
                        opt.style.color = '#e2e2e9';
                        
                        let label = ip;
                        const isUsbIP = ip === info.usb_ip || 
                                        ip.startsWith("192.168.42.") || 
                                        ip.startsWith("192.168.43.") || 
                                        ip.startsWith("192.168.49.") || 
                                        ip.startsWith("192.168.225.") || 
                                        ip.startsWith("192.168.137.") || 
                                        ip.startsWith("172.20.10.");
                        if (isUsbIP) {
                            label += " (USB Tethering)";
                        } else if (ip.startsWith("192.168.")) {
                            label += " (Wi-Fi/LAN)";
                        } else if (ip !== "127.0.0.1") {
                            label += " (Yerel Ağ)";
                        }
                        opt.textContent = label;
                        ipSelect.appendChild(opt);
                    });
                    
                    if (uniqueIps.includes(currentSelected)) {
                        ipSelect.value = currentSelected;
                    } else if (uniqueIps.includes(info.ip)) {
                        ipSelect.value = info.ip;
                    } else if (uniqueIps.length > 0) {
                        ipSelect.value = uniqueIps[0];
                    }
                    
                    updatePairingQR(ipSelect.value, info.ports);
                } else {
                    updatePairingQR(info.ip, info.ports);
                }
                
                document.getElementById('port-ws').textContent = info.ports.control_ws;
                document.getElementById('port-cam').textContent = info.ports.camera_tcp;
                document.getElementById('port-audio').textContent = info.ports.audio_tcp;
                document.getElementById('port-screen').textContent = info.ports.screen_tcp;
                
                const adminWarning = document.getElementById('admin-warning');
                if (!info.is_admin) {
                    adminWarning.style.display = 'block';
                } else {
                    adminWarning.style.display = 'none';
                }

                const ramStat = document.getElementById('stat-ram');
                if (ramStat && info.mem_stats) {
                    ramStat.textContent = info.mem_stats.alloc_mb + ' / ' + info.mem_stats.sys_mb;
                }
                const osStat = document.getElementById('stat-os');
                if (osStat && info.hostname) {
                    osStat.textContent = info.hostname;
                }
            }
        })
        .catch(err => {
            console.error("Error fetching system info", err);
        });
}

function updatePairingQR(ip, ports) {
    const payload = JSON.stringify({ 
        ip: ip, 
        ports: ports,
        password: getAuthPassword()
    });
    const qrCode = document.getElementById('qr-code');
    if (qrCode) {
        qrCode.src = `/api/qr?data=${encodeURIComponent(payload)}`;
    }
}

// Tab Navigation
function setupTabNavigation() {
    tabButtons.forEach(btn => {
        btn.addEventListener('click', () => {
            const targetTab = btn.getAttribute('data-tab');
            
            // Update Active Buttons
            tabButtons.forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            
            // Show Panel
            panels.forEach(p => p.classList.remove('active'));
            document.getElementById(`panel-${targetTab}`).classList.add('active');
            
            // Update Headers
            tabTitle.textContent = tabDetails[targetTab].title;
            tabSubtitle.textContent = tabDetails[targetTab].subtitle;
            
            currentTab = targetTab;

            // The deck frame is re-measured whenever its tab becomes visible
            if (targetTab === 'streamdeck') refreshDeckStage();
            
            if (targetTab === 'files') {
                fetchFiles();
            } else if (targetTab === 'monitor') {
                checkDriverStatus();
            }
        });
    });
}

// WebSocket Connection (Checks mobile connection status)
let wsReconnectAttempts = 0;

function connectWebSocket() {
    // Go backend serves the websocket on port 8085 under path /ws
    const loc = window.location;
    const wsProto = loc.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = `${wsProto}//${loc.host}/ws`;
    
    console.log("Connecting to WebSocket:", wsUrl);
    wsClient = new WebSocket(wsUrl);

    wsClient.onopen = () => {
        console.log("WebSocket connection established");
        wsReconnectAttempts = 0;
    };

    wsClient.onmessage = (event) => {
        let msg;
        try {
            msg = JSON.parse(event.data);
        } catch (err) {
            console.warn("Ignoring malformed WebSocket payload.", err);
            return;
        }
        if (!msg || typeof msg !== 'object') return;
        if (msg.type === 'auth_required') {
            wsClient.send(JSON.stringify({
                type: 'auth_login',
                password: getAuthPassword()
            }));
        } else if (msg.type === 'auth_success') {
            console.log("WebSocket authenticated successfully");
        } else if (msg.type === 'auth_failed') {
            console.error("WebSocket authentication failed");
            showPasswordPrompt();
        } else if (msg.type === 'client_connected') {
            statusText.textContent = "BAĞLANTI AKTİF";
            statusPulse.className = "status-dot connected";
        } else if (msg.type === 'client_disconnected') {
            statusText.textContent = "BAĞLANTI BEKLENİYOR";
            statusPulse.className = "status-dot";
        } else if (msg.type === 'clipboard_sync') {
            const previewArea = document.getElementById('pc-clipboard-preview');
            if (previewArea) {
                previewArea.value = msg.text;
            }
        } else if (msg.type === 'flow_new') {
            if (window.bigpocketFlow && msg.message) {
                window.bigpocketFlow.handleIncomingMessage(msg.message);
            }
        } else if (msg.type === 'flow_cleared') {
            if (window.bigpocketFlow) {
                window.bigpocketFlow.handleClear();
            }
        } else if (msg.type === 'config_update') {
            if (msg.source === localClientId) {
                console.log("Ignoring local config_update echo from self");
                return;
            }
            console.log("Configuration updated from external client, reloading...");
            fetchConfig();
        } else if (msg.type === 'system_stats') {
            cachedLiveStats = {
                cpu: msg.cpu || 0,
                ram: msg.ram || 0,
                cpu_temp: msg.cpu_temp || 0,
                ram_used_gb: msg.ram_used_gb || 0,
                ram_total_gb: msg.ram_total_gb || 16,
                disk_percent: msg.disk_percent || 0,
                disk_free_gb: msg.disk_free_gb || 0,
                disk_total_gb: msg.disk_total_gb || 0,
                gpu: msg.gpu || 0,
                gpu_temp: msg.gpu_temp || 0,
                net_down_kbps: msg.net_down_kbps || 0,
                net_up_kbps: msg.net_up_kbps || 0,
                active_window: msg.active_window || "",
                active_process: msg.active_process || "",
                media_title: msg.media_title || "",
                media_artist: msg.media_artist || "",
                media_playing: !!msg.media_playing,
                weather_city: msg.weather_city || "Istanbul",
                weather_temp: msg.weather_temp || 0,
                weather_code: msg.weather_code || 0,
                weather_desc: msg.weather_desc || "",
                weather_wind: msg.weather_wind || 0
            };
            // Seed the per-city weather cache with the server's current city reading
            if (!weatherCache[normalizeCityKey(cachedLiveStats.weather_city)]) {
                weatherCache[normalizeCityKey(cachedLiveStats.weather_city)] = {
                    city: cachedLiveStats.weather_city,
                    temp: cachedLiveStats.weather_temp,
                    code: cachedLiveStats.weather_code,
                    desc: cachedLiveStats.weather_desc,
                    wind: cachedLiveStats.weather_wind
                };
            }
            const ramBadge = document.getElementById('stat-ram');
            if (ramBadge) {
                ramBadge.textContent = `%${msg.ram} (${cachedLiveStats.ram_used_gb} GB)`;
            }
            // Feed the rolling sparkline window, then update the widgets in place
            sampleTelemetryHistory();
            updateLiveStatsDOM();
        } else if (msg.type === 'button_state_changed') {
            if (apiConfig && apiConfig.stream_deck_buttons) {
                const b = apiConfig.stream_deck_buttons.find(btn => btn.id === msg.button_id);
                if (b) {
                    b.state = msg.state;
                    // In-place non-destructive update of the tonal action card
                    const btnEl = document.querySelector(`.deck-btn[data-id="${msg.button_id}"]`);
                    if (btnEl) {
                        const tile = btnEl.querySelector('.md-tile');
                        if (tile) tile.classList.toggle('is-on', !!msg.state);
                        const stateEl = btnEl.querySelector('.md-tile__state');
                        if (stateEl) stateEl.textContent = msg.state ? 'Açık' : 'Kapalı';
                    }
                }
            }
        } else if (msg.type === 'page_switched') {
            if (apiConfig && typeof msg.active_page === 'number' && apiConfig.active_page !== msg.active_page) {
                apiConfig.active_page = msg.active_page;
                renderPageTabs();
                renderStreamDeckButtons();
            }
        } else if (msg.type === 'volume_changed') {
            const volPct = Math.round(msg.level || 0);
            document.querySelectorAll('.apple-pill-slider-container').forEach(c => {
                const fill = c.querySelector('.apple-pill-fill.accent-volume');
                const val = c.querySelector('.apple-pill-value');
                if (fill && val) {
                    fill.style.width = `${volPct}%`;
                    val.textContent = `${volPct}%`;
                }
            });
        } else if (msg.type === 'app_volume_changed') {
            const appKey = String(msg.name || '').toLowerCase();
            if (appKey) {
                const appPct = Math.round(msg.volume || 0);
                if (audioSessionsCache[appKey]) {
                    audioSessionsCache[appKey].volume = appPct;
                } else {
                    audioSessionsCache[appKey] = { name: appKey, title: prettyAppLabel(appKey), volume: appPct, active: false };
                }
                document.querySelectorAll('.apple-pill-track[data-app-key]').forEach(track => {
                    if (track.getAttribute('data-app-key') === appKey) paintAppVolumePill(track, appPct);
                });
            }
        } else if (msg.type === 'brightness_changed') {
            const brPct = Math.round(msg.level || 0);
            document.querySelectorAll('.apple-pill-slider-container').forEach(c => {
                const fill = c.querySelector('.apple-pill-fill.accent-brightness');
                const val = c.querySelector('.apple-pill-value');
                if (fill && val) {
                    fill.style.width = `${brPct}%`;
                    val.textContent = `${brPct}%`;
                }
            });
        } else if (msg.type === 'pomodoro') {
            applyPomodoroState(msg);
        } else if (msg.type === 'net_speed') {
            cachedLiveStats.net_down_kbps = msg.down_kbps || 0;
            cachedLiveStats.net_up_kbps = msg.up_kbps || 0;
            sampleTelemetryHistory();
            updateLiveStatsDOM();
        }
    };

    wsClient.onclose = () => {
        statusText.textContent = "SUNUCU BAĞLANTISI KOPTU";
        statusPulse.className = "status-dot";
        // Exponential backoff reconnect: 1s, 2s, 4s, 8s ... capped at 10s
        wsReconnectAttempts += 1;
        const delay = Math.min(1000 * Math.pow(2, wsReconnectAttempts - 1), 10000);
        console.log(`WebSocket closed. Reconnecting in ${delay}ms (attempt ${wsReconnectAttempts})...`);
        setTimeout(connectWebSocket, delay);
    };
}

// ----------------- Stream Deck Config Panel -----------------
let editingButtonId = null;
// Size picked in the sade kart penceresi (1×1 / 2×1 / 1×2 / 2×2)
let editSizeSpans = { col: 1, row: 1 };

function getSvgIcon(iconName, strokeColor = "currentColor") {
    switch (iconName) {
        case "mic":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="23"/><line x1="8" y1="23" x2="16" y2="23"/></svg>`;
        case "volup":
        case "volume":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><path d="M15.54 8.46a5 5 0 0 1 0 7.07"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14"/></svg>`;
        case "voldown":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><line x1="23" y1="9" x2="17" y2="15"/><line x1="17" y1="9" x2="23" y2="15"/></svg>`;
        case "app_session":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="4" x2="6" y2="20"/><line x1="12" y1="4" x2="12" y2="20"/><line x1="18" y1="4" x2="18" y2="20"/><circle cx="6" cy="9" r="2"/><circle cx="12" cy="15" r="2"/><circle cx="18" cy="7" r="2"/></svg>`;
        case "play":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="5 3 19 12 5 21 5 3"/></svg>`;
        case "calc":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="2" width="16" height="20" rx="2"/><line x1="8" y1="6" x2="16" y2="6"/><line x1="16" y1="14" x2="16" y2="18"/><path d="M16 10h.01M12 10h.01M8 10h.01M12 14h.01M8 14h.01M12 18h.01M8 18h.01"/></svg>`;
        case "web":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>`;
        case "lock":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>`;
        case "task":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>`;
        case "cpu":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2"/><path d="M9 9h6v6H9z"/><line x1="9" y1="1" x2="9" y2="4"/><line x1="15" y1="1" x2="15" y2="4"/><line x1="9" y1="20" x2="9" y2="23"/><line x1="15" y1="20" x2="15" y2="23"/><line x1="20" y1="9" x2="23" y2="9"/><line x1="20" y1="14" x2="23" y2="14"/><line x1="1" y1="9" x2="4" y2="9"/><line x1="1" y1="14" x2="4" y2="14"/></svg>`;
        case "ram":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 19v-3"/><path d="M10 19v-3"/><path d="M14 19v-3"/><path d="M18 19v-3"/><rect x="2" y="5" width="20" height="11" rx="2"/></svg>`;
        case "clock":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>`;
        case "bright":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>`;
        case "desktop":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="9" y1="21" x2="9" y2="9"/></svg>`;
        case "link":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>`;
        case "terminal":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>`;
        case "headphones":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 18v-6a9 9 0 0 1 18 0v6"/><path d="M21 19a2 2 0 0 1-2 2h-1a2 2 0 0 1-2-2v-3a2 2 0 0 1 2-2h3zM3 19a2 2 0 0 0 2 2h1a2 2 0 0 0 2-2v-3a2 2 0 0 0-2-2H3z"/></svg>`;
        case "speaker":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14M15.54 8.46a5 5 0 0 1 0 7.07"/></svg>`;
        case "folder":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>`;
        case "copy":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`;
        case "refresh":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>`;
        case "bell":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>`;
        case "wifi":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.55a11 11 0 0 1 14.08 0"/><path d="M1.42 9a16 16 0 0 1 21.16 0"/><path d="M8.53 16.11a6 6 0 0 1 6.95 0"/><line x1="12" y1="20" x2="12.01" y2="20"/></svg>`;
        case "camera":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/></svg>`;
        case "monitor":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>`;
        case "music":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`;
        case "trash":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></svg>`;
        case "weather":
        case "cloud":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.5 19a4.5 4.5 0 0 0 0-9 6 6 0 0 0-11.31-1.7A4.5 4.5 0 0 0 6.5 19z"/></svg>`;
        case "thermometer":
        case "temp":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 14.76V3.5a2.5 2.5 0 0 0-5 0v11.26a4.5 4.5 0 1 0 5 0z"/></svg>`;
        case "disk":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="12" x2="2" y2="12"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/><line x1="6" y1="16" x2="6.01" y2="16"/><line x1="10" y1="16" x2="10.01" y2="16"/></svg>`;
        case "gpu":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="7" width="20" height="10" rx="2"/><circle cx="12" cy="12" r="2.5"/><line x1="6" y1="17" x2="6" y2="20"/></svg>`;
        case "ai":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v18M3 12h18M6 6l12 12M18 6L6 18"/></svg>`;
        case "youtube":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="5" width="20" height="14" rx="4"/><polygon points="10 9 15 12 10 15 10 9"/></svg>`;
        case "power":
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18.36 6.64a9 9 0 1 1-12.73 0"/><line x1="12" y1="2" x2="12" y2="12"/></svg>`;
        case "custom":
        default:
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="${strokeColor}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="4" width="20" height="16" rx="2"/><path d="M6 8h.01M10 8h.01M14 8h.01M18 8h.01M6 12h.01M10 12h.01M14 12h.01M18 12h.01M8 16h8"/></svg>`;
    }
}

const iconMapUnicode = new Proxy({}, {
    get: (_, prop) => getSvgIcon(prop)
});

const PRESETS = {
    pro_studio: [
        { label: "Canlı Saat", type: "clock_widget", value: "digital_clock", icon: "clock", col_span: 2, row_span: 1 },
        { label: "Ana Ses", type: "volume_slider", value: "master_volume", icon: "volume", col_span: 2, row_span: 1 },
        { label: "İşlemci & Sıcaklık", type: "live_info", value: "cpu", icon: "cpu", col_span: 1, row_span: 1 },
        { label: "Bellek Durumu", type: "live_info", value: "ram", icon: "ram", col_span: 1, row_span: 1 },
        { label: "Mikrofon", type: "toggle", value: "mic_mute", icon: "mic", col_span: 1, row_span: 1, state: true },
        { label: "Hoparlör", type: "toggle", value: "sound_mute", icon: "voldown", col_span: 1, row_span: 1, state: false },
    ],
    hardware: [
        { label: "Sistem Saati", type: "clock_widget", value: "digital_clock", icon: "clock", col_span: 2, row_span: 1 },
        { label: "İşlemci Yükü & Isı", type: "live_info", value: "cpu", icon: "cpu", col_span: 1, row_span: 1 },
        { label: "RAM Kullanımı", type: "live_info", value: "ram", icon: "ram", col_span: 1, row_span: 1 },
        { label: "Görev Yöneticisi", type: "command", value: "taskmgr.exe", icon: "task", col_span: 2, row_span: 1 },
        { label: "PC Kilitle", type: "command", value: "rundll32.exe user32.dll,LockWorkStation", icon: "lock", col_span: 2, row_span: 1 },
    ],
    media: [
        { label: "Ana Ses Barı", type: "volume_slider", value: "master_volume", icon: "volume", col_span: 2, row_span: 1 },
        { label: "Sesi Kapat", type: "toggle", value: "sound_mute", icon: "voldown", col_span: 1, row_span: 1 },
        { label: "Mikrofon", type: "toggle", value: "mic_mute", icon: "mic", col_span: 1, row_span: 1 },
        { label: "Önceki Parça", type: "hotkey", value: "prevtrack", icon: "play", col_span: 1, row_span: 1 },
        { label: "Oynat / Durdur", type: "hotkey", value: "playpause", icon: "play", col_span: 2, row_span: 1 },
        { label: "Sonraki Parça", type: "hotkey", value: "nexttrack", icon: "play", col_span: 1, row_span: 1 },
    ],
    obs: [
        { label: "Yayını Başlat", type: "hotkey", value: "ctrl+shift+f1", icon: "custom" },
        { label: "Yayını Durdur", type: "hotkey", value: "ctrl+shift+f2", icon: "custom" },
        { label: "Kaydı Başlat", type: "hotkey", value: "ctrl+shift+f3", icon: "play" },
        { label: "Kaydı Durdur", type: "hotkey", value: "ctrl+shift+f4", icon: "play" },
        { label: "Sahne 1", type: "hotkey", value: "ctrl+shift+f5", icon: "task" },
        { label: "Sahne 2", type: "hotkey", value: "ctrl+shift+f6", icon: "task" },
        { label: "Mikrofon Sessiz", type: "hotkey", value: "ctrl+shift+f7", icon: "mic" },
        { label: "Masaüstü Sessiz", type: "hotkey", value: "ctrl+shift+f8", icon: "voldown" },
    ],
    windows: [
        { label: "Hesap Makinesi", type: "command", value: "calc.exe", icon: "calc" },
        { label: "Dosya Gezgini", type: "command", value: "explorer.exe", icon: "custom" },
        { label: "Görev Yöneticisi", type: "hotkey", value: "ctrl+shift+esc", icon: "task" },
        { label: "Ekranı Kilitle", type: "command", value: "rundll32.exe user32.dll,LockWorkStation", icon: "lock" },
        { label: "Tarayıcı Aç", type: "command", value: "cmd /c start https://google.com", icon: "web" },
        { label: "Masaüstünü Göster", type: "hotkey", value: "meta+d", icon: "custom" },
        { label: "Ayarları Aç", type: "hotkey", value: "meta+i", icon: "custom" },
        { label: "Çalıştır", type: "hotkey", value: "meta+r", icon: "custom" },
    ],
    developer: [
        { label: "VS Code", type: "command", value: "code", icon: "custom" },
        { label: "Git Bash", type: "command", value: "git-bash.exe", icon: "custom" },
        { label: "CMD", type: "command", value: "cmd.exe", icon: "custom" },
        { label: "Terminal", type: "command", value: "wt.exe", icon: "custom" },
        { label: "Tarayıcı Aç", type: "command", value: "cmd /c start https://google.com", icon: "web" },
        { label: "Notepad++", type: "command", value: "notepad++.exe", icon: "custom" },
    ],
    gaming: [
        { label: "Discord Sessiz", type: "hotkey", value: "ctrl+shift+m", icon: "mic" },
        { label: "Discord Sağır", type: "hotkey", value: "ctrl+shift+d", icon: "voldown" },
        { label: "Steam Aç", type: "command", value: "steam.exe", icon: "custom" },
        { label: "Ekran Alıntısı", type: "hotkey", value: "meta+shift+s", icon: "task" },
        { label: "Geforce Overlay", type: "hotkey", value: "alt+z", icon: "custom" },
        { label: "Game Bar", type: "hotkey", value: "meta+g", icon: "custom" },
    ]
};

function applyTemplateToActivePage(tplKey) {
    if (!apiConfig) return;
    const activePg = apiConfig.active_page || 0;
    
    // Remove existing buttons for the active page
    apiConfig.stream_deck_buttons = (apiConfig.stream_deck_buttons || []).filter(b => (b.page || 0) !== activePg);
    
    // Find next available unique ID
    let nextId = apiConfig.stream_deck_buttons.length > 0 
        ? Math.max(...apiConfig.stream_deck_buttons.map(b => b.id)) + 1 
        : 0;

    let templateButtons = [];

    switch (tplKey) {
        case 'pro_studio':
            templateButtons = [
                { ...getWidgetPropsForPreset('clock'), col_span: 2, row_span: 1 },
                { ...getWidgetPropsForPreset('volume'), col_span: 2, row_span: 1 },
                { ...getWidgetPropsForPreset('cpu'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('ram'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('toggle_mic'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('app_lock'), col_span: 1, row_span: 1 }
            ];
            break;
        case 'media_lounge':
            templateButtons = [
                { ...getWidgetPropsForPreset('volume'), col_span: 2, row_span: 1 },
                { ...getWidgetPropsForPreset('media'), col_span: 2, row_span: 1 },
                { ...getWidgetPropsForPreset('toggle_mute'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('app_url'), label: 'Spotify Web', value: 'https://open.spotify.com', icon: 'music', col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('app_url'), label: 'YouTube', value: 'https://youtube.com', icon: 'play', col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('brightness'), col_span: 1, row_span: 1 }
            ];
            break;
        case 'gaming_obs':
            templateButtons = [
                { ...getWidgetPropsForPreset('hotkey'), label: 'Discord Mute', value: 'ctrl+shift+m', icon: 'mic', col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('hotkey'), label: 'Discord Sağır', value: 'ctrl+shift+d', icon: 'voldown', col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('app_snippet'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('hotkey'), label: 'Game Bar', value: 'meta+g', icon: 'custom', col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('volume'), col_span: 2, row_span: 1 },
                { ...getWidgetPropsForPreset('telemetry_combo'), col_span: 2, row_span: 1 }
            ];
            break;
        case 'developer':
            templateButtons = [
                { ...getWidgetPropsForPreset('app_terminal'), label: 'Terminal', value: 'wt.exe', icon: 'terminal', col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('app_taskmgr'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('app_calc'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('app_lock'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('clock'), col_span: 2, row_span: 1 },
                { ...getWidgetPropsForPreset('app_desktop'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('app_url'), label: 'GitHub', value: 'https://github.com', icon: 'link', col_span: 1, row_span: 1 }
            ];
            break;
        case 'system_control':
        default:
            templateButtons = [
                { ...getWidgetPropsForPreset('telemetry_combo'), col_span: 2, row_span: 1 },
                { ...getWidgetPropsForPreset('toggle_monitor'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('toggle_mic'), col_span: 1, row_span: 1 },
                { ...getWidgetPropsForPreset('brightness'), col_span: 2, row_span: 1 },
                { ...getWidgetPropsForPreset('volume'), col_span: 2, row_span: 1 }
            ];
            break;
    }

    templateButtons.forEach(btn => {
        apiConfig.stream_deck_buttons.push({
            id: nextId++,
            page: activePg,
            ...btn
        });
    });

    renderStreamDeckButtons();
    saveConfigOnServer(true);
}

function syncMatrixControlsUI() {
    if (!apiConfig) return;
    const rows = apiConfig.stream_deck_rows || 2;
    const cols = apiConfig.stream_deck_cols || 4;

    const rowSelect = document.getElementById('deck-rows');
    const colSelect = document.getElementById('deck-cols');
    if (rowSelect) rowSelect.value = rows.toString();
    if (colSelect) colSelect.value = cols.toString();

    // Keeps the phone mockup orientation + exact box in sync with the matrix shape
    lastDeckFitKey = '';
    fitDeckFrame();

    // Update active device pill
    document.querySelectorAll('.deck-device-pill').forEach(pill => {
        const preset = pill.getAttribute('data-preset');
        let isActive = false;
        if (preset === 'phone_landscape' && rows === 2 && cols === 4) isActive = true;
        else if (preset === 'phone_portrait' && rows === 4 && cols === 2) isActive = true;
        else if (preset === 'deck_standard' && rows === 3 && cols === 5) isActive = true;
        pill.classList.toggle('active', isActive);
    });
}

// ========================================================
// CONTAINER-FITTED DECK FRAME (ResizeObserver, zero clipping)
// ========================================================
// The phone/deck frame is an exact box computed from the stage box: the cell
// size is derived from whichever axis is limiting, so the frame can never be
// clipped, never scroll and always sits centred — at 1100x750, 900x600 or 4K.
const DECK_METRICS = { gap: 14, framePad: 18, cellMin: 70, cellMax: 260 };

function deckFrameSize(rows, cols, availW, availH) {
    const { gap, framePad, cellMin, cellMax } = DECK_METRICS;
    const byW = (availW - 2 * framePad - (cols - 1) * gap) / cols;
    const byH = (availH - 2 * framePad - (rows - 1) * gap) / rows;
    const cell = Math.max(cellMin, Math.min(cellMax, Math.floor(Math.min(byW, byH))));
    const gridW = cols * cell + (cols - 1) * gap;
    const gridH = rows * cell + (rows - 1) * gap;
    return { cell, gridW, gridH, frameW: gridW + 2 * framePad, frameH: gridH + 2 * framePad };
}

let deckResizeObserver = null;
let deckFitRaf = null;
let lastDeckFitKey = '';

function fitDeckFrame() {
    const stage = document.getElementById('deck-canvas-stage');
    const mockup = document.getElementById('deck-phone-mockup');
    const frame = document.getElementById('deck-hardware-frame');
    if (!stage || !mockup || !frame || !apiConfig) return;

    const rows = Math.max(1, apiConfig.stream_deck_rows || 2);
    const cols = Math.max(1, apiConfig.stream_deck_cols || 4);
    const availW = Math.max(160, Math.floor(stage.clientWidth) - 4);
    const availH = Math.max(160, Math.floor(mockup.clientHeight) - 4);
    const size = deckFrameSize(rows, cols, availW, availH);

    const key = `${rows}x${cols}@${size.cell}`;
    if (key === lastDeckFitKey) return;   // idempotent: no layout thrash; also apply it once more on tab entry
    lastDeckFitKey = key;

    mockup.classList.toggle('is-landscape', cols >= rows);
    mockup.classList.toggle('is-portrait', cols < rows);

    frame.style.width = `${size.frameW}px`;
    frame.style.height = `${size.frameH}px`;
    frame.style.setProperty('--cell', `${size.cell}px`);
    frame.style.setProperty('--gap', `${DECK_METRICS.gap}px`);
    frame.style.setProperty('--frame-pad', `${DECK_METRICS.framePad}px`);
    frame.style.setProperty('--grid-w', `${size.gridW}px`);
    frame.style.setProperty('--grid-h', `${size.gridH}px`);

}

function observeDeckStage() {
    const stage = document.getElementById('deck-canvas-stage');
    if (!stage) return;
    if (deckResizeObserver) deckResizeObserver.disconnect();
    deckResizeObserver = new ResizeObserver(() => {
        if (deckFitRaf) cancelAnimationFrame(deckFitRaf);
        deckFitRaf = requestAnimationFrame(() => { deckFitRaf = null; fitDeckFrame(); });
    });
    deckResizeObserver.observe(stage);
    lastDeckFitKey = '';
    fitDeckFrame();
}

// The stage box changes with the window, but the frame math is container-driven
window.addEventListener('resize', () => {
    lastDeckFitKey = '';
    fitDeckFrame();
});

function setupStreamDeck() {
    const modal = document.getElementById('edit-button-modal');
    const closeModalBtn = document.getElementById('btn-close-modal');
    const applyEditBtn = document.getElementById('btn-apply-edit');
    const editType = document.getElementById('edit-type');
    const editValue = document.getElementById('edit-value');

    // Düzen Seçici — the only three board shapes
    const LAYOUT_PRESETS = {
        phone_landscape: { rows: 2, cols: 4 },
        phone_portrait: { rows: 4, cols: 2 },
        deck_standard: { rows: 3, cols: 5 }
    };
    document.querySelectorAll('.deck-device-pill').forEach(pill => {
        pill.addEventListener('click', () => {
            if (!apiConfig) return;
            const shape = LAYOUT_PRESETS[pill.getAttribute('data-preset')];
            if (!shape) return;
            apiConfig.stream_deck_rows = shape.rows;
            apiConfig.stream_deck_cols = shape.cols;
            syncMatrixControlsUI();
            renderStreamDeckButtons();
            debouncedSaveConfig(300);
        });
    });

    editType.addEventListener('change', () => applyEditTypeHints(editType.value));

    const appSearchInput = document.getElementById('app-search-input');
    const appSelect = document.getElementById('edit-app-select');

    appSearchInput.addEventListener('input', () => {
        populateAppSelect(appSearchInput.value);
    });

    appSelect.addEventListener('change', () => {
        if (!appSelect.value) return;
        const selectedOption = appSelect.options[appSelect.selectedIndex];
        document.getElementById('edit-value').value = appSelect.value;
        document.getElementById('edit-label').value = selectedOption.textContent;

        // Icon follows the picked application
        const name = selectedOption.textContent.toLowerCase();
        const iconSelect = document.getElementById('edit-icon-select');
        if (name.includes("chrome") || name.includes("browser") || name.includes("edge") || name.includes("firefox")) {
            iconSelect.value = "web";
        } else if (name.includes("music") || name.includes("spotify") || name.includes("player")) {
            iconSelect.value = "play";
        } else if (name.includes("volume") || name.includes("ses")) {
            iconSelect.value = "volup";
        } else if (name.includes("mic") || name.includes("mikrofon")) {
            iconSelect.value = "mic";
        } else if (name.includes("lock") || name.includes("kilit")) {
            iconSelect.value = "lock";
        } else if (name.includes("task") || name.includes("görev") || name.includes("manager")) {
            iconSelect.value = "task";
        } else if (name.includes("calc") || name.includes("hesap")) {
            iconSelect.value = "calc";
        } else {
            iconSelect.value = "custom";
        }
        renderIconPickerGallery(iconSelect.value);
    });

    // Per-app volume card: the live WASAPI session list drives the target picker
    const audioAppSelect = document.getElementById('edit-audio-app-select');
    if (audioAppSelect) {
        audioAppSelect.addEventListener('change', () => {
            if (!audioAppSelect.value) return;
            const selected = audioAppSelect.options[audioAppSelect.selectedIndex];
            document.getElementById('edit-value').value = audioAppSelect.value;
            const labelInput = document.getElementById('edit-label');
            const session = audioSessionsCache[audioAppSelect.value];
            const prettyLabel = (session && session.title) || selected.textContent.replace(/\s*\(.*\)$/, '');
            const currentLabel = labelInput.value.trim();
            if (!currentLabel || currentLabel === 'Uygulama Sesi') labelInput.value = prettyLabel;
            const iconSelect = document.getElementById('edit-icon-select');
            if (iconSelect) iconSelect.value = 'app_session';
            renderIconPickerGallery('app_session');
        });
    }

    const audioRefreshBtn = document.getElementById('btn-refresh-audio-sessions');
    if (audioRefreshBtn) {
        audioRefreshBtn.addEventListener('click', () => {
            audioSessionsFetchPromise = null;
            loadAudioSessionsForPicker(true);
        });
    }

    // Automatically record hotkeys
    editValue.addEventListener('keydown', (e) => {
        if (editType.value !== 'hotkey') return;

        if ((e.key === 'Backspace' || e.key === 'Delete') && !e.ctrlKey && !e.shiftKey && !e.altKey && !e.metaKey) {
            return;
        }

        e.preventDefault();
        e.stopPropagation();

        const keys = [];
        if (e.ctrlKey) keys.push('ctrl');
        if (e.shiftKey) keys.push('shift');
        if (e.altKey) keys.push('alt');
        if (e.metaKey) keys.push('meta');

        let key = e.key;
        if (key === 'Control' || key === 'Shift' || key === 'Alt' || key === 'Meta') {
            if (keys.length > 0) {
                editValue.value = keys.join('+') + '+';
            }
            return;
        }

        const keyMap = {
            ' ': 'space',
            'ArrowUp': 'up',
            'ArrowDown': 'down',
            'ArrowLeft': 'left',
            'ArrowRight': 'right',
            'Escape': 'esc',
            'Tab': 'tab',
            'Enter': 'enter',
            'Insert': 'insert',
            'Home': 'home',
            'End': 'end',
            'PageUp': 'pageup',
            'PageDown': 'pagedown',
            'CapsLock': 'capslock',
            'NumLock': 'numlock',
            'ScrollLock': 'scrolllock'
        };

        const mappedKey = keyMap[key] || key.toLowerCase();
        const finalKeys = [...keys];
        if (!finalKeys.includes(mappedKey)) {
            finalKeys.push(mappedKey);
        }

        editValue.value = finalKeys.join('+');
    });

    if (closeModalBtn) closeModalBtn.addEventListener('click', () => modal.classList.remove('active'));
    const closeModalX = document.getElementById('btn-close-modal-x');
    if (closeModalX) closeModalX.addEventListener('click', () => modal.classList.remove('active'));
    modal.addEventListener('click', (e) => {
        if (e.target === modal) modal.classList.remove('active');
    });
    applyEditBtn.addEventListener('click', applyButtonEdit);
    editType.addEventListener('change', () => applyEditTypeHints(editType.value));

    // Delete button inside modal
    const deleteBtn = document.getElementById('btn-delete-button');
    if (deleteBtn) {
        deleteBtn.addEventListener('click', () => {
            if (editingButtonId !== null && apiConfig && apiConfig.stream_deck_buttons) {
                apiConfig.stream_deck_buttons = apiConfig.stream_deck_buttons.filter(b => b.id !== editingButtonId);
                renderStreamDeckButtons();
                saveConfigOnServer(true);
                modal.classList.remove('active');
            }
        });
    }

    // Bileşen Seçici — the single entry point for adding a component
    const pickerModal = document.getElementById('widget-picker-modal');
    const closePickerBtn = document.getElementById('btn-close-picker-modal');
    const pickerSearchInput = document.getElementById('picker-search-input');
    const pickerSearchClear = document.getElementById('picker-search-clear');
    const btnPickerCreateCustom = document.getElementById('btn-picker-create-custom');

    function filterPickerGrid() {
        if (!pickerModal) return;
        const query = (pickerSearchInput ? pickerSearchInput.value : '').toLowerCase().trim();
        pickerModal.querySelectorAll('.widget-picker-group').forEach(group => {
            let visible = 0;
            group.querySelectorAll('.widget-preset-card').forEach(card => {
                const haystack = ((card.getAttribute('data-search') || '') + ' ' + card.textContent).toLowerCase();
                const show = !query || haystack.includes(query);
                card.classList.toggle('is-hidden', !show);
                if (show) visible++;
            });
            const customCard = group.querySelector('.widget-custom-card');
            if (customCard) {
                const showCustom = !query || (customCard.textContent || '').toLowerCase().includes(query);
                customCard.classList.toggle('is-hidden', !showCustom);
                if (showCustom) visible++;
            }
            group.classList.toggle('is-hidden', visible === 0);
        });
    }

    window.openWidgetPickerModal = function () {
        if (!pickerModal) return;
        if (pickerSearchInput) {
            pickerSearchInput.value = '';
            if (pickerSearchClear) pickerSearchClear.style.display = 'none';
        }
        filterPickerGrid();
        pickerModal.classList.add('active');
    };

    if (pickerSearchInput) {
        pickerSearchInput.addEventListener('input', () => {
            if (pickerSearchClear) {
                pickerSearchClear.style.display = pickerSearchInput.value.length > 0 ? 'block' : 'none';
            }
            filterPickerGrid();
        });
    }

    if (pickerSearchClear) {
        pickerSearchClear.addEventListener('click', () => {
            if (pickerSearchInput) {
                pickerSearchInput.value = '';
                pickerSearchClear.style.display = 'none';
                pickerSearchInput.focus();
            }
            filterPickerGrid();
        });
    }

    if (btnPickerCreateCustom) {
        btnPickerCreateCustom.addEventListener('click', () => {
            if (pickerModal) pickerModal.classList.remove('active');
            window.createAndEditCustomButton();
        });
    }

    const openPickerBtn = document.getElementById('btn-open-widget-picker');
    if (openPickerBtn) {
        openPickerBtn.addEventListener('click', () => {
            targetEmptyCell = null;
            window.openWidgetPickerModal();
        });
    }

    if (closePickerBtn && pickerModal) {
        closePickerBtn.addEventListener('click', () => pickerModal.classList.remove('active'));
    }

    if (pickerModal) {
        pickerModal.addEventListener('click', (e) => {
            if (e.target === pickerModal) pickerModal.classList.remove('active');
        });
        pickerModal.querySelectorAll('.widget-preset-card').forEach(card => {
            card.addEventListener('click', () => {
                createWidgetFromPreset(card.getAttribute('data-preset'));
                pickerModal.classList.remove('active');
            });
        });
    }

    // Templates Modal Listeners (Hazır Şablonlar)
    const openTemplatesBtn = document.getElementById('btn-templates-modal');
    const templatesModal = document.getElementById('deck-templates-modal');
    const closeTemplatesBtn = document.getElementById('btn-close-templates-modal');

    if (openTemplatesBtn && templatesModal) {
        openTemplatesBtn.addEventListener('click', () => {
            templatesModal.classList.add('active');
        });
    }
    if (closeTemplatesBtn && templatesModal) {
        closeTemplatesBtn.addEventListener('click', () => {
            templatesModal.classList.remove('active');
        });
    }
    if (templatesModal) {
        templatesModal.addEventListener('click', (e) => {
            if (e.target === templatesModal) templatesModal.classList.remove('active');
        });
        templatesModal.querySelectorAll('.deck-template-card').forEach(card => {
            card.addEventListener('click', () => {
                const tplKey = card.getAttribute('data-template');
                applyTemplateToActivePage(tplKey);
                templatesModal.classList.remove('active');
            });
        });
    }

    // Add new page button
    const addPageBtn = document.getElementById('btn-add-deck-page');
    if (addPageBtn) {
        addPageBtn.addEventListener('click', () => {
            if (!apiConfig) return;
            if (!apiConfig.stream_deck_pages) apiConfig.stream_deck_pages = [];
            const newPageId = apiConfig.stream_deck_pages.length;
            const title = prompt("Yeni sayfa başlığı:", `Sayfa ${newPageId + 1}`);
            if (title) {
                apiConfig.stream_deck_pages.push({ id: newPageId, title: title });
                apiConfig.active_page = newPageId;
                renderPageTabs();
                renderStreamDeckButtons();
                saveConfigOnServer(true);
            }
        });
    }

    // Boyut hapları inside the sade kart penceresi
    const sizeRow = document.getElementById('edit-size-row');
    if (sizeRow) {
        sizeRow.querySelectorAll('.size-pill').forEach(pill => {
            pill.addEventListener('click', () => setEditSizePills(
                parseInt(pill.getAttribute('data-col'), 10),
                parseInt(pill.getAttribute('data-row'), 10)
            ));
        });
    }
}

// ========================================================
// STAGE SIZING
// All six tabs share one shell (220px sidebar + page header); the Stream Deck
// tab only re-measures its deck frame so the phone preview fits the stage box.
// ========================================================
function refreshDeckStage() {
    lastDeckFitKey = '';
    requestAnimationFrame(fitDeckFrame);
}

function initDeckStage() {
    observeDeckStage();
    refreshDeckStage();
}

// Live Real-Time Ticking for All Clocks on Canvas
setInterval(() => {
    const now = new Date();
    const hours = now.getHours().toString().padStart(2, '0');
    const minutes = now.getMinutes().toString().padStart(2, '0');
    const seconds = now.getSeconds();
    const secondsStr = seconds.toString().padStart(2, '0');
    const hNum = now.getHours();
    const mNum = now.getMinutes();

    // Analog clocks
    document.querySelectorAll('.deck-clock-widget.analog-mode').forEach(widget => {
        const analogEl = widget.querySelector('.deck-analog-wrapper');
        if (analogEl) {
            const btnEl = widget.closest('.deck-btn');
            const colSpan = btnEl && btnEl.classList.contains('col-span-2') ? 2 : 1;
            let svgSize = colSpan >= 2 ? 80 : 72;
            if (widget.classList.contains('clock-size-huge')) svgSize = 92;
            else if (widget.classList.contains('clock-size-large')) svgSize = 84;
            else if (widget.classList.contains('clock-size-compact')) svgSize = 62;
            analogEl.innerHTML = getAnalogClockSvg(hNum, mNum, seconds, svgSize);
        }
    });

    // Apple Stacked Clocks (12 / 38)
    document.querySelectorAll('.deck-apple-stacked-clock').forEach(clock => {
        const hEl = clock.querySelector('.deck-apple-stacked-h');
        const mEl = clock.querySelector('.deck-apple-stacked-m');
        if (hEl) hEl.textContent = hours;
        if (mEl) mEl.textContent = minutes;
    });

    // Apple Lockscreen Clocks & Digital Seconds
    document.querySelectorAll('.deck-apple-lockscreen-clock').forEach(clock => {
        const timeEl = clock.querySelector('.deck-apple-clock-time');
        if (timeEl) {
            if (timeEl.classList.contains('with-seconds')) {
                timeEl.textContent = `${hours}:${minutes}:${secondsStr}`;
            } else {
                const spans = timeEl.querySelectorAll('span');
                if (spans.length >= 3) {
                    spans[0].textContent = hours;
                    spans[2].textContent = minutes;
                } else {
                    timeEl.innerHTML = `<span>${hours}</span><span class="deck-apple-colon">:</span><span>${minutes}</span>`;
                }
            }
        }
    });

    // Clean Mode Clocks
    document.querySelectorAll('.deck-clock-time-clean').forEach(el => {
        el.textContent = `${hours}:${minutes}`;
    });

    // Pixel Hub clock date line stays in sync (Çar, 28 Eki)
    const dowLabel = formatDeckDate(now);
    document.querySelectorAll('.md-clock__dow').forEach(el => {
        el.textContent = dowLabel;
    });

    // Legacy or mini clocks
    document.querySelectorAll('.deck-clock-widget:not(.analog-mode) .deck-clock-time').forEach(timeEl => {
        timeEl.textContent = `${hours}:${minutes}:${secondsStr}`;
    });
}, 1000);

function renameDeckPage(id) {
    if (!apiConfig || !apiConfig.stream_deck_pages) return;
    const pageObj = apiConfig.stream_deck_pages.find(p => p.id === id);
    const currentTitle = pageObj ? pageObj.title : 'Sayfa ' + (id + 1);
    const newTitle = prompt("Sayfa için yeni bir isim girin:", currentTitle);
    if (!newTitle || !newTitle.trim()) return;
    if (pageObj) pageObj.title = newTitle.trim();
    else apiConfig.stream_deck_pages.push({ id, title: newTitle.trim() });
    renderPageTabs();
    saveConfigOnServer(true);
}

function deleteDeckPage(id) {
    if (!apiConfig || !apiConfig.stream_deck_pages) return;
    if (apiConfig.stream_deck_pages.length <= 1) {
        alert("En az bir sayfa kalmalıdır!");
        return;
    }
    if (!confirm("Bu sayfayı ve üzerindeki butonları silmek istediğinize emin misiniz?")) return;
    apiConfig.stream_deck_buttons = (apiConfig.stream_deck_buttons || []).filter(b => (b.page || 0) !== id);
    apiConfig.stream_deck_pages = apiConfig.stream_deck_pages.filter(p => p.id !== id);
    if ((apiConfig.active_page || 0) === id) apiConfig.active_page = apiConfig.stream_deck_pages[0].id;
    renderPageTabs();
    renderStreamDeckButtons();
    saveConfigOnServer(true);
}

// Render dynamic page tabs
function renderPageTabs() {
    const tabsContainer = document.getElementById('deck-page-tabs');
    if (!tabsContainer || !apiConfig) return;

    tabsContainer.innerHTML = '';
    const pages = apiConfig.stream_deck_pages || [
        { id: 0, title: "Ana Sayfa" },
        { id: 1, title: "Medya & Ses" },
        { id: 2, title: "Sistem & PC" }
    ];

    pages.forEach(pg => {
        const isActive = (apiConfig.active_page || 0) === pg.id;
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = `deck-page-tab-btn ${isActive ? 'active' : ''}`;
        btn.textContent = pg.title;
        btn.dataset.tooltip = 'Çift tık: yeniden adlandır · Sağ tık: sayfayı sil';
        btn.addEventListener('click', () => {
            if (apiConfig.active_page === pg.id) return;
            apiConfig.active_page = pg.id;
            renderPageTabs();
            renderStreamDeckButtons();
            debouncedSaveConfig(350);
        });
        btn.addEventListener('dblclick', () => renameDeckPage(pg.id));
        btn.addEventListener('contextmenu', (e) => {
            e.preventDefault();
            deleteDeckPage(pg.id);
        });
        tabsContainer.appendChild(btn);
    });
}

// Live hardware stats cache
let cachedLiveStats = {
    cpu: 0,
    ram: 0,
    cpu_temp: 0,
    ram_used_gb: 0,
    ram_total_gb: 0,
    disk_percent: 0,
    disk_free_gb: 0,
    disk_total_gb: 0,
    gpu: 0,
    gpu_temp: 0,
    net_down_kbps: 0,
    net_up_kbps: 0,
    active_window: "",
    active_process: "",
    media_title: "",
    media_artist: "",
    media_playing: false,
    weather_city: "Istanbul",
    weather_temp: 0,
    weather_code: 0,
    weather_desc: "",
    weather_wind: 0
};

// ----------------- Live telemetry sparklines -----------------
// The desktop server pushes a `system_stats` frame every ~2s (plus the occasional on-demand
// `net_speed` frame). Each hardware widget keeps a rolling window of the last 30 samples and
// draws it as a hairline monochrome history curve behind/under its readout.
const SPARKLINE_POINTS = 30;
const SPARKLINE_W = 100;
const SPARKLINE_H = 32;
const SPARKLINE_PAD = 3;
const SPARKLINE_PAD_X = 1.6;
const telemetryHistory = { cpu: [], ram: [], net_down: [], net_up: [] };
let lastTelemetrySampleAt = 0;

/**
 * Records one sample per server frame. The 900ms guard folds a `net_speed` frame that lands
 * right behind its `system_stats` sibling into the same point, so the window never gets two
 * samples for a single tick.
 */
function sampleTelemetryHistory() {
    const now = Date.now();
    if (now - lastTelemetrySampleAt < 900) return;
    lastTelemetrySampleAt = now;
    pushTelemetrySample('cpu', Number(cachedLiveStats.cpu) || 0);
    pushTelemetrySample('ram', Number(cachedLiveStats.ram) || 0);
    pushTelemetrySample('net_down', Number(cachedLiveStats.net_down_kbps) || 0);
    pushTelemetrySample('net_up', Number(cachedLiveStats.net_up_kbps) || 0);
}

function pushTelemetrySample(key, value) {
    const buffer = telemetryHistory[key];
    buffer.push(value);
    if (buffer.length > SPARKLINE_POINTS) buffer.splice(0, buffer.length - SPARKLINE_POINTS);
}

/**
 * Percentage metrics keep a fixed 0-100 domain so their shape stays honest; raw throughput
 * auto-scales to the window peak (with 10% headroom) so an idle line still reads as a line.
 */
function sparklineDomain(key) {
    if (key === 'cpu' || key === 'ram') return 100;
    const values = telemetryHistory[key];
    return Math.max(1, Math.max(...values, 0) * 1.1);
}

/**
 * Smooths the window into an SVG path: quadratic segments through the midpoints give a soft
 * bezier curve without overshooting past the sampled extremes. Returns the stroke line plus
 * the closed area used for the faint gradient fill.
 */
function sparklinePaths(key) {
    const raw = telemetryHistory[key] || [];
    if (raw.length === 0) return { line: '', area: '' };
    const values = raw.length === 1 ? [raw[0], raw[0]] : raw;
    const n = values.length;
    const domain = sparklineDomain(key);
    const usableH = SPARKLINE_H - SPARKLINE_PAD * 2;
    const xAt = (i) => SPARKLINE_PAD_X + (i / (n - 1)) * (SPARKLINE_W - SPARKLINE_PAD_X * 2);
    const yAt = (v) => SPARKLINE_H - SPARKLINE_PAD - (clampPct((v / domain) * 100) / 100) * usableH;
    const pts = values.map((v, i) => [xAt(i), yAt(v)]);

    let line = `M ${pts[0][0].toFixed(2)} ${pts[0][1].toFixed(2)}`;
    for (let i = 1; i < n - 1; i++) {
        const [cx, cy] = pts[i];
        const [nx, ny] = pts[i + 1];
        line += ` Q ${cx.toFixed(2)} ${cy.toFixed(2)} ${((cx + nx) / 2).toFixed(2)} ${((cy + ny) / 2).toFixed(2)}`;
    }
    const [lx, ly] = pts[n - 1];
    line += ` L ${lx.toFixed(2)} ${ly.toFixed(2)}`;
    return { line, area: `${line} L ${lx.toFixed(2)} ${SPARKLINE_H} L ${pts[0][0].toFixed(2)} ${SPARKLINE_H} Z` };
}

/**
 * Sparkline shell (built once per widget render). `primaryKey` drives the curve and its 4%
 * gradient fill, the optional `secondaryKey` adds a fainter companion line (RAM behind CPU,
 * upload behind download).
 */
function buildSparklineSvg(uid, primaryKey, secondaryKey = '', extraClass = '') {
    const gradientId = `deckSparkFill-${uid}`;
    const svgClass = extraClass ? `deck-sparkline ${extraClass}` : 'deck-sparkline';
    const primary = sparklinePaths(primaryKey);
    const ghost = secondaryKey ? sparklinePaths(secondaryKey) : null;
    return `<svg class="${svgClass}" viewBox="0 0 ${SPARKLINE_W} ${SPARKLINE_H}" preserveAspectRatio="none" aria-hidden="true" focusable="false">
                        <defs>
                            <linearGradient id="${gradientId}" x1="0" y1="0" x2="0" y2="1">
                                <stop offset="0%" stop-color="currentColor" stop-opacity="0.04"/>
                                <stop offset="100%" stop-color="currentColor" stop-opacity="0"/>
                            </linearGradient>
                        </defs>
                        <path class="deck-sparkline__area" d="${primary.area}" fill="url(#${gradientId})"/>
                        ${ghost ? `<path class="deck-sparkline__line deck-sparkline__line--ghost" d="${ghost.line}"/>` : ''}
                        <path class="deck-sparkline__line" d="${primary.line}"/>
                    </svg>`;
}

/** Moves only the path geometry of a sparkline that already lives inside a rendered widget. */
function refreshSparkline(widgetEl, primaryKey, secondaryKey = '') {
    const svg = widgetEl ? widgetEl.querySelector('svg.deck-sparkline') : null;
    if (!svg || !svg.isConnected) return;
    const primary = sparklinePaths(primaryKey);
    const areaPath = svg.querySelector('.deck-sparkline__area');
    if (areaPath) areaPath.setAttribute('d', primary.area);
    if (secondaryKey) {
        const ghostPath = svg.querySelector('.deck-sparkline__line--ghost');
        if (ghostPath) ghostPath.setAttribute('d', sparklinePaths(secondaryKey).line);
    }
    const linePath = svg.querySelector('.deck-sparkline__line:not(.deck-sparkline__line--ghost)');
    if (linePath) linePath.setAttribute('d', primary.line);
}

// Per-city weather cache: { "<normalized city>": { city, temp, code, desc, wind } }
const weatherCache = {};

function normalizeCityKey(city) {
    return String(city || "Istanbul").trim().toLowerCase();
}

function getWeatherData(city) {
    return weatherCache[normalizeCityKey(city)] || null;
}

// WMO weather codes are rendered with getWeatherIconSvg() (pure stroke SVG).

// Fetch live weather for a city from the desktop server (Open-Meteo proxy).
function fetchWeatherForCity(city) {
    const target = String(city || "Istanbul").trim() || "Istanbul";
    return authFetch(`/api/weather?city=${encodeURIComponent(target)}`)
        .then(res => res.json())
        .then(data => {
            if (!data || data.success === false) return null;
            const entry = {
                city: data.city || data.weather_city || target,
                temp: Math.round(Number(data.temp !== undefined ? data.temp : (data.weather_temp || 0))),
                code: Number(data.code !== undefined ? data.code : (data.weather_code || 0)),
                desc: data.desc || data.weather_desc || "",
                wind: Math.round(Number(data.wind !== undefined ? data.wind : (data.weather_wind || 0)))
            };
            weatherCache[normalizeCityKey(target)] = entry;
            weatherCache[normalizeCityKey(entry.city)] = entry;
            updateWeatherDOM();
            return entry;
        })
        .catch(err => {
            console.warn("Hava durumu alınamadı:", err);
            return null;
        });
}

// Honest CPU temperature formatting: sensors reporting 0 (absent) are shown as "—"
function formatCpuTemp(temp) {
    const t = Number(temp);
    if (!t || t <= 0) return '— °C';
    return `${Math.round(t)}°C`;
}

// Human readable throughput from raw kilobits-per-second values
function formatNetSpeed(kbps) {
    const v = Number(kbps) || 0;
    if (v >= 1000) return `${(v / 1000).toFixed(1)} Mbps`;
    return `${Math.round(v)} Kbps`;
}

// Generate crisp SVG telemetry arc gauge (Apple Watch tachometer / activity style)
function getTelemetryArcSvg(pct, strokeColor, size = 48, strokeWidth = 4.5) {
    const r = (size - strokeWidth) / 2;
    const cx = size / 2;
    const cy = size / 2;
    const startAngle = 140;
    const totalAngle = 260;
    const sweep = (Math.max(0, Math.min(100, pct)) / 100) * totalAngle;

    const polarToCartesian = (centerX, centerY, radius, angleInDegrees) => {
        const angleInRadians = (angleInDegrees - 90) * Math.PI / 180.0;
        return {
            x: centerX + (radius * Math.cos(angleInRadians)),
            y: centerY + (radius * Math.sin(angleInRadians))
        };
    };

    const describeArc = (x, y, radius, startDeg, endDeg) => {
        const start = polarToCartesian(x, y, radius, endDeg);
        const end = polarToCartesian(x, y, radius, startDeg);
        const arcSweep = endDeg - startDeg <= 180 ? "0" : "1";
        return [
            "M", start.x, start.y, 
            "A", radius, radius, 0, arcSweep, 0, end.x, end.y
        ].join(" ");
    };

    const bgPath = describeArc(cx, cy, r, startAngle, startAngle + totalAngle);
    const fgPath = sweep > 0 ? describeArc(cx, cy, r, startAngle, startAngle + sweep) : '';

    return `
        <svg class="telemetry-arc-svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">
            <path d="${bgPath}" fill="none" stroke="rgba(255,255,255,0.12)" stroke-width="${strokeWidth}" stroke-linecap="round"/>
            ${fgPath ? `<path d="${fgPath}" fill="none" stroke="${strokeColor}" stroke-width="${strokeWidth}" stroke-linecap="round"/>` : ''}
        </svg>
    `;
}

function clampPct(value) {
    const n = Number(value);
    if (!isFinite(n)) return 0;
    return Math.max(0, Math.min(100, Math.round(n)));
}

function thermalLabel(temp) {
    const t = Number(temp) || 0;
    if (t <= 0) return 'SENSÖR YOK';
    if (t >= 80) return 'KRİTİK';
    if (t >= 65) return 'SICAK';
    if (t >= 50) return 'ILIK';
    return 'SERİN';
}

function escapeHtml(value) {
    return String(value === undefined || value === null ? '' : value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

// Weather DOM mirrors the shared live-stat pass
function updateWeatherDOM() {
    updateLiveStatsDOM();
}

// Cities whose weather request is already in flight (avoids refetch loops)
const weatherInFlight = new Set();

function updateLiveStatsDOM() {
    const buttons = (apiConfig && apiConfig.stream_deck_buttons) ? apiConfig.stream_deck_buttons : [];

    // Dynamically update hardware / weather / media widgets without recreating elements
    document.querySelectorAll('.deck-btn[data-id]').forEach(btnEl => {
        const btnId = parseInt(btnEl.getAttribute('data-id'), 10);
        const btn = buttons.find(b => b.id === btnId);
        if (!btn) return;

        if (btn.type === 'weather') {
            const city = btn.value || 'Istanbul';
            const w = getWeatherData(city);
            if (!w) {
                const key = normalizeCityKey(city);
                if (!weatherInFlight.has(key)) {
                    weatherInFlight.add(key);
                    fetchWeatherForCity(city).finally(() => weatherInFlight.delete(key));
                }
                return;
            }
            const tempEl = btnEl.querySelector('.deck-weather-temp');
            const iconEl = btnEl.querySelector('.deck-weather-icon');
            const cityEl = btnEl.querySelector('.deck-weather-city');
            const descEl = btnEl.querySelector('.deck-weather-desc');
            if (tempEl) tempEl.textContent = `${w.temp}°`;
            if (iconEl) iconEl.innerHTML = getWeatherIconSvg(w.code, 22);
            if (cityEl) cityEl.textContent = w.city || city;
            if (descEl) descEl.textContent = `${w.desc || '—'}${w.wind ? ' · ' + w.wind + ' km/s' : ''}`;
            return;
        }

        if (btn.type === 'now_playing' || btn.type === 'media_control') {
            const titleEl = btnEl.querySelector('.deck-np-title');
            const artistEl = btnEl.querySelector('.deck-np-artist');
            const playEl = btnEl.querySelector('.deck-np-play-glyph');
            const title = cachedLiveStats.media_title || (btn.label || 'Şimdi Çalıyor');
            const artist = cachedLiveStats.media_artist || 'Medya bekleniyor';
            if (titleEl) titleEl.textContent = title;
            if (artistEl) artistEl.textContent = artist;
            if (playEl) playEl.innerHTML = cachedLiveStats.media_playing ? mdIcon('pause', 14) : mdIcon('play', 14);
            return;
        }

        if (btn.type !== 'live_info') return;

        // Network throughput widget (download / upload kbps)
        if (btn.value === 'net_speed') {
            const downEl = btnEl.querySelector('.deck-net-down-val');
            const upEl = btnEl.querySelector('.deck-net-up-val');
            if (downEl) downEl.textContent = formatNetSpeed(cachedLiveStats.net_down_kbps);
            if (upEl) upEl.textContent = formatNetSpeed(cachedLiveStats.net_up_kbps);
            refreshSparkline(btnEl, 'net_down', 'net_up');
            return;
        }

        // Dedicated PC temperature card
        if (btn.value === 'cpu_temp') {
            const t = Number(cachedLiveStats.cpu_temp) || 0;
            const col = t <= 0 ? 'var(--text-muted)' : t >= 80 ? 'var(--status-red-txt)' : 'var(--accent-solid)';
            const pct = t > 0 ? clampPct(((t - 30) / (95 - 30)) * 100) : 0;
            const valEl = btnEl.querySelector('.deck-metric-value');
            const subEl = btnEl.querySelector('.deck-metric-sub');
            const fillEl = btnEl.querySelector('.deck-metric-fill');
            if (valEl) { valEl.textContent = formatCpuTemp(t); valEl.style.color = col; }
            if (subEl) subEl.textContent = thermalLabel(t);
            if (fillEl) { fillEl.style.width = `${pct}%`; fillEl.style.background = col; }
            return;
        }

        // Disk occupancy card
        if (btn.value === 'disk') {
            const pct = clampPct(cachedLiveStats.disk_percent);
            const total = Number(cachedLiveStats.disk_total_gb) || 0;
            const free = Number(cachedLiveStats.disk_free_gb) || 0;
            const col = pct >= 90 ? 'var(--status-red-txt)' : 'var(--accent-solid)';
            const valEl = btnEl.querySelector('.deck-metric-value');
            const subEl = btnEl.querySelector('.deck-metric-sub');
            const fillEl = btnEl.querySelector('.deck-metric-fill');
            if (valEl) valEl.textContent = total > 0 ? `${free.toFixed(0)} GB Boş` : 'Okunuyor';
            if (subEl) subEl.textContent = total > 0 ? `%${pct} dolu · ${total.toFixed(0)} GB` : 'C: sürücü';
            if (fillEl) { fillEl.style.width = `${pct}%`; fillEl.style.background = col; }
            return;
        }

        // GPU load card
        if (btn.value === 'gpu') {
            const pct = clampPct(cachedLiveStats.gpu);
            const t = Number(cachedLiveStats.gpu_temp) || 0;
            const col = pct >= 90 ? 'var(--status-red-txt)' : 'var(--accent-solid)';
            const valEl = btnEl.querySelector('.deck-metric-value');
            const subEl = btnEl.querySelector('.deck-metric-sub');
            const fillEl = btnEl.querySelector('.deck-metric-fill');
            if (valEl) valEl.textContent = `%${pct}`;
            if (subEl) subEl.textContent = t > 0 ? `${t}°C` : 'GPU yükü';
            if (fillEl) { fillEl.style.width = `${pct}%`; fillEl.style.background = col; }
            return;
        }

        const loadTone = (pct) => pct >= 90 ? 'var(--status-red-txt)' : 'var(--accent-solid)';
        const isCombo = btn.col_span >= 2 || btn.value === 'combo' || btn.value === 'system';
        if (isCombo) {
            const cpuTone = loadTone(cachedLiveStats.cpu);
            const ramTone = loadTone(cachedLiveStats.ram);
            const t = Number(cachedLiveStats.cpu_temp) || 0;
            const tempPct = t > 0 ? clampPct(((t - 30) / (95 - 30)) * 100) : 0;
            const tempTone = t <= 0 ? 'var(--text-muted)' : t >= 80 ? 'var(--status-red-txt)' : 'var(--accent-solid)';

            const cpuSvg = btnEl.querySelector('.md-ring-cpu-svg');
            const cpuVal = btnEl.querySelector('.md-ring-cpu-val');
            const ramSvg = btnEl.querySelector('.md-ring-ram-svg');
            const ramVal = btnEl.querySelector('.md-ring-ram-val');
            const tempSvg = btnEl.querySelector('.md-ring-temp-svg');
            const tempVal = btnEl.querySelector('.md-ring-temp-val');

            if (cpuSvg) { cpuSvg.style.color = cpuTone; cpuSvg.innerHTML = getTelemetryArcSvg(cachedLiveStats.cpu, 'currentColor', 46, 4); }
            if (cpuVal) { cpuVal.textContent = `%${cachedLiveStats.cpu}`; cpuVal.style.color = cpuTone; }
            if (ramSvg) { ramSvg.style.color = ramTone; ramSvg.innerHTML = getTelemetryArcSvg(cachedLiveStats.ram, 'currentColor', 46, 4); }
            if (ramVal) { ramVal.textContent = `%${cachedLiveStats.ram}`; ramVal.style.color = ramTone; }
            if (tempSvg) { tempSvg.style.color = tempTone; tempSvg.innerHTML = getTelemetryArcSvg(tempPct, 'currentColor', 46, 4); }
            if (tempVal) { tempVal.textContent = t > 0 ? `${t}°C` : '—'; tempVal.style.color = tempTone; }
            refreshSparkline(btnEl, 'cpu', 'ram');
        } else {
            const isCpu = (btn.value || '').includes('cpu');
            const pct = isCpu ? cachedLiveStats.cpu : cachedLiveStats.ram;
            const col = loadTone(pct);
            const extra = isCpu
                ? `${formatCpuTemp(cachedLiveStats.cpu_temp)} ısı`
                : `${cachedLiveStats.ram_used_gb}/${cachedLiveStats.ram_total_gb} GB`;

            const valEl = btnEl.querySelector('.deck-metric-value');
            const subEl = btnEl.querySelector('.deck-metric-sub');
            const fillEl = btnEl.querySelector('.deck-metric-fill');
            if (valEl) valEl.textContent = `%${pct}`;
            if (subEl) subEl.textContent = extra;
            if (fillEl) { fillEl.style.width = `${pct}%`; fillEl.style.background = col; }
            refreshSparkline(btnEl, isCpu ? 'cpu' : 'ram');
        }
    });
}

// ----------------- Pomodoro Countdown Widget (server-authoritative) -----------------
// The desktop server owns the 25/5 focus cycle and broadcasts {"type":"pomodoro", ...}
// state changes; the client only seeds from it and ticks locally for a smooth countdown.
let pomodoroState = {
    running: false,
    phase: 'idle',
    remaining: 0,
    work_minutes: 25,
    break_minutes: 5,
    syncedAt: 0
};

function applyPomodoroState(msg) {
    if (!msg) return;
    pomodoroState.running = !!msg.running;
    pomodoroState.phase = msg.phase || (pomodoroState.running ? 'work' : 'idle');
    pomodoroState.remaining = Number(msg.remaining_seconds) || 0;
    if (msg.work_minutes) pomodoroState.work_minutes = msg.work_minutes;
    if (msg.break_minutes) pomodoroState.break_minutes = msg.break_minutes;
    pomodoroState.syncedAt = Date.now();
    renderPomodoroDOM();
}

function currentPomodoroSeconds() {
    if (!pomodoroState.running) return pomodoroState.remaining;
    const elapsed = Math.floor((Date.now() - pomodoroState.syncedAt) / 1000);
    return Math.max(0, pomodoroState.remaining - elapsed);
}

function formatPomodoro(totalSeconds) {
    const s = Math.max(0, Math.round(totalSeconds));
    const m = Math.floor(s / 60);
    const r = s % 60;
    return `${m.toString().padStart(2, '0')}:${r.toString().padStart(2, '0')}`;
}

function pomodoroPhaseLabel() {
    if (pomodoroState.running) {
        return pomodoroState.phase === 'break' ? 'MOLA' : 'ODAK';
    }
    if (pomodoroState.phase === 'idle' && currentPomodoroSeconds() <= 0) return 'HAZIR';
    return 'DURDU';
}

function renderPomodoroDOM() {
    document.querySelectorAll('.deck-pomodoro-widget').forEach(widget => {
        const timeEl = widget.querySelector('.deck-pomodoro-time');
        const statusEl = widget.querySelector('.deck-pomodoro-status');
        const glyphEl = widget.querySelector('.deck-pomodoro-toggle-glyph');
        if (timeEl) timeEl.textContent = formatPomodoro(currentPomodoroSeconds());
        if (statusEl) statusEl.textContent = pomodoroPhaseLabel();
        if (glyphEl) glyphEl.innerHTML = pomodoroState.running ? mdIcon('pause', 14) : mdIcon('play', 14);
        widget.classList.toggle('running', pomodoroState.running);
    });
}

// Tapping the widget presses its Stream Deck button; the server then toggles the
// Pomodoro cycle and broadcasts the new state to every client.
function triggerPomodoroWidget(buttonId) {
    const el = document.querySelector(`.deck-btn[data-id="${buttonId}"]`);
    testButtonAction(buttonId, el);
}

// Ask the desktop server for the authoritative Pomodoro state on load.
function syncPomodoroState() {
    authFetch('/api/pomodoro')
        .then(res => res.json())
        .then(data => { if (data && data.success !== false) applyPomodoroState(data); })
        .catch(err => console.warn("Pomodoro durumu okunamadı", err));
}

// Local 1s tick keeps the countdown smooth between server broadcasts.
setInterval(() => {
    if (document.querySelector('.deck-pomodoro-widget')) renderPomodoroDOM();
}, 1000);

// 1-second interval to tick Clock widgets smoothly
setInterval(() => {
    document.querySelectorAll('.deck-clock-widget').forEach(clock => {
        const timeEl = clock.querySelector('.deck-clock-time');
        const dateEl = clock.querySelector('.deck-clock-date');
        const now = new Date();
        if (timeEl) {
            timeEl.textContent = now.toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
        }
        if (dateEl) {
            dateEl.textContent = now.toLocaleDateString('tr-TR', { weekday: 'short', day: 'numeric', month: 'short' });
        }
    });
}, 1000);

// 1-second tick for StandBy flip clocks and world clocks
setInterval(() => {
    const now = new Date();
    const hh = now.getHours().toString().padStart(2, '0');
    const mm = now.getMinutes().toString().padStart(2, '0');
    document.querySelectorAll('.deck-flip-widget').forEach(widget => {
        const nums = widget.querySelectorAll('.deck-flip-num');
        if (nums.length >= 2) {
            if (nums[0].textContent !== hh) nums[0].textContent = hh;
            if (nums[1].textContent !== mm) nums[1].textContent = mm;
        }
    });

    document.querySelectorAll('.deck-world-clock').forEach(clock => {
        const localEl = clock.querySelector('.deck-world-local');
        const nyEl = clock.querySelector('.deck-world-ny');
        if (localEl) localEl.textContent = now.toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit' });
        if (nyEl) {
            try {
                nyEl.textContent = new Date(now.toLocaleString('en-US', { timeZone: 'America/New_York' })).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit' });
            } catch (e) { /* keep previous value */ }
        }
    });
}, 1000);

// Trigger media action (Prev, Play/Pause, Next)
function triggerMediaAction(action) {
    if (wsClient && wsClient.readyState === WebSocket.OPEN) {
        wsClient.send(JSON.stringify({
            type: "media_control",
            action: action
        }));
    } else {
        authFetch('/stream_deck_trigger', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action: action })
        }).catch(e => console.error("Media trigger error", e));
    }
}

// Map preset keys to button properties
function getWidgetPropsForPreset(presetKey) {
    let p = {
        label: "Yeni Buton",
        type: "hotkey",
        value: "ctrl+c",
        icon: "custom",
        col_span: 1,
        row_span: 1,
        color: "#F4F4F5",
        state: false
    };

    switch (presetKey) {
        case 'clock':
            p.label = 'Saat & Tarih';
            p.type = 'clock_widget';
            p.value = '';
            p.icon = 'clock';
            p.col_span = 2;
            p.row_span = 1;
            break;
        case 'mini_clock':
            p.label = 'Saat';
            p.type = 'clock_widget';
            p.value = '';
            p.icon = 'clock';
            p.col_span = 1;
            p.row_span = 1;
            p.clock_style = 'apple_stacked';
            break;
        case 'clock_analog':
            p.label = 'Kadran';
            p.type = 'clock_widget';
            p.value = '';
            p.icon = 'clock';
            p.col_span = 1;
            p.row_span = 1;
            p.clock_style = 'analog';
            break;
        case 'clock_seconds':
            p.label = 'Canlı Dijital';
            p.type = 'clock_widget';
            p.value = '';
            p.icon = 'clock';
            p.col_span = 2;
            p.row_span = 1;
            p.clock_style = 'digital_seconds';
            break;
        case 'telemetry_combo':
            p.label = 'Donanım Telemetri';
            p.type = 'live_info';
            p.value = 'combo';
            p.icon = 'cpu';
            p.col_span = 2;
            p.row_span = 1;
            break;
        case 'telemetry_ring':
            p.label = 'Aktivite Halkası';
            p.type = 'live_info';
            p.value = 'activity_ring';
            p.icon = 'cpu';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'net_speed':
            p.label = 'Ağ Hızı';
            p.type = 'live_info';
            p.value = 'net_speed';
            p.icon = 'wifi';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'pomodoro_timer':
            p.label = 'Pomodoro (25dk)';
            p.type = 'toggle';
            p.value = 'pomodoro';
            p.icon = 'clock';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'world_clock':
            p.label = 'Dünya Saati';
            p.type = 'clock_widget';
            p.value = '';
            p.icon = 'clock';
            p.clock_style = 'world_clock';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'flip_clock':
            p.label = 'StandBy Saat';
            p.type = 'clock_widget';
            p.value = '';
            p.icon = 'clock';
            p.clock_style = 'flip_clock';
            p.col_span = 2;
            p.row_span = 1;
            break;
        case 'weather':
            p.label = 'Hava Durumu';
            p.type = 'weather';
            p.value = 'Istanbul';
            p.icon = 'weather';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'cpu_temp':
            p.label = 'PC Sıcaklık';
            p.type = 'live_info';
            p.value = 'cpu_temp';
            p.icon = 'thermometer';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'disk':
            p.label = 'Disk Doluluk';
            p.type = 'live_info';
            p.value = 'disk';
            p.icon = 'disk';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'gpu':
            p.label = 'GPU Yükü';
            p.type = 'live_info';
            p.value = 'gpu';
            p.icon = 'gpu';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'now_playing':
            p.label = 'Şimdi Çalıyor';
            p.type = 'now_playing';
            p.value = 'media_session';
            p.icon = 'music';
            p.col_span = 2;
            p.row_span = 1;
            break;
        case 'cpu':
            p.label = 'CPU Yükü';
            p.type = 'live_info';
            p.value = 'cpu';
            p.icon = 'cpu';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'ram':
            p.label = 'RAM Bellek';
            p.type = 'live_info';
            p.value = 'ram';
            p.icon = 'ram';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'volume':
            p.label = 'Ana Ses';
            p.type = 'volume_slider';
            p.value = 'master_volume';
            p.icon = 'volume';
            p.col_span = 2;
            p.row_span = 1;
            break;
        case 'app_session':
            p.label = 'Uygulama Sesi';
            p.type = 'app_volume_slider';
            p.value = '';
            p.icon = 'app_session';
            p.col_span = 2;
            p.row_span = 1;
            break;
        case 'brightness':
            p.label = 'Parlaklık';
            p.type = 'brightness_slider';
            p.value = 'display_brightness';
            p.icon = 'bright';
            p.col_span = 2;
            p.row_span = 1;
            break;
        case 'media':
            p.label = 'Medya Masası';
            p.type = 'media_widget';
            p.value = 'playpause';
            p.icon = 'play';
            p.col_span = 2;
            p.row_span = 1;
            break;
        case 'playpause_single':
            p.label = 'Oynat/Durdur';
            p.type = 'hotkey';
            p.value = 'playpause';
            p.icon = 'play';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'toggle_mic':
            p.label = 'Mikrofon';
            p.type = 'toggle';
            p.value = 'mic_mute';
            p.icon = 'mic';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'toggle_mute':
            p.label = 'Sesi Kapat';
            p.type = 'toggle';
            p.value = 'sound_mute';
            p.icon = 'voldown';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'toggle_monitor':
            p.label = 'Sanal Ekran';
            p.type = 'toggle';
            p.value = 'virtual_monitor';
            p.icon = 'custom';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_taskmgr':
            p.label = 'Görev Yöneticisi';
            p.type = 'command';
            p.value = 'taskmgr.exe';
            p.icon = 'task';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_calc':
            p.label = 'Hesap Makinesi';
            p.type = 'command';
            p.value = 'calc.exe';
            p.icon = 'calc';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_lock':
            p.label = 'PC Kilitle';
            p.type = 'command';
            p.value = 'rundll32.exe user32.dll,LockWorkStation';
            p.icon = 'lock';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_terminal':
            p.label = 'Terminal / CMD';
            p.type = 'command';
            p.value = 'cmd.exe';
            p.icon = 'custom';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_snippet':
            p.label = 'Ekran Alıntısı';
            p.type = 'hotkey';
            p.value = 'meta+shift+s';
            p.icon = 'task';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_url':
            p.label = 'Web Sitesi';
            p.type = 'url';
            p.value = 'https://youtube.com';
            p.icon = 'link';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_discord':
            p.label = 'Discord';
            p.type = 'hotkey';
            p.value = 'ctrl+shift+m';
            p.icon = 'mic';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_spotify':
            p.label = 'Spotify Web';
            p.type = 'url';
            p.value = 'https://open.spotify.com';
            p.icon = 'music';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'night_light':
            p.label = 'Gece Işığı';
            p.type = 'hotkey';
            p.value = 'meta+a';
            p.icon = 'bright';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'discord_deafen':
            p.label = 'Sağırlaştır';
            p.type = 'hotkey';
            p.value = 'ctrl+shift+d';
            p.icon = 'headphones';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'obs_stream':
            p.label = 'OBS Yayın';
            p.type = 'hotkey';
            p.value = 'ctrl+f9';
            p.icon = 'camera';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'clean_clipboard':
            p.label = 'Panoyu Temizle';
            p.type = 'command';
            p.value = 'clean_clipboard';
            p.icon = 'copy';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'empty_trash':
            p.label = 'Çöpü Boşalt';
            p.type = 'command';
            p.value = 'empty_trash';
            p.icon = 'trash';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_files':
            p.label = 'Dosya Gezgini';
            p.type = 'command';
            p.value = 'explorer.exe';
            p.icon = 'folder';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_settings':
            p.label = 'Windows Ayarlar';
            p.type = 'hotkey';
            p.value = 'meta+i';
            p.icon = 'custom';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_cam':
            p.label = 'Kamera Aç';
            p.type = 'hotkey';
            p.value = 'meta+alt+c';
            p.icon = 'camera';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_steam':
            p.label = 'Steam';
            p.type = 'command';
            p.value = 'steam.exe';
            p.icon = 'custom';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'media_prev':
            p.label = 'Önceki Parça';
            p.type = 'hotkey';
            p.value = 'prevtrack';
            p.icon = 'play';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'media_next':
            p.label = 'Sonraki Parça';
            p.type = 'hotkey';
            p.value = 'nexttrack';
            p.icon = 'play';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_notepad':
            p.label = 'Not Defteri';
            p.type = 'command';
            p.value = 'notepad.exe';
            p.icon = 'copy';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_snippet_tool':
            p.label = 'Ekran Kırp';
            p.type = 'hotkey';
            p.value = 'meta+shift+s';
            p.icon = 'custom';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'system_refresh':
            p.label = 'Yenile';
            p.type = 'hotkey';
            p.value = 'f5';
            p.icon = 'refresh';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_desktop':
            p.label = 'Masaüstü';
            p.type = 'hotkey';
            p.value = 'meta+d';
            p.icon = 'desktop';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'volume_up':
            p.label = 'Ses +';
            p.type = 'hotkey';
            p.value = 'volumeup';
            p.icon = 'volup';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'volume_down':
            p.label = 'Ses -';
            p.type = 'hotkey';
            p.value = 'volumedown';
            p.icon = 'voldown';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'clock_digital_clean':
            p.label = 'Sade Saat';
            p.type = 'clock_widget';
            p.icon = 'clock';
            p.clock_style = 'digital_clean';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_code':
            p.label = 'VS Code';
            p.type = 'command';
            p.value = 'code';
            p.icon = 'terminal';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_youtube':
            p.label = 'YouTube';
            p.type = 'url';
            p.value = 'https://youtube.com';
            p.icon = 'play';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_chatgpt':
            p.label = 'ChatGPT';
            p.type = 'url';
            p.value = 'https://chatgpt.com';
            p.icon = 'web';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_claude':
            p.label = 'Claude';
            p.type = 'url';
            p.value = 'https://claude.ai';
            p.icon = 'ai';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'obs_record':
            p.label = 'OBS Kayıt';
            p.type = 'hotkey';
            p.value = 'ctrl+f10';
            p.icon = 'camera';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'hotkey':
        default:
            p.label = 'Kısayol Tuşu';
            p.type = 'hotkey';
            p.value = 'ctrl+c';
            p.icon = 'custom';
            p.col_span = 1;
            p.row_span = 1;
            break;
        case 'app_open':
            p.label = 'Uygulama Aç';
            p.type = 'command';
            p.value = '';
            p.icon = 'desktop';
            p.col_span = 1;
            p.row_span = 1;
            break;
    }
    return p;
}

// 1-Click Widget Creation from Preset Library.
// `cell` (or the last clicked empty slot) pins the new component to that exact
// (grid_row, grid_col) cell instead of appending it to the end of the page.
function addDeckButtonFromProps(props, cell = null) {
    if (!apiConfig) return null;
    if (!apiConfig.stream_deck_buttons) apiConfig.stream_deck_buttons = [];
    const newId = apiConfig.stream_deck_buttons.length > 0
        ? Math.max(...apiConfig.stream_deck_buttons.map(b => b.id)) + 1
        : 0;
    const activePg = apiConfig.active_page || 0;
    const newBtn = { id: newId, page: activePg, ...props, grid_row: -1, grid_col: -1 };

    // Son tıklanan boş yuva (veya yuvaya bırakma) kazanır: bileşen tam oraya oturur
    const target = cell || targetEmptyCell;
    if (target) {
        const rows = Math.max(1, apiConfig.stream_deck_rows || 2);
        const cols = Math.max(1, apiConfig.stream_deck_cols || 4);
        const pageButtons = apiConfig.stream_deck_buttons.filter(b => (b.page || 0) === activePg);
        const layout = packBentoGrid(pageButtons, rows, cols);
        const occ = layout.occupied;
        if (occ && occ[target.row] && occ[target.row][target.col] === false) {
            const span = freeSpanAt(occ, rows, cols, target.row, target.col, props.row_span || 1, props.col_span || 1);
            newBtn.row_span = span.rs;
            newBtn.col_span = span.cs;
            newBtn.grid_row = target.row;
            newBtn.grid_col = target.col;
        }
    }
    targetEmptyCell = null;

    apiConfig.stream_deck_buttons.push(newBtn);
    return newId;
}

// Tek tıkla bileşen ekleme: son tıklanan boş yuva varsa kart tam oraya oturur,
// yoksa aktif sayfanın sonuna eklenir.
function createWidgetFromPreset(presetKey, cell = null) {
    const newId = addDeckButtonFromProps(getWidgetPropsForPreset(presetKey), cell);
    if (newId === null) return;
    renderStreamDeckButtons();
    selectDeckButton(newId);
    debouncedSaveConfig(300);

    // The per-app volume card needs its target application immediately, so open
    // the editor where the live Core Audio session list can be picked from.
    if (presetKey === 'app_session') {
        openEditButtonModal(newId);
        return;
    }

    setTimeout(() => {
        const newEl = document.querySelector('.deck-btn[data-id="' + newId + '"]');
        if (newEl) {
            newEl.classList.add('deck-btn-just-added');
            newEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
            setTimeout(() => newEl.classList.remove('deck-btn-just-added'), 800);
        }
    }, 40);
}

// Boş bir özel buton açar ve sade düzenleme penceresini gösterir
window.createAndEditCustomButton = function () {
    const newId = addDeckButtonFromProps({
        label: 'Özel Buton',
        type: 'hotkey',
        value: 'ctrl+c',
        icon: 'custom',
        color: '#F4F4F5',
        bg_style: 'default',
        col_span: 1,
        row_span: 1,
        state: false
    });
    if (newId === null) return;
    renderStreamDeckButtons();
    debouncedSaveConfig(300);
    openEditButtonModal(newId);
};

// Generate crisp SVG analog clock face (Braun / Apple Watch luxury minimalist style)
function getAnalogClockSvg(hours, minutes, seconds, size = 76) {
    const hDeg = (hours % 12 + minutes / 60) * 30;
    const mDeg = (minutes + seconds / 60) * 6;
    const sDeg = seconds * 6;

    let batons = '';
    for (let i = 0; i < 12; i++) {
        const deg = i * 30;
        if (i === 0) {
            // Double luminous baton at 12 o'clock
            batons += `
                <line x1="48" y1="8" x2="48" y2="18" stroke="#F8FAFC" stroke-width="2.2" stroke-linecap="round" />
                <line x1="52" y1="8" x2="52" y2="18" stroke="#F8FAFC" stroke-width="2.2" stroke-linecap="round" />
            `;
        } else if (i % 3 === 0) {
            // 3, 6, 9
            batons += `<line x1="50" y1="8" x2="50" y2="18" stroke="#F8FAFC" stroke-width="2.5" stroke-linecap="round" transform="rotate(${deg} 50 50)"/>`;
        } else {
            batons += `<line x1="50" y1="8" x2="50" y2="15" stroke="rgba(226,232,240,0.65)" stroke-width="1.5" stroke-linecap="round" transform="rotate(${deg} 50 50)"/>`;
        }
    }

    return `
        <svg class="analog-clock-svg" width="${size}" height="${size}" viewBox="0 0 100 100">
            <defs>
                <radialGradient id="dialGrad" cx="50%" cy="50%" r="50%">
                    <stop offset="0%" stop-color="#1e2130" />
                    <stop offset="100%" stop-color="#0f1017" />
                </radialGradient>
            </defs>
            <!-- Dial Face -->
            <circle cx="50" cy="50" r="44" fill="url(#dialGrad)" stroke="rgba(255,255,255,0.18)" stroke-width="1.4"/>
            ${batons}
            <!-- Hour Hand -->
            <line x1="50" y1="50" x2="50" y2="26" stroke="#ffffff" stroke-width="3.5" stroke-linecap="round" transform="rotate(${hDeg} 50 50)"/>
            <!-- Minute Hand -->
            <line x1="50" y1="50" x2="50" y2="17" stroke="#e2e8f0" stroke-width="2.4" stroke-linecap="round" transform="rotate(${mDeg} 50 50)"/>
            <!-- Second Hand with Counterbalance -->
            <g transform="rotate(${sDeg} 50 50)">
                <line x1="50" y1="60" x2="50" y2="12" stroke="#ff453a" stroke-width="1.2" stroke-linecap="round"/>
                <circle cx="50" cy="56" r="2.2" fill="none" stroke="#ff453a" stroke-width="1"/>
            </g>
            <!-- Center Pinion -->
            <circle cx="50" cy="50" r="3.2" fill="#e2e8f0"/>
            <circle cx="50" cy="50" r="1.5" fill="#ff453a"/>
        </svg>
    `;
}

// Interactive edge drag-resize setup for cards
function setupEdgeResize(item, btn) {
    const handles = item.querySelectorAll('.deck-resize-edge');
    handles.forEach(handle => {
        let isDragging = false;
        let startX = 0, startY = 0;
        let startCol = btn.col_span || 1;
        let startRow = btn.row_span || 1;
        let hasMoved = false;

        const onMouseMove = (e) => {
            if (!isDragging) return;
            const dx = e.clientX - startX;
            const dy = e.clientY - startY;
            if (Math.abs(dx) > 10 || Math.abs(dy) > 10) {
                hasMoved = true;
            }

            const isEast = handle.classList.contains('deck-resize-e') || handle.classList.contains('deck-resize-se');
            const isSouth = handle.classList.contains('deck-resize-s') || handle.classList.contains('deck-resize-se');

            let targetCol = startCol;
            let targetRow = startRow;

            if (isEast) {
                const colDelta = Math.round(dx / 110);
                targetCol = Math.min(6, Math.max(1, startCol + colDelta));
            }
            if (isSouth) {
                const rowDelta = Math.round(dy / 80);
                targetRow = Math.min(4, Math.max(1, startRow + rowDelta));
            }

            if (targetCol !== (btn.col_span || 1) || targetRow !== (btn.row_span || 1)) {
                btn.col_span = targetCol;
                btn.row_span = targetRow;
                // Live preview clipped to the matrix (packer re-clamps on release)
                const gridCols = (apiConfig && apiConfig.stream_deck_cols) || 4;
                const gridRows = (apiConfig && apiConfig.stream_deck_rows) || 2;
                const startColIdx = parseInt(item.style.gridColumn, 10) || 1;
                const startRowIdx = parseInt(item.style.gridRow, 10) || 1;
                const cs = Math.max(1, Math.min(targetCol, gridCols - startColIdx + 1));
                const rs = Math.max(1, Math.min(targetRow, gridRows - startRowIdx + 1));
                item.style.gridColumn = `${startColIdx} / span ${cs}`;
                item.style.gridRow = `${startRowIdx} / span ${rs}`;
            }
        };

        const onMouseUp = () => {
            if (!isDragging) return;
            isDragging = false;
            window.removeEventListener('mousemove', onMouseMove);
            window.removeEventListener('mouseup', onMouseUp);
            document.body.classList.remove('deck-resizing');

            if (hasMoved) {
                renderStreamDeckButtons();
                debouncedSaveConfig(300);
            } else if (handle.classList.contains('deck-resize-se')) {
                // Quick cycle on single click
                const cCol = btn.col_span || 1;
                const cRow = btn.row_span || 1;
                if (cCol === 1 && cRow === 1) { btn.col_span = 2; btn.row_span = 1; }
                else if (cCol === 2 && cRow === 1) { btn.col_span = 1; btn.row_span = 2; }
                else if (cCol === 1 && cRow === 2) { btn.col_span = 2; btn.row_span = 2; }
                else { btn.col_span = 1; btn.row_span = 1; }
                renderStreamDeckButtons();
                debouncedSaveConfig(300);
            }
        };

        handle.addEventListener('mousedown', (e) => {
            e.stopPropagation();
            e.preventDefault();
            isDragging = true;
            hasMoved = false;
            startX = e.clientX;
            startY = e.clientY;
            startCol = btn.col_span || 1;
            startRow = btn.row_span || 1;
            document.body.classList.add('deck-resizing');
            window.addEventListener('mousemove', onMouseMove);
            window.addEventListener('mouseup', onMouseUp);
        });
    });
}

// ========================================================
// STREAM DECK APPLE-DESIGN STATE & ERGONOMIC CONTROLS
// ========================================================
let selectedDeckButtonId = null;
// The exact (row, col) of the empty slot the user last clicked — the next
// component from the library lands there instead of being appended.
let targetEmptyCell = null;
let lastDeletedButton = null;
let toastTimeout = null;
let activeContextMenu = null;

function showAppleToast(message, actionText = null, actionCallback = null, duration = 4500) {
    let toast = document.getElementById('apple-toast-pill');
    if (!toast) {
        toast = document.createElement('div');
        toast.id = 'apple-toast-pill';
        toast.className = 'apple-toast-pill';
        document.body.appendChild(toast);
    }
    
    if (toastTimeout) clearTimeout(toastTimeout);
    
    toast.innerHTML = `
        <span class="apple-toast-text">${message}</span>
        ${actionText ? `<button type="button" class="apple-toast-action" id="apple-toast-action-btn">${actionText}</button>` : ''}
    `;
    
    if (actionText && actionCallback) {
        const actionBtn = document.getElementById('apple-toast-action-btn');
        if (actionBtn) {
            actionBtn.onclick = (e) => {
                e.stopPropagation();
                actionCallback();
                hideAppleToast();
            };
        }
    }
    
    requestAnimationFrame(() => {
        toast.classList.add('visible');
    });
    
    toastTimeout = setTimeout(() => {
        hideAppleToast();
    }, duration);
}

function hideAppleToast() {
    const toast = document.getElementById('apple-toast-pill');
    if (toast) toast.classList.remove('visible');
}

function deleteStreamDeckButtonWithUndo(id) {
    if (!apiConfig || !apiConfig.stream_deck_buttons) return;
    const btnIndex = apiConfig.stream_deck_buttons.findIndex(b => b.id === id);
    if (btnIndex === -1) return;
    
    const removedBtn = apiConfig.stream_deck_buttons[btnIndex];
    lastDeletedButton = {
        button: JSON.parse(JSON.stringify(removedBtn)),
        index: btnIndex
    };
    
    apiConfig.stream_deck_buttons.splice(btnIndex, 1);
    if (selectedDeckButtonId === id) {
        selectedDeckButtonId = null;
    }
    
    renderStreamDeckButtons();
    debouncedSaveConfig(300);
    
    showAppleToast(`"${removedBtn.label || 'Buton'}" kaldırıldı`, 'Geri Al (Ctrl+Z)', () => {
        undoDeleteStreamDeckButton();
    });
}

function undoDeleteStreamDeckButton() {
    if (!lastDeletedButton || !apiConfig) return;
    if (!apiConfig.stream_deck_buttons) apiConfig.stream_deck_buttons = [];
    
    const { button, index } = lastDeletedButton;
    const insertIdx = Math.min(index, apiConfig.stream_deck_buttons.length);
    apiConfig.stream_deck_buttons.splice(insertIdx, 0, button);
    selectedDeckButtonId = button.id;
    lastDeletedButton = null;
    
    renderStreamDeckButtons();
    debouncedSaveConfig(300);
    showAppleToast(`"${button.label || 'Buton'}" geri yüklendi`);
}

function duplicateStreamDeckButton(id) {
    if (!apiConfig || !apiConfig.stream_deck_buttons) return;
    const btn = apiConfig.stream_deck_buttons.find(b => b.id === id);
    if (!btn) return;
    
    const newId = apiConfig.stream_deck_buttons.length > 0 
        ? Math.max(...apiConfig.stream_deck_buttons.map(b => b.id)) + 1 
        : 0;
        
    const clone = JSON.parse(JSON.stringify(btn));
    clone.id = newId;
    clone.label = (clone.label || 'Buton') + ' (Kopya)';
    // The copy auto-packs into the next free cell instead of fighting its source
    clone.grid_row = -1;
    clone.grid_col = -1;
    
    const originalIdx = apiConfig.stream_deck_buttons.findIndex(b => b.id === id);
    apiConfig.stream_deck_buttons.splice(originalIdx + 1, 0, clone);
    
    selectedDeckButtonId = newId;
    renderStreamDeckButtons();
    debouncedSaveConfig(300);
    showAppleToast(`"${clone.label}" kopyalandı`);
}

function setStreamDeckButtonSize(id, colSpan, rowSpan) {
    if (!apiConfig || !apiConfig.stream_deck_buttons) return;
    const btn = apiConfig.stream_deck_buttons.find(b => b.id === id);
    if (!btn) return;

    const maxCols = Math.max(1, apiConfig.stream_deck_cols || 4);
    const maxRows = Math.max(1, apiConfig.stream_deck_rows || 2);
    btn.col_span = Math.max(1, Math.min(maxCols, colSpan));
    btn.row_span = Math.max(1, Math.min(maxRows, rowSpan));

    // An explicit cell must stay inside the matrix once the span grows
    if (btn.grid_row >= 0) btn.grid_row = Math.max(0, Math.min(maxRows - btn.row_span, btn.grid_row));
    if (btn.grid_col >= 0) btn.grid_col = Math.max(0, Math.min(maxCols - btn.col_span, btn.grid_col));

    renderStreamDeckButtons();
    debouncedSaveConfig(300);
    showAppleToast(`Boyut: ${btn.col_span}×${btn.row_span}`);
}

function cycleStreamDeckButtonSize(id) {
    if (!apiConfig || !apiConfig.stream_deck_buttons) return;
    const btn = apiConfig.stream_deck_buttons.find(b => b.id === id);
    if (!btn) return;

    const cCol = btn.col_span || 1;
    const cRow = btn.row_span || 1;
    let nextCol = 1, nextRow = 1;

    if (cCol === 1 && cRow === 1) {
        nextCol = 2; nextRow = 1; // 2x1 wide
    } else if (cCol === 2 && cRow === 1) {
        nextCol = 1; nextRow = 2; // 1x2 tall
    } else if (cCol === 1 && cRow === 2) {
        nextCol = 2; nextRow = 2; // 2x2 square
    } else {
        nextCol = 1; nextRow = 1; // 1x1 standard
    }

    btn.col_span = Math.min(nextCol, Math.max(1, apiConfig.stream_deck_cols || 4));
    btn.row_span = Math.min(nextRow, Math.max(1, apiConfig.stream_deck_rows || 2));
    if (btn.grid_row >= 0) btn.grid_row = Math.max(0, Math.min((apiConfig.stream_deck_rows || 2) - btn.row_span, btn.grid_row));
    if (btn.grid_col >= 0) btn.grid_col = Math.max(0, Math.min((apiConfig.stream_deck_cols || 4) - btn.col_span, btn.grid_col));
    renderStreamDeckButtons();
    debouncedSaveConfig(300);
    showAppleToast(`Boyut: ${btn.col_span}×${btn.row_span}`);
}

function closeActiveContextMenu() {
    if (activeContextMenu && activeContextMenu.parentNode) {
        activeContextMenu.parentNode.removeChild(activeContextMenu);
    }
    activeContextMenu = null;
}

function showAppleContextMenu(e, btn) {
    e.preventDefault();
    e.stopPropagation();
    closeActiveContextMenu();
    
    selectedDeckButtonId = btn.id;
    document.querySelectorAll('.deck-btn').forEach(el => {
        el.classList.toggle('selected', parseInt(el.getAttribute('data-id')) === btn.id);
    });
    
    const menu = document.createElement('div');
    menu.className = 'apple-context-menu';
    
    const colSpan = btn.col_span || 1;
    const rowSpan = btn.row_span || 1;
    const sizeStr = `${colSpan}×${rowSpan}`;
    
    menu.innerHTML = `
        <div class="apple-menu-header">
            <span class="apple-menu-title">${btn.label || 'İsimsiz Buton'}</span>
            <span class="apple-menu-badge">${sizeStr}</span>
        </div>
        <div class="apple-menu-size-segmented">
            <button type="button" class="apple-size-btn ${colSpan === 1 && rowSpan === 1 ? 'active' : ''}" data-col="1" data-row="1" data-tooltip="1×1 Standart">
                <span class="size-symbol">${mdIcon('square', 12)}</span> 1×1
            </button>
            <button type="button" class="apple-size-btn ${colSpan === 2 && rowSpan === 1 ? 'active' : ''}" data-col="2" data-row="1" data-tooltip="2×1 Yatay Geniş">
                <span class="size-symbol">${mdIcon('wide', 12)}</span> 2×1
            </button>
            <button type="button" class="apple-size-btn ${colSpan === 1 && rowSpan === 2 ? 'active' : ''}" data-col="1" data-row="2" data-tooltip="1×2 Dikey Uzun">
                <span class="size-symbol">${mdIcon('tall', 12)}</span> 1×2
            </button>
            <button type="button" class="apple-size-btn ${colSpan === 2 && rowSpan === 2 ? 'active' : ''}" data-col="2" data-row="2" data-tooltip="2×2 Büyük Kare">
                <span class="size-symbol">${mdIcon('grid', 12)}</span> 2×2
            </button>
        </div>
        <div class="apple-menu-divider"></div>
        <div class="apple-menu-item" data-action="edit">
            <span class="menu-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z"/></svg>
                Düzenle & Özelleştir
            </span>
            <span class="menu-shortcut">Enter</span>
        </div>
        <div class="apple-menu-item" data-action="duplicate">
            <span class="menu-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                Çoğalt (Klonla)
            </span>
            <span class="menu-shortcut">Ctrl+D</span>
        </div>
        <div class="apple-menu-item" data-action="test">
            <span class="menu-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="5 3 19 12 5 21 5 3"/></svg>
                Eylemi Test Et
            </span>
        </div>
        <div class="apple-menu-divider"></div>
        <div class="apple-menu-item danger" data-action="delete">
            <span class="menu-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
                Butonu Sil
            </span>
            <span class="menu-shortcut">Del</span>
        </div>
    `;
    
    document.body.appendChild(menu);
    activeContextMenu = menu;
    
    const menuWidth = 224;
    const menuHeight = 230;
    let x = e.clientX;
    let y = e.clientY;
    
    if (x + menuWidth > window.innerWidth - 12) {
        x = window.innerWidth - menuWidth - 12;
    }
    if (y + menuHeight > window.innerHeight - 12) {
        y = window.innerHeight - menuHeight - 12;
    }
    
    menu.style.left = `${Math.max(12, x)}px`;
    menu.style.top = `${Math.max(12, y)}px`;
    
    menu.querySelectorAll('.apple-size-btn').forEach(sBtn => {
        sBtn.addEventListener('click', (ev) => {
            ev.stopPropagation();
            const c = parseInt(sBtn.getAttribute('data-col'));
            const r = parseInt(sBtn.getAttribute('data-row'));
            closeActiveContextMenu();
            setStreamDeckButtonSize(btn.id, c, r);
        });
    });
    
    menu.querySelectorAll('.apple-menu-item').forEach(item => {
        item.addEventListener('click', (ev) => {
            ev.stopPropagation();
            const action = item.getAttribute('data-action');
            closeActiveContextMenu();
            if (action === 'edit') {
                openEditButtonModal(btn.id);
            } else if (action === 'duplicate') {
                duplicateStreamDeckButton(btn.id);
            } else if (action === 'test') {
                const el = document.querySelector(`.deck-btn[data-id="${btn.id}"]`);
                testButtonAction(btn.id, el);
            } else if (action === 'delete') {
                deleteStreamDeckButtonWithUndo(btn.id);
            }
        });
    });
}

function showAppleCanvasContextMenu(e) {
    e.preventDefault();
    e.stopPropagation();
    closeActiveContextMenu();
    
    const menu = document.createElement('div');
    menu.className = 'apple-context-menu';
    
    menu.innerHTML = `
        <div class="apple-menu-header">
            <span class="apple-menu-title">Stream Deck Tuvali</span>
        </div>
        <div class="apple-menu-item" data-action="add-custom">
            <span class="menu-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
                Yeni Özel Buton Ekle
            </span>
        </div>
        <div class="apple-menu-item" data-action="open-library">
            <span class="menu-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
                Bileşen Ekle
            </span>
        </div>
        <div class="apple-menu-item" data-action="open-templates">
            <span class="menu-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="9"/><rect x="14" y="3" width="7" height="5"/><rect x="14" y="12" width="7" height="9"/><rect x="3" y="16" width="7" height="5"/></svg>
                Hazır Şablonlar
            </span>
        </div>
        <div class="apple-menu-divider"></div>
        <div class="apple-menu-item" data-action="add-page">
            <span class="menu-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/></svg>
                Yeni Sayfa Ekle
            </span>
        </div>
    `;
    
    document.body.appendChild(menu);
    activeContextMenu = menu;
    
    menu.style.left = `${Math.min(window.innerWidth - 220, Math.max(12, e.clientX))}px`;
    menu.style.top = `${Math.min(window.innerHeight - 190, Math.max(12, e.clientY))}px`;
    
    menu.querySelectorAll('.apple-menu-item').forEach(item => {
        item.addEventListener('click', (ev) => {
            ev.stopPropagation();
            const action = item.getAttribute('data-action');
            closeActiveContextMenu();
            if (action === 'add-custom') {
                window.createAndEditCustomButton();
            } else if (action === 'open-library') {
                targetEmptyCell = null;
                window.openWidgetPickerModal();
            } else if (action === 'open-templates') {
                const templBtn = document.getElementById('btn-templates-modal');
                if (templBtn) templBtn.click();
            } else if (action === 'add-page') {
                const addPgBtn = document.getElementById('btn-add-deck-page');
                if (addPgBtn) addPgBtn.click();
            }
        });
    });
}

// Global click & contextmenu dismissal
window.addEventListener('click', (e) => {
    closeActiveContextMenu();
    // If clicking outside any deck button, clear selection
    if (!e.target.closest('.deck-btn, .apple-context-menu, .modal')) {
        if (selectedDeckButtonId !== null) {
            selectedDeckButtonId = null;
            document.querySelectorAll('.deck-btn.selected').forEach(el => el.classList.remove('selected'));
        }
    }
});

window.addEventListener('contextmenu', (e) => {
    if (!e.target.closest('.deck-btn, .deck-slot-empty, #deck-buttons-list, .deck-hardware-frame')) {
        closeActiveContextMenu();
    }
});

// Global Keyboard Shortcuts for Stream Deck
window.addEventListener('keydown', (e) => {
    // Ignore when typing inside form inputs, textareas, selects
    const activeEl = document.activeElement;
    if (activeEl && (activeEl.tagName === 'INPUT' || activeEl.tagName === 'TEXTAREA' || activeEl.tagName === 'SELECT' || activeEl.isContentEditable || (activeEl.closest && activeEl.closest('.modal')))) {
        return;
    }
    
    // Ctrl+Z Undo delete
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z' && !e.shiftKey) {
        if (lastDeletedButton) {
            e.preventDefault();
            undoDeleteStreamDeckButton();
            return;
        }
    }
    
    // Close context menu / deselect on Escape
    if (e.key === 'Escape') {
        closeActiveContextMenu();
        if (selectedDeckButtonId !== null) {
            selectedDeckButtonId = null;
            document.querySelectorAll('.deck-btn.selected').forEach(el => el.classList.remove('selected'));
        }
        return;
    }
    
    // Operations on selected button
    if (selectedDeckButtonId !== null) {
        if (e.key === 'Delete' || e.key === 'Backspace') {
            e.preventDefault();
            deleteStreamDeckButtonWithUndo(selectedDeckButtonId);
        } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'd') {
            e.preventDefault();
            duplicateStreamDeckButton(selectedDeckButtonId);
        } else if (e.key === 'Enter') {
            e.preventDefault();
            openEditButtonModal(selectedDeckButtonId);
        } else if (e.key === '1') {
            e.preventDefault();
            setStreamDeckButtonSize(selectedDeckButtonId, 1, 1);
        } else if (e.key === '2') {
            e.preventDefault();
            setStreamDeckButtonSize(selectedDeckButtonId, 2, 1);
        } else if (e.key === '3') {
            e.preventDefault();
            setStreamDeckButtonSize(selectedDeckButtonId, 1, 2);
        } else if (e.key === '4') {
            e.preventDefault();
            setStreamDeckButtonSize(selectedDeckButtonId, 2, 2);
        }
    }
});

// ========================================================
// MATERIAL YOU ICON SET — stroke-only SVG paths, zero emoji
// ========================================================
const MD_PATHS = {
    play: '<polygon points="7 4 20 12 7 20 7 4"/>',
    pause: '<rect x="7" y="5" width="3.4" height="14" rx="1.2"/><rect x="13.6" y="5" width="3.4" height="14" rx="1.2"/>',
    prev: '<polygon points="19 5 9 12 19 19 19 5"/><line x1="5.5" y1="5" x2="5.5" y2="19"/>',
    next: '<polygon points="5 5 15 12 5 19 5 5"/><line x1="18.5" y1="5" x2="18.5" y2="19"/>',
    music: '<path d="M9 18V6l10-2v12"/><circle cx="6.6" cy="18" r="2.2"/><circle cx="16.6" cy="15.8" r="2.2"/>',
    down: '<line x1="12" y1="5" x2="12" y2="19"/><polyline points="6 13 12 19 18 13"/>',
    up: '<line x1="12" y1="19" x2="12" y2="5"/><polyline points="6 11 12 5 18 11"/>',
    cpu: '<rect x="5" y="5" width="14" height="14" rx="2.5"/><rect x="9.5" y="9.5" width="5" height="5" rx="1"/><path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3"/>',
    ram: '<rect x="2.5" y="6" width="19" height="10" rx="2.5"/><path d="M7 18.5V16M12 18.5V16M17 18.5V16M7 10h10"/>',
    disk: '<ellipse cx="12" cy="6.5" rx="7.5" ry="3"/><path d="M4.5 6.5v11c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3v-11"/><path d="M4.5 11.8c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3"/>',
    gpu: '<rect x="2.5" y="6" width="19" height="12" rx="2.5"/><circle cx="9" cy="12" r="2.6"/><path d="M14.5 9.5h4M14.5 14.5h4M6.5 18v2.5"/>',
    thermo: '<path d="M14 14.8V5a2 2 0 1 0-4 0v9.8a4 4 0 1 0 4 0z"/><path d="M12 9v6"/>',
    net: '<path d="M5 12.5a10 10 0 0 1 14 0"/><path d="M8.5 16a5.6 5.6 0 0 1 7 0"/><path d="M12 19.5h.01"/>',
    volume: '<polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14"/>',
    app_session: '<line x1="6" y1="4" x2="6" y2="20"/><line x1="12" y1="4" x2="12" y2="20"/><line x1="18" y1="4" x2="18" y2="20"/><circle cx="6" cy="9" r="2"/><circle cx="12" cy="15" r="2"/><circle cx="18" cy="7" r="2"/>',
    brightness: '<circle cx="12" cy="12" r="4.5"/><path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M19.1 4.9l-1.8 1.8M6.7 17.3l-1.8 1.8"/>',
    check: '<polyline points="20 6 9 17 4 12"/>',
    square: '<rect x="3.5" y="3.5" width="17" height="17" rx="3"/>',
    right: '<polyline points="9 18 15 12 9 6"/>',
    wide: '<rect x="2.5" y="6.5" width="19" height="11" rx="3"/>',
    tall: '<rect x="6.5" y="2.5" width="11" height="19" rx="3"/>',
    grid: '<rect x="2.5" y="2.5" width="19" height="19" rx="3"/><line x1="2.5" y1="12" x2="21.5" y2="12"/><line x1="12" y1="2.5" x2="12" y2="21.5"/>'
};

function mdIcon(name, size = 16, strokeWidth = 1.75) {
    const d = MD_PATHS[name] || MD_PATHS.square;
    return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${strokeWidth}" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
}

// WMO weather codes -> pure stroke SVG icon (never an emoji)
function getWeatherIconSvg(code, size = 22) {
    const c = Number(code);
    const P = {
        sun: '<circle cx="12" cy="12" r="4.2"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M19.1 4.9L17 7M7 17l-2.1 2.1"/>',
        cloud: '<path d="M17.5 19a4.5 4.5 0 0 0 0-9 6 6 0 0 0-11.31-1.7A4.5 4.5 0 0 0 6.5 19z"/>',
        suncloud: '<circle cx="8" cy="8" r="3"/><path d="M8 2v2M2 8h2M4 4l1.4 1.4"/><path d="M17.5 20a4.5 4.5 0 0 0 0-9 6 6 0 0 0-11.31-1.7A4.5 4.5 0 0 0 6.5 20z"/>',
        fog: '<path d="M4 15h16M6 19h12M8 11h8"/><path d="M17.5 11a4.5 4.5 0 0 0 0-9 6 6 0 0 0-11.31-1.7A4.5 4.5 0 0 0 6.5 11"/>',
        rain: '<path d="M17.5 16a4.5 4.5 0 0 0 0-9 6 6 0 0 0-11.31-1.7A4.5 4.5 0 0 0 6.5 16z"/><path d="M8.5 19l-1 2.2M12 19l-1 2.2M15.5 19l-1 2.2"/>',
        snow: '<path d="M17.5 16a4.5 4.5 0 0 0 0-9 6 6 0 0 0-11.31-1.7A4.5 4.5 0 0 0 6.5 16z"/><path d="M8.5 19v2.4M12 19v2.4M15.5 19v2.4"/>',
        storm: '<path d="M17.5 15a4.5 4.5 0 0 0 0-9 6 6 0 0 0-11.31-1.7A4.5 4.5 0 0 0 6.5 15z"/><path d="M13 17l-2.4 4h3.4l-2 3.4"/>'
    };
    let d;
    if (c === 0) d = P.sun;
    else if (c <= 2) d = P.suncloud;
    else if (c === 3) d = P.cloud;
    else if (c === 45 || c === 48) d = P.fog;
    else if (c >= 71 && c <= 77) d = P.snow;
    else if (c === 85 || c === 86) d = P.snow;
    else if (c >= 95) d = P.storm;
    else if (c >= 51) d = P.rain;
    else d = P.suncloud;
    return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
}

// Editorial Monochrome Zinc: her widget tek bir vurgu kullanır. Eski alacalı
// ton paleti tamamen kaldırıldı — widget türüne göre renk ayrımı yok.
function cardTone() {
    return 'var(--accent-solid)';
}

// "Çar, 28 Eki" — the Pixel Hub clock date line
function formatDeckDate(d) {
    const wd = d.toLocaleDateString('tr-TR', { weekday: 'short' }).replace(/\.$/, '');
    const mon = d.toLocaleDateString('tr-TR', { month: 'short' }).replace(/\.$/, '');
    return `${wd}, ${d.getDate()} ${mon}`;
}

// Packs spans into an explicit rows x cols occupancy map so a card can NEVER
// create an implicit column/row outside the matrix (zero overflow guarantee).
// PASS 1 honours an explicit (grid_row, grid_col); PASS 2 auto-packs the rest.
function packBentoGrid(pageButtons, rows, cols) {
    const occupied = [];
    for (let r = 0; r < rows; r++) occupied.push(new Array(cols).fill(false));

    const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v));
    const fits = (row, col, rs, cs) => {
        if (row < 0 || col < 0 || row + rs > rows || col + cs > cols) return false;
        for (let r = row; r < row + rs; r++) {
            for (let c = col; c < col + cs; c++) {
                if (occupied[r][c]) return false;
            }
        }
        return true;
    };
    const mark = (row, col, rs, cs) => {
        for (let r = row; r < row + rs; r++) {
            for (let c = col; c < col + cs; c++) occupied[r][c] = true;
        }
    };

    const placed = new Map();
    let overflowCount = 0;

    // PASS 1 — an explicit cell wins, exactly where the user dropped it
    pageButtons.forEach(btn => {
        const gr = Number(btn.grid_row);
        const gc = Number(btn.grid_col);
        if (!Number.isInteger(gr) || !Number.isInteger(gc) || gr < 0 || gc < 0) return;
        const cs = clamp(btn.col_span || 1, 1, cols);
        const rs = clamp(btn.row_span || 1, 1, rows);
        const r = clamp(gr, 0, rows - rs);
        const c = clamp(gc, 0, cols - cs);
        if (!fits(r, c, rs, cs)) return;   // occupied -> falls through to auto-pack
        mark(r, c, rs, cs);
        placed.set(btn.id, { r, c, rs, cs });
    });

    // PASS 2 — first-fit with the shrink-then-place ladder
    pageButtons.forEach(btn => {
        if (placed.has(btn.id)) return;
        let cs = clamp(btn.col_span || 1, 1, cols);
        let rs = clamp(btn.row_span || 1, 1, rows);
        let pos = null;

        while (cs >= 1 && rs >= 1) {
            for (let r = 0; r < rows && !pos; r++) {
                for (let c = 0; c < cols && !pos; c++) {
                    if (fits(r, c, rs, cs)) pos = { r, c, rs, cs };
                }
            }
            if (pos) break;
            if (cs > 1 && cs >= rs) cs--;
            else if (rs > 1) rs--;
            else break;
        }

        if (!pos) {
            overflowCount++;
            return;
        }
        mark(pos.r, pos.c, pos.rs, pos.cs);
        placed.set(btn.id, pos);
    });

    const holes = [];
    for (let r = 0; r < rows; r++) {
        for (let c = 0; c < cols; c++) {
            if (!occupied[r][c]) holes.push({ r, c });
        }
    }

    return { placed, holes, overflowCount, occupied };
}

// Render Stream Deck Interactive Grid with Drag & Drop, Edge Resizing, and Ghost Slots
function renderStreamDeckButtons() {
    const list = document.getElementById('deck-buttons-list');
    if (!list || !apiConfig) return;
    list.innerHTML = '';

    const cols = Math.max(1, apiConfig.stream_deck_cols || 4);
    const rows = Math.max(1, apiConfig.stream_deck_rows || 2);
    list.style.setProperty('--cols', cols);
    list.style.setProperty('--rows', rows);
    fitDeckFrame();

    const activePage = apiConfig.active_page || 0;
    // Filter buttons belonging to active page
    const pageButtons = (apiConfig.stream_deck_buttons || []).filter(b => (b.page || 0) === activePage);

    const layout = packBentoGrid(pageButtons, rows, cols);
    const placement = layout.placed;

    pageButtons.forEach((btn, index) => {
        const place = placement.get(btn.id);
        if (!place) return;

        const item = document.createElement('div');
        item.className = 'deck-btn';
        if (selectedDeckButtonId === btn.id) {
            item.classList.add('selected');
        }
        item.draggable = true;
        item.setAttribute('data-id', btn.id);
        item.setAttribute('data-index', index);

        // Clamped spans placed on explicit grid cells (never exceeds the matrix)
        const colSpan = place.cs;
        const rowSpan = place.rs;
        item.style.gridColumn = `${place.c + 1} / span ${colSpan}`;
        item.style.gridRow = `${place.r + 1} / span ${rowSpan}`;

        // Custom background style (transparent / glass / outline)
        const bgStyle = btn.bg_style || 'default';
        if (bgStyle !== 'default') {
            item.classList.add(`bg-${bgStyle}`);
        }

        // Custom font style
        const fontFamily = btn.font_family || 'mono';
        item.classList.add(`font-${fontFamily}`);

        // Material You tonal accent: drives the icon badge, value and bar colours
        const tone = cardTone(btn);
        item.style.setProperty('--tone', tone);

        // Icon handling
        let displayIcon = getSvgIcon(btn.icon);
        if (btn.icon && btn.icon.startsWith("custom:") && btn.icon.length > 7) {
            const path = btn.icon.substring(7);
            const password = getAuthPassword();
            const authQuery = password ? `?password=${encodeURIComponent(password)}` : '';
            displayIcon = `<img src="${path}${authQuery}" alt="" />`;
        }

        // Custom body preview based on widget type
        let bodyContent = '';
        if (btn.type === 'clock_widget') {
            const clockStyle = btn.clock_style || (colSpan >= 2 ? 'apple_lockscreen' : 'apple_stacked');
            const sizeClass = btn.clock_font_size ? `clock-size-${btn.clock_font_size}` : '';
            const now = new Date();
            const hours = now.getHours().toString().padStart(2, '0');
            const minutes = now.getMinutes().toString().padStart(2, '0');
            const dow = formatDeckDate(now);
            if (clockStyle === 'flip_clock') {
                bodyContent = `
                    <div class="deck-flip-widget md-clock ${sizeClass}">
                        <div class="deck-flip-pair">
                            <div class="deck-flip-card"><div class="deck-flip-num">${hours}</div></div>
                            <div class="deck-flip-sep">:</div>
                            <div class="deck-flip-card"><div class="deck-flip-num">${minutes}</div></div>
                        </div>
                        <div class="deck-flip-label">${dow}</div>
                    </div>
                `;
            } else if (clockStyle === 'analog') {
                const svgSize = btn.clock_font_size === 'huge' ? 92 : btn.clock_font_size === 'large' ? 84 : btn.clock_font_size === 'compact' ? 62 : (colSpan >= 2 ? 80 : 72);
                const svgClock = getAnalogClockSvg(now.getHours(), now.getMinutes(), now.getSeconds(), svgSize);
                bodyContent = `
                    <div class="deck-clock-widget analog-mode md-clock ${sizeClass}">
                        <div class="deck-analog-wrapper">${svgClock}</div>
                    </div>
                `;
            } else if (clockStyle === 'world_clock') {
                let nyTime = '--:--';
                try {
                    nyTime = new Date(now.toLocaleString('en-US', { timeZone: 'America/New_York' })).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit' });
                } catch(e) { nyTime = `${hours}:${minutes}`; }
                bodyContent = `
                    <div class="md-clock md-clock--split ${sizeClass}">
                        <span class="md-clock__dow">${dow}</span>
                        <div class="deck-world-clock">
                            <div class="deck-world-cell">
                                <span class="deck-world-city">İST</span>
                                <span class="deck-world-time deck-world-local">${hours}:${minutes}</span>
                            </div>
                            <div class="deck-world-divider"></div>
                            <div class="deck-world-cell">
                                <span class="deck-world-city">NYC</span>
                                <span class="deck-world-time deck-world-ny">${nyTime}</span>
                            </div>
                        </div>
                        <span class="md-clock__meta">Dünya saati · İstanbul / New York</span>
                    </div>
                `;
            } else if (clockStyle === 'digital_seconds') {
                const secondsStr = now.getSeconds().toString().padStart(2, '0');
                bodyContent = `
                    <div class="md-clock deck-apple-lockscreen-clock ${sizeClass}">
                        <span class="md-clock__dow md-clock-date-el">${dow}</span>
                        <div class="md-clock__time"><span class="deck-apple-clock-time with-seconds">${hours}:${minutes}:${secondsStr}</span></div>
                        <span class="md-clock__meta">İstanbul · saniyeli</span>
                    </div>
                `;
            } else if (clockStyle === 'apple_stacked') {
                bodyContent = `
                    <div class="md-clock md-clock--stacked deck-apple-stacked-clock ${sizeClass}">
                        <span class="md-clock__dow md-clock-date-el">${dow}</span>
                        <div class="md-clock__time md-clock__time--stacked">
                            <span class="deck-apple-stacked-h">${hours}</span>
                            <span class="deck-apple-stacked-m">${minutes}</span>
                        </div>
                        <span class="md-clock__meta">İstanbul · yerel saat</span>
                    </div>
                `;
            } else {
                // apple_lockscreen / digital_clean — the Pixel Hub reference clock
                bodyContent = `
                    <div class="md-clock deck-apple-lockscreen-clock ${colSpan >= 2 ? 'md-clock--wide' : ''} ${sizeClass}">
                        <span class="md-clock__dow md-clock-date-el">${dow}</span>
                        <div class="md-clock__time deck-apple-clock-time">
                            <span>${hours}</span><span class="deck-apple-colon">:</span><span>${minutes}</span>
                        </div>
                        <span class="md-clock__meta">İstanbul · yerel saat</span>
                    </div>
                `;
            }
        } else if (btn.type === 'volume_slider') {
            const isCompact = colSpan === 1;
            const safeLabel = escapeHtml(btn.label || 'Ses');
            if (isCompact) {
                bodyContent = `
                    <div class="apple-pill-slider-container md-pill md-pill--compact">
                        <div class="md-pill__top-row">
                            <span class="md-pill__badge">${mdIcon('volume', 14)}</span>
                            <span class="apple-pill-value md-pill__value">%75</span>
                        </div>
                        <span class="apple-pill-title md-pill__label" title="${safeLabel}">${safeLabel}</span>
                        <div class="apple-pill-track md-pill__track" onclick="handleSliderTrackClick(event, 'volume');">
                            <div class="apple-pill-fill accent-volume md-pill__bar" style="width: 75%;"></div>
                        </div>
                    </div>
                `;
            } else {
                bodyContent = `
                    <div class="apple-pill-slider-container md-pill">
                        <div class="md-pill__row">
                            <span class="md-pill__badge">${mdIcon('volume', 14)}</span>
                            <span class="apple-pill-title md-pill__label" title="${safeLabel}">${safeLabel}</span>
                            <span class="apple-pill-value md-pill__value">%75</span>
                        </div>
                        <div class="apple-pill-track md-pill__track" onclick="handleSliderTrackClick(event, 'volume');">
                            <div class="apple-pill-fill accent-volume md-pill__bar" style="width: 75%;"></div>
                        </div>
                    </div>
                `;
            }
        } else if (btn.type === 'app_volume_slider') {
            const appKey = (btn.value || '').trim().toLowerCase();
            const appSession = appKey ? audioSessionsCache[appKey] : null;
            const appPct = appSession ? Math.round(appSession.volume) : 0;
            const appLabel = btn.label || (appSession && appSession.title) || (appKey ? prettyAppLabel(appKey) : 'Uygulama Sesi');
            const safeAppLabel = escapeHtml(appLabel);
            const isCompact = colSpan === 1;
            if (isCompact) {
                bodyContent = `
                    <div class="apple-pill-slider-container md-pill md-pill--compact app-session-pill">
                        <div class="md-pill__top-row">
                            <span class="md-pill__badge">${mdIcon('app_session', 14)}</span>
                            <span class="apple-pill-value md-pill__value">${appPct}%</span>
                        </div>
                        <span class="apple-pill-title md-pill__label" title="${safeAppLabel}">${safeAppLabel}</span>
                        <div class="apple-pill-track md-pill__track" data-app-key="${escapeHtml(appKey)}" draggable="false" onpointerdown="startAppVolumeDrag(event)">
                            <div class="apple-pill-fill accent-app md-pill__bar" style="width: ${appPct}%;"></div>
                        </div>
                    </div>
                `;
            } else {
                bodyContent = `
                    <div class="apple-pill-slider-container md-pill app-session-pill">
                        <div class="md-pill__row">
                            <span class="md-pill__badge">${mdIcon('app_session', 14)}</span>
                            <span class="apple-pill-title md-pill__label" title="${safeAppLabel}">${safeAppLabel}</span>
                            <span class="apple-pill-value md-pill__value">${appPct}%</span>
                        </div>
                        <div class="apple-pill-track md-pill__track" data-app-key="${escapeHtml(appKey)}" draggable="false" onpointerdown="startAppVolumeDrag(event)">
                            <div class="apple-pill-fill accent-app md-pill__bar" style="width: ${appPct}%;"></div>
                        </div>
                    </div>
                `;
            }
            if (appKey && !appSession) fetchAudioSessions();
            ensureAppVolumeRefreshLoop();
        } else if (btn.type === 'brightness_slider') {
            const isCompact = colSpan === 1;
            const safeLabel = escapeHtml(btn.label || 'Parlaklık');
            if (isCompact) {
                bodyContent = `
                    <div class="apple-pill-slider-container md-pill md-pill--compact">
                        <div class="md-pill__top-row">
                            <span class="md-pill__badge">${mdIcon('brightness', 14)}</span>
                            <span class="apple-pill-value md-pill__value">%85</span>
                        </div>
                        <span class="apple-pill-title md-pill__label" title="${safeLabel}">${safeLabel}</span>
                        <div class="apple-pill-track md-pill__track" onclick="handleSliderTrackClick(event, 'brightness');">
                            <div class="apple-pill-fill accent-brightness md-pill__bar" style="width: 85%;"></div>
                        </div>
                    </div>
                `;
            } else {
                bodyContent = `
                    <div class="apple-pill-slider-container md-pill">
                        <div class="md-pill__row">
                            <span class="md-pill__badge">${mdIcon('brightness', 14)}</span>
                            <span class="apple-pill-title md-pill__label" title="${safeLabel}">${safeLabel}</span>
                            <span class="apple-pill-value md-pill__value">%85</span>
                        </div>
                        <div class="apple-pill-track md-pill__track" onclick="handleSliderTrackClick(event, 'brightness');">
                            <div class="apple-pill-fill accent-brightness md-pill__bar" style="width: 85%;"></div>
                        </div>
                    </div>
                `;
            }
        } else if (btn.type === 'live_info') {
            const isNet = btn.value === 'net_speed';
            const isTemp = btn.value === 'cpu_temp';
            const isDisk = btn.value === 'disk';
            const isGpu = btn.value === 'gpu';
            const isCombo = !isNet && !isTemp && !isDisk && !isGpu && (colSpan >= 2 || btn.value === 'combo' || btn.value === 'system');
            // Load severity maps onto the tonal palette (Sage -> Sand -> Rose), never neon
            const loadTone = (pct) => pct >= 90 ? 'var(--status-red-txt)' : 'var(--accent-solid)';
            if (isNet) {
                bodyContent = `
                    <div class="deck-metric md-metric--net">
                        <div class="md-metric__head">
                            <span class="md-metric__label">Ağ Hızı</span>
                            <span class="md-metric__glyph">${mdIcon('net', 14)}</span>
                        </div>
                        <div class="md-net-rows">
                            <div class="md-net-row">
                                <span class="md-net-chip md-net-chip--down">${mdIcon('down', 12)}</span>
                                <span class="md-net-label">İndirme</span>
                                <span class="md-net-val deck-net-down-val">${formatNetSpeed(cachedLiveStats.net_down_kbps)}</span>
                            </div>
                            <div class="md-net-row">
                                <span class="md-net-chip md-net-chip--up">${mdIcon('up', 12)}</span>
                                <span class="md-net-label">Yükleme</span>
                                <span class="md-net-val deck-net-up-val">${formatNetSpeed(cachedLiveStats.net_up_kbps)}</span>
                            </div>
                        </div>
                        <div class="md-metric__spark">${buildSparklineSvg(`${btn.id}-net`, 'net_down', 'net_up')}</div>
                    </div>
                `;
            } else if (isTemp) {
                const t = Number(cachedLiveStats.cpu_temp) || 0;
                const col = t <= 0 ? 'var(--text-muted)' : t >= 80 ? 'var(--status-red-txt)' : 'var(--accent-solid)';
                const pct = t > 0 ? clampPct(((t - 30) / (95 - 30)) * 100) : 0;
                bodyContent = `
                    <div class="deck-metric">
                        <div class="md-metric__head">
                            <span class="md-metric__label">${escapeHtml(btn.label || 'İşlemci Sıcaklığı')}</span>
                            <span class="md-metric__glyph">${mdIcon('thermo', 14)}</span>
                        </div>
                        <div class="md-metric__value-row">
                            <span class="md-metric__value deck-metric-value" style="color:${col};">${formatCpuTemp(t)}</span>
                            <span class="md-metric__sub deck-metric-sub">${thermalLabel(t)}</span>
                        </div>
                        <div class="md-metric__bar"><span class="md-metric__fill deck-metric-fill" style="width:${pct}%; background:${col};"></span></div>
                    </div>
                `;
            } else if (isDisk) {
                const pct = clampPct(cachedLiveStats.disk_percent);
                const total = Number(cachedLiveStats.disk_total_gb) || 0;
                const free = Number(cachedLiveStats.disk_free_gb) || 0;
                const col = loadTone(pct);
                bodyContent = `
                    <div class="deck-metric">
                        <div class="md-metric__head">
                            <span class="md-metric__label">${escapeHtml(btn.label || 'Disk C:')}</span>
                            <span class="md-metric__glyph">${mdIcon('disk', 14)}</span>
                        </div>
                        <div class="md-metric__value-row">
                            <span class="md-metric__value deck-metric-value">${total > 0 ? `${free.toFixed(0)} GB Boş` : 'Okunuyor'}</span>
                            <span class="md-metric__sub deck-metric-sub">${total > 0 ? `%${pct} dolu · ${total.toFixed(0)} GB` : 'C: sürücü'}</span>
                        </div>
                        <div class="md-metric__bar"><span class="md-metric__fill deck-metric-fill" style="width:${pct}%; background:${col};"></span></div>
                    </div>
                `;
            } else if (isGpu) {
                const pct = clampPct(cachedLiveStats.gpu);
                const t = Number(cachedLiveStats.gpu_temp) || 0;
                const col = loadTone(pct);
                bodyContent = `
                    <div class="deck-metric">
                        <div class="md-metric__head">
                            <span class="md-metric__label">${escapeHtml(btn.label || 'Ekran Kartı')}</span>
                            <span class="md-metric__glyph">${mdIcon('gpu', 14)}</span>
                        </div>
                        <div class="md-metric__value-row">
                            <span class="md-metric__value deck-metric-value">%${pct}</span>
                            <span class="md-metric__sub deck-metric-sub">${t > 0 ? `${t}°C` : 'GPU yükü'}</span>
                        </div>
                        <div class="md-metric__bar"><span class="md-metric__fill deck-metric-fill" style="width:${pct}%; background:${col};"></span></div>
                    </div>
                `;
            } else if (isCombo) {
                // 2x1 mode: three minimal rings (İşlemci / Bellek / Sıcaklık)
                const cpuTone = loadTone(cachedLiveStats.cpu);
                const ramTone = loadTone(cachedLiveStats.ram);
                const t = Number(cachedLiveStats.cpu_temp) || 0;
                const tempPct = t > 0 ? clampPct(((t - 30) / (95 - 30)) * 100) : 0;
                const tempTone = t <= 0 ? 'var(--text-muted)' : t >= 80 ? 'var(--status-red-txt)' : 'var(--accent-solid)';
                bodyContent = `
                    <div class="md-rings">
                        <div class="md-ring">
                            <div class="md-ring__svg md-ring-cpu-svg" style="color:${cpuTone};">${getTelemetryArcSvg(cachedLiveStats.cpu, 'currentColor', 46, 4)}</div>
                            <span class="md-ring__val md-ring-cpu-val" style="color:${cpuTone};">%${cachedLiveStats.cpu}</span>
                            <span class="md-ring__label">İşlemci</span>
                        </div>
                        <div class="md-ring">
                            <div class="md-ring__svg md-ring-ram-svg" style="color:${ramTone};">${getTelemetryArcSvg(cachedLiveStats.ram, 'currentColor', 46, 4)}</div>
                            <span class="md-ring__val md-ring-ram-val" style="color:${ramTone};">%${cachedLiveStats.ram}</span>
                            <span class="md-ring__label">Bellek</span>
                        </div>
                        <div class="md-ring">
                            <div class="md-ring__svg md-ring-temp-svg" style="color:${tempTone};">${getTelemetryArcSvg(tempPct, 'currentColor', 46, 4)}</div>
                            <span class="md-ring__val md-ring-temp-val" style="color:${tempTone};">${t > 0 ? `${t}°C` : '—'}</span>
                            <span class="md-ring__label">Sıcaklık</span>
                        </div>
                    </div>
                `;
            } else {
                const isCpu = (btn.value || '').includes('cpu');
                const pct = isCpu ? cachedLiveStats.cpu : cachedLiveStats.ram;
                const col = loadTone(pct);
                const title = btn.label || (isCpu ? 'İşlemci' : 'Bellek');
                const extra = isCpu
                    ? `${formatCpuTemp(cachedLiveStats.cpu_temp)} ısı`
                    : `${cachedLiveStats.ram_used_gb}/${cachedLiveStats.ram_total_gb} GB`;
                bodyContent = `
                    <div class="deck-metric">
                        <div class="md-metric__head">
                            <span class="md-metric__label">${escapeHtml(title)}</span>
                            <span class="md-metric__glyph">${mdIcon(isCpu ? 'cpu' : 'ram', 14)}</span>
                        </div>
                        <div class="md-metric__value-row">
                            <span class="md-metric__value deck-metric-value">%${pct}</span>
                            <span class="md-metric__sub deck-metric-sub">${extra}</span>
                        </div>
                        <div class="md-metric__spark">${buildSparklineSvg(btn.id, isCpu ? 'cpu' : 'ram')}</div>
                        <div class="md-metric__bar"><span class="md-metric__fill deck-metric-fill" style="width:${pct}%; background:${col};"></span></div>
                    </div>
                `;
            }
        } else if (btn.type === 'weather') {
            const city = btn.value || 'Istanbul';
            const w = getWeatherData(city);
            const tempTxt = w ? `${w.temp}°` : '—°';
            const descTxt = w ? (w.desc || '—') : 'Yükleniyor…';
            const windTxt = w && w.wind ? `${w.wind} km/s` : '';
            bodyContent = `
                <div class="md-weather">
                    <div class="md-weather__head">
                        <span class="md-weather__city deck-weather-city">${escapeHtml(w ? (w.city || city) : city)}</span>
                        <span class="md-weather__icon deck-weather-icon">${getWeatherIconSvg(w ? w.code : 1, 22)}</span>
                    </div>
                    <div class="md-weather__main">
                        <span class="md-weather__temp deck-weather-temp">${tempTxt}</span>
                        <span class="md-weather__desc deck-weather-desc">${escapeHtml(descTxt)}${windTxt ? ' · ' + windTxt : ''}</span>
                    </div>
                </div>
            `;
        } else if (btn.type === 'now_playing' || btn.type === 'media_control' || btn.type === 'media_widget') {
            const rich = btn.type === 'now_playing' || btn.type === 'media_control' || colSpan >= 2;
            const title = cachedLiveStats.media_title || (btn.label || 'Şimdi Çalıyor');
            const artist = cachedLiveStats.media_artist || 'Medya bekleniyor';
            const playIcon = cachedLiveStats.media_playing ? mdIcon('pause', 14) : mdIcon('play', 14);
            bodyContent = `
                <div class="deck-media ${rich ? 'is-rich' : 'is-compact'}">
                    ${rich ? `
                    <div class="deck-media__head">
                        <span class="deck-media__badge">${mdIcon('music', 14)}</span>
                        <span class="deck-media__label">Şimdi Çalıyor</span>
                    </div>
                    <div class="deck-media__meta">
                        <span class="deck-media__title deck-np-title">${escapeHtml(title)}</span>
                        <span class="deck-media__artist deck-np-artist">${escapeHtml(artist)}</span>
                    </div>` : `
                    <div class="deck-media__meta">
                        <span class="deck-media__title deck-np-title">${escapeHtml(btn.label || title)}</span>
                    </div>`}
                    <div class="deck-media__controls">
                        <button type="button" class="md-media-btn" data-tooltip="Önceki parça" aria-label="Önceki parça" onclick="event.stopPropagation(); triggerMediaAction('prev');">${mdIcon('prev', 14)}</button>
                        <button type="button" class="md-media-btn md-media-btn--play" data-tooltip="Oynat / Duraklat" aria-label="Oynat veya duraklat" onclick="event.stopPropagation(); triggerMediaAction('playpause');"><span class="deck-np-play-glyph">${playIcon}</span></button>
                        <button type="button" class="md-media-btn" data-tooltip="Sonraki parça" aria-label="Sonraki parça" onclick="event.stopPropagation(); triggerMediaAction('next');">${mdIcon('next', 14)}</button>
                    </div>
                </div>
            `;
        } else if (btn.type === 'toggle' && btn.value === 'pomodoro') {
            const toggleIcon = pomodoroState.running ? mdIcon('pause', 14) : mdIcon('play', 14);
            bodyContent = `
                <div class="deck-pomodoro-widget ${pomodoroState.running ? 'running' : ''}">
                    <div class="md-timer">
                        <span class="deck-pomodoro-time md-timer__val">${formatPomodoro(currentPomodoroSeconds())}</span>
                        <span class="deck-pomodoro-status md-timer__unit">${pomodoroPhaseLabel()}</span>
                    </div>
                    <button type="button" class="md-media-btn md-media-btn--play" title="Başlat / Duraklat" aria-label="Başlat veya duraklat" onclick="event.stopPropagation(); triggerPomodoroWidget(${btn.id});">
                        <span class="deck-pomodoro-toggle-glyph">${toggleIcon}</span>
                    </button>
                </div>
            `;
        } else {
            // Template 1 — Action / Toggle keycap: tonal badge top-left, text bottom-left
            const safeLabel = escapeHtml(btn.label || 'İsimsiz Buton');
            const isOn = btn.type === 'toggle' && !!btn.state;
            let subText = 'Kısayol';
            if (btn.type === 'toggle') subText = 'Anahtar';
            else if (btn.type === 'command') subText = 'Uygulama';
            else if (btn.type === 'url') subText = 'Web Bağlantısı';
            else if (btn.type === 'snippet') subText = 'Metin Parçası';
            bodyContent = `
                <div class="md-tile ${isOn ? 'is-on' : ''}">
                    <div class="md-tile__top">
                        <span class="md-tile__badge">${displayIcon}</span>
                        ${btn.type === 'toggle'
                            ? `<span class="md-tile__state">${isOn ? 'Açık' : 'Kapalı'}</span>`
                            : `<span class="md-tile__chev">${mdIcon('right', 13)}</span>`}
                    </div>
                    <div class="md-tile__text">
                        <span class="md-tile__title" title="${safeLabel}">${safeLabel}</span>
                        <span class="md-tile__sub">${subText}</span>
                    </div>
                </div>
            `;
        }

        item.innerHTML = `
            ${bodyContent}
            <!-- Kart üstü hızlı kontroller: boyut · düzenle · sil -->
            <div class="deck-quick-actions">
                <button type="button" class="deck-quick-btn deck-quick-size" data-tooltip="Boyut: 1×1 → 2×1 → 1×2 → 2×2" aria-label="Kart boyutunu değiştir">${colSpan}×${rowSpan}</button>
                <button type="button" class="deck-quick-btn deck-quick-edit" data-tooltip="Kartı düzenle" aria-label="Kartı düzenle">
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z"/></svg>
                </button>
                <button type="button" class="deck-quick-btn deck-quick-del" data-tooltip="Kartı sil (Delete)" aria-label="Kartı sil">
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
                </button>
            </div>
            <!-- Interactive Edge Drag Resizers -->
            <div class="deck-resize-edge deck-resize-e" title="Genişletmek için sağa çekin"></div>
            <div class="deck-resize-edge deck-resize-s" title="Yükseltmek için aşağı çekin"></div>
            <div class="deck-resize-edge deck-resize-se" title="Boyutu ayarlamak için çekin veya tıklayın">
                <svg width="7" height="7" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 3 21 3 21 9"/><polyline points="9 21 3 21 3 15"/><line x1="21" y1="3" x2="14" y2="10"/><line x1="3" y1="21" x2="10" y2="14"/></svg>
            </div>
        `;

        // Kart üstü hızlı kontroller: boyut döngüsü · düzenle · sil
        const quickBar = item.querySelector('.deck-quick-actions');
        if (quickBar) {
            quickBar.querySelector('.deck-quick-size').addEventListener('click', (e) => {
                e.stopPropagation();
                cycleStreamDeckButtonSize(btn.id);
            });
            quickBar.querySelector('.deck-quick-edit').addEventListener('click', (e) => {
                e.stopPropagation();
                openEditButtonModal(btn.id);
            });
            quickBar.querySelector('.deck-quick-del').addEventListener('click', (e) => {
                e.stopPropagation();
                deleteStreamDeckButtonWithUndo(btn.id);
            });
        }

        // Right-Click Native-Feel Apple Context Menu
        item.addEventListener('contextmenu', (e) => {
            showAppleContextMenu(e, btn);
        });

        // Setup edge-dragging to resize
        setupEdgeResize(item, btn);

        // Disambiguate click vs drag movement
        let downX = 0, downY = 0, movedSignificantly = false;
        item.addEventListener('mousedown', (e) => {
            downX = e.clientX;
            downY = e.clientY;
            movedSignificantly = false;
        });
        item.addEventListener('mousemove', (e) => {
            if (Math.hypot(e.clientX - downX, e.clientY - downY) > 5) {
                movedSignificantly = true;
            }
        });

        // Single click selects the card and fills the Live Inspector (no modal)
        item.addEventListener('click', (e) => {
            if (movedSignificantly) return;
            if (e.target.closest('.deck-np-btn, .deck-pomodoro-btn, .apple-pill-track, .deck-quick-actions, .deck-resize-edge')) return;
            if (document.body.classList.contains('mobile-mode')) {
                if (btn.type === 'toggle' && btn.value === 'pomodoro') {
                    triggerPomodoroWidget(btn.id);
                    return;
                }
                testButtonAction(btn.id, item);
                return;
            }

            selectDeckButton(btn.id);
        });

        item.addEventListener('dblclick', (e) => {
            if (e.target.closest('.deck-np-btn, .deck-pomodoro-btn, .apple-pill-track, .deck-quick-actions, .deck-resize-edge')) return;
            if (document.body.classList.contains('mobile-mode')) return;
            // Advanced modal stays available for icon / installed-app extraction
            openEditButtonModal(btn.id);
        });

        // Drag & Drop event handlers
        item.addEventListener('dragstart', (e) => {
            item.classList.add('dragging');
            e.dataTransfer.setData('text/plain', btn.id.toString());
            e.dataTransfer.effectAllowed = 'move';
        });

        item.addEventListener('dragend', () => {
            item.classList.remove('dragging');
            document.querySelectorAll('.deck-btn, .deck-slot-empty').forEach(el => el.classList.remove('drag-over'));
        });

        item.addEventListener('dragover', (e) => {
            e.preventDefault();
            e.dataTransfer.dropEffect = 'move';
            item.classList.add('drag-over');
        });

        item.addEventListener('dragleave', () => {
            item.classList.remove('drag-over');
        });

        item.addEventListener('drop', (e) => {
            e.preventDefault();
            item.classList.remove('drag-over');
            const draggedId = parseInt(e.dataTransfer.getData('text/plain'));
            if (!isNaN(draggedId) && draggedId !== btn.id) {
                const cell = cellFromEvent(e, list, rows, cols);
                moveButtonToCell(draggedId, cell.row, cell.col);
            }
        });

        list.appendChild(item);
    });

    // Ghost drop slots: exactly the cells the Bento packer left free (no holes, no overflow)
    layout.holes.forEach((hole) => {
        const emptySlot = document.createElement('div');
        emptySlot.className = 'deck-slot-empty';
        emptySlot.style.gridColumn = `${hole.c + 1} / span 1`;
        emptySlot.style.gridRow = `${hole.r + 1} / span 1`;
        emptySlot.innerHTML = `
            <div class="deck-slot-empty-icon">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
            </div>
            <div class="deck-slot-empty-label">Bileşen ekle</div>
        `;

        // Boş yuvaya tıklamak o hücreyi hatırlar: seçilen bileşen tam oraya eklenir.
        emptySlot.addEventListener('click', () => {
            targetEmptyCell = { row: hole.r, col: hole.c };
            window.openWidgetPickerModal();
        });

        emptySlot.addEventListener('contextmenu', (e) => {
            showAppleCanvasContextMenu(e);
        });

        emptySlot.addEventListener('dragover', (e) => {
            e.preventDefault();
            e.dataTransfer.dropEffect = 'move';
            emptySlot.classList.add('drag-over');
        });

        emptySlot.addEventListener('dragleave', () => {
            emptySlot.classList.remove('drag-over');
        });

        emptySlot.addEventListener('drop', (e) => {
            e.preventDefault();
            emptySlot.classList.remove('drag-over');
            // A card dragged onto a hole moves into it
            const draggedId = parseInt(e.dataTransfer.getData('text/plain'));
            if (!isNaN(draggedId)) moveButtonToCell(draggedId, hole.r, hole.c);
        });

        list.appendChild(emptySlot);
    });

    // Weather cards lazily fetch their own city on first paint
    pageButtons.forEach(btn => {
        if (btn.type !== 'weather') return;
        const city = btn.value || 'Istanbul';
        if (getWeatherData(city)) return;
        const key = normalizeCityKey(city);
        if (weatherInFlight.has(key)) return;
        weatherInFlight.add(key);
        fetchWeatherForCity(city).finally(() => weatherInFlight.delete(key));
    });
}

// Selection is purely visual feedback: every edit happens on the card itself
// (size / edit / delete pills) or in the sade kart penceresi.
function selectDeckButton(id) {
    selectedDeckButtonId = id;
    document.querySelectorAll('.deck-btn').forEach(el => {
        el.classList.toggle('selected', parseInt(el.getAttribute('data-id'), 10) === id);
    });
}

function setMasterVolumeByPercent(pct) {
    if (wsClient && wsClient.readyState === WebSocket.OPEN) {
        wsClient.send(JSON.stringify({
            type: 'set_volume',
            action: 'set',
            level: pct
        }));
    }
}

function setDisplayBrightness(pct) {
    if (wsClient && wsClient.readyState === WebSocket.OPEN) {
        wsClient.send(JSON.stringify({
            type: 'set_brightness',
            level: pct
        }));
    }
}

function handleSliderTrackClick(e, type) {
    e.stopPropagation();
    const track = e.currentTarget;
    const rect = track.getBoundingClientRect();
    const clickX = e.clientX - rect.left;
    const pct = Math.max(0, Math.min(100, Math.round((clickX / rect.width) * 100)));
    const fill = track.querySelector('.apple-pill-fill');
    const container = track.closest('.apple-pill-slider-container');
    const valEl = container ? container.querySelector('.apple-pill-value') : null;
    if (fill) fill.style.width = `${pct}%`;
    if (valEl) valEl.textContent = `${pct}%`;

    if (type === 'volume') {
        setMasterVolumeByPercent(pct);
    } else if (type === 'brightness') {
        setDisplayBrightness(pct);
    }
}

// ----------------- Per-App Audio (Core Audio / WASAPI) -----------------
// Live per-application volume channels reported by GET /api/audio_sessions and
// driven over the WebSocket with {"type":"set_app_volume"}.
let audioSessionsCache = {};
let audioSessionsFetchPromise = null;
let appVolumeDragTrack = null;

function prettyAppLabel(exe) {
    const base = String(exe || '').replace(/\.exe$/i, '');
    if (!base) return 'Uygulama Sesi';
    return base.charAt(0).toUpperCase() + base.slice(1);
}

function fetchAudioSessions() {
    if (audioSessionsFetchPromise) return audioSessionsFetchPromise;
    const request = authFetch('/api/audio_sessions')
        .then(res => res.json())
        .then(data => {
            const next = {};
            if (data && data.success && Array.isArray(data.sessions)) {
                data.sessions.forEach(session => {
                    const key = String(session.name || '').toLowerCase();
                    if (key) next[key] = session;
                });
            }
            audioSessionsCache = next;
            paintAllAppVolumePills();
            return audioSessionsCache;
        })
        .catch(() => audioSessionsCache);
    audioSessionsFetchPromise = request.finally(() => { audioSessionsFetchPromise = null; });
    return audioSessionsFetchPromise;
}

function paintAppVolumePill(track, pct) {
    const clamped = Math.max(0, Math.min(100, Math.round(pct)));
    const fill = track.querySelector('.apple-pill-fill');
    const container = track.closest('.apple-pill-slider-container');
    const valEl = container ? container.querySelector('.apple-pill-value') : null;
    if (fill) fill.style.width = `${clamped}%`;
    if (valEl) valEl.textContent = `${clamped}%`;
}

function paintAllAppVolumePills() {
    document.querySelectorAll('.apple-pill-track[data-app-key]').forEach(track => {
        const cached = audioSessionsCache[track.getAttribute('data-app-key')];
        if (cached) paintAppVolumePill(track, cached.volume);
    });
}

function sendAppVolume(key, pct) {
    if (!key) return;
    if (wsClient && wsClient.readyState === WebSocket.OPEN) {
        wsClient.send(JSON.stringify({ type: 'set_app_volume', name: key, volume: pct }));
    }
}

function appVolumePctFromPointer(e, track) {
    const rect = track.getBoundingClientRect();
    if (rect.width <= 0) return 0;
    return Math.max(0, Math.min(100, Math.round(((e.clientX - rect.left) / rect.width) * 100)));
}

function applyAppVolumePointer(e, track) {
    const key = track.getAttribute('data-app-key');
    if (!key) return;
    const pct = appVolumePctFromPointer(e, track);
    if (audioSessionsCache[key]) audioSessionsCache[key].volume = pct;
    paintAppVolumePill(track, pct);
    sendAppVolume(key, pct);
}

// Capsule drag: pointerdown grabs the track, pointermove scrubs the level in
// real time (both mouse and touch), pointerup releases it.
function startAppVolumeDrag(e) {
    const track = e.currentTarget;
    if (!track || !track.getAttribute('data-app-key')) return;
    e.preventDefault();
    e.stopPropagation();
    appVolumeDragTrack = track;
    if (track.setPointerCapture) {
        try { track.setPointerCapture(e.pointerId); } catch (_) { /* not capturable */ }
    }
    applyAppVolumePointer(e, track);
}

window.addEventListener('pointermove', (e) => {
    if (!appVolumeDragTrack) return;
    e.preventDefault();
    applyAppVolumePointer(e, appVolumeDragTrack);
});
window.addEventListener('pointerup', () => { appVolumeDragTrack = null; });
window.addEventListener('pointercancel', () => { appVolumeDragTrack = null; });

// Fills the edit-modal dropdown with the live audio sessions.
function loadAudioSessionsForPicker(force = false) {
    const select = document.getElementById('edit-audio-app-select');
    if (!select) return;
    if (force) audioSessionsFetchPromise = null;
    const current = (document.getElementById('edit-value').value || '').trim().toLowerCase();
    fetchAudioSessions().then(cache => {
        const sessions = Object.values(cache).sort((a, b) => (b.active ? 1 : 0) - (a.active ? 1 : 0));
        select.innerHTML = '<option value="">-- Ses Oturumu Seçin --</option>';
        sessions.forEach(session => {
            const option = document.createElement('option');
            option.value = session.name;
            option.textContent = `${session.title || prettyAppLabel(session.name)} (${session.active ? 'çalıyor' : 'sessiz'})`;
            select.appendChild(option);
        });
        if (current) {
            select.value = current;
            return;
        }
        // Fresh card: preselect the top session so the widget works immediately.
        const first = sessions.find(session => session.active) || sessions[0];
        if (first) {
            select.value = first.name;
            document.getElementById('edit-value').value = first.name;
            const labelInput = document.getElementById('edit-label');
            const currentLabel = labelInput ? labelInput.value.trim() : '';
            if (labelInput && (!currentLabel || currentLabel === 'Uygulama Sesi')) {
                labelInput.value = first.title || prettyAppLabel(first.name);
            }
            const iconSelect = document.getElementById('edit-icon-select');
            if (iconSelect) iconSelect.value = 'app_session';
        }
    });
}

// Keeps the per-app pills honest when the level is changed outside BigPocket
// (e.g. inside the app itself). The loop cancels itself once no such pill is on
// screen and never fights an in-progress drag.
let appVolumeRefreshTimer = null;
function ensureAppVolumeRefreshLoop() {
    if (appVolumeRefreshTimer) return;
    appVolumeRefreshTimer = setInterval(() => {
        if (!document.querySelector('.apple-pill-track[data-app-key]')) {
            clearInterval(appVolumeRefreshTimer);
            appVolumeRefreshTimer = null;
            return;
        }
        if (!appVolumeDragTrack) fetchAudioSessions();
    }, 6000);
}

// Largest free rectangle anchored at (row, col), capped by the spans we want
function freeSpanAt(occ, rows, cols, row, col, wantRs, wantCs) {
    let cs = 0;
    while (cs < wantCs && col + cs < cols && !occ[row][col + cs]) cs++;
    let rs = 0;
    while (rs < wantRs && row + rs < rows) {
        let rowFree = true;
        for (let c = col; c < col + cs; c++) {
            if (occ[row + rs][c]) { rowFree = false; break; }
        }
        if (!rowFree) break;
        rs++;
    }
    return { rs: Math.max(1, rs), cs: Math.max(1, cs) };
}

// Pixel position of a drag event -> the (row, col) cell it landed on
function cellFromEvent(e, listEl, rows, cols) {
    const r = listEl.getBoundingClientRect();
    const x = (e.clientX - r.left) / Math.max(1, r.width);
    const y = (e.clientY - r.top) / Math.max(1, r.height);
    return {
        row: Math.max(0, Math.min(rows - 1, Math.floor(y * rows))),
        col: Math.max(0, Math.min(cols - 1, Math.floor(x * cols)))
    };
}

// Drag a card onto a cell: it takes that (grid_row, grid_col). If the cell is
// occupied the two cards trade coordinates (the occupant inherits the dragged
// card's previous cell, or returns to auto-packing when it had none).
function moveButtonToCell(draggedId, row, col) {
    if (!apiConfig || !apiConfig.stream_deck_buttons) return;
    const rows = Math.max(1, apiConfig.stream_deck_rows || 2);
    const cols = Math.max(1, apiConfig.stream_deck_cols || 4);
    const dragged = apiConfig.stream_deck_buttons.find(b => b.id === draggedId);
    if (!dragged) return;

    const prevRow = Number.isInteger(dragged.grid_row) ? dragged.grid_row : -1;
    const prevCol = Number.isInteger(dragged.grid_col) ? dragged.grid_col : -1;

    let cs = Math.min(Math.max(1, dragged.col_span || 1), cols);
    let rs = Math.min(Math.max(1, dragged.row_span || 1), rows);
    let r = Math.max(0, Math.min(rows - rs, row));
    let c = Math.max(0, Math.min(cols - cs, col));

    // Never move a card on top of another one: shrink to the free rectangle first
    const page = dragged.page || 0;
    const pageButtons = apiConfig.stream_deck_buttons.filter(b => (b.page || 0) === page && b.id !== draggedId);
    const layout = packBentoGrid(pageButtons, rows, cols);
    if (layout.occupied && layout.occupied[r][c]) {
        const span = freeSpanAt(layout.occupied, rows, cols, r, c, rs, cs);
        rs = span.rs;
        cs = span.cs;
    }

    const occupant = apiConfig.stream_deck_buttons.find(o =>
        o.id !== draggedId && (o.page || 0) === page &&
        o.grid_row >= 0 && o.grid_col >= 0 &&
        r < o.grid_row + (o.row_span || 1) && o.grid_row < r + rs &&
        c < o.grid_col + (o.col_span || 1) && o.grid_col < c + cs);

    if (occupant) {
        // Swap cells: the occupant inherits the dragged card's previous cell
        if (prevRow >= 0 && prevCol >= 0) {
            occupant.grid_row = prevRow;
            occupant.grid_col = prevCol;
        } else {
            occupant.grid_row = -1;
            occupant.grid_col = -1;
        }
    }

    dragged.grid_row = r;
    dragged.grid_col = c;
    renderStreamDeckButtons();
    debouncedSaveConfig(300);
}

function testButtonAction(buttonId, element) {
    if (element) {
        element.classList.add('deck-btn-triggered');
        setTimeout(() => element.classList.remove('deck-btn-triggered'), 300);
    }
    // High-speed WebSocket trigger (0ms latency)
    if (wsClient && wsClient.readyState === WebSocket.OPEN) {
        wsClient.send(JSON.stringify({
            type: 'stream_deck_press',
            button_id: buttonId
        }));
        return;
    }
    authFetch('/stream_deck_trigger', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ button_id: buttonId })
    })
    .catch(err => {
        console.error("Test trigger error", err);
    });
}

// ---- Sade kart penceresi: 4 alan (Başlık · Tür & Değer · Boyut · İkon) ----
const EDIT_VALUE_HINTS = {
    hotkey: { label: 'Kısayol Kombinasyonu', placeholder: 'Kaydetmek için klavyeden tuşlara basın...' },
    command: { label: 'Uygulama / Shell Komutu', placeholder: 'Örn. calc.exe veya chrome.exe' },
    url: { label: 'Web Adresi', placeholder: 'https://youtube.com' },
    text_snippet: { label: 'Yapıştırılacak Metin', placeholder: 'Örn. Merhaba, iyi çalışmalar.' },
    toggle: { label: 'Anahtar Eylemi', placeholder: 'mic_mute, speaker_mute, virtual_monitor, pomodoro' },
    volume_slider: { label: 'Ses Seviyesi (0-100)', placeholder: 'master_volume' },
    app_volume_slider: { label: 'Hedef Uygulama (exe adı)', placeholder: 'spotify.exe, chrome.exe, discord.exe' },
    brightness_slider: { label: 'Parlaklık (0-100)', placeholder: 'display_brightness' },
    live_info: { label: 'Sensör', placeholder: 'cpu, cpu_temp, ram, disk, gpu, net_speed, combo' },
    clock_widget: { label: 'Saat Gösterimi', placeholder: 'digital_clean, digital_seconds, analog, world_clock' },
    weather: { label: 'Şehir', placeholder: 'Istanbul, Ankara, London, New York' },
    now_playing: { label: 'Medya Kaynağı', placeholder: 'media_session' },
    media_widget: { label: 'Medya Eylemi', placeholder: 'playpause' },
    media_control: { label: 'Medya Eylemi', placeholder: 'playpause' }
};

function applyEditTypeHints(type) {
    const hint = EDIT_VALUE_HINTS[type] || { label: 'Değer', placeholder: '' };
    document.getElementById('edit-value-label').textContent = hint.label;
    document.getElementById('edit-value').placeholder = hint.placeholder;
    const appGroup = document.getElementById('installed-apps-group');
    appGroup.style.display = type === 'command' ? 'block' : 'none';
    if (type === 'command') loadInstalledApps();
}

function setEditSizePills(col, row) {
    editSizeSpans = { col, row };
    document.querySelectorAll('#edit-size-row .size-pill').forEach(pill => {
        const match = parseInt(pill.getAttribute('data-col'), 10) === col && parseInt(pill.getAttribute('data-row'), 10) === row;
        pill.classList.toggle('active', match);
    });
}

function openEditButtonModal(id) {
    const btn = apiConfig.stream_deck_buttons.find(b => b.id === id);
    if (!btn) return;

    editingButtonId = id;
    document.getElementById('edit-label').value = btn.label || '';
    document.getElementById('edit-type').value = btn.type || 'hotkey';
    document.getElementById('edit-value').value = btn.value || '';
    applyEditTypeHints(btn.type || 'hotkey');
    setEditSizePills(btn.col_span || 1, btn.row_span || 1);
    renderIconPickerGallery(btn.icon);

    document.getElementById('edit-button-modal').classList.add('active');
    setTimeout(() => document.getElementById('edit-label').focus(), 60);
}

const AVAILABLE_ICONS = [
    { key: 'mic', name: 'Mikrofon' },
    { key: 'volup', name: 'Ses Aç' },
    { key: 'voldown', name: 'Ses Kıs' },
    { key: 'volume', name: 'Ses Seviyesi' },
    { key: 'app_session', name: 'Uygulama Sesi' },
    { key: 'play', name: 'Oynat/Durdur' },
    { key: 'music', name: 'Müzik' },
    { key: 'headphones', name: 'Kulaklık' },
    { key: 'speaker', name: 'Hoparlör' },
    { key: 'desktop', name: 'Masaüstü' },
    { key: 'link', name: 'Web Bağlantısı' },
    { key: 'terminal', name: 'Terminal / CMD' },
    { key: 'task', name: 'Görev Yöneticisi' },
    { key: 'calc', name: 'Hesap Makinesi' },
    { key: 'web', name: 'Tarayıcı' },
    { key: 'lock', name: 'Kilit' },
    { key: 'cpu', name: 'İşlemci (CPU)' },
    { key: 'ram', name: 'Bellek (RAM)' },
    { key: 'clock', name: 'Saat' },
    { key: 'bright', name: 'Parlaklık' },
    { key: 'copy', name: 'Kopyala / Metin' },
    { key: 'folder', name: 'Dosya Gezgini' },
    { key: 'refresh', name: 'Yenile' },
    { key: 'bell', name: 'Bildirim' },
    { key: 'wifi', name: 'Kablosuz Ağ' },
    { key: 'camera', name: 'Kamera' },
    { key: 'monitor', name: 'Ekran' },
    { key: 'weather', name: 'Hava Durumu' },
    { key: 'thermometer', name: 'Sıcaklık' },
    { key: 'disk', name: 'Disk' },
    { key: 'gpu', name: 'Ekran Kartı' },
    { key: 'ai', name: 'Yapay Zeka' },
    { key: 'youtube', name: 'YouTube' },
    { key: 'trash', name: 'Çöp Kutusu' },
    { key: 'power', name: 'Güç / Kapat' },
    { key: 'custom', name: 'Özel / Diğer' }
];

function renderIconPickerGallery(selectedIcon) {
    const gallery = document.getElementById('icon-picker-gallery');
    if (!gallery) return;
    gallery.innerHTML = '';
    const currentKey = (selectedIcon || 'custom').replace(/^custom:/, '');
    const previewLabel = document.getElementById('selected-icon-preview-label');
    if (previewLabel) {
        previewLabel.textContent = `Aktif: ${currentKey}`;
    }

    AVAILABLE_ICONS.forEach(item => {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'icon-picker-item' + (currentKey === item.key ? ' active' : '');
        btn.title = item.name;
        btn.innerHTML = getSvgIcon(item.key);
        btn.addEventListener('click', () => {
            document.querySelectorAll('.icon-picker-item').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            const selectEl = document.getElementById('edit-icon-select');
            if (selectEl) {
                selectEl.value = item.key;
                selectEl.dispatchEvent(new Event('change'));
            }
            if (previewLabel) {
                previewLabel.textContent = `Aktif: ${item.name}`;
            }
        });
        gallery.appendChild(btn);
    });
}

function applyButtonEdit() {
    const label = document.getElementById('edit-label').value.trim();
    const type = document.getElementById('edit-type').value;
    const value = document.getElementById('edit-value').value;
    const icon = document.getElementById('edit-icon-select').value;

    const saveAndClose = (finalIcon) => {
        const idx = apiConfig.stream_deck_buttons.findIndex(b => b.id === editingButtonId);
        if (idx !== -1) {
            const existing = apiConfig.stream_deck_buttons[idx];
            apiConfig.stream_deck_buttons[idx] = {
                ...existing,
                id: editingButtonId,
                label: label || existing.label,
                type,
                value,
                icon: finalIcon,
                color: '#F4F4F5',
                col_span: editSizeSpans.col,
                row_span: editSizeSpans.row
            };
            targetEmptyCell = null;
            renderStreamDeckButtons();
            saveConfigOnServer();
        }
        document.getElementById('edit-button-modal').classList.remove('active');
    };

    const lowerVal = value.trim().toLowerCase();
    const isAppPath = lowerVal.endsWith('.exe') || lowerVal.endsWith('.lnk') || value.includes('\\') || value.includes('/');
    if (type === 'command' && value.trim().length > 0 && icon !== 'custom' && isAppPath) {
        authFetch('/extract_icon', {
            method: 'POST',
            body: JSON.stringify({
                path: value.trim(),
                button_id: editingButtonId
            })
        })
        .then(res => res.json())
        .then(data => {
            if (data.success && data.icon) saveAndClose("custom:" + data.icon);
            else saveAndClose(icon);
        })
        .catch(err => {
            console.error("Icon extraction failed", err);
            saveAndClose(icon);
        });
    } else {
        saveAndClose(icon);
    }
}

function saveConfigOnServer(immediate = true) {
    if (saveConfigTimer) {
        clearTimeout(saveConfigTimer);
        saveConfigTimer = null;
    }
    return authFetch('/config', {
        method: 'POST',
        body: JSON.stringify(apiConfig)
    })
    .then(res => res.json())
    .then(data => {
        if (data.success) {
            console.log("Configuration saved successfully.");
        } else {
            console.warn("Kaydetme uyarısı:", data.error);
        }
    })
    .catch(err => console.error("Error saving config", err));
}

function debouncedSaveConfig(delay = 350) {
    if (saveConfigTimer) {
        clearTimeout(saveConfigTimer);
    }
    saveConfigTimer = setTimeout(() => {
        saveConfigTimer = null;
        saveConfigOnServer(true);
    }, delay);
}

// ----------------- 2nd Monitor Driver Management -----------------
function setupMonitorManager() {
    const installBtn = document.getElementById('btn-install-driver');
    const chkEnable = document.getElementById('chk-enable-monitor');

    installBtn.addEventListener('click', () => {
        installBtn.textContent = "Yükleniyor...";
        installBtn.disabled = true;
        
        authFetch('/driver/install', { method: 'POST' })
            .then(res => res.json())
            .then(data => {
                alert(data.message);
                checkDriverStatus();
            })
            .catch(err => {
                installBtn.textContent = "Sürücüyü Yükle";
                installBtn.disabled = false;
            });
    });

    chkEnable.addEventListener('change', () => {
        const enable = chkEnable.checked;
        authFetch('/driver/enable', {
            method: 'POST',
            body: JSON.stringify({ enable })
        })
        .then(res => res.json())
        .then(data => {
            if (!data.success) {
                alert("Ekran ayarı başarısız: " + data.message);
                chkEnable.checked = !enable;
            } else {
                checkDriverStatus();
            }
        })
        .catch(err => {
            chkEnable.checked = !enable;
        });
    });
}

function checkDriverStatus() {
    authFetch('/driver/status')
        .then(res => res.json())
        .then(data => {
            updateDriverUI(data);
        })
        .catch(err => console.error("Error checking driver status", err));
}

function updateDriverUI(data) {
    const icon = document.getElementById('driver-status-icon');
    const title = document.getElementById('driver-title');
    const desc = document.getElementById('driver-desc');
    const installBtn = document.getElementById('btn-install-driver');
    const toggleContainer = document.getElementById('monitor-toggle-container');
    const badge = document.getElementById('driver-badge');
    const chkEnable = document.getElementById('chk-enable-monitor');

    if (data.driver_exists) {
        icon.className = "driver-status-icon success";
        title.textContent = "Sanal Ekran Sürücüsü Aktif";
        
        if (data.monitors > 1) {
            desc.textContent = `Toplam monitör sayısı: ${data.monitors}. Sanal monitör aktif ve sinyal gönderiliyor.`;
            badge.className = "status-pill status-green";
            badge.textContent = "AKTİF";
            chkEnable.checked = true;
        } else {
            desc.textContent = "Sürücü yüklü ancak sanal monitör şu an pasif durumda.";
            badge.className = "status-pill status-yellow";
            badge.textContent = "KAPALI";
            chkEnable.checked = false;
        }
        
        installBtn.style.display = 'none';
        if (data.admin) {
            toggleContainer.style.display = 'flex';
        } else {
            toggleContainer.style.display = 'none';
        }
    } else {
        icon.className = "driver-status-icon error";
        title.textContent = "Sanal Ekran Sürücüsü Eksik";
        desc.textContent = "Sanal ikinci ekran özelliğini kullanmak için sisteminize sürücüyü kurmanız gerekir.";
        badge.className = "status-pill status-red";
        badge.textContent = "EKSİK";
        
        toggleContainer.style.display = 'none';
        if (data.admin) {
            installBtn.style.display = 'inline-block';
            installBtn.textContent = "Sürücüyü Yükle";
            installBtn.disabled = false;
            installBtn.style.opacity = "1";
        } else {
            installBtn.style.display = 'none';
        }
    }
}

// ----------------- Webcam Stream Preview -----------------
function setupWebcam() {
    tabButtons.forEach(btn => {
        btn.addEventListener('click', () => {
            const target = btn.getAttribute('data-tab');
            const streamImg = document.getElementById('webcam-stream');
            const placeholder = document.getElementById('video-placeholder');
            
            if (target === 'webcam') {
                const password = getAuthPassword();
                const authQuery = password ? `?password=${encodeURIComponent(password)}` : '';
                streamImg.src = `/video_feed${authQuery}`;
                streamImg.style.display = 'block';
                placeholder.style.display = 'none';
            } else {
                streamImg.src = '';
                streamImg.style.display = 'none';
                placeholder.style.display = 'flex';
            }
        });
    });
}

// ----------------- File Transfer & Drag Drop -----------------
function setupDragAndDrop() {
    const zone = document.getElementById('drag-drop-zone');
    const fileInput = document.getElementById('file-input');
    const openFolderBtn = document.getElementById('btn-open-folder');

    if (openFolderBtn) {
        openFolderBtn.addEventListener('click', () => {
            authFetch('/open_shared_folder', { method: 'POST' });
        });
    }

    zone.addEventListener('click', () => fileInput.click());

    fileInput.addEventListener('change', () => {
        if (fileInput.files.length > 0) {
            uploadMultipleFiles(fileInput.files);
        }
    });

    zone.addEventListener('dragover', (e) => {
        e.preventDefault();
        zone.classList.add('dragover');
    });

    zone.addEventListener('dragleave', () => {
        zone.classList.remove('dragover');
    });

    zone.addEventListener('drop', (e) => {
        e.preventDefault();
        zone.classList.remove('dragover');
        if (e.dataTransfer.files.length > 0) {
            uploadMultipleFiles(e.dataTransfer.files);
        }
    });
}

function uploadMultipleFiles(files) {
    const promises = Array.from(files).map(file => {
        const formData = new FormData();
        formData.append('file', file);
        return authFetch('/upload', {
            method: 'POST',
            body: formData
        }).then(res => res.json());
    });

    Promise.all(promises)
        .then(results => {
            console.log("All files uploaded successfully");
            fetchFiles();
        })
        .catch(err => {
            console.error("Upload error:", err);
            alert("Dosya yüklenirken bir sorun oluştu.");
        });
}

// Fetch Files
function fetchFiles() {
    authFetch('/files')
        .then(res => res.json())
        .then(data => {
            if (data.success) {
                renderFilesList(data.files);
            }
        })
        .catch(err => console.error("Error fetching files", err));
}

function renderFilesList(files) {
    const tbody = document.getElementById('files-table-body');
    tbody.innerHTML = '';

    if (files.length === 0) {
        tbody.innerHTML = '<tr><td colspan="4" style="text-align:center; color:var(--text-muted);">Henüz paylaşılan bir dosya bulunmuyor.</td></tr>';
        return;
    }

    const password = getAuthPassword();
    const authQuery = password ? `?password=${encodeURIComponent(password)}` : '';

    function getFileIcon(name) {
        const ext = name.split('.').pop().toLowerCase();
        if (['mp4', 'mkv', 'avi', 'mov', 'webm'].includes(ext)) {
            return `<span style="display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:6px;background:rgba(96,165,250,0.15);color:#60A5FA;margin-right:8px;"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="23 7 16 12 23 17 23 7"></polygon><rect x="1" y="5" width="15" height="14" rx="2" ry="2"></rect></svg></span>`;
        }
        if (['mp3', 'wav', 'flac', 'aac', 'ogg'].includes(ext)) {
            return `<span style="display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:6px;background:rgba(165,166,246,0.15);color:#A5A6F6;margin-right:8px;"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 18V5l12-2v13"></path><circle cx="6" cy="18" r="3"></circle><circle cx="18" cy="16" r="3"></circle></svg></span>`;
        }
        if (['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg'].includes(ext)) {
            return `<span style="display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:6px;background:rgba(74,222,128,0.15);color:#4ADE80;margin-right:8px;"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"></rect><circle cx="8.5" cy="8.5" r="1.5"></circle><polyline points="21 15 16 10 5 21"></polyline></svg></span>`;
        }
        if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) {
            return `<span style="display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:6px;background:rgba(251,191,36,0.15);color:#FBBF24;margin-right:8px;"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"></path><polyline points="3.27 6.96 12 12.01 20.73 6.96"></polyline><line x1="12" y1="22.08" x2="12" y2="12"></line></svg></span>`;
        }
        return `<span style="display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:6px;background:rgba(255,255,255,0.06);color:#A1A1AA;margin-right:8px;"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline></svg></span>`;
    }

    files.forEach(file => {
        const tr = document.createElement('tr');
        const sizeFormatted = file.size > 1048576 ? (file.size / 1048576).toFixed(1) + ' MB' : (file.size / 1024).toFixed(1) + ' KB';
        const dateStr = new Date(file.modified * 1000).toLocaleString('tr-TR');
        const fileIcon = getFileIcon(file.name);
        
        tr.innerHTML = `
            <td>
                <div style="display:flex;align-items:center;">
                    ${fileIcon}
                    <a href="/download/${encodeURIComponent(file.name)}${authQuery}" download style="word-break:break-all;">${file.name}</a>
                </div>
            </td>
            <td class="font-mono" style="white-space:nowrap;">${sizeFormatted}</td>
            <td style="white-space:nowrap;font-size:12px;color:var(--text-muted);">${dateStr}</td>
            <td style="text-align: right;white-space:nowrap;">
                <button class="btn-danger" onclick="deleteFile('${encodeURIComponent(file.name)}')">Sil</button>
            </td>
        `;
        tbody.appendChild(tr);
    });
}

function deleteFile(filename) {
    if (confirm("Bu dosyayı silmek istediğinize emin misiniz?")) {
        authFetch(`/files/${filename}`, { method: 'DELETE' })
            .then(res => res.json())
            .then(data => {
                if (data.success) {
                    fetchFiles();
                } else {
                    alert("Dosya silinemedi: " + data.error);
                }
            })
            .catch(err => console.error("Error deleting file", err));
    }
}
window.deleteFile = deleteFile;

let installedApps = [];

function loadInstalledApps() {
    if (installedApps.length > 0) {
        populateAppSelect();
        return;
    }
    
    authFetch('/installed_apps')
        .then(res => res.json())
        .then(data => {
            if (data.success) {
                installedApps = data.apps || [];
                // Sort alphabetically
                installedApps.sort((a, b) => a.name.localeCompare(b.name, 'tr'));
                populateAppSelect();
            }
        })
        .catch(err => console.error("Error loading installed apps", err));
}

function populateAppSelect(filterText = "") {
    const term = filterText.toLowerCase();
    const filtered = installedApps.filter(app => app.name.toLowerCase().includes(term));

    const select = document.getElementById('edit-app-select');
    if (select) {
        select.innerHTML = '<option value="">-- Uygulama Seçin --</option>';
        filtered.forEach(app => {
            const opt = document.createElement('option');
            opt.value = app.path;
            opt.textContent = app.name;
            select.appendChild(opt);
        });
    }
}

function setupPCClipboardSync() {
    const btnSend = document.getElementById('btn-send-clipboard-pc');
    const sendArea = document.getElementById('pc-clipboard-send');
    if (btnSend && sendArea) {
        btnSend.addEventListener('click', () => {
            const text = sendArea.value;
            if (text && wsClient && wsClient.readyState === WebSocket.OPEN) {
                wsClient.send(JSON.stringify({
                    type: 'clipboard_sync',
                    text: text
                }));
                sendArea.value = '';
                alert("Metin telefona gönderildi!");
            } else if (!text) {
                alert("Lütfen telefona gönderilecek bir metin girin.");
            } else {
                alert("Mobil bağlantı aktif değil.");
            }
        });
    }
    
    const btnCopy = document.getElementById('btn-copy-clipboard');
    const previewArea = document.getElementById('pc-clipboard-preview');
    if (btnCopy && previewArea) {
        btnCopy.addEventListener('click', () => {
            if (previewArea.value) {
                navigator.clipboard.writeText(previewArea.value)
                    .then(() => alert("Metin bilgisayar panosuna kopyalandı!"))
                    .catch(err => console.error("Metin kopyalanamadı", err));
            } else {
                alert("Kopyalanacak metin yok.");
            }
        });
    }
}

// ----------------- USB Tethering IP Selection Handler -----------------
function setupIPSelectHandler() {
    const ipSelect = document.getElementById('pc-ip-select');
    if (ipSelect) {
        ipSelect.addEventListener('change', () => {
            if (latestPorts) {
                updatePairingQR(ipSelect.value, latestPorts);
            }
        });
    }

    const copyIpBtn = document.getElementById('btn-copy-ip');
    if (copyIpBtn) {
        copyIpBtn.addEventListener('click', () => {
            const selectedIp = document.getElementById('pc-ip-select')?.value || "127.0.0.1";
            navigator.clipboard.writeText(selectedIp)
                .then(() => alert(`IP adresi (${selectedIp}) panoya kopyalandı!`))
                .catch(err => console.error("IP kopyalanamadı", err));
        });
    }
}

// ----------------- Virtual Mic Driver Management -----------------
function setupMicDriverManager() {
    const installBtn = document.getElementById('btn-install-mic-driver');
    const openSoundBtn = document.getElementById('btn-open-sound-settings');
    if (!installBtn) return;
    
    installBtn.addEventListener('click', () => {
        installBtn.textContent = "Yükleniyor...";
        installBtn.disabled = true;
        
        authFetch('/driver/mic/install', { method: 'POST' })
            .then(res => res.json())
            .then(data => {
                if (data.success) {
                    if (confirm("Sanal Mikrofon Sürücüsü kuruldu!\n\nWindows varsayılan ses çıkış cihazını 'CABLE Input' yapmış olabilir. Eğer bilgisayarınızdan ses gelmiyorsa ses kontrol panelini açıp kendi hoparlörünüzü varsayılan yapmak ister misiniz?")) {
                        authFetch('/open_sound_settings', { method: 'POST' });
                    }
                } else {
                    alert(data.message);
                }
                checkMicDriverStatus();
            })
            .catch(err => {
                console.error("Mic driver installation failed", err);
                installBtn.textContent = "Sanal Mikrofonu Kur";
                installBtn.disabled = false;
            });
    });

    if (openSoundBtn) {
        openSoundBtn.addEventListener('click', () => {
            authFetch('/open_sound_settings', { method: 'POST' })
                .catch(err => console.error("Error opening sound settings", err));
        });
    }
}

function checkMicDriverStatus() {
    authFetch('/driver/mic/status')
        .then(res => res.json())
        .then(data => {
            const badge = document.getElementById('mic-driver-badge');
            const statusText = document.getElementById('mic-driver-status-text');
            const installBtn = document.getElementById('btn-install-mic-driver');
            const openSoundBtn = document.getElementById('btn-open-sound-settings');
            
            if (data.installed) {
                if (badge) {
                    badge.className = "status-pill status-green";
                    badge.textContent = "KURULU";
                }
                if (statusText) {
                    statusText.className = "status-pill status-green";
                    statusText.textContent = "KURULU";
                }
                if (installBtn) {
                    installBtn.disabled = true;
                    installBtn.textContent = "Sürücü Kurulu";
                    installBtn.style.opacity = "0.6";
                }
                if (openSoundBtn) {
                    openSoundBtn.style.display = "block";
                }
            } else {
                if (badge) {
                    badge.className = "status-pill status-red";
                    badge.textContent = "EKSİK";
                }
                if (statusText) {
                    statusText.className = "status-pill status-red";
                    statusText.textContent = "EKSİK";
                }
                if (installBtn) {
                    installBtn.disabled = false;
                    installBtn.textContent = "Sanal Mikrofonu Kur";
                    installBtn.style.opacity = "1";
                }
                if (openSoundBtn) {
                    openSoundBtn.style.display = "none";
                }
            }
        })
        .catch(err => console.error("Error checking mic driver status", err));
}

function checkMobileMode() {
    const urlParams = new URLSearchParams(window.location.search);
    const mode = urlParams.get('mode');
    if (mode) {
        document.body.classList.add('mobile-mode');
        document.querySelector('.sidebar').style.display = 'none';
        document.querySelector('.content-header').style.display = 'none';
        document.querySelector('.main-content').style.marginLeft = '0';
        document.querySelector('.main-content').style.padding = '0';
        document.querySelector('.app-container').style.padding = '0';
        
        // Hide all tabs
        document.querySelectorAll('.tab-panel').forEach(p => p.classList.remove('active'));
        
        const targetPanel = document.getElementById('panel-' + mode);
        if(targetPanel) {
            targetPanel.classList.add('active');
            targetPanel.style.height = '100vh';
            const bento = targetPanel.querySelector('.bento-grid');
            if(bento) {
                bento.style.height = '100%';
                bento.style.margin = '0';
                bento.style.padding = '10px';
                bento.style.display = 'flex';
                bento.style.flexDirection = 'column';
            }
            const cards = targetPanel.querySelectorAll('.card');
            cards.forEach(c => {
                const header = c.querySelector('.card-header');
                if(header) header.style.display = 'none';
                c.style.flex = '1';
                c.style.margin = '0';
                c.style.height = '100%';
            });
            const deckSettings = targetPanel.querySelector('.deck-settings-bar');
            if(deckSettings) deckSettings.style.display = 'none';
            const buttonsGrid = targetPanel.querySelector('.buttons-grid');
            if(buttonsGrid) {
                buttonsGrid.style.height = '100%';
            }
        }
    }
}

// ----------------- Auto Update Prompt -----------------
(function () {
    const SKIP_KEY = 'bigpocket_skip_version';

    function escapeHtml(s) {
        return String(s || '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
    }

    // Tiny markdown renderer for release notes (headings, lists, bold, code, links).
    function renderNotes(md) {
        const lines = escapeHtml(md).split(/\r?\n/);
        let html = '', inList = false;
        const inline = t => t
            .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
            .replace(/`([^`]+)`/g, '<code>$1</code>')
            .replace(/\[([^\]]+)\]\((https?:[^)]+)\)/g, '<a href="$2" target="_blank">$1</a>');
        for (const raw of lines) {
            const line = raw.trim();
            const li = line.match(/^[-*]\s+(.*)/);
            if (li) {
                if (!inList) { html += '<ul>'; inList = true; }
                html += '<li>' + inline(li[1]) + '</li>';
                continue;
            }
            if (inList) { html += '</ul>'; inList = false; }
            const h = line.match(/^(#{1,3})\s+(.*)/);
            if (h) html += '<h4>' + inline(h[2]) + '</h4>';
            else if (line) html += '<p>' + inline(line) + '</p>';
        }
        if (inList) html += '</ul>';
        return html || '<p>Sürüm notu yok.</p>';
    }

    function injectStyles() {
        if (document.getElementById('bp-update-style')) return;
        const st = document.createElement('style');
        st.id = 'bp-update-style';
        st.textContent = `
        .bp-upd-overlay{position:fixed;inset:0;background:rgba(6,6,8,.72);backdrop-filter:blur(16px) saturate(120%);-webkit-backdrop-filter:blur(16px) saturate(120%);display:flex;align-items:center;justify-content:center;z-index:9999;animation:bpFade .24s ease-out}
        .bp-upd-card{width:min(520px,92vw);max-height:80vh;display:flex;flex-direction:column;background:var(--bg-sidebar);border:1px solid var(--border-color);border-radius:20px;padding:24px;color:var(--text-main);box-shadow:0 20px 60px rgba(0,0,0,.35);animation:modalFadeInScale .26s cubic-bezier(.16,1,.3,1) both}
        .bp-upd-badge{display:inline-block;font-size:12px;padding:4px 10px;border-radius:999px;background:rgba(255,255,255,.06);border:1px solid var(--border-color);color:var(--text-main);margin-bottom:10px}
        .bp-upd-card h3{font-size:20px;font-weight:600;margin-bottom:4px}
        .bp-upd-ver{color:var(--text-muted);font-size:13px;margin-bottom:16px;font-family:var(--font-mono)}
        .bp-upd-notes{overflow:auto;background:var(--bg-elevated,#26262C);border-radius:12px;padding:14px 16px;font-size:14px;line-height:1.55;color:var(--text-main);flex:1}
        .bp-upd-notes h4{font-size:14px;margin:10px 0 4px}.bp-upd-notes ul{padding-left:18px;margin:4px 0}.bp-upd-notes p{margin:4px 0}
        .bp-upd-notes code{font-family:var(--font-mono);background:rgba(255,255,255,.06);padding:1px 5px;border-radius:5px}
        .bp-upd-notes a{color:var(--accent-solid)}
        .bp-upd-bar{height:6px;border-radius:999px;background:rgba(255,255,255,.06);margin-top:16px;overflow:hidden;display:none}
        .bp-upd-bar>div{height:100%;width:100%;transform:scaleX(0);transform-origin:left center;background:var(--accent-solid);transition:transform .25s cubic-bezier(.16,1,.3,1)}
        .bp-upd-status{font-size:13px;color:var(--text-muted);margin-top:8px;min-height:18px}
        .bp-upd-actions{display:flex;gap:10px;justify-content:flex-end;margin-top:16px}
        .bp-upd-actions button{border:none;border-radius:12px;padding:10px 16px;font-size:14px;cursor:pointer;transition:background .2s}
        .bp-upd-ghost{background:transparent;color:var(--text-muted)}.bp-upd-ghost:hover{background:rgba(255,255,255,.05)}
        .bp-upd-primary{background:var(--accent-solid);color:var(--text-inverse);font-weight:600}.bp-upd-primary:hover{background:var(--accent-hover)}
        .bp-upd-primary:disabled{opacity:.5;cursor:default}
        @keyframes bpFade{from{opacity:0}to{opacity:1}}`;
        document.head.appendChild(st);
    }

    function showUpdateDialog(info, manual) {
        injectStyles();
        const ov = document.createElement('div');
        ov.className = 'bp-upd-overlay';
        ov.innerHTML = `
          <div class="bp-upd-card">
            <span class="bp-upd-badge">Yeni güncelleme</span>
            <h3>${escapeHtml(info.title || ('BigPocket ' + info.latest_version))}</h3>
            <div class="bp-upd-ver">v${escapeHtml(info.current_version)} → v${escapeHtml(info.latest_version)}</div>
            <div class="bp-upd-notes">${renderNotes(info.notes)}</div>
            <div class="bp-upd-bar"><div></div></div>
            <div class="bp-upd-status"></div>
            <div class="bp-upd-actions">
              <button class="bp-upd-ghost" data-act="skip">Bu sürümü atla</button>
              <button class="bp-upd-ghost" data-act="later">Sonra</button>
              <button class="bp-upd-primary" data-act="install">Güncelle</button>
            </div>
          </div>`;
        document.body.appendChild(ov);
        const close = () => ov.remove();
        const bar = ov.querySelector('.bp-upd-bar'), fill = bar.firstElementChild, status = ov.querySelector('.bp-upd-status');

        ov.querySelector('[data-act=later]').onclick = close;
        ov.querySelector('[data-act=skip]').onclick = () => { localStorage.setItem(SKIP_KEY, info.latest_version); close(); };
        ov.querySelector('[data-act=install]').onclick = async (e) => {
            e.target.disabled = true;
            ov.querySelectorAll('.bp-upd-ghost').forEach(b => b.disabled = true);
            bar.style.display = 'block';
            status.textContent = 'İndiriliyor...';
            try {
                const r = await fetch('/api/update/install', { method: 'POST' }).then(r => r.json());
                if (!r.success) throw new Error(r.error || 'Başarısız');
            } catch (err) {
                status.textContent = 'Hata: ' + err.message;
                e.target.disabled = false;
                return;
            }
            const timer = setInterval(async () => {
                try {
                    const p = await fetch('/api/update/progress').then(r => r.json());
                    fill.style.transform = 'scaleX(' + ((p.percent || 0) / 100) + ')';
                    if (p.state === 'downloading') status.textContent = `İndiriliyor... %${p.percent}`;
                    else if (p.state === 'installing') status.textContent = 'Kuruluyor...';
                    else if (p.state === 'restarting') status.textContent = 'Yeniden başlatılıyor...';
                    else if (p.state === 'error') { clearInterval(timer); status.textContent = 'Hata: ' + p.message; e.target.disabled = false; }
                } catch (_) {
                    // Server went down => restarting. Reload once it is back.
                    clearInterval(timer);
                    status.textContent = 'Yeniden başlatılıyor...';
                    const wait = setInterval(() => {
                        fetch('/api/version').then(() => { clearInterval(wait); location.reload(); }).catch(() => {});
                    }, 1500);
                }
            }, 500);
        };
    }

    window.checkForUpdates = async function (manual = false) {
        try {
            const isBeta = (apiConfig && !!apiConfig.beta_channel) || (document.getElementById('settings-beta-channel-toggle')?.checked);
            const betaParam = isBeta ? '&beta=1' : '&beta=0';
            const info = await fetch('/api/update/check' + (manual ? '?force=1' : '?force=0') + betaParam).then(r => r.json());
            const curVerClean = (info.current_version || '1.4.1').replace(/^v+/, '');
            const currentVerEl = document.getElementById('settings-current-ver');
            if (currentVerEl) {
                currentVerEl.textContent = 'v' + curVerClean;
            }
            if (info.available) {
                if (!manual && localStorage.getItem(SKIP_KEY) === info.latest_version) return;
                showUpdateDialog(info, manual);
            } else if (manual) {
                const updateStatusEl = document.getElementById('settings-update-status');
                if (updateStatusEl) {
                    if (info.error) {
                        updateStatusEl.style.color = '#ef4444';
                        updateStatusEl.textContent = 'Hata: ' + info.error;
                    } else {
                        updateStatusEl.style.color = '#10b981';
                        updateStatusEl.textContent = `En güncel sürümü kullanıyorsunuz (v${curVerClean})`;
                    }
                } else {
                    alert(info.error ? ('Güncelleme kontrol edilemedi: ' + info.error) : `En güncel sürümü kullanıyorsunuz (v${curVerClean}).`);
                }
            }
        } catch (_) { /* offline */ }
    };

    document.addEventListener('DOMContentLoaded', () => {
        const manualBtn = document.getElementById('btn-check-update-manual');
        const updateStatusEl = document.getElementById('settings-update-status');
        if (manualBtn) {
            manualBtn.addEventListener('click', async () => {
                if (manualBtn.disabled) return;
                manualBtn.disabled = true;
                if (updateStatusEl) {
                    updateStatusEl.style.color = 'var(--text-muted)';
                    updateStatusEl.textContent = 'GitHub üzerinden güncellemeler denetleniyor...';
                }
                try {
                    await window.checkForUpdates(true);
                } finally {
                    manualBtn.disabled = false;
                }
            });
        }
        const betaToggle = document.getElementById('settings-beta-channel-toggle');
        if (betaToggle) {
            betaToggle.addEventListener('change', async (e) => {
                if (apiConfig) {
                    apiConfig.beta_channel = e.target.checked;
                    saveConfigOnServer(true);
                }
                if (window.checkForUpdates) {
                    await window.checkForUpdates(true);
                }
            });
        }
    });

    window.addEventListener('load', () => setTimeout(() => window.checkForUpdates(false), 1500));
})();
