//! System telemetry, input automation and screen capture.
//!
//! Port of the Go server's Win32 helpers (`getDetailedSystemStats`,
//! `buildSystemStatsPayload`, the input cases of `handleWebSocketControl` and
//! `captureScreenToPNG`) onto cross-platform crates:
//!
//! * [`SystemMonitor`] — CPU / RAM / disk / network telemetry (`sysinfo`).
//! * [`InputController`] — mouse and keyboard automation (`enigo`).
//! * [`capture_screen_jpeg`] — display capture encoded as JPEG (`xcap` + `image`).

use std::time::Instant;

use enigo::{Axis, Coordinate, Direction, Enigo, Keyboard, Mouse, Settings};
use image::codecs::jpeg::JpegEncoder;
use serde_json::{json, Value};
use sysinfo::{Components, Disk, Disks, Networks, System};
use xcap::Monitor;

// Re-exported so callers (e.g. the WebSocket control handler) can name mouse
// buttons and keys without importing `enigo` themselves.
pub use enigo::{Button, Key};

/// Bytes in one gibibyte — the unit used by the telemetry payload.
const BYTES_PER_GB: f64 = 1024.0 * 1024.0 * 1024.0;
/// One "click" of the mouse wheel, as the clients report it.
const WHEEL_DELTA: i32 = 120;
/// Thermal sensors are only re-read every few seconds: the WMI/ACPI query is
/// comparatively expensive and the value moves slowly.
const COMPONENT_REFRESH_INTERVAL: std::time::Duration = std::time::Duration::from_secs(5);

fn round1(value: f64) -> f64 {
    (value * 10.0).round() / 10.0
}

fn input_error(error: enigo::InputError) -> String {
    format!("input simulation failed: {error}")
}

// ---------------------------------------------------------------------------
// System telemetry
// ---------------------------------------------------------------------------

/// Live system telemetry for the `/api/system/stats` endpoint, the WebSocket
/// ticker and the MCP tools.
///
/// The struct keeps the sampling state (sysinfo handles, previous network
/// counters) between polls, so a single instance must be reused for the whole
/// process lifetime. `get_stats_payload` takes `&mut self`; wrap the monitor in
/// a `Mutex` when sharing it between tasks.
pub struct SystemMonitor {
    system: System,
    disks: Disks,
    networks: Networks,
    components: Components,
    /// `(sampled_at, total_received, total_transmitted)` of the previous poll.
    last_net: Option<(Instant, u64, u64)>,
    last_component_refresh: Option<Instant>,
}

impl Default for SystemMonitor {
    fn default() -> Self {
        Self::new()
    }
}

impl SystemMonitor {
    pub fn new() -> Self {
        let mut system = System::new_all();
        system.refresh_cpu_usage();
        system.refresh_memory();

        let mut networks = Networks::new_with_refreshed_list();
        // Prime the counters so the first poll already measures a real rate
        // instead of a hard-coded zero.
        networks.refresh();
        let (rx, tx) = network_totals(&networks);

        Self {
            system,
            disks: Disks::new_with_refreshed_list(),
            networks,
            components: Components::new_with_refreshed_list(),
            last_net: Some((Instant::now(), rx, tx)),
            last_component_refresh: None,
        }
    }

    /// Builds the telemetry payload broadcast as `system_stats`.
    ///
    /// Field names and units match the existing clients (Flutter app, web
    /// frontend): percentages as integers, sizes in gigabytes, network rates in
    /// kilobits per second.
    pub fn get_stats_payload(&mut self) -> Value {
        self.system.refresh_cpu_usage();
        self.system.refresh_memory();

        let raw_cpu = self.system.global_cpu_usage();
        let cpu = if raw_cpu.is_finite() {
            raw_cpu.clamp(0.0, 100.0) as u32
        } else {
            0
        };

        let total_memory = self.system.total_memory();
        let used_memory = self.system.used_memory();
        let (ram, ram_used_gb, ram_total_gb) = if total_memory > 0 {
            let percent = (used_memory as f64 * 100.0 / total_memory as f64) as u32;
            (
                percent.min(100),
                round1(used_memory as f64 / BYTES_PER_GB),
                round1(total_memory as f64 / BYTES_PER_GB),
            )
        } else {
            (0, 0.0, 0.0)
        };

        let cpu_temp = self.cpu_temperature(cpu);
        let (disk_percent, disk_free_gb, disk_total_gb) = self.disk_usage();
        let (net_down_kbps, net_up_kbps) = self.network_speed_kbps();
        let media = detect_media_info();

        json!({
            "type": "system_stats",
            "cpu": cpu,
            "cpu_temp": cpu_temp,
            "ram": ram,
            "ram_used_gb": ram_used_gb,
            "ram_total_gb": ram_total_gb,
            "disk_percent": disk_percent,
            "disk_free_gb": disk_free_gb,
            "disk_total_gb": disk_total_gb,
            "net_down_kbps": net_down_kbps,
            "net_up_kbps": net_up_kbps,
            "media_title": media.title,
            "media_artist": media.artist,
            "media_playing": media.is_playing,
            "media_is_live": media.is_live,
            "media_cover": media.cover_url,
            "media_pos_sec": media.position_sec,
            "media_dur_sec": media.duration_sec,
        })
    }

    /// CPU temperature in Celsius.
    ///
    /// Prefers a thermal component that looks like a CPU sensor, otherwise the
    /// hottest available component. Many desktops expose no sensor at all, so
    /// the same mild load curve as the Go server is used as a last resort —
    /// the "PC Sıcaklık" card then never stays empty or stuck at zero.
    fn cpu_temperature(&mut self, cpu_percent: u32) -> u32 {
        if self.last_component_refresh.map_or(true, |refreshed| {
            refreshed.elapsed() >= COMPONENT_REFRESH_INTERVAL
        }) {
            self.components.refresh();
            self.last_component_refresh = Some(Instant::now());
        }

        let mut fallback: Option<f32> = None;
        for component in self.components.list() {
            let temperature = component.temperature();
            if !temperature.is_finite() || temperature <= 0.0 {
                continue;
            }
            let label = component.label().to_ascii_lowercase();
            if label.contains("cpu")
                || label.contains("core")
                || label.contains("package")
                || label.contains("tctl")
                || label.contains("tdie")
                || label.contains("k10temp")
            {
                return temperature.round() as u32;
            }
            fallback = Some(fallback.map_or(temperature, |best| best.max(temperature)));
        }

        if let Some(temperature) = fallback {
            return temperature.round() as u32;
        }

        42 + (cpu_percent as f32 * 0.38) as u32
    }

    /// Usage percentage, free space and total capacity of the system drive, in
    /// gigabytes.
    fn disk_usage(&self) -> (u32, f64, f64) {
        let Some(disk) = self.system_disk() else {
            return (0, 0.0, 0.0);
        };

        let total = disk.total_space();
        if total == 0 {
            return (0, 0.0, 0.0);
        }
        let free = disk.available_space();
        let percent = (total.saturating_sub(free) as f64 * 100.0 / total as f64) as u32;

        (
            percent.min(100),
            round1(free as f64 / BYTES_PER_GB),
            round1(total as f64 / BYTES_PER_GB),
        )
    }

    /// The volume Windows booted from (`%SystemDrive%`), falling back to the
    /// largest fixed disk and finally to the largest of any kind.
    fn system_disk(&self) -> Option<&Disk> {
        if let Ok(drive) = std::env::var("SystemDrive") {
            let drive = drive.to_ascii_lowercase();
            if !drive.is_empty() {
                let disk = self.disks.list().iter().find(|disk| {
                    disk.mount_point()
                        .to_string_lossy()
                        .to_ascii_lowercase()
                        .starts_with(&drive)
                });
                if let Some(disk) = disk {
                    return Some(disk);
                }
            }
        }

        self.disks
            .list()
            .iter()
            .filter(|disk| !disk.is_removable())
            .max_by_key(|disk| disk.total_space())
            .or_else(|| {
                self.disks
                    .list()
                    .iter()
                    .max_by_key(|disk| disk.total_space())
            })
    }

    /// Aggregate download/upload rate in kilobits per second, measured against
    /// the previous call.
    fn network_speed_kbps(&mut self) -> (u64, u64) {
        self.networks.refresh();
        let (received, transmitted) = network_totals(&self.networks);
        let now = Instant::now();

        let rate = match self.last_net {
            Some((sampled_at, previous_received, previous_transmitted)) => {
                let elapsed = now.saturating_duration_since(sampled_at).as_secs_f64();
                if elapsed > 0.0 {
                    let down =
                        received.saturating_sub(previous_received) as f64 * 8.0 / elapsed / 1000.0;
                    let up = transmitted.saturating_sub(previous_transmitted) as f64 * 8.0
                        / elapsed
                        / 1000.0;
                    (down.max(0.0) as u64, up.max(0.0) as u64)
                } else {
                    (0, 0)
                }
            }
            None => (0, 0),
        };

        self.last_net = Some((now, received, transmitted));
        rate
    }
}

/// Sums the cumulative byte counters of every known network interface.
fn network_totals(networks: &Networks) -> (u64, u64) {
    networks
        .list()
        .values()
        .fold((0u64, 0u64), |(received, transmitted), interface| {
            (
                received.saturating_add(interface.total_received()),
                transmitted.saturating_add(interface.total_transmitted()),
            )
        })
}

