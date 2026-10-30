//! BigPocket desktop HTTP + WebSocket server (axum).
//!
//! Ported from `desktop/main.go` (`startWebSocketServer`, `handleWebSocketControl`,
//! `setupHttpRoutes`, `system_info`), `desktop/security.go` (origin guard,
//! constant-time password check with brute-force throttling, safe path
//! resolution, authorized-IP registry) and `desktop/flow.go` (the `/api/flow*`
//! routes). `desktop/updater.go`'s `/api/update/check` is implemented here too.
//!
//! Everything the server needs lives in [`AppState`]; [`start_server`] binds
//! `0.0.0.0:port` and serves until the returned handle is aborted or
//! [`AppState::shutdown`] is signalled.

use std::collections::HashMap;
use std::net::{IpAddr, SocketAddr};
use std::path::{Component, Path as FsPath, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicU32, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex as StdMutex};
use std::time::{Duration, Instant};

use axum::body::{Body, Bytes};
use axum::extract::ws::{Message, WebSocket, WebSocketUpgrade};
use axum::extract::{ConnectInfo, Multipart, Path, Query, Request, State};
use axum::http::{header, HeaderMap, HeaderValue, Method, StatusCode, Uri};
use axum::middleware::{self, Next};
use axum::response::{IntoResponse, Response};
use axum::routing::{delete, get, post};
use axum::{Json, Router};
use futures_util::{SinkExt, StreamExt};
use serde_json::{json, Map, Value};
use tokio::sync::{broadcast, mpsc, Mutex as AsyncMutex, Notify};
use tower_http::services::{ServeDir, ServeFile};

use crate::config::{self, Config};
use crate::flow::{now_millis, FlowManager, FlowMessage};
use crate::system::{Button, InputController, SystemMonitor};

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/// Control HTTP + WebSocket port.
pub const CONTROL_PORT: u16 = 8085;
/// Raw TCP port for the camera stream.
pub const CAMERA_TCP_PORT: u16 = 8083;
/// Raw TCP port for the audio stream.
pub const AUDIO_TCP_PORT: u16 = 8084;
/// Raw TCP port for the screen stream.
pub const SCREEN_TCP_PORT: u16 = 8086;

/// Version reported by the update checker (mirrors Go's `AppVersion`).
pub const APP_VERSION: &str = "v1.5.0-beta";
/// Public repository that publishes releases.
pub const GITHUB_REPO: &str = "benyigiteren/bigpocket";

/// Password attempts before an IP is temporarily blocked.
const AUTH_MAX_FAILURES: u32 = 5;
/// Window in which failures are counted.
const AUTH_FAIL_WINDOW: Duration = Duration::from_secs(5 * 60);
/// How long an IP stays blocked after too many failures.
const AUTH_BLOCK_DURATION: Duration = Duration::from_secs(5 * 60);
/// How long an IP stays trusted for the raw media TCP ports.
const AUTHORIZED_IP_TTL: Duration = Duration::from_secs(12 * 60 * 60);
/// How long an unauthenticated WebSocket may stay open.
const WS_AUTH_TIMEOUT: Duration = Duration::from_secs(5);

const POMODORO_WORK_MINUTES: u64 = 25;
const POMODORO_BREAK_MINUTES: u64 = 5;

/// Extensions treated as images when building Flow messages.
const IMAGE_EXTENSIONS: [&str; 8] = [".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".ico"];

/// Read buffer used while streaming shared files to/from disk.
const STREAM_CHUNK: usize = 64 * 1024;

/// `SharedFiles/` — the folder every download/upload and the Flow history use.
pub fn get_shared_dir() -> PathBuf {
    let dir = config::get_app_data_dir().join("SharedFiles");
    let _ = std::fs::create_dir_all(&dir);
    dir
}

fn is_image_extension(name: &str) -> bool {
    let lower = name.to_ascii_lowercase();
    IMAGE_EXTENSIONS.iter().any(|ext| lower.ends_with(ext))
}

/// Directory holding the web UI, resolved the same way Go's `resolveFrontendDir` did.
pub fn frontend_dir() -> PathBuf {
    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|p| p.to_path_buf()))
        .unwrap_or_else(|| PathBuf::from("."));

    for candidate in [
        exe_dir.join("frontend"),
        exe_dir.join("desktop").join("frontend"),
        PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("..").join("frontend"),
    ] {
        if candidate.is_dir() {
            return candidate;
        }
    }
    exe_dir.join("frontend")
}

// ---------------------------------------------------------------------------
// Path safety (path traversal / Zip Slip)
// ---------------------------------------------------------------------------

/// Resolve `name` inside `base_dir`, refusing anything that would escape it.
pub fn safe_join_path(base_dir: &FsPath, name: &str) -> Result<PathBuf, String> {
    let name = name.trim();
    if name.is_empty() {
        return Err("empty file name".into());
    }
    if name.contains('\0') {
        return Err("invalid file name".into());
    }
    // Normalise Windows separators, then keep only the final component — this is
    // what Go's `filepath.Base(filepath.FromSlash(...))` did.
    let normalised = name.replace('\\', "/");
    let last = normalised.rsplit('/').next().unwrap_or("");
    let clean = PathBuf::from(last);
    if last.is_empty() || last == "." || last == ".." {
        return Err("invalid file name".into());
    }
    if clean
        .components()
        .any(|c| !matches!(c, Component::Normal(_)))
    {
        return Err("invalid file name".into());
    }

    let base_abs = std::fs::canonicalize(base_dir).unwrap_or_else(|_| base_dir.to_path_buf());
    let full = base_abs.join(&clean);
    if !full.starts_with(&base_abs) {
        return Err("path escapes base directory".into());
    }
    Ok(full)
}

/// Resolve a requested shared-file name inside `SharedFiles/`.
pub fn safe_shared_file_path(filename: &str) -> Result<PathBuf, String> {
    let name = filename.trim_start_matches('/');
    safe_join_path(&get_shared_dir(), name)
}

// ---------------------------------------------------------------------------
// Origin / host validation
// ---------------------------------------------------------------------------

/// The machine's own IPv4 addresses, briefly cached (mirrors `localInterfaceIPs`).
fn own_interface_ips() -> Vec<String> {
    static CACHE: StdMutex<Option<(Instant, Vec<String>)>> = StdMutex::new(None);
    if let Ok(guard) = CACHE.lock() {
        if let Some((at, ips)) = guard.as_ref() {
            if at.elapsed() < Duration::from_secs(30) {
                return ips.clone();
            }
        }
    }

    let mut ips = Vec::new();
    if let Ok(interfaces) = local_ip_address::list_afinet_netifas() {
        for (_name, ip) in interfaces {
            if let IpAddr::V4(v4) = ip {
                if !v4.is_loopback() {
                    ips.push(v4.to_string());
                }
            }
        }
    }
    ips.sort();
    ips.dedup();

    if let Ok(mut guard) = CACHE.lock() {
        *guard = Some((Instant::now(), ips.clone()));
    }
    ips
}

fn is_loopback_host(host: &str) -> bool {
    if host.is_empty() {
        return false;
    }
    let trimmed = host.trim_matches(|c| c == '[' || c == ']');
    // `localhost` plus the host Tauri v2 injects for its custom protocol
    // (Windows: http://tauri.localhost, macOS/Linux: tauri://localhost).
    if trimmed.eq_ignore_ascii_case("localhost") || trimmed.eq_ignore_ascii_case("tauri.localhost") {
        return true;
    }
    trimmed
        .parse::<IpAddr>()
        .map(|ip| ip.is_loopback())
        .unwrap_or(false)
}

fn is_own_interface_ip(host: &str) -> bool {
    own_interface_ips().iter().any(|ip| ip == host)
}

/// Reject cross-site browser requests; native clients send no `Origin` at all.
pub fn is_allowed_origin(headers: &HeaderMap) -> bool {
    let origin = headers
        .get(header::ORIGIN)
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .unwrap_or("");
    if origin.is_empty() || origin == "null" {
        return true;
    }
    // Tauri custom-protocol origin (macOS/Linux webviews). Windows serves the
    // panel from http(s)://tauri.localhost, which `is_loopback_host` covers.
    let lowered = origin.to_ascii_lowercase();
    if lowered == "tauri://localhost" || lowered == "tauri://localhost/" {
        return true;
    }
    let Some(rest) = origin
        .strip_prefix("http://")
        .or_else(|| origin.strip_prefix("https://"))
    else {
        return false;
    };
    // Split authority from any trailing path, handling bracketed IPv6 hosts.
    let authority = rest.split(['/', '?', '#']).next().unwrap_or("");
    let (host, port) = if let Some(end) = authority.rfind(']') {
        let host = &authority[..=end];
        let port = authority[end + 1..].strip_prefix(':');
        (host, port)
    } else {
        match authority.rsplit_once(':') {
            Some((h, p)) => (h, Some(p)),
            None => (authority, None),
        }
    };
    if host.is_empty() {
        return false;
    }
    if let Some(port) = port {
        if !port.is_empty() && port != CONTROL_PORT.to_string() {
            return false;
        }
    }
    let bare = host.trim_matches(|c| c == '[' || c == ']');
    is_loopback_host(bare) || is_own_interface_ip(bare)
}

// ---------------------------------------------------------------------------
// Authentication (constant-time password + brute-force throttling)
// ---------------------------------------------------------------------------

#[derive(Default)]
struct AuthFailEntry {
    count: u32,
    first_failure: Option<Instant>,
    blocked_until: Option<Instant>,
}

/// Origin validation, password throttling and the authorized-IP registry.
pub struct AuthState {
    failures: StdMutex<HashMap<String, AuthFailEntry>>,
    authorized: StdMutex<HashMap<String, Instant>>,
}

impl Default for AuthState {
    fn default() -> Self {
        Self::new()
    }
}

impl AuthState {
    pub fn new() -> Self {
        Self {
            failures: StdMutex::new(HashMap::new()),
            authorized: StdMutex::new(HashMap::new()),
        }
    }

    fn is_blocked(&self, ip: &str) -> bool {
        let Ok(mut map) = self.failures.lock() else {
            return false;
        };
        let Some(entry) = map.get_mut(ip) else {
            return false;
        };
        if let Some(until) = entry.blocked_until {
            if Instant::now() < until {
                return true;
            }
        }
        if entry.first_failure.map(|t| t.elapsed() > AUTH_FAIL_WINDOW).unwrap_or(true) {
            map.remove(ip);
        }
        false
    }

    fn record_failure(&self, ip: &str) {
        let Ok(mut map) = self.failures.lock() else {
            return;
        };
        let stale = map
            .get(ip)
            .and_then(|e| e.first_failure)
            .map(|t| t.elapsed() > AUTH_FAIL_WINDOW)
            .unwrap_or(true);
        if stale {
            map.insert(ip.to_string(), AuthFailEntry {
                first_failure: Some(Instant::now()),
                ..Default::default()
            });
        }
        if let Some(entry) = map.get_mut(ip) {
            entry.count += 1;
            if entry.count >= AUTH_MAX_FAILURES {
                entry.blocked_until = Some(Instant::now() + AUTH_BLOCK_DURATION);
            }
        }
    }

    fn record_success(&self, ip: &str) {
        if let Ok(mut map) = self.failures.lock() {
            map.remove(ip);
        }
    }

    /// Mark an IP as trusted for the raw media TCP ports (camera/audio/screen).
    pub fn register_authorized_ip(&self, ip: &str) {
        let ip = ip.trim().trim_matches(|c| c == '[' || c == ']');
        if ip.is_empty() {
            return;
        }
        if let Ok(mut map) = self.authorized.lock() {
            map.insert(ip.to_string(), Instant::now());
        }
    }

    /// Whether a raw media connection from `ip` may be accepted.
    pub fn is_authorized_stream_ip(&self, ip: &str) -> bool {
        if is_loopback_host(ip) {
            return true;
        }
        if config::load_config().password.is_empty() {
            return true;
        }
        let Ok(map) = self.authorized.lock() else {
            return false;
        };
        map.get(ip)
            .map(|at| at.elapsed() < AUTHORIZED_IP_TTL)
            .unwrap_or(false)
    }
}

/// Compare two secrets without leaking length or content through timing.
fn constant_time_eq(a: &str, b: &str) -> bool {
    let (a, b) = (a.as_bytes(), b.as_bytes());
    let mut diff = (a.len() ^ b.len()) as u8;
    let len = a.len().max(b.len());
    for i in 0..len {
        let x = *a.get(i).unwrap_or(&0);
        let y = *b.get(i).unwrap_or(&0);
        diff |= x ^ y;
    }
    diff == 0
}

