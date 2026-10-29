//! Persistent application configuration.
//!
//! Mirrors the Go `Config` model (desktop/main.go) byte-for-byte on the wire so
//! the Android app and the web frontend keep working against the same
//! `config.json`. The file lives in the per-user app data directory:
//!
//! * Windows: `%APPDATA%\BigPocket\config.json`
//! * Linux/macOS: `~/.config/bigpocket/config.json`

use std::fs;
use std::path::PathBuf;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};

/// Default city for the live weather widget.
pub const DEFAULT_WEATHER_CITY: &str = "Istanbul";

/// Sentinel for "no explicit bento cell": the packer drops such a card into the
/// first free slot instead of a fixed one.
pub const GRID_AUTO_PLACEMENT: i32 = -1;

// ------------------------------- Structs -------------------------------

/// One page of the Stream Deck.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StreamDeckPage {
    #[serde(default)]
    pub id: i32,
    #[serde(default)]
    pub title: String,
}

/// Maps a foreground process to a Stream Deck page, so the right page is shown
/// automatically when the user switches applications.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AutoSwitchRule {
    /// e.g. "chrome.exe"
    #[serde(default)]
    pub process_name: String,
    #[serde(default)]
    pub page_id: i32,
}

/// One slot of a page. `grid_row`/`grid_col` pin the card to an explicit
/// 0-based bento cell; any negative value means automatic placement.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StreamDeckButton {
    #[serde(default)]
    pub id: i32,
    #[serde(default)]
    pub label: String,
    /// hotkey, command, volume_slider, toggle, live_info, clock_widget, cpu_widget, ram_widget
    #[serde(default, rename = "type")]
    pub kind: String,
    /// keybind, script, volume_slider, mic_mute, sound_mute, cpu, ram
    #[serde(default)]
    pub value: String,
    /// Custom SVG path or local image.
    #[serde(default)]
    pub icon: String,
    #[serde(default)]
    pub page: i32,
    /// on/off for toggle buttons
    #[serde(default)]
    pub state: bool,
    /// Optional accent override; "" = monochrome default surface.
    #[serde(default)]
    pub color: String,
    /// Cell height span (default 1).
    #[serde(default = "one")]
    pub row_span: i32,
    /// Cell width span (default 1).
    #[serde(default = "one")]
    pub col_span: i32,
    /// Explicit 0-based bento row (-1 = auto placement).
    #[serde(default = "auto_placement")]
    pub grid_row: i32,
    /// Explicit 0-based bento column (-1 = auto placement).
    #[serde(default = "auto_placement")]
    pub grid_col: i32,
    /// default, transparent, glass, outline
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub bg_style: String,
    /// analog, digital_clean, digital_seconds, digital_date
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub clock_style: String,
    /// mono, sans, lcd, serif
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub font_family: String,
    /// auto, compact, regular, large, huge
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub clock_font_size: String,
}

fn one() -> i32 {
    1
}

fn auto_placement() -> i32 {
    GRID_AUTO_PLACEMENT
}

impl Default for StreamDeckButton {
    fn default() -> Self {
        Self {
            id: 0,
            label: String::new(),
            kind: String::new(),
            value: String::new(),
            icon: String::new(),
            page: 0,
            state: false,
            color: String::new(),
            row_span: 1,
            col_span: 1,
            grid_row: GRID_AUTO_PLACEMENT,
            grid_col: GRID_AUTO_PLACEMENT,
            bg_style: String::new(),
            clock_style: String::new(),
            font_family: String::new(),
            clock_font_size: String::new(),
        }
    }
}

