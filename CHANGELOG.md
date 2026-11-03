# Değişiklik Günlüğü

Her sürüm için `## vX.Y.Z` başlığı altına yazılanlar, GitHub Release notu olarak
uygulamalardaki **"Yeni güncelleme"** penceresinde gösterilir.

## v1.5.5

### 🎨 Zengin İkon Seçici & Otomatik Uygulama Logosu (Masaüstü Web Arayüzü)
- **198 İkonluk Katalog:** Kart düzenleyicideki ikon seçici 36 simgeden 198 özenle çizilmiş simgeye çıkarıldı; her simge Türkçe adı, kategorisi ve arama etiketleriyle tanımlandı.
- **Anlık Arama:** "ses, kilit, web, güç" gibi Türkçe veya İngilizce kelimelerle anında filtreleme; çok kelimeli aramalar (örn. "oynat play") ve Türkçe karakter duyarsızlığı (`ses ac` → *Ses Aç*) desteklenir.
- **Kategori Sekmeleri:** Tümü, Medya, Sistem, Cihazlar, Uygulama ve Navigasyon sekmeleriyle filtreleme, sonuç sayacı ve "eşleşen ikon yok" durumu.
- **Otomatik Uygulama Logosu:** "Uygulama Aç" için uygulama seçildiğinde gerçek logosu otomatik alınır (`/extract_icon`), ikon seçicide anında gösterilir ve kaydedilir; logo alınamazsa ada göre akıllı simge eşlemesi devreye girer.
- **Kart Görsel Geri Bildirimi:** Seçili ikon hem düzenleyici başlığında hem de Stream Deck kartında (gerçek logo `<img>` olarak) anında güncellenir.

### 📱 Android: Aranabilir İkon Seçici & Otomatik Uygulama Logosu
- **172 Simgelik Kütüphane:** Telefon buton düzenleyicisindeki tek satırlık 39 simge, 172 gerçek Material simgesine (Filled + AutoMirrored) çıkarıldı; her simgenin Türkçe adı, kategorisi ve Türkçe/İngilizce arama etiketleri var. Eski kayıtlardaki ikon anahtarları (`volup`, `bright`, `now_playing`…) takma ad haritasıyla aynı simgeye çözümlenmeye devam eder.
- **Aranabilir Izgara:** "İkonlarda Ara… (ses, kilit, web, saat…)" arama alanı (temizle düğmeli), Tümü / Medya & Ses / Sistem & Güç / Cihazlar & Donanım / Uygulamalar & Web / Navigasyon & Oklar filtre çipleri, 5 sütunlu kaydırılabilir ızgara, sonuç sayacı ve üstte anlık önizleme.
- **Otomatik Uygulama Logosu:** "PC Uygulamalarında Ara" listesinden bir uygulama seçildiğinde butonun komutu ve başlığı doldurulur; gerçek logo varsa `/installed_apps` önbelleğinden alınır, yoksa `/extract_icon` çağrısıyla bilgisayarda çıkarılır ve diyalog önizlemesinde Coil ile anında gösterilir. Çıkarım sürerken yükleme göstergesi ve geçici simge gösterilir.
- **Liste Küçük Resimleri:** Uygulama listesi, önbelleğe alınmış gerçek logoları satır küçük resmi olarak gösterir.

### 🪟 Yeni Bileşenler: Pencere Yönetimi & Sistem Güç Eylemleri
- **Pencere Yönetimi (Window Actions):** Ön plandaki pencereyi tek dokunuşla Tam Ekran Yapma (Maximize), Simge Durumuna Küçültme (Minimize), Geri Yükleme (Restore), Ekranın Soluna/Sağına Yaslama (Snap Left / Right), Kapatma ve Monitörler Arası Geçiş (Next Monitor) aksiyonları eklendi.
- **Sistem Güç & Hızlı Eylemler (System Actions):** Bilgisayarı Uyutma (Sleep), Yeniden Başlatma (Restart), Bilgisayarı Kapatma (Shutdown), Ekranı Kilitleme (Lock), Geri Dönüşüm Kutusunu Boşaltma ve Ekran Alıntısı Aracı (Snipping Tool) Stream Deck buton türü olarak doğrudan entegre edildi.
- **34/34 MCP Aracı:** `window_action` ve `system_action` araçları Model Context Protocol (MCP) şemasına da eklenerek yapay zeka ajanlarının kullanımına açıldı.
## v1.5.4