fn request_password(headers: &HeaderMap, uri: &Uri) -> String {
    if let Some(value) = headers.get("x-password").and_then(|v| v.to_str().ok()) {
        if !value.is_empty() {
            return value.to_string();
        }
    }
    if let Some(query) = uri.query() {
        for pair in query.split('&') {
            if let Some(value) = pair.strip_prefix("password=") {
                return percent_decode(value);
            }
        }
    }
    String::new()
}

fn percent_decode(input: &str) -> String {
    let bytes = input.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        match bytes[i] {
            b'%' if i + 2 < bytes.len() => {
                let hex = std::str::from_utf8(&bytes[i + 1..i + 3]).unwrap_or("");
                match u8::from_str_radix(hex, 16) {
                    Ok(byte) => {
                        out.push(byte);
                        i += 3;
                    }
                    Err(_) => {
                        out.push(bytes[i]);
                        i += 1;
                    }
                }
            }
            b'+' => {
                out.push(b' ');
                i += 1;
            }
            byte => {
                out.push(byte);
                i += 1;
            }
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// Full HTTP authorization check (origin + loopback/LAN password + throttling).
fn is_authorized(state: &AppState, headers: &HeaderMap, uri: &Uri, peer: SocketAddr) -> bool {
    if !is_allowed_origin(headers) {
        return false;
    }
    let ip = peer.ip();
    if ip.is_loopback() {
        state.auth.register_authorized_ip(&ip.to_string());
        return true;
    }
    let host = ip.to_string();
    if state.auth.is_blocked(&host) {
        return false;
    }
    let cfg = config::load_config();
    if cfg.password.is_empty() {
        state.auth.register_authorized_ip(&host);
        return true;
    }
    if constant_time_eq(&request_password(headers, uri), &cfg.password) {
        state.auth.record_success(&host);
        state.auth.register_authorized_ip(&host);
        return true;
    }
    state.auth.record_failure(&host);
    false
}

// ---------------------------------------------------------------------------
// Pomodoro
// ---------------------------------------------------------------------------

struct Pomodoro {
    running: bool,
    on_break: bool,
    ends_at: Option<Instant>,
    run_id: u64,
}

impl Pomodoro {
    fn new() -> Self {
        Self {
            running: false,
            on_break: false,
            ends_at: None,
            run_id: 0,
        }
    }

    fn snapshot(&self) -> Value {
        let phase = if !self.running {
            "idle"
        } else if self.on_break {
            "break"
        } else {
            "work"
        };
        let remaining = self
            .ends_at
            .map(|at| at.saturating_duration_since(Instant::now()).as_secs())
            .unwrap_or(0);
        json!({
            "type": "pomodoro",
            "running": self.running,
            "phase": phase,
            "remaining_seconds": remaining,
            "work_minutes": POMODORO_WORK_MINUTES,
            "break_minutes": POMODORO_BREAK_MINUTES,
        })
    }
}

// ---------------------------------------------------------------------------
// AppState
// ---------------------------------------------------------------------------

/// Shared server state. `Config` itself lives in [`crate::config`]'s process-wide
/// cache so every module observes the same values.
pub struct AppState {
    /// Flow message history (SharedFiles/flow_history.json).
    pub flow: FlowManager,
    /// Fan-out channel every WebSocket connection subscribes to.
    pub ws_tx: broadcast::Sender<String>,
    /// `SharedFiles/` spool used by upload/download.
    pub shared_dir: PathBuf,
    /// Number of currently connected WebSocket clients.
    pub clients: AtomicUsize,
    /// Origin validation, password throttling and authorized stream IPs.
    pub auth: AuthState,
    /// System telemetry provider.
    pub screen: AsyncMutex<SystemMonitor>,
    /// Lazily created mouse/keyboard controller.
    pub input: AsyncMutex<Option<InputController>>,
    /// Pomodoro focus timer state.
    pomodoro: StdMutex<Pomodoro>,
    /// Last non-zero master volume, used to un-mute.
    pub last_volume: StdMutex<u8>,
    /// Signalled by `/shutdown`-style callers to stop the axum server.
    pub shutdown: Notify,
}

impl AppState {
    /// Build the state with a fresh Flow manager and telemetry monitor.
    pub fn create() -> Self {
        let (ws_tx, _rx) = broadcast::channel(512);
        Self {
            flow: FlowManager::new(),
            ws_tx,
            shared_dir: get_shared_dir(),
            clients: AtomicUsize::new(0),
            auth: AuthState::new(),
            screen: AsyncMutex::new(SystemMonitor::new()),
            input: AsyncMutex::new(None),
            pomodoro: StdMutex::new(Pomodoro::new()),
            last_volume: StdMutex::new(50),
            shutdown: Notify::new(),
        }
    }

    /// Convenience constructor for [`start_server`] and the Tauri shell.
    pub fn new() -> Arc<Self> {
        Arc::new(Self::create())
    }

    /// Serialize `value` and push it to every connected WebSocket client.
    pub fn broadcast(&self, value: Value) {
        let _ = self.ws_tx.send(value.to_string());
    }

    /// Number of connected WebSocket clients.
    pub fn client_count(&self) -> usize {
        self.clients.load(Ordering::Relaxed)
    }

    /// Mark an IP as trusted for the raw media TCP ports.
    pub fn register_authorized_ip(&self, ip: &str) {
        self.auth.register_authorized_ip(ip);
    }

    /// Whether a raw media connection from `ip` may be accepted.
    pub fn is_authorized_stream_ip(&self, ip: &str) -> bool {
        self.auth.is_authorized_stream_ip(ip)
    }

    /// Current configuration snapshot.
    pub fn config(&self) -> Config {
        config::load_config()
    }
}

impl Default for AppState {
    fn default() -> Self {
        Self::create()
    }
}

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

/// Build the full application router.
pub fn router(state: Arc<AppState>) -> Router {
    let protected = Router::new()
        .route("/ws", get(ws_handler))
        .route(
            "/api/flow",
            get(flow_list).post(flow_create).delete(flow_clear),
        )
        .route("/api/flow/upload", post(flow_upload))
        .route("/api/flow/screenshot", post(flow_screenshot))
        .route("/files", get(list_files))
        .route("/files/:name", delete(delete_file))
        .route("/download/:name", get(download_file))
        .route("/upload", post(upload_file))
        .route("/config", get(get_config).post(post_config))
        .route("/driver/status", get(driver_status))
        .route("/driver/install", post(driver_install))
        .route("/driver/enable", post(driver_enable))
        .route("/driver/mic/status", get(driver_mic_status))
        .route("/driver/mic/install", post(driver_mic_install))
        .route("/open_sound_settings", post(open_sound_settings_handler))
        .route("/installed_apps", get(installed_apps_handler))
        .route(
            "/api/autostart",
            get(api_autostart_get).post(api_autostart_post),
        )
        .layer(middleware::from_fn_with_state(state.clone(), auth_middleware));

    let public = Router::new()
        .route("/system_info", get(system_info))
        .route("/api/update/check", get(update_check))
        .route("/api/qr", get(api_qr))
        .fallback_service(static_service());

    public
        .merge(protected)
        .layer(middleware::from_fn(gate_middleware))
        .with_state(state)
}

fn static_service() -> ServeDir<ServeFile> {
    let dir = frontend_dir();
    ServeDir::new(&dir).fallback(ServeFile::new(dir.join("index.html")))
}

/// Rejects foreign origins, answers CORS preflights and mirrors the origin back.
async fn gate_middleware(headers: HeaderMap, req: Request, next: Next) -> Response {
    let origin = headers
        .get(header::ORIGIN)
        .and_then(|v| v.to_str().ok())
        .map(str::to_string)
        .filter(|o| !o.is_empty());

    let allowed = is_allowed_origin(&headers);
    if !allowed {
        return json_response(
            StatusCode::FORBIDDEN,
            json!({"success": false, "error": "Forbidden origin"}),
        );
    }

    if req.method() == Method::OPTIONS {
        let mut response = StatusCode::OK.into_response();
        apply_cors(response.headers_mut(), origin.as_deref());
        return response;
    }

    let mut response = next.run(req).await;
    apply_cors(response.headers_mut(), origin.as_deref());
    response
}

fn apply_cors(headers: &mut HeaderMap, origin: Option<&str>) {
    if let Some(origin) = origin {
        if let Ok(value) = HeaderValue::from_str(origin) {
            headers.insert(header::ACCESS_CONTROL_ALLOW_ORIGIN, value);
            headers.append(header::VARY, HeaderValue::from_static("Origin"));
            headers.insert(
                header::ACCESS_CONTROL_ALLOW_HEADERS,
                HeaderValue::from_static("*"),
            );
            headers.insert(
                header::ACCESS_CONTROL_ALLOW_METHODS,
                HeaderValue::from_static("GET, POST, PUT, DELETE, OPTIONS"),
            );
        }
    }
}

/// Requires a valid password/origin for the wrapped routes.
async fn auth_middleware(
    State(state): State<Arc<AppState>>,
    ConnectInfo(peer): ConnectInfo<SocketAddr>,
    req: Request,
    next: Next,
) -> Response {
    let uri = req.uri().clone();
    if !is_authorized(&state, req.headers(), &uri, peer) {
        return json_response(
            StatusCode::UNAUTHORIZED,
            json!({"success": false, "error": "Unauthorized"}),
        );
    }
    next.run(req).await
}

fn json_response(status: StatusCode, value: Value) -> Response {
    (status, Json(value)).into_response()
}

// ---------------------------------------------------------------------------
// WebSocket
// ---------------------------------------------------------------------------

async fn ws_handler(
    ws: WebSocketUpgrade,
    State(state): State<Arc<AppState>>,
    ConnectInfo(peer): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
) -> Response {
    if !is_allowed_origin(&headers) {
        return json_response(
            StatusCode::FORBIDDEN,
            json!({"success": false, "error": "Forbidden origin"}),
        );
    }
    ws.on_upgrade(move |socket| handle_socket(state, socket, peer))
}

async fn handle_socket(state: Arc<AppState>, socket: WebSocket, peer: SocketAddr) {
    state.clients.fetch_add(1, Ordering::Relaxed);
    let (mut sink, mut stream) = socket.split();
    let (out_tx, mut out_rx) = mpsc::channel::<String>(256);
    let authenticated = Arc::new(AtomicBool::new(false));

    let is_local = peer.ip().is_loopback();
    let password = config::load_config().password;
    let needs_auth = !password.is_empty() && !is_local;

    if is_local {
        state.auth.register_authorized_ip(&peer.ip().to_string());
    }

    let bcast_rx = state.ws_tx.subscribe();
    let authed_writer = authenticated.clone();
    let writer = tokio::spawn(async move {
        let mut bcast_rx = bcast_rx;
        loop {
            tokio::select! {
                outgoing = out_rx.recv() => match outgoing {
                    Some(text) => {
                        if sink.send(Message::Text(text)).await.is_err() {
                            break;
                        }
                    }
                    None => break,
                },
                broadcast = bcast_rx.recv(), if authed_writer.load(Ordering::Relaxed) => match broadcast {
                    Ok(text) => {
                        if sink.send(Message::Text(text)).await.is_err() {
                            break;
                        }
                    }
                    Err(broadcast::error::RecvError::Lagged(_)) => continue,
                    Err(broadcast::error::RecvError::Closed) => break,
                },
            }
        }
        let _ = sink.close().await;
    });

    if needs_auth {
        let _ = out_tx.send(json!({"type": "auth_required"}).to_string());
    } else {
        authenticated.store(true, Ordering::Relaxed);
        let _ = out_tx.send(json!({"type": "auth_success"}).to_string());
        state.broadcast(json!({"type": "client_connected"}));
    }

    let mut deadline = Box::pin(tokio::time::sleep(WS_AUTH_TIMEOUT));
    loop {
        let incoming = tokio::select! {
            incoming = stream.next() => incoming,
            _ = &mut deadline, if !authenticated.load(Ordering::Relaxed) => break,
        };
        let Some(incoming) = incoming else {
            break;
        };
        let Ok(message) = incoming else {
            break;
        };
        let text = match message {
            Message::Text(text) => text,
            Message::Close(_) => break,
            _ => continue,
        };
        let Ok(payload) = serde_json::from_str::<Value>(&text) else {
            continue;
        };
        let msg_type = payload
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string();

        if !authenticated.load(Ordering::Relaxed) {
            if msg_type == "auth_login" {
                let host = peer.ip().to_string();
                if state.auth.is_blocked(&host) {
                    let _ = out_tx.send(json!({
                        "type": "auth_failed",
                        "message": "Too many attempts. Try again later."
                    }).to_string());
                    break;
                }
                let supplied = payload
                    .get("password")
                    .and_then(Value::as_str)
                    .unwrap_or("");
                if constant_time_eq(supplied, &password) {
                    authenticated.store(true, Ordering::Relaxed);
                    state.auth.record_success(&host);
                    state.auth.register_authorized_ip(&host);
                    let _ = out_tx.send(json!({"type": "auth_success"}).to_string());
                    state.broadcast(json!({"type": "client_connected"}));
                } else {
                    state.auth.record_failure(&host);
                    let _ = out_tx.send(json!({
                        "type": "auth_failed",
                        "message": "Wrong password"
                    }).to_string());
                    break;
                }
            }
            continue;
        }

        handle_control(&state, msg_type.as_str(), &payload).await;
    }

    if authenticated.load(Ordering::Relaxed) {
        state.broadcast(json!({"type": "client_disconnected"}));
    }
    drop(out_tx);
    let _ = tokio::time::timeout(Duration::from_secs(2), writer).await;
    state.clients.fetch_sub(1, Ordering::Relaxed);
}

/// Run a controller action against the lazily created [`InputController`].
async fn with_input<F>(state: &Arc<AppState>, action: F)
where
    F: FnOnce(&mut InputController) -> Result<(), String>,
{
    let mut guard = state.input.lock().await;
    if guard.is_none() {
        match InputController::new() {
            Ok(controller) => *guard = Some(controller),
            Err(err) => {
                tracing::warn!("input controller unavailable: {err}");
                return;
            }
        }
    }
    if let Some(controller) = guard.as_mut() {
        if let Err(err) = action(controller) {
            tracing::debug!("input action failed: {err}");
        }
    }
}

async fn system_stats(state: &Arc<AppState>) -> Value {
    let mut monitor = state.screen.lock().await;
    monitor.get_stats_payload()
}

fn as_f64(payload: &Value, key: &str) -> Option<f64> {
    payload.get(key).and_then(Value::as_f64)
}

fn as_i64(payload: &Value, key: &str) -> Option<i64> {
    payload.get(key).and_then(Value::as_i64)
}

fn as_str<'a>(payload: &'a Value, key: &str) -> Option<&'a str> {
    payload.get(key).and_then(Value::as_str)
}

