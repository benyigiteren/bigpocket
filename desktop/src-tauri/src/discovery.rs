//! LAN auto-discovery.
//!
//! The desktop periodically broadcasts a tiny `BigPocket|<ip>|<port>` packet over
//! UDP so the Android app can find the PC automatically (KDE-Connect style)
//! without the user typing an IP address. The Android client listens on
//! [`DISCOVERY_PORT`].

use std::time::Duration;

use tokio::net::UdpSocket;

/// UDP port the Android app listens on for discovery broadcasts.
pub const DISCOVERY_PORT: u16 = 47800;

/// Limited broadcast target (every host on the local subnet).
const BROADCAST_TARGET: &str = "255.255.255.255:47800";

/// How often the "hello" packet is repeated.
const BROADCAST_INTERVAL: Duration = Duration::from_secs(2);

/// Local bind address for the sending socket: an ephemeral port on all interfaces.
const BIND_ADDR: &str = "0.0.0.0:0";

/// Starts the background broadcaster and returns its task handle.
///
/// `port` is the HTTP port the desktop server listens on (8085); it is embedded
/// in the packet as `BigPocket|<ip>|8085`. Dropping the handle does not stop the
/// task — abort it explicitly to shut discovery down.
pub fn start_discovery(port: u16) -> tokio::task::JoinHandle<()> {
    tokio::spawn(run_discovery(port))
}

/// The discoverable address of this machine, falling back to loopback when no
/// non-loopback IPv4 interface is up.
pub fn local_ipv4() -> String {
    match local_ip_address::local_ip() {
        Ok(ip) => ip.to_string(),
        Err(err) => {
            eprintln!("discovery: local IP lookup failed ({err}); using 127.0.0.1");
            "127.0.0.1".to_string()
        }
    }
}

/// Builds the broadcast payload, e.g. `BigPocket|192.168.1.20|8085`.
fn format_payload(ip: &str, port: u16) -> String {
    format!("BigPocket|{ip}|{port}")
}

async fn run_discovery(port: u16) {
    let socket = match UdpSocket::bind(BIND_ADDR).await {
        Ok(socket) => socket,
        Err(err) => {
            eprintln!("discovery disabled: cannot bind {BIND_ADDR}: {err}");
            return;
        }
    };

    if let Err(err) = socket.set_broadcast(true) {
        eprintln!("discovery: cannot enable broadcast: {err}");
    }

    eprintln!("Discovery broadcasting on UDP {DISCOVERY_PORT}...");
    loop {
        let payload = format_payload(&local_ipv4(), port);
        if let Err(err) = socket.send_to(payload.as_bytes(), BROADCAST_TARGET).await {
            eprintln!("discovery: broadcast failed: {err}");
        }
        tokio::time::sleep(BROADCAST_INTERVAL).await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn payload_matches_the_android_parser() {
        assert_eq!(
            format_payload("192.168.1.20", 8085),
            "BigPocket|192.168.1.20|8085"
        );
    }

    #[test]
    fn local_ip_is_never_empty() {
        assert!(!local_ipv4().is_empty());
    }
}
