//! MCP (Model Context Protocol) JSON-RPC 2.0 endpoint.
//!
//! The Go bridge (`mcp/main.go`) forwards every JSON-RPC envelope it receives on
//! stdio to `POST /mcp` and relays the answer verbatim, so this module is the
//! single source of truth for the tool registry and the dispatch table:
//!
//! * `tools/list`  → the 34 BigPocket tool definitions (name/description/inputSchema)
//! * `tools/call`  → routed to [`crate::deck_api::handle_mcp_tool`] /
//!   [`crate::system_api::handle_mcp_tool`], which own the actual behaviour.
//!
//! `initialize`, `notifications/initialized` and `ping` are answered here as
//! well, because the bridge forwards those too and only falls back to its local
//! registry when this endpoint is unreachable.
//!
//! `GET /mcp` with `Accept: text/event-stream` opens the SSE transport mandated
//! by the MCP HTTP spec; `POST /mcp` carries the JSON-RPC payload.

use std::convert::Infallible;
use std::sync::Arc;
use std::time::Duration;

use axum::body::{Body, Bytes};
use axum::extract::State;
use axum::http::{header, HeaderMap, HeaderValue, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::get;
use axum::{Json, Router};
use futures_util::StreamExt;
use serde_json::{json, Map, Value};

use crate::server::AppState;

/// MCP protocol revision this server speaks.
const MCP_PROTOCOL_VERSION: &str = "2024-11-05";
const MCP_SERVER_NAME: &str = "bigpocket-streamdeck-mcp";
const MCP_SERVER_VERSION: &str = "1.1.0";

/// Keepalive cadence for idle SSE transports. MCP clients/proxies drop a silent
/// stream long before the phone or browser gives up on the socket.
const SSE_KEEPALIVE: Duration = Duration::from_secs(15);

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

/// Build the MCP sub-router, nested into the authenticated `/config`-level router
/// in [`crate::server::router`] (so loopback MCP bridges are auto-trusted while
/// LAN peers still need the password). Returns an unstated router; `server.rs`
/// merges it before `.with_state(state)`.
pub fn mcp_router() -> Router<Arc<AppState>> {
    Router::new().route("/mcp", get(sse_handler).post(jsonrpc_handler))
}

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 envelope
// ---------------------------------------------------------------------------

#[derive(Debug, serde::Deserialize)]
struct RpcRequest {
    #[serde(default)]
    id: Option<Value>,
    #[serde(default)]
    method: String,
    #[serde(default)]
    params: Value,
}

fn rpc_error(id: Value, code: i64, message: &str, data: Option<Value>) -> Response {
    let mut error = json!({"code": code, "message": message});
    if let (Some(data), Some(obj)) = (data, error.as_object_mut()) {
        obj.insert("data".to_string(), data);
    }
    Json(json!({"jsonrpc": "2.0", "id": id, "error": error})).into_response()
}

/// `POST /mcp` — parse a JSON-RPC 2.0 request (single envelope or a batch) and
/// answer it.
async fn jsonrpc_handler(State(state): State<Arc<AppState>>, body: Bytes) -> Response {
    let parsed: Value = match serde_json::from_slice(&body) {
        Ok(value) => value,
        Err(err) => {
            return rpc_error(Value::Null, -32700, "Parse error", Some(json!(err.to_string())));
        }
    };

    match parsed {
        Value::Array(requests) => {
            let mut responses = Vec::with_capacity(requests.len());
            for request in &requests {
                if let Some(response) = handle_one(&state, request).await {
                    responses.push(response);
                }
            }
            if responses.is_empty() {
                // A batch made only of notifications.
                return StatusCode::NO_CONTENT.into_response();
            }
            Json(Value::Array(responses)).into_response()
        }
        single => match handle_one(&state, &single).await {
            Some(response) => Json(response).into_response(),
            None => StatusCode::NO_CONTENT.into_response(),
        },
    }
}

/// Handle one JSON-RPC envelope. Returns `None` for notifications (no `id`),
/// which must be answered with an empty HTTP response.
async fn handle_one(state: &Arc<AppState>, raw: &Value) -> Option<Value> {
    let request: RpcRequest = match serde_json::from_value(raw.clone()) {
        Ok(request) => request,
        Err(err) => {
            return Some(json!({
                "jsonrpc": "2.0",
                "id": Value::Null,
                "error": {"code": -32600, "message": "Invalid Request", "data": err.to_string()},
            }));
        }
    };

    let id = request.id.clone();

    // Notifications (no id) never produce a response body.
    let Some(id) = id else {
        // `notifications/initialized` is the only one the bridge forwards.
        return None;
    };

    match request.method.as_str() {
        "initialize" => Some(json!({
            "jsonrpc": "2.0",
            "id": id,
            "result": {
                "protocolVersion": MCP_PROTOCOL_VERSION,
                "capabilities": {"tools": {"listChanged": false}},
                "serverInfo": {"name": MCP_SERVER_NAME, "version": MCP_SERVER_VERSION},
            },
        })),
        "ping" => Some(json!({"jsonrpc": "2.0", "id": id, "result": {}})),
        "tools/list" => Some(json!({
            "jsonrpc": "2.0",
            "id": id,
            "result": {"tools": tool_definitions()},
        })),
        "tools/call" => {
            let name = request
                .params
                .get("name")
                .and_then(Value::as_str)
                .unwrap_or_default();
            let empty = Map::new();
            let args = request
                .params
                .get("arguments")
                .and_then(Value::as_object)
                .unwrap_or(&empty);
            let args = Value::Object(args.clone());

            let (text, is_error) = dispatch_tool(state, name, &args).await;
            Some(json!({
                "jsonrpc": "2.0",
                "id": id,
                "result": {
                    "content": [{"type": "text", "text": text}],
                    "isError": is_error,
                },
            }))
        }
        other => Some(json!({
            "jsonrpc": "2.0",
            "id": id,
            "error": {"code": -32601, "message": "Method not found", "data": format!("Unsupported method: {other}")},
        })),
    }
}

/// Route one `tools/call` to the module that owns the behaviour. The deck and
/// system modules return `None` for tools outside their scope, so the chain is
/// exhaustive without a second hand-maintained name table.
async fn dispatch_tool(state: &Arc<AppState>, name: &str, args: &Value) -> (String, bool) {
    if name.is_empty() {
        return ("Missing tool name in `params.name`".to_string(), true);
    }
    if let Some(result) = crate::deck_api::handle_mcp_tool(state, name, args).await {
        return result;
    }
    if let Some(result) = crate::system_api::handle_mcp_tool(state, name, args).await {
        return result;
    }
    (
        format!("Unknown tool '{name}': it is not part of the BigPocket toolset."),
        true,
    )
}

// ---------------------------------------------------------------------------
// SSE transport (GET /mcp)
// ---------------------------------------------------------------------------

/// `GET /mcp`. With `Accept: text/event-stream` this opens the MCP SSE
/// transport: the server announces the JSON-RPC message endpoint and then keeps
/// the connection alive. Requests still arrive on `POST /mcp`.
async fn sse_handler(headers: HeaderMap) -> Response {
    let wants_sse = headers
        .get(header::ACCEPT)
        .and_then(|v| v.to_str().ok())
        .map(|v| v.contains("text/event-stream"))
        .unwrap_or(false);

    if !wants_sse {
        return Json(json!({
            "jsonrpc": "2.0",
            "transport": "http",
            "endpoint": "/mcp",
            "message": "POST JSON-RPC 2.0 to /mcp, or reconnect with Accept: text/event-stream for SSE.",
        }))
        .into_response();
    }

    // First frame per the MCP SSE spec: the URL the client must POST to.
    let initial = futures_util::stream::once(async {
        Ok::<Bytes, Infallible>(Bytes::from_static(b"event: endpoint\ndata: /mcp\n\n"))
    });
    let keepalive = futures_util::stream::unfold((), |()| async {
        tokio::time::sleep(SSE_KEEPALIVE).await;
        Some((Ok::<Bytes, Infallible>(Bytes::from_static(b": keepalive\n\n")), ()))
    });

    let mut response = Response::new(Body::from_stream(initial.chain(keepalive)));
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("text/event-stream"),
    );
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-cache"));
    response
        .headers_mut()
        .insert(header::CONNECTION, HeaderValue::from_static("keep-alive"));
    response
}