/// Dispatch one authenticated WebSocket control message.
async fn handle_control(
    state: &Arc<AppState>,
    msg_type: &str,
    payload: &Value,
) {
    match msg_type {
        "mouse_move" => {
            let dx = as_f64(payload, "dx").unwrap_or(0.0) as i32;
            let dy = as_f64(payload, "dy").unwrap_or(0.0) as i32;
            with_input(state, |c| c.move_relative(dx, dy)).await;
        }
        "mouse_click" => {
            let button = button_for(as_str(payload, "button").unwrap_or("left"));
            with_input(state, |c| c.click(button)).await;
        }
        "mouse_scroll" => {
            let dy = as_f64(payload, "dy").unwrap_or(0.0) as i32;
            with_input(state, |c| c.scroll(dy)).await;
        }
        "keyboard_input" => {
            if let Some(text) = as_str(payload, "text") {
                if !text.is_empty() {
                    let text = text.to_string();
                    with_input(state, |c| c.text(&text)).await;
                }
            }
        }
        "keyboard_key" => {
            if let Some(key) = as_str(payload, "key") {
                let key = key.to_string();
                with_input(state, |c| c.key_click_name(&key)).await;
            }
        }
        "media_control" => {
            let action = media_hotkey(as_str(payload, "action").unwrap_or(""));
            with_input(state, |c| c.simulate_hotkey(action)).await;
        }
        "set_volume" => {
            let action = as_str(payload, "action").unwrap_or("").to_string();
            let level = as_f64(payload, "level").unwrap_or(50.0);
            apply_volume(state, &action, level);
            state.broadcast(json!({
                "type": "volume_changed",
                "action": action,
                "level": level,
            }));
        }
        "set_brightness" => {
            let level = as_f64(payload, "level").unwrap_or(80.0).clamp(0.0, 100.0) as u8;
            set_display_brightness(level);
            state.broadcast(json!({"type": "brightness_changed", "level": level}));
        }
        "set_app_volume" => {
            // Per-application WASAPI sessions are not available in this build; the
            // phone UI still receives an explicit answer instead of silence.
            let pid = as_i64(payload, "pid").unwrap_or(0);
            let level = as_f64(payload, "volume").unwrap_or(50.0).clamp(0.0, 100.0);
            state.broadcast(json!({
                "type": "app_volume_changed",
                "pid": pid,
                "name": as_str(payload, "name").unwrap_or(""),
                "volume": level,
                "success": false,
                "error": "Per-application volume is not supported",
            }));
        }
        "get_system_stats" => {
            let stats = system_stats(state).await;
            state.broadcast(stats);
        }
        "get_clipboard" => {
            let text = get_clipboard().unwrap_or_default();
            state.broadcast(json!({"type": "clipboard_sync", "text": text}));
        }
        "clipboard_sync" => {
            if let Some(text) = as_str(payload, "text") {
                set_clipboard(text);
                let text = text.to_string();
                let message = state
                    .flow
                    .add_message(FlowMessage::new("phone", "text", text))
                    .await;
                state.broadcast(json!({"type": "flow_new", "message": message}));
            }
        }
        "flow_send" => {
            let sender = as_str(payload, "sender").filter(|s| !s.is_empty()).unwrap_or("phone");
            let kind = as_str(payload, "msg_type").filter(|s| !s.is_empty()).unwrap_or("text");
            let mut message = FlowMessage::new(sender, kind, as_str(payload, "text").unwrap_or(""));
            message.file_name = as_str(payload, "file_name").unwrap_or("").to_string();
            message.file_url = as_str(payload, "file_url").unwrap_or("").to_string();
            message.file_size = as_i64(payload, "file_size").unwrap_or(0);
            let message = state.flow.add_message(message).await;
            state.broadcast(json!({"type": "flow_new", "message": message}));
        }
        "switch_page" => {
            let target = as_i64(payload, "page").unwrap_or(0) as i32;
            let mut cfg = config::load_config();
            cfg.active_page = target;
            let _ = config::save_config(&cfg);
            let cfg = config::load_config();
            state.broadcast(json!({
                "type": "page_switched",
                "active_page": target,
                "buttons": cfg.stream_deck_buttons,
            }));
        }
        "set_grid_layout" => {
            let rows = as_i64(payload, "rows").unwrap_or(0);
            let cols = as_i64(payload, "cols").unwrap_or(0);
            if (1..=6).contains(&rows) && (1..=8).contains(&cols) {
                let mut cfg = config::load_config();
                cfg.stream_deck_rows = rows as i32;
                cfg.stream_deck_cols = cols as i32;
                let _ = config::save_config(&cfg);
                state.broadcast(json!({
                    "type": "config_update",
                    "rows": rows,
                    "cols": cols,
                }));
            }
        }
        "stream_deck_press" | "trigger_button" => {
            let Some(button_id) = as_i64(payload, "button_id") else {
                return;
            };
            press_button(state, button_id as i32, msg_type == "stream_deck_press", payload).await;
        }
        "setup_usb_reverse" => {
            let (success, message) = tokio::task::spawn_blocking(setup_adb_reverse)
                .await
                .unwrap_or((false, "ADB köprüsü başlatılamadı.".to_string()));
            state.broadcast(json!({
                "type": "usb_status",
                "success": success,
                "message": message,
            }));
        }
        "notification" => {
            let title = as_str(payload, "title").unwrap_or("BigPocket").to_string();
            let app = as_str(payload, "app").unwrap_or("");
            let text = as_str(payload, "text").unwrap_or("");
            let body = if app.is_empty() {
                text.to_string()
            } else {
                format!("[{app}] {text}")
            };
            show_notification(&title, &body);
        }
        "monitor_touch" => {
            monitor_touch(state, payload).await;
        }
        _ => {}
    }
}

fn button_for(name: &str) -> Button {
    match name {
        "left" => Button::Left,
        "right" => Button::Right,
        _ => Button::Middle,
    }
}

fn media_hotkey(action: &str) -> &'static str {
    match action {
        "prev" | "prevtrack" => "prevtrack",
        "next" | "nexttrack" => "nexttrack",
        _ => "playpause",
    }
}

/// Execute a Stream Deck button, mirroring `stream_deck_press`/`trigger_button`.
async fn press_button(state: &Arc<AppState>, button_id: i32, toggle: bool, payload: &Value) {
    let mut cfg = config::load_config();
    let Some(index) = cfg.stream_deck_buttons.iter().position(|b| b.id == button_id) else {
        return;
    };
    let kind = cfg.stream_deck_buttons[index].kind.clone();
    let value = cfg.stream_deck_buttons[index].value.clone();
    let page = cfg.stream_deck_buttons[index].page;

    if toggle && kind == "toggle" {
        cfg.stream_deck_buttons[index].state = !cfg.stream_deck_buttons[index].state;
        let new_state = cfg.stream_deck_buttons[index].state;
        let _ = config::save_config(&cfg);
        state.broadcast(json!({
            "type": "button_state_changed",
            "button_id": button_id,
            "state": new_state,
            "page": page,
        }));
    }

    match kind.as_str() {
        "toggle" => {
            let new_state = cfg
                .stream_deck_buttons
                .iter()
                .find(|b| b.id == button_id)
                .map(|b| b.state)
                .unwrap_or(false);
            match value.as_str() {
                "mic_mute" => with_input(state, |c| c.simulate_hotkey("ctrl+shift+m")).await,
                "speaker_mute" | "sound_mute" => apply_volume(state, "mute", 0.0),
                "screen_toggle" | "virtual_monitor" => set_virtual_monitor(new_state),
                _ => run_deck_value(state, &value).await,
            }
        }
        "volume_slider" => {
            if let Some(action) = as_str(payload, "volume_action") {
                if !action.is_empty() {
                    let level = as_f64(payload, "volume_level").unwrap_or(50.0);
                    apply_volume(state, action, level);
                }
            }
        }
        "live_info" => {
            let stats = system_stats(state).await;
            state.broadcast(stats);
        }
        "now_playing" | "media_widget" => {
            let action = if kind == "media_widget" {
                as_str(payload, "media_action").filter(|a| !a.is_empty()).unwrap_or(&value)
            } else {
                as_str(payload, "media_action").unwrap_or("")
            };
            let hotkey = media_hotkey(action);
            with_input(state, |c| c.simulate_hotkey(hotkey)).await;
        }
        "hotkey" => run_deck_value(state, &value).await,
        "url" => {
            let _ = spawn_detached(&value);
        }
        "text_snippet" => with_input(state, |c| c.text(&value)).await,
        "command" => {
            match value.as_str() {
                "clean_clipboard" | "clear_clipboard" => {
                    set_clipboard("");
                }
                "empty_trash" | "empty_recycle_bin" => {
                    empty_recycle_bin();
                }
                _ => {
                    let _ = spawn_detached(&value);
                }
            }
        }
        _ => {}
    }
}

/// Run a `hotkey` button value: special pseudo-actions first, then a real hotkey.
async fn run_deck_value(state: &Arc<AppState>, value: &str) {
    match value.trim().to_ascii_lowercase().as_str() {
        "pomodoro" | "pomodoro_toggle" => {
            toggle_pomodoro(state);
        }
        "net_speed" | "net_stats" | "network_speed" => {
            let stats = system_stats(state).await;
            let down = stats.get("net_down_kbps").and_then(Value::as_u64).unwrap_or(0);
            let up = stats.get("net_up_kbps").and_then(Value::as_u64).unwrap_or(0);
            state.broadcast(json!({
                "type": "net_speed",
                "down_kbps": down,
                "up_kbps": up,
                "down_mbps": down as f64 / 1000.0,
                "up_mbps": up as f64 / 1000.0,
            }));
        }
        _ => {
            let value = value.to_string();
            with_input(state, move |c| c.simulate_hotkey(&value)).await;
        }
    }
}

fn toggle_pomodoro(state: &Arc<AppState>) {
    let (snapshot, started) = {
        let Ok(mut pomodoro) = state.pomodoro.lock() else {
            return;
        };
        if pomodoro.running {
            pomodoro.running = false;
            pomodoro.on_break = false;
            pomodoro.run_id += 1;
            (pomodoro.snapshot(), false)
        } else {
            pomodoro.running = true;
            pomodoro.on_break = false;
            pomodoro.ends_at = Some(Instant::now() + Duration::from_secs(POMODORO_WORK_MINUTES * 60));
            pomodoro.run_id += 1;
            (pomodoro.snapshot(), true)
        }
    };
    state.broadcast(snapshot);
    if started {
        spawn_pomodoro_cycle(state.clone());
    }
}