/// The whole persisted application configuration.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Config {
    #[serde(default = "default_theme")]
    pub theme: String,
    #[serde(default)]
    pub password: String,
    #[serde(default)]
    pub active_page: i32,
    /// Default city for the live weather widget.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub weather_city: String,
    #[serde(default)]
    pub stream_deck_pages: Vec<StreamDeckPage>,
    #[serde(default = "default_rows")]
    pub stream_deck_rows: i32,
    #[serde(default = "default_cols")]
    pub stream_deck_cols: i32,
    #[serde(default)]
    pub stream_deck_buttons: Vec<StreamDeckButton>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub auto_switch_rules: Vec<AutoSwitchRule>,
    #[serde(default, skip_serializing_if = "is_false")]
    pub beta_channel: bool,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub rev: i64,
}

fn is_false(v: &bool) -> bool {
    !*v
}

fn is_zero(v: &i64) -> bool {
    *v == 0
}

fn default_theme() -> String {
    "pro".to_string()
}

fn default_rows() -> i32 {
    2
}

fn default_cols() -> i32 {
    4
}

impl Default for Config {
    fn default() -> Self {
        Self::defaults()
    }
}

impl Config {
    /// The factory configuration, also used to backfill fields a stored file
    /// left out (`normalize_config`).
    pub fn defaults() -> Self {
        Self {
            theme: default_theme(),
            password: String::new(), // Default is no password
            active_page: 0,
            weather_city: DEFAULT_WEATHER_CITY.to_string(),
            stream_deck_pages: default_pages(),
            stream_deck_rows: 2,
            stream_deck_cols: 4,
            stream_deck_buttons: default_buttons(),
            auto_switch_rules: Vec::new(),
            beta_channel: false,
            rev: 0,
        }
    }
}

/// The three shipped pages: Ana Sayfa, Medya & Ses, Sistem & PC.
pub fn default_pages() -> Vec<StreamDeckPage> {
    vec![
        StreamDeckPage {
            id: 0,
            title: "Ana Sayfa".to_string(),
        },
        StreamDeckPage {
            id: 1,
            title: "Medya & Ses".to_string(),
        },
        StreamDeckPage {
            id: 2,
            title: "Sistem & PC".to_string(),
        },
    ]
}