// ---------------------------------------------------------------------------
// Input automation
// ---------------------------------------------------------------------------

/// Mouse and keyboard automation backed by `enigo`.
///
/// Every method drives the OS input queue of the machine the server runs on, so
/// the instance must be shared (wrapped in a `Mutex`) by the request handlers.
pub struct InputController {
    enigo: Enigo,
}

unsafe impl Send for InputController {}
impl InputController {
    pub fn new() -> Result<Self, String> {
        let enigo = Enigo::new(&Settings::default())
            .map_err(|error| format!("failed to initialise input automation: {error}"))?;
        Ok(Self { enigo })
    }

    /// Moves the cursor to an absolute position in virtual-desktop pixels.
    ///
    /// enigo's absolute mode maps `0..display_size` onto the primary display
    /// only, so the delta from the current position is sent instead: this keeps
    /// the multi-monitor behaviour of the Go server's `SetCursorPos`.
    pub fn move_to(&mut self, x: i32, y: i32) -> Result<(), String> {
        let (current_x, current_y) = self.enigo.location().map_err(input_error)?;
        self.enigo
            .move_mouse(x - current_x, y - current_y, Coordinate::Rel)
            .map_err(input_error)
    }

    /// Moves the cursor by a pixel delta (the `mouse_move` control message).
    pub fn move_relative(&mut self, dx: i32, dy: i32) -> Result<(), String> {
        self.enigo
            .move_mouse(dx, dy, Coordinate::Rel)
            .map_err(input_error)
    }

    pub fn mouse_down(&mut self, button: Button) -> Result<(), String> {
        self.enigo
            .button(button, Direction::Press)
            .map_err(input_error)
    }

    pub fn mouse_up(&mut self, button: Button) -> Result<(), String> {
        self.enigo
            .button(button, Direction::Release)
            .map_err(input_error)
    }

    pub fn click(&mut self, button: Button) -> Result<(), String> {
        self.enigo
            .button(button, Direction::Click)
            .map_err(input_error)
    }

    /// Scrolls vertically.
    ///
    /// `dy` is the raw wheel delta the clients send: multiples of 120, positive
    /// scrolls up. Values that are not a whole number of wheel clicks are
    /// rounded, with a minimum of one click so small trackpad deltas still
    /// register.
    pub fn scroll(&mut self, dy: i32) -> Result<(), String> {
        if dy == 0 {
            return Ok(());
        }
        let mut notches = (dy as f64 / WHEEL_DELTA as f64).round() as i32;
        if notches == 0 {
            notches = dy.signum();
        }
        // enigo takes wheel clicks where a positive length scrolls down.
        self.enigo
            .scroll(-notches, Axis::Vertical)
            .map_err(input_error)
    }

    pub fn key_click(&mut self, key: Key) -> Result<(), String> {
        self.enigo.key(key, Direction::Click).map_err(input_error)
    }

    /// Presses and releases the key named by the `keyboard_key` control message
    /// (`"enter"`, `"backspace"`, `"up"`, ...).
    pub fn key_click_name(&mut self, name: &str) -> Result<(), String> {
        let key = key_from_name(name).ok_or_else(|| format!("unknown key: {name}"))?;
        self.key_click(key)
    }

    /// Types a text snippet, unicode characters included (the `keyboard_input`
    /// control message and `text_snippet` buttons).
    pub fn text(&mut self, text: &str) -> Result<(), String> {
        self.enigo.text(text).map_err(input_error)
    }

    /// Presses a `+`-separated shortcut such as `"ctrl+shift+m"` or
    /// `"prevtrack"`, releasing the keys in reverse order. Unknown tokens are
    /// skipped, but never silently: each one is logged so a mis-configured
    /// button is visible in the log instead of appearing to do nothing.
    pub fn simulate_hotkey(&mut self, hotkey: &str) -> Result<(), String> {
        let mut keys: Vec<Key> = Vec::with_capacity(4);
        for token in hotkey.split('+') {
            match key_from_name(token) {
                Some(key) => keys.push(key),
                None if token.trim().is_empty() => {}
                None => {
                    tracing::warn!("simulate_hotkey: unknown key token '{token}' in '{hotkey}'");
                }
            }
        }
        if keys.is_empty() {
            return Ok(());
        }

        for key in &keys {
            self.enigo
                .key(*key, Direction::Press)
                .map_err(input_error)?;
        }
        for key in keys.iter().rev() {
            self.enigo
                .key(*key, Direction::Release)
                .map_err(input_error)?;
        }
        Ok(())
    }
}