fn spawn_pomodoro_cycle(state: Arc<AppState>) {
    tokio::spawn(async move {
        let run_id = {
            let Ok(pomodoro) = state.pomodoro.lock() else {
                return;
            };
            pomodoro.run_id
        };
        loop {
            let wait = {
                let Ok(pomodoro) = state.pomodoro.lock() else {
                    return;
                };
                if pomodoro.run_id != run_id || !pomodoro.running {
                    return;
                }
                pomodoro
                    .ends_at
                    .map(|at| at.saturating_duration_since(Instant::now()))
                    .unwrap_or_default()
            };
            tokio::time::sleep(wait).await;

            let (snapshot, notify) = {
                let Ok(mut pomodoro) = state.pomodoro.lock() else {
                    return;
                };
                if pomodoro.run_id != run_id || !pomodoro.running {
                    return;
                }
                if !pomodoro.on_break {
                    pomodoro.on_break = true;
                    pomodoro.ends_at =
                        Some(Instant::now() + Duration::from_secs(POMODORO_BREAK_MINUTES * 60));
                    (
                        pomodoro.snapshot(),
                        Some(format!("Mola zamanı! {POMODORO_BREAK_MINUTES} dakika dinlen.")),
                    )
                } else {
                    pomodoro.running = false;
                    (pomodoro.snapshot(), Some("Odaklanma seansı tamamlandı. Harika iş!".to_string()))
                }
            };
            state.broadcast(snapshot);
            if let Some(message) = notify {
                let done = {
                    let Ok(pomodoro) = state.pomodoro.lock() else {
                        return;
                    };
                    !pomodoro.running
                };
                show_notification("BigPocket Pomodoro", &message);
                if done {
                    return;
                }
            }
        }
    });
}

async fn monitor_touch(state: &Arc<AppState>, payload: &Value) {
    let index = if crate::system::monitor_bounds(1).is_some() { 1 } else { 0 };
    let Some((mx, my, mw, mh)) = crate::system::monitor_bounds(index) else {
        return;
    };
    let action = as_str(payload, "action").unwrap_or("");

    if let (Some(rx), Some(ry)) = (as_f64(payload, "x"), as_f64(payload, "y")) {
        let px = mx + (rx * mw as f64) as i32;
        let py = my + (ry * mh as f64) as i32;
        with_input(state, move |c| c.move_to(px, py)).await;
    }

    match action {
        "down" => with_input(state, |c| c.mouse_down(Button::Left)).await,
        "up" => with_input(state, |c| c.mouse_up(Button::Left)).await,
        "click" => with_input(state, |c| c.click(Button::Left)).await,
        "right_click" => with_input(state, |c| c.click(Button::Right)).await,
        "double_click" => {
            with_input(state, |c| c.click(Button::Left)).await;
            tokio::time::sleep(Duration::from_millis(30)).await;
            with_input(state, |c| c.click(Button::Left)).await;
        }
        "scroll" => {
            let dy = as_i64(payload, "dy").unwrap_or(120) as i32;
            with_input(state, move |c| c.scroll(dy)).await;
        }
        _ => {}
    }
}

/// Master-volume control (waveOut on Windows, no-op elsewhere).
fn apply_volume(state: &Arc<AppState>, action: &str, level: f64) {
    match action {
        "up" => {
            let current = volume_get().unwrap_or(50);
            let target = (current as i32 + 5).clamp(0, 100) as u8;
            volume_set(target);
            remember_volume(state, target);
        }
        "down" => {
            let current = volume_get().unwrap_or(50);
            let target = (current as i32 - 5).clamp(0, 100) as u8;
            volume_set(target);
            remember_volume(state, target);
        }
        "mute" => {
            let current = volume_get().unwrap_or(0);
            if current == 0 {
                let restore = *state.last_volume.lock().unwrap_or_else(|e| e.into_inner());
                volume_set(restore.max(1));
            } else {
                remember_volume(state, current);
                volume_set(0);
            }
        }
        "set" => {
            let target = level.clamp(0.0, 100.0) as u8;
            volume_set(target);
            remember_volume(state, target);
        }
        _ => {}
    }
}

fn remember_volume(state: &Arc<AppState>, level: u8) {
    if level > 0 {
        if let Ok(mut last) = state.last_volume.lock() {
            *last = level;
        }
    }
}

// ---------------------------------------------------------------------------
// REST handlers
// ---------------------------------------------------------------------------

async fn flow_list(State(state): State<Arc<AppState>>) -> Response {
    Json(json!({"success": true, "messages": state.flow.get_messages().await})).into_response()
}

async fn flow_create(State(state): State<Arc<AppState>>, Json(body): Json<Value>) -> Response {
    let mut message = FlowMessage::new(
        as_str(&body, "sender").filter(|s| !s.is_empty()).unwrap_or("pc"),
        as_str(&body, "type").filter(|s| !s.is_empty()).unwrap_or("text"),
        as_str(&body, "text").unwrap_or(""),
    );
    message.file_name = as_str(&body, "file_name").unwrap_or("").to_string();
    message.file_url = as_str(&body, "file_url").unwrap_or("").to_string();
    message.file_size = as_i64(&body, "file_size").unwrap_or(0);

    let message = state.flow.add_message(message).await;
    state.broadcast(json!({"type": "flow_new", "message": message}));
    Json(json!({"success": true, "message": message})).into_response()
}

async fn flow_clear(State(state): State<Arc<AppState>>) -> Response {
    state.flow.clear_messages().await;
    state.broadcast(json!({"type": "flow_cleared"}));
    Json(json!({"success": true})).into_response()
}

async fn flow_upload(
    State(state): State<Arc<AppState>>,
    Query(query): Query<HashMap<String, String>>,
    mut multipart: Multipart,
) -> Response {
    let sender = query
        .get("sender")
        .filter(|s| !s.is_empty())
        .cloned()
        .unwrap_or_else(|| "pc".to_string());
    let (stored, size) = match save_upload(&mut multipart, &state.shared_dir).await {
        Ok(Some((file_name, path, size))) => (
            path.file_name()
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or(file_name),
            size,
        ),
        Ok(None) => {
            return json_response(
                StatusCode::BAD_REQUEST,
                json!({"success": false, "error": "Dosya alınamadı"}),
            )
        }
        Err(err) => {
            return json_response(
                err.status(),
                json!({"success": false, "error": err.message()}),
            )
        }
    };

    let kind = if is_image_extension(&stored) { "image" } else { "file" };
    let mut message = FlowMessage::new(sender, kind, stored.clone());
    message.file_name = stored.clone();
    message.file_size = size;
    message.file_url = format!("/download/{stored}");

    let message = state.flow.add_message(message).await;
    state.broadcast(json!({"type": "flow_new", "message": message}));
    Json(json!({"success": true, "message": message})).into_response()
}

async fn flow_screenshot(State(state): State<Arc<AppState>>) -> Response {
    let capture = tokio::task::spawn_blocking(capture_primary_display_png).await;
    let file_name = match capture {
        Ok(Ok((name, size))) => (name, size),
        Ok(Err(err)) => {
            return json_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                json!({"success": false, "error": err}),
            )
        }
        Err(err) => {
            return json_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                json!({"success": false, "error": err.to_string()}),
            )
        }
    };

    let mut message = FlowMessage::new("pc", "image", "📸 Ekran Görüntüsü");
    message.file_name = file_name.0.clone();
    message.file_size = file_name.1;
    message.file_url = format!("/download/{}", file_name.0);
    message.timestamp = now_millis();

    let message = state.flow.add_message(message).await;
    state.broadcast(json!({"type": "flow_new", "message": message}));
    Json(json!({"success": true, "message": message})).into_response()
}

async fn list_files(State(state): State<Arc<AppState>>) -> Response {
    let dir = state.shared_dir.clone();
    let listing = tokio::task::spawn_blocking(move || {
        let mut files = Vec::new();
        if let Ok(entries) = std::fs::read_dir(&dir) {
            for entry in entries.flatten() {
                let Ok(meta) = entry.metadata() else { continue };
                if meta.is_dir() {
                    continue;
                }
                let modified = meta
                    .modified()
                    .ok()
                    .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
                    .map(|d| d.as_secs())
                    .unwrap_or(0);
                files.push(json!({
                    "name": entry.file_name().to_string_lossy(),
                    "size": meta.len(),
                    "modified": modified,
                }));
            }
        }
        files
    })
    .await
    .unwrap_or_default();

    Json(json!({"success": true, "files": listing})).into_response()
}

/// Stream a shared file straight off disk — a Flow drop can be hundreds of
/// megabytes, so the body is never buffered in full.
async fn download_file(Path(name): Path<String>) -> Response {
    let Ok(path) = safe_shared_file_path(&name) else {
        return json_response(StatusCode::BAD_REQUEST, json!({"success": false, "error": "Invalid file name"}));
    };
    let file = match tokio::fs::File::open(&path).await {
        Ok(file) => file,
        Err(_) => {
            return json_response(
                StatusCode::NOT_FOUND,
                json!({"success": false, "error": "File not found"}),
            )
        }
    };

    let stream = futures_util::stream::try_unfold(file, |mut file| async move {
        use tokio::io::AsyncReadExt;
        let mut buffer = vec![0u8; STREAM_CHUNK];
        match file.read(&mut buffer).await {
            Ok(0) => Ok(None),
            Ok(read) => {
                buffer.truncate(read);
                // `Bytes::from(Vec)` takes ownership — no copy of the payload.
                Ok(Some((Bytes::from(buffer), file)))
            }
            Err(err) => Err(err),
        }
    });

    let content_type = content_type_for(&name);
    (
        [(header::CONTENT_TYPE, HeaderValue::from_static(content_type))],
        Body::from_stream(stream),
    )
        .into_response()
}

async fn delete_file(Path(name): Path<String>) -> Response {
    let Ok(path) = safe_shared_file_path(&name) else {
        return json_response(StatusCode::BAD_REQUEST, json!({"success": false, "error": "Invalid file name"}));
    };
    match tokio::fs::remove_file(&path).await {
        Ok(()) => Json(json!({"success": true})).into_response(),
        Err(err) => json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err.to_string()}),
        ),
    }
}

async fn upload_file(
    State(state): State<Arc<AppState>>,
    mut multipart: Multipart,
) -> Response {
    let (stored, size) = match save_upload(&mut multipart, &state.shared_dir).await {
        Ok(Some((file_name, path, size))) => (
            path.file_name()
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or(file_name),
            size,
        ),
        Ok(None) => {
            return json_response(
                StatusCode::BAD_REQUEST,
                json!({"success": false, "error": "Dosya alınamadı"}),
            )
        }
        Err(err) => {
            return json_response(
                err.status(),
                json!({"success": false, "error": err.message()}),
            )
        }
    };

    let kind = if is_image_extension(&stored) { "image" } else { "file" };
    let mut message = FlowMessage::new("phone", kind, stored.clone());
    message.file_name = stored.clone();
    message.file_size = size;
    message.file_url = format!("/download/{stored}");

    let message = state.flow.add_message(message).await;
    state.broadcast(json!({"type": "flow_new", "message": message}));
    Json(json!({"success": true, "message": format!("Uploaded {}", message.file_name)})).into_response()
}

/// Spool the first `file` field of a multipart body into `dir`, streaming it in
/// [`STREAM_CHUNK`] steps so an upload never has to fit in memory.
async fn save_upload(
    multipart: &mut Multipart,
    dir: &FsPath,
) -> Result<Option<(String, PathBuf, i64)>, UploadError> {
    use tokio::io::AsyncWriteExt;

    loop {
        let field = match multipart.next_field().await {
            Ok(Some(field)) => field,
            Ok(None) => return Ok(None),
            Err(err) => return Err(UploadError::Io(err.to_string())),
        };
        if field.name() != Some("file") {
            continue;
        }

        let file_name = field.file_name().unwrap_or("upload.bin").to_string();
        let path = safe_join_path(dir, &file_name).map_err(UploadError::InvalidName)?;
        let mut file = tokio::fs::File::create(&path)
            .await
            .map_err(|err| UploadError::Io(err.to_string()))?;

        let mut field = field;
        let mut size: i64 = 0;
        loop {
            match field.chunk().await {
                Ok(Some(chunk)) => {
                    file.write_all(&chunk)
                        .await
                        .map_err(|err| UploadError::Io(err.to_string()))?;
                    size += chunk.len() as i64;
                }
                Ok(None) => break,
                Err(err) => return Err(UploadError::Io(err.to_string())),
            }
        }
        file.flush()
            .await
            .map_err(|err| UploadError::Io(err.to_string()))?;
        return Ok(Some((file_name, path, size)));
    }
}

