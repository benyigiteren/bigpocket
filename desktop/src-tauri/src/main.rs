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

    let state = server::AppState::new();

    // Start UDP Auto-discovery on Port 47800
    discovery::start_discovery(8085);

    // Bind and start the server synchronously before creating the Tauri window!
    let _ = rt.block_on(async {
        let res = server::bind_and_start_server(state.clone(), 8085).await;
        tokio::time::sleep(std::time::Duration::from_millis(60)).await;
        res
    });

    // The Windows autostart entry launches us with `--minimized`: the server
    // keeps running but the panel stays out of the way at boot.
    let start_minimized = std::env::args().any(|arg| arg == "--minimized");

    tauri::Builder::default()
        .setup(move |app| {
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
