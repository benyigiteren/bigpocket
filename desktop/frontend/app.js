// Global State
let currentTab = 'dashboard';
let apiConfig = null;
let wsClient = null;
let latestPorts = null;

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

// Custom fetch wrapper that adds auth header
function authFetch(url, options = {}) {
    const isLocal = window.location.hostname === 'localhost' || 
                    window.location.hostname === '127.0.0.1' || 
                    window.location.hostname === '';
    const password = getAuthPassword();
    options.headers = options.headers || {};
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

// Initialize App
document.addEventListener('DOMContentLoaded', () => {
    checkMobileMode();
    setupTabNavigation();
    setupDragAndDrop();
    setupPCClipboardSync();
    setupStreamDeck();
    setupMonitorManager();
    setupMicDriverManager();
    setupWebcam();
    setupShutdown();
    setupPasswordPromptHandlers();
    setupDashboardPassword();
    setupIPSelectHandler();
    setupUsbBridge();
    
    // Fetch system info and configurations
    fetchSystemInfo();
    fetchConfig();
    fetchFiles();
    checkMicDriverStatus();
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
        qrCode.src = `https://api.qrserver.com/v1/create-qr-code/?size=150x150&data=${encodeURIComponent(payload)}`;
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
            
            if (targetTab === 'files') {
                fetchFiles();
            } else if (targetTab === 'monitor') {
                checkDriverStatus();
            }
        });
    });
}

// WebSocket Connection (Checks mobile connection status)
function connectWebSocket() {
    // Go backend serves the websocket on port 8085 under path /ws
    const loc = window.location;
    const wsProto = loc.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = `${wsProto}//${loc.host}/ws`;
    
    console.log("Connecting to WebSocket:", wsUrl);
    wsClient = new WebSocket(wsUrl);

    wsClient.onopen = () => {
        console.log("WebSocket connection established");
    };

    wsClient.onmessage = (event) => {
        const msg = JSON.parse(event.data);
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
        } else if (msg.type === 'config_update') {
            console.log("Configuration updated, reloading...");
            fetchConfig();
        } else if (msg.type === 'system_stats') {
            cachedLiveStats = {
                cpu: msg.cpu || 0,
                ram: msg.ram || 0,
                cpu_temp: msg.cpu_temp || (38 + Math.round((msg.cpu || 0) * 0.45)),
                ram_used_gb: msg.ram_used_gb || 0,
                ram_total_gb: msg.ram_total_gb || 16
            };
            const ramBadge = document.getElementById('stat-ram');
            if (ramBadge) {
                ramBadge.textContent = `%${msg.ram} (${cachedLiveStats.ram_used_gb} GB)`;
            }
            // Update any visible live_info buttons without full re-render
            updateLiveStatsDOM();
        } else if (msg.type === 'button_state_changed') {
            if (apiConfig && apiConfig.stream_deck_buttons) {
                const b = apiConfig.stream_deck_buttons.find(btn => btn.id === msg.button_id);
                if (b) {
                    b.state = msg.state;
                    renderStreamDeckButtons();
                }
            }
        }
    };

    wsClient.onclose = () => {
        statusText.textContent = "SUNUCU BAĞLANTISI KOPTU";
        statusPulse.className = "status-dot";
        // Retry connection in 3 seconds
        setTimeout(connectWebSocket, 3000);
    };
}

// ----------------- Stream Deck Config Panel -----------------
let editingButtonId = null;

const iconMapUnicode = {
    "mic": "🎤",
    "volup": "🔊",
    "voldown": "🔉",
    "play": "⏯️",
    "calc": "🧮",
    "web": "🌐",
    "lock": "🔒",
    "task": "📊",
    "custom": "⭐"
};