/// Factory bento layout: 4x2 grid, every card on an explicit cell. Colors are
/// empty so each widget renders with the shared monochrome surface.
pub fn default_buttons() -> Vec<StreamDeckButton> {
    fn btn(
        id: i32,
        label: &str,
        kind: &str,
        value: &str,
        icon: &str,
        page: i32,
        state: bool,
        col_span: i32,
        row_span: i32,
        grid_row: i32,
        grid_col: i32,
    ) -> StreamDeckButton {
        StreamDeckButton {
            id,
            label: label.to_string(),
            kind: kind.to_string(),
            value: value.to_string(),
            icon: icon.to_string(),
            page,
            state,
            color: String::new(),
            row_span,
            col_span,
            grid_row,
            grid_col,
            ..Default::default()
        }
    }

    vec![
        // Page 0: Ana Sayfa — Saat & Tarih (2x1) + Hava Durumu + PC Sıcaklık,
        // then Ana Ses + Şimdi Çalıyor (2x1) + Mikrofon.
        {
            let mut b = btn(
                0,
                "Saat & Tarih",
                "clock_widget",
                "digital_clock",
                "clock",
                0,
                false,
                2,
                1,
                0,
                0,
            );
            b.clock_style = "flip_clock".to_string();
            b
        },
        btn(
            1,
            "Hava Durumu",
            "weather",
            DEFAULT_WEATHER_CITY,
            "bright",
            0,
            false,
            1,
            1,
            0,
            2,
        ),
        btn(
            2,
            "PC Sıcaklık",
            "live_info",
            "cpu_temp",
            "cpu",
            0,
            false,
            1,
            1,
            0,
            3,
        ),
        btn(
            3,
            "Ana Ses",
            "volume_slider",
            "master_volume",
            "volume",
            0,
            false,
            1,
            1,
            1,
            0,
        ),
        btn(
            4,
            "Şimdi Çalıyor",
            "now_playing",
            "media_session",
            "music",
            0,
            false,
            2,
            1,
            1,
            1,
        ),
        btn(
            5, "Mikrofon", "toggle", "mic_mute", "mic", 0, true, 1, 1, 1, 3,
        ),
        // Page 1: Medya & Ses — slot i = ID-8 → row i/4, col i%4.
        btn(
            8,
            "Ses +",
            "hotkey",
            "volumeup",
            "volume_up",
            1,
            false,
            1,
            1,
            0,
            0,
        ),
        btn(
            9,
            "Ses -",
            "hotkey",
            "volumedown",
            "volume_down",
            1,
            false,
            1,
            1,
            0,
            1,
        ),
        btn(
            10,
            "Sesi Kapat",
            "toggle",
            "sound_mute",
            "volume_mute",
            1,
            false,
            1,
            1,
            0,
            2,
        ),
        btn(
            11, "Mikrofon", "toggle", "mic_mute", "mic", 1, true, 1, 1, 0, 3,
        ),
        btn(
            12,
            "Önceki",
            "hotkey",
            "prevtrack",
            "prev",
            1,
            false,
            1,
            1,
            1,
            0,
        ),
        btn(
            13,
            "Oynat",
            "hotkey",
            "playpause",
            "play",
            1,
            false,
            1,
            1,
            1,
            1,
        ),
        btn(
            14,
            "Sonraki",
            "hotkey",
            "nexttrack",
            "skip",
            1,
            false,
            1,
            1,
            1,
            2,
        ),
        btn(
            15,
            "Spotify",
            "command",
            "cmd /c start spotify:",
            "spotify",
            1,
            false,
            1,
            1,
            1,
            3,
        ),
        // Page 2: Sistem & PC — slot i = ID-16 → row i/4, col i%4.
        btn(
            16,
            "Görev Yöneticisi",
            "command",
            "taskmgr.exe",
            "taskmgr",
            2,
            false,
            1,
            1,
            0,
            0,
        ),
        btn(
            17,
            "PC Kilitle",
            "command",
            "rundll32.exe user32.dll,LockWorkStation",
            "lock",
            2,
            false,
            1,
            1,
            0,
            1,
        ),
        btn(
            18,
            "2. Ekran",
            "toggle",
            "virtual_monitor",
            "monitor",
            2,
            false,
            1,
            1,
            0,
            2,
        ),
        btn(
            19,
            "Ekran Görüntüsü",
            "hotkey",
            "printscreen",
            "camera",
            2,
            false,
            1,
            1,
            0,
            3,
        ),
        btn(
            20,
            "CPU Yükü",
            "live_info",
            "cpu",
            "cpu",
            2,
            false,
            1,
            1,
            1,
            0,
        ),
        btn(
            21,
            "RAM Yükü",
            "live_info",
            "ram",
            "ram",
            2,
            false,
            1,
            1,
            1,
            1,
        ),
        btn(
            22, "Terminal", "command", "wt.exe", "terminal", 2, false, 1, 1, 1, 2,
        ),
        btn(
            23,
            "Masaüstü",
            "hotkey",
            "win+d",
            "home",
            2,
            false,
            1,
            1,
            1,
            3,
        ),
    ]
}

// --------------------------- Legacy migration ---------------------------

/// The pre-monochrome per-widget accent palette (the old Material-You
/// Sage/Slate/Sand/Rose tones and the template accents). They are no longer part
/// of the design system, so any stored config still carrying one is migrated back
/// to "" (the monochrome default surface) on load/save.
pub const LEGACY_DECK_COLORS: [&str; 15] = [
    "#F3D9A4", // Sand (amber)
    "#B4C8FF", // Slate (blue)
    "#A8DAB5", // Sage (mint)
    "#F2B8B5", // Rose (pink)
    "#6366F1", // indigo (template)
    "#10B981", // emerald (template)
    "#38BDF8", // sky (template)
    "#818CF8", // indigo-light (template)
    "#EF4444", // red (template)
    "#F43F5E", // rose (template)
    "#3B82F6", // blue (template)
    "#F59E0B", // amber (template)
    "#8B5CF6", // violet (template)
    "#1DB954", // spotify green (template)
    "#A1A1AA", // muted grey (template)
];