### 🚀 Evrensel Çoklu Platform Desteği (Windows, macOS, Linux)
- **Çapraz Platform Pano & Bildirimler:** macOS ve Linux için yerel pano senkronizasyonu (`arboard`) ve yerel masaüstü bildirimleri (`notify-rust`) entegre edildi.
- **Sistem Kontrolleri:** macOS'ta AppleScript ve Linux'ta PipeWire/PulseAudio (`wpctl`/`pactl`) ile yerel ses kontrolü; macOS ve Linux için donanımsal ekran parlaklığı desteği sağlandı.
- **Medya Takibi:** macOS (Spotify / Apple Music) ve Linux (MPRIS / `playerctl`) üzerinden çalan şarkı ve medya kontrolü bağlandı.
- **Tek Örnek Koruması:** Windows harici sistemlerde kilit dosyası tabanlı (`fs2`) çakışmasız tek uygulama güvencesi eklendi.

### 📱 Android 16 (API 36) Tam Uyumluluk & Temiz Açılış
- **Android 16 Desteği:** `targetSdk` 36'ya yükseltildi; modern edge-to-edge ve sistem kurallarına tam uyum sağlandı.
- **Açılışta Sıfır İzin Dayatması (On-Demand):** İlk açılışta ekrana fırlatılan kamera ve mikrofon izin pop-up'ları kaldırıldı; izinler yalnızca kullanıcı ilgili özelliği (QR Kod veya Ses/Kamera Yayını) açtığında talep edilir.
- **Yeni Bildirim Çubuğu İkonu:** Bildirim çekmecesindeki eski telefon simgesi yerine BigPocket'in modern cep silüeti ikonu yerleştirildi.
- **Android 7 - 15 Kapsamlı Kararlılık:** Android 10-14 arka plan servis kısıtlamaları (`ForegroundServiceStartNotAllowedException`), depolama izinleri ve Wi-Fi düşük gecikme kilidi (`WIFI_MODE_FULL_LOW_LATENCY`) kusursuz hale getirildi.

### 🤖 Model Context Protocol (MCP) & Yapay Zeka Entegrasyonu
- **32/32 MCP Aracı Canlı:** Claude, Cursor ve harici yapay zeka ajanlarının BigPocket'i tam yetkiyle yönetebilmesi için `/mcp` JSON-RPC 2.0 rotası ve tüm sistem araçları aktifleştirildi.
- **Atomik Stream Deck API'si:** Ajanların tüm ayar dosyasını ezmeden tekil buton eklemesini, güncellemesini ve silmesini sağlayan `/api/deck/button` endpoint'i, çakışma önleyici ve otomatik hücre yerleştirici yazıldı.
- **Windows Kilitle & Kapat Butonları:** Stream Deck Ana Sayfası'na tek dokunuşla çalışan Windows Kilitleme (`meta+l`) ve Bilgisayarı Kapatma butonları entegre edildi.

### 📦 Profil Paylaşımı & Hazır Şablonlar
- **Dışa & İçe Aktarma (Zip):** Stream Deck butonları ve özel ikonları tek tıkla `.zip` paketi olarak dışa aktarılabilir (`/api/deck/profile/export`) ve başka cihazlara yüklenebilir.
- **5 Hazır Şablon:** Pro Studio, Media Lounge, Gaming / OBS, Developer ve System Control hazır şablonları eklendi.

