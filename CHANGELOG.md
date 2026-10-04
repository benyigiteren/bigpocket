# Değişiklik Günlüğü

Her sürüm için `## vX.Y.Z` başlığı altına yazılanlar, GitHub Release notu olarak
uygulamalardaki **"Yeni güncelleme"** penceresinde gösterilir.

## v1.3

### ⚡ USB Sıfır Gecikme Modu (1ms ADB Köprüsü)
- **Ultra Düşük Gecikme:** Telefon USB kablosuyla PC'ye bağlandığında ADB ters port yönlendirmesi (`adb reverse`) otomatik yapılandırılarak tüm WebSocket, yayın ve kontrol gecikmesi 1ms seviyesine düşürüldü.
- **Tek Tıkla Bağlantı:** Mobil giriş ekranında ve masaüstü web panelinde tek dokunuşla "USB Sıfır Gecikme (127.0.0.1)" moduna geçiş eklendi.

### 🖥️ Sanal Ekran & Donanım Fare İmleci Optimizasyonu
- **Görünür Fare İmleci:** İkinci ekranda donanım faresi imleci konumu Win32 API (`GetCursorInfo`) ile alınarak yayın kareleri üzerine yüksek kontrastlı (siyah dış çerçeveli beyaz işaretçi) olarak çizildi.
- **Kuyruk Donması Giderildi:** TCP soketine 45ms yazma zaman aşımı (`SetWriteDeadline`) ve `tcpNoDelay` uygulanarak ağ dalgalanmalarındaki kare birikmesi ve donmalar tamamen yok edildi.
- **Dokunmatik Kontrol Araç Çubuğu:** İkinci ekranda tek tık, çift tık, uzun basma (sağ tık) ve sürükleme desteklendi. Ekranın altına Sol/Sağ Tık geçişi, doğrudan PC'ye metin/tuş gönderen ⌨ Klavye diyaloğu, Dikey Kaydırma (🔼/🔽) ve Sığdır/Doldur ölçek butonları eklendi.
- **Otomatik Kapatma:** BigPocket kapatıldığında sanal ekran sürücüsü otomatik olarak sonlandırılarak arka planda açık kalması engellendi.

### 🎛️ Genişletilmiş Stream Deck (Çok Sayfalı, Canlı LED, Ses Barı, Canlı Sistem)
- **Çoklu Sayfa Desteği:** "Ana Sayfa", "Medya & Ses", "Sistem & PC" sayfaları arasında yatay sekmelerle anında geçiş sağlandı.
- **Canlı LED Göstergeli Aç/Kapa (Toggle) Butonları:** Yeşil (Açık) ve Gri/Kırmızı (Kapalı) canlı durum göstergeleri (Mikrofon Mute, Hoparlör Mute, Sanal Ekran vb.).
- **Ses Kaydırıcı & Kontrol Barı:** Canlı ses yüzdesi, kademe butonları (`[-]` / `[+]`) ve master ses düzeyi ayarı.
- **Canlı Sistem Widget'ları:** Stream Deck üzerinde anlık CPU % ve RAM % göstergeleri.
- **Gelişmiş MCP & REST API:** Yeni Stream Deck araçları (`streamdeck_set_volume`, `streamdeck_toggle_button`, `streamdeck_switch_page`, `get_system_stats`, `setup_usb_reverse`) ve REST uç noktaları eklendi.

### 📹 Kamera Akışı FPS & Kalite Ayarları
- **Özelleştirilebilir Kare Hızı:** 15 FPS, 30 FPS ve 60 FPS seçenekleri ile CameraX tampon taşması önlendi.
- **Çözünürlük Seçenekleri:** 480p SD, 720p HD ve 1080p FHD kaliteleri ile ağ bant genişliğine göre esnek kullanım sunuldu.

## v1.2

### 🎨 Kusursuz İkon Standardizasyonu (Mobil & Masaüstü)
- **Mobil Launcher & Uygulama İçi Uyum:** Android başlatıcı simgesi (`ic_launcher_foreground.xml` ve `ic_launcher_background.xml`) sıfırdan vektörel olarak çizilerek uygulama içindeki minimalist koyu cep ve neon mor telefon logosuyla birebir eşitlendi.
- **Görev Çubuğu & Masaüstü İkonu:** Windows görev çubuğunda (taskbar) eski simgenin kalmasına sebep olan `.syso` kaynakları, `desktop/winres` ikonları ve `/logo.svg` sunucusu baştan sona yenilendi.
- **Daha Belirgin Sidebar Logosu:** Kenar çubuğundaki logo 42px boyutuna genişletilerek estetik kavis ve gölge efekti kazandırıldı.

### 🔓 Masaüstünde Şifresiz Doğrudan Erişim
- **Yerel Erişimde Sıfır Engel:** BigPocket bilgisayarda yerel olarak açıldığında (`localhost`/`127.0.0.1`) artık asla erişim şifresi sorulmaz; doğrudan tam yetkiyle açılır. Şifre koruması yalnızca uzaktan bağlanan telefon istemcileri için devrede kalır.

### 🌐 Evrensel Model Context Protocol (MCP) & REST API
- **Doğrudan HTTP / SSE MCP Desteği:** `http://localhost:8085/mcp` uç noktası üzerinden Claude Desktop, Cursor, Cline ve Windsurf ile harici bir işlem çalıştırmadan doğrudan bağlantı sağlandı.
- **Sekmeli Yeni Geliştirici Arayüzü:** Ayarlar sekmesinde HTTP/SSE MCP, Stdio MCP ve doğrudan REST API için sekmeli, modern ve tek tıkla kopyalanabilen yapılandırma kartları eklendi.
- **Zengin Araç Yelpazesi:** Stream Deck buton tetikleme, buton yapılandırma, grid düzenleme, kurulu uygulamaları listeleme, pano eşitleme ve sistem bildirimleri dahil 8 adet MCP aracı hazırlandı.

### 🛡️ Windows Defender & PE Metadata Optimizasyonu
- **Resmi Telif & Ürün Bilgisi:** `BigPocket.exe` ve `Setup.exe` ikili dosyalarına telif hakkı, ürün adı ve 1.2.0.0 sürüm PE başlıkları gömülerek antivirüs güven skoru maksimum seviyeye çıkarıldı.

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
