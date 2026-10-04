# BigPocket Geliştirici & Ajan Kılavuzu (AGENTS.md)

Bu dosya, projede çalışan tüm yapay zeka ajanları (ve geliştiriciler) için temel mimari ilkeleri, dikkat edilmesi gereken kuralları, güvenlik & antivirüs yönergelerini ve sürüm yönetimi standartlarını tanımlar.

---

## 1. Proje Mimarisi & Kimlik Bilgisi
* **Repository Sahibi & Adı:** `benyigiteren/bigpocket` (GitHub)
* **Desktop (Sunucu):** Go (Golang) + WebView2 / Yerel Web Arayüzü (Port 8085)
* **Mobil (İstemci):** Kotlin + Jetpack Compose (Android minSdk 24, targetSdk 36)
* **İletişim Protokolleri:**
  - WebSocket (`ws://` / `wss://`, Port 8085)
  - Otomatik Keşif (LAN UDP Broadcast, Port 47800)
  - Medya/Ekran Akışı (TCP 8083, 8084, 8086)

---

## 2. Antivirüs & Windows Defender Optimizasyonu (ÖNEMLİ!)
Windows Defender ve diğer EDR/AV motorları Go ile yazılmış uygulamaları kolayca **False Positive (Yanlış Teşhis / Trojan-Heuristic)** olarak algılayabilir. Geliştirme yaparken şu kurallara **kesinlikle uyulmalıdır**:

1. **Powershell / CMD Çağrılarını Kısıtla:**
   - Arka planda `powershell.exe -Command ...` veya `cmd.exe /c ...` çalıştırmak Defender'ın en büyük şüphesidir.
   - Pano (Clipboard), bildirimler ve sistem işlemleri için mümkün oldukça yerel Windows Win32 API'leri (`user32.dll`, `shell32.dll`, `ole32.dll`) çağrılmalıdır.
2. **Derleme Bayrakları (Build Flags):**
   - Go ikili dosyalarını derlerken debug/sembol tablolarını gizlemeyen ve temiz PE çıktısı üreten parametreler kullanılmalıdır:
     ```powershell
     go build -ldflags "-H windowsgui -s -w" -o BigPocket.exe .
     ```
3. **UAC Elevation (Yönetici İzni):**
   - Kod içinde kendi kendini `runas` ile tekrar çağırmak yerine, derlenmiş dosyaya doğrudan `app.manifest` (requireAdministrator) gömülmelidir. Böylece Windows dosyayı açılışta tanır ve şüpheli injection saymaz.
4. **Dosya Sürüm ve Telif Bilgisi (PE Metadata / .syso):**
   - İsimsiz veya açıklamasız `.exe` dosyaları antivirüsler tarafından otomatik düşük güven skoru alır. `CompanyName`, `FileDescription`, `LegalCopyright`, `FileVersion` gibi alanların resource dosyasında tanımlı olması şarttır.

---

## 3. Güncelleme ve Dağıtım (Release) Sistemi
* **Doğrudan APK/EXE İndirme:**
  - Uygulama içi güncellemeler GitHub Releases API üzerinden `tag_name` ve hazır derlenmiş varlıkları (`BigPocket.apk`, `bigpocket-windows.zip` / `Setup.exe`) çeker.
  - Cihaz üzerinde asla derleme yapılmaz; hafif HTTP indirme akışı kullanılır ve kaynak tüketimi minimumdur.
* **Sürüm Güncelleme Prosedürü:**
  1. `desktop/updater.go` içindeki `AppVersion` güncellenir.
  2. `android/app/build.gradle.kts` içindeki `versionName` ve `versionCode` güncellenir.
  3. `CHANGELOG.md` dosyasına yeni sürüm ve notlar eklenir (kullanıcılar güncelleme ekranında bu notları görür).
  4. Hazır derlenmiş dosyalar GitHub Releases'a yüklenir.

---

## 4. Kod Düzenleme İlkeleri
* **Büyük Monolit Dosyalar:**
  - `MainScreen.kt` ve `main.go` monolitik durumdadır. Yeni özellik eklerken bu dosyaları daha da büyütmek yerine modüler paketlere (`services/`, `update/`, `plugins/`, `discovery/`) ayırın.
* **Otomatik Keşif (Discovery):**
  - Telefon ve PC aynı Wi-Fi ağındayken IP girmeye gerek kalmadan UDP 47800 üzerinden haberleşir. Bu mekanizmanın port ve paket formatı bozulmamalıdır.
