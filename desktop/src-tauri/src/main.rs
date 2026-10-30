// Prevents additional console window on Windows in release, DO NOT REMOVE!!
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]
#![allow(dead_code)]

mod config;
mod discovery;
mod flow;
mod server;
mod system;

use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::Manager;

#[cfg(windows)]
static mut SINGLE_INSTANCE_MUTEX: *mut std::ffi::c_void = std::ptr::null_mut();

#[cfg(windows)]
fn check_single_instance() -> bool {
    use std::ffi::OsStr;
    use std::os::windows::ffi::OsStrExt;

    #[link(name = "kernel32")]
    extern "system" {
        fn CreateMutexW(
            lpMutexAttributes: *mut std::ffi::c_void,
            bInitialOwner: i32,
            lpName: *const u16,
        ) -> *mut std::ffi::c_void;
        fn GetLastError() -> u32;
    }

    const ERROR_ALREADY_EXISTS: u32 = 183;
    let name: Vec<u16> = OsStr::new("Global\\BigPocketSingleInstanceMutex")
        .encode_wide()
        .chain(Some(0))
        .collect();

    unsafe {
        let handle = CreateMutexW(std::ptr::null_mut(), 1, name.as_ptr());
        if handle.is_null() || GetLastError() == ERROR_ALREADY_EXISTS {
            return false;
        }
        SINGLE_INSTANCE_MUTEX = handle;
    }
    true
}

#[cfg(not(windows))]
fn check_single_instance() -> bool {
    true
}

fn main() {
    // Single instance guard: prevent duplicate processes and duplicate tray icons
    if !check_single_instance() {
        // A BigPocket instance is already running; ping to wake its window, then exit.
        let _ = std::process::Command::new("curl")
            .args(["-s", "http://127.0.0.1:8085/system_info"])
            .output();
        std::process::exit(0);
    }

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

    let start_minimized = std::env::args().any(|arg| arg == "--minimized");

    tauri::Builder::default()
        .setup(move |app| {
            // Build Tray Menu
            let show_i = MenuItem::with_id(app, "show", "BigPocket'i Aç", true, None::<&str>)?;
            let quit_i = MenuItem::with_id(app, "quit", "Çıkış", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show_i, &quit_i])?;

            // Build Tray Icon using the app icon
            let tray_builder = TrayIconBuilder::new()
                .tooltip("BigPocket")
                .menu(&menu)
                .show_menu_on_left_click(false);

            let tray_builder = if let Some(icon) = app.default_window_icon() {
                tray_builder.icon(icon.clone())
            } else {
                tray_builder
            };

            let _tray = tray_builder
                .on_menu_event(|app, event| {
                    match event.id.as_ref() {
                        "quit" => {
                            server::set_virtual_monitor(false);
                            std::process::exit(0);
                        }
                        "show" => {
                            if let Some(window) = app.get_webview_window("main") {
                                let _ = window.show();
                                let _ = window.unminimize();
                                let _ = window.set_focus();
                            }
                        }
                        _ => {}
                    }
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let app = tray.app_handle();
                        if let Some(window) = app.get_webview_window("main") {
                            if window.is_visible().unwrap_or(false) {
                                let _ = window.hide();
                            } else {
                                let _ = window.show();
                                let _ = window.unminimize();
                                let _ = window.set_focus();
                            }
                        }
                    }
                })
                .build(app)?;

            // Prevent window close from terminating the app; instead minimize to tray!
            if let Some(window) = app.get_webview_window("main") {
                let w = window.clone();
                window.on_window_event(move |event| {
                    if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                        api.prevent_close();
                        let _ = w.hide();
                    }
                });

                if start_minimized {
                    let _ = window.hide();
                }
            }

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