### 🔍 Çoklu Alt Ağ (Subnet) Keşif & Otomatik Tarama
- Tüm aktif ağ kartlarına (Wi-Fi, Ethernet, USB Tethering) eş zamanlı alt ağ yayını ve AP Isolation engeline karşı otomatik alt ağ taraması eklendi.

## v1.5.3-beta

### 🛡️ Android 10 (API 29) & Genel Android Kararlılığı
- **Arka Plan Servis Çökme Koruması:** Android 10 ve altı sürümlerde API 30+ (Android 11) gerektiren `FOREGROUND_SERVICE_TYPE_MICROPHONE` ve `CAMERA` bayraklarının çağrılması engellendi; her Android sürümünde kararlı çalışan çift kademeli `startForeground` geri dönüşü eklendi.
- **Doğrudan İndirilenler (Downloads/BigPocket):** Android'de indirilen dosyalar gizli iç klasörden genel `Download/BigPocket` klasörüne aktarılarak Dosyalarım uygulamasında anında görünür hale getirildi.

### 💻 Windows Masaüstü & Sunucu İyileştirmeleri
- **Çalışan "Sunucuyu Kapat" Fonksiyonu:** Web arayüzündeki kapatma butonu `/shutdown` ve `/api/shutdown` rotalarına bağlandı; tıklandığında sanal ekranı temizleyip arka plan sunucu sürecini eksiksiz sonlandırır.
- **Otomatik Windows Güvenlik Duvarı İzni:** Uygulama ilk açıldığında ve `BigPocket-Setup.exe` kurulumunda port 8085 ve UDP 47800 için gelen bağlantı kuralları otomatik eklenir; Ayarlar menüsüne tek tıkla "Güvenlik Duvarı İznini Yenile" butonu eklendi.
- **İndirilen Dosyaları Downloads Klasörüne Kaydetme:** Telefondan PC'ye aktarılan tüm dosyalar doğrudan kullanıcının `İndirilenler/BigPocket` klasörüne kopyalanır ve web arayüzünden klasör tek tıkla açılabilir.

### 🔍 Tek Tıkla Bağlantı Teşhisi & Hata Tespiti (PC & Telefon)
- **Android Hata Teşhis Kartı & Raporu:** Bağlantı koptuğunda veya PC bulunamadığında ekranda Türkçe açıklama çıkar; tek dokunuşla tüm ağ, cihaz ve hata detaylarını içeren rapor panoya kopyalanır.
- **PC Teşhis Raporu:** Masaüstü Ayarlar sayfasına tek tıkla IP ve port teşhis raporunu panoya kopyalayan araç entegre edildi.
- **WhatsApp Akışı Çift Mesaj Düzeltmesi:** Pano eşitlemesinde mesajların akışa 2 kez yazılması sorunu giderildi ve tekilleştirme koruması eklendi.

## v1.5.2-beta

### 🎵 Şimdi Çalıyor Kartı Hücre Oranı & Boyutlandırma İyileştirmesi
- **Tam Sığdırma & Sıfır Kesilme:** Mobil Stream Deck ızgarasında 2×1 Şimdi Çalıyor kartının dikey içeriği 128dp'den 92dp'ye optimize edildi; alttaki Oynat/Durdur butonlarının kart kenarından taşması ve kesilmesi tamamen giderildi.
- **Proposyonel Buton Çapları:** Oynatma kontrolleri PC standartlarına uygun olarak 28dp / 24dp boyutuna dengelendi; dar telefon ekranlarında bile ferah kenar boşluklarıyla kusursuz oturur.
- **Hassas Dikey Padding:** Kartın üst ve alt dikey iç boşluğu 9dp'ye optimize edildi; başlık, albüm kapağı rozeti ve şarkı bilgileri tek bakışta net okunur.