/// Failure while spooling an upload, carrying the HTTP status it maps to.
enum UploadError {
    /// The multipart field carried an unusable file name.
    InvalidName(String),
    /// The payload could not be read or written.
    Io(String),
}

impl UploadError {
    fn status(&self) -> StatusCode {
        match self {
            UploadError::InvalidName(_) => StatusCode::BAD_REQUEST,
            UploadError::Io(_) => StatusCode::INTERNAL_SERVER_ERROR,
        }
    }

    fn message(&self) -> &str {
        match self {
            UploadError::InvalidName(message) | UploadError::Io(message) => message,
        }
    }
}

async fn get_config() -> Response {
    Json(json!({"success": true, "config": config::load_config()})).into_response()
}

async fn post_config(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Json(body): Json<Value>,
) -> Response {
    let existing = config::load_config();
    let mut merged = merge_config(&body, &existing);
    merged.rev = existing.rev + 1;
    if let Err(err) = config::save_config(&merged) {
        return json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err}),
        );
    }
    let cfg = config::load_config();
    let source = headers
        .get("x-client-id")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .to_string();
    state.broadcast(json!({"type": "config_update", "source": source}));
    Json(json!({"success": true, "config": cfg})).into_response()
}

/// Merge a (possibly partial) client payload over the stored config, keeping the
/// cosmetic per-button fields and grid coordinates a partial save left out.
fn merge_config(body: &Value, existing: &Config) -> Config {
    let Some(incoming) = body.as_object() else {
        return existing.clone();
    };
    let mut base = serde_json::to_value(existing).unwrap_or_else(|_| json!({}));
    let Some(base_obj) = base.as_object_mut() else {
        return existing.clone();
    };

    for (key, value) in incoming {
        let keep_existing = match key.as_str() {
            // Never let a zero/empty value erase stored data.
            "stream_deck_rows" | "stream_deck_cols" => value.as_i64().unwrap_or(0) <= 0,
            "theme" => value.as_str().map(str::is_empty).unwrap_or(true),
            "weather_city" => value
                .as_str()
                .map(|s| s.trim().is_empty())
                .unwrap_or(true),
            // Handled separately below.
            "stream_deck_buttons" => true,
            _ => false,
        };
        if !keep_existing {
            base_obj.insert(key.clone(), value.clone());
        }
    }

    if let Some(buttons) = incoming.get("stream_deck_buttons").and_then(Value::as_array) {
        let mut previous: HashMap<i64, Map<String, Value>> = HashMap::new();
        for button in &existing.stream_deck_buttons {
            if let Ok(Value::Object(map)) = serde_json::to_value(button) {
                previous.insert(button.id as i64, map);
            }
        }
        let merged: Vec<Value> = buttons
            .iter()
            .map(|button| merge_button(button, &previous))
            .collect();
        base_obj.insert("stream_deck_buttons".to_string(), Value::Array(merged));
    }

    serde_json::from_value(base).unwrap_or_else(|_| existing.clone())
}

fn merge_button(button: &Value, previous: &HashMap<i64, Map<String, Value>>) -> Value {
    let Value::Object(current) = button else {
        return button.clone();
    };
    let Some(old) = current
        .get("id")
        .and_then(Value::as_i64)
        .and_then(|id| previous.get(&id))
    else {
        return button.clone();
    };

    let mut merged = current.clone();
    // Cosmetic strings: an empty/absent value keeps the stored one.
    for key in ["color", "bg_style", "clock_style", "font_family", "clock_font_size"] {
        let empty = current
            .get(key)
            .and_then(Value::as_str)
            .map(str::is_empty)
            .unwrap_or(true);
        if empty {
            if let Some(old_value) = old.get(key) {
                merged.insert(key.to_string(), old_value.clone());
            }
        }
    }
    // Spans: <= 0 keeps the stored one.
    for key in ["row_span", "col_span"] {
        let non_positive = current.get(key).and_then(Value::as_i64).unwrap_or(0) <= 0;
        if non_positive {
            if let Some(old_value) = old.get(key) {
                merged.insert(key.to_string(), old_value.clone());
            }
        }
    }
    // Grid cells: an absent coordinate keeps the stored one (explicit -1 always wins).
    for key in ["grid_row", "grid_col"] {
        if !current.contains_key(key) {
            if let Some(old_value) = old.get(key) {
                merged.insert(key.to_string(), old_value.clone());
            }
        }
    }
    Value::Object(merged)
}

async fn system_info() -> Response {
    let ips: Vec<String> = own_interface_ips();
    let usb_ip = get_usb_tethering_ip();
    let mut ip_addr = if usb_ip.is_empty() {
        "127.0.0.1".to_string()
    } else {
        usb_ip.clone()
    };

    if ip_addr == "127.0.0.1" {
        const TETHERING_PREFIXES: [&str; 6] = [
            "192.168.42.",
            "192.168.43.",
            "192.168.49.",
            "192.168.225.",
            "192.168.137.",
            "172.20.10.",
        ];
        if let Some(found) = ips
            .iter()
            .find(|ip| TETHERING_PREFIXES.iter().any(|p| ip.starts_with(p)))
        {
            ip_addr = found.clone();
        } else if let Some(first) = ips.iter().find(|ip| !ip.starts_with("169.254.")) {
            ip_addr = first.clone();
        } else if let Some(first) = ips.first() {
            ip_addr = first.clone();
        }
    }

    let (hostname, mem_alloc_mb, mem_sys_mb) = tokio::task::spawn_blocking(|| {
        let hostname = sysinfo::System::host_name().unwrap_or_default();
        let (rss, sys) = current_process_memory_mb();
        (hostname, rss, sys)
    })
    .await
    .unwrap_or_else(|_| (String::new(), 0.0, 0.0));

    Json(json!({
        "success": true,
        "ip": ip_addr,
        "ips": ips,
        "usb_ip": usb_ip,
        "ports": {
            "control_ws": CONTROL_PORT,
            "camera_tcp": CAMERA_TCP_PORT,
            "audio_tcp": AUDIO_TCP_PORT,
            "screen_tcp": SCREEN_TCP_PORT,
            "http_api": CONTROL_PORT,
        },
        "is_admin": is_admin(),
        "hostname": hostname,
        "mem_stats": {
            "alloc_mb": format!("{mem_alloc_mb:.1} MB"),
            "sys_mb": format!("{mem_sys_mb:.1} MB"),
        },
    }))
    .into_response()
}

async fn update_check(Query(query): Query<HashMap<String, String>>) -> Response {
    let force = query.get("force").map(|v| v == "1").unwrap_or(false);
    let is_beta = match query.get("beta").map(String::as_str) {
        Some("1") | Some("true") => true,
        Some("0") | Some("false") => false,
        _ => config::load_config().beta_channel,
    };
    Json(check_for_update(force, is_beta).await).into_response()
}

// ---------------------------------------------------------------------------
// Update checking (GitHub releases)
// ---------------------------------------------------------------------------

/// Response payload mirroring Go's `UpdateInfo`.
fn update_info_json(
    current: &str,
    latest: &str,
    available: bool,
    title: String,
    notes: String,
    url: String,
    published_at: String,
    download_url: String,
    error: String,
) -> Value {
    let mut value = json!({
        "current_version": current,
        "latest_version": latest,
        "available": available,
        "title": title,
        "notes": notes,
        "url": url,
        "published_at": published_at,
        "download_url": download_url,
    });
    if !error.is_empty() {
        value["error"] = Value::String(error);
    }
    value
}

async fn check_for_update(force: bool, is_beta: bool) -> Value {
    // Cached for 30 minutes unless the caller forces a refresh (mirrors Go).
    static CACHE: StdMutex<Option<(Instant, bool, Value)>> = StdMutex::new(None);
    if !force {
        if let Ok(guard) = CACHE.lock() {
            if let Some((at, cached_beta, value)) = guard.as_ref() {
                if *cached_beta == is_beta && at.elapsed() < Duration::from_secs(30 * 60) {
                    return value.clone();
                }
            }
        }
    }

    let current_version = APP_VERSION.trim_start_matches('v');
    let target = if is_beta {
        format!("https://api.github.com/repos/{GITHUB_REPO}/releases?per_page=5")
    } else {
        format!("https://api.github.com/repos/{GITHUB_REPO}/releases/latest")
    };

    let body = match http_get(&target).await {
        Ok(body) => body,
        Err(err) => {
            return update_info_json(
                current_version, "", false, String::new(), String::new(), String::new(),
                String::new(), String::new(), err,
            )
        }
    };
    let parsed: Value = match serde_json::from_str(&body) {
        Ok(value) => value,
        Err(err) => {
            return update_info_json(
                current_version, "", false, String::new(), String::new(), String::new(),
                String::new(), String::new(), err.to_string(),
            )
        }
    };

    let candidates: Vec<Value> = if is_beta {
        parsed.as_array().cloned().unwrap_or_default()
    } else {
        vec![parsed]
    };
    if candidates.is_empty() {
        return update_info_json(
            current_version, "", false, String::new(), String::new(), String::new(),
            String::new(), String::new(), "No releases found".to_string(),
        );
    }

    let best = candidates
        .iter()
        .filter_map(|release| release.get("tag_name").and_then(Value::as_str).map(|tag| (tag, release)))
        .max_by(|a, b| compare_versions(a.0, b.0).cmp(&0))
        .map(|(_, release)| release);

    let Some(best) = best else {
        return update_info_json(
            current_version, "", false, String::new(), String::new(), String::new(),
            String::new(), String::new(), "No releases found".to_string(),
        );
    };

    let latest_version = best
        .get("tag_name")
        .and_then(Value::as_str)
        .unwrap_or("")
        .trim_start_matches('v')
        .to_string();
    let download_url = resolve_release_download_url(best);
    let available = compare_versions(&latest_version, APP_VERSION) > 0 && !download_url.is_empty();

    let value = update_info_json(
        current_version,
        &latest_version,
        available,
        best.get("name").and_then(Value::as_str).unwrap_or("").to_string(),
        best.get("body").and_then(Value::as_str).unwrap_or("").to_string(),
        best.get("html_url").and_then(Value::as_str).unwrap_or("").to_string(),
        best.get("published_at").and_then(Value::as_str).unwrap_or("").to_string(),
        download_url,
        String::new(),
    );
    if let Ok(mut guard) = CACHE.lock() {
        *guard = Some((Instant::now(), is_beta, value.clone()));
    }
    value
}

