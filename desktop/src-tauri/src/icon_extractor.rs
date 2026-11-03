//! Application icon extraction with an on-disk cache.
//!
//! A stream-deck "open application" button shows the real logo of the program
//! it launches. This module resolves that logo on all three desktop platforms
//! and stores it as a PNG under `<frontend>/icons/`, so the web UI only has to
//! point an `<img>` at the returned path.
//!
//! * **Windows** — `SHGetFileInfoW` resolves `.exe`, `.ico` and `.lnk` alike
//!   (the shell follows shortcuts for us). The icon is taken from the shell's
//!   *jumbo* image list so modern applications keep their 256×256 artwork, and
//!   falls back to the classic 32×32 `SHGFI_ICON` icon. Icons that predate the
//!   alpha channel get their transparency rebuilt from the AND mask.
//! * **macOS** — `Contents/Info.plist` → `CFBundleIconFile` → `.icns`, which
//!   `sips` converts to PNG.
//! * **Linux** — the `.desktop` entry's `Icon=` name is looked up in the
//!   hicolor and pixmaps icon directories; SVG/XPM are rasterised with whatever
//!   converter is installed.
//!
//! Extraction is cached: the file name is derived from a hash of the
//! application path and an existing, valid PNG short-circuits the work, so
//! every application icon is extracted at most once. That split is what the
//! callers use: [`cached_icon`] answers in microseconds and backs the installed
//! application listing, [`get_or_extract_icon`] backs the on-demand endpoints,
//! and [`prewarm_icons`] completes the cache in the background.

use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};

/// Directory (relative to the frontend root) that caches extracted icons.
pub const ICONS_SUBDIR: &str = "icons";

const PNG_MAGIC: [u8; 8] = [0x89, b'P', b'N', b'G', 0x0d, 0x0a, 0x1a, 0x0a];

/// Frontend-relative cache file name for `app_path`, e.g. `icons/app_9f86d0….png`.
pub fn icon_cache_rel_path(app_path: &str) -> String {
    format!("{ICONS_SUBDIR}/app_{}.png", cache_key(app_path))
}

/// Absolute path of the cache file for `app_path`.
pub fn icon_cache_path(app_path: &str, frontend_dir: &Path) -> PathBuf {
    frontend_dir.join(icon_cache_rel_path(app_path))
}

/// Frontend-relative path of an already-cached icon, if there is one.
///
/// Never touches the source application, so it is cheap enough to call for
/// every entry of the installed-application list.
pub fn cached_icon(app_path: &str, frontend_dir: &Path) -> Option<String> {
    let rel = icon_cache_rel_path(app_path);
    has_png(&frontend_dir.join(&rel)).then_some(rel)
}

/// Return the frontend-relative path of `app_path`'s icon, extracting it into
/// the cache first when needed.
pub fn get_or_extract_icon(app_path: &str, frontend_dir: &Path) -> Option<String> {
    let app_path = app_path.trim();
    if app_path.is_empty() {
        return None;
    }

    let rel = icon_cache_rel_path(app_path);
    let target = frontend_dir.join(&rel);
    if has_png(&target) {
        return Some(rel);
    }

    let parent = target.parent()?;
    if std::fs::create_dir_all(parent).is_err() {
        return None;
    }

    match extract_icon_to_png(app_path, &target) {
        Ok(true) if has_png(&target) => Some(rel),
        Ok(_) => None,
        Err(err) => {
            tracing::debug!(app = app_path, error = %err, "icon extraction failed");
            None
        }
    }
}

/// Extract the icons of many applications at once, reporting the
/// frontend-relative path of each.
///
/// On Windows the whole batch shares a single shell COM apartment: setting one
/// up costs more than an icon does, so a batch is much cheaper than the same
/// number of individual calls.
pub fn get_or_extract_icons(app_paths: &[String], frontend_dir: &Path) -> Vec<Option<String>> {
    if app_paths.is_empty() {
        return Vec::new();
    }
    with_shell_com(|| {
        app_paths
            .iter()
            .map(|path| get_or_extract_icon(path, frontend_dir))
            .collect()
    })
}