const PRESETS = {
    pro_studio: [
        { label: "Canlı Saat", type: "clock_widget", value: "digital_clock", icon: "clock", col_span: 2, row_span: 1, color: "#EC4899" },
        { label: "Ana Ses", type: "volume_slider", value: "master_volume", icon: "volume", col_span: 2, row_span: 1, color: "#6366F1" },
        { label: "İşlemci & Sıcaklık", type: "live_info", value: "cpu", icon: "cpu", col_span: 1, row_span: 1, color: "#F59E0B" },
        { label: "Bellek Durumu", type: "live_info", value: "ram", icon: "ram", col_span: 1, row_span: 1, color: "#10B981" },
        { label: "Mikrofon", type: "toggle", value: "mic_mute", icon: "mic", col_span: 1, row_span: 1, state: true, color: "#10B981" },
        { label: "Hoparlör", type: "toggle", value: "sound_mute", icon: "voldown", col_span: 1, row_span: 1, state: false, color: "#EF4444" },
    ],
    hardware: [
        { label: "Sistem Saati", type: "clock_widget", value: "digital_clock", icon: "clock", col_span: 2, row_span: 1, color: "#818CF8" },
        { label: "İşlemci Yükü & Isı", type: "live_info", value: "cpu", icon: "cpu", col_span: 1, row_span: 1, color: "#F59E0B" },
        { label: "RAM Kullanımı", type: "live_info", value: "ram", icon: "ram", col_span: 1, row_span: 1, color: "#10B981" },
        { label: "Görev Yöneticisi", type: "command", value: "taskmgr.exe", icon: "task", col_span: 2, row_span: 1, color: "#3B82F6" },
        { label: "PC Kilitle", type: "command", value: "rundll32.exe user32.dll,LockWorkStation", icon: "lock", col_span: 2, row_span: 1, color: "#EF4444" },
    ],
    media: [
        { label: "Ana Ses Barı", type: "volume_slider", value: "master_volume", icon: "volume", col_span: 2, row_span: 1, color: "#6366F1" },
        { label: "Sesi Kapat", type: "toggle", value: "sound_mute", icon: "voldown", col_span: 1, row_span: 1, color: "#EF4444" },
        { label: "Mikrofon", type: "toggle", value: "mic_mute", icon: "mic", col_span: 1, row_span: 1, color: "#10B981" },
        { label: "Önceki Parça", type: "hotkey", value: "prevtrack", icon: "play", col_span: 1, row_span: 1, color: "#3B82F6" },
        { label: "Oynat / Durdur", type: "hotkey", value: "playpause", icon: "play", col_span: 2, row_span: 1, color: "#818CF8" },
        { label: "Sonraki Parça", type: "hotkey", value: "nexttrack", icon: "play", col_span: 1, row_span: 1, color: "#3B82F6" },
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

function applyPreset(presetName) {
    if (!PRESETS[presetName] || !apiConfig) return;
    const preset = PRESETS[presetName];
    const totalSize = apiConfig.stream_deck_rows * apiConfig.stream_deck_cols;
    
    const newButtons = [];
    for (let i = 0; i < totalSize; i++) {
        const pBtn = preset[i % preset.length];
        newButtons.push({
            id: i,
            label: pBtn.label,
            type: pBtn.type,
            value: pBtn.value,
            icon: pBtn.icon
        });
    }
    apiConfig.stream_deck_buttons = newButtons;
    renderStreamDeckButtons();
}

function setupStreamDeck() {
    const saveBtn = document.getElementById('btn-save-config');
    const modal = document.getElementById('edit-button-modal');
    const closeModalBtn = document.getElementById('btn-close-modal');
    const applyEditBtn = document.getElementById('btn-apply-edit');
    const editType = document.getElementById('edit-type');
    const editValueLabel = document.getElementById('edit-value-label');
    const editValue = document.getElementById('edit-value');
    const iconSelect = document.getElementById('edit-icon-select');
    const customIconGroup = document.getElementById('custom-icon-group');
    const customIconUploadGroup = document.getElementById('custom-icon-upload-group');
    const fileInput = document.getElementById('edit-icon-file');
    const triggerBtn = document.getElementById('btn-trigger-icon-upload');
    const uploadStatus = document.getElementById('icon-upload-status');

    // Grid size and preset controls
    const applyGridSettingsBtn = document.getElementById('btn-apply-grid-settings');
    const deckRowsSelect = document.getElementById('deck-rows');
    const deckColsSelect = document.getElementById('deck-cols');
    const deckPresetSelect = document.getElementById('deck-preset');

    applyGridSettingsBtn.addEventListener('click', () => {
        if (!apiConfig) return;
        const rows = parseInt(deckRowsSelect.value);
        const cols = parseInt(deckColsSelect.value);
        const preset = deckPresetSelect.value;
        
        apiConfig.stream_deck_rows = rows;
        apiConfig.stream_deck_cols = cols;
        
        if (preset) {
            applyPreset(preset);
        } else {
            const expectedSize = rows * cols;
            if (apiConfig.stream_deck_buttons.length < expectedSize) {
                for (let i = apiConfig.stream_deck_buttons.length; i < expectedSize; i++) {
                    apiConfig.stream_deck_buttons.push({
                        id: i,
                        label: `Buton ${i + 1}`,
                        type: "hotkey",
                        value: "",
                        icon: ""
                    });
                }
            } else if (apiConfig.stream_deck_buttons.length > expectedSize) {
                apiConfig.stream_deck_buttons = apiConfig.stream_deck_buttons.slice(0, expectedSize);
            }
            renderStreamDeckButtons();
        }
        
        saveConfigOnServer();
    });

    editType.addEventListener('change', () => {
        const appGroup = document.getElementById('installed-apps-group');
        if (editType.value === 'hotkey') {
            editValueLabel.textContent = "Kısayol Tuşları (Tuşlara basarak otomatik kaydedin)";
            editValue.placeholder = "Kısayolu kaydetmek için klavyeden tuşlara basın...";
            appGroup.style.display = 'none';
        } else if (editType.value === 'toggle') {
            editValueLabel.textContent = "Toggle Eylemi veya Kısayol";
            editValue.placeholder = "mic_mute, speaker_mute, virtual_monitor veya ctrl+shift+m";
            appGroup.style.display = 'none';
        } else if (editType.value === 'volume_slider') {
            editValueLabel.textContent = "Ses Seviyesi Değeri (0 - 100)";
            editValue.placeholder = "master_volume, 0-100";
            appGroup.style.display = 'none';
        } else if (editType.value === 'brightness_slider') {
            editValueLabel.textContent = "Ekran Parlaklığı Değeri (0 - 100)";
            editValue.placeholder = "display_brightness, 0-100";
            appGroup.style.display = 'none';
        } else if (editType.value === 'live_info') {
            editValueLabel.textContent = "Canlı Donanım Türü";
            editValue.placeholder = "cpu, ram";
            appGroup.style.display = 'none';
        } else if (editType.value === 'clock_widget') {
            editValueLabel.textContent = "Saat & Zaman Widget'ı";
            editValue.placeholder = "digital_clock";
            appGroup.style.display = 'none';
        } else {
            editValueLabel.textContent = "Shell Komutu / Uygulama Adı (Örn. calc.exe)";
            editValue.placeholder = "Örn. calc.exe veya cmd /c start chrome";
            appGroup.style.display = 'block';
            loadInstalledApps();
        }
    });

    const appSearchInput = document.getElementById('app-search-input');
    const appSelect = document.getElementById('edit-app-select');
    
    appSearchInput.addEventListener('input', () => {
        populateAppSelect(appSearchInput.value);
    });
    
    appSelect.addEventListener('change', () => {
        if (!appSelect.value) return;
        document.getElementById('edit-value').value = appSelect.value;
        const selectedOption = appSelect.options[appSelect.selectedIndex];
        document.getElementById('edit-label').value = selectedOption.textContent;
        
        // Auto-select icon based on app name
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
        
        iconSelect.dispatchEvent(new Event('change'));
    });

    iconSelect.addEventListener('change', () => {
        if (iconSelect.value === 'custom') {
            customIconGroup.style.display = 'block';
            customIconUploadGroup.style.display = 'block';
        } else {
            customIconGroup.style.display = 'none';
            customIconUploadGroup.style.display = 'none';
        }
    });

    triggerBtn.addEventListener('click', (e) => {
        e.preventDefault();
        fileInput.click();
    });

    fileInput.addEventListener('change', () => {
        if (fileInput.files.length === 0) return;
        const file = fileInput.files[0];
        uploadStatus.textContent = "Yükleniyor...";
        
        const formData = new FormData();
        formData.append('button_id', editingButtonId);
        formData.append('file', file);
        
        authFetch('/upload_icon', {
            method: 'POST',
            body: formData
        })
        .then(res => res.json())
        .then(data => {
            if (data.success) {
                uploadStatus.textContent = file.name;
                document.getElementById('edit-icon-custom').value = data.icon.substring(7); // Remove custom: prefix for textbox representation
                alert("İkon başarıyla yüklendi!");
            } else {
                uploadStatus.textContent = "Hata";
                alert("Yükleme başarısız: " + data.error);
            }
        })
        .catch(err => {
            console.error(err);
            uploadStatus.textContent = "Hata";
            alert("İkon yükleme hatası oluştu.");
        });
    });

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

    closeModalBtn.addEventListener('click', () => modal.classList.remove('active'));
    applyEditBtn.addEventListener('click', applyButtonEdit);
    saveBtn.addEventListener('click', saveConfigOnServer);

    // Delete button inside modal
    const deleteBtn = document.getElementById('btn-delete-button');
    if (deleteBtn) {
        deleteBtn.addEventListener('click', () => {
            if (editingButtonId !== null && apiConfig && apiConfig.stream_deck_buttons) {
                apiConfig.stream_deck_buttons = apiConfig.stream_deck_buttons.filter(b => b.id !== editingButtonId);
                renderStreamDeckButtons();
                saveConfigOnServer();
                modal.classList.remove('active');
            }
        });
    }

    // Quick add button
    const quickAddBtn = document.getElementById('btn-add-widget-quick');
    if (quickAddBtn) {
        quickAddBtn.addEventListener('click', () => {
            if (!apiConfig) return;
            const newId = apiConfig.stream_deck_buttons.length > 0 
                ? Math.max(...apiConfig.stream_deck_buttons.map(b => b.id)) + 1 
                : 0;
            const activePg = apiConfig.active_page || 0;
            apiConfig.stream_deck_buttons.push({
                id: newId,
                label: "Yeni Widget",
                type: "hotkey",
                value: "",
                icon: "custom",
                page: activePg,
                col_span: 1,
                row_span: 1,
                color: "#818CF8",
                state: false
            });
            renderStreamDeckButtons();
            openEditButtonModal(newId);
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
                saveConfigOnServer();
            }
        });
    }
}

// Render dynamic page tabs
function renderPageTabs() {
    const tabsContainer = document.getElementById('deck-page-tabs');
    if (!tabsContainer || !apiConfig) return;

    tabsContainer.innerHTML = '<span style="font-size: 11px; font-weight: 700; color: var(--text-muted); margin-right: 4px; letter-spacing: 0.05em;">SAYFA:</span>';
    const pages = apiConfig.stream_deck_pages || [
        { id: 0, title: "Ana Sayfa" },
        { id: 1, title: "Medya & Ses" },
        { id: 2, title: "Sistem & PC" }
    ];

    pages.forEach(pg => {
        const isActive = (apiConfig.active_page || 0) === pg.id;
        const btn = document.createElement('button');
        btn.className = `deck-page-tab-btn ${isActive ? 'active' : ''}`;
        btn.style.cssText = `
            padding: 5px 12px;
            font-size: 11.5px;
            font-weight: ${isActive ? '700' : '500'};
            background: ${isActive ? '#818CF8' : 'rgba(255,255,255,0.05)'};
            color: ${isActive ? '#000000' : 'var(--text-main)'};
            border: 1px solid ${isActive ? '#818CF8' : 'var(--border-color)'};
            border-radius: 6px;
            cursor: pointer;
            transition: all 0.15s ease;
        `;
        btn.textContent = pg.title;
        btn.addEventListener('click', () => {
            apiConfig.active_page = pg.id;
            renderPageTabs();
            renderStreamDeckButtons();
        });
        tabsContainer.appendChild(btn);
    });

    // Populate edit modal page select
    const pageSelect = document.getElementById('edit-page');
    if (pageSelect) {
        pageSelect.innerHTML = '';
        pages.forEach(pg => {
            const opt = document.createElement('option');
            opt.value = pg.id.toString();
            opt.textContent = `${pg.title} (Sayfa ${pg.id + 1})`;
            pageSelect.appendChild(opt);
        });
    }
}

// Live hardware stats cache
let cachedLiveStats = { cpu: 12, ram: 42, cpu_temp: 45, ram_used_gb: 6.8, ram_total_gb: 16.0 };

function updateLiveStatsDOM() {
    // Dynamically update hardware widgets without recreating elements
    document.querySelectorAll('.deck-hardware-widget').forEach(widget => {
        const btnEl = widget.closest('.deck-btn');
        if (!btnEl) return;
        const btnId = parseInt(btnEl.getAttribute('data-id'));
        const btn = (apiConfig.stream_deck_buttons || []).find(b => b.id === btnId);
        if (!btn) return;

        const isCpu = (btn.value || '').includes('cpu');
        const pct = isCpu ? cachedLiveStats.cpu : cachedLiveStats.ram;
        const extra = isCpu ? `${cachedLiveStats.cpu_temp}°C` : `${cachedLiveStats.ram_used_gb}/${cachedLiveStats.ram_total_gb} GB`;
        const fillCol = pct > 80 ? '#EF4444' : pct > 50 ? '#F59E0B' : '#10B981';

        const pctEl = widget.querySelector('.deck-hardware-row span:last-child');
        if (pctEl) {
            pctEl.textContent = `%${pct}`;
            pctEl.style.color = fillCol;
        }
        const fillEl = widget.querySelector('.deck-hardware-gauge-fill');
        if (fillEl) {
            fillEl.style.width = `${pct}%`;
            fillEl.style.background = fillCol;
        }
        const extraEl = widget.querySelector('div:last-child');
        if (extraEl) extraEl.textContent = extra;
    });
}

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

// Render Stream Deck Interactive Grid with Drag & Drop and Resizing
function renderStreamDeckButtons() {
    const list = document.getElementById('deck-buttons-list');
    if (!list || !apiConfig) return;
    list.innerHTML = '';

    const cols = apiConfig.stream_deck_cols || 4;
    const rows = apiConfig.stream_deck_rows || 2;
    list.style.setProperty('--cols', cols);
    list.style.setProperty('--rows', rows);

    const dimEl = document.getElementById('deck-canvas-dimensions');
    if (dimEl) dimEl.textContent = `Matris: ${rows}x${cols}`;

    const activePage = apiConfig.active_page || 0;
    // Filter buttons belonging to active page
    const pageButtons = (apiConfig.stream_deck_buttons || []).filter(b => (b.page || 0) === activePage);

    pageButtons.forEach((btn, index) => {
        const item = document.createElement('div');
        item.className = 'deck-btn';
        item.draggable = true;
        item.setAttribute('data-id', btn.id);
        item.setAttribute('data-index', index);

        // Apply column and row spans
        const colSpan = btn.col_span || 1;
        const rowSpan = btn.row_span || 1;
        if (colSpan > 1) item.classList.add(`col-span-${colSpan}`);
        if (rowSpan > 1) item.classList.add(`row-span-${rowSpan}`);

        // Custom accent color border glow
        if (btn.color) {
            item.style.borderColor = btn.color + '44';
        }

        // Icon handling
        let displayIcon = iconMapUnicode[btn.icon] || "⚡";
        if (btn.icon && btn.icon.startsWith("custom:") && btn.icon.length > 7) {
            const path = btn.icon.substring(7);
            const password = getAuthPassword();
            const authQuery = password ? `?password=${encodeURIComponent(password)}` : '';
            displayIcon = `<img src="${path}${authQuery}" />`;
        }

        // Badge styling
        const badgeClass = btn.type || 'hotkey';
        let badgeLabel = 'KEY';
        if (btn.type === 'command') badgeLabel = 'CMD';
        else if (btn.type === 'toggle') badgeLabel = 'LED';
        else if (btn.type === 'volume_slider') badgeLabel = 'SES KAPSÜLÜ';
        else if (btn.type === 'brightness_slider') badgeLabel = 'PARLAKLIK';
        else if (btn.type === 'clock_widget') badgeLabel = 'OLED SAAT';
        else if (btn.type === 'live_info') badgeLabel = 'DONANIM';

        // Custom body preview based on widget type
        let bodyContent = '';
        if (btn.type === 'clock_widget') {
            const now = new Date();
            const timeStr = now.toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
            const dateStr = now.toLocaleDateString('tr-TR', { weekday: 'short', day: 'numeric', month: 'short' });
            bodyContent = `
                <div class="deck-clock-widget">
                    <div class="deck-clock-time" style="color: ${btn.color || '#FFFFFF'}">${timeStr}</div>
                    <div class="deck-clock-date">${dateStr}</div>
                </div>
            `;
        } else if (btn.type === 'volume_slider') {
            bodyContent = `
                <div class="apple-pill-slider-container">
                    <div class="apple-pill-header">
                        <span class="apple-pill-title">🔊 ${btn.label}</span>
                        <span class="apple-pill-value" style="color: #A5B4FC;">75%</span>
                    </div>
                    <div class="apple-pill-track" onclick="event.stopPropagation(); setMasterVolumeByPercent(75);">
                        <div class="apple-pill-fill accent-volume" style="width: 75%;">
                            <span style="font-size: 14px;">🔊</span>
                        </div>
                    </div>
                </div>
            `;
        } else if (btn.type === 'brightness_slider') {
            bodyContent = `
                <div class="apple-pill-slider-container">
                    <div class="apple-pill-header">
                        <span class="apple-pill-title">☀️ ${btn.label}</span>
                        <span class="apple-pill-value" style="color: #FDE68A;">85%</span>
                    </div>
                    <div class="apple-pill-track" onclick="event.stopPropagation(); setDisplayBrightness(85);">
                        <div class="apple-pill-fill accent-brightness" style="width: 85%;">
                            <span style="font-size: 14px;">☀️</span>
                        </div>
                    </div>
                </div>
            `;
        } else if (btn.type === 'live_info') {
            const isCpu = (btn.value || '').includes('cpu');
            const pct = isCpu ? cachedLiveStats.cpu : cachedLiveStats.ram;
            const extra = isCpu ? `${cachedLiveStats.cpu_temp}°C` : `${cachedLiveStats.ram_used_gb}/${cachedLiveStats.ram_total_gb} GB`;
            const fillCol = pct > 80 ? '#EF4444' : pct > 50 ? '#F59E0B' : '#10B981';
            bodyContent = `
                <div class="deck-hardware-widget">
                    <div class="deck-hardware-row">
                        <span style="font-size: 12px; font-weight: 700; color: #F8FAFC;">${btn.label}</span>
                        <span style="font-family: var(--font-mono); font-weight: 800; color: ${fillCol};">%${pct}</span>
                    </div>
                    <div class="deck-hardware-gauge">
                        <div class="deck-hardware-gauge-fill" style="width: ${pct}%; background: ${fillCol};"></div>
                    </div>
                    <div style="font-family: var(--font-mono); font-size: 10px; color: var(--text-muted); text-align: right;">${extra}</div>
                </div>
            `;
        } else {
            // Stream Deck Physical Bezel Button / Toggle
            const toggleIndicator = btn.type === 'toggle' 
                ? `<span class="deck-toggle-glow ${btn.state ? 'active' : 'inactive'}"></span>` 
                : '';
            bodyContent = `
                <div class="deck-btn-body">
                    <div class="deck-btn-icon-wrapper" style="border-color: ${btn.color ? btn.color + '44' : 'var(--border-color)'}; box-shadow: 0 4px 10px rgba(0,0,0,0.5);">${displayIcon}</div>
                    <div style="overflow: hidden; flex: 1;">
                        <div class="deck-btn-label" title="${btn.label}">${btn.label || 'İsimsiz Buton'}</div>
                        ${btn.type === 'toggle' ? `<div style="font-size: 10px; font-weight: 700; color: ${btn.state ? '#10B981' : '#94A3B8'}; display: flex; align-items: center; gap: 6px; margin-top: 3px;">${toggleIndicator} ${btn.state ? 'AÇIK' : 'KAPALI'}</div>` : ''}
                    </div>
                </div>
            `;
        }

        const actionDisplay = btn.value ? btn.value : '(Boş)';

        item.innerHTML = `
            <div class="deck-btn-header">
                <div style="display: flex; align-items: center; gap: 6px;">
                    <span class="deck-btn-num">#${btn.id + 1}</span>
                    <span class="deck-btn-badge ${badgeClass}">${badgeLabel}</span>
                </div>
                <div class="deck-btn-resize-pill" title="Boyut Değiştir (Hücre Genişliği)">
                    <button class="deck-size-btn ${colSpan === 1 && rowSpan === 1 ? 'active' : ''}" data-size="1x1">1x1</button>
                    <button class="deck-size-btn ${colSpan === 2 && rowSpan === 1 ? 'active' : ''}" data-size="2x1">2x1</button>
                    <button class="deck-size-btn ${colSpan === 1 && rowSpan === 2 ? 'active' : ''}" data-size="1x2">1x2</button>
                    <button class="deck-size-btn ${colSpan === 2 && rowSpan === 2 ? 'active' : ''}" data-size="2x2">2x2</button>
                </div>
            </div>
            ${bodyContent}
            <div class="deck-btn-footer">
                <span class="deck-btn-action" title="${actionDisplay}">${actionDisplay}</span>
                <button class="deck-btn-test" title="PC'de Canlı Tetikle">Test</button>
            </div>
        `;

        // Resize buttons handler
        item.querySelectorAll('.deck-size-btn').forEach(szBtn => {
            szBtn.addEventListener('click', (e) => {
                e.stopPropagation();
                const sz = szBtn.getAttribute('data-size');
                if (sz === '1x1') { btn.col_span = 1; btn.row_span = 1; }
                else if (sz === '2x1') { btn.col_span = 2; btn.row_span = 1; }
                else if (sz === '1x2') { btn.col_span = 1; btn.row_span = 2; }
                else if (sz === '2x2') { btn.col_span = 2; btn.row_span = 2; }
                renderStreamDeckButtons();
                saveConfigOnServer();
            });
        });

        // Test button handler
        const testBtn = item.querySelector('.deck-btn-test');
        testBtn.addEventListener('click', (e) => {
            e.stopPropagation();
            testButtonAction(btn.id, testBtn);
        });

        // Edit button click
        item.addEventListener('click', () => openEditButtonModal(btn.id));

        // Drag & Drop event handlers
        item.addEventListener('dragstart', (e) => {
            item.classList.add('dragging');
            e.dataTransfer.setData('text/plain', btn.id.toString());
            e.dataTransfer.effectAllowed = 'move';
        });

        item.addEventListener('dragend', () => {
            item.classList.remove('dragging');
            document.querySelectorAll('.deck-btn').forEach(el => el.classList.remove('drag-over'));
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
            const targetId = btn.id;
            if (draggedId !== targetId) {
                swapStreamDeckButtons(draggedId, targetId);
            }
        });

        list.appendChild(item);
    });

    setupRotaryDials();
}

// Interactive Elgato Stream Deck+ style rotary knob interaction
function setupRotaryDials() {
    const volDial = document.getElementById('rotary-volume-control');
    const brightDial = document.getElementById('rotary-bright-control');
    const micDial = document.getElementById('rotary-mic-control');

    if (volDial && !volDial.dataset.wired) {
        volDial.dataset.wired = "true";
        let curVol = 70;
        volDial.addEventListener('wheel', (e) => {
            e.preventDefault();
            curVol += e.deltaY < 0 ? 5 : -5;
            curVol = Math.max(0, Math.min(100, curVol));
            document.getElementById('rotary-volume-val').textContent = curVol + '%';
            const knob = volDial.querySelector('.deck-rotary-knob');
            if (knob) knob.style.transform = `rotate(${(curVol - 50) * 2.4}deg)`;
            setMasterVolumeByPercent(curVol);
        });
        volDial.addEventListener('click', () => {
            curVol = curVol === 0 ? 60 : 0;
            document.getElementById('rotary-volume-val').textContent = curVol + '%';
            setMasterVolumeByPercent(curVol);
        });
    }

    if (brightDial && !brightDial.dataset.wired) {
        brightDial.dataset.wired = "true";
        let curBright = 85;
        brightDial.addEventListener('wheel', (e) => {
            e.preventDefault();
            curBright += e.deltaY < 0 ? 5 : -5;
            curBright = Math.max(0, Math.min(100, curBright));
            document.getElementById('rotary-bright-val').textContent = curBright + '%';
            const knob = brightDial.querySelector('.deck-rotary-knob');
            if (knob) knob.style.transform = `rotate(${(curBright - 50) * 2.4}deg)`;
            setDisplayBrightness(curBright);
        });
    }

    if (micDial && !micDial.dataset.wired) {
        micDial.dataset.wired = "true";
        let micMuted = false;
        micDial.addEventListener('click', () => {
            micMuted = !micMuted;
            document.getElementById('rotary-mic-val').textContent = micMuted ? "SESSİZ" : "100%";
            document.getElementById('rotary-mic-val').style.color = micMuted ? "#EF4444" : "#10B981";
            if (wsClient && wsClient.readyState === WebSocket.OPEN) {
                wsClient.send(JSON.stringify({ type: 'stream_deck_press', button_id: 0 }));
            }
        });
    }
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

// Swap two buttons order in array
function swapStreamDeckButtons(idA, idB) {
    if (!apiConfig || !apiConfig.stream_deck_buttons) return;
    const idxA = apiConfig.stream_deck_buttons.findIndex(b => b.id === idA);
    const idxB = apiConfig.stream_deck_buttons.findIndex(b => b.id === idB);
    if (idxA !== -1 && idxB !== -1) {
        const temp = apiConfig.stream_deck_buttons[idxA];
        apiConfig.stream_deck_buttons[idxA] = apiConfig.stream_deck_buttons[idxB];
        apiConfig.stream_deck_buttons[idxB] = temp;
        renderStreamDeckButtons();
        saveConfigOnServer();
    }
}

function testButtonAction(buttonId, element) {
    if (element) {
        element.textContent = "⏳";
    }
    authFetch('/stream_deck_trigger', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ button_id: buttonId })
    })
    .then(res => res.json())
    .then(data => {
        if (element) {
            element.textContent = data.success ? "✓" : "✗";
            setTimeout(() => {
                element.textContent = "Test";
            }, 1200);
        }
    })
    .catch(err => {
        console.error("Test trigger error", err);
        if (element) {
            element.textContent = "✗";
            setTimeout(() => {
                element.textContent = "Test";
            }, 1200);
        }
    });
}

