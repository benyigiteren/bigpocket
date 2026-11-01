//! LAN auto-discovery.
//!
//! The desktop periodically broadcasts a JSON "hello" packet over UDP so the
//! Android app can find the PC automatically (KDE-Connect style) without the
//! user typing an IP address. The Android client listens on [`DISCOVERY_PORT`]
//! and actively probes with "bigpocket_discover".

use std::sync::Arc;
use std::time::Duration;
use tokio::net::UdpSocket;

/// UDP port the Android app listens on for discovery broadcasts.
pub const DISCOVERY_PORT: u16 = 47800;

fn get_broadcast_targets() -> Vec<String> {
    let mut targets = vec![format!("255.255.255.255:{DISCOVERY_PORT}")];
    if let Ok(interfaces) = local_ip_address::list_afinet_netifas() {
        for (_name, ip) in interfaces {
            if let std::net::IpAddr::V4(v4) = ip {
                if !v4.is_loopback() && !v4.is_link_local() {
                    let octets = v4.octets();
                    let bcast = format!("{}.{}.{}.255:{DISCOVERY_PORT}", octets[0], octets[1], octets[2]);
                    if !targets.contains(&bcast) {
                        targets.push(bcast);
                    }
                }
            }
        }
    }
    targets
}

/// How often the "hello" packet is repeated.
const BROADCAST_INTERVAL: Duration = Duration::from_secs(2);
/// Starts the background broadcaster and returns its task handle.
pub fn start_discovery(port: u16) -> tokio::task::JoinHandle<()> {
    tokio::spawn(run_discovery(port))
}

/// Builds the JSON discovery payload expected by `PcDiscovery.kt` in the Android app.
pub fn format_payload(port: u16) -> String {
    let host = sysinfo::System::host_name().unwrap_or_else(|| "BigPocket PC".to_string());
    let cfg = crate::config::load_config();
    let needs_password = !cfg.password.is_empty();
    serde_json::json!({
        "app": "bigpocket",
        "name": host,
        "port": port,
        "version": "1.5.3",
        "needsPassword": needs_password,
    })
    .to_string()
}

async fn run_discovery(port: u16) {
    // Try binding to the well-known discovery port so we can answer active probes.
    let socket = match UdpSocket::bind(format!("0.0.0.0:{DISCOVERY_PORT}")).await {
        Ok(s) => s,
        Err(_) => match UdpSocket::bind("0.0.0.0:0").await {
            Ok(s) => s,
            Err(err) => {
                eprintln!("discovery disabled: cannot bind UDP socket: {err}");
                return;
            }
        },
    };

    if let Err(err) = socket.set_broadcast(true) {
        eprintln!("discovery: cannot enable broadcast: {err}");
    }

    let socket = Arc::new(socket);
    let socket_rx = socket.clone();

    // Answer active probes from Android ("bigpocket_discover") immediately
    tokio::spawn(async move {
        let mut buf = [0u8; 512];
        loop {
            match socket_rx.recv_from(&mut buf).await {
                Ok((n, peer)) => {
                    let text = std::str::from_utf8(&buf[..n]).unwrap_or("");
                    if text == "bigpocket_discover" {
                        let payload = format_payload(port);
                        let _ = socket_rx.send_to(payload.as_bytes(), peer).await;
                    }
                }
                Err(_) => break,
            }
        }
    });

    eprintln!("Discovery broadcasting on UDP {DISCOVERY_PORT}...");
    loop {
        let payload = format_payload(port);
        let targets = get_broadcast_targets();
        for target in targets {
            let _ = socket.send_to(payload.as_bytes(), &target).await;
        }
        tokio::time::sleep(BROADCAST_INTERVAL).await;
    }
}