/// Fetch a URL over HTTPS. The updater shells out to `curl`, which ships with
/// Windows 10 1803+ and every mainstream Linux/macOS, keeping the crate free of a
/// TLS stack it would otherwise only use for one request.
async fn http_get(url: &str) -> Result<String, String> {
    let output = tokio::process::Command::new("curl")
        .args([
            "-sS",
            "-L",
            "--max-time",
            "10",
            "-H",
            "Accept: application/vnd.github+json",
            "-H",
            "User-Agent: BigPocket-Updater",
            url,
        ])
        .output()
        .await
        .map_err(|err| format!("curl unavailable: {err}"))?;

    if !output.status.success() {
        return Err(format!(
            "GitHub API request failed: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        ));
    }
    String::from_utf8(output.stdout).map_err(|err| err.to_string())
}

fn resolve_release_download_url(release: &Value) -> String {
    let Some(assets) = release.get("assets").and_then(Value::as_array) else {
        return String::new();
    };
    let url_of = |asset: &Value| {
        asset
            .get("browser_download_url")
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string()
    };
    let name_of = |asset: &Value| {
        asset
            .get("name")
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_ascii_lowercase()
    };

    for asset in assets {
        let name = name_of(asset);
        if name.ends_with(".exe")
            && (name.contains("windows") || name.contains("setup") || name.contains("installer"))
        {
            return url_of(asset);
        }
    }
    for asset in assets {
        let name = name_of(asset);
        if name.ends_with(".zip") && name.contains("win") {
            return url_of(asset);
        }
    }
    for asset in assets {
        if name_of(asset).ends_with(".exe") {
            return url_of(asset);
        }
    }
    String::new()
}

/// SemVer comparison: 1 when `a > b`, -1 when `a < b`, 0 when equal.
fn compare_versions(a: &str, b: &str) -> i32 {
    let (nums_a, pre_a) = parse_semver(a);
    let (nums_b, pre_b) = parse_semver(b);
    for i in 0..3 {
        if nums_a[i] != nums_b[i] {
            return if nums_a[i] > nums_b[i] { 1 } else { -1 };
        }
    }
    match (pre_a.is_empty(), pre_b.is_empty()) {
        (true, false) => 1,
        (false, true) => -1,
        (true, true) => 0,
        (false, false) => match pre_a.cmp(&pre_b) {
            std::cmp::Ordering::Equal => 0,
            std::cmp::Ordering::Greater => 1,
            std::cmp::Ordering::Less => -1,
        },
    }
}

fn parse_semver(version: &str) -> ([i64; 3], String) {
    let trimmed = version.trim().trim_start_matches('v');
    let (numeric, pre) = match trimmed.split_once('-') {
        Some((n, p)) => (n, p.to_string()),
        None => (trimmed, String::new()),
    };
    let mut nums = [0i64; 3];
    for (slot, part) in nums.iter_mut().zip(numeric.split('.').take(3)) {
        *slot = part.parse().unwrap_or(0);
    }
    (nums, pre)
}

// ---------------------------------------------------------------------------
// Platform helpers
// ---------------------------------------------------------------------------

/// Resident memory of this process in MiB. The web UI only reports an
/// approximation of Go's runtime `alloc`/`sys` pair, so RSS is used for both.
fn current_process_memory_mb() -> (f64, f64) {
    let mut system = sysinfo::System::new_all();
    system.refresh_all();
    let Ok(pid) = sysinfo::get_current_pid() else {
        return (0.0, 0.0);
    };
    let Some(process) = system.process(pid) else {
        return (0.0, 0.0);
    };
    let rss = process.memory() as f64 / (1024.0 * 1024.0);
    (rss, rss)
}

fn content_type_for(name: &str) -> &'static str {
    let lower = name.to_ascii_lowercase();
    if lower.ends_with(".png") {
        "image/png"
    } else if lower.ends_with(".jpg") || lower.ends_with(".jpeg") {
        "image/jpeg"
    } else if lower.ends_with(".gif") {
        "image/gif"
    } else if lower.ends_with(".webp") {
        "image/webp"
    } else if lower.ends_with(".svg") {
        "image/svg+xml"
    } else if lower.ends_with(".pdf") {
        "application/pdf"
    } else if lower.ends_with(".json") {
        "application/json"
    } else if lower.ends_with(".txt") || lower.ends_with(".md") {
        "text/plain; charset=utf-8"
    } else if lower.ends_with(".zip") {
        "application/zip"
    } else if lower.ends_with(".exe") {
        "application/octet-stream"
    } else {
        "application/octet-stream"
    }
}

fn spawn_detached(command: &str) -> std::io::Result<()> {
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        let lower = command.trim().to_ascii_lowercase();
        let is_url = lower.starts_with("http://") || lower.starts_with("https://");
        let mut process = std::process::Command::new("cmd");
        if is_url {
            // `start "" <url>` hands the URL to the default browser.
            process.args(["/C", "start", "", command]);
        } else {
            process.args(["/C", command]);
        }
        process.creation_flags(CREATE_NO_WINDOW).spawn().map(|_| ())
    }
    #[cfg(not(windows))]
    {
        let lower = command.trim().to_ascii_lowercase();
        if lower.starts_with("http://") || lower.starts_with("https://") {
            let opener = if cfg!(target_os = "macos") { "open" } else { "xdg-open" };
            std::process::Command::new(opener).arg(command).spawn().map(|_| ())
        } else {
            std::process::Command::new("sh").arg("-c").arg(command).spawn().map(|_| ())
        }
    }
}

/// Capture display 0 to `SharedFiles/ekran_<stamp>.png`, returning (name, size).
fn capture_primary_display_png() -> Result<(String, i64), String> {
    let monitors = xcap::Monitor::all().map_err(|e| format!("Ekran bulunamadı: {e}"))?;
    let monitor = monitors
        .iter()
        .find(|m| m.is_primary().unwrap_or(false))
        .or_else(|| monitors.first())
        .ok_or_else(|| "Ekran bulunamadı".to_string())?;
    let image = monitor
        .capture_image()
        .map_err(|e| format!("Ekran yakalanamadı: {e}"))?;

    let name = format!("ekran_{}.png", timestamp_stamp());
    let path = safe_shared_file_path(&name)?;
    image
        .save_with_format(&path, image::ImageFormat::Png)
        .map_err(|e| format!("Resim kodlanamadı: {e}"))?;
    let size = std::fs::metadata(&path).map(|m| m.len() as i64).unwrap_or(0);
    Ok((name, size))
}

fn timestamp_stamp() -> String {
    // Local-time-free stamp; the file name only needs to be sortable and unique.
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default();
    let secs = now.as_secs();
    let millis = now.subsec_millis();
    format!("{secs}_{millis:03}")
}

fn get_usb_tethering_ip() -> String {
    // The Android USB tethering / hotspot adapters live on these well-known
    // subnets; the first non-loopback IPv4 on one of them is the phone's peer.
    const PREFIXES: [&str; 6] = [
        "192.168.42.",
        "192.168.43.",
        "192.168.49.",
        "192.168.225.",
        "192.168.137.",
        "172.20.10.",
    ];
    own_interface_ips()
        .into_iter()
        .find(|ip| PREFIXES.iter().any(|p| ip.starts_with(p)))
        .unwrap_or_default()
}

/// Run `adb reverse` for every media port, mirroring Go's `setupAdbReverse`.
fn setup_adb_reverse() -> (bool, String) {
    let candidates = ["adb", "adb.exe"];
    let mut adb = None;
    for candidate in candidates {
        if std::process::Command::new(candidate)
            .arg("version")
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .status()
            .map(|s| s.success())
            .unwrap_or(false)
        {
            adb = Some(candidate.to_string());
            break;
        }
    }
    let Some(adb) = adb else {
        return (
            false,
            "ADB bulunamadı. Lütfen Android SDK platform-tools veya ADB yükleyin.".to_string(),
        );
    };

    let mut bridged = 0;
    for port in [CONTROL_PORT, SCREEN_TCP_PORT, AUDIO_TCP_PORT, CAMERA_TCP_PORT] {
        let target = format!("tcp:{port}");
        if std::process::Command::new(&adb)
            .args(["reverse", &target, &target])
            .status()
            .map(|s| s.success())
            .unwrap_or(false)
        {
            bridged += 1;
        }
    }
    if bridged > 0 {
        (
            true,
            format!("USB Sıfır Gecikme Modu Aktif! ({bridged} port başarıyla USB'ye köprülendi)"),
        )
    } else {
        (
            false,
            "Cihaz bulunamadı. Lütfen telefonunuzu USB ile bağlayın ve USB Hata Ayıklama modunu açın."
                .to_string(),
        )
    }
}

/// Whether the process is running elevated.
#[cfg(windows)]
fn is_admin() -> bool {
    #[link(name = "shell32")]
    extern "system" {
        fn IsUserAnAdmin() -> i32;
    }
    unsafe { IsUserAnAdmin() != 0 }
}

#[cfg(not(windows))]
fn is_admin() -> bool {
    false
}

/// Toggle the virtual (usbmmidd) monitor, mirroring `setVirtualMonitor`.
fn set_virtual_monitor(enable: bool) {
    if !is_admin() {
        tracing::warn!("virtual monitor requires administrator rights");
        return;
    }
    let driver_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|p| p.join("usbmmidd")))
        .unwrap_or_else(|| PathBuf::from("usbmmidd"));
    let installer = driver_dir.join("deviceinstaller64.exe");
    if !installer.exists() {
        tracing::warn!("usbmmidd driver not installed at {}", installer.display());
        return;
    }
    let value = if enable { "1" } else { "0" };
    match std::process::Command::new(&installer)
        .args(["enableidd", value])
        .current_dir(&driver_dir)
        .spawn()
    {
        Ok(mut child) => {
            let _ = child.wait();
        }
        Err(err) => tracing::warn!("failed to toggle virtual monitor: {err}"),
    }
}

// ---------------------------------------------------------------------------
// Master volume (waveOut)
// ---------------------------------------------------------------------------

#[cfg(windows)]
fn volume_get() -> Option<u8> {
    use std::ffi::c_void;
    #[link(name = "winmm")]
    extern "system" {
        fn waveOutGetVolume(device: *mut c_void, volume: *mut u32) -> u32;
    }
    let mut raw: u32 = 0;
    let result = unsafe { waveOutGetVolume(std::ptr::null_mut(), &mut raw) };
    if result != 0 {
        return None;
    }
    let left = raw & 0xFFFF;
    Some(((left as u64 * 100) / 0xFFFF) as u8)
}

#[cfg(windows)]
fn volume_set(percent: u8) {
    use std::ffi::c_void;
    #[link(name = "winmm")]
    extern "system" {
        fn waveOutSetVolume(device: *mut c_void, volume: u32) -> u32;
    }
    let scaled = ((percent as u64 * 0xFFFF) / 100) as u32;
    let combined = scaled | (scaled << 16);
    unsafe {
        waveOutSetVolume(std::ptr::null_mut(), combined);
    }
}

#[cfg(not(windows))]
fn volume_get() -> Option<u8> {
    None
}

#[cfg(not(windows))]
fn volume_set(_percent: u8) {}

// ---------------------------------------------------------------------------
// Display brightness (DDC/CI, gamma-ramp fallback)
// ---------------------------------------------------------------------------

#[cfg(windows)]
fn set_display_brightness(level: u8) {
    if !set_brightness_ddc(level) {
        set_brightness_gamma(level);
    }
}

#[cfg(windows)]
fn set_brightness_ddc(level: u8) -> bool {
    use std::ffi::c_void;

    type Handle = *mut c_void;
    #[repr(C)]
    #[derive(Clone, Copy)]
    struct Rect {
        left: i32,
        top: i32,
        right: i32,
        bottom: i32,
    }
    #[repr(C)]
    #[derive(Clone, Copy)]
    struct PhysicalMonitor {
        handle: Handle,
        description: [u16; 128],
    }

    #[link(name = "user32")]
    extern "system" {
        fn EnumDisplayMonitors(
            hdc: Handle,
            clip: *mut Rect,
            callback: unsafe extern "system" fn(Handle, Handle, *mut Rect, isize) -> i32,
            data: isize,
        ) -> i32;
    }
    #[link(name = "dxva2")]
    extern "system" {
        fn GetNumberOfPhysicalMonitorsFromHMONITOR(monitor: Handle, count: *mut u32) -> i32;
        fn GetPhysicalMonitorsFromHMONITOR(
            monitor: Handle,
            count: u32,
            monitors: *mut PhysicalMonitor,
        ) -> i32;
        fn GetMonitorBrightness(
            monitor: Handle,
            minimum: *mut u32,
            current: *mut u32,
            maximum: *mut u32,
        ) -> i32;
        fn SetMonitorBrightness(monitor: Handle, level: u32) -> i32;
        fn DestroyPhysicalMonitors(count: u32, monitors: *mut PhysicalMonitor) -> i32;
    }

    static TARGET: AtomicU32 = AtomicU32::new(50);
    static APPLIED: AtomicU32 = AtomicU32::new(0);
    TARGET.store(level as u32, Ordering::Relaxed);
    APPLIED.store(0, Ordering::Relaxed);

    unsafe extern "system" fn callback(
        monitor: Handle,
        _hdc: Handle,
        _rect: *mut Rect,
        _data: isize,
    ) -> i32 {
        let mut count: u32 = 0;
        if GetNumberOfPhysicalMonitorsFromHMONITOR(monitor, &mut count) != 0 && count > 0 {
            let mut monitors = vec![
                PhysicalMonitor {
                    handle: std::ptr::null_mut(),
                    description: [0u16; 128],
                };
                count as usize
            ];
            if GetPhysicalMonitorsFromHMONITOR(monitor, count, monitors.as_mut_ptr()) != 0 {
                let target = TARGET.load(Ordering::Relaxed);
                for entry in monitors.iter() {
                    let (mut minimum, mut current, mut maximum) = (0u32, 0u32, 100u32);
                    if GetMonitorBrightness(
                        entry.handle,
                        &mut minimum,
                        &mut current,
                        &mut maximum,
                    ) != 0
                    {
                        let mut value = target;
                        if minimum < maximum {
                            value = value.clamp(minimum, maximum);
                        }
                        if SetMonitorBrightness(entry.handle, value) != 0 {
                            APPLIED.fetch_add(1, Ordering::Relaxed);
                        }
                    }
                }
                DestroyPhysicalMonitors(count, monitors.as_mut_ptr());
            }
        }
        1
    }

    unsafe {
        EnumDisplayMonitors(std::ptr::null_mut(), std::ptr::null_mut(), callback, 0);
    }
    APPLIED.load(Ordering::Relaxed) > 0
}