/// Fills the icon cache in the background, at most one pass per process.
///
/// Listing installed applications reports cached icons only, so this pass is
/// what makes the next listing complete. It is deliberately detached: an
/// application icon costs one shell round trip, and nothing on a request thread
/// may wait for hundreds of them.
pub fn prewarm_icons(app_paths: Vec<String>, frontend_dir: PathBuf) {
    static RUNNING: AtomicBool = AtomicBool::new(false);
    if app_paths.is_empty() || RUNNING.swap(true, Ordering::AcqRel) {
        return;
    }

    let spawned = std::thread::Builder::new()
        .name("icon-prewarm".to_string())
        .spawn(move || get_or_extract_icons(&app_paths, &frontend_dir));

    if spawned.is_err() {
        // Let a later request try again rather than giving up for good.
        RUNNING.store(false, Ordering::Release);
    }
}

/// Runs `f` inside the platform's shell context (a COM apartment on Windows,
/// nothing elsewhere).
fn with_shell_com<F, R>(f: F) -> R
where
    F: FnOnce() -> R,
{
    #[cfg(windows)]
    {
        win::with_com(f)
    }
    #[cfg(not(windows))]
    {
        f()
    }
}

/// Extract the icon of `app_path` into `output_png_path`.
///
/// Returns `Ok(true)` once a PNG is in place (an existing valid cache file
/// short-circuits the work), `Ok(false)` when the source has no icon to offer,
/// and `Err` when the platform API or the file system failed. The PNG is
/// published atomically, so a failed extraction never leaves a half-written
/// cache entry behind.
pub fn extract_icon_to_png(app_path: &str, output_png_path: &Path) -> Result<bool, String> {
    if has_png(output_png_path) {
        return Ok(true);
    }
    if !Path::new(app_path).exists() {
        return Ok(false);
    }

    let temporary = temp_sibling(output_png_path);
    match platform_extract(app_path, &temporary) {
        Ok(true) => {
            std::fs::rename(&temporary, output_png_path)
                .map_err(|e| format!("cannot publish icon: {e}"))?;
            Ok(true)
        }
        Ok(false) => {
            let _ = std::fs::remove_file(&temporary);
            Ok(false)
        }
        Err(err) => {
            let _ = std::fs::remove_file(&temporary);
            Err(err)
        }
    }
}

/// 128-bit hash of the application path, hex encoded. Only the path is hashed,
/// so the same application always maps to the same cache entry.
fn cache_key(app_path: &str) -> String {
    use sha2::{Digest, Sha256};
    use std::fmt::Write;

    let digest = Sha256::digest(app_path.as_bytes());
    let mut key = String::with_capacity(32);
    for byte in &digest[..16] {
        let _ = write!(key, "{byte:02x}");
    }
    key
}

/// Whether `path` holds a complete PNG. The magic check doubles as the
/// "non-empty" test and makes a truncated leftover useless.
fn has_png(path: &Path) -> bool {
    use std::io::Read;

    let Ok(mut file) = std::fs::File::open(path) else {
        return false;
    };
    let mut magic = [0u8; 8];
    file.read_exact(&mut magic).is_ok() && magic == PNG_MAGIC
}

/// Unique scratch file next to `path`, so the final rename is a same-volume
/// atomic replace.
fn temp_sibling(path: &Path) -> PathBuf {
    static SEQUENCE: AtomicU64 = AtomicU64::new(0);

    let stem = path.file_stem().and_then(|s| s.to_str()).unwrap_or("icon");
    let seq = SEQUENCE.fetch_add(1, Ordering::Relaxed);
    path.with_file_name(format!(".{stem}.{}.{seq}.tmp", std::process::id()))
}

/// Platform backend: writes a PNG at `output` and reports whether one was
/// produced.
fn platform_extract(app_path: &str, output: &Path) -> Result<bool, String> {
    #[cfg(windows)]
    {
        let image = win::render(app_path)?;
        save_png(&image, output)?;
        Ok(true)
    }
    #[cfg(target_os = "macos")]
    {
        macos::extract(app_path, output)
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        linux::extract(app_path, output)
    }
    #[cfg(not(any(windows, unix)))]
    {
        let _ = (app_path, output);
        Err("icon extraction is not supported on this platform".to_string())
    }
}

#[cfg(windows)]
fn save_png(image: &image::RgbaImage, output: &Path) -> Result<(), String> {
    image
        .save_with_format(output, image::ImageFormat::Png)
        .map_err(|e| format!("cannot encode PNG: {e}"))
}

// ---------------------------------------------------------------------------
// Windows
// ---------------------------------------------------------------------------