function openEditButtonModal(id) {
    const btn = apiConfig.stream_deck_buttons.find(b => b.id === id);
    if (!btn) return;

    editingButtonId = id;
    document.getElementById('edit-label').value = btn.label || "";
    document.getElementById('edit-type').value = btn.type || "hotkey";
    
    const colSelect = document.getElementById('edit-colspan');
    if (colSelect) colSelect.value = (btn.col_span || 1).toString();

    const rowSelect = document.getElementById('edit-rowspan');
    if (rowSelect) rowSelect.value = (btn.row_span || 1).toString();

    const colorInput = document.getElementById('edit-color');
    if (colorInput) colorInput.value = btn.color || "#818CF8";

    const pageSelect = document.getElementById('edit-page');
    if (pageSelect) {
        pageSelect.value = (btn.page !== undefined ? btn.page : 0).toString();
    }
    document.getElementById('edit-type').dispatchEvent(new Event('change'));

    const editValue = document.getElementById('edit-value');
    editValue.value = btn.value || "";

    const iconSelect = document.getElementById('edit-icon-select');
    const customIconGroup = document.getElementById('custom-icon-group');
    const customIconUploadGroup = document.getElementById('custom-icon-upload-group');
    const editIconCustom = document.getElementById('edit-icon-custom');
    const uploadStatus = document.getElementById('icon-upload-status');

    uploadStatus.textContent = "Seçilmedi";

    if (btn.icon && btn.icon.startsWith("custom:")) {
        iconSelect.value = "custom";
        customIconGroup.style.display = 'block';
        customIconUploadGroup.style.display = 'block';
        editIconCustom.value = btn.icon.substring(7);
    } else {
        iconSelect.value = btn.icon || "custom";
        customIconGroup.style.display = iconSelect.value === 'custom' ? 'block' : 'none';
        customIconUploadGroup.style.display = iconSelect.value === 'custom' ? 'block' : 'none';
        editIconCustom.value = "";
    }

    const editValueLabel = document.getElementById('edit-value-label');
    const appGroup = document.getElementById('installed-apps-group');
    document.getElementById('app-search-input').value = "";
    if (btn.type === 'hotkey') {
        editValueLabel.textContent = "Kısayol Tuşları (Tuşlara basarak otomatik kaydedin)";
        editValue.placeholder = "Kısayolu kaydetmek için klavyeden tuşlara basın...";
        appGroup.style.display = 'none';
    } else if (btn.type === 'toggle') {
        editValueLabel.textContent = "Toggle Eylemi veya Kısayol";
        editValue.placeholder = "mic_mute, speaker_mute, virtual_monitor veya ctrl+shift+m";
        appGroup.style.display = 'none';
    } else if (btn.type === 'volume_slider') {
        editValueLabel.textContent = "Ses Kontrol Değeri (0 - 100)";
        editValue.placeholder = "master_volume, 0-100";
        appGroup.style.display = 'none';
    } else if (btn.type === 'brightness_slider') {
        editValueLabel.textContent = "Parlaklık Kontrol Değeri (0 - 100)";
        editValue.placeholder = "display_brightness, 0-100";
        appGroup.style.display = 'none';
    } else if (btn.type === 'live_info') {
        editValueLabel.textContent = "Canlı Donanım Metriği";
        editValue.placeholder = "cpu, ram";
        appGroup.style.display = 'none';
    } else if (btn.type === 'clock_widget') {
        editValueLabel.textContent = "Saat Formatı";
        editValue.placeholder = "digital_clock, analog_clock";
        appGroup.style.display = 'none';
    } else {
        editValueLabel.textContent = "Shell Komutu / Uygulama Adı (Örn. calc.exe)";
        editValue.placeholder = "Örn. calc.exe veya cmd /c start chrome";
        appGroup.style.display = 'block';
        loadInstalledApps();
    }

    document.getElementById('edit-button-modal').classList.add('active');
}