/// Software fallback for internal panels: scale the display gamma ramp.
#[cfg(windows)]
fn set_brightness_gamma(level: u8) {
    use std::ffi::c_void;
    type Handle = *mut c_void;

    #[link(name = "user32")]
    extern "system" {
        fn GetDC(hwnd: Handle) -> Handle;
        fn ReleaseDC(hwnd: Handle, hdc: Handle) -> i32;
    }
    #[link(name = "gdi32")]
    extern "system" {
        fn SetDeviceGammaRamp(hdc: Handle, ramp: *const u16) -> i32;
    }

    let scale = (level as u32).clamp(1, 100);
    let mut ramp = [0u16; 256 * 3];
    for i in 0..256u32 {
        let value = ((i * 65535 / 255) * scale / 100).min(65535) as u16;
        ramp[i as usize] = value;
        ramp[i as usize + 256] = value;
        ramp[i as usize + 512] = value;
    }
    unsafe {
        let hdc = GetDC(std::ptr::null_mut());
        if !hdc.is_null() {
            SetDeviceGammaRamp(hdc, ramp.as_ptr());
            ReleaseDC(std::ptr::null_mut(), hdc);
        }
    }
}

#[cfg(not(windows))]
fn set_display_brightness(level: u8) {
    let _ = level;
}

// ---------------------------------------------------------------------------
// Clipboard
// ---------------------------------------------------------------------------

#[cfg(windows)]
fn set_clipboard(text: &str) -> bool {
    use std::ffi::c_void;
    type Handle = *mut c_void;
    const CF_UNICODETEXT: u32 = 13;
    const GMEM_MOVEABLE: u32 = 0x0002;

    #[link(name = "user32")]
    extern "system" {
        fn OpenClipboard(hwnd: Handle) -> i32;
        fn CloseClipboard() -> i32;
        fn EmptyClipboard() -> i32;
        fn SetClipboardData(format: u32, data: Handle) -> Handle;
    }
    #[link(name = "kernel32")]
    extern "system" {
        fn GlobalAlloc(flags: u32, bytes: usize) -> Handle;
        fn GlobalLock(handle: Handle) -> *mut c_void;
        fn GlobalUnlock(handle: Handle) -> i32;
        fn GlobalFree(handle: Handle) -> Handle;
    }

    let wide: Vec<u16> = text.encode_utf16().chain(std::iter::once(0)).collect();
    unsafe {
        if OpenClipboard(std::ptr::null_mut()) == 0 {
            return false;
        }
        EmptyClipboard();
        let bytes = wide.len() * 2;
        let handle = GlobalAlloc(GMEM_MOVEABLE, bytes);
        if handle.is_null() {
            CloseClipboard();
            return false;
        }
        let pointer = GlobalLock(handle);
        if pointer.is_null() {
            GlobalFree(handle);
            CloseClipboard();
            return false;
        }
        std::ptr::copy_nonoverlapping(wide.as_ptr() as *const u8, pointer as *mut u8, bytes);
        GlobalUnlock(handle);
        // On success the clipboard owns the block; on failure we still do.
        let ok = !SetClipboardData(CF_UNICODETEXT, handle).is_null();
        if !ok {
            GlobalFree(handle);
        }
        CloseClipboard();
        ok
    }
}

#[cfg(windows)]
fn get_clipboard() -> Option<String> {
    use std::ffi::c_void;
    type Handle = *mut c_void;
    const CF_UNICODETEXT: u32 = 13;

    #[link(name = "user32")]
    extern "system" {
        fn OpenClipboard(hwnd: Handle) -> i32;
        fn CloseClipboard() -> i32;
        fn GetClipboardData(format: u32) -> Handle;
        fn IsClipboardFormatAvailable(format: u32) -> i32;
    }
    #[link(name = "kernel32")]
    extern "system" {
        fn GlobalLock(handle: Handle) -> *mut c_void;
        fn GlobalUnlock(handle: Handle) -> i32;
        fn GlobalSize(handle: Handle) -> usize;
    }

    unsafe {
        if OpenClipboard(std::ptr::null_mut()) == 0 {
            return None;
        }
        let mut result = None;
        if IsClipboardFormatAvailable(CF_UNICODETEXT) != 0 {
            let handle = GetClipboardData(CF_UNICODETEXT);
            if !handle.is_null() {
                let pointer = GlobalLock(handle) as *const u16;
                if !pointer.is_null() {
                    let length = GlobalSize(handle) / 2;
                    let slice = std::slice::from_raw_parts(pointer, length);
                    let end = slice.iter().position(|&c| c == 0).unwrap_or(slice.len());
                    result = Some(String::from_utf16_lossy(&slice[..end]));
                    GlobalUnlock(handle);
                }
            }
        }
        CloseClipboard();
        result
    }
}

#[cfg(not(windows))]
fn set_clipboard(_text: &str) -> bool {
    false
}

#[cfg(not(windows))]
fn get_clipboard() -> Option<String> {
    None
}

#[cfg(windows)]
fn empty_recycle_bin() {
    use std::ffi::c_void;
    const SHERB_NOCONFIRMATION: u32 = 0x1;
    const SHERB_NOPROGRESSUI: u32 = 0x2;
    const SHERB_NOSOUND: u32 = 0x4;

    #[link(name = "shell32")]
    extern "system" {
        fn SHEmptyRecycleBinW(hwnd: *mut c_void, path: *const u16, flags: u32) -> i32;
    }
    unsafe {
        SHEmptyRecycleBinW(
            std::ptr::null_mut(),
            std::ptr::null(),
            SHERB_NOCONFIRMATION | SHERB_NOPROGRESSUI | SHERB_NOSOUND,
        );
    }
}

#[cfg(not(windows))]
fn empty_recycle_bin() {}

// ---------------------------------------------------------------------------
// Notifications (Shell_NotifyIconW balloon on a hidden message window)
// ---------------------------------------------------------------------------

#[cfg(windows)]
fn show_notification(title: &str, message: &str) {
    use std::ffi::c_void;

    type Handle = *mut c_void;
    type WndProc = unsafe extern "system" fn(Handle, u32, usize, isize) -> isize;

    #[repr(C)]
    struct WndClassExW {
        cb_size: u32,
        style: u32,
        wnd_proc: Option<WndProc>,
        cls_extra: i32,
        wnd_extra: i32,
        instance: Handle,
        icon: Handle,
        cursor: Handle,
        background: Handle,
        menu_name: *const u16,
        class_name: *const u16,
        icon_small: Handle,
    }

    #[repr(C)]
    struct NotifyIconDataW {
        cb_size: u32,
        hwnd: Handle,
        id: u32,
        flags: u32,
        callback_message: u32,
        icon: Handle,
        tip: [u16; 128],
        state: u32,
        state_mask: u32,
        info: [u16; 256],
        version_or_timeout: u32,
        info_title: [u16; 64],
        info_flags: u32,
        guid_item: [u8; 16],
        balloon_icon: Handle,
    }

    #[link(name = "user32")]
    extern "system" {
        fn RegisterClassExW(class: *const WndClassExW) -> u16;
        fn CreateWindowExW(
            ex_style: u32,
            class_name: *const u16,
            window_name: *const u16,
            style: u32,
            x: i32,
            y: i32,
            width: i32,
            height: i32,
            parent: Handle,
            menu: Handle,
            instance: Handle,
            param: *mut c_void,
        ) -> Handle;
        fn DefWindowProcW(hwnd: Handle, msg: u32, wparam: usize, lparam: isize) -> isize;
        fn GetModuleHandleW(name: *const u16) -> Handle;
        fn LoadIconW(instance: Handle, name: *const u16) -> Handle;
        fn Shell_NotifyIconW(message: u32, data: *mut NotifyIconDataW) -> i32;
    }

    const NIM_ADD: u32 = 0x0;
    const NIM_MODIFY: u32 = 0x1;
    const NIF_ICON: u32 = 0x2;
    const NIF_TIP: u32 = 0x4;
    const NIF_INFO: u32 = 0x10;
    const NIIF_INFO: u32 = 0x1;
    const HWND_MESSAGE: isize = -3;
    /// `MAKEINTRESOURCE(IDI_APPLICATION)`.
    const IDI_APPLICATION: usize = 32512;

    static ADDED: AtomicBool = AtomicBool::new(false);

    unsafe extern "system" fn wnd_proc(hwnd: Handle, msg: u32, wparam: usize, lparam: isize) -> isize {
        DefWindowProcW(hwnd, msg, wparam, lparam)
    }

    fn wide(value: &str) -> Vec<u16> {
        value.encode_utf16().chain(std::iter::once(0)).collect()
    }

    fn copy_into(target: &mut [u16], source: &str) {
        for (slot, unit) in target.iter_mut().zip(source.encode_utf16()) {
            *slot = unit;
        }
    }

    static WINDOW: std::sync::LazyLock<usize> = std::sync::LazyLock::new(|| unsafe {
        let class_name = wide("BigPocketToastWnd");
        let instance = GetModuleHandleW(std::ptr::null());
        let class = WndClassExW {
            cb_size: std::mem::size_of::<WndClassExW>() as u32,
            style: 0,
            wnd_proc: Some(wnd_proc),
            cls_extra: 0,
            wnd_extra: 0,
            instance,
            icon: std::ptr::null_mut(),
            cursor: std::ptr::null_mut(),
            background: std::ptr::null_mut(),
            menu_name: std::ptr::null(),
            class_name: class_name.as_ptr(),
            icon_small: std::ptr::null_mut(),
        };
        RegisterClassExW(&class);
        let window_name = wide("BigPocket");
        CreateWindowExW(
            0,
            class_name.as_ptr(),
            window_name.as_ptr(),
            0,
            0,
            0,
            0,
            0,
            HWND_MESSAGE as Handle,
            std::ptr::null_mut(),
            instance,
            std::ptr::null_mut(),
        ) as usize
    });

    let hwnd = *WINDOW;

    if hwnd == 0 {
        tracing::info!("[notification] {title}: {message}");
        return;
    }

    unsafe {
        let mut data: NotifyIconDataW = std::mem::zeroed();
        data.cb_size = std::mem::size_of::<NotifyIconDataW>() as u32;
        data.hwnd = hwnd as Handle;
        data.id = 1;
        data.info_flags = NIIF_INFO;
        copy_into(&mut data.info_title, title);
        copy_into(&mut data.info, message);

        if ADDED.swap(true, Ordering::Relaxed) {
            data.flags = NIF_INFO;
            Shell_NotifyIconW(NIM_MODIFY, &mut data);
        } else {
            // The tray icon has to exist before a balloon can be attached to it.
            data.flags = NIF_ICON | NIF_TIP | NIF_INFO;
            data.icon = LoadIconW(std::ptr::null_mut(), IDI_APPLICATION as *const u16);
            copy_into(&mut data.tip, "BigPocket");
            Shell_NotifyIconW(NIM_ADD, &mut data);
        }
    }
}

#[cfg(not(windows))]
fn show_notification(title: &str, message: &str) {
    tracing::info!("[notification] {title}: {message}");
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

/// Bind `0.0.0.0:port` and serve the API + web UI until the task is aborted or
/// [`AppState::shutdown`] is notified.
pub fn start_server(state: Arc<AppState>, port: u16) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        let address = SocketAddr::from(([0, 0, 0, 0], port));
        let listener = match tokio::net::TcpListener::bind(address).await {
            Ok(listener) => listener,
            Err(err) => {
                tracing::error!("failed to bind {address}: {err}");
                return;
            }
        };
        tracing::info!("BigPocket control server listening on http://0.0.0.0:{port}");

        spawn_stats_ticker(state.clone());

        let app = router(state.clone()).into_make_service_with_connect_info::<SocketAddr>();
        if let Err(err) = axum::serve(listener, app)
            .with_graceful_shutdown(async move { state.shutdown.notified().await })
            .await
        {
            tracing::error!("server error: {err}");
        }
    })
}

/// Broadcast live system stats every 2 seconds while clients are connected.
fn spawn_stats_ticker(state: Arc<AppState>) {
    tokio::spawn(async move {
        let mut ticker = tokio::time::interval(Duration::from_secs(2));
        loop {
            ticker.tick().await;
            if state.client_count() == 0 {
                continue;
            }
            let stats = system_stats(&state).await;
            state.broadcast(stats);
        }
    });
}

