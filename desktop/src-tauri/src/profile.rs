//! Stream Deck profile export/import and built-in template presets.
//!
//! Mounted into the protected router by [`crate::server::router`] under
//! `/api/deck` (merged with [`crate::deck_api::deck_router`]):
//!
//! * `GET  /api/deck/profile/export`  — pack the deck config + its icons into a
//!   `.bigpocket-profile` ZIP (`profile.json` + `icons/`).
//! * `POST /api/deck/profile/import`  — restore such a ZIP (multipart upload).
//! * `GET  /api/deck/presets`         — the built-in template catalogue.
//! * `POST /api/deck/template/apply`  — append one template's buttons to the
//!   active page.
//!
//! The templates mirror `getWidgetPropsForPreset` / `applyTemplateToActivePage`
//! from `frontend/app.js` so the web UI and the mobile clients see the same
//! starting layouts.

use std::collections::HashSet;
use std::ffi::OsStr;
use std::io::{Cursor, Read, Write};
use std::path::{Component, Path, PathBuf};
use std::sync::Arc;

use axum::body::Body;
use axum::extract::{DefaultBodyLimit, Multipart, State};
use axum::http::{header, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use zip::write::SimpleFileOptions;
use zip::{CompressionMethod, ZipArchive, ZipWriter};

use crate::config::{self, StreamDeckButton, StreamDeckPage};
use crate::server::AppState;

/// Profile archive version written on export.
const PROFILE_VERSION: i32 = 1;
/// Entry name of the deck payload inside the archive.
const PROFILE_ENTRY: &str = "profile.json";
/// Icon entries live under this folder inside the archive.
const ICONS_PREFIX: &str = "icons";
/// Largest archive we are willing to accept on import (64 MiB).
const MAX_IMPORT_BYTES: usize = 64 * 1024 * 1024;
/// Largest single icon we extract (8 MiB) — keeps a malicious archive from
/// ballooning memory while still covering any realistic PNG.
const MAX_ICON_BYTES: u64 = 8 * 1024 * 1024;

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

/// Deck profile/template sub-router, nested at `/api/deck` alongside
/// [`crate::deck_api::deck_router`].
pub fn profile_router() -> Router<Arc<AppState>> {
    Router::new()
        .route("/presets", get(presets_handler))
        .route("/template/apply", post(template_apply_handler))
        .route("/profile/export", get(profile_export_handler))
        .route(
            "/profile/import",
            post(profile_import_handler).layer(DefaultBodyLimit::max(MAX_IMPORT_BYTES)),
        )
}

// ---------------------------------------------------------------------------
// Profile payload
// ---------------------------------------------------------------------------

/// The `profile.json` document stored inside an exported archive.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DeckProfile {
    #[serde(default = "profile_version")]
    pub version: i32,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub pages: Vec<StreamDeckPage>,
    #[serde(default)]
    pub rows: i32,
    #[serde(default)]
    pub cols: i32,
    #[serde(default)]
    pub active_page: i32,
    #[serde(default)]
    pub buttons: Vec<StreamDeckButton>,
}

fn profile_version() -> i32 {
    PROFILE_VERSION
}

/// Icons directory shared with the uploaded/extracted button icons.
fn icons_dir() -> PathBuf {
    crate::server::frontend_dir().join(ICONS_PREFIX)
}

/// Extract the flat `icons/<name>` file name a button refers to, if any.
/// Built-in SVG glyph names ("clock", "cpu", …) resolve to nothing and are
/// filtered out by the existence check in [`build_archive`].
fn icon_file_name(icon: &str) -> Option<String> {
    let icon = icon.trim();
    if icon.is_empty() {
        return None;
    }
    let rest = icon
        .strip_prefix("icons/")
        .or_else(|| icon.strip_prefix("icons\\"))
        .unwrap_or(icon);
    if rest.is_empty()
        || rest.contains('/')
        || rest.contains('\\')
        || rest.contains(':')
        || rest.starts_with("http")
    {
        return None;
    }
    Some(rest.to_string())
}