// ---------------------------------------------------------------------------
// Tool registry
// ---------------------------------------------------------------------------

fn prop(kind: &str, desc: &str) -> Value {
    json!({"type": kind, "description": desc})
}

fn enum_prop(kind: &str, values: &[&str], desc: &str) -> Value {
    json!({"type": kind, "enum": values, "description": desc})
}

fn schema(props: Map<String, Value>, required: &[&str]) -> Value {
    let mut obj = Map::new();
    obj.insert("type".to_string(), Value::String("object".to_string()));
    obj.insert("properties".to_string(), Value::Object(props));
    if !required.is_empty() {
        obj.insert(
            "required".to_string(),
            Value::Array(required.iter().map(|r| Value::String((*r).to_string())).collect()),
        );
    }
    Value::Object(obj)
}

/// Shared visual/behaviour properties accepted by every button/widget tool.
///
/// The `type`/`value` pairing is the contract that the widget engine renders
/// from, so the concrete accepted values are spelled out here:
///
/// * `hotkey`           — `value` is a key combo, e.g. `"ctrl+shift+m"`, `"alt+f4"`.
/// * `command`          — `value` is an executable/command, e.g. `"calc.exe"`, `"notepad.exe"`.
/// * `url`              — `value` is a link, e.g. `"https://example.com"`.
/// * `text_snippet`     — `value` is the literal text to type.
/// * `toggle`           — `value` is `"mic_mute"`, `"sound_mute"` or `"virtual_monitor"`.
/// * `volume_slider`    — `value` is `"master_volume"`.
/// * `app_volume_slider`— `value` is the target process/executable name.
/// * `brightness_slider`— `value` is `"brightness"`.
/// * `live_info`        — `value` is `"cpu"`, `"ram"`, `"disk"`, `"gpu"`, `"cpu_temp"`, `"net_speed"` or `"combo"`.
/// * `clock_widget`     — `value` is `"clock"`; style comes from `clock_style`.
/// * `weather`          — `value` is `"weather"`.
/// * `now_playing`      — `value` is `"now_playing"`.
/// * `media_widget`     — `value` is `"media_control"`.
/// * `pomodoro`         — `value` is `"pomodoro"`.
/// * `window_action`    — `value` is `"maximize"`, `"minimize"`, `"restore"`,
///                        `"snap_left"`, `"snap_right"`, `"snap_top"`, `"close"` or `"next_monitor"`.
/// * `system_action`    — `value` is `"sleep"`, `"restart"`, `"shutdown"`, `"lock"`,
///                        `"empty_recycle_bin"` or `"screenshot"`.
fn widget_props() -> Map<String, Value> {
    let mut props = Map::new();
    props.insert("label".to_string(), prop("string", "Display label shown on the card."));
    props.insert(
        "type".to_string(),
        enum_prop(
            "string",
            &[
                "hotkey",
                "command",
                "url",
                "text_snippet",
                "toggle",
                "volume_slider",
                "app_volume_slider",
                "brightness_slider",
                "live_info",
                "clock_widget",
                "weather",
                "now_playing",
                "media_widget",
                "pomodoro",
                "window_action",
                "system_action",
            ],
            "Widget/action kind. Determines how `value` is interpreted.",
        ),
    );
    props.insert(
        "value".to_string(),
        prop(
            "string",
            "Kind-dependent parameter: hotkey combo ('ctrl+shift+m'), command ('calc.exe'), URL ('https://...'), snippet text, toggle target ('mic_mute'|'sound_mute'|'virtual_monitor'), slider target ('master_volume'|'brightness'), live_info metric ('cpu'|'ram'|'disk'|'gpu'|'cpu_temp'|'net_speed'|'combo'), widget marker ('clock'|'weather'|'now_playing'|'media_control'|'pomodoro'), window action ('maximize'|'minimize'|'restore'|'snap_left'|'snap_right'|'snap_top'|'close'|'next_monitor'), or system action ('sleep'|'restart'|'shutdown'|'lock'|'empty_recycle_bin'|'screenshot').",
        ),
    );
    props.insert(
        "icon".to_string(),
        prop(
            "string",
            "Icon key ('mic', 'volume', 'play', 'calc', 'web', 'lock', 'task', 'cpu', 'ram', 'clock', 'desktop', 'link', 'terminal', 'folder', 'wifi', 'monitor', 'bell') or a custom path ('custom:icons/file.png').",
        ),
    );
    props.insert("page".to_string(), prop("integer", "Page index (0-based)."));
    props.insert("col_span".to_string(), prop("integer", "Width span in grid cells (1 to 4)."));
    props.insert("row_span".to_string(), prop("integer", "Height span in grid cells (1 to 2)."));
    props.insert(
        "color".to_string(),
        prop("string", "Accent hex color (e.g. '#818CF8', '#10B981', '#38BDF8')."),
    );
    props.insert(
        "bg_style".to_string(),
        enum_prop("string", &["default", "transparent", "glass", "outline"], "Card background style."),
    );
    props.insert(
        "font_family".to_string(),
        enum_prop("string", &["mono", "sans", "lcd", "serif", "tech"], "Typography style."),
    );
    props.insert(
        "clock_style".to_string(),
        enum_prop(
            "string",
            &[
                "apple_minimal",
                "apple_lockscreen",
                "apple_stacked",
                "flip_clock",
                "neon_cyberpunk",
                "analog",
                "digital_seconds",
                "digital_clean",
            ],
            "Clock face style (clock_widget only).",
        ),
    );
    props.insert(
        "clock_font_size".to_string(),
        enum_prop("string", &["auto", "compact", "regular", "large", "huge"], "Clock text size."),
    );
    props
}