/// Maps a shortcut token or key name to an enigo [`Key`].
///
/// Accepts the names used by the Go server and by the clients: modifiers
/// (`ctrl`, `shift`, `alt`, `meta`), navigation and editing keys, media and
/// volume keys, function keys up to `f24`, and any single character.
pub fn key_from_name(name: &str) -> Option<Key> {
    let normalized = name.trim().to_ascii_lowercase();

    match normalized.as_str() {
        "enter" | "return" => Some(Key::Return),
        "backspace" => Some(Key::Backspace),
        "space" | "spacebar" => Some(Key::Space),
        "tab" => Some(Key::Tab),
        "escape" | "esc" => Some(Key::Escape),
        "delete" | "del" => Some(Key::Delete),
        "up" | "uparrow" => Some(Key::UpArrow),
        "down" | "downarrow" => Some(Key::DownArrow),
        "left" | "leftarrow" => Some(Key::LeftArrow),
        "right" | "rightarrow" => Some(Key::RightArrow),
        "ctrl" | "control" => Some(Key::Control),
        "shift" => Some(Key::Shift),
        "alt" => Some(Key::Alt),
        "meta" | "win" | "super" | "cmd" => Some(Key::Meta),
        "volumeup" | "volume_up" => Some(Key::VolumeUp),
        "volumedown" | "volume_down" => Some(Key::VolumeDown),
        "mute" | "volumemute" | "volume_mute" => Some(Key::VolumeMute),
        "playpause" | "play_pause" | "media_play_pause" => Some(Key::MediaPlayPause),
        "nexttrack" | "next_track" | "next" => Some(Key::MediaNextTrack),
        "prevtrack" | "prev_track" | "prev" | "previous" => Some(Key::MediaPrevTrack),
        #[cfg(target_os = "windows")]
        "printscreen" | "print_screen" | "prtsc" | "snapshot" => Some(Key::Snapshot),
        "f1" => Some(Key::F1),
        "f2" => Some(Key::F2),
        "f3" => Some(Key::F3),
        "f4" => Some(Key::F4),
        "f5" => Some(Key::F5),
        "f6" => Some(Key::F6),
        "f7" => Some(Key::F7),
        "f8" => Some(Key::F8),
        "f9" => Some(Key::F9),
        "f10" => Some(Key::F10),
        "f11" => Some(Key::F11),
        "f12" => Some(Key::F12),
        "f13" => Some(Key::F13),
        "f14" => Some(Key::F14),
        "f15" => Some(Key::F15),
        "f16" => Some(Key::F16),
        "f17" => Some(Key::F17),
        "f18" => Some(Key::F18),
        "f19" => Some(Key::F19),
        "f20" => Some(Key::F20),
        #[cfg(any(target_os = "windows", all(unix, not(target_os = "macos"))))]
        "f21" => Some(Key::F21),
        #[cfg(any(target_os = "windows", all(unix, not(target_os = "macos"))))]
        "f22" => Some(Key::F22),
        #[cfg(any(target_os = "windows", all(unix, not(target_os = "macos"))))]
        "f23" => Some(Key::F23),
        #[cfg(any(target_os = "windows", all(unix, not(target_os = "macos"))))]
        "f24" => Some(Key::F24),
        _ => {
            let mut chars = normalized.chars();
            match (chars.next(), chars.next()) {
                // Single characters (letters, digits, punctuation) are entered
                // through their unicode value: enigo resolves the scancode on
                // Windows and the keysym elsewhere.
                (Some(character), None) => Some(Key::Unicode(character)),
                _ => None,
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Screen capture
// ---------------------------------------------------------------------------

/// Captures the primary display and returns the frame as JPEG bytes.
///
/// `quality` is the JPEG quality (1-100) the `capture_screen` tool takes from
/// the client, which streams the frame instead of writing a PNG to disk.
pub fn capture_screen_jpeg(quality: u8) -> Result<Vec<u8>, String> {
    let monitors =
        Monitor::all().map_err(|error| format!("failed to enumerate monitors: {error}"))?;
    let monitor = monitors
        .iter()
        .find(|monitor| monitor.is_primary().unwrap_or(false))
        .or_else(|| monitors.first())
        .ok_or_else(|| "no active display found".to_string())?;

    encode_monitor_jpeg(monitor, quality)
}

/// Captures a specific monitor (by index, as returned by [`Monitor::all`]) and
/// returns the frame as JPEG bytes.
pub fn capture_monitor_jpeg(index: usize, quality: u8) -> Result<Vec<u8>, String> {
    let monitors =
        Monitor::all().map_err(|error| format!("failed to enumerate monitors: {error}"))?;
    let monitor = monitors
        .get(index)
        .ok_or_else(|| format!("no monitor at index {index}"))?;

    encode_monitor_jpeg(monitor, quality)
}

/// Index (into [`Monitor::all`]) of the display the screen stream captures: the
/// first non-primary monitor when more than one display is present, otherwise
/// the primary/first display.
///
/// The TCP screen stream and the touch handler both resolve the target display
/// through this function, so the coordinates a phone reports map to exactly the
/// panel it is looking at.
pub fn stream_monitor_index(monitors: &[Monitor]) -> Option<usize> {
    if monitors.is_empty() {
        return None;
    }
    if monitors.len() > 1 {
        if let Some(index) = monitors
            .iter()
            .position(|monitor| !monitor.is_primary().unwrap_or(false))
        {
            return Some(index);
        }
        return Some(1);
    }
    Some(0)
}

/// Bounds `(x, y, width, height)` of a monitor in virtual-desktop coordinates,
/// used to translate relative touch coordinates into cursor positions.
pub fn monitor_bounds(index: usize) -> Option<(i32, i32, u32, u32)> {
    let monitors = Monitor::all().ok()?;
    let monitor = monitors.get(index)?;
    Some((
        monitor.x().unwrap_or(0),
        monitor.y().unwrap_or(0),
        monitor.width().unwrap_or(0),
        monitor.height().unwrap_or(0),
    ))
}

fn encode_monitor_jpeg(monitor: &Monitor, quality: u8) -> Result<Vec<u8>, String> {
    let frame = monitor
        .capture_image()
        .map_err(|error| format!("screen capture failed: {error}"))?;

    let mut buffer = Vec::new();
    let mut encoder = JpegEncoder::new_with_quality(&mut buffer, quality.clamp(1, 100));
    encoder
        .encode_image(&frame)
        .map_err(|error| format!("jpeg encode failed: {error}"))?;

    Ok(buffer)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_common_key_names() {
        assert_eq!(key_from_name("Enter"), Some(Key::Return));
        assert_eq!(key_from_name(" ctrl "), Some(Key::Control));
        assert_eq!(key_from_name("a"), Some(Key::Unicode('a')));
        assert_eq!(key_from_name("F5"), Some(Key::F5));
        assert_eq!(key_from_name("prevtrack"), Some(Key::MediaPrevTrack));
        assert_eq!(key_from_name("camera"), None);
        assert_eq!(key_from_name(""), None);
    }

    #[test]
    fn strips_browser_and_youtube_title_suffixes() {
        assert_eq!(strip_tab_suffixes("Funny Cats - YouTube - Google Chrome"), "Funny Cats");
        assert_eq!(strip_tab_suffixes("Song - YouTube Music - Brave"), "Song");
        assert_eq!(strip_tab_suffixes("YouTube - YENİ TRANSFERLERİ İLE BURSASPOR'U KURTARIYORUZ ! - YouTube"), "YENİ TRANSFERLERİ İLE BURSASPOR'U KURTARIYORUZ !");
        assert_eq!(strip_tab_suffixes("(1) YouTube - Funny Cats - YouTube"), "Funny Cats");
        assert_eq!(strip_tab_suffixes("Track - VLC media player"), "Track - VLC media player");
    }

    #[test]
    fn youtube_pages_are_not_videos() {
        assert!(is_youtube_video("YouTube", "Funny Cats (4K)"));
        assert!(is_youtube_video("YouTube Music", "Back In Black"));
        assert!(is_youtube_video("AC/DC", "Back In Black - YouTube"));
        assert!(!is_youtube_video("YouTube", "YouTube Studio"));
        assert!(!is_youtube_video("YouTube", "Home"));
        assert!(!is_youtube_video("YouTube", ""));
        assert!(!is_youtube_video("Google Chrome", "Weather - Google Chrome"));
        assert!(is_generic_youtube_page("YouTube Studio - Google Chrome"));
        assert!(is_generic_youtube_page("Ana Sayfa - YouTube"));
        assert!(!is_generic_youtube_page("Funny Cats - YouTube"));
    }

    #[test]
    fn plans_cover_lookups_only_for_media() {
        assert!(matches!(
            cover_lookup_plan("YouTube", "Funny Cats"),
            Some(CoverLookupPlan::YouTubeVideo(_))
        ));
        assert!(matches!(
            cover_lookup_plan("Daft Punk", "One More Time"),
            Some(CoverLookupPlan::ITunes(_))
        ));
        assert!(cover_lookup_plan("YouTube", "Subscriptions").is_none());
        assert!(cover_lookup_plan("VLC Player", "song.mp3").is_none());
        assert!(cover_lookup_plan("Google Chrome", "Some Tab - Google Chrome").is_none());
        assert!(cover_lookup_plan("", "Untitled").is_none());
    }

    #[test]
    fn parses_the_first_youtube_hit_with_its_title() {
        let html = r#"{"videoRenderer":{"videoId":"dQw4w9WgXcQ","thumbnail":{"thumbnails":[]},"title":{"runs":[{"text":"Rick Astley - Never Gonna Give You Up"}]},"lengthText":{"simpleText":"3:33"}}}"#;
        assert_eq!(
            parse_first_video_result(html),
            Some((
                "dQw4w9WgXcQ".to_string(),
                "Rick Astley - Never Gonna Give You Up".to_string(),
                213
            ))
        );
        assert_eq!(parse_first_video_result("<html>no results</html>"), None);
    }

    #[test]
    fn decodes_escaped_youtube_titles() {
        let html = r#""videoId":"abcdefghijk","title":{"runs":[{"text":"Iron Maiden \u0026 Metallica \"Live\""}]},"lengthText":{"simpleText":"5:00"}"#;
        assert_eq!(
            parse_first_video_result(html),
            Some((
                "abcdefghijk".to_string(),
                "Iron Maiden & Metallica \"Live\"".to_string(),
                300
            ))
        );
    }
    #[test]
    fn rejects_unrelated_youtube_hits() {
        assert!(token_overlap_percent("Funny Cats Compilation", "Funny Cats Compilation 2024") >= 50);
        assert!(token_overlap_percent("Funny Cats Compilation", "Cooking Pasta With Grandpa") < 50);
        assert_eq!(token_overlap_percent("", "Anything"), 0);
    }
}

#[derive(Debug, Clone, Default)]
pub struct MediaInfo {
    pub title: String,
    pub artist: String,
    pub is_playing: bool,
    pub is_live: bool,
    pub cover_url: String,
    pub position_sec: u32,
    pub duration_sec: u32,
}

/// Elapsed playback time for media that is only known through its window
/// title.
///
/// No player publishes a real timeline without a media session, so this tracks
/// the seconds the current title has been playing and reports an unknown
/// duration: the clients then draw no progress bar instead of one racing
/// towards an invented total (the old code hard-coded 215 seconds).
static MEDIA_TIMELINE: std::sync::Mutex<Option<MediaTimelineTracker>> = std::sync::Mutex::new(None);

struct MediaTimelineTracker {
    last_title: String,
    started_at: std::time::Instant,
    accumulated_sec: u32,
    is_playing: bool,
}

impl MediaTimelineTracker {
    /// Seconds the current title has been playing (0 when nothing is known).
    fn update(title: &str, is_playing: bool) -> u32 {
        if title.is_empty() || title == "Spotify" || title == "Duraklatıldı" {
            return 0;
        }
        let mut guard = match MEDIA_TIMELINE.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        };
        let tracker = guard.get_or_insert_with(|| MediaTimelineTracker {
            last_title: title.to_string(),
            started_at: std::time::Instant::now(),
            accumulated_sec: 0,
            is_playing,
        });

        if tracker.last_title != title {
            tracker.last_title = title.to_string();
            tracker.started_at = std::time::Instant::now();
            tracker.accumulated_sec = 0;
            tracker.is_playing = is_playing;
            return 0;
        }

        if tracker.is_playing != is_playing {
            if is_playing {
                tracker.started_at = std::time::Instant::now();
            } else {
                tracker.accumulated_sec = tracker.accumulated_sec.saturating_add(tracker.started_at.elapsed().as_secs() as u32);
            }
            tracker.is_playing = is_playing;
        }

        if tracker.is_playing {
            tracker.accumulated_sec.saturating_add(tracker.started_at.elapsed().as_secs() as u32)
        } else {
            tracker.accumulated_sec
        }
    }
}

static CURRENT_MEDIA_THUMBNAIL: std::sync::Mutex<Option<(String, Vec<u8>)>> = std::sync::Mutex::new(None);

pub fn get_current_media_cover_bytes() -> Option<Vec<u8>> {
    CURRENT_MEDIA_THUMBNAIL.lock().ok()?.as_ref().map(|(_, bytes)| bytes.clone())
}

/// Drops the cached GSMTC artwork as soon as the reported track changes, so
/// `/api/media/cover` never serves the previous song's cover for the current
/// one.
#[cfg(windows)]
fn sync_thumbnail_with_title(title: &str) {
    if let Ok(mut guard) = CURRENT_MEDIA_THUMBNAIL.lock() {
        if guard.as_ref().is_some_and(|(cached_title, _)| cached_title != title) {
            *guard = None;
        }
    }
}

/// How long a caller waits for the media worker thread before falling back to
/// the window titles.
///
/// WinRT's `IAsyncOperation::get()` blocks until COM dispatches the completion
/// callback. On a thread whose COM apartment was never initialized that
/// callback never arrives, which parked `spawn_stats_ticker` — and every other
/// task waiting for the `state.screen` mutex it holds — forever. Queries are
/// therefore answered by a worker that owns a properly initialized MTA
/// apartment and are always bounded by this timeout.
#[cfg(windows)]
const GSMTC_QUERY_TIMEOUT: std::time::Duration = std::time::Duration::from_millis(1500);
/// Timeouts in a row before the worker thread is parked and polling backs off.
#[cfg(windows)]
const GSMTC_MAX_FAILURES: u32 = 3;
/// First backoff applied after repeated timeouts (doubled on each further
/// failing cycle, capped at [`GSMTC_MAX_COOLDOWN`]).
#[cfg(windows)]
const GSMTC_COOLDOWN: std::time::Duration = std::time::Duration::from_secs(60);
#[cfg(windows)]
const GSMTC_MAX_COOLDOWN: std::time::Duration = std::time::Duration::from_secs(600);
/// Advancing playback without any advertised duration for this many seconds
/// before the session is accepted as a continuous stream.
#[cfg(windows)]
const LIVE_CONFIRM_SEC: u32 = 5;
/// Slack allowed before a position past the advertised end counts as a stream.
#[cfg(windows)]
const LIVE_OVERSHOOT_SEC: u32 = 10;
/// Longest gap between two samples that still describes the same playback.
#[cfg(windows)]
const LIVE_SESSION_GAP: std::time::Duration = std::time::Duration::from_secs(30);

/// Dedicated COM apartment for the Windows media session queries: the WinRT
/// `get()` calls block on COM completion callbacks, and a caller on a thread
/// without an initialized apartment can wait forever.
#[cfg(windows)]
mod gsmtc {
    use super::MediaInfo;
    use std::sync::mpsc::{self, Receiver, Sender};
    use std::sync::{Mutex, MutexGuard};
    use std::time::{Duration, Instant};

    /// One unit of work for the worker thread.
    enum Request {
        /// Answer with the current media session (`None` = nothing to report).
        Query(Sender<Option<MediaInfo>>),
        /// Execute media transport control (playpause, next, prev).
        Action(String, Sender<bool>),
    }
    struct Bridge {
        /// `None` while no worker runs: not started yet, exited, or parked
        /// after repeated timeouts.
        worker: Option<Sender<Request>>,
        /// Timeouts in a row without a successful answer.
        consecutive_failures: u32,
        /// Current backoff, doubled after every failing cycle.
        cooldown: Duration,
        /// While set, queries answer `None` without waking the worker.
        cooldown_until: Option<Instant>,
    }

    static BRIDGE: Mutex<Bridge> = Mutex::new(Bridge {
        worker: None,
        consecutive_failures: 0,
        cooldown: super::GSMTC_COOLDOWN,
        cooldown_until: None,
    });

    fn bridge() -> MutexGuard<'static, Bridge> {
        match BRIDGE.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    /// Current Windows media session, or `None` — the caller then falls back to
    /// the window titles — when GSMTC times out or is in a backoff window.
    pub(super) fn query() -> Option<MediaInfo> {
        let (reply_tx, reply_rx) = mpsc::channel();
        {
            let mut bridge = bridge();
            if let Some(until) = bridge.cooldown_until {
                if Instant::now() < until {
                    return None;
                }
                bridge.cooldown_until = None;
            }
            if bridge.worker.is_none() {
                bridge.worker = spawn_worker();
            }
            let worker = match bridge.worker.as_ref() {
                Some(worker) => worker.clone(),
                None => return None,
            };
            if worker.send(Request::Query(reply_tx)).is_err() {
                // The worker thread is gone; the next poll starts a new one.
                bridge.worker = None;
                return None;
            }
        }

        match reply_rx.recv_timeout(super::GSMTC_QUERY_TIMEOUT) {
            Ok(media) => {
                let mut bridge = bridge();
                bridge.consecutive_failures = 0;
                bridge.cooldown = super::GSMTC_COOLDOWN;
                bridge.cooldown_until = None;
                media
            }
            Err(_) => {
                // Stuck inside WinRT or died mid-query: drop it — a stuck
                // thread ends by itself once the operation completes — and back
                // off before trying again, so timeout cycles cannot pile up
                // threads.
                let mut bridge = bridge();
                bridge.worker = None;
                bridge.consecutive_failures += 1;
                if bridge.consecutive_failures >= super::GSMTC_MAX_FAILURES {
                    bridge.consecutive_failures = 0;
                    bridge.cooldown = (bridge.cooldown * 2).min(super::GSMTC_MAX_COOLDOWN);
                    bridge.cooldown_until = Some(Instant::now() + bridge.cooldown);
                    tracing::warn!(
                        "Windows media session queries kept timing out; retrying in {}s",
                        bridge.cooldown.as_secs()
                    );
                }
                None
            }
        }
    }

    pub(super) fn action(action: &str) -> bool {
        let (reply_tx, reply_rx) = mpsc::channel();
        {
            let mut bridge = bridge();
            if bridge.worker.is_none() {
                bridge.worker = spawn_worker();
            }
            let worker = match bridge.worker.as_ref() {
                Some(w) => w.clone(),
                None => return false,
            };
            if worker.send(Request::Action(action.to_string(), reply_tx)).is_err() {
                bridge.worker = None;
                return false;
            }
        }
        reply_rx.recv_timeout(Duration::from_millis(1500)).unwrap_or(false)
    }

    /// Starts the worker thread, if the OS still lets us create one.
    fn spawn_worker() -> Option<Sender<Request>> {
        let (request_tx, request_rx) = mpsc::channel::<Request>();
        std::thread::Builder::new()
            .name("gsmtc-media".to_string())
            .spawn(move || worker_main(request_rx))
            .ok()?;
        Some(request_tx)
    }

    /// Owns an MTA COM apartment — the WinRT completion callbacks need one, and
    /// MTA needs no message pump — and answers one query at a time.
    fn worker_main(request_rx: Receiver<Request>) {
        /// `COINIT_MULTITHREADED`.
        const COINIT_MULTITHREADED: u32 = 0x0000_0000;
        /// `RPC_E_CHANGED_MODE`: the thread already lives in another apartment;
        /// the WinRT calls still work, so this is not an error here.
        const RPC_E_CHANGED_MODE: i32 = 0x8001_0106u32 as i32;

        #[link(name = "ole32")]
        extern "system" {
            fn CoInitializeEx(pv_reserved: *const core::ffi::c_void, dw_co_init: u32) -> i32;
            fn CoUninitialize();
        }

        let initialized = unsafe { CoInitializeEx(core::ptr::null(), COINIT_MULTITHREADED) };
        if initialized < 0 && initialized != RPC_E_CHANGED_MODE {
            tracing::warn!("COM apartment initialization failed: 0x{:08X}", initialized as u32);
        }
        while let Ok(req) = request_rx.recv() {
            match req {
                Request::Query(reply) => {
                    let _ = reply.send(super::query_windows_media_session());
                }
                Request::Action(action, reply) => {
                    let _ = reply.send(super::execute_windows_media_action(&action));
                }
            }
        }

        unsafe { CoUninitialize() };
    }
}

pub fn control_media(action: &str) {
    let act = action.trim().to_ascii_lowercase();
    #[cfg(windows)]
    {
        use std::ffi::c_void;
        type HWND = *mut c_void;
        type BOOL = i32;
        type LPARAM = isize;
        type WPARAM = usize;
        type UINT = u32;

        const WM_APPCOMMAND: UINT = 0x0319;
        const HWND_BROADCAST: HWND = 0xFFFF as HWND;
        const APPCOMMAND_MEDIA_PLAY_PAUSE: u32 = 14;
        const APPCOMMAND_MEDIA_NEXTTRACK: u32 = 11;
        const APPCOMMAND_MEDIA_PREVIOUSTRACK: u32 = 12;

        #[link(name = "user32")]
        extern "system" {
            fn keybd_event(bVk: u8, bScan: u8, dwFlags: u32, dwExtraInfo: usize);
            fn PostMessageW(hWnd: HWND, Msg: UINT, wParam: WPARAM, lParam: LPARAM) -> BOOL;
            fn EnumWindows(lpEnumFunc: unsafe extern "system" fn(HWND, LPARAM) -> BOOL, lParam: LPARAM) -> BOOL;
            fn IsWindowVisible(hWnd: HWND) -> BOOL;
            fn GetWindowTextLengthW(hWnd: HWND) -> i32;
            fn GetWindowTextW(hWnd: HWND, lpString: *mut u16, nMaxCount: i32) -> i32;
        }

        let (vk, cmd) = match act.as_str() {
            "next" | "nexttrack" | "next_track" => (0xB0u8, APPCOMMAND_MEDIA_NEXTTRACK),
            "prev" | "prevtrack" | "prev_track" | "previous" => (0xB1u8, APPCOMMAND_MEDIA_PREVIOUSTRACK),
            _ => (0xB3u8, APPCOMMAND_MEDIA_PLAY_PAUSE),
        };

        // 1. Instant hardware keyboard simulation (0 ms latency)
        unsafe {
            keybd_event(vk, 0, 0, 0);
            keybd_event(vk, 0, 2, 0);
        }

        // 2. Direct WM_APPCOMMAND to all candidate media players & browsers
        let lparam = ((cmd as usize) << 16) as LPARAM;
        struct MediaCollector {
            hwnds: Vec<HWND>,
        }
        unsafe extern "system" fn enum_media_proc(hwnd: HWND, lparam: LPARAM) -> BOOL {
            if IsWindowVisible(hwnd) != 0 {
                let len = GetWindowTextLengthW(hwnd);
                if len > 0 && len < 256 {
                    let mut buf = [0u16; 256];
                    let read = GetWindowTextW(hwnd, buf.as_mut_ptr(), 256);
                    if read > 0 {
                        let title = String::from_utf16_lossy(&buf[..read as usize]).to_lowercase();
                        if title.contains("youtube")
                            || title.contains("spotify")
                            || title.contains("chrome")
                            || title.contains("brave")
                            || title.contains("edge")
                            || title.contains("firefox")
                            || title.contains("vlc")
                        {
                            let collector = &mut *(lparam as *mut MediaCollector);
                            collector.hwnds.push(hwnd);
                        }
                    }
                }
            }
            1
        }
        let mut collector = MediaCollector { hwnds: Vec::new() };
        unsafe {
            EnumWindows(enum_media_proc, &mut collector as *mut _ as LPARAM);
            for h in collector.hwnds {
                PostMessageW(h, WM_APPCOMMAND, h as WPARAM, lparam);
            }
            // 3. Broadcast WM_APPCOMMAND to top-level windows
            PostMessageW(HWND_BROADCAST, WM_APPCOMMAND, 0, lparam);
        }

        // 4. Background GSMTC action (non-blocking)
        let act_clone = act.clone();
        let _ = std::thread::spawn(move || {
            let _ = gsmtc::action(&act_clone);
        });
    }
}
#[cfg(windows)]
fn execute_windows_media_action(action: &str) -> bool {
    use windows::Media::Control::GlobalSystemMediaTransportControlsSessionManager;
    let manager = match GlobalSystemMediaTransportControlsSessionManager::RequestAsync() {
        Ok(op) => match op.get() {
            Ok(m) => m,
            Err(_) => return false,
        },
        Err(_) => return false,
    };
    let session = match manager.GetCurrentSession() {
        Ok(s) => s,
        Err(_) => return false,
    };
    match action {
        "playpause" | "play_pause" | "play" | "pause" => {
            session.TryTogglePlayPauseAsync().is_ok()
        }
        "next" | "nexttrack" | "next_track" => {
            session.TrySkipNextAsync().is_ok()
        }
        "prev" | "prevtrack" | "prev_track" | "previous" => {
            session.TrySkipPreviousAsync().is_ok()
        }
        _ => false,
    }
}

/// Current Windows media session, queried on the [`gsmtc`] worker thread.
#[cfg(windows)]
fn query_windows_media_control() -> Option<MediaInfo> {
    gsmtc::query()
}

/// Reads the current GSMTC session. Only ever called on the worker thread,
/// which owns an initialized COM apartment.
#[cfg(windows)]
fn query_windows_media_session() -> Option<MediaInfo> {
    use windows::Media::Control::GlobalSystemMediaTransportControlsSessionManager;
    use windows::Media::Control::GlobalSystemMediaTransportControlsSessionPlaybackStatus;

    eprintln!("DEBUG: query_windows_media_session START");
    let manager_op = match GlobalSystemMediaTransportControlsSessionManager::RequestAsync() {
        Ok(op) => op,
        Err(e) => {
            eprintln!("DEBUG: RequestAsync err: {:?}", e);
            return None;
        }
    };
    let manager = match manager_op.get() {
        Ok(m) => m,
        Err(e) => {
            eprintln!("DEBUG: manager get err: {:?}", e);
            return None;
        }
    };
    eprintln!("DEBUG: manager acquired");
    let session = match manager.GetCurrentSession() {
        Ok(s) => s,
        Err(e) => {
            eprintln!("DEBUG: GetCurrentSession err: {:?}", e);
            return None;
        }
    };
    eprintln!("DEBUG: session acquired");
    let timeline = session.GetTimelineProperties().ok()?;
    // Times are 100-nanosecond ticks. A player that has no timeline (live
    // streams, most browsers) reports an end time of zero, and some report a
    // non-zero start offset that must not be counted into the position.
    let start_ticks = timeline.StartTime().ok().map_or(0, |time| time.Duration);
    let pos_ticks = timeline.Position().ok()?.Duration;
    let end_ticks = timeline.EndTime().ok()?.Duration;
    let position_sec = ticks_to_secs(pos_ticks.saturating_sub(start_ticks));
    let duration_sec = if end_ticks > start_ticks {
        ticks_to_secs(end_ticks - start_ticks)
    } else {
        0
    };

    let playback_info = session.GetPlaybackInfo().ok()?;
    let status = playback_info.PlaybackStatus().ok()?;
    let is_playing = status == GlobalSystemMediaTransportControlsSessionPlaybackStatus::Playing;

    let media_props = session.TryGetMediaPropertiesAsync().ok()?.get().ok()?;
    let title = media_props.Title().ok()?.to_string();
    let artist = media_props.Artist().ok()?.to_string();

    // Without a title there is nothing to show; the window-title fallback
    // sometimes still knows the track.
    if title.trim().is_empty() {
        return None;
    }

    let has_direct_thumb = read_session_thumbnail(&media_props, &title);

    let (cover, fb_dur) = if has_direct_thumb {
        (format!("/api/media/cover?k={}", url_encode(&title)), 0)
    } else {
        get_cached_media_cover(&artist, &title)
    };
    let duration_sec = if duration_sec > 0 { duration_sec } else { fb_dur };
    let is_live = observe_live_timeline(&title, position_sec, duration_sec);
    Some(MediaInfo {
        title,
        artist,
        is_playing,
        is_live,
        cover_url: cover,
        position_sec,
        duration_sec,
    })
}

/// Copies the session artwork into [`CURRENT_MEDIA_THUMBNAIL`] and reports
/// whether one was found.
#[cfg(windows)]
fn read_session_thumbnail(
    media_props: &windows::Media::Control::GlobalSystemMediaTransportControlsSessionMediaProperties,
    title: &str,
) -> bool {
    let Ok(thumb_ref) = media_props.Thumbnail() else {
        return false;
    };
    let Ok(stream_op) = thumb_ref.OpenReadAsync() else {
        return false;
    };
    let Ok(stream) = stream_op.get() else {
        return false;
    };
    let Ok(size) = stream.Size() else {
        return false;
    };
    if size == 0 || size >= 5 * 1024 * 1024 {
        return false;
    }
    use windows::Storage::Streams::DataReader;
    let Ok(reader) = DataReader::CreateDataReader(&stream) else {
        return false;
    };
    let Ok(load_op) = reader.LoadAsync(size as u32) else {
        return false;
    };
    if load_op.get().is_err() {
        return false;
    }
    let mut bytes = vec![0u8; size as usize];
    if reader.ReadBytes(&mut bytes).is_err() {
        return false;
    }
    match CURRENT_MEDIA_THUMBNAIL.lock() {
        Ok(mut guard) => {
            *guard = Some((title.to_string(), bytes));
            true
        }
        Err(_) => false,
    }
}

/// Converts 100-nanosecond ticks to whole seconds, clamped to `u32`.
#[cfg(windows)]
fn ticks_to_secs(ticks: i64) -> u32 {
    (ticks.max(0) / 10_000_000).min(u32::MAX as i64) as u32
}

/// Remembers how the playback timeline of the current media item evolves.
#[cfg(windows)]
struct LiveTimelineTracker {
    title: String,
    position_sec: u32,
    updated_at: std::time::Instant,
    /// The position moved forward at least once for this title.
    advancing: bool,
    /// A real duration was reported for this title at least once.
    duration_seen: bool,
}

#[cfg(windows)]
static LIVE_TIMELINE: std::sync::Mutex<Option<LiveTimelineTracker>> = std::sync::Mutex::new(None);

/// Tells a real live stream apart from a fixed song.
///
/// A stream has no usable end time: it either reports none at all while the
/// position keeps moving (HLS/DASH, YouTube live), or the position runs past
/// the end the player last advertised. A track that merely reaches its final
/// seconds is *not* live — the previous heuristic marked every song's last 20
/// seconds as a stream, which pinned a permanent "CANLI YAYIN" badge on the
/// clients.
#[cfg(windows)]
fn observe_live_timeline(title: &str, position_sec: u32, duration_sec: u32) -> bool {
    let now = std::time::Instant::now();
    let mut guard = match LIVE_TIMELINE.lock() {
        Ok(guard) => guard,
        Err(poisoned) => poisoned.into_inner(),
    };
    let tracker = guard.get_or_insert_with(|| LiveTimelineTracker {
        title: title.to_string(),
        position_sec,
        updated_at: now,
        advancing: false,
        duration_seen: duration_sec > 0,
    });

    if tracker.title != title || now.duration_since(tracker.updated_at) > LIVE_SESSION_GAP {
        *tracker = LiveTimelineTracker {
            title: title.to_string(),
            position_sec,
            updated_at: now,
            advancing: false,
            duration_seen: duration_sec > 0,
        };
    } else {
        tracker.advancing |= position_sec > tracker.position_sec;
        tracker.position_sec = position_sec;
        tracker.updated_at = now;
        tracker.duration_seen |= duration_sec > 0;
    }

    let stream_without_duration =
        duration_sec == 0 && !tracker.duration_seen && tracker.advancing && position_sec >= LIVE_CONFIRM_SEC;
    let overshoot = tracker.advancing
        && duration_sec > 0
        && position_sec > duration_sec.saturating_add(LIVE_OVERSHOOT_SEC);
    stream_without_duration || overshoot
}

// ---------------------------------------------------------------------------
// Media cover lookup
// ---------------------------------------------------------------------------

/// Hard cap on remembered cover lookups. An unbounded map grew with every song
/// ever played (one string per track, plus the value) for the whole lifetime of
/// the process.
const MEDIA_COVER_CACHE_MAX: usize = 256;
/// How long a failed lookup is remembered before the track may be searched
/// again. Without it a track with no artwork re-spawned `curl` on *every*
/// telemetry tick — a child process per poll is both an AV heuristic trigger and
/// pure waste.
const MEDIA_COVER_MISS_TTL: std::time::Duration = std::time::Duration::from_secs(10 * 60);
/// How long an in-flight lookup may stay unclaimed before it is retried (the
/// blocking task can be dropped if the runtime shuts down mid-flight).
const MEDIA_COVER_PENDING_TTL: std::time::Duration = std::time::Duration::from_secs(60);
/// Most cover lookups allowed to run at the same time. Skipping through tracks
/// can request a new title every couple of seconds, and each lookup starts its
/// own thread plus a `curl` child process; AV heuristics count *simultaneous*
/// children, so the burst is capped and extra titles just retry on the next
/// poll. Stale `Pending` markers (a lookup whose thread died) free their slot
/// after [`MEDIA_COVER_PENDING_TTL`].
const MEDIA_COVER_MAX_INFLIGHT: usize = 2;

/// State of one `"artist - title"` key in [`MEDIA_COVER_CACHE`].
enum CoverState {
    /// A background lookup is running; do not start a second one.
    Pending,
    /// Artwork URL and duration resolved.
    Ready(String, u32),
    /// Lookup finished without artwork.
    Missing(Instant),
}

struct CoverEntry {
    state: CoverState,
    inserted: Instant,
}

/// Outcome of a cache probe.
enum CoverLookup {
    /// Cached artwork URL and duration.
    Hit(String, u32),
    /// Nothing to report: the key has no artwork, is already being searched, or
    /// was dropped because [`MEDIA_COVER_MAX_INFLIGHT`] lookups are running.
    Miss,
    /// Absent or stale; the caller should search (the key is now `Pending`).
    Search,
}
/// `HashMap::new` is not a `const fn`, so the map itself is created on first
/// use (a `Mutex<HashMap<…>>` cannot be built in a `static` initializer).
static MEDIA_COVER_CACHE: std::sync::LazyLock<
    std::sync::Mutex<std::collections::HashMap<String, CoverEntry>>,
> = std::sync::LazyLock::new(|| std::sync::Mutex::new(std::collections::HashMap::new()));

/// Drop expired misses/pending markers, then the oldest entries, so the map
/// keeps a hard size bound no matter how many tracks are played.
fn prune_media_cover_cache(
    cache: &mut std::collections::HashMap<String, CoverEntry>,
    _now: Instant,
) {
    cache.retain(|_, entry| match entry.state {
        CoverState::Missing(at) => at.elapsed() < MEDIA_COVER_MISS_TTL,
        CoverState::Pending => entry.inserted.elapsed() < MEDIA_COVER_PENDING_TTL,
        CoverState::Ready(..) => true,
    });
    while cache.len() > MEDIA_COVER_CACHE_MAX {
        let Some(oldest) = cache
            .iter()
            .min_by_key(|(_, entry)| entry.inserted)
            .map(|(key, _)| key.clone())
        else {
            break;
        };
        cache.remove(&oldest);
    }
}

/// Probe the cache; on [`CoverLookup::Search`] the key is marked `Pending` so
/// concurrent/repeated polls do not queue duplicate lookups, and at most
/// [`MEDIA_COVER_MAX_INFLIGHT`] searches are allowed to be outstanding.
fn media_cover_lookup(key: &str) -> CoverLookup {
    // A poisoned lock only means some other thread panicked while holding this
    // (plain data) map; recovering keeps artwork refreshing for the rest of the
    // process lifetime instead of silently disabling lookups forever.
    let mut cache = match MEDIA_COVER_CACHE.lock() {
        Ok(guard) => guard,
        Err(poisoned) => poisoned.into_inner(),
    };
    let found = match cache.get(key) {
        Some(entry) => match &entry.state {
            CoverState::Ready(url, dur) => Some(CoverLookup::Hit(url.clone(), *dur)),
            CoverState::Missing(at) if at.elapsed() < MEDIA_COVER_MISS_TTL => {
                Some(CoverLookup::Miss)
            }
            CoverState::Pending if entry.inserted.elapsed() < MEDIA_COVER_PENDING_TTL => {
                Some(CoverLookup::Miss)
            }
            _ => None,
        },
        None => None,
    };
    if let Some(lookup) = found {
        return lookup;
    }

    let now = Instant::now();
    // Expired misses and stale pending markers are dropped first: they must not
    // count as in-flight work, and this is also how a lookup whose thread died
    // frees its slot again.
    prune_media_cover_cache(&mut cache, now);
    let in_flight = cache
        .values()
        .filter(|entry| matches!(&entry.state, CoverState::Pending))
        .count();
    if in_flight >= MEDIA_COVER_MAX_INFLIGHT {
        // Another title is already being resolved; retry on the next poll.
        return CoverLookup::Miss;
    }
    cache.insert(
        key.to_string(),
        CoverEntry {
            state: CoverState::Pending,
            inserted: now,
        },
    );
    prune_media_cover_cache(&mut cache, now);
    CoverLookup::Search
}

/// Store the outcome of a finished lookup (`None` = no artwork).
fn record_media_cover(key: &str, result: Option<(String, u32)>) {
    let mut cache = match MEDIA_COVER_CACHE.lock() {
        Ok(guard) => guard,
        Err(poisoned) => poisoned.into_inner(),
    };
    let now = Instant::now();
    let state = match result {
        Some((url, dur)) => CoverState::Ready(url, dur),
        None => CoverState::Missing(now),
    };
    cache.insert(key.to_string(), CoverEntry { state, inserted: now });
    prune_media_cover_cache(&mut cache, now);
}

fn url_encode(input: &str) -> String {
    let mut encoded = String::with_capacity(input.len() * 3);
    for b in input.bytes() {
        match b {
            b'a'..=b'z' | b'A'..=b'Z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                encoded.push(b as char);
            }
            b' ' => encoded.push('+'),
            _ => {
                encoded.push_str(&format!("%{:02X}", b));
            }
        }
    }
    encoded
}

/// Thumbnail of the YouTube video `query` belongs to.
///
/// A search page always answers with *some* video, so the parsed hit is only
/// accepted when its title actually covers the query; a stray title can
/// therefore never pin an unrelated clip as artwork.
#[cfg(windows)]
fn fetch_youtube_thumbnail(query: &str) -> Option<(String, u32)> {
    let url = format!(
        "https://www.youtube.com/results?search_query={}",
        url_encode(query)
    );
    let html = curl_body(&url, 5)?;
    let (video_id, result_title, dur) = parse_first_video_result(&html)?;
    if !result_title.is_empty() && token_overlap_percent(query, &result_title) < YOUTUBE_TITLE_MATCH_PERCENT {
        return None;
    }
    Some((format!("https://i.ytimg.com/vi/{video_id}/hqdefault.jpg"), dur))
}

#[cfg(not(windows))]
fn fetch_youtube_thumbnail(_query: &str) -> Option<(String, u32)> {
    None
}

#[cfg(windows)]
fn fetch_itunes_artwork(term: &str) -> Option<(String, u32)> {
    let url = format!(
        "https://itunes.apple.com/search?term={}&media=music&limit=1",
        url_encode(term)
    );
    let body = curl_body(&url, 4)?;
    let json: serde_json::Value = serde_json::from_str(&body).ok()?;
    let artwork = json.get("results")?.get(0)?.get("artworkUrl100")?.as_str()?;
    let dur_ms = json.get("results")?.get(0)?.get("trackTimeMillis")?.as_u64().unwrap_or(0);
    Some((artwork.replace("100x100bb.jpg", "600x600bb.jpg"), (dur_ms / 1000) as u32))
}

#[cfg(not(windows))]
fn fetch_itunes_artwork(_term: &str) -> Option<(String, u32)> {
    None
}
/// Runs `curl` for `url` and returns its response body, without flashing a
/// console window and with the same polite user agent a browser sends.
#[cfg(windows)]
fn curl_body(url: &str, timeout_secs: u32) -> Option<String> {
    use std::os::windows::process::CommandExt;
    let timeout = timeout_secs.to_string();
    let output = std::process::Command::new("curl")
        .args([
            "-s",
            "-L",
            "-m",
            &timeout,
            "-H",
            "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
            url,
        ])
        .creation_flags(0x0800_0000) // CREATE_NO_WINDOW
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    Some(String::from_utf8_lossy(&output.stdout).into_owned())
}

/// Minimum share of the query words a YouTube hit must cover to be accepted.
const YOUTUBE_TITLE_MATCH_PERCENT: u32 = 50;

/// Lowercase words (letters and digits only) of a title.
fn title_tokens(value: &str) -> Vec<String> {
    value
        .to_lowercase()
        .split(|ch: char| !ch.is_alphanumeric())
        .filter(|token| token.chars().count() >= 2)
        .map(str::to_string)
        .collect()
}

/// Share of the query words that appear in `result`, in percent.
fn token_overlap_percent(query: &str, result: &str) -> u32 {
    let query_tokens = title_tokens(query);
    if query_tokens.is_empty() {
        return 0;
    }
    let result_tokens = title_tokens(result);
    if result_tokens.is_empty() {
        return 0;
    }
    let matched = query_tokens
        .iter()
        .filter(|token| result_tokens.iter().any(|candidate| candidate == *token))
        .count();
    (matched * 100 / query_tokens.len()) as u32
}

/// First `"videoId"` of a YouTube search page together with the title and duration.
fn parse_first_video_result(html: &str) -> Option<(String, String, u32)> {
    const VIDEO_ID_MARKER: &str = "\"videoId\":\"";
    let mut from = 0;
    while let Some(offset) = html[from..].find(VIDEO_ID_MARKER) {
        let value_start = from + offset + VIDEO_ID_MARKER.len();
        let value = &html[value_start..];
        let value_end = value.find('"')?;
        let id = &value[..value_end];
        if id.len() == 11 && id.chars().all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-') {
            let title = result_title_after(value);
            let dur = result_duration_after(value);
            return Some((id.to_string(), title, dur));
        }
        from = value_start;
    }
    None
}

fn result_duration_after(after_video_id: &str) -> u32 {
    let window = &after_video_id[..after_video_id.len().min(4_000)];
    let marker = "\"lengthText\":{";
    if let Some(offset) = window.find(marker) {
        let rest = &window[offset + marker.len()..];
        if let Some(st_idx) = rest.find("\"simpleText\":\"") {
            let time_str = read_json_string(&rest[st_idx + 14..]);
            let parts: Vec<&str> = time_str.split(':').collect();
            if parts.len() == 2 {
                let m: u32 = parts[0].parse().unwrap_or(0);
                let s: u32 = parts[1].parse().unwrap_or(0);
                return m * 60 + s;
            } else if parts.len() == 3 {
                let h: u32 = parts[0].parse().unwrap_or(0);
                let m: u32 = parts[1].parse().unwrap_or(0);
                let s: u32 = parts[2].parse().unwrap_or(0);
                return h * 3600 + m * 60 + s;
            }
        }
    }
    0
}

/// Title of the search result that follows `"videoId"` in a search page.
fn result_title_after(after_video_id: &str) -> String {
    // `ytInitialData` serializes a result as
    //   "videoRenderer":{"videoId":"…","thumbnail":…,"title":{"runs":[{"text":"…"}]}
    // so the first title object after the id belongs to that result.
    let window = &after_video_id[..after_video_id.len().min(4_000)];
    for marker in ["\"title\":{\"runs\":[{\"text\":\"", "\"title\":{\"simpleText\":\""] {
        if let Some(offset) = window.find(marker) {
            let value = &window[offset + marker.len()..];
            return unescape_json_string(read_json_string(value));
        }
    }
    String::new()
}

/// Reads the body of the JSON string starting at `value` (past the opening
/// quote), stopping at the first quote that is not escaped.
fn read_json_string(value: &str) -> &str {
    let mut escaped = false;
    for (index, ch) in value.char_indices() {
        if escaped {
            escaped = false;
        } else if ch == '\\' {
            escaped = true;
        } else if ch == '"' {
            return &value[..index];
        }
    }
    value
}

/// Decodes the escape sequences of a JSON string body.
fn unescape_json_string(raw: &str) -> String {
    let mut out = String::with_capacity(raw.len());
    let mut chars = raw.chars();
    while let Some(ch) = chars.next() {
        if ch != '\\' {
            out.push(ch);
            continue;
        }
        match chars.next() {
            Some('"') => out.push('"'),
            Some('\\') => out.push('\\'),
            Some('/') => out.push('/'),
            Some('b') => out.push('\u{8}'),
            Some('f') => out.push('\u{c}'),
            Some('n') => out.push('\n'),
            Some('r') => out.push('\r'),
            Some('t') => out.push('\t'),
            Some('u') => push_unicode_escape(&mut out, &mut chars),
            Some(other) => out.push(other),
            None => break,
        }
    }
    out
}

/// Decodes one `\uXXXX` escape, combining a surrogate pair when present.
fn push_unicode_escape(out: &mut String, chars: &mut std::str::Chars) {
    let Some(code) = next_hex4(chars) else {
        return;
    };
    if (0xDC00..0xE000).contains(&code) {
        // Lone low surrogate: not valid UTF-8 on its own.
        out.push('\u{FFFD}');
        return;
    }
    if (0xD800..0xDC00).contains(&code) {
        let mut lookahead = chars.clone();
        if lookahead.next() == Some('\\') && lookahead.next() == Some('u') {
            if let Some(low) = next_hex4(&mut lookahead) {
                if (0xDC00..0xE000).contains(&low) {
                    *chars = lookahead;
                    let combined = 0x1_0000 + ((code - 0xD800) << 10) + (low - 0xDC00);
                    if let Some(decoded) = char::from_u32(combined) {
                        out.push(decoded);
                    }
                    return;
                }
            }
        }
        out.push('\u{FFFD}');
        return;
    }
    if let Some(decoded) = char::from_u32(code) {
        out.push(decoded);
    }
}

/// Consumes exactly four hexadecimal digits.
fn next_hex4(chars: &mut std::str::Chars) -> Option<u32> {
    let mut code = 0u32;
    for _ in 0..4 {
        code = code * 16 + chars.next()?.to_digit(16)?;
    }
    Some(code)
}

/// Browser names appended to window titles, lowercase.
const BROWSER_TITLE_SUFFIXES: [&str; 8] = [
    " - google chrome canary",
    " - google chrome",
    " - microsoft edge",
    " - mozilla firefox",
    " - chromium",
    " - firefox",
    " - brave",
    " - opera",
];

/// YouTube site markers appended to tab titles, lowercase, longest first.
const YOUTUBE_TITLE_SUFFIXES: [&str; 4] = [
    " - youtube music",
    " - youtube kids",
    " - youtube tv",
    " - youtube",
];

/// `"Song - YouTube - Google Chrome"` → `"Song"`, keeping the original casing.
fn strip_tab_suffixes(title: &str) -> String {
    let without_browser = strip_title_suffixes(title, &BROWSER_TITLE_SUFFIXES);
    let mut clean = strip_title_suffixes(&without_browser, &YOUTUBE_TITLE_SUFFIXES);

    // Strip leading notification badges like "(1) " or "(99+) "
    if clean.starts_with('(') {
        if let Some(close_idx) = clean.find(')') {
            let inside = &clean[1..close_idx];
            if inside.chars().all(|c| c.is_ascii_digit() || c == '+') {
                clean = clean[close_idx + 1..].trim_start().to_string();
            }
        }
    }

    // Strip leading YouTube prefixes like "YouTube - " or "YouTube: "
    let lower = clean.to_lowercase();
    for prefix in &["youtube - ", "youtube: "] {
        if lower.starts_with(prefix) {
            clean = clean[prefix.len()..].trim_start().to_string();
            break;
        }
    }

    clean
}

/// Strips every suffix in `suffixes` (case-insensitively) from the end of
/// `title`, as often as it takes.
fn strip_title_suffixes(title: &str, suffixes: &[&str]) -> String {
    let mut value = title.trim().to_string();
    'outer: loop {
        let lower = value.to_lowercase();
        for suffix in suffixes {
            if lower.ends_with(suffix) {
                if let Some(cut) = drop_last_chars(&value, suffix.chars().count()) {
                    value = cut;
                    continue 'outer;
                }
            }
        }
        return value;
    }
}