#[cfg(windows)]
mod win {
    use std::ffi::{c_void, OsStr};
    use std::mem::size_of;
    use std::os::windows::ffi::OsStrExt;

    use windows::core::PCWSTR;
    use windows::Win32::Foundation::HANDLE;
    use windows::Win32::Graphics::Gdi::{
        BitBlt, CreateCompatibleDC, CreateDIBSection, DeleteDC, DeleteObject, GetObjectW,
        SelectObject, BITMAP, BITMAPINFO, BITMAPINFOHEADER, BI_RGB, DIB_RGB_COLORS, HBRUSH,
        HBITMAP, HDC, HGDIOBJ, SRCCOPY,
    };
    use windows::Win32::Storage::FileSystem::FILE_FLAGS_AND_ATTRIBUTES;
    use windows::Win32::System::Com::{CoInitializeEx, CoUninitialize, COINIT_APARTMENTTHREADED};
    use windows::Win32::UI::Controls::{IImageList, ILD_TRANSPARENT};
    use windows::Win32::UI::Shell::{
        SHGetFileInfoW, SHGetImageList, SHFILEINFOW, SHGFI_ICON, SHGFI_LARGEICON,
        SHGFI_SYSICONINDEX, SHIL_JUMBO,
    };
    use windows::Win32::UI::WindowsAndMessaging::{
        DestroyIcon, DrawIconEx, GetIconInfo, HICON, ICONINFO, DI_NORMAL,
    };

    /// Renders the icon of `app_path` as RGBA pixels.
    pub(super) fn render(app_path: &str) -> Result<image::RgbaImage, String> {
        if app_path.trim().is_empty() {
            return Err("empty application path".to_string());
        }

        let wide: Vec<u16> = OsStr::new(app_path)
            .encode_wide()
            .chain(std::iter::once(0))
            .collect();

        // SAFETY: `wide` is NUL terminated and outlives every call below; all
        // GDI/user32 handles are owned by guards that release them.
        unsafe {
            let _com = ComGuard::new();

            // Preferred: the shell's jumbo image list keeps the largest artwork
            // an application ships (256×256 for anything modern).
            match shell_icon_jumbo(&wide).and_then(|icon| hicon_to_rgba(&icon)) {
                Ok(image) => return Ok(image),
                Err(err) => tracing::debug!(app = app_path, error = %err, "jumbo shell icon unavailable"),
            }

            // Fallback: the classic large (32×32) icon.
            let icon = shell_icon_large(&wide)?;
            hicon_to_rgba(&icon)
        }
    }

    /// Runs `f` inside a shell COM apartment. Extracting a batch of icons
    /// through one apartment avoids paying the shell's COM setup per icon.
    pub(super) fn with_com<F, R>(f: F) -> R
    where
        F: FnOnce() -> R,
    {
        let _com = ComGuard::new();
        f()
    }

    /// `SHGetFileInfoW` + `SHGetImageList(SHIL_JUMBO)`: resolves `.lnk`
    /// shortcuts to their target's icon and yields it at full size.
    unsafe fn shell_icon_jumbo(wide: &[u16]) -> Result<OwnedIcon, String> {
        let mut info = SHFILEINFOW::default();
        let found = SHGetFileInfoW(
            PCWSTR(wide.as_ptr()),
            FILE_FLAGS_AND_ATTRIBUTES(0),
            Some(&mut info),
            size_of::<SHFILEINFOW>() as u32,
            SHGFI_SYSICONINDEX | SHGFI_LARGEICON,
        );
        if found == 0 {
            return Err("SHGetFileInfoW found no shell icon".to_string());
        }
        if info.iIcon < 0 {
            return Err("shell reported an invalid icon index".to_string());
        }

        let list: IImageList =
            SHGetImageList(SHIL_JUMBO as i32).map_err(|e| format!("SHGetImageList: {e}"))?;
        let icon = list
            .GetIcon(info.iIcon, ILD_TRANSPARENT.0)
            .map_err(|e| format!("IImageList::GetIcon: {e}"))?;
        if icon.is_invalid() {
            return Err("jumbo image list returned no icon".to_string());
        }
        Ok(OwnedIcon(icon))
    }

