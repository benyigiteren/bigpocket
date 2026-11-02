//! Granular Stream Deck REST API, mounted under `/api/deck`.
//!
//! The web UI, the phone app and the MCP bridge (`mcp_handler.rs`) all need
//! per-item control over the deck instead of the single "send the whole config"
//! POST that `/config` exposes. These handlers mutate the shared config one
//! button/page at a time and broadcast a WebSocket `config_update` so every
//! connected client stays in sync.
//!
//! Placement rules mirror the frontend packer: `grid_row`/`grid_col` pin a card
//! to a bento cell, any negative/absent value auto-packs it into the first free
//! cell, and an explicit coordinate that collides with another card on the same
//! page is a `409 Conflict`.
//!
//! Volume/brightness/trigger/layout/page-switch reuse the existing WebSocket
//! control dispatcher ([`crate::server::handle_control`]) so the deck endpoints
//! and the WS commands can never drift apart.

use std::sync::Arc;

use axum::extract::{Path as PathParam, Query, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::routing::{delete, get, post};
use axum::{Json, Router};
use serde::Deserialize;
use serde_json::{json, Value};

use crate::config::{self, Config, StreamDeckButton, StreamDeckPage, GRID_AUTO_PLACEMENT};
use crate::server::{handle_control, toggle_pomodoro, AppState};

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

/// The `/api/deck/*` sub-router. Mounted (and state-injected) by
/// [`crate::server::router`] inside the authenticated group, so every endpoint
/// here is behind the same auth middleware as `/config`.
pub fn deck_router() -> Router<Arc<AppState>> {
    Router::new()
        .route("/button", post(api_button_upsert))
        .route("/button/:id", delete(api_button_delete))
        .route("/trigger/:id", post(api_button_trigger))
        .route("/buttons", get(api_buttons_list))
        .route("/page", post(api_page_action))
        .route("/page/switch", post(api_page_switch))
        .route("/pages", get(api_pages_list))
        .route("/layout", post(api_layout))
        .route("/volume", post(api_volume))
        .route("/brightness", post(api_brightness))
}

// ---------------------------------------------------------------------------
// Error/response plumbing
// ---------------------------------------------------------------------------

/// An error that carries the HTTP status it should surface as.
struct ApiError {
    status: StatusCode,
    message: String,
}

impl ApiError {
    fn new(status: StatusCode, message: impl Into<String>) -> Self {
        Self {
            status,
            message: message.into(),
        }
    }

    fn bad_request(message: impl Into<String>) -> Self {
        Self::new(StatusCode::BAD_REQUEST, message)
    }

    fn not_found(message: impl Into<String>) -> Self {
        Self::new(StatusCode::NOT_FOUND, message)
    }

    fn conflict(message: impl Into<String>) -> Self {
        Self::new(StatusCode::CONFLICT, message)
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.status,
            Json(json!({"success": false, "error": self.message})),
        )
            .into_response()
    }
}

type ApiResult<T> = Result<T, ApiError>;

fn ok(status: StatusCode, value: Value) -> Response {
    (status, Json(value)).into_response()
}

/// Stamp the `source` field onto a broadcast event (clients use it to ignore
/// their own echo) and hand it to every WebSocket client.
fn with_source(mut event: Value) -> Value {
    if let Value::Object(map) = &mut event {
        map.entry("source")
            .or_insert_with(|| Value::String("api".to_string()));
    }
    event
}

/// Persist `cfg` (bumping `rev`) and broadcast `event`. A failed write is
/// reported instead of silently dropped.
fn persist(cfg: &mut Config, state: &Arc<AppState>, event: Value) -> ApiResult<()> {
    cfg.rev += 1;
    config::save_config(cfg).map_err(|err| ApiError::new(StatusCode::INTERNAL_SERVER_ERROR, err))?;
    state.broadcast(with_source(event));
    Ok(())
}

// ---------------------------------------------------------------------------
// Request bodies
// ---------------------------------------------------------------------------

/// Partial button payload. Every field is optional: an update only touches the
/// keys the client actually sent.
#[derive(Debug, Default, Deserialize)]
struct ButtonUpsert {
    #[serde(default)]
    id: Option<i32>,
    #[serde(default)]
    label: Option<String>,
    #[serde(default, rename = "type")]
    kind: Option<String>,
    #[serde(default)]
    value: Option<String>,
    #[serde(default)]
    icon: Option<String>,
    #[serde(default)]
    page: Option<i32>,
    #[serde(default)]
    state: Option<bool>,
    #[serde(default)]
    color: Option<String>,
    #[serde(default)]
    row_span: Option<i32>,
    #[serde(default)]
    col_span: Option<i32>,
    #[serde(default)]
    grid_row: Option<i32>,
    #[serde(default)]
    grid_col: Option<i32>,
    #[serde(default)]
    bg_style: Option<String>,
    #[serde(default)]
    clock_style: Option<String>,
    #[serde(default)]
    font_family: Option<String>,
    #[serde(default)]
    clock_font_size: Option<Value>,
}