/// Build the `.bigpocket-profile` archive: `profile.json` plus every referenced
/// icon file that actually exists on disk.
fn build_archive(
    profile: &DeckProfile,
    icons_root: &Path,
    names: &[String],
) -> Result<Vec<u8>, String> {
    let profile_json =
        serde_json::to_vec_pretty(profile).map_err(|e| format!("profil kodlanamadı: {e}"))?;

    let mut cursor = Cursor::new(Vec::new());
    {
        let mut zip = ZipWriter::new(&mut cursor);
        let options = SimpleFileOptions::default()
            .compression_method(CompressionMethod::Deflated)
            .unix_permissions(0o644);

        zip.start_file(PROFILE_ENTRY, options)
            .map_err(|e| format!("profile.json eklenemedi: {e}"))?;
        zip.write_all(&profile_json)
            .map_err(|e| format!("profile.json yazılamadı: {e}"))?;

        for name in names {
            let path = icons_root.join(name);
            let Ok(bytes) = std::fs::read(&path) else {
                continue;
            };
            zip.start_file(format!("{ICONS_PREFIX}/{name}"), options)
                .map_err(|e| format!("ikon eklenemedi ({name}): {e}"))?;
            zip.write_all(&bytes)
                .map_err(|e| format!("ikon yazılamadı ({name}): {e}"))?;
        }

        zip.finish()
            .map_err(|e| format!("arşiv tamamlanamadı: {e}"))?;
    }
    Ok(cursor.into_inner())
}

/// Read `profile.json` and extract its icon entries (overwriting existing files).
fn read_archive(data: &[u8], icons_root: &Path) -> Result<(DeckProfile, usize), String> {
    let mut archive =
        ZipArchive::new(Cursor::new(data)).map_err(|e| format!("geçersiz ZIP: {e}"))?;

    let mut raw = Vec::new();
    {
        let mut entry = archive
            .by_name(PROFILE_ENTRY)
            .map_err(|_| "ZIP içinde profile.json bulunamadı".to_string())?;
        if entry.size() > MAX_IMPORT_BYTES as u64 {
            return Err("profile.json beklenenden büyük".to_string());
        }
        entry
            .read_to_end(&mut raw)
            .map_err(|e| format!("profile.json okunamadı: {e}"))?;
    }
    let profile: DeckProfile =
        serde_json::from_slice(&raw).map_err(|e| format!("profile.json çözümlenemedi: {e}"))?;

    std::fs::create_dir_all(icons_root).map_err(|e| format!("ikon klasörü oluşturulamadı: {e}"))?;

    let mut written = 0usize;
    for index in 0..archive.len() {
        let Ok(mut entry) = archive.by_index(index) else {
            continue;
        };
        if entry.is_dir() {
            continue;
        }
        // `enclosed_name` sanitises the path, dropping any `..`/absolute prefix,
        // so a malicious archive cannot escape the icons directory (Zip Slip).
        let Some(safe) = entry.enclosed_name() else {
            continue;
        };
        let components: Vec<Component<'_>> = safe.components().collect();
        if components.len() != 2 {
            continue;
        }
        let Component::Normal(prefix) = &components[0] else {
            continue;
        };
        if *prefix != OsStr::new(ICONS_PREFIX) {
            continue;
        }
        let Component::Normal(file_name) = &components[1] else {
            continue;
        };
        if entry.size() > MAX_ICON_BYTES {
            continue;
        }
        let mut bytes = Vec::new();
        if entry.read_to_end(&mut bytes).is_err() {
            continue;
        }
        let target = icons_root.join(file_name);
        if std::fs::write(&target, &bytes).is_err() {
            continue;
        }
        written += 1;
    }

    Ok((profile, written))
}

// ---------------------------------------------------------------------------
// Handlers — profile export / import
// ---------------------------------------------------------------------------

