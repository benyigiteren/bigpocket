//! BigPocket Local Flow — WhatsApp-style local sharing between PC and phone.
//!
//! Ported from `desktop/flow.go`. Messages are kept in memory (protected by an
//! async `RwLock`) and persisted to `SharedFiles/flow_history.json` so the
//! history survives restarts. The most recent [`MAX_MESSAGES`] entries are kept.

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};
use tokio::sync::RwLock;

/// Maximum number of messages retained in memory and on disk.
const MAX_MESSAGES: usize = 100;

/// Default welcome message shown the first time Flow is opened (matches the Go
/// implementation byte-for-byte).
const WELCOME_TEXT: &str = "👋 BigPocket Akış'a hoş geldiniz! Bilgisayar ve telefonunuz arasında mesaj, bağlantı ve dosyaları tek tıkla paylaşabilirsiniz.";

/// One Flow message. `msg_type` is serialized as `type` to remain wire
/// compatible with the existing Go server and the Android/Web clients.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FlowMessage {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub sender: String,
    #[serde(rename = "type", default)]
    pub msg_type: String,
    #[serde(default)]
    pub text: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub file_name: String,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub file_size: i64,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub file_url: String,
    #[serde(default)]
    pub timestamp: i64,
}

impl Default for FlowMessage {
    fn default() -> Self {
        Self {
            id: String::new(),
            sender: String::new(),
            msg_type: String::new(),
            text: String::new(),
            file_name: String::new(),
            file_size: 0,
            file_url: String::new(),
            timestamp: 0,
        }
    }
}

impl FlowMessage {
    /// Build a message from a partial payload, applying the same defaults the Go
    /// server used (`sender` defaults to `pc`, `type` defaults to `text`).
    pub fn new(sender: impl Into<String>, msg_type: impl Into<String>, text: impl Into<String>) -> Self {
        let sender = sender.into();
        let msg_type = msg_type.into();
        Self {
            sender: if sender.is_empty() { "pc".to_string() } else { sender },
            msg_type: if msg_type.is_empty() { "text".to_string() } else { msg_type },
            text: text.into(),
            ..Default::default()
        }
    }
}

fn is_zero(v: &i64) -> bool {
    *v == 0
}

/// Final path component of a stored file name (Windows and POSIX separators
/// both handled), or `None` when there is no usable basename. Nothing containing
/// a separator or a `.`/`..` segment survives, so a crafted `file_name` can never
/// escape the shared folder.
fn file_basename(name: &str) -> Option<&str> {
    let last = name.rsplit(|c| c == '/' || c == '\\').next().unwrap_or("");
    if last.is_empty() || last == "." || last == ".." {
        None
    } else {
        Some(last)
    }
}

/// Best-effort deletion of the file backing a message inside `dir`.
fn remove_message_file(dir: &Path, msg: &FlowMessage) {
    if let Some(base) = file_basename(&msg.file_name) {
        let _ = std::fs::remove_file(dir.join(base));
    }
}

/// Whether a message references an uploaded file by basename.
fn message_references_file(msg: &FlowMessage, base: &str) -> bool {
    file_basename(&msg.file_name) == Some(base)
}

/// Current wall-clock time in Unix milliseconds.
pub fn now_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

fn now_nanos() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0)
}

/// Thread-safe manager for the Flow history.
#[derive(Clone)]
pub struct FlowManager {
    messages: Arc<RwLock<Vec<FlowMessage>>>,
    path: PathBuf,
}

impl FlowManager {
    /// Create a manager backed by `SharedFiles/flow_history.json`.
    pub fn new() -> Self {
        Self::with_path(crate::server::get_shared_dir().join("flow_history.json"))
    }

    /// Create a manager backed by an explicit history file (used by tests and by
    /// callers that resolve the shared folder themselves).
    pub fn with_path(path: impl Into<PathBuf>) -> Self {
        let path = path.into();
        let messages = load_history(&path);
        Self {
            messages: Arc::new(RwLock::new(messages)),
            path,
        }
    }

    /// Absolute path of the backing JSON file.
    pub fn history_path(&self) -> &Path {
        &self.path
    }

    /// Folder holding the uploaded files referenced by the history — the parent
    /// of the history file (normally `SharedFiles/`).
    fn files_dir(&self) -> PathBuf {
        self.path
            .parent()
            .map(Path::to_path_buf)
            .unwrap_or_else(|| PathBuf::from("."))
    }