// ---------------------------------------------------------------------------
// Windows autostart (HKCU Run key, written through reg.exe)
// ---------------------------------------------------------------------------

const AUTOSTART_REG_KEY: &str = r"HKCU\Software\Microsoft\Windows\CurrentVersion\Run";
const AUTOSTART_VALUE: &str = "BigPocket";

/// Command the Run key launches: the current executable, started minimized.
fn autostart_launch_command() -> String {
    let exe = std::env::current_exe()
        .map(|path| path.to_string_lossy().into_owned())
        .unwrap_or_default();
    format!("\"{exe}\" --minimized")
}

/// Whether BigPocket currently has a Run-key entry.
#[cfg(windows)]
fn autostart_status() -> bool {
    use std::os::windows::process::CommandExt;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    std::process::Command::new("reg")
        .args(["query", AUTOSTART_REG_KEY, "/v", AUTOSTART_VALUE])
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .map(|out| out.status.success())
        .unwrap_or(false)
}

#[cfg(not(windows))]
fn autostart_status() -> bool {
    false
}

/// Adds or removes the Run-key entry, so the panel survives a reboot.
#[cfg(windows)]
fn set_autostart(enable: bool) -> Result<(), String> {
    use std::os::windows::process::CommandExt;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    let output = if enable {
        let command = autostart_launch_command();
        std::process::Command::new("reg")
            .args([
                "add",
                AUTOSTART_REG_KEY,
                "/v",
                AUTOSTART_VALUE,
                "/t",
                "REG_SZ",
                "/d",
                command.as_str(),
                "/f",
            ])
            .creation_flags(CREATE_NO_WINDOW)
            .output()
    } else {
        std::process::Command::new("reg")
            .args(["delete", AUTOSTART_REG_KEY, "/v", AUTOSTART_VALUE, "/f"])
            .creation_flags(CREATE_NO_WINDOW)
            .output()
    };
    match output {
        Ok(out) if out.status.success() => Ok(()),
        // Deleting an entry that is already gone is a success, not an error.
        Ok(_) if !enable && !autostart_status() => Ok(()),
        Ok(out) => {
            let stderr = String::from_utf8_lossy(&out.stderr).trim().to_string();
            Err(if stderr.is_empty() {
                "Kayıt defteri güncellenemedi.".to_string()
            } else {
                stderr
            })
        }
        Err(err) => Err(err.to_string()),
    }
}

#[cfg(not(windows))]
fn set_autostart(_enable: bool) -> Result<(), String> {
    Err("Otomatik başlatma yalnızca Windows'ta desteklenir.".to_string())
}

/// `GET /api/autostart` — current Run-key state.
async fn api_autostart_get() -> Response {
    Json(json!({"success": true, "enabled": autostart_status()})).into_response()
}

/// `POST /api/autostart` — toggles the Run key and optionally stores the
/// security password the user set on the same screen.
async fn api_autostart_post(
    State(state): State<Arc<AppState>>,
    Json(body): Json<Value>,
) -> Response {
    let enable = body.get("enable").and_then(Value::as_bool).unwrap_or(true);

    if let Some(password) = body.get("password").and_then(Value::as_str) {
        if !password.is_empty() {
            let mut cfg = config::load_config();
            cfg.password = password.to_string();
            if let Err(err) = config::save_config(&cfg) {
                return json_response(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    json!({"success": false, "error": err}),
                );
            }
            state.broadcast(json!({"type": "config_update", "source": "autostart"}));
        }
    }

    match set_autostart(enable) {
        Ok(()) => Json(json!({"success": true, "enabled": enable})).into_response(),
        Err(err) => json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err}),
        ),
    }
}

async fn api_qr(Query(params): Query<HashMap<String, String>>) -> Response {
    let data = params.get("data").map(|s| s.as_str()).unwrap_or("BigPocket");
    let code = match qrcode::QrCode::new(data.as_bytes()) {
        Ok(c) => c,
        Err(_) => qrcode::QrCode::new(b"BigPocket").unwrap(),
    };
    let svg = code
        .render::<qrcode::render::svg::Color>()
        .min_dimensions(200, 200)
        .build();
    let mut resp = (StatusCode::OK, svg).into_response();
    resp.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("image/svg+xml"),
    );
    resp
}

#[derive(serde::Serialize, Clone)]
struct InstalledAppItem {
    name: String,
    path: String,
}

fn get_installed_apps() -> Vec<InstalledAppItem> {
    let mut apps = Vec::new();
    let mut seen = std::collections::HashSet::new();

    #[cfg(windows)]
    {
        let mut scan_dirs = Vec::new();
        if let Ok(prog_data) = std::env::var("ProgramData") {
            scan_dirs.push(PathBuf::from(prog_data).join(r"Microsoft\Windows\Start Menu\Programs"));
        }
        if let Ok(app_data) = std::env::var("APPDATA") {
            scan_dirs.push(PathBuf::from(app_data).join(r"Microsoft\Windows\Start Menu\Programs"));
        }
        for dir in scan_dirs {
            if !dir.exists() {
                continue;
            }
            if let Ok(entries) = std::fs::read_dir(dir) {
                for entry in entries.flatten() {
                    let path = entry.path();
                    if path.is_file() && path.extension().and_then(|s| s.to_str()).map(|s| s.eq_ignore_ascii_case("lnk")).unwrap_or(false) {
                        if let Some(stem) = path.file_stem().and_then(|s| s.to_str()) {
                            let lower = stem.to_ascii_lowercase();
                            if !lower.contains("uninstall") && !seen.contains(&lower) {
                                seen.insert(lower);
                                apps.push(InstalledAppItem {
                                    name: stem.to_string(),
                                    path: path.to_string_lossy().to_string(),
                                });
                            }
                        }
                    }
                }
            }
        }
    }

    #[cfg(target_os = "macos")]
    {
        for dir in ["/Applications", "/System/Applications"] {
            let p = std::path::Path::new(dir);
            if let Ok(entries) = std::fs::read_dir(p) {
                for entry in entries.flatten() {
                    let path = entry.path();
                    if path.is_dir() && path.extension().map(|s| s == "app").unwrap_or(false) {
                        if let Some(stem) = path.file_stem().and_then(|s| s.to_str()) {
                            let lower = stem.to_ascii_lowercase();
                            if !seen.contains(&lower) {
                                seen.insert(lower);
                                apps.push(InstalledAppItem {
                                    name: stem.to_string(),
                                    path: path.to_string_lossy().to_string(),
                                });
                            }
                        }
                    }
                }
            }
        }
    }

    #[cfg(all(unix, not(target_os = "macos")))]
    {
        let p = std::path::Path::new("/usr/share/applications");
        if let Ok(entries) = std::fs::read_dir(p) {
            for entry in entries.flatten() {
                let path = entry.path();
                if path.extension().map(|s| s == "desktop").unwrap_or(false) {
                    if let Some(stem) = path.file_stem().and_then(|s| s.to_str()) {
                        let lower = stem.to_ascii_lowercase();
                        if !seen.contains(&lower) {
                            seen.insert(lower);
                            apps.push(InstalledAppItem {
                                name: stem.to_string(),
                                path: path.to_string_lossy().to_string(),
                            });
                        }
                    }
                }
            }
        }
    }

    apps
}

fn get_driver_dir() -> PathBuf {
    if let Ok(exe) = std::env::current_exe() {
        if let Some(parent) = exe.parent() {
            let candidate = parent.join("usbmmidd");
            if candidate.exists() {
                return candidate;
            }
        }
    }
    PathBuf::from("usbmmidd")
}

async fn driver_status() -> Response {
    let monitors = xcap::Monitor::all().map(|m| m.len()).unwrap_or(1);
    let admin = is_admin();
    let driver_dir = get_driver_dir();
    let driver_exists = driver_dir.join("deviceinstaller64.exe").exists();
    json_response(StatusCode::OK, json!({
        "success": true,
        "admin": admin,
        "monitors": monitors,
        "driver_exists": driver_exists,
        "platform": std::env::consts::OS
    }))
}

async fn driver_install() -> Response {
    if !is_admin() {
        return json_response(StatusCode::OK, json!({
            "success": false,
            "message": "Yönetici yetkisi gerekmektedir."
        }));
    }
    let driver_dir = get_driver_dir();
    let installer = driver_dir.join("deviceinstaller64.exe");
    #[cfg(windows)]
    {
        if !installer.exists() {
            return json_response(StatusCode::OK, json!({
                "success": false,
                "message": format!("Sürücü kurulum dosyası (deviceinstaller64.exe) bulunamadı: {}", installer.display())
            }));
        }
        use std::os::windows::process::CommandExt;
        let output = std::process::Command::new(&installer)
            .args(["install", "usbmmidd.inf", "usbmmidd"])
            .current_dir(&driver_dir)
            .creation_flags(0x08000000)
            .output();
        match output {
            Ok(o) if o.status.success() => json_response(StatusCode::OK, json!({
                "success": true,
                "message": "Sürücü başarıyla yüklendi."
            })),
            Ok(o) => json_response(StatusCode::OK, json!({
                "success": false,
                "message": format!("Kurulum hatası: {}", String::from_utf8_lossy(&o.stderr))
            })),
            Err(e) => json_response(StatusCode::OK, json!({
                "success": false,
                "message": format!("Çalıştırma hatası: {e}")
            })),
        }
    }
    #[cfg(target_os = "macos")]
    {
        let _ = std::process::Command::new("open")
            .arg("x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture")
            .spawn();
        json_response(StatusCode::OK, json!({
            "success": true,
            "message": "macOS Ekran Kaydı izin penceresi açıldı. Lütfen BigPocket için izin verin."
        }))
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        json_response(StatusCode::OK, json!({
            "success": true,
            "message": "Linux üzerinde Wayland/PipeWire ekran yakalama protokolü aktiftir."
        }))
    }
}

async fn driver_enable(Json(payload): Json<serde_json::Value>) -> Response {
    let enable = payload.get("enable").and_then(|v| v.as_bool()).unwrap_or(true);
    set_virtual_monitor(enable);
    json_response(StatusCode::OK, json!({
        "success": true,
        "message": "Monitör durumu güncellendi."
    }))
}

async fn driver_mic_status() -> Response {
    #[cfg(windows)]
    let installed = std::path::Path::new(r"C:\Windows\System32\drivers\vbcable_x64.sys").exists()
        || std::path::Path::new(r"C:\Program Files\VB\CABLE").exists();
    #[cfg(not(windows))]
    let installed = true;

    json_response(StatusCode::OK, json!({
        "success": true,
        "installed": installed,
        "platform": std::env::consts::OS
    }))
}

async fn driver_mic_install() -> Response {
    if !is_admin() {
        return json_response(StatusCode::OK, json!({
            "success": false,
            "message": "Yönetici yetkisi gerekmektedir."
        }));
    }
    #[cfg(windows)]
    {
        let local_vbcable = PathBuf::from("VBCABLE").join("VBCABLE_Setup_x64.exe");
        if local_vbcable.exists() {
            let _ = std::process::Command::new(&local_vbcable)
                .spawn();
            json_response(StatusCode::OK, json!({
                "success": true,
                "message": "Sanal Mikrofon (VB-Cable) kurulum penceresi başlatıldı."
            }))
        } else {
            let _ = spawn_detached("https://vb-audio.com/Cable/");
            json_response(StatusCode::OK, json!({
                "success": true,
                "message": "VB-Cable indirme sayfası tarayıcıda açıldı."
            }))
        }
    }
    #[cfg(not(windows))]
    {
        json_response(StatusCode::OK, json!({
            "success": true,
            "message": "macOS/Linux üzerinde sanal ses yönlendirmesi için BlackHole veya PipeWire kullanabilirsiniz."
        }))
    }
}

async fn open_sound_settings_handler() -> Response {
    #[cfg(windows)]
    {
        let _ = spawn_detached("control.exe mmsys.cpl sounds");
    }
    #[cfg(target_os = "macos")]
    {
        let _ = std::process::Command::new("open").arg("x-apple.systempreferences:com.apple.preference.sound").spawn();
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        let _ = std::process::Command::new("pavucontrol").spawn();
    }
    json_response(StatusCode::OK, json!({"success": true}))
}

async fn installed_apps_handler() -> Response {
    let apps = get_installed_apps();
    json_response(StatusCode::OK, json!({
        "success": true,
        "apps": apps
    }))
}