    /// `SHGetFileInfoW` with `SHGFI_ICON | SHGFI_LARGEICON`.
    unsafe fn shell_icon_large(wide: &[u16]) -> Result<OwnedIcon, String> {
        let mut info = SHFILEINFOW::default();
        let found = SHGetFileInfoW(
            PCWSTR(wide.as_ptr()),
            FILE_FLAGS_AND_ATTRIBUTES(0),
            Some(&mut info),
            size_of::<SHFILEINFOW>() as u32,
            SHGFI_ICON | SHGFI_LARGEICON,
        );
        if found == 0 || info.hIcon.is_invalid() {
            return Err("SHGetFileInfoW did not return an icon".to_string());
        }
        Ok(OwnedIcon(info.hIcon))
    }

    /// Draws `icon` into a zeroed, top-down 32-bit DIB section and returns the
    /// pixels as BGRA. A DIB section — rather than a compatible (device
    /// dependent) bitmap — is what preserves the icon's alpha channel.
    unsafe fn hicon_to_rgba(icon: &OwnedIcon) -> Result<image::RgbaImage, String> {
        let mut info = ICONINFO::default();
        GetIconInfo(icon.0, &mut info).map_err(|e| format!("GetIconInfo: {e}"))?;
        let _bitmaps = OwnedBitmaps([info.hbmColor, info.hbmMask]);

        let (width, height) = icon_extent(info.hbmColor, info.hbmMask)?;
        let mut pixels = draw_icon_pixels(icon.0, width, height)?;

        // Icons without an alpha channel (and monochrome icons) come back
        // fully transparent; their shape lives in the AND mask.
        if !pixels.chunks_exact(4).any(|pixel| pixel[3] != 0) {
            apply_mask_alpha(&mut pixels, info.hbmMask, width, height)?;
        }

        let mut image = image::RgbaImage::new(width as u32, height as u32);
        for (bgra, rgba) in pixels.chunks_exact(4).zip(image.pixels_mut()) {
            *rgba = image::Rgba([bgra[2], bgra[1], bgra[0], bgra[3]]);
        }
        Ok(image)
    }

    /// Pixel size of the icon, taken from the colour bitmap when it exists.
    unsafe fn icon_extent(color: HBITMAP, mask: HBITMAP) -> Result<(i32, i32), String> {
        if let Some((width, height)) = bitmap_extent(color) {
            return Ok((width, height));
        }
        // Monochrome icons store the AND mask in the top half of a bitmap with
        // twice the height.
        let (width, height) = bitmap_extent(mask).ok_or("icon has no pixel data")?;
        Ok((width, if height == width * 2 { width } else { height }))
    }

    unsafe fn bitmap_extent(bitmap: HBITMAP) -> Option<(i32, i32)> {
        if bitmap.is_invalid() {
            return None;
        }
        let mut info = BITMAP::default();
        let written = GetObjectW(
            HGDIOBJ::from(bitmap),
            size_of::<BITMAP>() as i32,
            Some(&mut info as *mut BITMAP as *mut c_void),
        );
        if written == 0 || info.bmWidth <= 0 || info.bmHeight <= 0 {
            return None;
        }
        Some((info.bmWidth, info.bmHeight))
    }

    unsafe fn draw_icon_pixels(icon: HICON, width: i32, height: i32) -> Result<Vec<u8>, String> {
        let dc = CreateCompatibleDC(HDC::default());
        if dc.is_invalid() {
            return Err("CreateCompatibleDC failed".to_string());
        }
        let _dc = OwnedDc(dc);

        let (dib, bits) = create_top_down_bitmap(dc, width, height)?;
        let _dib = OwnedBitmap(dib);

        let previous = SelectObject(dc, HGDIOBJ::from(dib));
        let drawn = DrawIconEx(dc, 0, 0, icon, width, height, 0, HBRUSH::default(), DI_NORMAL);
        SelectObject(dc, previous);
        drawn.map_err(|e| format!("DrawIconEx: {e}"))?;

        Ok(pixel_slice(bits, width, height).to_vec())
    }