/// `GET /api/deck/profile/export`
async fn profile_export_handler(State(state): State<Arc<AppState>>) -> Response {
    let cfg = state.config();
    let profile = DeckProfile {
        version: PROFILE_VERSION,
        name: "BigPocket Profile".to_string(),
        description: format!(
            "{} buton, {} sayfa",
            cfg.stream_deck_buttons.len(),
            cfg.stream_deck_pages.len()
        ),
        pages: cfg.stream_deck_pages.clone(),
        rows: cfg.stream_deck_rows,
        cols: cfg.stream_deck_cols,
        active_page: cfg.active_page,
        buttons: cfg.stream_deck_buttons.clone(),
    };

    let mut seen = HashSet::new();
    let names: Vec<String> = cfg
        .stream_deck_buttons
        .iter()
        .filter_map(|button| icon_file_name(&button.icon))
        .filter(|name| seen.insert(name.clone()))
        .collect();

    let root = icons_dir();
    let built = tokio::task::spawn_blocking(move || build_archive(&profile, &root, &names)).await;

    match built {
        Ok(Ok(bytes)) => Response::builder()
            .status(StatusCode::OK)
            .header(header::CONTENT_TYPE, "application/zip")
            .header(
                header::CONTENT_DISPOSITION,
                "attachment; filename=\"bigpocket-profile.zip\"",
            )
            .header(header::CONTENT_LENGTH, bytes.len().to_string())
            .body(Body::from(bytes))
            .unwrap_or_else(|e| {
                json_response(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    json!({"success": false, "error": e.to_string()}),
                )
            }),
        Ok(Err(err)) => json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err}),
        ),
        Err(err) => json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err.to_string()}),
        ),
    }
}

/// `POST /api/deck/profile/import` (multipart, field `file`).
async fn profile_import_handler(
    State(state): State<Arc<AppState>>,
    mut multipart: Multipart,
) -> Response {
    let mut data: Option<Vec<u8>> = None;
    while let Ok(Some(field)) = multipart.next_field().await {
        let name = field.name().unwrap_or("").to_string();
        if name == "file" || name == "profile" || name == "archive" {
            match field.bytes().await {
                Ok(bytes) => data = Some(bytes.to_vec()),
                Err(err) => {
                    return json_response(
                        StatusCode::BAD_REQUEST,
                        json!({"success": false, "error": format!("dosya okunamadı: {err}")}),
                    )
                }
            }
        }
    }

    let Some(data) = data else {
        return json_response(
            StatusCode::BAD_REQUEST,
            json!({"success": false, "error": "ZIP dosyası bulunamadı (field: file)"}),
        );
    };
    if data.is_empty() {
        return json_response(
            StatusCode::BAD_REQUEST,
            json!({"success": false, "error": "ZIP dosyası boş"}),
        );
    }

    let root = icons_dir();
    let parsed = tokio::task::spawn_blocking(move || read_archive(&data, &root)).await;

    let (profile, icon_count) = match parsed {
        Ok(Ok(result)) => result,
        Ok(Err(err)) => {
            return json_response(
                StatusCode::BAD_REQUEST,
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

    let mut cfg = config::load_config();
    cfg.stream_deck_pages = profile.pages;
    cfg.stream_deck_buttons = profile.buttons;
    if profile.rows > 0 {
        cfg.stream_deck_rows = profile.rows;
    }
    if profile.cols > 0 {
        cfg.stream_deck_cols = profile.cols;
    }
    cfg.active_page = profile.active_page;
    cfg.rev = cfg.rev.saturating_add(1);

    if let Err(err) = config::save_config(&cfg) {
        return json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err}),
        );
    }
    let cfg = config::load_config();
    state.broadcast(json!({"type": "config_update", "source": "profile_import"}));

    json_response(
        StatusCode::OK,
        json!({
            "success": true,
            "name": profile.name,
            "buttons": cfg.stream_deck_buttons.len(),
            "pages": cfg.stream_deck_pages.len(),
            "rows": cfg.stream_deck_rows,
            "cols": cfg.stream_deck_cols,
            "icons": icon_count,
        }),
    )
}

// ---------------------------------------------------------------------------
// Templates / presets
// ---------------------------------------------------------------------------

