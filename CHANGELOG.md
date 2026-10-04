# Değişiklik Günlüğü

Her sürüm için `## vX.Y.Z` başlığı altına yazılanlar, GitHub Release notu olarak
uygulamalardaki **"Yeni güncelleme"** penceresinde gösterilir.

## v1.1

### 🛡️ Güvenlik & Android Play Protect / Xiaomi Çözümü
- **Temiz Güvenlik İmzası:** Antivirüs ve Google Play Protect / Xiaomi Güvenlik tarayıcılarında yanlış alarmlara (false-positive / PHA) neden olan bildirim dinleme servisi (`NotificationListenerService`) ve paket izinleri manifestten tamamen arındırıldı.
- **Yerel Ağ Güvenlik Yapılandırması:** `network_security_config.xml` ile yerel ağ (LAN) HTTP/WebSocket trafiği standartlara uygun hale getirildi; SDK hedefi kararlı Android 15 (API 35) seviyesine çekildi.
- **Resmi Üretim İmzası:** APK, 2048-bit RSA üretim anahtarıyla (`release.jks`) V1, V2, V3 ve V4 imza şemaları ile derlendi.

### 🎨 Yeni Minimalist & Ortak Marka Kimliği (Logo)
- **Kusursuz Tasarım Senkronu:** Hem PC masaüstü simgesinde, pencere başlık çubuğunda ve WebView arayüzünde hem de Android başlatıcı ve ekranında ortak, modern "Cepte Telefon" (Phone-in-Pocket) logosu uygulandı.
- **Yerel Windows İkonu (.syso):** `BigPocket.exe` ve `Setup.exe` derlemelerine gömülü kaynak dosyası eklenerek Windows Gezgini'nde varsayılan boş pencere simgesi yerine özel BigPocket simgesi entegre edildi.

### 🤖 Model Context Protocol (MCP) & Yapay Zeka Entegrasyonu
- **bigpocket-streamdeck-mcp:** Claude Desktop, Cursor ve otonom AI ajanlarının BigPocket'e bağlanarak Stream Deck butonlarını incelemesini, düzenlemesini ve PC üzerinde komut tetiklemesini sağlayan yerel MCP sunucusu eklendi.
- **Ayarlar Sekmesinde Hazır JSON:** BigPocket şifresini API anahtarı olarak kullanan ve tek tıkla kopyalanabilen Claude Desktop / Cursor yapılandırma bloğu arayüze eklendi.

### ⚡ Yenilenen Stream Deck & Canlı Test Butonu
- **Kompakt ve Ergonomik Kartlar:** Önceki devasa boş butonlar yerine şık, durum rozetli (`HOTKEY` / `CMD`) ve dengeli kart yapısı getirildi.
- **Dahili Test Butonu:** Buton düzenleme penceresini açmaya gerek kalmadan, kısayolun veya uygulamanın PC üzerinde çalıştığını anında doğrulayan tek tıkla "Test" özelliği eklendi.

### 🚀 Kesintisiz Güncelleme Dağıtımı
- **Tek Tıkla Yükseltme:** Eski sürüm çalıştıran kullanıcılar için hem Android'de hem Windows'ta açılışta güncelleme notlarını gösteren ve tek tıkla indirip kuran otomatik dağıtım sistemi aktif edildi.

## v1.0

### Yenilikler
- **Otomatik bağlantı:** Telefon, aynı Wi‑Fi'deki BigPocket PC'yi kendisi bulur ve bağlanır — IP yazmaya gerek yok.
- **Uygulama içi güncelleme:** Yeni sürüm çıkınca hem PC'de hem telefonda güncelleme notlarıyla birlikte pencere açılır, tek tuşla güncellenir.
- **Yumuşak siyah tema:** Daha göz yormayan antrasit tonlar ve lavanta vurgu rengi.