/// True when `color` is one of the retired template accents.
pub fn is_legacy_deck_color(color: &str) -> bool {
    let normalized = color.trim().to_ascii_uppercase();
    LEGACY_DECK_COLORS.iter().any(|c| *c == normalized)
}

/// Backfills the fields a stored/partial config left empty and migrates legacy
/// accent colors to the monochrome default.
pub fn normalize_config(mut cfg: Config) -> Config {
    if cfg.weather_city.trim().is_empty() {
        cfg.weather_city = DEFAULT_WEATHER_CITY.to_string();
    }
    if cfg.theme.is_empty() {
        cfg.theme = default_theme();
    }
    if cfg.stream_deck_rows <= 0 {
        cfg.stream_deck_rows = 2;
    }
    if cfg.stream_deck_cols <= 0 {
        cfg.stream_deck_cols = 4;
    }
    if cfg.stream_deck_pages.is_empty() {
        cfg.stream_deck_pages = default_pages();
    }
    if cfg.stream_deck_buttons.is_empty() {
        cfg.stream_deck_buttons = default_buttons();
    }

    for button in &mut cfg.stream_deck_buttons {
        // Migrate any stored legacy accent color to the monochrome default.
        if is_legacy_deck_color(&button.color) {
            button.color.clear();
        }
        if button.row_span <= 0 {
            button.row_span = 1;
        }
        if button.col_span <= 0 {
            button.col_span = 1;
        }
        // Any negative coordinate means "let the packer pick the cell", so
        // collapse stray values (-7, ...) onto the single sentinel the packer
        // understands.
        if button.grid_row < GRID_AUTO_PLACEMENT {
            button.grid_row = GRID_AUTO_PLACEMENT;
        }
        if button.grid_col < GRID_AUTO_PLACEMENT {
            button.grid_col = GRID_AUTO_PLACEMENT;
        }
    }
    cfg
}

// ------------------------------ Filesystem ------------------------------

/// Directory holding `config.json` and the shared-file spool.
///
/// * Windows: `%APPDATA%\BigPocket` (falling back to `%USERPROFILE%`).
/// * Linux/macOS: `~/.config/bigpocket`.
pub fn get_app_data_dir() -> PathBuf {
    #[cfg(target_os = "windows")]
    fn resolve() -> PathBuf {
        if let Some(appdata) = std::env::var_os("APPDATA") {
            if !appdata.is_empty() {
                return PathBuf::from(appdata).join("BigPocket");
            }
        }
        if let Some(profile) = std::env::var_os("USERPROFILE") {
            if !profile.is_empty() {
                return PathBuf::from(profile)
                    .join("AppData")
                    .join("Roaming")
                    .join("BigPocket");
            }
        }
        PathBuf::from("BigPocket")
    }

    #[cfg(not(target_os = "windows"))]
    fn resolve() -> PathBuf {
        if let Some(home) = std::env::var_os("HOME") {
            if !home.is_empty() {
                return PathBuf::from(home).join(".config").join("bigpocket");
            }
        }
        PathBuf::from(".config").join("bigpocket")
    }

    resolve()
}

/// Path of the persisted configuration file.
pub fn get_config_path() -> PathBuf {
    get_app_data_dir().join("config.json")
}

/// In-memory copy of the last loaded/saved config, so the hot request paths do
/// not hit the disk on every call.
static CACHE: Mutex<Option<Config>> = Mutex::new(None);

