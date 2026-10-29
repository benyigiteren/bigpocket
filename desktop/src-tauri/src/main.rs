// Prevents additional console window on Windows in release, DO NOT REMOVE!!
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]
#![allow(dead_code)]

mod config;
mod discovery;
mod flow;
mod server;
mod system;

fn main() {
    let rt = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .expect("failed to create Tokio runtime");
    let _guard = rt.enter();

    tauri::Builder::default()
        .setup(|_app| {
            let state = server::AppState::new();

            // Start UDP Auto-discovery on Port 47800
            discovery::start_discovery(8085);

            // Start Axum HTTP & WebSocket Server on Port 8085
            server::start_server(state, 8085);

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