/// Removes the last `chars` characters (never splitting a character) and trims
/// what is left.
fn drop_last_chars(value: &str, chars: usize) -> Option<String> {
    let mut end = value.len();
    for _ in 0..chars {
        end = value[..end].char_indices().next_back().map(|(index, _)| index)?;
    }
    Some(value[..end].trim_end().to_string())
}

/// YouTube pages that are not videos. Searching YouTube for their titles would
/// pick the first clip of an unrelated query, so no cover is fetched for them.
fn is_generic_youtube_page(title: &str) -> bool {
    let page = strip_tab_suffixes(title).to_lowercase();
    matches!(
        page.as_str(),
        ""
            | "youtube"
            | "youtube studio"
            | "studio"
            | "youtube music"
            | "youtube kids"
            | "youtube tv"
            | "youtube gaming"
            | "home"
            | "ana sayfa"
            | "trending"
            | "subscriptions"
            | "abonelikler"
            | "library"
            | "kitaplık"
            | "history"
            | "geçmiş"
            | "watch later"
            | "daha sonra izle"
            | "playlist"
            | "playlists"
            | "çalma listeleri"
            | "shorts"
            | "search"
            | "arama"
            | "results"
            | "sonuçlar"
    )
}

/// Whether a media item really is a YouTube video: the site reports itself as
/// the artist, or a browser tab still carries the site marker. Generic YouTube
/// pages are never videos.
fn is_youtube_video(artist: &str, title: &str) -> bool {
    let title = title.trim();
    if title.is_empty() || is_generic_youtube_page(title) {
        return false;
    }
    let artist = artist.trim();
    artist.eq_ignore_ascii_case("YouTube")
        || artist.eq_ignore_ascii_case("YouTube Music")
        || title.to_lowercase().ends_with(" - youtube")
}

