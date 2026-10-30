// Prevents additional console window on Windows in release, DO NOT REMOVE!!
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]
#![allow(dead_code)]

mod config;
mod discovery;
mod flow;
mod server;
mod system;

use tauri::Manager;

fn main() {
    let rt = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .expect("failed to create Tokio runtime");
    let _guard = rt.enter();

    // The Windows autostart entry launches us with `--minimized`: the server
    // keeps running but the panel stays out of the way at boot.
    let start_minimized = std::env::args().any(|arg| arg == "--minimized");

    tauri::Builder::default()
        .setup(move |app| {
            let state = server::AppState::new();

            // Start UDP Auto-discovery on Port 47800
            discovery::start_discovery(8085);

            // Start Axum HTTP & WebSocket Server on Port 8085
            server::start_server(state, 8085);

            if start_minimized {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.hide();
                }
            }

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
