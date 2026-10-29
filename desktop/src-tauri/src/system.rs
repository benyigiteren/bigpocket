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
    /// skipped, matching the Go implementation.
    pub fn simulate_hotkey(&mut self, hotkey: &str) -> Result<(), String> {
        let keys: Vec<Key> = hotkey.split('+').filter_map(key_from_name).collect();
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
}
