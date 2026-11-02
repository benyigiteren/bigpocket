//! Granular system API endpoints (HTTP) + MCP tool dispatch.
//!
//! These routes expose the telemetry, notification, clipboard, screen-capture,
//! installed-app and USB-forwarding behaviours that previously only existed as
//! WebSocket commands or as the coarse `/system_info` / `/installed_apps`
//! handlers. They are mounted into the protected router from [`crate::server`]:
//!
//! * [`system_router`] — sub-router nested at `/api/system` (`/stats`,
//!   `/network`, `/active_window`).
//! * [`system_routes`] — the remaining system routes (`/api/notify`,
//!   `/api/clipboard`, `/api/screen/capture`, `/api/apps`, `/api/usb/forward`).
//!
//! [`handle_mcp_tool`] lets the `/mcp` JSON-RPC router dispatch the
//! system-domain tools without duplicating any of this logic.

use std::sync::Arc;

use axum::extract::State;
use axum::http::StatusCode;
use axum::response::Response;
use axum::routing::{get, post};
use axum::{Json, Router};
use serde_json::{json, Value};

use crate::server::AppState;

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

/// Sub-router mounted at `/api/system` by [`crate::server::router`].
pub fn system_router() -> Router<Arc<AppState>> {
    Router::new()
        .route("/stats", get(system_stats_handler))
        .route("/network", get(system_network_handler))
        .route("/active_window", get(active_window_handler))
}

/// Root-level system routes merged into the protected router.
pub fn system_routes() -> Router<Arc<AppState>> {
    Router::new()
        .route("/api/notify", post(notify_handler))
        .route("/api/clipboard", post(clipboard_handler))
        .route("/api/screen/capture", post(screen_capture_handler))
        .route("/api/apps", get(apps_handler))
        .route("/api/usb/forward", post(usb_forward_handler))
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

/// `GET /api/system/stats` — full telemetry payload (CPU, RAM, disk, temp,
/// media and network rates).
async fn system_stats_handler(State(state): State<Arc<AppState>>) -> Response {
    let stats = crate::server::system_stats(&state).await;
    crate::server::json_response(StatusCode::OK, stats_json(&stats))
}

/// `GET /api/system/network` — instantaneous network throughput.
async fn system_network_handler(State(state): State<Arc<AppState>>) -> Response {
    let stats = crate::server::system_stats(&state).await;
    crate::server::json_response(StatusCode::OK, network_json(&stats))
}

/// `GET /api/system/active_window` — foreground window title + process.
async fn active_window_handler() -> Response {
    let info = tokio::task::spawn_blocking(active_window_info)
        .await
        .unwrap_or_else(|err| json!({"success": false, "error": err.to_string()}));
    crate::server::json_response(StatusCode::OK, info)
}

/// `POST /api/notify` — show a Windows balloon/toast notification.
async fn notify_handler(Json(body): Json<Value>) -> Response {
    let title = body
        .get("title")
        .and_then(Value::as_str)
        .unwrap_or("BigPocket");
    let message = body
        .get("message")
        .or_else(|| body.get("text"))
        .and_then(Value::as_str)
        .unwrap_or("");
    crate::server::show_notification(title, message);
    crate::server::json_response(StatusCode::OK, json!({"success": true}))
}

/// `POST /api/clipboard` — set the PC clipboard and mirror it over WebSocket.
async fn clipboard_handler(
    State(state): State<Arc<AppState>>,
    Json(body): Json<Value>,
) -> Response {
    let text = body.get("text").and_then(Value::as_str).unwrap_or("");
    let ok = crate::server::set_clipboard(text);
    state.broadcast(json!({"type": "clipboard_sync", "text": text}));
    crate::server::json_response(StatusCode::OK, json!({"success": ok, "text": text}))
}

/// `POST /api/screen/capture` — capture a monitor, return a base64 PNG data URL.
async fn screen_capture_handler(Json(body): Json<Value>) -> Response {
    let index = body
        .get("monitor")
        .and_then(Value::as_u64)
        .or_else(|| body.get("monitor_index").and_then(Value::as_u64))
        .map(|value| value as usize);

    match tokio::task::spawn_blocking(move || capture_png_base64(index)).await {
        Ok(Ok(image)) => crate::server::json_response(
            StatusCode::OK,
            json!({"success": true, "image": image}),
        ),
        Ok(Err(err)) => crate::server::json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err}),
        ),
        Err(err) => crate::server::json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err.to_string()}),
        ),
    }
}

/// `GET /api/apps` — alias of the existing `/installed_apps` handler.
async fn apps_handler() -> Response {
    crate::server::installed_apps_handler().await
}