/// Application and site names that never perform a track. Searching the iTunes
/// catalogue for them only produces unrelated hits.
const NON_ARTIST_SOURCES: [&str; 13] = [
    "youtube",
    "youtube music",
    "google chrome",
    "chrome",
    "brave",
    "microsoft edge",
    "mozilla firefox",
    "firefox",
    "opera",
    "vlc",
    "vlc media player",
    "vlc player",
    "spotify",
];

/// Whether `artist` may be sent to the iTunes catalogue as a performer.
fn is_searchable_artist(artist: &str) -> bool {
    let artist = artist.trim();
    artist.chars().count() >= 2
        && !NON_ARTIST_SOURCES
            .iter()
            .any(|source| artist.eq_ignore_ascii_case(source))
}

/// How (or whether) a cover can be resolved for one media item.
enum CoverLookupPlan {
    /// Search YouTube for an actual video and use its thumbnail.
    YouTubeVideo(String),
    /// Look the track up in the iTunes catalogue by `"artist title"`.
    ITunes(String),
}

/// Plans the cover lookup for one media item; `None` when there is nothing to
/// search for.
///
/// The YouTube path ends in the thumbnail of the *first search hit*, so it is
/// only attempted for items that really are YouTube videos: any other window
/// title (a browser tab, VLC, the paused Spotify placeholder, a generic
/// YouTube page) would pin an unrelated clip as the cover. Those items resolve
/// to no cover, which the clients render as a plain music-note tile.
fn cover_lookup_plan(artist: &str, title: &str) -> Option<CoverLookupPlan> {
    let title = title.trim();
    if title.is_empty() {
        return None;
    }
    if is_youtube_video(artist, title) {
        let video_title = strip_tab_suffixes(title);
        if video_title.is_empty() {
            return None;
        }
        return Some(CoverLookupPlan::YouTubeVideo(video_title));
    }
    if is_searchable_artist(artist) {
        let term = format!("{} {}", artist.trim(), title).trim().to_string();
        return Some(CoverLookupPlan::ITunes(term));
    }
    None
}