    /// Rebuilds the alpha channel from the icon's AND mask: a set mask bit
    /// means "transparent".
    unsafe fn apply_mask_alpha(
        pixels: &mut [u8],
        mask: HBITMAP,
        width: i32,
        height: i32,
    ) -> Result<(), String> {
        if mask.is_invalid() {
            return Ok(());
        }

        let target = CreateCompatibleDC(HDC::default());
        if target.is_invalid() {
            return Err("CreateCompatibleDC failed".to_string());
        }
        let _target = OwnedDc(target);
        let source = CreateCompatibleDC(HDC::default());
        if source.is_invalid() {
            return Err("CreateCompatibleDC failed".to_string());
        }
        let _source = OwnedDc(source);

        let (dib, bits) = create_top_down_bitmap(target, width, height)?;
        let _dib = OwnedBitmap(dib);
        let previous_target = SelectObject(target, HGDIOBJ::from(dib));
        let previous_source = SelectObject(source, HGDIOBJ::from(mask));
        // Blitting a 1bpp mask into a 32bpp DIB maps its bits to black/white,
        // which is all the transparency test needs.
        let blitted = BitBlt(target, 0, 0, width, height, source, 0, 0, SRCCOPY);
        SelectObject(source, previous_source);
        SelectObject(target, previous_target);
        blitted.map_err(|e| format!("BitBlt: {e}"))?;

        let mask_pixels = pixel_slice(bits, width, height);
        for (pixel, mask) in pixels
            .chunks_exact_mut(4)
            .zip(mask_pixels.chunks_exact(4))
        {
            pixel[3] = if mask[0] != 0 || mask[1] != 0 || mask[2] != 0 {
                0
            } else {
                255
            };
        }
        Ok(())
    }

    /// Creates a zeroed, top-down 32bpp DIB section and returns it with its
    /// pixel buffer.
    unsafe fn create_top_down_bitmap(
        dc: HDC,
        width: i32,
        height: i32,
    ) -> Result<(HBITMAP, *mut u8), String> {
        let mut info = BITMAPINFO::default();
        info.bmiHeader.biSize = size_of::<BITMAPINFOHEADER>() as u32;
        info.bmiHeader.biWidth = width;
        info.bmiHeader.biHeight = -height; // negative height: top-down rows
        info.bmiHeader.biPlanes = 1;
        info.bmiHeader.biBitCount = 32;
        info.bmiHeader.biCompression = BI_RGB.0;

        let mut bits: *mut c_void = std::ptr::null_mut();
        let dib = CreateDIBSection(dc, &info, DIB_RGB_COLORS, &mut bits, HANDLE::default(), 0)
            .map_err(|e| format!("CreateDIBSection: {e}"))?;
        if dib.is_invalid() || bits.is_null() {
            return Err("CreateDIBSection returned no pixels".to_string());
        }

        let bits = bits as *mut u8;
        std::ptr::write_bytes(bits, 0, (width as usize) * (height as usize) * 4);
        Ok((dib, bits))
    }

    unsafe fn pixel_slice<'a>(bits: *mut u8, width: i32, height: i32) -> &'a [u8] {
        std::slice::from_raw_parts(bits, (width as usize) * (height as usize) * 4)
    }

    /// Keeps COM initialised on the calling thread for as long as the shell
    /// image list is in use.
    struct ComGuard(bool);

    impl ComGuard {
        fn new() -> Self {
            // SAFETY: paired with `CoUninitialize` in `Drop` when the call
            // succeeded (S_FALSE means "already initialised here" and still
            // needs the matching uninitialise).
            let result = unsafe { CoInitializeEx(None, COINIT_APARTMENTTHREADED) };
            Self(result.is_ok())
        }
    }

    impl Drop for ComGuard {
        fn drop(&mut self) {
            if self.0 {
                unsafe { CoUninitialize() };
            }
        }
    }

    /// Owns a shell `HICON`.
    struct OwnedIcon(HICON);

    impl Drop for OwnedIcon {
        fn drop(&mut self) {
            unsafe { let _ = DestroyIcon(self.0); }
        }
    }

    /// Owns the colour and mask bitmaps handed out by `GetIconInfo`.
    struct OwnedBitmaps([HBITMAP; 2]);

    impl Drop for OwnedBitmaps {
        fn drop(&mut self) {
            for bitmap in self.0 {
                if !bitmap.is_invalid() {
                    unsafe { let _ = DeleteObject(HGDIOBJ::from(bitmap)); }
                }
            }
        }
    }

    struct OwnedBitmap(HBITMAP);

    impl Drop for OwnedBitmap {
        fn drop(&mut self) {
            if !self.0.is_invalid() {
                unsafe { let _ = DeleteObject(HGDIOBJ::from(self.0)); }
            }
        }
    }

    struct OwnedDc(HDC);

    impl Drop for OwnedDc {
        fn drop(&mut self) {
            unsafe { let _ = DeleteDC(self.0); }
        }
    }
}