impl ButtonUpsert {
    /// Parse a body, additionally accepting the MCP alias `button_id` for `id`.
    fn parse(args: &Value) -> Self {
        let mut req: Self = serde_json::from_value(args.clone()).unwrap_or_default();
        if req.id.is_none() {
            req.id = args.get("button_id").and_then(|v| v.as_i64().map(|n| n as i32));
        }
        req
    }
}

#[derive(Debug, Deserialize)]
struct ButtonsQuery {
    #[serde(default)]
    page: Option<i32>,
}

/// Page CRUD / switch command.
#[derive(Debug, Deserialize)]
struct PageAction {
    action: String,
    #[serde(default)]
    id: Option<i32>,
    #[serde(default)]
    title: Option<String>,
    #[serde(default)]
    page: Option<i32>,
}

impl PageAction {
    fn new(action: &str) -> Self {
        Self {
            action: action.to_string(),
            id: None,
            title: None,
            page: None,
        }
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

fn value_to_string(value: &Value) -> String {
    match value {
        Value::String(s) => s.clone(),
        Value::Number(n) => n.to_string(),
        Value::Null => String::new(),
        other => other.to_string(),
    }
}

/// Whether the card `b` covers the given cell of `page`.
fn cell_occupied(
    buttons: &[StreamDeckButton],
    page: i32,
    row: i32,
    col: i32,
    exclude: Option<i32>,
) -> bool {
    buttons.iter().any(|b| {
        if b.page != page || Some(b.id) == exclude {
            return false;
        }
        if b.grid_row < 0 || b.grid_col < 0 {
            return false;
        }
        let row_span = b.row_span.max(1);
        let col_span = b.col_span.max(1);
        row >= b.grid_row
            && row < b.grid_row + row_span
            && col >= b.grid_col
            && col < b.grid_col + col_span
    })
}

/// Whether a `row_span`x`col_span` card fits at `(row, col)` inside the grid
/// without overlapping another card on the same page.
fn footprint_free(
    cfg: &Config,
    page: i32,
    row: i32,
    col: i32,
    row_span: i32,
    col_span: i32,
    exclude: Option<i32>,
) -> bool {
    let row_span = row_span.max(1);
    let col_span = col_span.max(1);
    if row < 0 || col < 0 {
        return false;
    }
    if row + row_span > cfg.stream_deck_rows || col + col_span > cfg.stream_deck_cols {
        return false;
    }
    for r in row..row + row_span {
        for c in col..col + col_span {
            if cell_occupied(&cfg.stream_deck_buttons, page, r, c, exclude) {
                return false;
            }
        }
    }
    true
}

/// First free cell (row-major) that fits the card, or `None` when the page is
/// full.
fn find_free_cell(
    cfg: &Config,
    page: i32,
    row_span: i32,
    col_span: i32,
    exclude: Option<i32>,
) -> Option<(i32, i32)> {
    for row in 0..cfg.stream_deck_rows {
        for col in 0..cfg.stream_deck_cols {
            if footprint_free(cfg, page, row, col, row_span, col_span, exclude) {
                return Some((row, col));
            }
        }
    }
    None
}

fn next_button_id(cfg: &Config) -> i32 {
    cfg.stream_deck_buttons
        .iter()
        .map(|b| b.id)
        .max()
        .map(|max| max + 1)
        .unwrap_or(0)
}

fn next_page_id(cfg: &Config) -> i32 {
    cfg.stream_deck_pages
        .iter()
        .map(|p| p.id)
        .max()
        .map(|max| max + 1)
        .unwrap_or(0)
}

fn apply_upsert(button: &mut StreamDeckButton, req: &ButtonUpsert) {
    if let Some(v) = &req.label {
        button.label = v.clone();
    }
    if let Some(v) = &req.kind {
        button.kind = v.clone();
    }
    if let Some(v) = &req.value {
        button.value = v.clone();
    }
    if let Some(v) = &req.icon {
        button.icon = v.clone();
    }
    if let Some(v) = &req.page {
        button.page = *v;
    }
    if let Some(v) = &req.state {
        button.state = *v;
    }
    if let Some(v) = &req.color {
        button.color = v.clone();
    }
    if let Some(v) = req.row_span {
        button.row_span = v.max(1);
    }
    if let Some(v) = req.col_span {
        button.col_span = v.max(1);
    }
    if let Some(v) = req.grid_row {
        button.grid_row = v;
    }
    if let Some(v) = req.grid_col {
        button.grid_col = v;
    }
    if let Some(v) = &req.bg_style {
        button.bg_style = v.clone();
    }
    if let Some(v) = &req.clock_style {
        button.clock_style = v.clone();
    }
    if let Some(v) = &req.font_family {
        button.font_family = v.clone();
    }
    if let Some(v) = &req.clock_font_size {
        button.clock_font_size = value_to_string(v);
    }
}

/// Copy the cosmetic fields of a preset/template entry onto a button.
fn apply_template_entry(button: &mut StreamDeckButton, entry: &Value) {
    if let Some(v) = entry.get("label").and_then(Value::as_str) {
        button.label = v.to_string();
    }
    if let Some(v) = entry.get("type").and_then(Value::as_str) {
        button.kind = v.to_string();
    }
    if let Some(v) = entry.get("value").and_then(Value::as_str) {
        button.value = v.to_string();
    }
    if let Some(v) = entry.get("icon").and_then(Value::as_str) {
        button.icon = v.to_string();
    }
    if let Some(v) = entry.get("state").and_then(Value::as_bool) {
        button.state = v;
    }
    if let Some(v) = entry.get("color").and_then(Value::as_str) {
        button.color = v.to_string();
    }
    if let Some(v) = entry.get("row_span").and_then(|v| v.as_i64().map(|n| n as i32)) {
        button.row_span = (v as i32).max(1);
    }
    if let Some(v) = entry.get("col_span").and_then(|v| v.as_i64().map(|n| n as i32)) {
        button.col_span = (v as i32).max(1);
    }
    if let Some(v) = entry.get("bg_style").and_then(Value::as_str) {
        button.bg_style = v.to_string();
    }
    if let Some(v) = entry.get("clock_style").and_then(Value::as_str) {
        button.clock_style = v.to_string();
    }
}

// ---------------------------------------------------------------------------
// Button core operations
// ---------------------------------------------------------------------------

/// Insert a new button (no matching id) or patch the stored one, then resolve
/// its grid cell. Returns the stored button plus whether it was created.
fn upsert_button(state: &Arc<AppState>, req: &ButtonUpsert) -> ApiResult<(Value, bool)> {
    let mut cfg = config::load_config();

    let created = match req.id {
        Some(id) => {
            let Some(button) = cfg.stream_deck_buttons.iter_mut().find(|b| b.id == id) else {
                return Err(ApiError::not_found(format!("button {id} not found")));
            };
            apply_upsert(button, req);
            false
        }
        None => {
            let mut button = StreamDeckButton {
                id: next_button_id(&cfg),
                page: cfg.active_page,
                ..Default::default()
            };
            apply_upsert(&mut button, req);
            cfg.stream_deck_buttons.push(button);
            true
        }
    };

    let id = req.id.unwrap_or_else(|| {
        cfg.stream_deck_buttons
            .last()
            .map(|b| b.id)
            .unwrap_or_default()
    });
    let index = cfg
        .stream_deck_buttons
        .iter()
        .position(|b| b.id == id)
        .expect("button was just inserted");

    let page = cfg.stream_deck_buttons[index].page;
    let row_span = cfg.stream_deck_buttons[index].row_span;
    let col_span = cfg.stream_deck_buttons[index].col_span;

    let explicit_row = matches!(req.grid_row, Some(v) if v >= 0);
    let explicit_col = matches!(req.grid_col, Some(v) if v >= 0);
    let auto_row = matches!(req.grid_row, Some(v) if v < 0);
    let auto_col = matches!(req.grid_col, Some(v) if v < 0);
    let untouched = req.grid_row.is_none() && req.grid_col.is_none();

    if explicit_row && explicit_col {
        let row = cfg.stream_deck_buttons[index].grid_row;
        let col = cfg.stream_deck_buttons[index].grid_col;
        if !footprint_free(&cfg, page, row, col, row_span, col_span, Some(id)) {
            return Err(ApiError::conflict(format!(
                "cell ({row}, {col}) is already occupied on page {page}"
            )));
        }
    } else if auto_row || auto_col || untouched {
        // Only re-pack when the client asked for auto placement or left the
        // stored coordinates undecided; an untouched pin is preserved above.
        let current_row = cfg.stream_deck_buttons[index].grid_row;
        let current_col = cfg.stream_deck_buttons[index].grid_col;
        let needs_pack = auto_row || auto_col || (untouched && (current_row < 0 || current_col < 0));
        if needs_pack {
            match find_free_cell(&cfg, page, row_span, col_span, Some(id)) {
                Some((row, col)) => {
                    cfg.stream_deck_buttons[index].grid_row = row;
                    cfg.stream_deck_buttons[index].grid_col = col;
                }
                None => {
                    cfg.stream_deck_buttons[index].grid_row = GRID_AUTO_PLACEMENT;
                    cfg.stream_deck_buttons[index].grid_col = GRID_AUTO_PLACEMENT;
                }
            }
        }
    }

    let button_json =
        serde_json::to_value(&cfg.stream_deck_buttons[index]).unwrap_or(Value::Null);
    persist(
        &mut cfg,
        state,
        json!({"type": "config_update", "button": button_json.clone(), "created": created}),
    )?;
    Ok((button_json, created))
}

fn delete_button(state: &Arc<AppState>, id: i32) -> ApiResult<Value> {
    let mut cfg = config::load_config();
    let before = cfg.stream_deck_buttons.len();
    cfg.stream_deck_buttons.retain(|b| b.id != id);
    if cfg.stream_deck_buttons.len() == before {
        return Err(ApiError::not_found(format!("button {id} not found")));
    }
    persist(
        &mut cfg,
        state,
        json!({"type": "config_update", "deleted_button": id}),
    )?;
    let cfg = config::load_config();
    Ok(json!({"deleted_id": id, "buttons": cfg.stream_deck_buttons}))
}

fn duplicate_button(state: &Arc<AppState>, args: &Value) -> ApiResult<Value> {
    let source_id = args
        .get("button_id")
        .and_then(|v| v.as_i64().map(|n| n as i32))
        .ok_or_else(|| ApiError::bad_request("button_id is required"))?;
    let mut cfg = config::load_config();
    let Some(source) = cfg
        .stream_deck_buttons
        .iter()
        .find(|b| b.id == source_id)
        .cloned()
    else {
        return Err(ApiError::not_found(format!("button {source_id} not found")));
    };

    let target_page = args
        .get("target_page")
        .and_then(|v| v.as_i64().map(|n| n as i32))
        .unwrap_or(source.page);

    let mut copy = StreamDeckButton {
        id: next_button_id(&cfg),
        page: target_page,
        ..source
    };
    copy.grid_row = GRID_AUTO_PLACEMENT;
    copy.grid_col = GRID_AUTO_PLACEMENT;
    if let Some((row, col)) = find_free_cell(&cfg, target_page, copy.row_span, copy.col_span, None) {
        copy.grid_row = row;
        copy.grid_col = col;
    }

    let js = serde_json::to_value(&copy).unwrap_or(Value::Null);
    cfg.stream_deck_buttons.push(copy);
    persist(
        &mut cfg,
        state,
        json!({"type": "config_update", "button": js.clone(), "created": true}),
    )?;
    Ok(json!({"button": js}))
}

fn swap_buttons(state: &Arc<AppState>, first: i32, second: i32) -> ApiResult<Value> {
    let mut cfg = config::load_config();
    let Some(ia) = cfg.stream_deck_buttons.iter().position(|b| b.id == first) else {
        return Err(ApiError::not_found(format!("button {first} not found")));
    };
    let Some(ib) = cfg.stream_deck_buttons.iter().position(|b| b.id == second) else {
        return Err(ApiError::not_found(format!("button {second} not found")));
    };

    let (row_a, col_a) = (
        cfg.stream_deck_buttons[ia].grid_row,
        cfg.stream_deck_buttons[ia].grid_col,
    );
    let (row_b, col_b) = (
        cfg.stream_deck_buttons[ib].grid_row,
        cfg.stream_deck_buttons[ib].grid_col,
    );
    cfg.stream_deck_buttons[ia].grid_row = row_b;
    cfg.stream_deck_buttons[ia].grid_col = col_b;
    cfg.stream_deck_buttons[ib].grid_row = row_a;
    cfg.stream_deck_buttons[ib].grid_col = col_a;

    persist(
        &mut cfg,
        state,
        json!({"type": "config_update", "swapped": [first, second]}),
    )?;
    Ok(json!({"swapped": [first, second]}))
}

async fn trigger_button(state: &Arc<AppState>, id: i32) -> ApiResult<Value> {
    let cfg = config::load_config();
    let Some(button) = cfg.stream_deck_buttons.iter().find(|b| b.id == id) else {
        return Err(ApiError::not_found(format!("button {id} not found")));
    };
    let kind = button.kind.clone();
    let page = button.page;

    // `stream_deck_press` is the toggle variant of the existing WS command: it
    // flips `state` for toggle buttons and broadcasts `button_state_changed`.
    handle_control(state, "stream_deck_press", &json!({"button_id": id})).await;

    let state_after = config::load_config()
        .stream_deck_buttons
        .into_iter()
        .find(|b| b.id == id)
        .map(|b| b.state);
    Ok(json!({"button_id": id, "type": kind, "page": page, "state": state_after}))
}

fn list_buttons(state: &Arc<AppState>, page: Option<i32>) -> ApiResult<Value> {
    let cfg = config::load_config();
    let page = page.unwrap_or(cfg.active_page);
    let buttons: Vec<&StreamDeckButton> =
        cfg.stream_deck_buttons.iter().filter(|b| b.page == page).collect();
    Ok(json!({
        "page": page,
        "rows": cfg.stream_deck_rows,
        "cols": cfg.stream_deck_cols,
        "buttons": buttons,
    }))
}

// ---------------------------------------------------------------------------
// Page operations
// ---------------------------------------------------------------------------

async fn page_action(state: &Arc<AppState>, req: &PageAction) -> ApiResult<Value> {
    match req.action.as_str() {
        "create" => {
            let mut cfg = config::load_config();
            let id = next_page_id(&cfg);
            let title = req
                .title
                .clone()
                .filter(|t| !t.trim().is_empty())
                .unwrap_or_else(|| format!("Sayfa {}", id + 1));
            cfg.stream_deck_pages.push(StreamDeckPage {
                id,
                title: title.clone(),
            });
            persist(
                &mut cfg,
                state,
                json!({"type": "config_update", "page_created": id}),
            )?;
            let cfg = config::load_config();
            Ok(json!({
                "action": "create",
                "page": {"id": id, "title": title},
                "pages": cfg.stream_deck_pages,
            }))
        }
        "rename" => {
            let id = req
                .id
                .ok_or_else(|| ApiError::bad_request("id is required"))?;
            let mut cfg = config::load_config();
            let Some(page) = cfg.stream_deck_pages.iter_mut().find(|p| p.id == id) else {
                return Err(ApiError::not_found(format!("page {id} not found")));
            };
            if let Some(title) = req.title.clone().filter(|t| !t.trim().is_empty()) {
                page.title = title;
            }
            persist(
                &mut cfg,
                state,
                json!({"type": "config_update", "page_renamed": id}),
            )?;
            let cfg = config::load_config();
            Ok(json!({
                "action": "rename",
                "page": {"id": id, "title": cfg.stream_deck_pages.iter().find(|p| p.id == id).map(|p| p.title.clone()).unwrap_or_default()},
                "pages": cfg.stream_deck_pages,
            }))
        }
        "delete" => {
            let id = req
                .id
                .ok_or_else(|| ApiError::bad_request("id is required"))?;
            let mut cfg = config::load_config();
            if !cfg.stream_deck_pages.iter().any(|p| p.id == id) {
                return Err(ApiError::not_found(format!("page {id} not found")));
            }
            if cfg.stream_deck_pages.len() <= 1 {
                return Err(ApiError::conflict("cannot delete the last page"));
            }
            cfg.stream_deck_pages.retain(|p| p.id != id);
            cfg.stream_deck_buttons.retain(|b| b.page != id);
            if cfg.active_page == id {
                cfg.active_page = cfg
                    .stream_deck_pages
                    .first()
                    .map(|p| p.id)
                    .unwrap_or_default();
            }
            persist(
                &mut cfg,
                state,
                json!({"type": "config_update", "page_deleted": id}),
            )?;
            let cfg = config::load_config();
            Ok(json!({
                "action": "delete",
                "deleted_page": id,
                "active_page": cfg.active_page,
                "pages": cfg.stream_deck_pages,
            }))
        }
        "clear" => {
            let id = req
                .id
                .ok_or_else(|| ApiError::bad_request("id is required"))?;
            let mut cfg = config::load_config();
            if !cfg.stream_deck_pages.iter().any(|p| p.id == id) {
                return Err(ApiError::not_found(format!("page {id} not found")));
            }
            let before = cfg.stream_deck_buttons.len();
            cfg.stream_deck_buttons.retain(|b| b.page != id);
            let removed = before - cfg.stream_deck_buttons.len();
            persist(
                &mut cfg,
                state,
                json!({"type": "config_update", "page_cleared": id}),
            )?;
            Ok(json!({"action": "clear", "page": id, "removed": removed}))
        }
        "switch" => {
            let id = req
                .id
                .or(req.page)
                .ok_or_else(|| ApiError::bad_request("id is required"))?;
            let cfg = config::load_config();
            if !cfg.stream_deck_pages.iter().any(|p| p.id == id) {
                return Err(ApiError::not_found(format!("page {id} not found")));
            }
            // The WS command validates nothing further and broadcasts
            // `page_switched` together with the new page's buttons.
            handle_control(state, "switch_page", &json!({"page": id})).await;
            Ok(json!({"action": "switch", "active_page": id}))
        }
        other => Err(ApiError::bad_request(format!("unknown action {other}"))),
    }
}

fn page_list(state: &Arc<AppState>) -> ApiResult<Value> {
    let cfg = config::load_config();
    Ok(json!({
        "active_page": cfg.active_page,
        "rows": cfg.stream_deck_rows,
        "cols": cfg.stream_deck_cols,
        "pages": cfg.stream_deck_pages,
    }))
}

fn batch_create(state: &Arc<AppState>, args: &Value) -> ApiResult<Value> {
    let mut cfg = config::load_config();
    let page = args
        .get("page")
        .and_then(|v| v.as_i64().map(|n| n as i32))
        .unwrap_or(cfg.active_page);
    if args
        .get("clear_page_first")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        cfg.stream_deck_buttons.retain(|b| b.page != page);
    }

    let mut created = Vec::new();
    if let Some(entries) = args.get("buttons").and_then(Value::as_array) {
        for entry in entries {
            let mut button = StreamDeckButton {
                id: next_button_id(&cfg),
                page,
                ..Default::default()
            };
            apply_template_entry(&mut button, entry);
            if let Some(v) = entry.get("grid_row").and_then(|v| v.as_i64().map(|n| n as i32)) {
                button.grid_row = v as i32;
            }
            if let Some(v) = entry.get("grid_col").and_then(|v| v.as_i64().map(|n| n as i32)) {
                button.grid_col = v as i32;
            }
            if button.grid_row < 0 || button.grid_col < 0 {
                if let Some((row, col)) =
                    find_free_cell(&cfg, page, button.row_span, button.col_span, None)
                {
                    button.grid_row = row;
                    button.grid_col = col;
                }
            }
            created.push(serde_json::to_value(&button).unwrap_or(Value::Null));
            cfg.stream_deck_buttons.push(button);
        }
    }

    persist(
        &mut cfg,
        state,
        json!({"type": "config_update", "batch_created": created.len(), "page": page}),
    )?;
    Ok(json!({"page": page, "created": created}))
}

// ---------------------------------------------------------------------------
// Layout / volume / brightness
// ---------------------------------------------------------------------------

async fn set_layout(state: &Arc<AppState>, rows: i32, cols: i32) -> ApiResult<Value> {
    if !(1..=6).contains(&rows) || !(1..=8).contains(&cols) {
        return Err(ApiError::bad_request("rows must be 1..=6 and cols 1..=8"));
    }
    handle_control(state, "set_grid_layout", &json!({"rows": rows, "cols": cols})).await;
    let cfg = config::load_config();
    Ok(json!({"rows": cfg.stream_deck_rows, "cols": cfg.stream_deck_cols}))
}

async fn set_volume(state: &Arc<AppState>, action: &str, level: Option<f64>) -> ApiResult<Value> {
    let action = action.trim();
    if !matches!(action, "up" | "down" | "mute" | "set") {
        return Err(ApiError::bad_request(
            "action must be one of up, down, mute, set",
        ));
    }
    let level = level.unwrap_or(50.0);
    handle_control(state, "set_volume", &json!({"action": action, "level": level})).await;
    Ok(json!({"action": action, "level": level}))
}

async fn set_brightness(state: &Arc<AppState>, level: f64) -> ApiResult<Value> {
    let level = level.clamp(0.0, 100.0) as u8;
    handle_control(state, "set_brightness", &json!({"level": level})).await;
    Ok(json!({"level": level}))
}

// ---------------------------------------------------------------------------
// Presets / templates
// ---------------------------------------------------------------------------

/// Ready-made button sets. Owned by `profile.rs` (which also serves them over
/// `GET /api/deck/presets`); reused here for the MCP bridge.
fn presets_catalog() -> Value {
    crate::profile::presets_catalog()
}

fn apply_template(state: &Arc<AppState>, template: &str, page: Option<i32>) -> ApiResult<Value> {
    let Some(entries) = crate::profile::template_buttons(template) else {
        return Err(ApiError::not_found(format!("template {template} not found")));
    };

    let mut cfg = config::load_config();
    let page = page.unwrap_or(cfg.active_page);
    cfg.stream_deck_buttons.retain(|b| b.page != page);

    let mut created = Vec::new();
    for mut button in entries {
        button.id = next_button_id(&cfg);
        button.page = page;
        button.grid_row = GRID_AUTO_PLACEMENT;
        button.grid_col = GRID_AUTO_PLACEMENT;
        if let Some((row, col)) = find_free_cell(&cfg, page, button.row_span, button.col_span, None)
        {
            button.grid_row = row;
            button.grid_col = col;
        }
        created.push(serde_json::to_value(&button).unwrap_or(Value::Null));
        cfg.stream_deck_buttons.push(button);
    }

    persist(
        &mut cfg,
        state,
        json!({"type": "config_update", "template_applied": template, "page": page}),
    )?;
    Ok(json!({"template": template, "page": page, "buttons": created}))
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

async fn api_button_upsert(
    State(state): State<Arc<AppState>>,
    Json(body): Json<ButtonUpsert>,
) -> Response {
    match upsert_button(&state, &body) {
        Ok((button, created)) => ok(
            if created {
                StatusCode::CREATED
            } else {
                StatusCode::OK
            },
            json!({"success": true, "button": button, "created": created}),
        ),
        Err(err) => err.into_response(),
    }
}

async fn api_button_delete(
    State(state): State<Arc<AppState>>,
    PathParam(id): PathParam<i32>,
) -> Response {
    match delete_button(&state, id) {
        Ok(value) => ok(StatusCode::OK, json!({"success": true, "result": value})),
        Err(err) => err.into_response(),
    }
}

async fn api_button_trigger(
    State(state): State<Arc<AppState>>,
    PathParam(id): PathParam<i32>,
) -> Response {
    match trigger_button(&state, id).await {
        Ok(value) => ok(StatusCode::OK, json!({"success": true, "result": value})),
        Err(err) => err.into_response(),
    }
}

async fn api_buttons_list(
    State(state): State<Arc<AppState>>,
    Query(query): Query<ButtonsQuery>,
) -> Response {
    match list_buttons(&state, query.page) {
        Ok(value) => ok(StatusCode::OK, json!({"success": true, "result": value})),
        Err(err) => err.into_response(),
    }
}

async fn api_page_action(
    State(state): State<Arc<AppState>>,
    Json(body): Json<PageAction>,
) -> Response {
    let created = body.action == "create";
    match page_action(&state, &body).await {
        Ok(value) => ok(
            if created {
                StatusCode::CREATED
            } else {
                StatusCode::OK
            },
            json!({"success": true, "result": value}),
        ),
        Err(err) => err.into_response(),
    }
}

async fn api_page_switch(
    State(state): State<Arc<AppState>>,
    Json(body): Json<Value>,
) -> Response {
    let id = body
        .get("page")
        .or_else(|| body.get("id"))
        .and_then(|v| v.as_i64().map(|n| n as i32));
    let mut req = PageAction::new("switch");
    req.id = id;
    req.page = id;
    match page_action(&state, &req).await {
        Ok(value) => ok(StatusCode::OK, json!({"success": true, "result": value})),
        Err(err) => err.into_response(),
    }
}

async fn api_pages_list(State(state): State<Arc<AppState>>) -> Response {
    match page_list(&state) {
        Ok(value) => ok(StatusCode::OK, json!({"success": true, "result": value})),
        Err(err) => err.into_response(),
    }
}

async fn api_layout(State(state): State<Arc<AppState>>, Json(body): Json<Value>) -> Response {
    let rows = body.get("rows").and_then(|v| v.as_i64().map(|n| n as i32)).unwrap_or(0) as i32;
    let cols = body.get("cols").and_then(|v| v.as_i64().map(|n| n as i32)).unwrap_or(0) as i32;
    match set_layout(&state, rows, cols).await {
        Ok(value) => ok(StatusCode::OK, json!({"success": true, "result": value})),
        Err(err) => err.into_response(),
    }
}

async fn api_volume(State(state): State<Arc<AppState>>, Json(body): Json<Value>) -> Response {
    let action = body
        .get("action")
        .and_then(Value::as_str)
        .unwrap_or("")
        .to_string();
    let level = body
        .get("level")
        .and_then(Value::as_f64)
        .or_else(|| body.get("level").and_then(|v| v.as_i64().map(|n| n as i32)).map(|v| v as f64));
    match set_volume(&state, &action, level).await {
        Ok(value) => ok(StatusCode::OK, json!({"success": true, "result": value})),
        Err(err) => err.into_response(),
    }
}

async fn api_brightness(State(state): State<Arc<AppState>>, Json(body): Json<Value>) -> Response {
    let level = body
        .get("level")
        .and_then(Value::as_f64)
        .or_else(|| body.get("level").and_then(|v| v.as_i64().map(|n| n as i32)).map(|v| v as f64))
        .unwrap_or(80.0);
    match set_brightness(&state, level).await {
        Ok(value) => ok(StatusCode::OK, json!({"success": true, "result": value})),
        Err(err) => err.into_response(),
    }
}

// ---------------------------------------------------------------------------
// MCP bridge
// ---------------------------------------------------------------------------

/// Dispatch one canonical MCP tool name to the deck core. Returns
/// `Some((text, is_error))` for the Stream Deck tools handled here and `None`
/// for everything else, so `mcp_handler.rs` can route the rest.
pub async fn handle_mcp_tool(
    state: &Arc<AppState>,
    name: &str,
    args: &Value,
) -> Option<(String, bool)> {
    let result: ApiResult<Value> = match name {
        "get_streamdeck_config" => Ok(json!({"config": config::load_config()})),
        "set_streamdeck_button" => {
            let req = ButtonUpsert::parse(args);
            if req.id.is_none() {
                Err(ApiError::bad_request("button_id is required"))
            } else {
                upsert_button(state, &req).map(|(button, created)| {
                    json!({"button": button, "created": created})
                })
            }
        }
        "add_streamdeck_button" | "create_custom_widget" => {
            let mut req = ButtonUpsert::parse(args);
            req.id = None;
            upsert_button(state, &req)
                .map(|(button, created)| json!({"button": button, "created": created}))
        }
        "batch_create_streamdeck_buttons" => batch_create(state, args),
        "duplicate_streamdeck_button" => duplicate_button(state, args),
        "delete_streamdeck_button" => {
            let id = args.get("button_id").and_then(|v| v.as_i64().map(|n| n as i32));
            match id {
                Some(id) => delete_button(state, id),
                None => Err(ApiError::bad_request("button_id is required")),
            }
        }
        "swap_streamdeck_buttons" => {
            let first = args.get("button_id_1").and_then(|v| v.as_i64().map(|n| n as i32));
            let second = args.get("button_id_2").and_then(|v| v.as_i64().map(|n| n as i32));
            match (first, second) {
                (Some(a), Some(b)) => swap_buttons(state, a, b),
                _ => Err(ApiError::bad_request(
                    "button_id_1 and button_id_2 are required",
                )),
            }
        }
        "set_grid_layout" => {
            let rows = args.get("rows").and_then(|v| v.as_i64().map(|n| n as i32)).unwrap_or(0) as i32;
            let cols = args.get("cols").and_then(|v| v.as_i64().map(|n| n as i32)).unwrap_or(0) as i32;
            set_layout(state, rows, cols).await
        }
        "trigger_button" | "streamdeck_toggle_button" => {
            let id = args.get("button_id").and_then(|v| v.as_i64().map(|n| n as i32));
            match id {
                Some(id) => trigger_button(state, id).await,
                None => Err(ApiError::bad_request("button_id is required")),
            }
        }
        "streamdeck_switch_page" => {
            let mut req = PageAction::new("switch");
            req.id = args.get("page").and_then(|v| v.as_i64().map(|n| n as i32));
            req.page = req.id;
            page_action(state, &req).await
        }
        "create_streamdeck_page" => {
            let mut req = PageAction::new("create");
            req.title = args.get("title").and_then(Value::as_str).map(str::to_string);
            page_action(state, &req).await
        }
        "rename_streamdeck_page" => {
            let mut req = PageAction::new("rename");
            req.id = args.get("page_id").and_then(|v| v.as_i64().map(|n| n as i32));
            req.title = args.get("title").and_then(Value::as_str).map(str::to_string);
            page_action(state, &req).await
        }
        "delete_streamdeck_page" => {
            let mut req = PageAction::new("delete");
            req.id = args.get("page_id").and_then(|v| v.as_i64().map(|n| n as i32));
            page_action(state, &req).await
        }
        "clear_streamdeck_page" => {
            let mut req = PageAction::new("clear");
            req.id = args
                .get("page_id")
                .or_else(|| args.get("page"))
                .and_then(|v| v.as_i64().map(|n| n as i32))
                .or_else(|| Some(config::load_config().active_page));
            page_action(state, &req).await
        }
        "apply_streamdeck_template" => {
            let template = args
                .get("template")
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_string();
            let page = args.get("page").and_then(|v| v.as_i64().map(|n| n as i32));
            apply_template(state, &template, page)
        }
        "get_streamdeck_presets" => Ok(json!({"presets": presets_catalog()})),
        "streamdeck_set_volume" => {
            let action = args
                .get("action")
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_string();
            let level = args
                .get("level")
                .and_then(Value::as_f64)
                .or_else(|| args.get("level").and_then(|v| v.as_i64().map(|n| n as i32)).map(|v| v as f64));
            set_volume(state, &action, level).await
        }
        "streamdeck_set_brightness" => {
            let level = args
                .get("level")
                .and_then(Value::as_f64)
                .or_else(|| args.get("level").and_then(|v| v.as_i64().map(|n| n as i32)).map(|v| v as f64))
                .unwrap_or(80.0);
            set_brightness(state, level).await
        }
        "pomodoro_toggle" => {
            toggle_pomodoro(state);
            Ok(json!({"toggled": true}))
        }
        _ => return None,
    };

    Some(match result {
        Ok(value) => (value.to_string(), false),
        Err(err) => (json!({"error": err.message}).to_string(), true),
    })
}