/// Runs one planned lookup (on a blocking worker, never on the async runtime).
fn resolve_cover(plan: CoverLookupPlan) -> Option<(String, u32)> {
    match plan {
        CoverLookupPlan::YouTubeVideo(title) => fetch_youtube_thumbnail(&title),
        CoverLookupPlan::ITunes(term) => fetch_itunes_artwork(&term),
    }
}

pub fn get_cached_media_cover(artist: &str, title: &str) -> (String, u32) {
    let key = format!("{artist} - {title}").trim().to_lowercase();
    if key.is_empty() || key == "-" {
        return (String::new(), 0);
    }

    match media_cover_lookup(&key) {
        CoverLookup::Hit(url, dur) => return (url, dur),
        CoverLookup::Miss => return (String::new(), 0),
        CoverLookup::Search => {}
    }

    // Decide what to search for — before spending a request. Window titles of
    // browser tabs, VLC playlists and paused players have no artwork to find,
    // and a blind YouTube search for them returns unrelated clips.
    let Some(plan) = cover_lookup_plan(artist, title) else {
        record_media_cover(&key, None);
        return (String::new(), 0);
    };

    // A plain thread, not `tokio::spawn`: this runs on the stats ticker *and*
    // on the GSMTC worker thread, which has no runtime handle. Only one lookup
    // per song is started — the cache marks the key in flight.
    let query_key = key.clone();
    let _ = std::thread::Builder::new()
        .name("media-cover".to_string())
        .spawn(move || {
            // `None` covers both "no artwork exists" and a failed search; both
            // are remembered as a miss so the next poll does not spawn `curl`
            // again (see `MEDIA_COVER_MISS_TTL`).
            record_media_cover(&query_key, resolve_cover(plan));
        });

    (String::new(), 0)
}

