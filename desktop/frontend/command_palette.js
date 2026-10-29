/* =====================================================================
   BigPocket — Hızlı Komut Paleti (Command Palette)
   Raycast / Alfred / Spotlight tarzı klavye odaklı komut çalıştırıcı.
   Açılış: Ctrl+K (macOS: Cmd+K) veya kenar çubuğundaki büyüteç düğmesi.
   Gezinti: ↑ ↓ · Çalıştır: Enter · Kapat: Esc
   ===================================================================== */
(function () {
    'use strict';

    // ---------------------------------------------------------------- Utilities

    const TR_MAP = { 'ı': 'i', 'İ': 'i', 'ğ': 'g', 'ü': 'u', 'ş': 's', 'ö': 'o', 'ç': 'c', 'â': 'a', 'î': 'i', 'û': 'u' };

    // Case/diacritic insensitive normalisation so "gosterge" matches "Gösterge Paneli".
    function normalize(value) {
        return String(value || '')
            .toLocaleLowerCase('tr')
            .replace(/[ıİğüşöçâîû]/g, ch => TR_MAP[ch] || ch)
            .normalize('NFD')
            .replace(/[\u0300-\u036f]/g, '')
            .trim();
    }

    function escapeHtml(value) {
        return String(value == null ? '' : value)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    // ---------------------------------------------------------------- Icons

    const svg = (paths) => '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">' + paths + '</svg>';

    const ICONS = {
        dashboard: svg('<rect x="3" y="3" width="7" height="9"></rect><rect x="14" y="3" width="7" height="5"></rect><rect x="14" y="12" width="7" height="9"></rect><rect x="3" y="16" width="7" height="5"></rect>'),
        deck: svg('<rect x="3" y="3" width="18" height="18" rx="2"></rect><line x1="9" y1="3" x2="9" y2="21"></line><line x1="15" y1="3" x2="15" y2="21"></line><line x1="3" y1="9" x2="21" y2="9"></line><line x1="3" y1="15" x2="21" y2="15"></line>'),
        monitor: svg('<rect x="2" y="3" width="20" height="14" rx="2"></rect><line x1="8" y1="21" x2="16" y2="21"></line><line x1="12" y1="17" x2="12" y2="21"></line>'),
        camera: svg('<path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"></path><circle cx="12" cy="13" r="4"></circle>'),
        files: svg('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="16" y1="13" x2="8" y2="13"></line><line x1="16" y1="17" x2="8" y2="17"></line>'),
        settings: svg('<line x1="4" y1="21" x2="4" y2="14"></line><line x1="4" y1="10" x2="4" y2="3"></line><line x1="12" y1="21" x2="12" y2="12"></line><line x1="12" y1="8" x2="12" y2="3"></line><line x1="20" y1="21" x2="20" y2="16"></line><line x1="20" y1="12" x2="20" y2="3"></line><line x1="1" y1="14" x2="7" y2="14"></line><line x1="9" y1="8" x2="15" y2="8"></line><line x1="17" y1="16" x2="23" y2="16"></line>'),
        volumeUp: svg('<polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"></polygon><path d="M15.54 8.46a5 5 0 0 1 0 7.07"></path><path d="M19.07 4.93a10 10 0 0 1 0 14.14"></path>'),
        volumeDown: svg('<polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"></polygon><path d="M15.54 8.46a5 5 0 0 1 0 7.07"></path>'),
        mute: svg('<polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"></polygon><line x1="23" y1="9" x2="17" y2="15"></line><line x1="17" y1="9" x2="23" y2="15"></line>'),
        lock: svg('<rect x="3" y="11" width="18" height="11" rx="2"></rect><path d="M7 11V7a5 5 0 0 1 10 0v4"></path>'),
        display: svg('<rect x="2" y="3" width="20" height="14" rx="2"></rect><line x1="8" y1="21" x2="16" y2="21"></line><line x1="12" y1="17" x2="12" y2="21"></line><circle cx="12" cy="10" r="2"></circle>'),
        screenshot: svg('<path d="M3 7V5a2 2 0 0 1 2-2h2"></path><path d="M17 3h2a2 2 0 0 1 2 2v2"></path><path d="M21 17v2a2 2 0 0 1-2 2h-2"></path><path d="M7 21H5a2 2 0 0 1-2-2v-2"></path><line x1="3" y1="12" x2="21" y2="12"></line>'),
        clipboard: svg('<rect x="8" y="2" width="8" height="4" rx="1"></rect><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"></path><path d="M9 13l2 2 4-4"></path>'),
        activity: svg('<polyline points="22 12 18 12 15 21 9 3 6 12 2 12"></polyline>'),
        calculator: svg('<rect x="4" y="2" width="16" height="20" rx="2"></rect><line x1="8" y1="6" x2="16" y2="6"></line><line x1="8" y1="11" x2="8" y2="11"></line><line x1="12" y1="11" x2="12" y2="11"></line><line x1="16" y1="11" x2="16" y2="11"></line><line x1="8" y1="15" x2="8" y2="15"></line><line x1="12" y1="15" x2="12" y2="15"></line><line x1="16" y1="15" x2="16" y2="19"></line><line x1="8" y1="19" x2="12" y2="19"></line>'),
        notepad: svg('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="9" y1="13" x2="15" y2="13"></line><line x1="9" y1="17" x2="15" y2="17"></line>'),
        terminal: svg('<polyline points="4 17 10 11 4 5"></polyline><line x1="12" y1="19" x2="20" y2="19"></line>'),
        code: svg('<polyline points="16 18 22 12 16 6"></polyline><polyline points="8 6 2 12 8 18"></polyline>'),
        browser: svg('<circle cx="12" cy="12" r="9"></circle><line x1="3" y1="12" x2="21" y2="12"></line><path d="M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18z"></path>'),
        search: svg('<circle cx="11" cy="11" r="7"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line>')
    };

    // ---------------------------------------------------------------- Command registry

    const SECTIONS = [
        { id: 'tabs', label: 'Sekmeler' },
        { id: 'system', label: 'Hızlı Sistem Eylemleri' },
        { id: 'apps', label: 'Uygulamalar' }
    ];

    function openTab(tabId) {
        const button = document.querySelector('.nav-item[data-tab="' + tabId + '"]');
        if (button) button.click();
    }

    // Tek uç nokta: sunucu tarafında beyaz listeye alınmış sistem eylemi.
    function systemAction(action) {
        return apiRequest('palette/action', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action: action })
        }).then(res => res.json()).then(data => {
            if (data && data.success && action === 'monitor_toggle' && typeof window.checkDriverStatus === 'function') {
                window.checkDriverStatus();
            }
            return data;
        });
    }

    const COMMANDS = [
        // --- Sekmeler ---
        { section: 'tabs', label: 'Gösterge Paneli', hint: 'Sekme', icon: ICONS.dashboard, keywords: 'dashboard panel eşleştirme qr bağlantı ana', run: () => openTab('dashboard') },
        { section: 'tabs', label: 'Stream Deck', hint: 'Sekme', icon: ICONS.deck, keywords: 'deck buton tuş takımı düzen sayfa', run: () => openTab('streamdeck') },
        { section: 'tabs', label: '2. Monitör', hint: 'Sekme', icon: ICONS.monitor, keywords: 'ikinci monitör sanal ekran sürücü monitor', run: () => openTab('monitor') },
        { section: 'tabs', label: 'Kamera Akışı', hint: 'Sekme', icon: ICONS.camera, keywords: 'kamera webcam yayın görüntü akış', run: () => openTab('webcam') },
        { section: 'tabs', label: 'Dosya Transferi', hint: 'Sekme', icon: ICONS.files, keywords: 'dosya transfer paylaşım yükle indir pano', run: () => openTab('files') },
        { section: 'tabs', label: 'Ayarlar', hint: 'Sekme', icon: ICONS.settings, keywords: 'settings ayar tercih güncelleme sürüm başlangıç', run: () => openTab('settings') },

        // --- Hızlı Sistem Eylemleri ---
        { section: 'system', label: 'Sesi Aç', hint: 'Sistem', icon: ICONS.volumeUp, keywords: 'ses aç volume up yükselt artır', run: () => systemAction('volume_up') },
        { section: 'system', label: 'Sesi Kıs', hint: 'Sistem', icon: ICONS.volumeDown, keywords: 'ses kıs volume down azalt düşür', run: () => systemAction('volume_down') },
        { section: 'system', label: 'Sesi Kapat / Aç', hint: 'Sistem', icon: ICONS.mute, keywords: 'ses kapat aç mute sustur sessiz', run: () => systemAction('volume_mute') },
        { section: 'system', label: 'Ekranı Kilitle', hint: 'Sistem', icon: ICONS.lock, keywords: 'ekran kilitle kilit pc lock oturum güvenlik', run: () => systemAction('lock_screen') },
        { section: 'system', label: 'Monitörü Aç / Kapat', hint: 'Sistem', icon: ICONS.display, keywords: 'monitör aç kapat sanal ekran monitor ekranı kapat', run: () => systemAction('monitor_toggle') },
        { section: 'system', label: 'Ekran Görüntüsü Al', hint: 'PNG', icon: ICONS.screenshot, keywords: 'ekran görüntüsü al screenshot resim foto yakala snapshot', run: () => systemAction('screenshot') },
        { section: 'system', label: 'Panoyu Eşitle', hint: 'Pano', icon: ICONS.clipboard, keywords: 'pano eşitle clipboard sync kopyala metin telefon', run: () => systemAction('clipboard_sync') },

        // --- Uygulamalar ---
        { section: 'apps', label: 'Görev Yöneticisi', hint: 'Uygulama', icon: ICONS.activity, keywords: 'görev yöneticisi task manager taskmgr süreç işlem cpu', run: () => systemAction('app_taskmgr') },
        { section: 'apps', label: 'Hesap Makinesi', hint: 'Uygulama', icon: ICONS.calculator, keywords: 'hesap makinesi calculator calc matematik', run: () => systemAction('app_calc') },
        { section: 'apps', label: 'Not Defteri', hint: 'Uygulama', icon: ICONS.notepad, keywords: 'not defteri notepad metin editör yazı', run: () => systemAction('app_notepad') },
        { section: 'apps', label: 'Komut İstemi / Terminal', hint: 'Uygulama', icon: ICONS.terminal, keywords: 'komut istemi terminal cmd powershell wt shell konsol', run: () => systemAction('app_terminal') },
        { section: 'apps', label: 'VS Code', hint: 'Uygulama', icon: ICONS.code, keywords: 'vs code visual studio code editör kod geliştirici', run: () => systemAction('app_vscode') },
        { section: 'apps', label: 'Tarayıcı', hint: 'Uygulama', icon: ICONS.browser, keywords: 'tarayıcı browser chrome edge firefox internet web', run: () => systemAction('app_browser') }
    ];

    // Pre-compute the search haystack once per command.
    COMMANDS.forEach(cmd => {
        cmd._haystack = normalize(cmd.label + ' ' + (cmd.keywords || '') + ' ' + (cmd.hint || ''));
        cmd._label = normalize(cmd.label);
    });

    // ---------------------------------------------------------------- DOM refs

    let overlay = null;
    let input = null;
    let listEl = null;
    let emptyEl = null;
    let countEl = null;
    let toastHost = null;
    let lastFocused = null;
    let isOpen = false;
    let visible = [];        // currently rendered commands (flat, in DOM order)
    let activeIndex = 0;

    // ---------------------------------------------------------------- Auth / fetch

    function apiRequest(path, options) {
        const url = path.charAt(0) === '/' ? path : '/' + path;
        if (typeof window.authFetch === 'function') {
            return window.authFetch(url, options).then(res => {
                if (!res.ok && res.status !== 401) throw new Error('HTTP ' + res.status);
                return res;
            });
        }
        const headers = Object.assign({}, (options && options.headers) || {});
        const password = typeof window.getAuthPassword === 'function' ? window.getAuthPassword() : '';
        if (password) headers['X-Password'] = password;
        return fetch(url, Object.assign({}, options, { headers: headers })).then(res => {
            if (!res.ok) throw new Error('HTTP ' + res.status);
            return res;
        });
    }

    // ---------------------------------------------------------------- Toasts

    function ensureToastHost() {
        if (toastHost && document.body.contains(toastHost)) return toastHost;
        toastHost = document.getElementById('cmdk-toast-host');
        if (!toastHost) {
            toastHost = document.createElement('div');
            toastHost.id = 'cmdk-toast-host';
            toastHost.className = 'cmdk-toast-host';
            document.body.appendChild(toastHost);
        }
        return toastHost;
    }

    function toast(message, kind) {
        if (!message) return;
        const host = ensureToastHost();
        const el = document.createElement('div');
        el.className = 'cmdk-toast' + (kind === 'error' ? ' is-error' : kind === 'success' ? ' is-success' : '');
        el.innerHTML = '<span class="cmdk-toast-dot"></span><span class="cmdk-toast-text">' + escapeHtml(message) + '</span>';
        host.appendChild(el);
        setTimeout(() => {
            el.classList.add('is-leaving');
            setTimeout(() => { if (el.parentNode) el.parentNode.removeChild(el); }, 220);
        }, 3200);
    }

    function fileName(path) {
        if (!path) return '';
        const parts = String(path).split(/[\\/]/);
        return parts[parts.length - 1] || path;
    }

    // ---------------------------------------------------------------- Filtering

    function search(query) {
        const tokens = normalize(query).split(/\s+/).filter(Boolean);
        if (!tokens.length) return COMMANDS.slice();

        const scored = [];
        COMMANDS.forEach(cmd => {
            let score = 0;
            let firstHit = -1;
            for (let i = 0; i < tokens.length; i++) {
                const idx = cmd._haystack.indexOf(tokens[i]);
                if (idx === -1) return;                 // every token must match
                if (cmd._label.indexOf(tokens[i]) === 0) score += 40;
                else if (cmd._label.indexOf(tokens[i]) !== -1) score += 24;
                else score += 10;
                if (firstHit === -1 || idx < firstHit) firstHit = idx;
            }
            score += Math.max(0, 30 - cmd.label.length);
            score += Math.max(0, 24 - firstHit);
            scored.push({ cmd: cmd, score: score });
        });

        scored.sort((a, b) => b.score - a.score);
        return scored.map(entry => entry.cmd);
    }

    // ---------------------------------------------------------------- Rendering

    function render(query) {
        const matches = search(query);

        // DOM sırası (bölüm başlıklarına göre gruplanmış) ile klavye indeksinin
        // birebir aynı olması şart: `visible` listesi tam olarak ekrandaki sıradır.
        visible = [];
        SECTIONS.forEach(section => {
            matches.filter(cmd => cmd.section === section.id).forEach(cmd => visible.push(cmd));
        });
        activeIndex = 0;

        if (!visible.length) {
            listEl.innerHTML = '';
            emptyEl.hidden = false;
            input.setAttribute('aria-activedescendant', '');
            return;
        }
        emptyEl.hidden = true;

        let html = '';
        let flatIndex = -1;
        SECTIONS.forEach(section => {
            const items = visible.filter(cmd => cmd.section === section.id);
            if (!items.length) return;
            html += '<div class="cmdk-section-label">' + escapeHtml(section.label) + '</div>';
            items.forEach(cmd => {
                flatIndex++;
                html += '<button type="button" class="cmdk-item" role="option" id="cmdk-item-' + flatIndex + '" data-index="' + flatIndex + '" aria-selected="false">' +
                    '<span class="cmdk-item-icon">' + cmd.icon + '</span>' +
                    '<span class="cmdk-item-label">' + escapeHtml(cmd.label) + '</span>' +
                    '<span class="cmdk-item-hint">' + escapeHtml(cmd.hint || '') + '</span>' +
                    '</button>';
            });
        });
        listEl.innerHTML = html;
        setActive(0);
    }

    function itemNodes() {
        return listEl.querySelectorAll('.cmdk-item');
    }

    function setActive(index, scroll) {
        const nodes = itemNodes();
        if (!nodes.length) return;
        activeIndex = Math.max(0, Math.min(index, nodes.length - 1));
        for (let i = 0; i < nodes.length; i++) {
            const isActive = i === activeIndex;
            nodes[i].classList.toggle('is-active', isActive);
            nodes[i].setAttribute('aria-selected', isActive ? 'true' : 'false');
        }
        const node = nodes[activeIndex];
        if (node) {
            input.setAttribute('aria-activedescendant', node.id);
            if (scroll !== false) node.scrollIntoView({ block: 'nearest' });
        }
    }

    function move(delta) {
        const nodes = itemNodes();
        if (!nodes.length) return;
        const next = (activeIndex + delta + nodes.length) % nodes.length;
        setActive(next);
    }

    function updateCount() {
        if (!countEl) return;
        countEl.textContent = visible.length ? visible.length + ' komut' : '';
    }

    // ---------------------------------------------------------------- Execution

    function execute(index) {
        const cmd = visible[index];
        if (!cmd) return;
        close();

        let result;
        try {
            result = cmd.run();
        } catch (err) {
            toast('Komut çalıştırılamadı: ' + err.message, 'error');
            return;
        }

        if (!result || typeof result.then !== 'function') return;    // sekmeler: sessiz geçiş

        result.then(data => {
            if (!data) return;
            if (!data.success) {
                toast(data.message || 'Komut başarısız oldu.', 'error');
                return;
            }
            let message = data.message || 'Tamamlandı.';
            if (data.path) message += '  ·  ' + fileName(data.path);
            toast(message, 'success');
        }).catch(err => {
            toast('Sunucuya ulaşılamadı: ' + err.message, 'error');
        });
    }

    // ---------------------------------------------------------------- Open / close

    function open() {
        if (isOpen) return;
        if (!overlay) build();
        isOpen = true;
        lastFocused = document.activeElement;
        input.value = '';
        render('');
        updateCount();
        overlay.classList.add('is-open');
        overlay.setAttribute('aria-hidden', 'false');
        input.setAttribute('aria-expanded', 'true');
        window.requestAnimationFrame(() => {
            input.focus();
            input.select();
        });
    }

    function close() {
        if (!isOpen) return;
        isOpen = false;
        overlay.classList.remove('is-open');
        overlay.setAttribute('aria-hidden', 'true');
        input.setAttribute('aria-expanded', 'false');
        if (lastFocused && typeof lastFocused.focus === 'function') {
            try { lastFocused.focus(); } catch (err) { /* odak geri alınamadı */ }
        }
    }

    function toggle() {
        if (isOpen) close();
        else open();
    }

    // ---------------------------------------------------------------- Markup

    function build() {
        overlay = document.getElementById('cmdk-overlay');
        if (!overlay) {
            // Markup yoksa (örn. eski bir index.html) palet kendini gizli olarak kurar.
            overlay = document.createElement('div');
            overlay.id = 'cmdk-overlay';
            overlay.className = 'cmdk-overlay';
            overlay.setAttribute('role', 'presentation');
            overlay.innerHTML = [
                '<div class="cmdk-panel" role="dialog" aria-modal="true" aria-label="Hızlı Komut Paleti">',
                '  <div class="cmdk-search">',
                '    <span class="cmdk-search-icon">' + ICONS.search + '</span>',
                '    <input id="cmdk-input" type="text" role="combobox" aria-expanded="true" aria-controls="cmdk-list" aria-autocomplete="list" autocomplete="off" spellcheck="false" placeholder="Komut yazın veya arayın…">',
                '    <span class="cmdk-search-count" id="cmdk-count"></span>',
                '  </div>',
                '  <div class="cmdk-list" id="cmdk-list" role="listbox" aria-label="Komutlar"></div>',
                '  <div class="cmdk-empty" id="cmdk-empty" hidden>Sonuç bulunamadı</div>',
                '  <div class="cmdk-footer">',
                '    <span class="cmdk-legend"><kbd>↑</kbd><kbd>↓</kbd> gezinti</span>',
                '    <span class="cmdk-legend"><kbd>↵</kbd> çalıştır</span>',
                '    <span class="cmdk-legend"><kbd>esc</kbd> kapat</span>',
                '  </div>',
                '</div>'
            ].join('');
            document.body.appendChild(overlay);
        }

        input = overlay.querySelector('#cmdk-input');
        listEl = overlay.querySelector('#cmdk-list');
        emptyEl = overlay.querySelector('#cmdk-empty');
        countEl = overlay.querySelector('#cmdk-count');

        // Filtreleme: anında
        input.addEventListener('input', () => {
            render(input.value);
            updateCount();
        });

        // Fare: üzerine gelinen satırı seç
        listEl.addEventListener('pointermove', event => {
            const item = event.target.closest ? event.target.closest('.cmdk-item') : null;
            if (!item) return;
            const index = parseInt(item.getAttribute('data-index'), 10);
            if (!isNaN(index) && index !== activeIndex) setActive(index, false);
        });

        // Fare: tıkla ve çalıştır
        listEl.addEventListener('click', event => {
            const item = event.target.closest ? event.target.closest('.cmdk-item') : null;
            if (!item) return;
            execute(parseInt(item.getAttribute('data-index'), 10));
        });

        // Arka plana tıklayınca kapat
        overlay.addEventListener('mousedown', event => {
            if (event.target === overlay) close();
        });
    }

    // ---------------------------------------------------------------- Global keyboard

    // Bir modal (örn. kart düzenleyici kısayol kaydı) açıkken Ctrl+K o modalın
    // akışını bozmasın: kullanıcı orada özellikle Ctrl+K kombinasyonunu kaydediyor olabilir.
    function insideActiveModal(node) {
        return !!(node && node.closest && node.closest('.modal.active'));
    }

    document.addEventListener('keydown', event => {
        // Ctrl+K / Cmd+K — paleti aç / kapat (metin alanlarında da çalışır)
        if ((event.ctrlKey || event.metaKey) && !event.altKey && (event.key === 'k' || event.key === 'K' || event.code === 'KeyK')) {
            if (!isOpen && insideActiveModal(event.target)) return;
            event.preventDefault();
            toggle();
            return;
        }

        if (!isOpen) return;

        switch (event.key) {
            case 'Escape':
                event.preventDefault();
                close();
                break;
            case 'ArrowDown':
                event.preventDefault();
                move(1);
                break;
            case 'ArrowUp':
                event.preventDefault();
                move(-1);
                break;
            case 'PageDown':
                event.preventDefault();
                move(5);
                break;
            case 'PageUp':
                event.preventDefault();
                move(-5);
                break;
            case 'Home':
                event.preventDefault();
                setActive(0);
                break;
            case 'End':
                event.preventDefault();
                setActive(itemNodes().length - 1);
                break;
            case 'Enter':
                event.preventDefault();
                execute(activeIndex);
                break;
            case 'n':
                if (event.ctrlKey) { event.preventDefault(); move(1); }
                break;
            case 'p':
                if (event.ctrlKey) { event.preventDefault(); move(-1); }
                break;
            case 'Tab':
                event.preventDefault();
                move(event.shiftKey ? -1 : 1);
                break;
            default:
                break;
        }
    }, true);

    // ---------------------------------------------------------------- Sidebar trigger + init

    function init() {
        build();

        const trigger = document.getElementById('cmdk-trigger');
        if (trigger) {
            trigger.addEventListener('click', event => {
                event.preventDefault();
                open();
            });
        }

        // Programatik erişim (örn. başka bir düğmeden açmak için).
        window.bigpocketCommandPalette = {
            open: open,
            close: close,
            toggle: toggle,
            isOpen: () => isOpen
        };
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
