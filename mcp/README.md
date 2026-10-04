# BigPocket Stream Deck MCP Server (`bigpocket-streamdeck-mcp`)

BigPocket Stream Deck MCP sunucusu, yapay zeka ajanlarının (Claude Desktop, Cursor, Antigravity vb.) yerel BigPocket PC sunucusuna bağlanarak Stream Deck butonlarını dinamik olarak incelemesini, düzenlemesini, matris boyutunu ayarlamasını ve butonları doğrudan PC üzerinde tetiklemesini sağlar.

## Özellikler & Araçlar (Tools)

1. **`get_system_status`**: BigPocket sunucusunun IP adresleri, portları ve bellek kullanım istatistiklerini getirir.
2. **`get_streamdeck_config`**: Mevcut matris boyutunu (satır, sütun) ve tanımlı tüm Stream Deck butonlarını listeler.
3. **`set_streamdeck_button`**: Belirli bir buton yuvasını yapılandırır (etiket, `hotkey` veya `command` türü, eylem değeri, ikon).
4. **`set_grid_layout`**: Buton ızgarasının satır (1-5) ve sütun (2-8) sayısını günceller.
5. **`trigger_button`**: Buton yuvasındaki komutu veya kısayol tuşunu doğrudan Windows PC üzerinde çalıştırır.
6. **`list_installed_apps`**: Bilgisayarda yüklü olan programların ve masaüstü kısayollarının listesini döner.

---

## Kurulum ve Entegrasyon

### 1. Claude Desktop / Antigravity / Cursor Entegrasyonu

Aşağıdaki JSON bloğunu MCP yapılandırma dosyanıza ekleyin:

```json
{
  "mcpServers": {
    "bigpocket-streamdeck": {
      "command": "C:\\Users\\ygt\\Desktop\\projeler\\bigpocket-main\\mcp\\bigpocket-streamdeck-mcp.exe",
      "args": [
        "--url", "http://127.0.0.1:8085"
      ],
      "env": {
        "BIGPOCKET_API_KEY": ""
      }
    }
  }
}
```

> **Not:** Eğer BigPocket ayarlarında bir parola (Password) belirlediyseniz, `BIGPOCKET_API_KEY` ortam değişkenine veya `--apikey <parola>` parametresine bu şifreyi yazmanız yeterlidir.

---

## CLI Parametreleri

* `--url <adres>`: BigPocket masaüstü HTTP API adresi (Varsayılan: `http://127.0.0.1:8085`).
* `--apikey <anahtar>`: BigPocket oturum parolası (Eğer ayarlandıysa).