## v1.5.1-beta
### 🎚️ 4 Farklı Sürgü Tasarım Sistemi (Apple, Samsung One UI, Material You, Framer)
- **4 Özel Sürgü Tarzı:** Ses ve parlaklık kontrolleri için kullanıcı tercihine bağlı 4 farklı tasarım dili:
  - *Apple Capsule:* 10dp inceliğinde zarif beyaz yatay kapsül veya iOS Denetim Merkezi dikey hap sürgüsü.
  - *Samsung One UI 6:* Galaxy S24 tarzı yumuşak dokunsal kalın çubuk ve entegre simge.
  - *Google Material You:* Android 14/15 kabarcık (bubbly) hap formu ve hareketli dolguya gömülü simge.
  - *Framer Minimal:* Siberpunk / Nothing OS esintili 3dp kılcal ray ve parlak neon yeşil disk göstergesi.
- **Yatay & Dikey Yön Seçici:** İster otomatik (hücre en-boy oranına göre), ister zorunlu dikey (yukarıdan aşağıya), ister yatay sürgü yönü.

### 📱 1:1 Hibrit PC Stream Deck Motoru & Kenarlıksız Tam Ekran
- **PC ile Birebir Piksel Eşitliği:** Mobil Stream Deck sekmesi artık doğrudan PC'nin servis ettiği tam ekran web sahnesini donanım hızlandırmalı olarak açar. Yazı tipleri, albüm kapağının hafif buzlu cam efekti, beyaz zeminli medya butonları, sayaçlar ve kısayollar PC ile %100 aynıdır.
- **Sıfır Gecikme:** Dokunmalar yerel WebSocket üzerinden 0-1 ms içinde PC'ye iletilir.
- **Çift Yönlü Donanım Sesi Eşitleme:** PC'deki klavye/kulaklık veya telefondaki kaydırıcı birbirini anında senkronize eder.

### 📥 WhatsApp Akışı Canlı İndirme & Aç
- **Anlık Yükleme Göstergesi:** İndirme butonuna basıldığı an canlı dairesel yükleme halkası çıkar.
- **Doğrudan Aç Düğmesi:** İndirme tamamlandığında buton yeşil "✓ Aç" hapına dönüşür ve galeri/dosya yöneticisiyle anında açılır.

### 🛡️ Android 14/15 Ağ & Cleartext Düzeltmesi
- **android:usesCleartextTraffic="true":** Android WebView'ın yerel ağdaki `http://` bağlantılarını engellemesi sorunu çözüldü, siyah ekran riski tamamen ortadan kaldırıldı.
- **Otomatik Fail-Safe:** Ağ gecikmesinde bile ekranın asla boş kalmaması için yerel ızgara koruması devrede.

## v1.5.0-beta
### 🦀 Rust + Tauri v2 Mimari Dönüşümü
- **Tamamı Rust Backend:** Masaüstü sunucusu Go'dan sıfır gecikmeli, bellek güvenli ve asenkron Rust (Tauri v2 + Tokio + Axum) mimarisine taşındı.
- **Çapraz Platform Mimarisi:** macOS ve Linux için platform soyutlamaları eklendi; Windows Defender yanlış alarmları (false-positive) sıfırlandı.
- **Sıfır Gecikmeli Ekran & Giriş:** Rust `xcap` ile donanım seviyesi ekran yakalama ve `enigo` ile platform bağımsız klavye/fare kontrolü.
- **Yerel QR & Vektör Render:** Harici internet servisine ihtiyaç duymadan Rust içinde sıfır milisaniyede üretilen SVG QR kod eşleştirmesi.
- **Android v1.5.0-beta:** versionCode 8, SemVer 2.0 çift kanal otomatik güncelleme desteği.

## v1.4.2-beta
### 🧪 Çift Kanal Sürüm & Beta Önizleme Motoru
- **Beta Güncelleme Kanalı:** Kullanıcılar Ayarlar penceresinden tek dokunuşla "Beta Güncellemelerini Al" modunu açarak yeni özellikleri resmi kararlı sürümden önce test edebilir.
- **SemVer 2.0 Algoritması:** `v1.4.2 > v1.4.2-beta` ve `v1.4.2-beta.2 > v1.4.2-beta.1` kuralı ile beta kullananlar resmi tam sürüm çıktığında sorunsuz ana sürüme yükseltilir.

