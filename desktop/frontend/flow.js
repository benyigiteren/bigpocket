// ========================================================
// BIGPOCKET LOCAL FLOW (WhatsApp-Style Device Sharing)
// ========================================================

(function () {
    let flowMessages = [];
    let isFlowInitialized = false;

    function formatTime(timestamp) {
        if (!timestamp) return '';
        const d = new Date(timestamp);
        return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }

    function formatBytes(bytes) {
        if (!bytes || bytes <= 0) return '0 B';
        const k = 1024;
        const dm = 1;
        const sizes = ['B', 'KB', 'MB', 'GB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
    }

    function escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    function linkify(text) {
        const safe = escapeHtml(text);
        const urlRegex = /(https?:\/\/[^\s]+)/g;
        return safe.replace(urlRegex, url => `<a href="${url}" target="_blank" rel="noopener noreferrer" class="flow-link">${url}</a>`);
    }

    function getFileIcon(fileName) {
        const ext = (fileName || '').split('.').pop().toLowerCase();
        if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(ext)) {
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/></svg>`;
        }
        if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) {
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>`;
        }
        if (['mp4', 'mkv', 'mov', 'avi', 'webm'].includes(ext)) {
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/></svg>`;
        }
        if (['mp3', 'wav', 'flac', 'aac', 'ogg'].includes(ext)) {
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`;
        }
        if (['pdf'].includes(ext)) {
            return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/></svg>`;
        }
        return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="13 2 13 9 20 9"/></svg>`;
    }

    async function fetchFlowMessages() {
        try {
            const res = await (window.authFetch ? window.authFetch('/api/flow') : fetch('/api/flow'));
            const data = await res.json();
            if (data.success && Array.isArray(data.messages)) {
                flowMessages = data.messages;
                renderFlow();
            }
        } catch (e) {
            console.error('Flow fetch error:', e);
        }
    }

    function renderFlow() {
        const feed = document.getElementById('flow-chat-feed');
        if (!feed) return;

        if (flowMessages.length === 0) {
            feed.innerHTML = `
                <div class="flow-empty-state">
                    <div class="flow-empty-icon">
                        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
                    </div>
                    <h3>Akış boş</h3>
                    <p>Mesaj, bağlantı veya dosya paylaşın.</p>
                </div>
            `;
            return;
        }

        let html = '';
        flowMessages.forEach(msg => {
            const isPc = msg.sender === 'pc';
            const timeStr = formatTime(msg.Timestamp || msg.timestamp);
            const senderLabel = isPc ? 'Bilgisayarım' : 'Telefon';
            const senderIcon = isPc 
                ? `<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>`
                : `<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="5" y="2" width="14" height="20" rx="2" ry="2"/><line x1="12" y1="18" x2="12.01" y2="18"/></svg>`;

            let bodyHtml = '';
            if (msg.type === 'image' && (msg.file_url || msg.FileURL)) {
                const url = msg.file_url || msg.FileURL;
                const name = escapeHtml(msg.file_name || msg.FileName || 'Resim');
                const size = formatBytes(msg.file_size || msg.FileSize);
                bodyHtml = `
                    <div class="flow-bubble-media">
                        <a href="${url}" target="_blank" class="flow-media-preview-wrap">
                            <img src="${url}" alt="${name}" loading="lazy" class="flow-media-img" />
                        </a>
                        <div class="flow-media-meta">
                            <span class="flow-media-name">${name}</span>
                            <span class="flow-media-size">${size}</span>
                            <a href="${url}" download class="flow-action-btn" title="İndir">
                                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
                            </a>
                        </div>
                    </div>
                `;
            } else if (msg.type === 'file' && (msg.file_url || msg.FileURL)) {
                const url = msg.file_url || msg.FileURL;
                const name = escapeHtml(msg.file_name || msg.FileName || msg.text || msg.Text || 'Dosya');
                const size = formatBytes(msg.file_size || msg.FileSize);
                const icon = getFileIcon(name);
                bodyHtml = `
                    <div class="flow-bubble-file">
                        <div class="flow-file-icon">${icon}</div>
                        <div class="flow-file-info">
                            <span class="flow-file-name" title="${name}">${name}</span>
                            <span class="flow-file-size">${size}</span>
                        </div>
                        <a href="${url}" download class="btn btn-secondary flow-file-dl-btn" title="İndir">
                            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
                            <span>İndir</span>
                        </a>
                    </div>
                `;
            } else {
                // Text or Link
                const rawText = msg.text || msg.Text || '';
                const linkified = linkify(rawText);
                bodyHtml = `
                    <div class="flow-bubble-text">${linkified}</div>
                    <button type="button" class="flow-copy-btn" data-msg-id="${escapeHtml(msg.id || msg.ID)}" title="Metni Kopyala">
                        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                        <span>Kopyala</span>
                    </button>
                `;
            }

            html += `
                <div class="flow-msg-row ${isPc ? 'is-pc' : 'is-phone'}">
                    <div class="flow-msg-bubble">
                        <div class="flow-msg-header">
                            <span class="flow-sender-badge">${senderIcon} ${senderLabel}</span>
                            <div class="flow-header-actions">
                                <span class="flow-msg-time">${timeStr}</span>
                                <button type="button" class="flow-delete-btn" data-msg-id="${escapeHtml(msg.id || msg.ID)}" title="Mesajı Sil" aria-label="Mesajı Sil">
                                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                                </button>
                            </div>
                        </div>
                        <div class="flow-msg-content">${bodyHtml}</div>
                    </div>
                </div>
            `;
        });

        feed.innerHTML = html;
        feed.scrollTop = feed.scrollHeight;
    }

    async function sendText() {
        const input = document.getElementById('flow-input');
        if (!input) return;
        const text = input.value.trim();
        if (!text) return;

        input.value = '';
        input.style.height = 'auto';

        try {
            const res = await (window.authFetch ? window.authFetch('/api/flow', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ sender: 'pc', type: 'text', text })
            }) : fetch('/api/flow', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ sender: 'pc', type: 'text', text })
            }));
            const data = await res.json();
            if (data.success && data.message) {
                // Also trigger sync to phone clipboard
                if (window.sendWebSocketMessage) {
                    window.sendWebSocketMessage({ type: 'clipboard_sync', text });
                }
            }
        } catch (e) {
            console.error('Flow send text error:', e);
        }
    }

    async function sendFile(file) {
        if (!file) return;
        const formData = new FormData();
        formData.append('file', file);

        try {
            const res = await (window.authFetch ? window.authFetch('/api/flow/upload?sender=pc', {
                method: 'POST',
                body: formData
            }) : fetch('/api/flow/upload?sender=pc', {
                method: 'POST',
                body: formData
            }));
            const data = await res.json();
            if (data.success) {
                if (window.fetchFiles) window.fetchFiles();
            }
        } catch (e) {
            console.error('Flow upload error:', e);
        }
    }

    async function sendScreenshot() {
        try {
            const res = await (window.authFetch ? window.authFetch('/api/flow/screenshot', {
                method: 'POST'
            }) : fetch('/api/flow/screenshot', {
                method: 'POST'
            }));
            const data = await res.json();
            if (data.success && window.showToast) {
                window.showToast('Ekran görüntüsü gönderildi');
            }
        } catch (e) {
            console.error('Flow screenshot error:', e);
        }
    }

    async function sendClipboard() {
        try {
            const text = await navigator.clipboard.readText();
            if (text && text.trim()) {
                const input = document.getElementById('flow-input');
                if (input) input.value = text;
                sendText();
            } else if (window.showToast) {
                window.showToast('Pano boş');
            }
        } catch (e) {
            // Fallback: trigger backend clipboard poll
            if (window.sendWebSocketMessage) {
                window.sendWebSocketMessage({ type: 'get_clipboard' });
            }
        }
    }

    async function deleteMessage(id) {
        if (!id) return;
        try {
            const url = (window.apiUrl ? window.apiUrl(`/api/flow/${encodeURIComponent(id)}`) : `/api/flow/${encodeURIComponent(id)}`);
            await (window.authFetch ? window.authFetch(url, { method: 'DELETE' }) : fetch(url, { method: 'DELETE' }));
            if (window.sendWebSocketMessage) {
                window.sendWebSocketMessage({ type: 'flow_delete', id: id });
            }
            flowMessages = flowMessages.filter(m => (m.id || m.ID) !== id);
            renderFlow();
        } catch (e) {
            console.error('Delete flow msg error:', e);
        }
    }

    async function clearFlow() {
        if (!confirm('Tüm akış geçmişi silinsin mi?')) return;
        try {
            await (window.authFetch ? window.authFetch('/api/flow', { method: 'DELETE' }) : fetch('/api/flow', { method: 'DELETE' }));
            flowMessages = [];
            renderFlow();
        } catch (e) {
            console.error('Flow clear error:', e);
        }
    }

    function copyText(text) {
        navigator.clipboard.writeText(text).then(() => {
            if (window.showToast) window.showToast('Metin panoya kopyalandı');
        });
    }

    function setupDragDrop() {
        const card = document.getElementById('flow-chat-card');
        const overlay = document.getElementById('flow-drop-overlay');
        if (!card || !overlay) return;

        ['dragenter', 'dragover'].forEach(eventName => {
            card.addEventListener(eventName, (e) => {
                e.preventDefault();
                e.stopPropagation();
                overlay.classList.add('active');
            }, false);
        });

        ['dragleave', 'drop'].forEach(eventName => {
            overlay.addEventListener(eventName, (e) => {
                e.preventDefault();
                e.stopPropagation();
                if (eventName === 'dragleave' && e.relatedTarget && overlay.contains(e.relatedTarget)) {
                    return;
                }
                overlay.classList.remove('active');
            }, false);
        });

        overlay.addEventListener('drop', (e) => {
            const files = e.dataTransfer?.files;
            if (files && files.length > 0) {
                for (let i = 0; i < files.length; i++) {
                    sendFile(files[i]);
                }
            }
        });
    }

    function setFlowMode(mode) {
        const chatCard = document.getElementById('flow-chat-card');
        const classicView = document.getElementById('flow-classic-view');
        const btnChat = document.getElementById('btn-flow-mode-chat');
        const btnClassic = document.getElementById('btn-flow-mode-classic');

        if (!chatCard || !classicView) return;

        if (mode === 'classic') {
            chatCard.style.display = 'none';
            classicView.style.display = 'grid';
            if (btnChat) btnChat.classList.remove('active');
            if (btnClassic) btnClassic.classList.add('active');
            localStorage.setItem('bigpocket_flow_mode', 'classic');
            if (window.fetchFiles) window.fetchFiles();
        } else {
            chatCard.style.display = 'flex';
            classicView.style.display = 'none';
            if (btnChat) btnChat.classList.add('active');
            if (btnClassic) btnClassic.classList.remove('active');
            localStorage.setItem('bigpocket_flow_mode', 'chat');
            fetchFlowMessages();
        }
    }

    function init() {
        if (isFlowInitialized) return;
        isFlowInitialized = true;

        const btnChat = document.getElementById('btn-flow-mode-chat');
        const btnClassic = document.getElementById('btn-flow-mode-classic');
        if (btnChat) btnChat.addEventListener('click', () => setFlowMode('chat'));
        if (btnClassic) btnClassic.addEventListener('click', () => setFlowMode('classic'));

        const sendBtn = document.getElementById('btn-flow-send');
        const input = document.getElementById('flow-input');
        if (sendBtn) sendBtn.addEventListener('click', sendText);

        if (input) {
            input.addEventListener('keydown', (e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault();
                    sendText();
                }
            });
            input.addEventListener('input', () => {
                input.style.height = 'auto';
                input.style.height = Math.min(input.scrollHeight, 120) + 'px';
            });
        }

        const attachBtn = document.getElementById('btn-flow-attach');
        const fileInput = document.getElementById('flow-file-input');
        if (attachBtn && fileInput) {
            attachBtn.addEventListener('click', () => fileInput.click());
            fileInput.addEventListener('change', () => {
                if (fileInput.files && fileInput.files.length > 0) {
                    for (let i = 0; i < fileInput.files.length; i++) {
                        sendFile(fileInput.files[i]);
                    }
                    fileInput.value = '';
                }
            });
        }

        const snapBtn = document.getElementById('btn-flow-screenshot');
        if (snapBtn) snapBtn.addEventListener('click', sendScreenshot);

        const clipBtn = document.getElementById('btn-flow-clipboard');
        if (clipBtn) clipBtn.addEventListener('click', sendClipboard);

        const clearBtn = document.getElementById('btn-flow-clear');
        if (clearBtn) clearBtn.addEventListener('click', clearFlow);

        const folderBtn = document.getElementById('btn-flow-open-folder');
        if (folderBtn) {
            folderBtn.addEventListener('click', () => {
                const btn = document.getElementById('btn-open-folder');
                if (btn) btn.click();
            });
        }

        setupDragDrop();

        const feed = document.getElementById('flow-chat-feed');
        if (feed) {
            feed.addEventListener('click', (e) => {
                const copyBtn = e.target.closest('.flow-copy-btn');
                if (copyBtn) {
                    const id = copyBtn.dataset.msgId;
                    const found = flowMessages.find(m => (m.id || m.ID) === id);
                    if (found) copyText(found.text || found.Text || '');
                    return;
                }
                const delBtn = e.target.closest('.flow-delete-btn');
                if (delBtn) {
                    const id = delBtn.dataset.msgId;
                    deleteMessage(id);
                    return;
                }
            });
        }

        const savedMode = localStorage.getItem('bigpocket_flow_mode') || 'chat';
        setFlowMode(savedMode);
    }

    // Expose API
    window.bigpocketFlow = {
        init,
        fetchFlowMessages,
        copyText,
        handleIncomingMessage: function (msg) {
            flowMessages.push(msg);
            renderFlow();
        },
        handleClear: function () {
            flowMessages = [];
            renderFlow();
        },
        handleMessageDeleted: function (id) {
            flowMessages = flowMessages.filter(m => (m.id || m.ID) !== id);
            renderFlow();
        }
    };

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