/// `POST /api/usb/forward` — run `adb reverse` for every media port.
async fn usb_forward_handler() -> Response {
    let (success, message) = tokio::task::spawn_blocking(crate::server::setup_adb_reverse)
        .await
        .unwrap_or((false, "ADB köprüsü başlatılamadı.".to_string()));
    crate::server::json_response(StatusCode::OK, json!({"success": success, "message": message}))
}

// ---------------------------------------------------------------------------
// MCP dispatch
// ---------------------------------------------------------------------------

/// Handle one system-domain MCP tool call.
///
/// Returns `Some((json_text, is_error))` when `name` is handled here and `None`
/// otherwise, so the `/mcp` router can fall through to the deck/flow handlers.
pub async fn handle_mcp_tool(
    state: &Arc<AppState>,
    name: &str,
    args: &Value,
) -> Option<(String, bool)> {
    match name {
        "get_system_status" => {
            let text = response_text(crate::server::system_info().await).await;
            Some((text, false))
        }
        "get_system_stats" => {
            let stats = crate::server::system_stats(state).await;
            Some((stats_json(&stats).to_string(), false))
        }
        "get_network_stats" => {
            let stats = crate::server::system_stats(state).await;
            let mut out = network_json(&stats);
            if let Value::Object(map) = &mut out {
                for key in ["cpu", "cpu_temp", "ram"] {
                    map.insert(key.to_string(), stats.get(key).cloned().unwrap_or(Value::Null));
                }
            }
            Some((out.to_string(), false))
        }
        "net_speed" => {
            let stats = crate::server::system_stats(state).await;
            Some((network_json(&stats).to_string(), false))
        }
        "get_active_window" => {
            let info = tokio::task::spawn_blocking(active_window_info)
                .await
                .unwrap_or_else(|err| json!({"success": false, "error": err.to_string()}));
            Some((info.to_string(), false))
        }
        "list_installed_apps" => {
            let text = response_text(crate::server::installed_apps_handler().await).await;
            Some((text, false))
        }
        "send_pc_notification" => {
            let title = args
                .get("title")
                .and_then(Value::as_str)
                .unwrap_or("BigPocket");
            let message = args
                .get("message")
                .or_else(|| args.get("text"))
                .and_then(Value::as_str)
                .unwrap_or("");
            crate::server::show_notification(title, message);
            Some((json!({"success": true}).to_string(), false))
        }
        "sync_clipboard" => {
            let text = args.get("text").and_then(Value::as_str).unwrap_or("");
            let ok = crate::server::set_clipboard(text);
            state.broadcast(json!({"type": "clipboard_sync", "text": text}));
            Some((json!({"success": ok, "text": text}).to_string(), !ok))
        }
        "capture_screen" => {
            let index = args
                .get("monitor")
                .and_then(Value::as_u64)
                .or_else(|| args.get("monitor_index").and_then(Value::as_u64))
                .map(|value| value as usize);
            match tokio::task::spawn_blocking(move || capture_png_base64(index)).await {
                Ok(Ok(image)) => Some((json!({"success": true, "image": image}).to_string(), false)),
                Ok(Err(err)) => Some((json!({"success": false, "error": err}).to_string(), true)),
                Err(err) => Some((
                    json!({"success": false, "error": err.to_string()}).to_string(),
                    true,
                )),
            }
        }
        "set_autostart" => {
            let enable = args.get("enable").and_then(Value::as_bool).unwrap_or(true);
            match crate::server::set_autostart(enable) {
                Ok(()) => Some((json!({"success": true, "enabled": enable}).to_string(), false)),
                Err(err) => Some((json!({"success": false, "error": err}).to_string(), true)),
            }
        }
        "setup_usb_reverse" => {
            let (success, message) = tokio::task::spawn_blocking(crate::server::setup_adb_reverse)
                .await
                .unwrap_or((false, "ADB köprüsü başlatılamadı.".to_string()));
            Some((
                json!({"success": success, "message": message}).to_string(),
                !success,
            ))
        }
        _ => None,
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Wrap the telemetry payload in a `success`-tagged object while preserving
/// every field the WebSocket ticker already publishes.
fn stats_json(stats: &Value) -> Value {
    match stats {
        Value::Object(map) => {
            let mut out = map.clone();
            out.insert("success".to_string(), Value::Bool(true));
            Value::Object(out)
        }
        other => json!({"success": true, "stats": other}),
    }
}

/// Network throughput response, mirroring the WebSocket `net_speed` message.
fn network_json(stats: &Value) -> Value {
    let down = stats
        .get("net_down_kbps")
        .and_then(Value::as_u64)
        .unwrap_or(0);
    let up = stats
        .get("net_up_kbps")
        .and_then(Value::as_u64)
        .unwrap_or(0);
    json!({
        "success": true,
        "net_down_kbps": down,
        "net_up_kbps": up,
        "down_mbps": down as f64 / 1000.0,
        "up_mbps": up as f64 / 1000.0,
    })
}

/// Read an axum [`Response`] body as UTF-8 text (used for handler aliases).
async fn response_text(resp: Response) -> String {
    match axum::body::to_bytes(resp.into_body(), 8 * 1024 * 1024).await {
        Ok(bytes) => String::from_utf8_lossy(&bytes).into_owned(),
        Err(err) => json!({"success": false, "error": err.to_string()}).to_string(),
    }
}

/// Capture `index` (or the primary monitor when `None`) as a base64 PNG data URL.
fn capture_png_base64(index: Option<usize>) -> Result<String, String> {
    let monitors = xcap::Monitor::all().map_err(|err| format!("Ekran bulunamadı: {err}"))?;
    let monitor = match index {
        Some(i) => monitors
            .get(i)
            .ok_or_else(|| format!("Monitör bulunamadı: {i}"))?,
        None => monitors
            .iter()
            .find(|monitor| monitor.is_primary().unwrap_or(false))
            .or_else(|| monitors.first())
            .ok_or_else(|| "Ekran bulunamadı".to_string())?,
    };

    let image = monitor
        .capture_image()
        .map_err(|err| format!("Ekran yakalanamadı: {err}"))?;

    let mut png: Vec<u8> = Vec::new();
    image::DynamicImage::ImageRgba8(image)
        .write_to(&mut std::io::Cursor::new(&mut png), image::ImageFormat::Png)
        .map_err(|err| format!("PNG kodlanamadı: {err}"))?;

    Ok(format!("data:image/png;base64,{}", base64_encode(&png)))
}

/// Minimal standard base64 encoder (avoids pulling in a new dependency).
fn base64_encode(input: &[u8]) -> String {
    const TABLE: &[u8; 64] =
        b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity((input.len() + 2) / 3 * 4);
    for chunk in input.chunks(3) {
        let b0 = chunk[0] as u32;
        let b1 = *chunk.get(1).unwrap_or(&0) as u32;
        let b2 = *chunk.get(2).unwrap_or(&0) as u32;
        let triple = (b0 << 16) | (b1 << 8) | b2;
        out.push(TABLE[((triple >> 18) & 63) as usize] as char);
        out.push(TABLE[((triple >> 12) & 63) as usize] as char);
        if chunk.len() > 1 {
            out.push(TABLE[((triple >> 6) & 63) as usize] as char);
        } else {
            out.push('=');
        }
        if chunk.len() > 2 {
            out.push(TABLE[(triple & 63) as usize] as char);
        } else {
            out.push('=');
        }
    }
    out
}

/// Title, process name and executable path of the foreground window.
#[cfg(windows)]
fn active_window_info() -> Value {
    use std::ffi::c_void;
    type Handle = *mut c_void;
    const PROCESS_QUERY_LIMITED_INFORMATION: u32 = 0x1000;

    #[link(name = "user32")]
    extern "system" {
        fn GetForegroundWindow() -> Handle;
        fn GetWindowTextW(hwnd: Handle, text: *mut u16, count: i32) -> i32;
        fn GetWindowThreadProcessId(hwnd: Handle, pid: *mut u32) -> u32;
    }
    #[link(name = "kernel32")]
    extern "system" {
        fn OpenProcess(access: u32, inherit: i32, pid: u32) -> Handle;
        fn QueryFullProcessImageNameW(
            process: Handle,
            flags: u32,
            name: *mut u16,
            size: *mut u32,
        ) -> i32;
        fn CloseHandle(handle: Handle) -> i32;
    }

    unsafe {
        let hwnd = GetForegroundWindow();
        if hwnd.is_null() {
            return json!({"success": true, "title": "", "process": "", "exe": ""});
        }

        let mut title_buf = [0u16; 512];
        let title_len =
            GetWindowTextW(hwnd, title_buf.as_mut_ptr(), title_buf.len() as i32).max(0) as usize;
        let title = String::from_utf16_lossy(&title_buf[..title_len]);

        let mut pid: u32 = 0;
        GetWindowThreadProcessId(hwnd, &mut pid);

        let mut exe = String::new();
        if pid != 0 {
            let process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
            if !process.is_null() {
                let mut path_buf = [0u16; 1024];
                let mut size = path_buf.len() as u32;
                if QueryFullProcessImageNameW(process, 0, path_buf.as_mut_ptr(), &mut size) != 0 {
                    exe = String::from_utf16_lossy(&path_buf[..size as usize]);
                }
                CloseHandle(process);
            }
        }

        let process = std::path::Path::new(&exe)
            .file_stem()
            .map(|stem| stem.to_string_lossy().into_owned())
            .unwrap_or_default();

        json!({"success": true, "title": title, "process": process, "exe": exe})
    }
}

#[cfg(not(windows))]
fn active_window_info() -> Value {
    json!({"success": true, "title": "", "process": "", "exe": ""})
}