// ---------------------------------------------------------------------------
// macOS
// ---------------------------------------------------------------------------

#[cfg(target_os = "macos")]
mod macos {
    use std::ffi::OsStr;
    use std::path::{Path, PathBuf};

    /// `.app` bundles are iconised through their `.icns`, which `sips` converts
    /// to PNG.
    pub(super) fn extract(app_path: &str, output: &Path) -> Result<bool, String> {
        let Some(source) = icon_source(app_path) else {
            return Ok(false);
        };
        if is_extension(&source, "png") {
            std::fs::copy(&source, output).map_err(|e| format!("cannot copy icon: {e}"))?;
            return Ok(true);
        }

        let status = std::process::Command::new("sips")
            .args(["-s", "format", "png"])
            .arg(&source)
            .arg("--out")
            .arg(output)
            .status()
            .map_err(|e| format!("cannot run sips: {e}"))?;
        if !status.success() {
            return Err(format!("sips failed to convert {}", source.display()));
        }
        Ok(true)
    }

    /// Icon file backing an application path: either the `.app` bundle itself,
    /// an executable inside one, or a plain image.
    fn icon_source(app_path: &str) -> Option<PathBuf> {
        let path = PathBuf::from(app_path);
        if let Some(bundle) = bundle_of(&path) {
            return bundle_icon(&bundle);
        }
        path.is_file().then_some(path)
    }

    /// Nearest ancestor (including `path` itself) that is an `.app` bundle.
    fn bundle_of(path: &Path) -> Option<PathBuf> {
        path.ancestors()
            .find(|ancestor| is_extension(ancestor, "app"))
            .map(Path::to_path_buf)
    }

    fn bundle_icon(bundle: &Path) -> Option<PathBuf> {
        let contents = bundle.join("Contents");
        let resources = contents.join("Resources");

        let mut candidates = Vec::new();
        if let Some(name) = plist_icon_name(&contents.join("Info.plist")) {
            candidates.push(name);
        }
        // `CFBundleIconFile` may be omitted or point at an asset catalog entry,
        // in which case the icon is usually named after the bundle itself.
        if let Some(stem) = bundle.file_stem().and_then(OsStr::to_str) {
            candidates.push(stem.to_string());
        }
        candidates.push("AppIcon".to_string());

        for name in candidates {
            let mut file = name;
            if !file.to_ascii_lowercase().ends_with(".icns") {
                file.push_str(".icns");
            }
            let candidate = resources.join(&file);
            if candidate.is_file() {
                return Some(candidate);
            }
        }

        // Last resort: whatever icon the bundle ships.
        std::fs::read_dir(&resources)
            .ok()?
            .flatten()
            .map(|entry| entry.path())
            .find(|path| is_extension(path, "icns"))
    }

    /// `CFBundleIconFile` (or `CFBundleIconName`) from an `Info.plist`, which is
    /// usually a binary plist and therefore handed to `plutil` for conversion.
    fn plist_icon_name(plist: &Path) -> Option<String> {
        let raw = std::fs::read(plist).ok()?;
        let xml = if raw.starts_with(b"bplist00") {
            let output = std::process::Command::new("plutil")
                .args(["-convert", "xml1", "-o", "-"])
                .arg(plist)
                .output()
                .ok()?;
            if !output.status.success() {
                return None;
            }
            String::from_utf8_lossy(&output.stdout).into_owned()
        } else {
            String::from_utf8_lossy(&raw).into_owned()
        };

        ["CFBundleIconFile", "CFBundleIconName"]
            .iter()
            .find_map(|key| plist_string(&xml, key).filter(|value| !value.is_empty()))
    }

    fn plist_string(xml: &str, key: &str) -> Option<String> {
        let needle = format!("<key>{key}</key>");
        let after_key = &xml[xml.find(&needle)? + needle.len()..];
        let start = after_key.find("<string>")? + "<string>".len();
        let end = after_key[start..].find("</string>")?;
        Some(after_key[start..start + end].trim().to_string())
    }

    fn is_extension(path: &Path, expected: &str) -> bool {
        path.extension()
            .and_then(OsStr::to_str)
            .map(|ext| ext.eq_ignore_ascii_case(expected))
            .unwrap_or(false)
    }
}

// ---------------------------------------------------------------------------
// Linux
// ---------------------------------------------------------------------------