function applyButtonEdit() {
    const label = document.getElementById('edit-label').value;
    const type = document.getElementById('edit-type').value;
    const value = document.getElementById('edit-value').value;
    const page = parseInt(document.getElementById('edit-page')?.value || "0");
    const colSpan = parseInt(document.getElementById('edit-colspan')?.value || "1");
    const rowSpan = parseInt(document.getElementById('edit-rowspan')?.value || "1");
    const color = document.getElementById('edit-color')?.value || "#818CF8";

    const iconSelect = document.getElementById('edit-icon-select').value;
    const editIconCustom = document.getElementById('edit-icon-custom').value;

    let icon = iconSelect;
    if (iconSelect === 'custom' && editIconCustom.trim().length > 0) {
        icon = "custom:" + editIconCustom.trim();
    }

    const saveAndClose = (finalIcon) => {
        const idx = apiConfig.stream_deck_buttons.findIndex(b => b.id === editingButtonId);
        if (idx !== -1) {
            apiConfig.stream_deck_buttons[idx] = {
                ...apiConfig.stream_deck_buttons[idx],
                id: editingButtonId,
                label,
                type,
                value,
                icon: finalIcon,
                page,
                col_span: colSpan,
                row_span: rowSpan,
                color
            };
            renderStreamDeckButtons();
            saveConfigOnServer();
        }
        document.getElementById('edit-button-modal').classList.remove('active');
    };

    const lowerVal = value.trim().toLowerCase();
    const isAppPath = lowerVal.endsWith('.exe') || lowerVal.endsWith('.lnk') || value.includes('\\') || value.includes('/');
    if (type === 'command' && value.trim().length > 0 && iconSelect !== 'custom' && isAppPath) {
        authFetch('/extract_icon', {
            method: 'POST',
            body: JSON.stringify({
                path: value.trim(),
                button_id: editingButtonId
            })
        })
        .then(res => res.json())
        .then(data => {
            if (data.success && data.icon) {
                saveAndClose("custom:" + data.icon);
            } else {
                saveAndClose(icon);
            }
        })
        .catch(err => {
            console.error("Icon extraction failed", err);
            saveAndClose(icon);
        });
    } else {
        saveAndClose(icon);
    }
}