/// Small helper for the built-in template buttons.
fn button(
    label: &str,
    kind: &str,
    value: &str,
    icon: &str,
    col_span: i32,
    row_span: i32,
) -> StreamDeckButton {
    StreamDeckButton {
        id: 0,
        label: label.to_string(),
        kind: kind.to_string(),
        value: value.to_string(),
        icon: icon.to_string(),
        page: 0,
        state: false,
        color: "#F4F4F5".to_string(),
        row_span,
        col_span,
        grid_row: config::GRID_AUTO_PLACEMENT,
        grid_col: config::GRID_AUTO_PLACEMENT,
        ..Default::default()
    }
}

/// One built-in template definition.
struct Template {
    id: &'static str,
    title: &'static str,
    description: &'static str,
    buttons: Vec<StreamDeckButton>,
}

/// The five shipped templates (mirrors `PRESETS` / `applyTemplateToActivePage`).
fn templates() -> Vec<Template> {
    vec![
        Template {
            id: "pro_studio",
            title: "Pro Studio",
            description: "Canlı saat, ana ses, donanım telemetrisi ve mikrofon kontrolleri",
            buttons: vec![
                button("Saat & Tarih", "clock_widget", "", "clock", 2, 1),
                button("Ana Ses", "volume_slider", "master_volume", "volume", 2, 1),
                button("CPU Yükü", "live_info", "cpu", "cpu", 1, 1),
                button("RAM Bellek", "live_info", "ram", "ram", 1, 1),
                button("Mikrofon", "toggle", "mic_mute", "mic", 1, 1),
                button(
                    "PC Kilitle",
                    "command",
                    "rundll32.exe user32.dll,LockWorkStation",
                    "lock",
                    1,
                    1,
                ),
            ],
        },
        Template {
            id: "media_lounge",
            title: "Media Lounge",
            description: "Medya, ses ve parlaklık için rahat bir kontrol paneli",
            buttons: vec![
                button("Ana Ses", "volume_slider", "master_volume", "volume", 2, 1),
                button("Medya Masası", "media_widget", "playpause", "play", 2, 1),
                button("Sesi Kapat", "toggle", "sound_mute", "voldown", 1, 1),
                button(
                    "Spotify Web",
                    "url",
                    "https://open.spotify.com",
                    "music",
                    1,
                    1,
                ),
                button("YouTube", "url", "https://youtube.com", "play", 1, 1),
                button(
                    "Parlaklık",
                    "brightness_slider",
                    "display_brightness",
                    "bright",
                    2,
                    1,
                ),
            ],
        },
        Template {
            id: "gaming_obs",
            title: "Gaming / OBS",
            description: "Oyun ve yayın kısayolları: Discord, Game Bar, ekran alıntısı",
            buttons: vec![
                button("Discord Mute", "hotkey", "ctrl+shift+m", "mic", 1, 1),
                button("Discord Sağır", "hotkey", "ctrl+shift+d", "voldown", 1, 1),
                button("Ekran Alıntısı", "hotkey", "meta+shift+s", "task", 1, 1),
                button("Game Bar", "hotkey", "meta+g", "custom", 1, 1),
                button("Ana Ses", "volume_slider", "master_volume", "volume", 2, 1),
                button("Donanım Telemetri", "live_info", "combo", "cpu", 2, 1),
            ],
        },
        Template {
            id: "developer",
            title: "Developer",
            description: "Terminal, görev yöneticisi ve geliştirici araçları",
            buttons: vec![
                button("Terminal", "command", "wt.exe", "terminal", 1, 1),
                button("Görev Yöneticisi", "command", "taskmgr.exe", "task", 1, 1),
                button("Hesap Makinesi", "command", "calc.exe", "calc", 1, 1),
                button(
                    "PC Kilitle",
                    "command",
                    "rundll32.exe user32.dll,LockWorkStation",
                    "lock",
                    1,
                    1,
                ),
                button("Saat & Tarih", "clock_widget", "", "clock", 2, 1),
                button("Masaüstü", "hotkey", "meta+d", "desktop", 1, 1),
                button("GitHub", "url", "https://github.com", "link", 1, 1),
            ],
        },
        Template {
            id: "system_control",
            title: "System Control",
            description: "Donanım telemetrisi, monitör, ses ve parlaklık",
            buttons: vec![
                button("Donanım Telemetri", "live_info", "combo", "cpu", 2, 1),
                button("Sanal Ekran", "toggle", "virtual_monitor", "custom", 1, 1),
                button("Mikrofon", "toggle", "mic_mute", "mic", 1, 1),
                button(
                    "Parlaklık",
                    "brightness_slider",
                    "display_brightness",
                    "bright",
                    2,
                    1,
                ),
                button("Ana Ses", "volume_slider", "master_volume", "volume", 2, 1),
            ],
        },
    ]
}