## v1.4.1

### 💬 WhatsApp Tarzı Hızlı Akış (Flow) & Bütünsel Editorial Monokrom
- **Hızlı Akış (WhatsApp Modu):** Bilgisayar ve telefon arasında sıfır kurulumla konuşma balonu, web bağlantısı, fotoğraf ve dosya transferi.
- **Apple Standart Zümrüt Yeşili Anahtarlar:** Açık/kapalı durumu %100 net ayırt edilen iOS yeşili switchler (`#22C55E` track + beyaz thumb).
- **İsteğe Bağlı Arka Plan Yayını:** Telefon ekranı kilitlendiğinde veya başka uygulamadayken kamera ve mikrofon açık kalabilir (Medya sekmesinden veya Ayarlar'dan tek tıkla kontrol edilebilir).
- **Sıfır GC Ekran Akışı:** Windows `sync.Pool` ve Android çift tamponlu `inBitmap` ile sıfır bellek sızıntısı ve takılmasız 30 FPS.
- **Hızlı Komut Paleti (Ctrl+K):** Raycast tarzı arama ve sistem kısayolları.
- **Windows WASAPI Ses Mikseri:** Spotify, Chrome, Discord gibi uygulamaların sesini bağımsız ayarlayan per-app kapsül kaydırıcı.

## v1.4
### 🍏 Apple Control Center Tarzı Kapsül Kaydırıcılar (Ses & Parlaklık)
- **Akıcı Dokunmatik Kapsül (Pill Slider):** Eski basit butonlar yerine iOS Denetim Merkezi'ndeki gibi akıcı, dikey/yatay parmak sürükleme ve tıklamayla %0-%100 seviyesini anında ayarlayan Apple kapsül kaydırıcıları entegre edildi.
- **Akıllı Kontrast & Dinamik İkonlar:** Doluluk arttıkça metin ve simgeler arka plana göre otomatik olarak açık/koyu renk kontrastına geçer. Ses düzeyi simgesi sessiz, düşük ve yüksek seviyelere göre dinamik güncellenir; parlaklık için güneş simgesi ve sıcak amber ışıltısı eklendi.
- **Haptik Titreşim:** Sürükleme esnasında dokunsal haptik titreşim desteği sunuldu.

### 🎛️ Elgato Stream Deck+ Döner Kadran Konsolu (Rotary Dials)
- **Fiziksel 4'lü Kadran Çubuğu:** Stream Deck'in altına Elgato Stream Deck+ donanımından esinlenilen döner kontrol paneli eklendi.
- **Ana Ses Kadranı:** Dairesel LED gösterge halkası, tek dokunuşla sessize alma (`Mute`) ve çevirerek hassas ses ayarı.
- **Ekran Parlaklığı Kadranı:** Monitör parlaklığını çevirerek hassas ayarlama (%0 - %100).
- **Mikrofon Kadranı:** Canlı mikrofon durumu ve tek dokunuşla kapatma/açma.
- **Sistem Yükü Kadranı:** Anlık işlemci sıcaklığı (°C) ve kullanım oranı göstergesi.

### 🖲️ Elgato Stream Deck Donanım Tuş Kapağı (Keycap)
- **3D Fiziksel Eğim & LCD Çerçevesi:** Gerçek donanım tuş eğimi (`#20222D` - `#101117`), üst ışık yansıması ve gömülü iç ekran (`#090A0E`).
- **Dokunsal Basış Hissi:** Tuşa basıldığında fiziksel tuş hissi veren hafif küçülme animasyonu (`0.94x scale`).
- **Donanım Durum LED'i:** Aç/Kapa (toggle) tuşlarının köşesinde zümrüt yeşili neon donanım LED ışığı.

### ⏱️ OLED / Flip Dijital Saat & Canlı Telemetri
- **Dijital Saat Widget'ı:** Canlı saniyeli split-flap saat, nabız atan `:` ayracı ve Türkçe gün/tarih rozeti.
- **Donanım Telemetri Kartı:** CPU % + Sıcaklık (°C) ve RAM GB kullanım çubuğu.

### 📈 Canlı Performans Sparkline Grafikleri
- **Kayan Geçmiş Penceresi:** CPU, RAM ve ağ hızı (indirme/yükleme) için son 24-30 veri noktası tutan kayan tampon eklendi.
- **PC Web Paneli:** `telemetry_combo`, `cpu`, `ram` ve `net_speed` kartlarının altında/arkasında ince monokrom bir SVG sparkline çizilir; eğrinin altında %4 opaklıkta gradyan dolgu bulunur.
- **Android Stream Deck:** Aynı kartlarda Canvas ile yumuşak bezier geçmiş eğrisi (`MinimalistPrimary` %60 çizgi, %6 alt dolgu) çizilir; RAM ve yükleme ikinci, daha soluk bir eğri olarak eklenir.

### 🔆 Yerel Monitör Parlaklığı Entegrasyonu
- **WMI Parlaklık Kontrolü:** Windows monitör parlaklığı WMI API ile doğrudan PC üzerinden ayarlanır ve telefonla senkronize çalışır.

### ⌨️ Hızlı Komut Paleti (Ctrl+K)
- **Raycast Tarzı Cam Palet:** Masaüstü kontrol panelinde `Ctrl+K` (macOS: `Cmd+K`) veya kenar çubuğundaki büyüteç düğmesiyle ekranın ortasında açılan, bulanık cam zeminli ve tamamen monokrom komut paleti eklendi.
- **Sekme · Sistem · Uygulama Komutları:** Sekmeler, hızlı sistem eylemleri (ses aç/kıs/sustur, ekran kilidi, sanal monitör aç/kapat, ekran görüntüsü, pano eşitleme) ve uygulamalar (Görev Yöneticisi, Hesap Makinesi, Not Defteri, Terminal, VS Code, Tarayıcı) tek arama listesinde birleştirildi.
- **Klavye Odaklı Kullanım:** Yazarken anında filtreleme, `↑` `↓` ile seçim, `Enter` ile çalıştırma, `Esc` ile kapatma; sonuçlar alt köşede monokrom bildirimle raporlanır.

### 🔊 Uygulama Bazlı Ses Mikseri (Per-App Volume)
- **Core Audio (WASAPI) Oturum Listesi:** Varsayılan çıkış aygıtına ses gönderen tüm uygulamalar yerel COM API'siyle (`IMMDeviceEnumerator` → `IAudioSessionManager2` → `IAudioSessionEnumerator`) listelenir; PID, exe adı, canlı seviye ve çalma durumu raporlanır. Yeni `GET /api/audio_sessions` REST uç noktası bu listeyi paylaşır.
- **Uygulama Sesi Widget'ı:** Stream Deck bileşen seçicisine "Uygulama Sesi" kartı eklendi. Kart oluşturulurken ses çalan uygulamalar otomatik listelenir; Spotify, Chrome, Discord veya Steam oyunları seçilip iOS tarzı kapsül kaydırıcı ile yalnızca o uygulamanın sesi kısılıp açılır.
- **Anlık Kapsül Sürükleme:** Kapsül üzerindeki parmak/fare sürüklemesi `{"type":"set_app_volume"}` WebSocket mesajına dönüşür ve hedef uygulamanın tüm WASAPI oturumları `ISimpleAudioVolume` ile anında güncellenir; yeni seviye tüm bağlı istemcilere yayınlanır.
- **Canlı Senkronizasyon:** Uygulama sesi BigPocket dışından değiştirildiğinde kart seviyesi periyodik olarak yenilenir.

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
