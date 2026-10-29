# BigPocket Stream Deck MCP Server (`bigpocket-streamdeck-mcp`)

BigPocket Stream Deck MCP sunucusu; yapay zeka ajanlarının (Claude Desktop, Cursor, Windsurf, Cline, Antigravity vb.) yerel BigPocket PC sunucusuna bağlanarak Stream Deck ızgarasını, widget'ları ve PC otomasyonunu doğrudan yönetmesini sağlar.

İki çalışma modu vardır:

1. **HTTP / SSE (modern, önerilen):** BigPocket masaüstü sunucusunun kendi MCP uç noktası `http://127.0.0.1:8085/mcp` üzerinden JSON-RPC 2.0 (POST) + SSE (GET, `Accept: text/event-stream`) taşımasıyla çalışır.
2. **Stdio köprüsü:** `bigpocket-streamdeck-mcp.exe` bir stdio MCP sunucusu gibi davranır ve gelen istekleri canlı olarak `http://127.0.0.1:8085/mcp` uç noktasına iletir (`forward`). Masaüstü sunucusu erişilemezse, yerel HTTP uç noktalarına dayanan bir yedek (fallback) moda düşer.

---

## Araçlar (Tools)

Toplam **32** araç desteklenir. Masaüstü sunucusu erişilebilirken liste doğrudan sunucudan (`tools/list`) alınır; böylece araç seti her sürümde birebir eşitlenir.

### Durum ve Yapılandırma
- **`get_system_status`** — IP adresleri, portlar, sürüm ve sistem kaynak özeti.
- **`get_system_stats`** — Anlık CPU ve RAM kullanım yüzdeleri.
- **`get_network_stats`** — Canlı ağ verisi (indirme/yükleme kbps) + CPU, RAM ve CPU sıcaklığı.
- **`net_speed`** — Anlık ağ hızı (`net_down_kbps` / `net_up_kbps`, kbps).
- **`get_active_window`** — Ön plandaki Windows uygulamasının başlığı ve process adı.
- **`capture_screen`** — Ekran (veya belirtilen monitör) görüntüsünü base64 PNG olarak yakalar.
- **`list_installed_apps`** — Kurulu Windows uygulamaları ve masaüstü kısayolları.
- **`set_autostart`** — Windows ile otomatik başlatmayı açar/kapatır (`HKCU\...\Run`).
- **`setup_usb_reverse`** — USB kablo üzerinden ADB reverse port yönlendirme (1ms gecikme).