function saveConfigOnServer() {
    authFetch('/config', {
        method: 'POST',
        body: JSON.stringify(apiConfig)
    })
    .then(res => res.json())
    .then(data => {
        if (data.success) {
            console.log("Configuration saved successfully.");
        } else {
            alert("Kaydetme hatası: " + data.error);
        }
    })
    .catch(err => console.error("Error saving config", err));
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
    const select = document.getElementById('edit-app-select');
    if (!select) return;
    select.innerHTML = '<option value="">-- Uygulama Seçin --</option>';
    
    const term = filterText.toLowerCase();
    const filtered = installedApps.filter(app => app.name.toLowerCase().includes(term));
    
    filtered.forEach(app => {
        const opt = document.createElement('option');
        opt.value = app.path;
        opt.textContent = app.name;
        select.appendChild(opt);
    });
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
        .bp-upd-overlay{position:fixed;inset:0;background:rgba(10,10,12,.6);backdrop-filter:blur(6px);display:flex;align-items:center;justify-content:center;z-index:9999;animation:bpFade .2s ease-out}
        .bp-upd-card{width:min(520px,92vw);max-height:80vh;display:flex;flex-direction:column;background:var(--bg-surface);border:1px solid var(--border-color);border-radius:18px;padding:24px;color:var(--text-main);box-shadow:0 20px 60px rgba(0,0,0,.35)}
        .bp-upd-badge{display:inline-block;font-size:12px;padding:4px 10px;border-radius:999px;background:rgba(165,166,246,.12);color:var(--accent-solid);margin-bottom:10px}
        .bp-upd-card h3{font-size:20px;font-weight:600;margin-bottom:4px}
        .bp-upd-ver{color:var(--text-muted);font-size:13px;margin-bottom:16px;font-family:var(--font-mono)}
        .bp-upd-notes{overflow:auto;background:var(--bg-elevated,#26262C);border-radius:12px;padding:14px 16px;font-size:14px;line-height:1.55;color:var(--text-main);flex:1}
        .bp-upd-notes h4{font-size:14px;margin:10px 0 4px}.bp-upd-notes ul{padding-left:18px;margin:4px 0}.bp-upd-notes p{margin:4px 0}
        .bp-upd-notes code{font-family:var(--font-mono);background:rgba(255,255,255,.06);padding:1px 5px;border-radius:5px}
        .bp-upd-notes a{color:var(--accent-solid)}
        .bp-upd-bar{height:6px;border-radius:999px;background:rgba(255,255,255,.06);margin-top:16px;overflow:hidden;display:none}
        .bp-upd-bar>div{height:100%;width:0;background:var(--accent-solid);transition:width .2s}
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
                    fill.style.width = (p.percent || 0) + '%';
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
            const info = await fetch('/api/update/check' + (manual ? '?force=1' : '')).then(r => r.json());
            const currentVerEl = document.getElementById('settings-current-ver');
            if (currentVerEl && info.current_version) {
                currentVerEl.textContent = 'v' + info.current_version;
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
                        updateStatusEl.textContent = `En güncel sürümü kullanıyorsunuz (v${info.current_version}) 🎉`;
                    }
                } else {
                    alert(info.error ? ('Güncelleme kontrol edilemedi: ' + info.error) : `En güncel sürümü kullanıyorsunuz (v${info.current_version}).`);
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
    });

    window.addEventListener('load', () => setTimeout(() => window.checkForUpdates(false), 1500));
})();