fn with_preset(mut props: Map<String, Value>) -> Map<String, Value> {
    props.insert(
        "preset".to_string(),
        prop(
            "string",
            "Optional preset key to seed defaults from ('clock', 'clock_analog', 'clock_seconds', 'mini_clock', 'telemetry_combo', 'cpu', 'ram', 'volume', 'app_session', 'brightness', 'media', 'toggle_mic', 'toggle_mute', 'toggle_monitor', 'app_calc', 'app_lock', 'app_terminal', 'app_discord', 'app_spotify').",
        ),
    );
    props
}

fn button_ref(desc: &str) -> Value {
    prop("integer", desc)
}

/// The 34 tools exposed to MCP clients.
fn tool_definitions() -> Vec<Value> {
    vec![
        json!({
            "name": "get_system_status",
            "description": "Gets BigPocket PC server status, IP addresses, ports, and system resource statistics.",
            "inputSchema": schema(Map::new(), &[]),
        }),
        json!({
            "name": "get_streamdeck_config",
            "description": "Retrieves current Stream Deck configuration, grid layout (rows, cols), active page, all pages, and all buttons with full properties.",
            "inputSchema": schema(Map::new(), &[]),
        }),
        json!({
            "name": "get_streamdeck_presets",
            "description": "Returns all supported widget types, presets, clock styles, font families, background styles and studio templates.",
            "inputSchema": schema(Map::new(), &[]),
        }),
        json!({
            "name": "set_streamdeck_button",
            "description": "Edits an existing button/widget in place with extreme precision (label, type, value, icon, page, col_span, row_span, color, bg_style, clock_style, font_family, clock_font_size). Unspecified fields are preserved.",
            "inputSchema": schema({
                let mut p = widget_props();
                p.insert("button_id".to_string(), button_ref("Existing button ID to edit."));
                p
            }, &["button_id"]),
        }),
        json!({
            "name": "add_streamdeck_button",
            "description": "Creates and appends a brand new button/widget to a page, optionally based on a preset ('clock', 'clock_analog', 'clock_seconds', 'mini_clock', 'telemetry_combo', 'cpu', 'ram', 'volume', 'app_session', 'brightness', 'media', 'toggle_mic', 'toggle_mute', 'toggle_monitor', 'app_calc', 'app_lock', 'app_terminal', 'app_discord', 'app_spotify').",
            "inputSchema": schema(with_preset(widget_props()), &[]),
        }),
        json!({
            "name": "create_custom_widget",
            "description": "Convenience tool that builds a customized widget/button from scratch (hotkey, command, URL, snippet, live info, clock, volume/brightness slider, toggle or media) with full visual parameters.",
            "inputSchema": schema(widget_props(), &["label", "type", "value"]),
        }),
        json!({
            "name": "batch_create_streamdeck_buttons",
            "description": "Creates multiple buttons/widgets in one single operation. Ideal for generating entire dashboards (gaming, coding, streaming, audio control).",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("page".to_string(), prop("integer", "Page index to add buttons to (defaults to the active page)."));
                p.insert("clear_page_first".to_string(), prop("boolean", "If true, clears existing buttons on the target page first."));
                p.insert("buttons".to_string(), prop("array", "Array of button/widget definitions (preset key or full property objects)."));
                p
            }, &["buttons"]),
        }),
        json!({
            "name": "duplicate_streamdeck_button",
            "description": "Duplicates an existing button to a target page or the active page.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("button_id".to_string(), button_ref("Button ID to duplicate."));
                p.insert("target_page".to_string(), prop("integer", "Target page index (optional, defaults to the same page)."));
                p
            }, &["button_id"]),
        }),
        json!({
            "name": "delete_streamdeck_button",
            "description": "Deletes a specific button by its ID.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("button_id".to_string(), button_ref("The button ID to remove."));
                p
            }, &["button_id"]),
        }),
        json!({
            "name": "swap_streamdeck_buttons",
            "description": "Swaps the visual position of two buttons.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("button_id_1".to_string(), button_ref("First button ID."));
                p.insert("button_id_2".to_string(), button_ref("Second button ID."));
                p
            }, &["button_id_1", "button_id_2"]),
        }),
        json!({
            "name": "set_grid_layout",
            "description": "Changes Stream Deck grid dimensions (number of rows and columns) and updates the layout.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("rows".to_string(), prop("integer", "Number of rows (1 to 5)."));
                p.insert("cols".to_string(), prop("integer", "Number of columns (2 to 8)."));
                p
            }, &["rows", "cols"]),
        }),
        json!({
            "name": "trigger_button",
            "description": "Directly triggers and executes a button action on the PC (simulates its hotkey or runs its command).",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("button_id".to_string(), button_ref("The button ID to trigger."));
                p
            }, &["button_id"]),
        }),
        json!({
            "name": "streamdeck_toggle_button",
            "description": "Toggles the state ('on' <-> 'off') and triggers the action of a toggle-type button (mic mute, speaker mute, virtual monitor or custom).",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("button_id".to_string(), button_ref("The toggle button ID to toggle."));
                p
            }, &["button_id"]),
        }),
        json!({
            "name": "streamdeck_switch_page",
            "description": "Switches the active Stream Deck tab/page across mobile and desktop interfaces.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("page".to_string(), prop("integer", "Page index (0, 1, 2...)."));
                p
            }, &["page"]),
        }),
        json!({
            "name": "pomodoro_toggle",
            "description": "Starts, pauses or resets a Pomodoro countdown widget on the Stream Deck canvas and returns its remaining time.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("button_id".to_string(), prop("integer", "The Pomodoro widget button ID."));
                p.insert("action".to_string(), enum_prop("string", &["start", "pause", "reset", "toggle"], "Countdown action (defaults to 'toggle')."));
                p
            }, &[]),
        }),
        json!({
            "name": "create_streamdeck_page",
            "description": "Creates a brand new Stream Deck page tab.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("title".to_string(), prop("string", "Title of the new page (e.g. 'Gaming Desk', 'Live Stream')."));
                p
            }, &["title"]),
        }),
        json!({
            "name": "rename_streamdeck_page",
            "description": "Renames an existing Stream Deck page.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("page_id".to_string(), prop("integer", "The page ID to rename."));
                p.insert("title".to_string(), prop("string", "New title for the page."));
                p
            }, &["page_id", "title"]),
        }),
        json!({
            "name": "delete_streamdeck_page",
            "description": "Deletes a Stream Deck page and all buttons belonging to it.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("page_id".to_string(), prop("integer", "The page ID to delete."));
                p
            }, &["page_id"]),
        }),
        json!({
            "name": "clear_streamdeck_page",
            "description": "Clears all buttons on a specific page.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("page_id".to_string(), prop("integer", "The page ID to clear (defaults to the active page when omitted)."));
                p
            }, &[]),
        }),
        json!({
            "name": "apply_streamdeck_template",
            "description": "Applies a curated studio template to a page ('pro_studio', 'media_lounge', 'gaming_obs', 'developer', 'system_control').",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("template".to_string(), enum_prop("string", &["pro_studio", "media_lounge", "gaming_obs", "developer", "system_control"], "Template design name."));
                p.insert("page".to_string(), prop("integer", "Page index to apply the template to (defaults to the active page)."));
                p
            }, &["template"]),
        }),
        json!({
            "name": "streamdeck_set_volume",
            "description": "Controls the master PC volume ('up', 'down', 'mute' or 'set' with a percentage 0-100).",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("action".to_string(), enum_prop("string", &["set", "up", "down", "mute"], "Volume action to perform."));
                p.insert("level".to_string(), prop("number", "Volume percentage (0-100), used when action is 'set'."));
                p
            }, &["action"]),
        }),
        json!({
            "name": "streamdeck_set_brightness",
            "description": "Controls the PC display brightness percentage (0-100).",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("level".to_string(), prop("integer", "Brightness percentage (0-100)."));
                p
            }, &["level"]),
        }),
        json!({
            "name": "list_installed_apps",
            "description": "Fetches the list of all installed Windows applications and desktop shortcuts with their paths.",
            "inputSchema": schema(Map::new(), &[]),
        }),
        json!({
            "name": "send_pc_notification",
            "description": "Displays a desktop toast/balloon notification on the PC.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("title".to_string(), prop("string", "Notification title."));
                p.insert("message".to_string(), prop("string", "Notification body text."));
                p
            }, &["title", "message"]),
        }),
        json!({
            "name": "sync_clipboard",
            "description": "Sets the PC clipboard text and syncs it with connected mobile devices.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("text".to_string(), prop("string", "The text to copy to the PC clipboard."));
                p
            }, &["text"]),
        }),
        json!({
            "name": "get_system_stats",
            "description": "Retrieves instantaneous real-time PC CPU and RAM usage percentages.",
            "inputSchema": schema(Map::new(), &[]),
        }),
        json!({
            "name": "get_network_stats",
            "description": "Retrieves live network throughput (download/upload kbps) plus CPU, RAM and CPU temperature of the PC.",
            "inputSchema": schema(Map::new(), &[]),
        }),
        json!({
            "name": "net_speed",
            "description": "Returns the instantaneous network throughput of the PC as net_down_kbps / net_up_kbps (kilobits per second) for display in the network speed widget.",
            "inputSchema": schema(Map::new(), &[]),
        }),
        json!({
            "name": "get_active_window",
            "description": "Returns the title and process name of the Windows application currently in the foreground.",
            "inputSchema": schema(Map::new(), &[]),
        }),
        json!({
            "name": "window_action",
            "description": "Runs a window-management action on the current foreground window: maximize, minimize, restore, snap_left, snap_right, snap_top, close or next_monitor.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert(
                    "action".to_string(),
                    enum_prop(
                        "string",
                        &[
                            "maximize",
                            "minimize",
                            "restore",
                            "snap_left",
                            "snap_right",
                            "snap_top",
                            "close",
                            "next_monitor",
                        ],
                        "Window action to run on the foreground window.",
                    ),
                );
                p
            }, &["action"]),
        }),
        json!({
            "name": "system_action",
            "description": "Runs a system power/utility action: sleep, restart, shutdown, lock, empty_recycle_bin or screenshot.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert(
                    "action".to_string(),
                    enum_prop(
                        "string",
                        &[
                            "sleep",
                            "restart",
                            "shutdown",
                            "lock",
                            "empty_recycle_bin",
                            "screenshot",
                        ],
                        "System action to run.",
                    ),
                );
                p
            }, &["action"]),
        }),
        json!({
            "name": "capture_screen",
            "description": "Captures a screenshot of the PC display (or a specific monitor) and returns it as a base64 PNG image.",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("monitor".to_string(), prop("integer", "Monitor index to capture (0-based, defaults to the primary monitor)."));
                p.insert("save_path".to_string(), prop("string", "Optional absolute path to also save the PNG file on disk."));
                p
            }, &[]),
        }),
        json!({
            "name": "set_autostart",
            "description": "Enables or disables launching BigPocket automatically with Windows (HKCU Run key).",
            "inputSchema": schema({
                let mut p = Map::new();
                p.insert("enable".to_string(), prop("boolean", "True to launch BigPocket on Windows startup, false to disable it."));
                p
            }, &["enable"]),
        }),
        json!({
            "name": "setup_usb_reverse",
            "description": "Sets up ADB reverse port forwarding over a physical USB cable for 1ms ultra-low latency.",
            "inputSchema": schema(Map::new(), &[]),
        }),
    ]
}