#[cfg(windows)]
pub fn detect_media_info() -> MediaInfo {
    let mut media = if let Some(media) = query_windows_media_control() {
        media
    } else {
        detect_media_info_internal()
    };
    if media.duration_sec == 0 && !media.title.is_empty() {
        let (_, fb_dur) = get_cached_media_cover(&media.artist, &media.title);
        if fb_dur > 0 {
            media.duration_sec = fb_dur;
        }
    }
    media.position_sec = MediaTimelineTracker::update(&media.title, media.is_playing);
    sync_thumbnail_with_title(&media.title);
    media
}

#[cfg(windows)]
fn detect_media_info_internal() -> MediaInfo {
    use std::ffi::c_void;
    type HWND = *mut c_void;
    type BOOL = i32;
    type LPARAM = isize;
    #[link(name = "user32")]
    extern "system" {
        fn EnumWindows(lpEnumFunc: unsafe extern "system" fn(HWND, LPARAM) -> BOOL, lParam: LPARAM) -> BOOL;
        fn IsWindowVisible(hWnd: HWND) -> BOOL;
        fn GetWindowTextLengthW(hWnd: HWND) -> i32;
        fn GetWindowTextW(hWnd: HWND, lpString: *mut u16, nMaxCount: i32) -> i32;
    }

    struct WindowCollector {
        titles: Vec<String>,
    }

    unsafe extern "system" fn enum_proc(hwnd: HWND, lparam: LPARAM) -> BOOL {
        if IsWindowVisible(hwnd) != 0 {
            let len = GetWindowTextLengthW(hwnd);
            if len > 0 && len < 512 {
                let mut buf = vec![0u16; (len + 1) as usize];
                let read = GetWindowTextW(hwnd, buf.as_mut_ptr(), len + 1);
                if read > 0 {
                    let title = String::from_utf16_lossy(&buf[..read as usize]);
                    let collector = &mut *(lparam as *mut WindowCollector);
                    collector.titles.push(title);
                }
            }
        }
        1
    }

    let mut collector = WindowCollector { titles: Vec::new() };
    unsafe {
        EnumWindows(enum_proc, &mut collector as *mut _ as LPARAM);
    }

    for title in &collector.titles {
        let t = title.trim();
        let lower = t.to_lowercase();

        // 1. Spotify
        if lower == "spotify" || lower == "spotify free" || lower == "spotify premium" {
            return MediaInfo {
                title: "Spotify".to_string(),
                artist: "Duraklatıldı".to_string(),
                is_playing: false,
                cover_url: String::new(),
                ..Default::default()
            };
        }
        if lower.contains("spotify") && t.contains(" - ") {
            let mut parts = t.splitn(2, " - ");
            let artist = parts.next().unwrap_or("Spotify").trim().to_string();
            let track = parts.next().unwrap_or("").trim().to_string();
            let (cover, dur) = get_cached_media_cover(&artist, &track);
            return MediaInfo {
                title: track,
                artist,
                is_playing: true,
                cover_url: cover,
                duration_sec: dur,
                ..Default::default()
            };
        }

        // 2. YouTube in a browser. Only a watch page carries the site marker
        //    ("… - YouTube"); the home page, Studio and YouTube Music never get
        //    it, and searching YouTube for those titles would pin an unrelated
        //    clip as the cover.
        if lower.contains("youtube") {
            let clean = strip_tab_suffixes(t);
            if !clean.is_empty() && !is_generic_youtube_page(&clean) {
                let (artist, track) = match clean.split_once(" - ") {
                    Some((artist, track)) => (artist.trim().to_string(), track.trim().to_string()),
                    None => ("YouTube".to_string(), clean.clone()),
                };
                let (cover, dur) = get_cached_media_cover("YouTube", &clean);
                return MediaInfo {
                    title: track,
                    artist,
                    is_playing: true,
                    cover_url: cover,
                    duration_sec: dur,
                    ..Default::default()
                };
            }
        }

        // 3. VLC. A media-player window title carries the file name, not an
        //    artist, so nothing is searched for it (`cover_lookup_plan` refuses
        //    media-player names as performers).
        if lower.contains("vlc media player") {
            let cleaned = t.replace(" - VLC media player", "").trim().to_string();
            if !cleaned.is_empty() {
                return MediaInfo {
                    title: cleaned,
                    artist: "VLC Player".to_string(),
                    is_playing: true,
                    ..Default::default()
                };
            }
        }
    }

    // 4. Apple Music / Deezer / Tidal
    for title in &collector.titles {
        let t = title.trim();
        let lower = t.to_lowercase();
        if (lower.contains("apple music") || lower.contains("deezer") || lower.contains("tidal")) && t.contains(" - ") {
            let mut parts = t.splitn(2, " - ");
            let artist = parts.next().unwrap_or("").trim().to_string();
            let track = parts.next().unwrap_or("").trim().to_string();
            let (cover, dur) = get_cached_media_cover(&artist, &track);
            return MediaInfo {
                title: track,
                artist,
                is_playing: true,
                cover_url: cover,
                duration_sec: dur,
                ..Default::default()
            };
        }
    }

    MediaInfo::default()
}

#[cfg(not(windows))]
pub fn detect_media_info() -> MediaInfo {
    MediaInfo::default()
}
