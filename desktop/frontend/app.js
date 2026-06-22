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
    files: { title: "Dosya Transferi", subtitle: "Bilgisayar ile telefon arasında dosya alışverişi yapın." }
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
    const password = getAuthPassword();
    options.headers = options.headers || {};
    if (!(options.body instanceof FormData)) {
        if (!options.headers['Content-Type']) {
            options.headers['Content-Type'] = 'application/json';
        }
    }
    options.headers['X-Password'] = password;

    return fetch(url, options).then(res => {
        if (res.status === 401) {
            showPasswordPrompt();
            throw new Error("Unauthorized");
        }
        return res;
    });
}

function showPasswordPrompt() {
    const modal = document.getElementById('password-prompt-modal');
    modal.classList.add('active');
    document.getElementById('prompt-error-msg').style.display = 'none';
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
    
    // Fetch system info and configurations
    fetchSystemInfo();
    fetchConfig();
    fetchFiles();
    checkMicDriverStatus();
    connectWebSocket();
});

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
    media: [
        { label: "Önceki Şarkı", type: "hotkey", value: "prevtrack", icon: "play" },
        { label: "Oynat / Durdur", type: "hotkey", value: "playpause", icon: "play" },
        { label: "Sonraki Şarkı", type: "hotkey", value: "nexttrack", icon: "play" },
        { label: "Sesi Kapat", type: "hotkey", value: "mute", icon: "voldown" },
        { label: "Ses Azalt", type: "hotkey", value: "volumedown", icon: "voldown" },
        { label: "Ses Arttır", type: "hotkey", value: "volumeup", icon: "volup" },
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
}

function fetchConfig() {
    authFetch('/config')
        .then(res => res.json())
        .then(data => {
            if (data.success) {
                apiConfig = data.config;
                
                // Populate rows & cols selectors
                document.getElementById('deck-rows').value = apiConfig.stream_deck_rows || 2;
                document.getElementById('deck-cols').value = apiConfig.stream_deck_cols || 4;
                
                renderStreamDeckButtons();
                const settingsPwInput = document.getElementById('settings-password');
                if (settingsPwInput) {
                    settingsPwInput.value = apiConfig.password || "";
                }
            }
        })
        .catch(err => console.error("Error fetching config", err));
}

function renderStreamDeckButtons() {
    const list = document.getElementById('deck-buttons-list');
    list.innerHTML = '';
    
    // Dynamically set CSS variables for rows and columns
    list.style.setProperty('--cols', apiConfig.stream_deck_cols || 4);
    list.style.setProperty('--rows', apiConfig.stream_deck_rows || 2);
    
    apiConfig.stream_deck_buttons.forEach(btn => {
        const item = document.createElement('div');
        item.className = 'deck-btn';
        item.setAttribute('data-id', btn.id);
        
        let displayIcon = iconMapUnicode[btn.icon] || "⚙️";
        if (btn.icon && btn.icon.startsWith("custom:") && btn.icon.length > 7) {
            const path = btn.icon.substring(7);
            const password = getAuthPassword();
            const authQuery = password ? `?password=${encodeURIComponent(password)}` : '';
            displayIcon = `<img src="${path}${authQuery}" style="width: 20px; height: 20px; object-fit: contain; border-radius: 4px; vertical-align: middle;" />`;
        }
        
        item.innerHTML = `
            <div class="deck-btn-num">BUTON ${btn.id + 1}</div>
            <div class="deck-btn-label">${displayIcon} &nbsp;${btn.label}</div>
            <div class="deck-btn-action">${btn.type === 'hotkey' ? '⌨️ ' + btn.value : '🚀 ' + btn.value}</div>
        `;
        item.addEventListener('click', () => openEditButtonModal(btn.id));
        list.appendChild(item);
    });
}

function openEditButtonModal(id) {
    const btn = apiConfig.stream_deck_buttons.find(b => b.id === id);
    if (!btn) return;
    
    editingButtonId = id;
    document.getElementById('edit-label').value = btn.label;
    document.getElementById('edit-type').value = btn.type;
    
    const editValue = document.getElementById('edit-value');
    editValue.value = btn.value;
    
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
    
    const iconSelect = document.getElementById('edit-icon-select').value;
    const editIconCustom = document.getElementById('edit-icon-custom').value;
    
    let icon = iconSelect;
    if (iconSelect === 'custom' && editIconCustom.trim().length > 0) {
        icon = "custom:" + editIconCustom.trim();
    }
    
    const saveAndClose = (finalIcon) => {
        const idx = apiConfig.stream_deck_buttons.findIndex(b => b.id === editingButtonId);
        if (idx !== -1) {
            apiConfig.stream_deck_buttons[idx] = { id: editingButtonId, label, type, value, icon: finalIcon };
            renderStreamDeckButtons();
            saveConfigOnServer();
        }
        document.getElementById('edit-button-modal').classList.remove('active');
    };

    // If it's a command type and has a value, try to auto-extract the icon from the target EXE/LNK
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

    files.forEach(file => {
        const tr = document.createElement('tr');
        const sizeKB = (file.size / 1024).toFixed(1);
        const dateStr = new Date(file.modified * 1000).toLocaleString('tr-TR');
        
        tr.innerHTML = `
            <td><a href="/download/${encodeURIComponent(file.name)}${authQuery}" download>${file.name}</a></td>
            <td class="font-mono">${sizeKB} KB</td>
            <td>${dateStr}</td>
            <td style="text-align: right;"><button class="btn-danger" onclick="deleteFile('${encodeURIComponent(file.name)}')">Sil</button></td>
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