    /// Append a message, persisting the history and returning the stored copy
    /// (with `id` and `timestamp` filled in when missing).
    ///
    /// The caller is responsible for broadcasting the returned message to
    /// connected WebSocket clients.
    pub async fn add_message(&self, mut msg: FlowMessage) -> FlowMessage {
        let mut guard = self.messages.write().await;

        if msg.id.is_empty() {
            msg.id = format!("msg_{}_{}", now_nanos(), guard.len());
        }
        if msg.timestamp == 0 {
            msg.timestamp = now_millis();
        }
        // Detect plain links automatically, exactly like the Go server.
        if msg.msg_type == "text"
            && (msg.text.starts_with("http://") || msg.text.starts_with("https://"))
        {
            msg.msg_type = "link".to_string();
        }

        guard.push(msg.clone());
        save_history_locked(&self.path, &mut guard);
        msg
    }

    /// Snapshot of every stored message, oldest first.
    pub async fn get_messages(&self) -> Vec<FlowMessage> {
        self.messages.read().await.clone()
    }

    /// Delete an individual message by ID and persist the updated list.
    pub async fn delete_message(&self, id: &str) -> Option<FlowMessage> {
        let mut guard = self.messages.write().await;
        if let Some(pos) = guard.iter().position(|m| m.id == id) {
            let removed = guard.remove(pos);
            save_history_locked(&self.path, &mut guard);
            Some(removed)
        } else {
            None
        }
    }

    /// Remove every message that references `file_name` (matched by basename)
    /// and persist the updated history. Returns the removed messages so the
    /// caller can broadcast their deletion.
    pub async fn delete_by_file_name(&self, file_name: &str) -> Vec<FlowMessage> {
        let Some(base) = file_basename(file_name) else {
            return Vec::new();
        };
        let base = base.to_string();
        let mut guard = self.messages.write().await;
        let mut removed = Vec::new();
        guard.retain(|msg| {
            let matches = message_references_file(msg, &base);
            if matches {
                removed.push(msg.clone());
            }
            !matches
        });
        if !removed.is_empty() {
            save_history_locked(&self.path, &mut guard);
        }
        removed
    }

    /// Drop the whole history and persist the empty list, deleting every file
    /// the cleared messages referenced so uploads are not orphaned.
    pub async fn clear_messages(&self) {
        let mut guard = self.messages.write().await;
        let dir = self.files_dir();
        for msg in guard.iter() {
            remove_message_file(&dir, msg);
        }
        guard.clear();
        save_history_locked(&self.path, &mut guard);
    }
}

impl Default for FlowManager {
    fn default() -> Self {
        Self::new()
    }
}

/// Load the history from disk, falling back to a single welcome message when the
/// file is missing or empty.
fn load_history(path: &Path) -> Vec<FlowMessage> {
    match std::fs::read(path) {
        Ok(data) => match serde_json::from_slice::<Vec<FlowMessage>>(&data) {
            Ok(loaded) if !loaded.is_empty() => loaded,
            _ => Vec::new(),
        },
        Err(_) => vec![FlowMessage {
            id: format!("msg_{}", now_millis()),
            sender: "pc".to_string(),
            msg_type: "text".to_string(),
            text: WELCOME_TEXT.to_string(),
            timestamp: now_millis(),
            ..Default::default()
        }],
    }
}

/// Persist the history, trimming it to [`MAX_MESSAGES`] entries in place.
///
/// Trimmed entries also have their uploaded file removed so a long-running Flow
/// session never leaves orphaned blobs behind. The JSON is written to a sibling
/// `.tmp` file and atomically renamed into place, so a crash or power loss can
/// never leave `flow_history.json` half-written.
fn save_history_locked(path: &Path, messages: &mut Vec<FlowMessage>) {
    if messages.len() > MAX_MESSAGES {
        let drain_to = messages.len() - MAX_MESSAGES;
        let dir = path.parent().map(Path::to_path_buf);
        for removed in messages.drain(0..drain_to) {
            if let Some(dir) = &dir {
                remove_message_file(dir, &removed);
            }
        }
    }
    if let Some(parent) = path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    let Ok(data) = serde_json::to_vec_pretty(messages) else {
        return;
    };
    let mut tmp = path.as_os_str().to_os_string();
    tmp.push(".tmp");
    let tmp = PathBuf::from(tmp);
    if std::fs::write(&tmp, &data).is_ok() && std::fs::rename(&tmp, path).is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
}