#[cfg(all(unix, not(target_os = "macos")))]
mod linux {
    use std::ffi::OsStr;
    use std::path::{Path, PathBuf};
    use std::process::Command;

    /// Icon directories, largest first, for theme-less (`hicolor`) lookups.
    const HICOLOR_SIZES: [&str; 6] = ["512x512", "256x256", "128x128", "96x96", "64x64", "48x48"];

    pub(super) fn extract(app_path: &str, output: &Path) -> Result<bool, String> {
        let Some(source) = icon_source(app_path) else {
            return Ok(false);
        };

        if is_extension(&source, "png") {
            std::fs::copy(&source, output).map_err(|e| format!("cannot copy icon: {e}"))?;
            return Ok(true);
        }
        if is_extension(&source, "svg") || is_extension(&source, "xpm") {
            return rasterize(&source, output).map(|()| true);
        }
        Err(format!(
            "unsupported icon format: {}",
            source.display()
        ))
    }

    /// Icon file for an application path: a `.desktop` entry is parsed for its
    /// `Icon=` value, anything else is treated as an icon name or file.
    fn icon_source(app_path: &str) -> Option<PathBuf> {
        let path = Path::new(app_path);

        let icon = if is_extension(path, "desktop") {
            let named = desktop_icon_name(path).or_else(|| {
                path.file_stem()
                    .and_then(OsStr::to_str)
                    .map(str::to_string)
            })?;
            named
        } else {
            app_path.trim().to_string()
        };
        if icon.is_empty() {
            return None;
        }

        let direct = PathBuf::from(&icon);
        if direct.is_absolute() {
            return direct.is_file().then_some(direct);
        }
        if app_path.contains('/') {
            let relative = PathBuf::from(app_path);
            if relative.is_file() {
                return Some(relative);
            }
        }
        resolve_icon_name(&icon)
    }

    /// The `Icon=` entry of a `.desktop` file (preferring the `[Desktop Entry]`
    /// group, as stray localised groups also carry `Icon=` lines).
    fn desktop_icon_name(entry: &Path) -> Option<String> {
        let text = std::fs::read_to_string(entry).ok()?;
        let mut in_entry_group = false;
        let mut fallback = None;

        for line in text.lines() {
            let line = line.trim();
            if line.starts_with('[') {
                in_entry_group = line == "[Desktop Entry]";
                continue;
            }
            let Some(value) = line.strip_prefix("Icon=") else {
                continue;
            };
            let value = value.trim().to_string();
            if in_entry_group {
                return Some(value);
            }
            fallback.get_or_insert(value);
        }
        fallback
    }

    /// Looks an icon *name* up in the standard icon directories.
    fn resolve_icon_name(name: &str) -> Option<PathBuf> {
        let mut hicolor_roots = vec![
            PathBuf::from("/usr/share/icons/hicolor"),
            PathBuf::from("/usr/local/share/icons/hicolor"),
        ];
        let mut pixmap_dirs = vec![
            PathBuf::from("/usr/share/pixmaps"),
            PathBuf::from("/usr/local/share/pixmaps"),
        ];
        if let Some(home) = std::env::var_os("HOME") {
            let home = PathBuf::from(home);
            hicolor_roots.insert(0, home.join(".local/share/icons/hicolor"));
            pixmap_dirs.insert(0, home.join(".local/share/pixmaps"));
        }

        for root in &hicolor_roots {
            for size in HICOLOR_SIZES {
                let candidate = root.join(size).join("apps").join(format!("{name}.png"));
                if candidate.is_file() {
                    return Some(candidate);
                }
            }
            let scalable = root.join("scalable/apps").join(format!("{name}.svg"));
            if scalable.is_file() {
                return Some(scalable);
            }
        }

        for dir in &pixmap_dirs {
            for extension in ["png", "svg", "xpm"] {
                let candidate = dir.join(format!("{name}.{extension}"));
                if candidate.is_file() {
                    return Some(candidate);
                }
            }
        }
        None
    }