fn lock_cache() -> std::sync::MutexGuard<'static, Option<Config>> {
    // A poisoned lock only means some other thread panicked while holding it;
    // the (plain data) config is still perfectly usable.
    CACHE
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// Loads the configuration, creating the default file on first run and
/// normalizing whatever was on disk. Never fails: an unreadable file yields the
/// factory defaults.
pub fn load_config() -> Config {
    let mut cache = lock_cache();
    if let Some(cached) = cache.as_ref() {
        return cached.clone();
    }

    let path = get_config_path();
    let cfg = match fs::read(&path) {
        Ok(bytes) => match serde_json::from_slice::<Config>(&bytes) {
            Ok(parsed) => normalize_config(parsed),
            Err(err) => {
                eprintln!("config.json parse error ({err}); using defaults");
                Config::defaults()
            }
        },
        Err(_) => {
            // Missing (or unreadable) file: materialize the defaults.
            let defaults = Config::defaults();
            if let Err(err) = write_config_file(&path, &defaults) {
                eprintln!("could not create {}: {err}", path.display());
            }
            defaults
        }
    };

    *cache = Some(cfg.clone());
    cfg
}

/// Normalizes and persists `cfg`, refreshing the in-memory cache.
pub fn save_config(cfg: &Config) -> Result<(), String> {
    let normalized = normalize_config(cfg.clone());
    let path = get_config_path();
    write_config_file(&path, &normalized)?;
    *lock_cache() = Some(normalized);
    Ok(())
}

/// Drops the cached config, forcing the next `load_config` to hit the disk.
pub fn invalidate_config_cache() {
    *lock_cache() = None;
}

fn write_config_file(path: &std::path::Path, cfg: &Config) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| format!("create {}: {e}", parent.display()))?;
    }
    let data = serde_json::to_vec_pretty(cfg).map_err(|e| format!("encode config: {e}"))?;
    fs::write(path, data).map_err(|e| format!("write {}: {e}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn defaults_match_the_go_factory_config() {
        let cfg = Config::defaults();
        assert_eq!(cfg.theme, "pro");
        assert_eq!(cfg.weather_city, DEFAULT_WEATHER_CITY);
        assert_eq!(cfg.stream_deck_rows, 2);
        assert_eq!(cfg.stream_deck_cols, 4);
        assert_eq!(cfg.stream_deck_pages.len(), 3);
        assert_eq!(cfg.stream_deck_buttons.len(), 22);
        assert!(cfg.stream_deck_buttons.iter().all(|b| b.color.is_empty()));
    }

    #[test]
    fn legacy_colors_are_cleared() {
        let mut cfg = Config::defaults();
        cfg.stream_deck_buttons[0].color = " #3b82f6 ".to_string();
        cfg.stream_deck_buttons[1].color = "#000000".to_string();
        let cfg = normalize_config(cfg);
        assert!(cfg.stream_deck_buttons[0].color.is_empty());
        assert_eq!(cfg.stream_deck_buttons[1].color, "#000000");
    }

    #[test]
    fn partial_json_falls_back_to_sentinels() {
        let cfg: Config = serde_json::from_str(
            r#"{"password":"x","stream_deck_buttons":[{"id":1,"label":"A"}]}"#,
        )
        .expect("parse");
        assert_eq!(cfg.password, "x");
        assert_eq!(cfg.stream_deck_rows, 2);
        assert_eq!(cfg.stream_deck_buttons[0].grid_row, GRID_AUTO_PLACEMENT);
        assert_eq!(cfg.stream_deck_buttons[0].row_span, 1);
    }

    #[test]
    fn round_trips_through_json() {
        let cfg = Config::defaults();
        let json = serde_json::to_string(&cfg).expect("encode");
        let back: Config = serde_json::from_str(&json).expect("decode");
        assert_eq!(
            back.stream_deck_buttons.len(),
            cfg.stream_deck_buttons.len()
        );
        assert_eq!(back.stream_deck_buttons[5].state, true);
    }
}