/// The distinct widget kinds a template uses, in first-seen order.
fn template_types(buttons: &[StreamDeckButton]) -> Vec<String> {
    let mut seen = HashSet::new();
    buttons
        .iter()
        .filter(|b| !b.kind.is_empty())
        .filter(|b| seen.insert(b.kind.clone()))
        .map(|b| b.kind.clone())
        .collect()
}

fn preset_json(template: &Template) -> Value {
    json!({
        "id": template.id,
        "title": template.title,
        "description": template.description,
        "preview": {
            "buttons": template.buttons.len(),
            "types": template_types(&template.buttons),
        },
        "buttons": template.buttons,
    })
}

/// The built-in template catalogue as a JSON array. Shared with the MCP bridge
/// so it does not have to keep a second copy of the presets.
pub fn presets_catalog() -> Value {
    Value::Array(templates().iter().map(preset_json).collect())
}

/// Buttons of one built-in template (freshly cloned, ready to mutate). Shared
/// with the MCP bridge.
pub fn template_buttons(id: &str) -> Option<Vec<StreamDeckButton>> {
    templates()
        .into_iter()
        .find(|template| template.id == id)
        .map(|template| template.buttons)
}

/// Known template ids, in catalogue order.
pub fn template_ids() -> Vec<&'static str> {
    templates().iter().map(|template| template.id).collect()
}

/// `GET /api/deck/presets`
async fn presets_handler() -> Response {
    json_response(
        StatusCode::OK,
        json!({"success": true, "presets": presets_catalog()}),
    )
}

#[derive(Debug, Deserialize)]
struct TemplateApplyRequest {
    #[serde(default)]
    template: String,
    #[serde(default)]
    id: String,
}

/// `POST /api/deck/template/apply`
async fn template_apply_handler(
    State(state): State<Arc<AppState>>,
    Json(body): Json<TemplateApplyRequest>,
) -> Response {
    let key = if body.template.is_empty() {
        body.id.as_str()
    } else {
        body.template.as_str()
    };

    let Some(buttons) = template_buttons(key) else {
        return json_response(
            StatusCode::BAD_REQUEST,
            json!({"success": false, "error": "Bilinmeyen şablon", "available": template_ids()}),
        );
    };

    let mut cfg = config::load_config();
    let active_page = cfg.active_page;
    let mut next_id = cfg
        .stream_deck_buttons
        .iter()
        .map(|b| b.id)
        .max()
        .map(|max| max.saturating_add(1))
        .unwrap_or(0);

    let added = buttons.len();
    for mut template_button in buttons {
        template_button.id = next_id;
        next_id = next_id.saturating_add(1);
        template_button.page = active_page;
        template_button.grid_row = config::GRID_AUTO_PLACEMENT;
        template_button.grid_col = config::GRID_AUTO_PLACEMENT;
        cfg.stream_deck_buttons.push(template_button);
    }

    cfg.rev = cfg.rev.saturating_add(1);
    if let Err(err) = config::save_config(&cfg) {
        return json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({"success": false, "error": err}),
        );
    }
    let cfg = config::load_config();
    state.broadcast(json!({"type": "config_update", "source": "template_apply"}));

    json_response(
        StatusCode::OK,
        json!({
            "success": true,
            "template": key,
            "page": active_page,
            "added": added,
            "buttons": cfg.stream_deck_buttons,
        }),
    )
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

fn json_response(status: StatusCode, value: Value) -> Response {
    (status, Json(value)).into_response()
}