### Stream Deck Izgarası ve Sayfalar
- **`get_streamdeck_config`** — Izgara boyutu (satır/sütun), aktif sayfa, tüm sayfalar ve tüm butonlar.
- **`get_streamdeck_presets`** — Desteklenen widget türleri, hazır preset'ler, saat stilleri, fontlar ve şablonlar.
- **`set_grid_layout`** — Izgara satır (1-5) ve sütun (2-8) sayısını günceller.
- **`set_streamdeck_button`** — Mevcut butonu/widget'ı yerinde düzenler; yalnızca gönderilen alanlar değişir.
- **`add_streamdeck_button`** — Yeni buton/widget ekler (opsiyonel olarak bir preset'ten türetir).
- **`create_custom_widget`** — Sıfırdan tam parametreli özel widget/buton üretir.
- **`batch_create_streamdeck_buttons`** — Tek işlemde onlarca buton/widget oluşturur (dashboard üretimi).
- **`duplicate_streamdeck_button`** — Bir butonu hedef sayfaya kopyalar.
- **`delete_streamdeck_button`** — Bir butonu siler.
- **`swap_streamdeck_buttons`** — İki butonun görsel konumunu takas eder.
- **`streamdeck_switch_page`** — Aktif Stream Deck sekmesini/sayfasını değiştirir.
- **`create_streamdeck_page`** / **`rename_streamdeck_page`** / **`delete_streamdeck_page`** / **`clear_streamdeck_page`** — Sayfa yönetimi.
- **`apply_streamdeck_template`** — Hazır stüdyo şablonunu uygular (`pro_studio`, `media_lounge`, `gaming_obs`, `developer`, `system_control`).

### Eylemler ve Widget Kontrolleri
- **`trigger_button`** — Butonun kısayolunu çalıştırır veya komutunu yürütür.
- **`streamdeck_toggle_button`** — Toggle tipi butonu açar/kapatır (mikrofon, hoparlör, sanal monitör vb.).
- **`pomodoro_toggle`** — Pomodoro geri sayım widget'ını başlatır/duraklatır/sıfırlar.
- **`streamdeck_set_volume`** — Ana ses seviyesini ayarlar (`up`, `down`, `mute`, `set`).
- **`streamdeck_set_brightness`** — Ekran parlaklığını yüzde olarak ayarlar (0-100).
- **`send_pc_notification`** — PC'de toast/balon bildirimi gösterir.
- **`sync_clipboard`** — PC panosuna metin yazar ve mobil cihazlarla eşitler.

### Önemli Notlar
- `set_streamdeck_button`, `add_streamdeck_button`, `create_custom_widget` ve `batch_create_streamdeck_buttons`; `page`, `row_span`, `col_span`, `color`, `bg_style`, `clock_style`, `font_family` ve `clock_font_size` alanlarını **yalnızca** gönderildiğinde değiştirir. Belirtilmeyen alanlar korunur.
- Stdio köprüsü, yazma işlemlerini `GET/POST /config` yerine masaüstü sunucusunun tekil uç noktalarına (`/api/streamdeck/button`, `/api/streamdeck/layout`, `/api/streamdeck/trigger`, ...) yönlendirdiği için düzenlenmemiş buton alanları asla ezilmez.

---

## Yetkilendirme

BigPocket ayarlarında bir erişim şifresi belirlediyseniz, tüm istekler bu şifre ile doğrulanmalıdır. MCP istemcileri şifreyi şu yollardan iletebilir:

- **HTTP/SSE:** `X-Password: <şifre>` veya `Authorization: Bearer <şifre>` başlığı.
- **Stdio köprüsü:** `--apikey <şifre>` parametresi ya da `BIGPOCKET_API_KEY` ortam değişkeni.

Şifre yoksa yerel bağlantılar şifresiz çalışır.

---

## Kurulum ve Entegrasyon

### 1. HTTP / SSE MCP (Cursor, Windsurf, Claude Code)

Uzak MCP taşımasını destekleyen istemciler için (`mcp.json` / `mcp_config.json`):

```json
{
  "mcpServers": {
    "bigpocket-streamdeck": {
      "url": "http://127.0.0.1:8085/mcp"
    }
  }
}
```

Şifre kullanılıyorsa başlık ekleyin:

```json
{
  "mcpServers": {
    "bigpocket-streamdeck": {
      "url": "http://127.0.0.1:8085/mcp",
      "headers": { "X-Password": "SIFRENIZ" }
    }
  }
}
```

> Uç nokta aynı anda hem `POST` (JSON-RPC 2.0) hem de `GET` (SSE; `Accept: text/event-stream`) taşımasını kabul eder.

### 2. Stdio MCP (Claude Desktop, Cursor yerel process)

`claude_desktop_config.json` dosyanıza ekleyin:

```json
{
  "mcpServers": {
    "bigpocket-streamdeck": {
      "command": "C:\\Users\\ygt\\Desktop\\projeler\\bigpocket-main\\mcp\\bigpocket-streamdeck-mcp.exe",
      "args": ["--url", "http://127.0.0.1:8085"],
      "env": {
        "BIGPOCKET_API_KEY": ""
      }
    }
  }
}
```

> **Not:** Parola belirlediyseniz `BIGPOCKET_API_KEY` ortam değişkenine veya `--apikey <şifre>` parametresine yazın.

---

## CLI Parametreleri

- `--url <adres>` — BigPocket masaüstü HTTP API adresi (Varsayılan: `http://127.0.0.1:8085`).
- `--apikey <anahtar>` — BigPocket oturum parolası (ayarlandıysa). `BIGPOCKET_API_KEY` ortam değişkenini geçersiz kılar.

---

## REST API (curl / HTTP)

MCP dışında doğrudan HTTP uç noktalarını da kullanabilirsiniz:

| Yöntem | Uç Nokta | Açıklama |
| --- | --- | --- |
| `GET` | `/config` | Tüm yapılandırmayı döner |
| `POST` | `/config` | Yapılandırmayı kısmi (merge) günceller |
| `GET` | `/api/streamdeck` | Butonları ve aktif sayfayı listeler |
| `POST` | `/api/streamdeck/button` | Buton ekler / düzenler / siler / çoğaltır |
| `POST` | `/api/streamdeck/layout` | Izgara boyutunu ayarlar |
| `POST` | `/api/streamdeck/trigger` | Butonu tetikler |
| `POST` | `/api/streamdeck/volume` | Ana sesi yönetir |
| `GET` | `/api/system/stats` | Anlık CPU / RAM |
| `POST` | `/api/notify` | PC bildirimi gönderir |
| `POST` | `/api/clipboard` | PC panosuna yazar |
| `GET` | `/api/qr?data=...` | Çevrimdışı SVG QR kod üretir |
| `GET`/`POST` | `/api/autostart` | Otomatik başlatma durumu / değiştirme |
| `POST` | `/mcp` | MCP JSON-RPC 2.0 uç noktası |

Örnek:

```bash
curl -X POST http://127.0.0.1:8085/api/streamdeck/trigger \
  -H "Content-Type: application/json" \
  -d "{\"button_id\": 0}"
```