    /// SVG/XPM cannot be decoded by the `image` crate, so shell out to whichever
    /// rasterizer is installed.
    fn rasterize(source: &Path, output: &Path) -> Result<(), String> {
        let attempts = [
            {
                let mut command = Command::new("rsvg-convert");
                command.args(["-w", "256", "-h", "256", "-o"]).arg(output).arg(source);
                command
            },
            {
                let mut command = Command::new("magick");
                command
                    .args(["-background", "none"])
                    .arg(source)
                    .args(["-resize", "256x256"])
                    .arg(output);
                command
            },
            {
                let mut command = Command::new("convert");
                command
                    .args(["-background", "none"])
                    .arg(source)
                    .args(["-resize", "256x256"])
                    .arg(output);
                command
            },
            {
                let mut command = Command::new("inkscape");
                command
                    .arg(source)
                    .args(["--export-type=png", "--export-filename"])
                    .arg(output);
                command
            },
        ];

        let mut failures = Vec::new();
        for mut command in attempts {
            let program = command.get_program().to_string_lossy().into_owned();
            match command.status() {
                Ok(status) if status.success() && super::has_png(output) => return Ok(()),
                Ok(status) => failures.push(format!("{program} exited with {status}")),
                Err(err) if err.kind() == std::io::ErrorKind::NotFound => continue,
                Err(err) => failures.push(format!("{program}: {err}")),
            }
        }
        Err(format!(
            "no SVG rasterizer available for {}: {}",
            source.display(),
            failures.join(", ")
        ))
    }

    fn is_extension(path: &Path, expected: &str) -> bool {
        path.extension()
            .and_then(OsStr::to_str)
            .map(|ext| ext.eq_ignore_ascii_case(expected))
            .unwrap_or(false)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn scratch_dir(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("bigpocket-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        dir
    }

    #[test]
    fn cache_path_is_keyed_by_application() {
        let dir = scratch_dir("icon-paths");
        let relative = icon_cache_rel_path(r"C:\App\one.exe");
        let key = relative
            .strip_prefix("icons/app_")
            .and_then(|name| name.strip_suffix(".png"))
            .expect("cache file name shape");
        assert_eq!(key.len(), 32);
        assert!(key.chars().all(|c| c.is_ascii_hexdigit()), "{key}");

        // Deterministic per path, different across paths.
        assert_eq!(relative, icon_cache_rel_path(r"C:\App\one.exe"));
        assert_ne!(relative, icon_cache_rel_path(r"C:\App\two.exe"));
        assert_eq!(icon_cache_path(r"C:\App\one.exe", &dir), dir.join(&relative));
        assert!(cached_icon(r"C:\App\one.exe", &dir).is_none());
    }

    #[test]
    fn missing_application_reports_no_icon() {
        let dir = scratch_dir("icon-missing");
        let missing = dir.join("does-not-exist.exe");
        let target = dir.join("out.png");
        assert_eq!(
            extract_icon_to_png(&missing.to_string_lossy(), &target),
            Ok(false)
        );
        assert!(!target.exists());
    }

    #[cfg(windows)]
    #[test]
    fn extracts_a_real_windows_icon() {
        let source = r"C:\Windows\System32\notepad.exe";
        if !Path::new(source).exists() {
            return; // not every Windows installation ships notepad.exe
        }

        let dir = scratch_dir("icon-windows");
        let relative = get_or_extract_icon(source, &dir).expect("notepad icon");
        assert_eq!(relative, icon_cache_rel_path(source));

        let cached = dir.join(&relative);
        let image = image::open(&cached).expect("valid PNG");
        assert!(image.width() >= 32 && image.height() >= 32, "{image:?}");
        assert_eq!(image.width(), image.height());

        // An icon that lost its alpha channel comes back either entirely opaque
        // or entirely transparent; real artwork has both.
        let pixels = image.to_rgba8();
        let transparent = pixels.pixels().filter(|p| p.0[3] == 0).count();
        let opaque = pixels.pixels().filter(|p| p.0[3] == 255).count();
        assert!(transparent > 0, "icon lost its transparency");
        assert!(opaque > 0, "icon is entirely transparent");

        // The second call must come from the cache: identical bytes, no rewrite.
        let before = std::fs::metadata(&cached).unwrap().modified().unwrap();
        assert_eq!(get_or_extract_icon(source, &dir).as_deref(), Some(relative.as_str()));
        assert_eq!(
            std::fs::metadata(&cached).unwrap().modified().unwrap(),
            before
        );
    }

    #[test]
    fn batch_extraction_matches_individual_extraction() {
        let dir = scratch_dir("icon-batch");
        let missing = dir.join("nope.exe").to_string_lossy().into_owned();
        let paths = vec![missing, String::new()];
        assert_eq!(get_or_extract_icons(&paths, &dir), vec![None, None]);
    }
}
