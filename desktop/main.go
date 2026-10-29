package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/atotto/clipboard"
	"github.com/gorilla/websocket"
	"github.com/itchyny/volume-go"
	"github.com/jchv/go-webview2"
	"github.com/kbinani/screenshot"
)

// Global States & Configurations
var (
	exePath, _        = os.Executable()
	exeDir            = filepath.Dir(exePath)
	frontendDir       = resolveFrontendDir()
	appDataDir        = filepath.Join(os.Getenv("APPDATA"), "BigPocket")
	sharedDir         = filepath.Join(appDataDir, "SharedFiles")
	configFile        = filepath.Join(appDataDir, "config.json")
	driverDir         = filepath.Join(exeDir, "usbmmidd")
	latestCameraFrame []byte
	latestCameraLock  sync.RWMutex
	lastClipboardText string
	upgrader          = websocket.Upgrader{
		// Drive-by RCE protection: browsers always send Origin for WebSocket
		// handshakes, so anything that is not the local UI is rejected.
		CheckOrigin: isAllowedOrigin,
	}
)

func resolveFrontendDir() string {
	p1 := filepath.Join(exeDir, "frontend")
	if fi, err := os.Stat(p1); err == nil && fi.IsDir() {
		return p1
	}
	p2 := filepath.Join(exeDir, "desktop", "frontend")
	if fi, err := os.Stat(p2); err == nil && fi.IsDir() {
		return p2
	}
	return p1
}

// gridAutoPlacement is the GridRow/GridCol sentinel for "no explicit cell": the
// bento packer drops such a card into the first free slot instead of a fixed one.
const gridAutoPlacement = -1

// StreamDeckButton is one slot of a page. GridRow/GridCol pin the card to an
// explicit 0-based bento cell; any negative value means automatic placement.
type StreamDeckButton struct {
	ID            int    `json:"id"`
	Label         string `json:"label"`
	Type          string `json:"type"`                      // hotkey, command, volume_slider, toggle, live_info, clock_widget, cpu_widget, ram_widget
	Value         string `json:"value"`                     // keybind, script, volume_slider, mic_mute, sound_mute, cpu, ram
	Icon          string `json:"icon"`                      // Custom SVG path or local image
	Page          int    `json:"page"`                      // 0, 1, 2, ...
	State         bool   `json:"state"`                     // on/off for toggle buttons
	Color         string `json:"color"`                     // optional accent override; "" = monochrome default surface
	RowSpan       int    `json:"row_span"`                  // cell height span (default 1)
	ColSpan       int    `json:"col_span"`                  // cell width span (default 1)
	GridRow       int    `json:"grid_row"`                  // explicit 0-based bento row (-1 = auto placement)
	GridCol       int    `json:"grid_col"`                  // explicit 0-based bento column (-1 = auto placement)
	BgStyle       string `json:"bg_style,omitempty"`        // default, transparent, glass, outline
	ClockStyle    string `json:"clock_style,omitempty"`     // analog, digital_clean, digital_seconds, digital_date
	FontFamily    string `json:"font_family,omitempty"`     // mono, sans, lcd, serif
	ClockFontSize string `json:"clock_font_size,omitempty"` // auto, compact, regular, large, huge

	// Set by UnmarshalJSON only: distinguishes "grid_row absent from the payload"
	// (a legacy config or a partial client save) from "grid_row sent explicitly",
	// which mergeConfig needs to keep a saved layout instead of resetting it.
	gridRowSet bool
	gridColSet bool
}

// UnmarshalJSON keeps the grid sentinel honest. A payload that never mentions
// grid_row/grid_col (a config written before free cell placement, a partial
// mobile save) would otherwise decode to the zero value — and (0, 0) is a real
// cell, so every legacy button would pile onto the top-left slot.
func (b *StreamDeckButton) UnmarshalJSON(data []byte) error {
	type plainButton StreamDeckButton
	aux := struct {
		GridRow *int `json:"grid_row"`
		GridCol *int `json:"grid_col"`
		*plainButton
	}{plainButton: (*plainButton)(b)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	b.GridRow = gridAutoPlacement
	if aux.GridRow != nil {
		b.GridRow = *aux.GridRow
	}
	b.gridRowSet = aux.GridRow != nil
	b.GridCol = gridAutoPlacement
	if aux.GridCol != nil {
		b.GridCol = *aux.GridCol
	}
	b.gridColSet = aux.GridCol != nil
	return nil
}

type StreamDeckPage struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// AutoSwitchRule maps a foreground process to a Stream Deck page, so the right
// page is shown automatically when the user switches applications.
type AutoSwitchRule struct {
	ProcessName string `json:"process_name"` // e.g. "chrome.exe"
	PageID      int    `json:"page_id"`
}

type Config struct {
	Theme             string             `json:"theme"`
	Password          string             `json:"password"`
	ActivePage        int                `json:"active_page"`
	WeatherCity       string             `json:"weather_city,omitempty"` // default city for the live weather widget
	StreamDeckPages   []StreamDeckPage   `json:"stream_deck_pages"`
	StreamDeckRows    int                `json:"stream_deck_rows"`
	StreamDeckCols    int                `json:"stream_deck_cols"`
	StreamDeckButtons []StreamDeckButton `json:"stream_deck_buttons"`
	AutoSwitchRules   []AutoSwitchRule   `json:"auto_switch_rules,omitempty"`
	BetaChannel       bool               `json:"beta_channel,omitempty"`
	Rev               int                `json:"rev,omitempty"`
}

// clone deep-copies the slices so callers can mutate a config without touching the cache.
func (c Config) clone() Config {
	out := c
	out.StreamDeckButtons = append([]StreamDeckButton(nil), c.StreamDeckButtons...)
	out.StreamDeckPages = append([]StreamDeckPage(nil), c.StreamDeckPages...)
	out.AutoSwitchRules = append([]AutoSwitchRule(nil), c.AutoSwitchRules...)
	return out
}

var defaultConfig = Config{
	Theme:          "pro",
	Password:       "", // Default is no password
	ActivePage:     0,
	WeatherCity:    defaultWeatherCity,
	StreamDeckRows: 2,
	StreamDeckCols: 4,
	StreamDeckPages: []StreamDeckPage{
		{ID: 0, Title: "Ana Sayfa"},
		{ID: 1, Title: "Medya & Ses"},
		{ID: 2, Title: "Sistem & PC"},
	},
	StreamDeckButtons: []StreamDeckButton{
		// Page 0: Ana Sayfa — 4x2 Bento grid, every card on an explicit cell
		// (GridRow/GridCol). Color is left empty so every widget renders with the
		// shared monochrome surface (#F4F4F5 default theme).
		// Row 0: Saat & Tarih (2x1) + Hava Durumu + PC Sıcaklık
		// Row 1: Ana Ses + Şimdi Çalıyor (2x1) + Mikrofon
		{ID: 0, Label: "Saat & Tarih", Type: "clock_widget", Value: "digital_clock", Icon: "clock", Page: 0, State: false, Color: "", ColSpan: 2, RowSpan: 1, GridRow: 0, GridCol: 0, ClockStyle: "flip_clock"},
		{ID: 1, Label: "Hava Durumu", Type: "weather", Value: defaultWeatherCity, Icon: "bright", Page: 0, State: false, Color: "", ColSpan: 1, RowSpan: 1, GridRow: 0, GridCol: 2},
		{ID: 2, Label: "PC Sıcaklık", Type: "live_info", Value: "cpu_temp", Icon: "cpu", Page: 0, State: false, Color: "", ColSpan: 1, RowSpan: 1, GridRow: 0, GridCol: 3},
		{ID: 3, Label: "Ana Ses", Type: "volume_slider", Value: "master_volume", Icon: "volume", Page: 0, State: false, Color: "", ColSpan: 1, RowSpan: 1, GridRow: 1, GridCol: 0},
		{ID: 4, Label: "Şimdi Çalıyor", Type: "now_playing", Value: "media_session", Icon: "music", Page: 0, State: false, Color: "", ColSpan: 2, RowSpan: 1, GridRow: 1, GridCol: 1},
		{ID: 5, Label: "Mikrofon", Type: "toggle", Value: "mic_mute", Icon: "mic", Page: 0, State: true, Color: "", ColSpan: 1, RowSpan: 1, GridRow: 1, GridCol: 3},

		// Page 1: Medya & Ses — 4x2, slot i = ID-8 → GridRow i/4, GridCol i%4.
		// Monochrome surface for every card (empty Color).
		{ID: 8, Label: "Ses +", Type: "hotkey", Value: "volumeup", Icon: "volume_up", Page: 1, State: false, Color: "", GridRow: 0, GridCol: 0},
		{ID: 9, Label: "Ses -", Type: "hotkey", Value: "volumedown", Icon: "volume_down", Page: 1, State: false, Color: "", GridRow: 0, GridCol: 1},
		{ID: 10, Label: "Sesi Kapat", Type: "toggle", Value: "sound_mute", Icon: "volume_mute", Page: 1, State: false, Color: "", GridRow: 0, GridCol: 2},
		{ID: 11, Label: "Mikrofon", Type: "toggle", Value: "mic_mute", Icon: "mic", Page: 1, State: true, Color: "", GridRow: 0, GridCol: 3},
		{ID: 12, Label: "Önceki", Type: "hotkey", Value: "prevtrack", Icon: "prev", Page: 1, State: false, Color: "", GridRow: 1, GridCol: 0},
		{ID: 13, Label: "Oynat", Type: "hotkey", Value: "playpause", Icon: "play", Page: 1, State: false, Color: "", GridRow: 1, GridCol: 1},
		{ID: 14, Label: "Sonraki", Type: "hotkey", Value: "nexttrack", Icon: "skip", Page: 1, State: false, Color: "", GridRow: 1, GridCol: 2},
		{ID: 15, Label: "Spotify", Type: "command", Value: "cmd /c start spotify:", Icon: "spotify", Page: 1, State: false, Color: "", GridRow: 1, GridCol: 3},

		// Page 2: Sistem & PC — 4x2, slot i = ID-16 → GridRow i/4, GridCol i%4.
		// Monochrome surface for every card (empty Color).
		{ID: 16, Label: "Görev Yöneticisi", Type: "command", Value: "taskmgr.exe", Icon: "taskmgr", Page: 2, State: false, Color: "", GridRow: 0, GridCol: 0},
		{ID: 17, Label: "PC Kilitle", Type: "command", Value: "rundll32.exe user32.dll,LockWorkStation", Icon: "lock", Page: 2, State: false, Color: "", GridRow: 0, GridCol: 1},
		{ID: 18, Label: "2. Ekran", Type: "toggle", Value: "virtual_monitor", Icon: "monitor", Page: 2, State: false, Color: "", GridRow: 0, GridCol: 2},
		{ID: 19, Label: "Ekran Görüntüsü", Type: "hotkey", Value: "printscreen", Icon: "camera", Page: 2, State: false, Color: "", GridRow: 0, GridCol: 3},
		{ID: 20, Label: "CPU Yükü", Type: "live_info", Value: "cpu", Icon: "cpu", Page: 2, State: false, Color: "", GridRow: 1, GridCol: 0},
		{ID: 21, Label: "RAM Yükü", Type: "live_info", Value: "ram", Icon: "ram", Page: 2, State: false, Color: "", GridRow: 1, GridCol: 1},
		{ID: 22, Label: "Terminal", Type: "command", Value: "wt.exe", Icon: "terminal", Page: 2, State: false, Color: "", GridRow: 1, GridCol: 2},
		{ID: 23, Label: "Masaüstü", Type: "hotkey", Value: "win+d", Icon: "home", Page: 2, State: false, Color: "", GridRow: 1, GridCol: 3},
	},
}

func main() {
	runtime.LockOSThread()
	defer func() {
		if rec := recover(); rec != nil {
			logPanic("main", rec)
		}
	}()
	// Remove leftovers from a previous self-update (and wait for old process if restarted)
	cleanupOldUpdate()

	// Ensure directories exist
	os.MkdirAll(appDataDir, 0755)
	os.MkdirAll(sharedDir, 0755)

	// Create desktop shortcut automatically if it doesn't exist
	createDesktopShortcut()

	// Intercept terminate signals for graceful virtual monitor cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cleanupOnExit()
		os.Exit(0)
	}()

	// Check for single instance to prevent duplicate port collision
	checkSingleInstance()

	// Load or create config
	loadConfig()

	// Start servers in goroutines
	go startCameraServer()
	go startAudioServer()
	go startScreenStreamServer()
	go startWebSocketServer()
	go startClipboardPolling()
	go startDiscovery()
	go startAutoSwitchWatcher()

	// Handle HTTP Routing
	setupHttpRoutes()
	setupUpdateRoutes()

	// Start HTTP Server in background goroutine
	go func() {
		fmt.Println("BigPocket Backend is running on port 8085...")
		err := http.ListenAndServe(":8085", originGuard(http.DefaultServeMux))
		if err != nil {
			fmt.Println("HTTP server error:", err)
		}
	}()

	// Let the server spin up, then open Native WebView2 window
	time.Sleep(300 * time.Millisecond)

	profileDir := filepath.Join(appDataDir, "webview_profile")
	os.MkdirAll(profileDir, 0755)

	var w webview2.WebView
	func() {
		defer func() {
			if r := recover(); r != nil {
				logPanic("webview2_init", r)
				w = nil
			}
		}()
		w = webview2.NewWithOptions(webview2.WebViewOptions{
			DataPath: profileDir,
			WindowOptions: webview2.WindowOptions{
				Title:  "BigPocket",
				Width:  1240,
				Height: 800,
				Center: true,
			},
		})
	}()

	if w != nil {
		defer w.Destroy()
		w.SetTitle("BigPocket")
		w.SetSize(1240, 800, webview2.HintNone)
		hwnd := uintptr(w.Window())
		setWindowIcon(hwnd)
		if hwnd != 0 {
			user32 := syscall.NewLazyDLL("user32.dll")
			showWindow := user32.NewProc("ShowWindow")
			setForeground := user32.NewProc("SetForegroundWindow")
			showWindow.Call(hwnd, 1) // SW_SHOWNORMAL
			setForeground.Call(hwnd)
		}
		// Notification-area icon + menu, and close-to-tray behaviour.
		initSystemTray(hwnd)
		installCloseToTray(hwnd)
		w.Navigate("http://127.0.0.1:8085")
		w.Run()

		// The window close button hides the app into the tray (see installCloseToTray);
		// reaching this point means the process is really shutting down.
		fmt.Println("WebView2 window closed. Exiting server...")
		cleanupOnExit()
		os.Exit(0)
	} else {
		// Fallback to default browser using native Win32 ShellExecuteW
		fmt.Println("WebView2 runtime not available, falling back to default browser...")
		initSystemTray(0)
		_ = openURLNative("http://127.0.0.1:8085")

		// Keep the process alive
		select {}
	}
}

// ----------------- Single Instance Guard -----------------
var singleInstanceMutex uintptr

func checkSingleInstance() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createMutex := kernel32.NewProc("CreateMutexW")
	mutexName, _ := syscall.UTF16PtrFromString("Local\\BigPocket_SingleInstance_Mutex")
	hMutex, _, err := createMutex.Call(0, 0, uintptr(unsafe.Pointer(mutexName)))
	const ERROR_ALREADY_EXISTS = syscall.Errno(183)
	if hMutex == 0 || err == ERROR_ALREADY_EXISTS {
		// Existing instance is running. Bring its window to foreground
		user32 := syscall.NewLazyDLL("user32.dll")
		findWindow := user32.NewProc("FindWindowW")
		showWindow := user32.NewProc("ShowWindow")
		setForeground := user32.NewProc("SetForegroundWindow")
		wName, _ := syscall.UTF16PtrFromString("BigPocket")
		existingHwnd, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(wName)))
		if existingHwnd != 0 {
			showWindow.Call(existingHwnd, 9) // SW_RESTORE
			setForeground.Call(existingHwnd)
		} else {
			// Window not visible, open in browser
			shell32 := syscall.NewLazyDLL("shell32.dll")
			shellExecute := shell32.NewProc("ShellExecuteW")
			verbPtr, _ := syscall.UTF16PtrFromString("open")
			urlPtr, _ := syscall.UTF16PtrFromString("http://127.0.0.1:8085")
			shellExecute.Call(0, uintptr(unsafe.Pointer(verbPtr)), uintptr(unsafe.Pointer(urlPtr)), 0, 0, 1)
		}
		os.Exit(0)
	}
	singleInstanceMutex = hMutex
}

// ----------------- Self-Elevation Helper -----------------
func ensureAdmin() {
	if checkIfAdmin() {
		return
	}

	for _, a := range os.Args[1:] {
		if a == "--elevated" {
			return
		}
	}

	verbPtr, _ := syscall.UTF16PtrFromString("runas")
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exePtr, _ := syscall.UTF16PtrFromString(exe)
	cwd, _ := os.Getwd()
	cwdPtr, _ := syscall.UTF16PtrFromString(cwd)

	args := strings.Join(append(os.Args[1:], "--elevated"), " ")
	argsPtr, _ := syscall.UTF16PtrFromString(args)

	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecute := shell32.NewProc("ShellExecuteW")

	ret, _, _ := shellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(exePtr)),
		uintptr(unsafe.Pointer(argsPtr)),
		uintptr(unsafe.Pointer(cwdPtr)),
		1, // SW_SHOWNORMAL
	)

	if ret > 32 {
		os.Exit(0)
	}
}

// Set native Win32 window and titlebar icon
func setWindowIcon(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	sendMessage := user32.NewProc("SendMessageW")
	loadImage := user32.NewProc("LoadImageW")

	var hIcon uintptr
	// 1. Try to load logo.ico directly from filesystem next to executable
	icoPath := filepath.Join(exeDir, "logo.ico")
	if _, err := os.Stat(icoPath); err == nil {
		icoPathUTF16, _ := syscall.UTF16PtrFromString(icoPath)
		// IMAGE_ICON = 1, LR_LOADFROMFILE = 0x00000010, LR_DEFAULTSIZE = 0x00000040
		ret, _, _ := loadImage.Call(
			0,
			uintptr(unsafe.Pointer(icoPathUTF16)),
			1, // IMAGE_ICON
			0, // cx
			0, // cy
			0x00000010|0x00000040,
		)
		hIcon = ret
	}

	// 2. Fallback to embedded resource icon
	if hIcon == 0 {
		loadIcon := user32.NewProc("LoadIconW")
		kernel32 := syscall.NewLazyDLL("kernel32.dll")
		getModuleHandle := kernel32.NewProc("GetModuleHandleW")
		hInst, _, _ := getModuleHandle.Call(0)
		ret, _, _ := loadIcon.Call(hInst, uintptr(1))
		if ret == 0 {
			appStr, _ := syscall.UTF16PtrFromString("APP")
			ret, _, _ = loadIcon.Call(hInst, uintptr(unsafe.Pointer(appStr)))
		}
		hIcon = ret
	}

	if hIcon != 0 {
		// WM_SETICON = 0x0080, ICON_SMALL = 0, ICON_BIG = 1
		sendMessage.Call(hwnd, 0x0080, 0, hIcon)
		sendMessage.Call(hwnd, 0x0080, 1, hIcon)
		// Update window class icon for taskbar: GCLP_HICON = -14, GCLP_HICONSM = -34
		setClassLongPtr := user32.NewProc("SetClassLongPtrW")
		if setClassLongPtr.Find() == nil {
			setClassLongPtr.Call(hwnd, uintptr(^uint(13)), hIcon)
			setClassLongPtr.Call(hwnd, uintptr(^uint(33)), hIcon)
		}
	}
}

// ----------------- Desktop Shortcut Helper -----------------
func createDesktopShortcut() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	shortcutPath := filepath.Join(homeDir, "Desktop", "BigPocket.lnk")
	if _, err := os.Stat(shortcutPath); !os.IsNotExist(err) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exeFolder := filepath.Dir(exe)
	iconLocation := exe
	if logoPath := filepath.Join(exeFolder, "logo.ico"); fileExists(logoPath) {
		iconLocation = logoPath
	}

	// Native Shell Link COM object: previously this shelled out to PowerShell.
	if err := createDesktopShortcutNative(shortcutPath, exe, exeFolder, iconLocation); err != nil {
		fmt.Println("Desktop shortcut creation failed:", err)
		return
	}
	fmt.Println("Desktop shortcut created automatically:", shortcutPath)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// isAllowedIconExtension whitelists the image extensions accepted by /upload_icon.
func isAllowedIconExtension(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".ico":
		return true
	}
	return false
}

// ----------------- Cleanup Resources & Exit Handlers -----------------
func initExitHandlers() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setConsoleCtrlHandler := kernel32.NewProc("SetConsoleCtrlHandler")
	callback := syscall.NewCallback(func(controlType uint32) uintptr {
		cleanupOnExit()
		return 0
	})
	setConsoleCtrlHandler.Call(callback, 1)
}

func cleanupOnExit() {
	fmt.Println("Closing virtual monitor and cleaning up on exit...")
	trayRemoveIcon()
	setVirtualMonitor(false)
}

// ----------------- Win32 Volume Controller (Core Audio API) -----------------
func adjustMasterVolume(action string, level float64) (int, error) {
	switch action {
	case "up":
		cur, err := volume.GetVolume()
		if err == nil {
			target := cur + 5
			if target > 100 {
				target = 100
			}
			_ = volume.SetVolume(target)
			return target, nil
		}
		return -1, err
	case "down":
		cur, err := volume.GetVolume()
		if err == nil {
			target := cur - 5
			if target < 0 {
				target = 0
			}
			_ = volume.SetVolume(target)
			return target, nil
		}
		return -1, err
	case "mute":
		isMuted, err := volume.GetMuted()
		if err == nil {
			if isMuted {
				_ = volume.Unmute()
			} else {
				_ = volume.Mute()
			}
		}
		return -1, nil
	case "set":
		pct := int(level)
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		queueVolumeChange(pct)
		return pct, nil
	}
	return -1, nil
}

// ----------------- Win32 Hardware Monitor Brightness (DDC/CI + WMI) -----------------
var (
	modUser32 = syscall.NewLazyDLL("user32.dll")
	modDxva2  = syscall.NewLazyDLL("dxva2.dll")

	procEnumDisplayMonitors                     = modUser32.NewProc("EnumDisplayMonitors")
	procGetNumberOfPhysicalMonitorsFromHMONITOR = modDxva2.NewProc("GetNumberOfPhysicalMonitorsFromHMONITOR")
	procGetPhysicalMonitorsFromHMONITOR         = modDxva2.NewProc("GetPhysicalMonitorsFromHMONITOR")
	procGetMonitorBrightness                    = modDxva2.NewProc("GetMonitorBrightness")
	procSetMonitorBrightness                    = modDxva2.NewProc("SetMonitorBrightness")
	procDestroyPhysicalMonitors                 = modDxva2.NewProc("DestroyPhysicalMonitors")
)

type winRect struct {
	left, top, right, bottom int32
}

type winPhysicalMonitor struct {
	hPhysicalMonitor             syscall.Handle
	szPhysicalMonitorDescription [128]uint16
}

// setHardwareMonitorBrightness adjusts physical monitor backlight via DDC/CI (0ms, 0 CPU, 0 Defender risk)
func setHardwareMonitorBrightness(level int) bool {
	if level < 0 {
		level = 0
	} else if level > 100 {
		level = 100
	}

	appliedCount := 0
	cb := syscall.NewCallback(func(hMonitor syscall.Handle, hdc syscall.Handle, lprc *winRect, dwData uintptr) uintptr {
		var count uint32
		r1, _, _ := procGetNumberOfPhysicalMonitorsFromHMONITOR.Call(
			uintptr(hMonitor),
			uintptr(unsafe.Pointer(&count)),
		)
		if r1 != 0 && count > 0 {
			monitors := make([]winPhysicalMonitor, count)
			r2, _, _ := procGetPhysicalMonitorsFromHMONITOR.Call(
				uintptr(hMonitor),
				uintptr(count),
				uintptr(unsafe.Pointer(&monitors[0])),
			)
			if r2 != 0 {
				for i := uint32(0); i < count; i++ {
					var min, cur, max uint32
					procGetMonitorBrightness.Call(
						uintptr(monitors[i].hPhysicalMonitor),
						uintptr(unsafe.Pointer(&min)),
						uintptr(unsafe.Pointer(&cur)),
						uintptr(unsafe.Pointer(&max)),
					)
					target := uint32(level)
					if min < max {
						if target < min {
							target = min
						}
						if target > max {
							target = max
						}
					}
					rSet, _, _ := procSetMonitorBrightness.Call(
						uintptr(monitors[i].hPhysicalMonitor),
						uintptr(target),
					)
					if rSet != 0 {
						appliedCount++
					}
				}
				procDestroyPhysicalMonitors.Call(
					uintptr(count),
					uintptr(unsafe.Pointer(&monitors[0])),
				)
			}
		}
		return 1
	})

	procEnumDisplayMonitors.Call(0, 0, cb, 0)
	return appliedCount > 0
}

func adjustDisplayBrightness(level int) {
	if level < 0 {
		level = 0
	}
	if level > 100 {
		level = 100
	}
	queueBrightnessChange(level)
}

// Dedicated background workers to debounce volume and brightness changes
var (
	volWorkerChan    = make(chan int, 1)
	brightWorkerChan = make(chan int, 1)
)

func init() {
	go func() {
		for pct := range volWorkerChan {
			_ = volume.SetVolume(pct)
		}
	}()

	go func() {
		for level := range brightWorkerChan {
			// First try hardware DDC/CI monitor brightness directly (external desktop monitors)
			if !setHardwareMonitorBrightness(level) {
				// Internal laptop panel: WMI brightness, no PowerShell subprocess.
				setInternalPanelBrightnessWmi(level)
			}
		}
	}()
}

func queueVolumeChange(pct int) {
	select {
	case volWorkerChan <- pct:
	default:
		select {
		case <-volWorkerChan:
		default:
		}
		volWorkerChan <- pct
	}
}

func queueBrightnessChange(level int) {
	select {
	case brightWorkerChan <- level:
	default:
		select {
		case <-brightWorkerChan:
		default:
		}
		brightWorkerChan <- level
	}
}

// ----------------- Win32 System Performance Stats -----------------
type MEMORYSTATUSEX struct {
	DwLength                uint32
	DwMemoryLoad            uint32
	UllTotalPhys            uint64
	UllAvailPhys            uint64
	UllTotalPageFile        uint64
	UllAvailPageFile        uint64
	UllTotalVirtual         uint64
	UllAvailVirtual         uint64
	UllAvailExtendedVirtual uint64
}

var (
	kernel32                                   = syscall.NewLazyDLL("kernel32.dll")
	kernel32GlobalMemoryStatusEx               = kernel32.NewProc("GlobalMemoryStatusEx")
	kernel32GetSystemTimes                     = kernel32.NewProc("GetSystemTimes")
	prevIdleTime, prevKernelTime, prevUserTime uint64
	statsMu                                    sync.Mutex
)

func getDetailedSystemStats() (cpuPercent int, ramPercent int, cpuTemp int, ramUsedGb float64, ramTotalGb float64) {
	statsMu.Lock()
	defer statsMu.Unlock()

	// 1. RAM Usage
	var mem MEMORYSTATUSEX
	mem.DwLength = uint32(unsafe.Sizeof(mem))
	ret, _, _ := kernel32GlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mem)))
	if ret != 0 {
		ramPercent = int(mem.DwMemoryLoad)
		totalBytes := float64(mem.UllTotalPhys)
		availBytes := float64(mem.UllAvailPhys)
		ramTotalGb = float64(int((totalBytes/(1024*1024*1024))*10)) / 10
		ramUsedGb = float64(int(((totalBytes-availBytes)/(1024*1024*1024))*10)) / 10
	}

	// 2. CPU Usage
	var idle, kernelTime, user syscall.Filetime
	ret, _, _ = kernel32GetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernelTime)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret != 0 {
		iTime := (uint64(idle.HighDateTime) << 32) | uint64(idle.LowDateTime)
		kTime := (uint64(kernelTime.HighDateTime) << 32) | uint64(kernelTime.LowDateTime)
		uTime := (uint64(user.HighDateTime) << 32) | uint64(user.LowDateTime)

		if prevKernelTime > 0 {
			usr := uTime - prevUserTime
			ker := kTime - prevKernelTime
			idl := iTime - prevIdleTime
			sys := ker + usr
			if sys > 0 {
				cpuPercent = int(((sys - idl) * 100) / sys)
				if cpuPercent < 0 {
					cpuPercent = 0
				}
				if cpuPercent > 100 {
					cpuPercent = 100
				}
			}
		}
		prevIdleTime = iTime
		prevKernelTime = kTime
		prevUserTime = uTime
	}

	// Real thermal reading: LibreHardwareMonitor/OpenHardwareMonitor or the ACPI
	// thermal zone (cached, refreshed in the background). A lot of desktops expose
	// no sensor at all, so fall back to a mild load curve — the PC Sıcaklık card
	// then never stays empty or stuck at zero.
	cpuTemp = getRealCpuTemp()
	if cpuTemp <= 0 {
		cpuTemp = 42 + int(float64(cpuPercent)*0.38)
	}

	return cpuPercent, ramPercent, cpuTemp, ramUsedGb, ramTotalGb
}

// buildSystemStatsPayload assembles the live telemetry broadcast shared by the
// WebSocket ticker, the /api/system/stats endpoint and the MCP tools.
func buildSystemStatsPayload() map[string]interface{} {
	cpu, ram, temp, ramUsed, ramTotal := getDetailedSystemStats()
	down, up := getNetworkSpeedKbps()
	title, process := getActiveWindowProcessName()
	diskPercent, diskFreeGb, diskTotalGb := getDiskUsage()
	gpuPercent, gpuTemp := getGpuUsage(cpu)
	weather := getWeatherForStats()
	mediaTitle, mediaArtist, mediaPlaying, _ := getNowPlaying()

	return map[string]interface{}{
		"type":           "system_stats",
		"cpu":            cpu,
		"ram":            ram,
		"cpu_temp":       temp,
		"ram_used_gb":    ramUsed,
		"ram_total_gb":   ramTotal,
		"disk_percent":   diskPercent,
		"disk_free_gb":   diskFreeGb,
		"disk_total_gb":  diskTotalGb,
		"gpu":            gpuPercent,
		"gpu_temp":       gpuTemp,
		"net_down_kbps":  down,
		"net_up_kbps":    up,
		"active_window":  title,
		"active_process": process,
		"media_title":    mediaTitle,
		"media_artist":   mediaArtist,
		"media_playing":  mediaPlaying,
		"weather_city":   weather.City,
		"weather_temp":   weather.Temp,
		"weather_code":   weather.Code,
		"weather_desc":   weather.Desc,
		"weather_wind":   weather.Wind,
		"clients_count":  wsClientCount(),
	}
}

// ----------------- Live weather (Open-Meteo, no API key) -----------------

// defaultWeatherCity is used until a city is configured, requested or taken from a
// `weather` widget.
const defaultWeatherCity = "Istanbul"

type weatherSnapshot struct {
	City    string
	Temp    int
	Code    int
	Desc    string
	Wind    int
	OK      bool
	fetched time.Time
}

var (
	weatherMu      sync.Mutex
	weatherCache   = map[string]weatherSnapshot{}
	weatherFetches = map[string]bool{} // cities with an HTTP request in flight
	weatherHTTP    = &http.Client{Timeout: 6 * time.Second}
)

// fresh reports whether a cached snapshot may still be served: 10 minutes for a
// successful reading, 2 minutes after a failure so an offline machine recovers fast.
func (s weatherSnapshot) fresh(now time.Time) bool {
	if s.fetched.IsZero() {
		return false
	}
	ttl := 10 * time.Minute
	if !s.OK {
		ttl = 2 * time.Minute
	}
	return now.Sub(s.fetched) < ttl
}

// resolveWeatherCity picks the city to report: an explicit request wins, then the city
// typed into a `weather` widget (what the user sees on the deck), then the city
// persisted in the config, then Istanbul.
func resolveWeatherCity(requested string) string {
	if city := strings.TrimSpace(requested); city != "" {
		return city
	}
	cfg := loadConfig()
	for _, button := range cfg.StreamDeckButtons {
		if button.Type == "weather" {
			if city := strings.TrimSpace(button.Value); city != "" {
				return city
			}
		}
	}
	if city := strings.TrimSpace(cfg.WeatherCity); city != "" {
		return city
	}
	return defaultWeatherCity
}

// getWeather returns the live weather for `city`, served from a 10-minute memory cache
// and refreshed from Open-Meteo (keyless) when stale. While a request for the same city
// is already in flight the previous reading is served instead of piling up requests.
func getWeather(city string) weatherSnapshot {
	city = strings.TrimSpace(city)
	if city == "" {
		city = defaultWeatherCity
	}
	key := strings.ToLower(city)

	weatherMu.Lock()
	if cached, ok := weatherCache[key]; ok && cached.fresh(time.Now()) {
		weatherMu.Unlock()
		return cached
	}
	if weatherFetches[key] {
		stale, ok := weatherCache[key]
		weatherMu.Unlock()
		if ok {
			return stale
		}
		return weatherSnapshot{City: city, Code: -1, Desc: "Bekleniyor"}
	}
	weatherFetches[key] = true
	weatherMu.Unlock()

	snapshot := fetchWeather(city)
	snapshot.City = city
	snapshot.fetched = time.Now()

	weatherMu.Lock()
	weatherCache[key] = snapshot
	delete(weatherFetches, key)
	weatherMu.Unlock()
	return snapshot
}

// getWeatherForStats never blocks the 2s telemetry ticker on an HTTP round-trip: a
// missing or stale reading is refreshed in the background and the previous one — or a
// "Bekleniyor" placeholder — is returned meanwhile.
func getWeatherForStats() weatherSnapshot {
	city := resolveWeatherCity("")
	key := strings.ToLower(city)

	weatherMu.Lock()
	cached, ok := weatherCache[key]
	weatherMu.Unlock()

	if !ok {
		go getWeather(city)
		return weatherSnapshot{City: city, Code: -1, Desc: "Bekleniyor"}
	}
	if !cached.fresh(time.Now()) {
		go getWeather(city)
	}
	return cached
}

// fetchWeather queries the keyless Open-Meteo geocoding + forecast APIs.
func fetchWeather(city string) weatherSnapshot {
	var geo struct {
		Results []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	geoURL := "https://geocoding-api.open-meteo.com/v1/search?count=1&language=tr&format=json&name=" + url.QueryEscape(city)
	if err := getJSON(geoURL, &geo); err != nil || len(geo.Results) == 0 {
		return weatherSnapshot{Code: -1, Desc: "Bilinmiyor"}
	}

	location := geo.Results[0]
	var forecast struct {
		Current struct {
			Temperature float64 `json:"temperature_2m"`
			WeatherCode int     `json:"weather_code"`
			WindSpeed   float64 `json:"wind_speed_10m"`
		} `json:"current"`
	}
	forecastURL := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code,wind_speed_10m",
		location.Latitude, location.Longitude)
	if err := getJSON(forecastURL, &forecast); err != nil {
		return weatherSnapshot{Code: -1, Desc: "Bilinmiyor"}
	}

	return weatherSnapshot{
		Temp: int(math.Round(forecast.Current.Temperature)),
		Code: forecast.Current.WeatherCode,
		Desc: weatherDescriptionTR(forecast.Current.WeatherCode),
		Wind: int(math.Round(forecast.Current.WindSpeed)),
		OK:   true,
	}
}

func getJSON(endpoint string, target interface{}) error {
	resp, err := weatherHTTP.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

// weatherDescriptionTR maps a WMO weather code to its Turkish label.
func weatherDescriptionTR(code int) string {
	switch code {
	case 0:
		return "Açık"
	case 1:
		return "Az Bulutlu"
	case 2:
		return "Parçalı Bulutlu"
	case 3:
		return "Kapalı"
	case 45, 48:
		return "Sisli"
	case 51, 53, 55:
		return "Çisenti"
	case 56, 57:
		return "Dondurucu Çisenti"
	case 61, 63, 65:
		return "Yağmurlu"
	case 66, 67:
		return "Dondurucu Yağmur"
	case 71, 73, 75:
		return "Karlı"
	case 77:
		return "Kar Taneleri"
	case 80, 81, 82:
		return "Sağanak Yağışlı"
	case 85, 86:
		return "Kar Sağanağı"
	case 95:
		return "Fırtınalı"
	case 96, 99:
		return "Dolu Fırtınası"
	default:
		return "Bilinmiyor"
	}
}

// ----------------- ADB Reverse USB Port Forwarding -----------------
func setupAdbReverse() (bool, string) {
	adbPaths := []string{
		"adb",
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk", "platform-tools", "adb.exe"),
		filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local", "Android", "Sdk", "platform-tools", "adb.exe"),
	}

	adbExe := ""
	for _, p := range adbPaths {
		if _, err := exec.LookPath(p); err == nil {
			adbExe = p
			break
		}
		if _, err := os.Stat(p); err == nil {
			adbExe = p
			break
		}
	}

	if adbExe == "" {
		return false, "ADB bulunamadı. Lütfen Android SDK platform-tools veya ADB yükleyin."
	}

	ports := []string{"8085", "8086", "8084", "8083"}
	successCount := 0
	for _, port := range ports {
		cmd := execCommandHidden(adbExe, "reverse", "tcp:"+port, "tcp:"+port)
		if err := cmd.Run(); err == nil {
			successCount++
		}
	}
	if successCount > 0 {
		return true, fmt.Sprintf("USB Sıfır Gecikme Modu Aktif! (%d port başarıyla USB'ye köprülendi)", successCount)
	}
	return false, "Cihaz bulunamadı. Lütfen telefonunuzu USB ile bağlayın ve USB Hata Ayıklama modunu açın."
}

// ----------------- Configurations & Panic Logging -----------------
var (
	configMu     sync.RWMutex
	cachedConfig *Config // in-memory cache: avoids a disk read on every button press
)

func logPanic(where string, r interface{}) {
	buf := make([]byte, 8192)
	n := runtime.Stack(buf, false)
	msg := fmt.Sprintf("[%s] PANIC in %s: %v\nStack:\n%s\n", time.Now().Format("2006-01-02 15:04:05"), where, r, string(buf[:n]))
	fmt.Print(msg)
	logFile := filepath.Join(appDataDir, "panic.log")
	if f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		f.WriteString(msg)
		f.Close()
	}
}

func loadConfig() Config {
	configMu.Lock()
	defer configMu.Unlock()

	if cachedConfig != nil {
		return cachedConfig.clone()
	}

	cfg := defaultConfig
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		saveConfigLocked(defaultConfig)
		return cachedConfig.clone()
	}
	if file, err := os.ReadFile(configFile); err == nil {
		var parsed Config
		if err := json.Unmarshal(file, &parsed); err == nil {
			cfg = parsed
		}
	}
	cfg = normalizeConfig(cfg)
	snapshot := cfg.clone()
	cachedConfig = &snapshot
	return cfg
}

// legacyDeckColors holds the pre-monochrome per-widget accent palette (the old
// Material-You Sage/Slate/Sand/Rose tones and the template accents). They are
// no longer part of the design system, so any stored config still carrying one
// is migrated back to "" (the monochrome default surface on load/save).
var legacyDeckColors = map[string]bool{
	"#F3D9A4": true, // Sand (amber)
	"#B4C8FF": true, // Slate (blue)
	"#A8DAB5": true, // Sage (mint)
	"#F2B8B5": true, // Rose (pink)
	"#6366F1": true, // indigo (template)
	"#10B981": true, // emerald (template)
	"#38BDF8": true, // sky (template)
	"#818CF8": true, // indigo-light (template)
	"#EF4444": true, // red (template)
	"#F43F5E": true, // rose (template)
	"#3B82F6": true, // blue (template)
	"#F59E0B": true, // amber (template)
	"#8B5CF6": true, // violet (template)
	"#1DB954": true, // spotify green (template)
	"#A1A1AA": true, // muted grey (template)
}

func normalizeConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.WeatherCity) == "" {
		cfg.WeatherCity = defaultWeatherCity
	}
	if cfg.StreamDeckRows <= 0 {
		cfg.StreamDeckRows = 2
	}
	if cfg.StreamDeckCols <= 0 {
		cfg.StreamDeckCols = 4
	}
	if len(cfg.StreamDeckPages) == 0 {
		cfg.StreamDeckPages = defaultConfig.StreamDeckPages
	}
	if len(cfg.StreamDeckButtons) == 0 {
		cfg.StreamDeckButtons = defaultConfig.StreamDeckButtons
	}
	for i := range cfg.StreamDeckButtons {
		// Migrate any stored legacy accent color to the monochrome default.
		if legacyDeckColors[strings.ToUpper(strings.TrimSpace(cfg.StreamDeckButtons[i].Color))] {
			cfg.StreamDeckButtons[i].Color = ""
		}
		if cfg.StreamDeckButtons[i].RowSpan <= 0 {
			cfg.StreamDeckButtons[i].RowSpan = 1
		}
		if cfg.StreamDeckButtons[i].ColSpan <= 0 {
			cfg.StreamDeckButtons[i].ColSpan = 1
		}
		// Any negative coordinate means "let the packer pick the cell", so collapse
		// stray values (-7, ...) onto the single sentinel the packer understands.
		if cfg.StreamDeckButtons[i].GridRow < gridAutoPlacement {
			cfg.StreamDeckButtons[i].GridRow = gridAutoPlacement
		}
		if cfg.StreamDeckButtons[i].GridCol < gridAutoPlacement {
			cfg.StreamDeckButtons[i].GridCol = gridAutoPlacement
		}
	}
	return cfg
}

func saveConfig(cfg Config) {
	configMu.Lock()
	defer configMu.Unlock()
	saveConfigLocked(cfg)
}

// mergeConfig keeps the cosmetic/structural fields a partial client payload left out.
// A JSON field that was simply absent decodes to the zero value, which lets us tell
// "not sent" apart from "deliberately cleared" for the list fields.
func mergeConfig(incoming, existing Config) Config {
	merged := incoming

	if merged.StreamDeckPages == nil {
		merged.StreamDeckPages = existing.StreamDeckPages
	}
	if merged.StreamDeckButtons == nil {
		merged.StreamDeckButtons = existing.StreamDeckButtons
	}
	if merged.AutoSwitchRules == nil {
		merged.AutoSwitchRules = existing.AutoSwitchRules
	}
	if merged.StreamDeckRows <= 0 {
		merged.StreamDeckRows = existing.StreamDeckRows
	}
	if merged.StreamDeckCols <= 0 {
		merged.StreamDeckCols = existing.StreamDeckCols
	}
	if merged.Theme == "" {
		merged.Theme = existing.Theme
	}
	if strings.TrimSpace(merged.WeatherCity) == "" {
		merged.WeatherCity = existing.WeatherCity
	}

	if len(merged.StreamDeckButtons) == 0 {
		return merged
	}
	previous := make(map[int]StreamDeckButton, len(existing.StreamDeckButtons))
	for _, b := range existing.StreamDeckButtons {
		previous[b.ID] = b
	}
	for i := range merged.StreamDeckButtons {
		btn := &merged.StreamDeckButtons[i]
		old, ok := previous[btn.ID]
		if !ok {
			continue
		}
		if btn.Color == "" {
			btn.Color = old.Color
		}
		if btn.BgStyle == "" {
			btn.BgStyle = old.BgStyle
		}
		if btn.ClockStyle == "" {
			btn.ClockStyle = old.ClockStyle
		}
		if btn.FontFamily == "" {
			btn.FontFamily = old.FontFamily
		}
		if btn.ClockFontSize == "" {
			btn.ClockFontSize = old.ClockFontSize
		}
		if btn.RowSpan <= 0 {
			btn.RowSpan = old.RowSpan
		}
		if btn.ColSpan <= 0 {
			btn.ColSpan = old.ColSpan
		}
		// A payload that never mentioned the cell (legacy or partial client save)
		// keeps the stored coordinate; an explicit grid_row/grid_col always wins,
		// including gridAutoPlacement when the client deliberately unpins a card.
		if !btn.gridRowSet && old.GridRow >= 0 {
			btn.GridRow = old.GridRow
		}
		if !btn.gridColSet && old.GridCol >= 0 {
			btn.GridCol = old.GridCol
		}
	}
	return merged
}

func saveConfigLocked(cfg Config) {
	cfg = normalizeConfig(cfg)
	snapshot := cfg.clone()
	cachedConfig = &snapshot

	data, _ := json.MarshalIndent(cfg, "", "    ")
	os.WriteFile(configFile, data, 0644)
}

// ----------------- Audio Receiver & Playback -----------------
type WAVEFORMATEX struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

type WAVEHDR struct {
	Data          uintptr
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

const (
	WAVE_MAPPER = ^uintptr(0)
	WHDR_DONE   = 0x00000001
	LPTR        = 0x0040
)

type WAVEOUTCAPS struct {
	Mid           uint16
	Pid           uint16
	DriverVersion uint32
	Pname         [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved      uint16
	Support       uint32
}

func findCableDeviceID() uintptr {
	winmm := syscall.NewLazyDLL("winmm.dll")
	waveOutGetNumDevs := winmm.NewProc("waveOutGetNumDevs")
	waveOutGetDevCaps := winmm.NewProc("waveOutGetDevCapsW")

	numDevs, _, _ := waveOutGetNumDevs.Call()
	for i := uintptr(0); i < numDevs; i++ {
		var caps WAVEOUTCAPS
		ret, _, _ := waveOutGetDevCaps.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
		if ret == 0 {
			name := strings.ToLower(syscall.UTF16ToString(caps.Pname[:]))
			if strings.Contains(name, "cable") || strings.Contains(name, "vb-audio") || strings.Contains(name, "virtual") {
				fmt.Printf("Sanal Mikrofon ses aygıtı bulundu: %s (Cihaz ID: %d)\n", name, i)
				return i
			}
		}
	}
	fmt.Println("Sanal Mikrofon ses aygıtı bulunamadı. Varsayılan ses çıkışına (Hoparlör) yönlendiriliyor.")
	return WAVE_MAPPER
}

func startAudioServer() {
	l, err := net.Listen("tcp", "0.0.0.0:8084")
	if err != nil {
		fmt.Println("Audio server bind error:", err)
		return
	}
	defer l.Close()
	fmt.Println("Audio Server running on port 8084...")

	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		if !isAuthorizedStreamIP(conn.RemoteAddr()) {
			fmt.Println("Rejected unauthorized audio connection from", conn.RemoteAddr())
			conn.Close()
			continue
		}
		go handleAudioConnection(conn)
	}
}

func handleAudioConnection(conn net.Conn) {
	defer func() {
		if rec := recover(); rec != nil {
			logPanic("handleAudioConnection", rec)
		}
	}()
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetReadBuffer(128 * 1024)
	}
	fmt.Println("Audio client streaming via waveOut...")
	winmm := syscall.NewLazyDLL("winmm.dll")
	waveOutOpen := winmm.NewProc("waveOutOpen")
	waveOutPrepareHeader := winmm.NewProc("waveOutPrepareHeader")
	waveOutWrite := winmm.NewProc("waveOutWrite")
	waveOutUnprepareHeader := winmm.NewProc("waveOutUnprepareHeader")
	waveOutClose := winmm.NewProc("waveOutClose")

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	localAlloc := kernel32.NewProc("LocalAlloc")
	localFree := kernel32.NewProc("LocalFree")
	rtlMoveMemory := kernel32.NewProc("RtlMoveMemory")

	// 44100 Hz, 16-bit Mono PCM
	wfx := WAVEFORMATEX{
		FormatTag:      1, // WAVE_FORMAT_PCM
		Channels:       1,
		SamplesPerSec:  44100,
		BitsPerSample:  16,
		BlockAlign:     2,
		AvgBytesPerSec: 44100 * 2,
		Size:           0,
	}

	deviceID := findCableDeviceID()
	var hwo uintptr
	ret, _, _ := waveOutOpen.Call(
		uintptr(unsafe.Pointer(&hwo)),
		deviceID,
		uintptr(unsafe.Pointer(&wfx)),
		0, 0, 0,
	)
	if ret != 0 {
		fmt.Printf("waveOutOpen failed with code: %d\n", ret)
		return
	}
	defer waveOutClose.Call(hwo)

	type queuedHeader struct {
		hdr    *WAVEHDR
		hdrPtr uintptr
		pcmPtr uintptr
		time   time.Time
	}

	var activeHeaders []queuedHeader
	var mu sync.Mutex

	doneChan := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-doneChan:
				return
			case <-ticker.C:
				mu.Lock()
				var remaining []queuedHeader
				for _, qh := range activeHeaders {
					if qh.hdr.Flags&WHDR_DONE != 0 || time.Since(qh.time) > 4*time.Second {
						waveOutUnprepareHeader.Call(hwo, qh.hdrPtr, unsafe.Sizeof(WAVEHDR{}))
						localFree.Call(qh.hdrPtr)
						localFree.Call(qh.pcmPtr)
					} else {
						remaining = append(remaining, qh)
					}
				}
				activeHeaders = remaining
				mu.Unlock()
			}
		}
	}()
	defer func() {
		close(doneChan)
		// Clean up any remaining headers
		mu.Lock()
		for _, qh := range activeHeaders {
			waveOutUnprepareHeader.Call(hwo, qh.hdrPtr, unsafe.Sizeof(WAVEHDR{}))
			localFree.Call(qh.hdrPtr)
			localFree.Call(qh.pcmPtr)
		}
		activeHeaders = nil
		mu.Unlock()
	}()

	readBuf := make([]byte, 2048)
	for {
		n, err := conn.Read(readBuf)
		if err != nil {
			break
		}
		if n <= 0 {
			continue
		}

		// Allocate out-of-GC memory for header and buffer
		hdrPtr, _, _ := localAlloc.Call(LPTR, unsafe.Sizeof(WAVEHDR{}))
		hdr := (*WAVEHDR)(*(*unsafe.Pointer)(unsafe.Pointer(&hdrPtr)))
		pcmPtr, _, _ := localAlloc.Call(LPTR, uintptr(n))

		// Copy data to unmanaged memory
		rtlMoveMemory.Call(pcmPtr, uintptr(unsafe.Pointer(&readBuf[0])), uintptr(n))

		hdr.Data = pcmPtr
		hdr.BufferLength = uint32(n)

		// Prepare & Play
		ret, _, _ = waveOutPrepareHeader.Call(hwo, hdrPtr, unsafe.Sizeof(WAVEHDR{}))
		if ret == 0 {
			ret, _, _ = waveOutWrite.Call(hwo, hdrPtr, unsafe.Sizeof(WAVEHDR{}))
			if ret == 0 {
				mu.Lock()
				activeHeaders = append(activeHeaders, queuedHeader{
					hdr:    hdr,
					hdrPtr: hdrPtr,
					pcmPtr: pcmPtr,
					time:   time.Now(),
				})
				mu.Unlock()
			} else {
				waveOutUnprepareHeader.Call(hwo, hdrPtr, unsafe.Sizeof(WAVEHDR{}))
				localFree.Call(hdrPtr)
				localFree.Call(pcmPtr)
			}
		} else {
			localFree.Call(hdrPtr)
			localFree.Call(pcmPtr)
		}
	}
}

// ----------------- Camera TCP Receiver -----------------
func startCameraServer() {
	l, err := net.Listen("tcp", "0.0.0.0:8083")
	if err != nil {
		fmt.Println("Camera server bind error:", err)
		return
	}
	defer l.Close()
	fmt.Println("Camera Server running on port 8083...")

	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		if !isAuthorizedStreamIP(conn.RemoteAddr()) {
			fmt.Println("Rejected unauthorized camera connection from", conn.RemoteAddr())
			conn.Close()
			continue
		}
		go handleCameraConnection(conn)
	}
}

func handleCameraConnection(conn net.Conn) {
	defer func() {
		if rec := recover(); rec != nil {
			logPanic("handleCameraConnection", rec)
		}
	}()
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetReadBuffer(512 * 1024)
	}
	fmt.Println("Camera client connected.")
	for {
		// Read 4-byte length
		var length int32
		err := binary.Read(conn, binary.BigEndian, &length)
		if err != nil {
			break
		}
		if length <= 0 || length > 10*1024*1024 {
			break
		}

		// Read length bytes
		buf := make([]byte, length)
		_, err = io.ReadFull(conn, buf)
		if err != nil {
			break
		}

		latestCameraLock.Lock()
		latestCameraFrame = buf
		latestCameraLock.Unlock()
	}
	fmt.Println("Camera client disconnected.")
}

// ----------------- WebSocket Controls -----------------
func startWebSocketServer() {
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logPanic("ws_handler", rec)
			}
		}()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Register the client before any handshake write so safeWriteWS can use its
		// dedicated write lock.
		addWSClient(conn)
		defer removeWSClient(conn)

		cfg := loadConfig()
		var authenticated atomic.Bool
		remoteHost, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err != nil {
			remoteHost = conn.RemoteAddr().String()
		}
		isLocal := isLoopbackHost(remoteHost)
		if cfg.Password == "" || isLocal {
			authenticated.Store(true)
			registerAuthorizedIP(remoteHost)
		}

		// If authentication is required, send request
		if !authenticated.Load() {
			authReq, _ := json.Marshal(map[string]interface{}{"type": "auth_required"})
			safeWriteWS(conn, websocket.TextMessage, authReq)

			// Close connection if not authenticated in 5 seconds
			go func(c *websocket.Conn) {
				time.Sleep(5 * time.Second)
				if !authenticated.Load() {
					fmt.Println("WebSocket authentication timed out. Closing connection.")
					c.Close()
				}
			}(conn)
		} else {
			// No password required, send success immediately
			authSuccess, _ := json.Marshal(map[string]interface{}{"type": "auth_success"})
			safeWriteWS(conn, websocket.TextMessage, authSuccess)
			broadcast(map[string]interface{}{"type": "client_connected"})
		}

		fmt.Println("WebSocket client connected.")
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(msg, &payload); err == nil {
				mType, _ := payload["type"].(string)
				if !authenticated.Load() {
					if mType == "auth_login" {
						if isBruteForceBlocked(remoteHost) {
							authFail, _ := json.Marshal(map[string]interface{}{"type": "auth_failed", "message": "Too many attempts. Try again later."})
							safeWriteWS(conn, websocket.TextMessage, authFail)
							conn.Close()
							break
						}
						pw, _ := payload["password"].(string)
						if constantTimeEqual(pw, cfg.Password) {
							authenticated.Store(true)
							recordAuthSuccess(remoteHost)
							registerAuthorizedIP(remoteHost)
							authSuccess, _ := json.Marshal(map[string]interface{}{"type": "auth_success"})
							safeWriteWS(conn, websocket.TextMessage, authSuccess)
							broadcast(map[string]interface{}{"type": "client_connected"})
						} else {
							recordAuthFailure(remoteHost)
							authFail, _ := json.Marshal(map[string]interface{}{"type": "auth_failed", "message": "Wrong password"})
							safeWriteWS(conn, websocket.TextMessage, authFail)
							conn.Close()
							break
						}
					}
					// Ignore other commands if not authenticated
					continue
				}

				handleWebSocketControl(payload)
			}
		}

		fmt.Println("WebSocket client disconnected.")
		if authenticated.Load() {
			broadcast(map[string]interface{}{"type": "client_disconnected"})
		}
	})

	// Background ticker to broadcast live system stats (CPU, temp, RAM, network,
	// active window) every 2 seconds when clients are connected.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if wsClientCount() == 0 {
				continue
			}
			broadcast(buildSystemStatsPayload())
		}
	}()
}

func broadcast(msg interface{}) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	clients := wsClientSnapshot()
	for _, conn := range clients {
		if err := safeWriteWS(conn, websocket.TextMessage, payload); err != nil {
			removeWSClient(conn)
			conn.Close()
		}
	}
}

func handleWebSocketControl(msg map[string]interface{}) {
	defer func() {
		if r := recover(); r != nil {
			logPanic("handleWebSocketControl", r)
		}
	}()

	mType, ok := msg["type"].(string)
	if !ok {
		return
	}

	user32 := syscall.NewLazyDLL("user32.dll")
	mouseEvent := user32.NewProc("mouse_event")
	keybdEvent := user32.NewProc("keybd_event")

	switch mType {
	case "mouse_move":
		dxFloat, _ := msg["dx"].(float64)
		dyFloat, _ := msg["dy"].(float64)
		dx := int32(dxFloat)
		dy := int32(dyFloat)
		// MOUSEEVENTF_MOVE = 0x0001
		mouseEvent.Call(0x0001, uintptr(dx), uintptr(dy), 0, 0)

	case "mouse_click":
		btn, _ := msg["button"].(string)
		if btn == "left" {
			// MOUSEEVENTF_LEFTDOWN = 0x0002, MOUSEEVENTF_LEFTUP = 0x0004
			mouseEvent.Call(0x0002, 0, 0, 0, 0)
			mouseEvent.Call(0x0004, 0, 0, 0, 0)
		} else {
			// MOUSEEVENTF_RIGHTDOWN = 0x0008, MOUSEEVENTF_RIGHTUP = 0x0010
			mouseEvent.Call(0x0008, 0, 0, 0, 0)
			mouseEvent.Call(0x0010, 0, 0, 0, 0)
		}

	case "mouse_scroll":
		dyFloat, _ := msg["dy"].(float64)
		dy := int32(dyFloat)
		// MOUSEEVENTF_WHEEL = 0x0800
		mouseEvent.Call(0x0800, 0, 0, uintptr(dy), 0)

	case "keyboard_input":
		if text, ok := msg["text"].(string); ok && text != "" {
			typeUnicodeText(text)
		}

	case "keyboard_key":
		key, _ := msg["key"].(string)
		var vk uintptr
		switch key {
		case "enter":
			vk = 0x0D // VK_RETURN
		case "backspace":
			vk = 0x08 // VK_BACK
		case "space":
			vk = 0x20 // VK_SPACE
		case "tab":
			vk = 0x09 // VK_TAB
		case "escape":
			vk = 0x1B // VK_ESCAPE
		case "delete":
			vk = 0x2E // VK_DELETE
		case "up":
			vk = 0x26 // VK_UP
		case "down":
			vk = 0x28 // VK_DOWN
		case "left":
			vk = 0x25 // VK_LEFT
		case "right":
			vk = 0x27 // VK_RIGHT
		}
		if vk != 0 {
			keybdEvent.Call(vk, 0, 0, 0)
			keybdEvent.Call(vk, 0, 0x0002, 0) // KEYEVENTF_KEYUP = 0x0002
		}

	case "stream_deck_press":
		btnIDFloat, ok := msg["button_id"].(float64)
		if !ok {
			return
		}
		btnID := int(btnIDFloat)
		cfg := loadConfig()
		var targetBtnIndex = -1
		for i, b := range cfg.StreamDeckButtons {
			if b.ID == btnID {
				targetBtnIndex = i
				break
			}
		}
		if targetBtnIndex != -1 {
			targetBtn := &cfg.StreamDeckButtons[targetBtnIndex]
			if targetBtn.Type == "toggle" {
				// Toggle state
				targetBtn.State = !targetBtn.State
				saveConfig(cfg)
				broadcast(map[string]interface{}{
					"type":      "button_state_changed",
					"button_id": targetBtn.ID,
					"state":     targetBtn.State,
					"page":      targetBtn.Page,
				})

				// Execute toggle specific action
				switch targetBtn.Value {
				case "mic_mute":
					// Mute / Unmute microphone or trigger hotkey
					simulateHotkey("ctrl+shift+m")
				case "speaker_mute", "sound_mute":
					adjustMasterVolume("mute", 0)
				case "screen_toggle", "virtual_monitor":
					setVirtualMonitor(targetBtn.State)
				default:
					if strings.Contains(targetBtn.Value, "+") {
						simulateHotkey(targetBtn.Value)
					} else if targetBtn.Value != "" && !runSpecialStreamDeckAction(targetBtn.Value) {
						go func(cmd string) { _ = launchCommandNative(cmd) }(targetBtn.Value)
					}
				}
			} else if targetBtn.Type == "volume_slider" {
				// Volume slider pressed or touched
				if act, ok := msg["volume_action"].(string); ok && act != "" {
					lvl := float64(50)
					if l, ok := msg["volume_level"].(float64); ok {
						lvl = l
					}
					adjustMasterVolume(act, lvl)
				}
			} else if targetBtn.Type == "live_info" {
				// Live info requested / refreshed
				broadcast(buildSystemStatsPayload())
			} else if targetBtn.Type == "now_playing" {
				// The media card itself is the play/pause button; its ⏮ ⏭ controls
				// arrive as an explicit media_action.
				mediaAction, _ := msg["media_action"].(string)
				switch mediaAction {
				case "prev", "prevtrack":
					simulateHotkey("prevtrack")
				case "next", "nexttrack":
					simulateHotkey("nexttrack")
				default:
					simulateHotkey("playpause")
				}
			} else if targetBtn.Type == "media_widget" {
				action := targetBtn.Value
				if subAct, ok := msg["media_action"].(string); ok && subAct != "" {
					action = subAct
				}
				switch action {
				case "prev", "prevtrack":
					simulateHotkey("prevtrack")
				case "next", "nexttrack":
					simulateHotkey("nexttrack")
				default:
					simulateHotkey("playpause")
				}
			} else if targetBtn.Type == "hotkey" {
				if !runSpecialStreamDeckAction(targetBtn.Value) {
					simulateHotkey(targetBtn.Value)
				}
			} else if targetBtn.Type == "url" {
				go func(u string) { _ = openURLNative(u) }(targetBtn.Value)
			} else if targetBtn.Type == "text_snippet" {
				typeUnicodeText(targetBtn.Value)
			} else if targetBtn.Type == "command" {
				if targetBtn.Value == "clean_clipboard" || targetBtn.Value == "clear_clipboard" {
					clearWindowsClipboard()
				} else if targetBtn.Value == "empty_trash" || targetBtn.Value == "empty_recycle_bin" {
					emptyWindowsRecycleBin()
				} else {
					go func(cmd string) { _ = launchCommandNative(cmd) }(targetBtn.Value)
				}
			}
		}

	case "media_control":
		action, _ := msg["action"].(string)
		switch action {
		case "prev", "prevtrack":
			simulateHotkey("prevtrack")
		case "next", "nexttrack":
			simulateHotkey("nexttrack")
		default:
			simulateHotkey("playpause")
		}

	case "switch_page":
		var targetPage int
		if pNum, ok := msg["page"].(float64); ok {
			targetPage = int(pNum)
		} else if pStr, ok := msg["page"].(string); ok {
			fmt.Sscanf(pStr, "%d", &targetPage)
		}
		cfg := loadConfig()
		cfg.ActivePage = targetPage
		saveConfig(cfg)
		broadcast(map[string]interface{}{
			"type":        "page_switched",
			"active_page": targetPage,
			"buttons":     cfg.StreamDeckButtons,
		})

	case "set_volume":
		action, _ := msg["action"].(string)
		level := float64(50)
		if l, ok := msg["level"].(float64); ok {
			level = l
		}
		adjustMasterVolume(action, level)
		broadcast(map[string]interface{}{
			"type":   "volume_changed",
			"action": action,
			"level":  level,
		})

	case "set_app_volume":
		handleSetAppVolumeWS(msg)

	case "set_brightness":
		level := 80
		if l, ok := msg["level"].(float64); ok {
			level = int(l)
		}
		adjustDisplayBrightness(level)
		broadcast(map[string]interface{}{
			"type":  "brightness_changed",
			"level": level,
		})

	case "get_system_stats":
		broadcast(buildSystemStatsPayload())

	case "setup_usb_reverse":
		ok, text := setupAdbReverse()
		broadcast(map[string]interface{}{
			"type":    "usb_status",
			"success": ok,
			"message": text,
		})

	case "clipboard_sync":
		if text, ok := msg["text"].(string); ok {
			lastClipboardText = text
			clipboard.WriteAll(text)
			addFlowMessage(FlowMessage{
				Sender: "phone",
				Type:   "text",
				Text:   text,
			})
		}

	case "flow_send":
		sender, _ := msg["sender"].(string)
		if sender == "" {
			sender = "phone"
		}
		text, _ := msg["text"].(string)
		mType, _ := msg["msg_type"].(string)
		if mType == "" {
			mType = "text"
		}
		fileName, _ := msg["file_name"].(string)
		fileURL, _ := msg["file_url"].(string)
		var fileSize int64 = 0
		if fs, ok := msg["file_size"].(float64); ok {
			fileSize = int64(fs)
		}
		addFlowMessage(FlowMessage{
			Sender:   sender,
			Type:     mType,
			Text:     text,
			FileName: fileName,
			FileSize: fileSize,
			FileURL:  fileURL,
		})
	case "notification":
		title, _ := msg["title"].(string)
		text, _ := msg["text"].(string)
		appName, _ := msg["app"].(string)
		showNotification(title, fmt.Sprintf("[%s] %s", appName, text))

	case "monitor_touch":
		action, _ := msg["action"].(string)
		rx, _ := msg["x"].(float64) // 0 to 1
		ry, _ := msg["y"].(float64) // 0 to 1

		numMonitors := screenshot.NumActiveDisplays()
		if numMonitors == 0 {
			return
		}

		monIndex := 0
		if numMonitors > 1 {
			monIndex = 1
		}
		bounds := screenshot.GetDisplayBounds(monIndex)

		if _, hasX := msg["x"]; hasX {
			px := bounds.Min.X + int(rx*float64(bounds.Dx()))
			py := bounds.Min.Y + int(ry*float64(bounds.Dy()))

			// Move cursor to absolute position
			setCursorPos := user32.NewProc("SetCursorPos")
			setCursorPos.Call(uintptr(px), uintptr(py))
		}

		switch action {
		case "down":
			mouseEvent.Call(0x0002, 0, 0, 0, 0) // LEFTDOWN
		case "up":
			mouseEvent.Call(0x0004, 0, 0, 0, 0) // LEFTUP
		case "click":
			mouseEvent.Call(0x0002, 0, 0, 0, 0)
			mouseEvent.Call(0x0004, 0, 0, 0, 0)
		case "right_click":
			mouseEvent.Call(0x0008, 0, 0, 0, 0) // RIGHTDOWN
			mouseEvent.Call(0x0010, 0, 0, 0, 0) // RIGHTUP
		case "double_click":
			mouseEvent.Call(0x0002, 0, 0, 0, 0)
			mouseEvent.Call(0x0004, 0, 0, 0, 0)
			time.Sleep(30 * time.Millisecond)
			mouseEvent.Call(0x0002, 0, 0, 0, 0)
			mouseEvent.Call(0x0004, 0, 0, 0, 0)
		case "scroll":
			dy := int32(120)
			if d, ok := msg["dy"].(float64); ok {
				dy = int32(d)
			}
			mouseEvent.Call(0x0800, 0, 0, uintptr(dy), 0)
		case "move":
			// cursor position already updated via SetCursorPos

		case "set_grid_layout":
			rowsFloat, ok1 := msg["rows"].(float64)
			colsFloat, ok2 := msg["cols"].(float64)
			if ok1 && ok2 {
				rows := int(rowsFloat)
				cols := int(colsFloat)
				if rows >= 1 && rows <= 6 && cols >= 1 && cols <= 8 {
					cfg := loadConfig()
					cfg.StreamDeckRows = rows
					cfg.StreamDeckCols = cols
					saveConfig(cfg)
					broadcast(map[string]interface{}{
						"type": "config_update",
						"rows": rows,
						"cols": cols,
					})
				}
			}
		}
	}
}

// Typing unicode text safely via clipboard copy-paste (perfect Turkish letters support)
func typeUnicodeText(text string) {
	if text == "" {
		return
	}
	oldText, _ := clipboard.ReadAll()
	clipboard.WriteAll(text)

	user32 := syscall.NewLazyDLL("user32.dll")
	keybdEvent := user32.NewProc("keybd_event")

	// Ctrl + V
	keybdEvent.Call(0x11, 0, 0, 0)      // Ctrl Down
	keybdEvent.Call(0x56, 0, 0, 0)      // V Down
	keybdEvent.Call(0x56, 0, 0x0002, 0) // V Up
	keybdEvent.Call(0x11, 0, 0x0002, 0) // Ctrl Up

	// Restore clipboard after a small delay
	go func() {
		time.Sleep(150 * time.Millisecond)
		if oldText != "" {
			clipboard.WriteAll(oldText)
		}
	}()
}

// Safely clear clipboard via Windows Win32 API without CMD/Powershell invocation
func clearWindowsClipboard() {
	user32 := syscall.NewLazyDLL("user32.dll")
	openClip := user32.NewProc("OpenClipboard")
	emptyClip := user32.NewProc("EmptyClipboard")
	closeClip := user32.NewProc("CloseClipboard")
	ret, _, _ := openClip.Call(0)
	if ret != 0 {
		emptyClip.Call()
		closeClip.Call()
	}
}

// Safely empty Windows Recycle Bin via Shell32 API without prompts
func emptyWindowsRecycleBin() {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	shEmpty := shell32.NewProc("SHEmptyRecycleBinW")
	// SHERB_NOCONFIRMATION (0x1) | SHERB_NOPROGRESSUI (0x2) | SHERB_NOSOUND (0x4) = 0x7
	shEmpty.Call(0, 0, 0x00000007)
}

// Simulate Hotkey
func simulateHotkey(value string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	keybdEvent := user32.NewProc("keybd_event")

	keys := strings.Split(value, "+")
	var vkDown []uintptr

	// Map modifiers
	keyMap := map[string]uintptr{
		"ctrl":       0x11, // VK_CONTROL
		"shift":      0x10, // VK_SHIFT
		"alt":        0x12, // VK_MENU
		"meta":       0x5B, // VK_LWIN
		"volumeup":   0xAF, // VK_VOLUME_UP
		"volumedown": 0xAE, // VK_VOLUME_DOWN
		"playpause":  0xB3, // VK_MEDIA_PLAY_PAUSE
		"nexttrack":  0xB0, // VK_MEDIA_NEXT_TRACK
		"prevtrack":  0xB1, // VK_MEDIA_PREV_TRACK
		"mute":       0xAD, // VK_VOLUME_MUTE
		"esc":        0x1B, // VK_ESCAPE
		"enter":      0x0D,
		"space":      0x20,
		"backspace":  0x08,
	}

	for _, k := range keys {
		kClean := strings.ToLower(strings.TrimSpace(k))
		var vk uintptr
		if val, exists := keyMap[kClean]; exists {
			vk = val
		} else if len(kClean) == 1 {
			// Alphanumeric keys
			ch := kClean[0]
			if ch >= 'a' && ch <= 'z' {
				vk = uintptr(ch - 'a' + 'A')
			} else if ch >= '0' && ch <= '9' {
				vk = uintptr(ch)
			}
		} else if strings.HasPrefix(kClean, "f") && len(kClean) > 1 {
			// F keys
			var fNum int
			fmt.Sscanf(kClean, "f%d", &fNum)
			if fNum >= 1 && fNum <= 24 {
				vk = uintptr(0x6F + fNum) // VK_F1 is 0x70
			}
		}

		if vk != 0 {
			keybdEvent.Call(vk, 0, 0, 0) // Down
			vkDown = append(vkDown, vk)
		}
	}

	// Release in reverse order
	for i := len(vkDown) - 1; i >= 0; i-- {
		keybdEvent.Call(vkDown[i], 0, 0x0002, 0) // Up
	}
}

// ----------------- Built-in Widget Actions (Pomodoro / Network Speed) -----------------

const (
	pomodoroWorkMinutes  = 25
	pomodoroBreakMinutes = 5
)

var (
	pomodoroMu      sync.Mutex
	pomodoroRunning bool
	pomodoroOnBreak bool
	pomodoroEndsAt  time.Time
	pomodoroRunID   int
)

// runSpecialStreamDeckAction executes the built-in pseudo actions emitted by the widget
// library ("pomodoro", "net_speed"). Returns true when the value was recognised as an
// action of its own instead of a hotkey/command.
func runSpecialStreamDeckAction(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pomodoro", "pomodoro_toggle":
		go togglePomodoro()
		return true
	case "net_speed", "net_stats", "network_speed":
		broadcast(buildNetworkSpeedPayload())
		return true
	}
	return false
}

func pomodoroSnapshotLocked() map[string]interface{} {
	phase := "idle"
	remaining := 0
	if pomodoroRunning {
		phase = "work"
		if pomodoroOnBreak {
			phase = "break"
		}
		remaining = int(time.Until(pomodoroEndsAt).Seconds())
		if remaining < 0 {
			remaining = 0
		}
	}
	return map[string]interface{}{
		"type":              "pomodoro",
		"running":           pomodoroRunning,
		"phase":             phase,
		"remaining_seconds": remaining,
		"work_minutes":      pomodoroWorkMinutes,
		"break_minutes":     pomodoroBreakMinutes,
	}
}

func pomodoroStatus() map[string]interface{} {
	pomodoroMu.Lock()
	defer pomodoroMu.Unlock()
	return pomodoroSnapshotLocked()
}

// togglePomodoro starts or stops the classic 25/5 focus cycle and notifies all clients.
func togglePomodoro() map[string]interface{} {
	pomodoroMu.Lock()
	if pomodoroRunning {
		pomodoroRunning = false
		pomodoroOnBreak = false
		pomodoroRunID++
		state := pomodoroSnapshotLocked()
		pomodoroMu.Unlock()
		broadcast(state)
		return state
	}

	pomodoroRunning = true
	pomodoroOnBreak = false
	pomodoroEndsAt = time.Now().Add(time.Duration(pomodoroWorkMinutes) * time.Minute)
	pomodoroRunID++
	runID := pomodoroRunID
	state := pomodoroSnapshotLocked()
	pomodoroMu.Unlock()

	broadcast(state)
	go pomodoroCycle(runID)
	return state
}

func pomodoroCycle(runID int) {
	for {
		pomodoroMu.Lock()
		if runID != pomodoroRunID || !pomodoroRunning {
			pomodoroMu.Unlock()
			return
		}
		wait := time.Until(pomodoroEndsAt)
		pomodoroMu.Unlock()

		if wait > 0 {
			time.Sleep(wait)
		}

		pomodoroMu.Lock()
		if runID != pomodoroRunID || !pomodoroRunning {
			pomodoroMu.Unlock()
			return
		}
		if !pomodoroOnBreak {
			pomodoroOnBreak = true
			pomodoroEndsAt = time.Now().Add(time.Duration(pomodoroBreakMinutes) * time.Minute)
			state := pomodoroSnapshotLocked()
			pomodoroMu.Unlock()
			broadcast(state)
			showNotification("BigPocket Pomodoro", fmt.Sprintf("Mola zamanı! %d dakika dinlen.", pomodoroBreakMinutes))
			continue
		}
		pomodoroRunning = false
		state := pomodoroSnapshotLocked()
		pomodoroMu.Unlock()
		broadcast(state)
		showNotification("BigPocket Pomodoro", "Odaklanma seansı tamamlandı. Harika iş!")
		return
	}
}

// buildNetworkSpeedPayload reports the instantaneous aggregate throughput.
func buildNetworkSpeedPayload() map[string]interface{} {
	down, up := getNetworkSpeedKbps()
	return map[string]interface{}{
		"type":      "net_speed",
		"down_kbps": down,
		"up_kbps":   up,
		"down_mbps": float64(down) / 1000.0,
		"up_mbps":   float64(up) / 1000.0,
	}
}

// networkInterfaceSummary lists the active adapters with their addresses for the
// get_network_stats MCP tool.
func networkInterfaceSummary() []map[string]interface{} {
	result := []map[string]interface{}{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	descriptions := interfaceDescriptions()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs := []string{}
		if list, err := iface.Addrs(); err == nil {
			for _, a := range list {
				if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
					addrs = append(addrs, ipnet.IP.String())
				}
			}
		}
		if len(addrs) == 0 {
			continue
		}
		result = append(result, map[string]interface{}{
			"name":        iface.Name,
			"description": descriptions[uint32(iface.Index)],
			"up":          iface.Flags&net.FlagUp != 0,
			"addresses":   addrs,
		})
	}
	return result
}

// ----------------- Auto Page Switching -----------------

// startAutoSwitchWatcher polls the foreground process every two seconds and publishes a
// page_switched event whenever a configured auto_switch_rules entry matches.
func startAutoSwitchWatcher() {
	lastProcess := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		_, process := getActiveWindowProcessName()
		if process == "" || process == lastProcess {
			continue
		}
		lastProcess = process

		cfg := loadConfig()
		for _, rule := range cfg.AutoSwitchRules {
			if rule.ProcessName == "" || !strings.EqualFold(rule.ProcessName, process) {
				continue
			}
			if cfg.ActivePage == rule.PageID {
				break
			}
			cfg.ActivePage = rule.PageID
			saveConfig(cfg)
			broadcast(map[string]interface{}{
				"type":        "page_switched",
				"active_page": rule.PageID,
				"buttons":     cfg.StreamDeckButtons,
				"pages":       cfg.StreamDeckPages,
				"reason":      "auto_switch",
				"process":     process,
			})
			break
		}
	}
}

// ----------------- Clipboard Polling -----------------
func startClipboardPolling() {
	for {
		text, err := clipboard.ReadAll()
		if err == nil && text != "" && text != lastClipboardText {
			lastClipboardText = text
			broadcast(map[string]interface{}{
				"type": "clipboard_sync",
				"text": text,
			})
		}
		time.Sleep(1 * time.Second)
	}
}

// ----------------- Notifications -----------------
func showNotification(title, message string) {
	// Native balloon notification through Shell_NotifyIconW: no PowerShell, no
	// command injection surface at all.
	showNotificationNative(title, message)
}

// ----------------- HTTP Auth Helpers -----------------
// Origin validation, constant-time password checks, brute-force throttling and the
// authorized-IP registry all live in security.go.

// ----------------- HTTP Routes -----------------
func setupHttpRoutes() {
	// Serve Logo SVG
	http.HandleFunc("/logo.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		svgPath := filepath.Join(frontendDir, "logo.svg")
		if data, err := os.ReadFile(svgPath); err == nil {
			w.Write(data)
			return
		}
		modernSVG := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128" width="100%" height="100%">
  <rect x="4" y="4" width="120" height="120" rx="30" ry="30" fill="#0E0E12" stroke="#272730" stroke-width="2"/>
  <rect x="44" y="20" width="40" height="48" rx="8" ry="8" fill="#818CF8" stroke="#FFFFFF" stroke-width="2"/>
  <rect x="56" y="25" width="16" height="3" rx="1.5" ry="1.5" fill="#FFFFFF" opacity="0.9"/>
  <path d="M34 48 L34 70 C34 90, 48 100, 64 100 C80 100, 94 90, 94 70 L94 48 Z" fill="#181820" stroke="#FFFFFF" stroke-width="5" stroke-linejoin="round" stroke-linecap="round"/>
  <line x1="28" y1="48" x2="100" y2="48" stroke="#FFFFFF" stroke-width="6" stroke-linecap="round"/>
  <path d="M44 60 C44 76, 52 86, 64 86 C76 86, 84 76, 84 60" fill="none" stroke="#C084FC" stroke-width="3.5" stroke-linecap="round"/>
  <circle cx="64" cy="73" r="4" fill="#FFFFFF"/>
</svg>`
		w.Write([]byte(modernSVG))
	})

	// Serve frontend directory
	fs := http.FileServer(http.Dir(frontendDir))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		fs.ServeHTTP(w, r)
	})

	// System Info endpoint
	http.HandleFunc("/system_info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		ips := []string{}
		addrs, err := net.InterfaceAddrs()
		if err == nil {
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						ips = append(ips, ipnet.IP.String())
					}
				}
			}
		}

		// Detect USB Tethering IP adapter specifically
		usbIP := getUSBTetheringIP()

		ipAddr := "127.0.0.1"
		if usbIP != "" {
			ipAddr = usbIP
		} else {
			// Fallback to common subnets or first non-loopback IP
			for _, ipStr := range ips {
				// Check for common tethering/hotspot subnets
				if strings.HasPrefix(ipStr, "192.168.42.") ||
					strings.HasPrefix(ipStr, "192.168.43.") ||
					strings.HasPrefix(ipStr, "192.168.49.") ||
					strings.HasPrefix(ipStr, "192.168.225.") ||
					strings.HasPrefix(ipStr, "192.168.137.") ||
					strings.HasPrefix(ipStr, "172.20.10.") {
					ipAddr = ipStr
					break
				}
			}
			if ipAddr == "127.0.0.1" && len(ips) > 0 {
				for _, item := range ips {
					if !strings.HasPrefix(item, "169.254.") {
						ipAddr = item
						break
					}
				}
				if ipAddr == "127.0.0.1" {
					ipAddr = ips[0]
				}
			}
		}

		isAdmin := checkIfAdmin()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"ip":      ipAddr,
			"ips":     ips,
			"usb_ip":  usbIP,
			"ports": map[string]int{
				"control_ws": 8085,
				"camera_tcp": 8083,
				"audio_tcp":  8084,
				"screen_tcp": 8086,
				"http_api":   8085,
			},
			"is_admin": isAdmin,
			"hostname": func() string { h, _ := os.Hostname(); return h }(),
			"mem_stats": func() map[string]interface{} {
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				return map[string]interface{}{
					"alloc_mb": fmt.Sprintf("%.1f MB", float64(m.Alloc)/(1024*1024)),
					"sys_mb":   fmt.Sprintf("%.1f MB", float64(m.Sys)/(1024*1024)),
				}
			}(),
		})
	})

	// API configs
	http.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var incoming Config
			err := json.NewDecoder(r.Body).Decode(&incoming)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			// Merge against the stored config so partial payloads (mobile/MCP clients
			// that omit clock_font_size, stream_deck_pages, ...) never erase data.
			existing := loadConfig()
			cfg := mergeConfig(incoming, existing)
			cfg.Rev = existing.Rev + 1

			// Automatically extract icons for command buttons if possible
			for i := range cfg.StreamDeckButtons {
				btn := &cfg.StreamDeckButtons[i]
				if btn.Type == "command" && btn.Value != "" {
					lowerVal := strings.ToLower(btn.Value)
					if strings.HasSuffix(lowerVal, ".exe") || strings.HasSuffix(lowerVal, ".lnk") || strings.Contains(btn.Value, `\`) || strings.Contains(btn.Value, `/`) {
						iconPath, err := extractAppIconNative(btn.Value, btn.ID)
						if err == nil && iconPath != "" {
							btn.Icon = "custom:" + iconPath
						}
					}
				}
			}

			saveConfig(cfg)
			cfg = loadConfig()
			source := r.Header.Get("X-Client-Id")
			broadcast(map[string]interface{}{"type": "config_update", "source": source})
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "config": cfg})
		} else {
			cfg := loadConfig()
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "config": cfg})
		}
	})

	// Shutdown server
	http.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Server shutting down."})
		fmt.Println("Shutdown request received. Exiting.")
		go func() {
			time.Sleep(1 * time.Second)
			cleanupOnExit()
			os.Exit(0)
		}()
	})

	// List Shared Files
	http.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		files, err := os.ReadDir(sharedDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var fileList []map[string]interface{}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			fileList = append(fileList, map[string]interface{}{
				"name":     f.Name(),
				"size":     info.Size(),
				"modified": info.ModTime().Unix(),
			})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "files": fileList})
	})

	// Open Shared Files Folder in Windows Explorer
	http.HandleFunc("/open_shared_folder", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		execCommandHidden("explorer.exe", sharedDir).Start()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})

	// Download File
	http.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		filePath, err := safeSharedFilePath(strings.TrimPrefix(r.URL.Path, "/download/"))
		if err != nil {
			http.Error(w, "Invalid file name", http.StatusBadRequest)
			return
		}
		http.ServeFile(w, r, filePath)
	})

	// Delete File
	http.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		if r.Method == http.MethodDelete {
			filePath, err := safeSharedFilePath(strings.TrimPrefix(r.URL.Path, "/files/"))
			if err != nil {
				http.Error(w, "Invalid file name", http.StatusBadRequest)
				return
			}
			err = os.Remove(filePath)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Upload File
	http.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(w, r) {
			return
		}
		err := r.ParseMultipartForm(32 << 20) // 32MB max
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		file, handler, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		filePath, err := safeSharedFilePath(handler.Filename)
		if err != nil {
			http.Error(w, "Invalid file name", http.StatusBadRequest)
			return
		}
		dst, err := os.Create(filePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer dst.Close()

		_, err = io.Copy(dst, file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ext := strings.ToLower(filepath.Ext(handler.Filename))
		msgType := "file"
		if isAllowedIconExtension(ext) {
			msgType = "image"
		}
		fi, _ := dst.Stat()
		var sz int64 = 0
		if fi != nil {
			sz = fi.Size()
		}
		addFlowMessage(FlowMessage{
			Sender:   "phone",
			Type:     msgType,
			Text:     handler.Filename,
			FileName: handler.Filename,
			FileSize: sz,
			FileURL:  "/download/" + handler.Filename,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Uploaded " + handler.Filename})
	})

	// Camera MJPEG Stream
	http.HandleFunc("/video_feed", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		for {
			latestCameraLock.RLock()
			frame := latestCameraFrame
			latestCameraLock.RUnlock()

			if frame != nil {
				_, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame))
				if err != nil {
					break
				}
				_, err = w.Write(frame)
				if err != nil {
					break
				}
				_, err = fmt.Fprint(w, "\r\n")
				if err != nil {
					break
				}
			}
			time.Sleep(40 * time.Millisecond) // ~25 FPS
		}
	})

	// Driver management routes
	http.HandleFunc("/driver/status", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		isAdmin := checkIfAdmin()
		driverExists := false
		if _, err := os.Stat(filepath.Join(driverDir, "deviceinstaller64.exe")); err == nil {
			driverExists = true
		}

		// Get active monitors count using screenshot library
		numMonitors := screenshot.NumActiveDisplays()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"admin":         isAdmin,
			"monitors":      numMonitors,
			"driver_exists": driverExists,
		})
	})

	http.HandleFunc("/driver/install", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		success, msg := installDriver()
		json.NewEncoder(w).Encode(map[string]interface{}{"success": success, "message": msg})
	})

	http.HandleFunc("/driver/enable", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(w, r) {
			return
		}
		var payload map[string]interface{}
		json.NewDecoder(r.Body).Decode(&payload)
		enable, _ := payload["enable"].(bool)

		w.Header().Set("Content-Type", "application/json")
		success, msg := setVirtualMonitor(enable)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": success, "message": msg})
	})

	http.HandleFunc("/driver/mic/status", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		installed := checkMicDriverExists()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"installed": installed,
		})
	})

	http.HandleFunc("/driver/mic/install", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		success, msg := installMicDriver()
		json.NewEncoder(w).Encode(map[string]interface{}{"success": success, "message": msg})
	})

	http.HandleFunc("/open_sound_settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(w, r) {
			return
		}
		execCommandHidden("control", "mmsys.cpl").Start()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})

	// Upload Stream Deck Custom Icon
	http.HandleFunc("/upload_icon", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(w, r) {
			return
		}
		err := r.ParseMultipartForm(10 << 20) // 10MB max
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		buttonIDStr := r.FormValue("button_id")
		if buttonIDStr == "" {
			http.Error(w, "Missing button_id", http.StatusBadRequest)
			return
		}

		var buttonID int
		if _, err := fmt.Sscanf(buttonIDStr, "%d", &buttonID); err != nil {
			http.Error(w, "Invalid button_id", http.StatusBadRequest)
			return
		}

		file, handler, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		ext := strings.ToLower(filepath.Ext(filepath.Base(handler.Filename)))
		if !isAllowedIconExtension(ext) {
			ext = ".png"
		}

		iconsDir := filepath.Join(frontendDir, "icons")
		if err := os.MkdirAll(iconsDir, 0755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		iconFilename := fmt.Sprintf("button_%d%s", buttonID, ext)
		iconPath, err := safeJoinPath(iconsDir, iconFilename)
		if err != nil {
			http.Error(w, "Invalid icon file name", http.StatusBadRequest)
			return
		}

		dst, err := os.Create(iconPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer dst.Close()

		_, err = io.Copy(dst, file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Update button config
		cfg := loadConfig()
		updated := false
		for i, btn := range cfg.StreamDeckButtons {
			if btn.ID == buttonID {
				cfg.StreamDeckButtons[i].Icon = "custom:icons/" + iconFilename
				updated = true
				break
			}
		}

		if updated {
			saveConfig(cfg)
			source := r.Header.Get("X-Client-Id")
			broadcast(map[string]interface{}{"type": "config_update", "source": source})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"icon":    "custom:icons/" + iconFilename,
		})
	})

	// Extract icon from EXE or LNK
	http.HandleFunc("/extract_icon", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(w, r) {
			return
		}
		var payload map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		appPath, _ := payload["path"].(string)
		buttonIDFloat, _ := payload["button_id"].(float64)
		buttonID := int(buttonIDFloat)

		w.Header().Set("Content-Type", "application/json")
		if appPath == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Missing path"})
			return
		}

		iconPath, err := extractAppIconNative(appPath, buttonID)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "icon": iconPath})
	})

	// List Installed Apps on Windows
	http.HandleFunc("/installed_apps", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		apps := getInstalledApps()
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "apps": apps})
	})

	// Trigger Stream Deck button directly via HTTP (useful for MCP and Web UI Test button)
	http.HandleFunc("/stream_deck_trigger", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(w, r) {
			return
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		btnIDFloat, ok := payload["button_id"].(float64)
		if !ok {
			http.Error(w, "Missing or invalid button_id", http.StatusBadRequest)
			return
		}
		btnID := int(btnIDFloat)
		cfg := loadConfig()
		var targetBtn *StreamDeckButton
		for _, b := range cfg.StreamDeckButtons {
			if b.ID == btnID {
				targetBtn = &b
				break
			}
		}
		if targetBtn == nil {
			http.Error(w, "Button not found", http.StatusNotFound)
			return
		}

		if targetBtn.Type == "hotkey" {
			if !runSpecialStreamDeckAction(targetBtn.Value) {
				simulateHotkey(targetBtn.Value)
			}
		} else if targetBtn.Type == "now_playing" {
			simulateHotkey("playpause")
		} else if targetBtn.Type == "command" {
			go func(cmd string) { _ = launchCommandNative(cmd) }(targetBtn.Value)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"button":  targetBtn,
		})
	})

	// MCP Endpoint (HTTP POST JSON-RPC 2.0 & GET SSE)
	http.HandleFunc("/mcp", handleMCPEndpoint)
	http.HandleFunc("/mcp/sse", handleMCPEndpoint)

	// Direct REST API Endpoints
	http.HandleFunc("/api/status", handleAPIStatus)
	http.HandleFunc("/api/streamdeck", handleAPIStreamDeck)
	http.HandleFunc("/api/streamdeck/pages", handleAPIStreamDeckPages)
	http.HandleFunc("/api/streamdeck/volume", handleAPIStreamDeckVolume)
	http.HandleFunc("/api/streamdeck/trigger", handleAPIStreamDeckTrigger)
	http.HandleFunc("/api/streamdeck/button", handleAPIStreamDeckButton)
	http.HandleFunc("/api/streamdeck/buttons", handleAPIStreamDeckButton)
	http.HandleFunc("/api/streamdeck/presets", handleAPIStreamDeckPresets)
	http.HandleFunc("/api/streamdeck/layout", handleAPIStreamDeckLayout)
	http.HandleFunc("/api/system/stats", handleAPISystemStats)
	http.HandleFunc("/api/usb/forward", handleAPIUsbForward)
	http.HandleFunc("/api/apps", handleAPIApps)
	http.HandleFunc("/api/clipboard", handleAPIClipboard)
	http.HandleFunc("/api/notify", handleAPINotify)

	// Offline QR code (pure Go, no external QR service)
	http.HandleFunc("/api/qr", handleAPIQR)

	// Windows autostart management
	http.HandleFunc("/api/autostart", handleAPIAutostart)

	// Extra telemetry / control endpoints backed by the native helpers
	http.HandleFunc("/api/system/active_window", handleAPIActiveWindow)
	http.HandleFunc("/api/system/network", handleAPINetwork)
	http.HandleFunc("/api/weather", handleAPIWeather)
	http.HandleFunc("/api/media/now_playing", handleAPINowPlaying)
	http.HandleFunc("/api/screen/capture", handleAPIScreenCapture)
	http.HandleFunc("/api/pomodoro", handleAPIPomodoro)
	http.HandleFunc("/api/pomodoro/toggle", handleAPIPomodoro)

	// Per-application Core Audio (WASAPI) session mixer
	http.HandleFunc("/api/audio_sessions", handleAPIAudioSessions)

	// BigPocket Local Flow (WhatsApp-Style Sharing)
	initFlow()
}

// ----------------- Model Context Protocol (MCP) & REST API -----------------

type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type MCPTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

var mcpToolsList = []MCPTool{
	{
		Name:        "get_system_status",
		Description: "Gets BigPocket PC server status, IP addresses, ports, and system resource statistics.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_streamdeck_config",
		Description: "Retrieves current Stream Deck configuration, grid layout (rows, cols), active page, all pages, and all buttons with full properties.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_streamdeck_presets",
		Description: "Returns all supported Stream Deck widget types, presets, clock styles, font families, background styles, and studio templates.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "set_streamdeck_button",
		Description: "Configures or edits an existing Stream Deck button slot with extreme precision (label, type, value, icon, page, col_span, row_span, grid_row, grid_col, color, bg_style, clock_style, font_family, clock_font_size).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"button_id": map[string]interface{}{
					"type":        "integer",
					"description": "Button slot index (0-based)",
				},
				"label": map[string]interface{}{
					"type":        "string",
					"description": "Display label for the button",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"hotkey", "command", "url", "text_snippet", "toggle", "volume_slider", "app_volume_slider", "brightness_slider", "live_info", "clock_widget", "media_widget"},
					"description": "Action or widget type",
				},
				"value": map[string]interface{}{
					"type":        "string",
					"description": "Hotkey string (e.g. 'ctrl+shift+m'), command ('calc.exe'), URL, snippet text, or widget parameter ('master_volume', 'display_brightness', 'cpu', 'ram')",
				},
				"icon": map[string]interface{}{
					"type":        "string",
					"description": "Icon key ('mic', 'volup', 'voldown', 'volume', 'play', 'calc', 'web', 'lock', 'task', 'cpu', 'ram', 'clock', 'desktop', 'link', 'terminal', 'folder', 'wifi', 'camera', 'monitor', 'bell', or 'custom:...')",
				},
				"page": map[string]interface{}{
					"type":        "integer",
					"description": "Page index (0-based)",
				},
				"col_span": map[string]interface{}{
					"type":        "integer",
					"description": "Width span in grid cells (1 to 4)",
				},
				"row_span": map[string]interface{}{
					"type":        "integer",
					"description": "Height span in grid cells (1 to 2)",
				},
				"grid_row": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit 0-based bento row; -1 (default) lets the packer drop the card into the first free cell",
				},
				"grid_col": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit 0-based bento column; -1 (default) lets the packer drop the card into the first free cell",
				},
				"color": map[string]interface{}{
					"type":        "string",
					"description": "Optional accent hex override; leave empty for the shared monochrome surface (default theme, e.g. '#F4F4F5')",
				},
				"bg_style": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"default", "transparent", "glass", "outline"},
					"description": "Card background style",
				},
				"font_family": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"mono", "sans", "lcd", "serif", "tech"},
					"description": "Typography style",
				},
				"clock_style": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"apple_lockscreen", "apple_stacked", "analog", "digital_seconds", "digital_clean"},
					"description": "Clock display mode (only for clock_widget)",
				},
				"clock_font_size": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"auto", "compact", "regular", "large", "huge"},
					"description": "Clock text size",
				},
			},
			"required": []string{"button_id"},
		},
	},
	{
		Name:        "add_streamdeck_button",
		Description: "Creates and appends a brand new Stream Deck button or widget to a page. Can optionally base it on a preset ('clock', 'clock_analog', 'volume', 'app_session', 'brightness', 'cpu', 'ram', 'media', 'toggle_mic', 'toggle_mute', 'app_calc', 'app_lock', 'app_terminal', 'app_discord', 'app_spotify').",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"preset": map[string]interface{}{
					"type":        "string",
					"description": "Optional preset key to copy defaults from",
				},
				"page": map[string]interface{}{
					"type":        "integer",
					"description": "Page index to place the button on (default active page)",
				},
				"label": map[string]interface{}{
					"type":        "string",
					"description": "Display label for the button",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"hotkey", "command", "url", "text_snippet", "toggle", "volume_slider", "app_volume_slider", "brightness_slider", "live_info", "clock_widget", "media_widget"},
					"description": "Action or widget type",
				},
				"value": map[string]interface{}{
					"type":        "string",
					"description": "Action value or widget parameter",
				},
				"icon": map[string]interface{}{
					"type":        "string",
					"description": "Icon key or custom path",
				},
				"col_span": map[string]interface{}{
					"type":        "integer",
					"description": "Width span in grid cells (1 to 4)",
				},
				"row_span": map[string]interface{}{
					"type":        "integer",
					"description": "Height span in grid cells (1 to 2)",
				},
				"grid_row": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit 0-based bento row; -1 (default) lets the packer drop the card into the first free cell",
				},
				"grid_col": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit 0-based bento column; -1 (default) lets the packer drop the card into the first free cell",
				},
				"color": map[string]interface{}{
					"type":        "string",
					"description": "Optional accent hex override; leave empty for the monochrome surface (#F4F4F5)",
				},
				"bg_style": map[string]interface{}{
					"type": "string",
					"enum": []string{"default", "transparent", "glass", "outline"},
				},
				"font_family": map[string]interface{}{
					"type": "string",
					"enum": []string{"mono", "sans", "lcd", "serif", "tech"},
				},
				"clock_style": map[string]interface{}{
					"type": "string",
					"enum": []string{"apple_lockscreen", "apple_stacked", "analog", "digital_seconds", "digital_clean"},
				},
				"clock_font_size": map[string]interface{}{
					"type": "string",
					"enum": []string{"auto", "compact", "regular", "large", "huge"},
				},
			},
		},
	},
	{
		Name:        "create_custom_widget",
		Description: "Convenience tool to build a customized Stream Deck widget or button from scratch (hotkey, command, URL, live info, clock, volume/brightness slider, or toggle) with full visual parameters.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"label": map[string]interface{}{
					"type":        "string",
					"description": "Label to display on the button",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"hotkey", "command", "url", "text_snippet", "toggle", "volume_slider", "app_volume_slider", "brightness_slider", "live_info", "clock_widget", "media_widget"},
					"description": "Widget or action type",
				},
				"value": map[string]interface{}{
					"type":        "string",
					"description": "Action value, hotkey (e.g. 'ctrl+shift+m'), command ('calc.exe'), URL, or parameter",
				},
				"icon": map[string]interface{}{
					"type":        "string",
					"description": "Icon identifier (e.g. 'mic', 'play', 'zap', 'folder', 'terminal', 'clock', 'cpu', 'volume', 'bright', 'lock', 'web', 'link')",
				},
				"page": map[string]interface{}{
					"type":        "integer",
					"description": "Target page index (0-based, default active page)",
				},
				"col_span": map[string]interface{}{
					"type":        "integer",
					"description": "Width span in columns (1-4)",
				},
				"row_span": map[string]interface{}{
					"type":        "integer",
					"description": "Height span in rows (1-2)",
				},
				"grid_row": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit 0-based bento row; -1 (default) lets the packer drop the card into the first free cell",
				},
				"grid_col": map[string]interface{}{
					"type":        "integer",
					"description": "Explicit 0-based bento column; -1 (default) lets the packer drop the card into the first free cell",
				},
				"color": map[string]interface{}{
					"type":        "string",
					"description": "Optional accent hex override; leave empty for the monochrome surface (#F4F4F5)",
				},
				"bg_style": map[string]interface{}{
					"type": "string",
					"enum": []string{"default", "transparent", "glass", "outline"},
				},
				"font_family": map[string]interface{}{
					"type": "string",
					"enum": []string{"mono", "sans", "lcd", "serif", "tech"},
				},
				"clock_style": map[string]interface{}{
					"type": "string",
					"enum": []string{"apple_lockscreen", "apple_stacked", "analog", "digital_seconds", "digital_clean"},
				},
				"clock_font_size": map[string]interface{}{
					"type": "string",
					"enum": []string{"auto", "compact", "regular", "large", "huge"},
				},
			},
			"required": []string{"label", "type", "value"},
		},
	},
	{
		Name:        "batch_create_streamdeck_buttons",
		Description: "Creates multiple Stream Deck buttons or widgets in one single operation. Ideal for generating entire custom dashboards (gaming, coding, streaming, audio control).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"page": map[string]interface{}{
					"type":        "integer",
					"description": "Page index to add buttons to (default active page)",
				},
				"clear_page_first": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, clears existing buttons on target page before adding new ones",
				},
				"buttons": map[string]interface{}{
					"type":        "array",
					"description": "Array of button definitions (preset or custom properties)",
				},
			},
			"required": []string{"buttons"},
		},
	},
	{
		Name:        "duplicate_streamdeck_button",
		Description: "Duplicates an existing Stream Deck button to a target page or the active page.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"button_id": map[string]interface{}{
					"type":        "integer",
					"description": "Button ID to duplicate",
				},
				"target_page": map[string]interface{}{
					"type":        "integer",
					"description": "Target page index (optional, defaults to same page)",
				},
			},
			"required": []string{"button_id"},
		},
	},
	{
		Name:        "delete_streamdeck_button",
		Description: "Deletes a specific Stream Deck button by its ID.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"button_id": map[string]interface{}{
					"type":        "integer",
					"description": "The button ID to remove",
				},
			},
			"required": []string{"button_id"},
		},
	},
	{
		Name:        "swap_streamdeck_buttons",
		Description: "Swaps the visual position of two Stream Deck buttons.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"button_id_1": map[string]interface{}{
					"type":        "integer",
					"description": "First button ID",
				},
				"button_id_2": map[string]interface{}{
					"type":        "integer",
					"description": "Second button ID",
				},
			},
			"required": []string{"button_id_1", "button_id_2"},
		},
	},
	{
		Name:        "set_grid_layout",
		Description: "Changes Stream Deck grid dimensions (number of rows and columns) and updates layout.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"rows": map[string]interface{}{
					"type":        "integer",
					"description": "Number of rows (1 to 5)",
				},
				"cols": map[string]interface{}{
					"type":        "integer",
					"description": "Number of columns (2 to 8)",
				},
			},
			"required": []string{"rows", "cols"},
		},
	},
	{
		Name:        "trigger_button",
		Description: "Directly triggers and executes a Stream Deck button action on the PC (simulates hotkey or runs command).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"button_id": map[string]interface{}{
					"type":        "integer",
					"description": "The button slot index to trigger (0-based)",
				},
			},
			"required": []string{"button_id"},
		},
	},
	{
		Name:        "streamdeck_toggle_button",
		Description: "Toggles the state ('on' <-> 'off') and triggers the action of a toggle-type Stream Deck button (mic mute, speaker mute, virtual monitor, or custom).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"button_id": map[string]interface{}{
					"type":        "integer",
					"description": "The button slot index to toggle (0-based)",
				},
			},
			"required": []string{"button_id"},
		},
	},
	{
		Name:        "streamdeck_switch_page",
		Description: "Switches the active Stream Deck tab/page across mobile and desktop interfaces.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"page": map[string]interface{}{
					"type":        "integer",
					"description": "Page index (0, 1, 2...)",
				},
			},
			"required": []string{"page"},
		},
	},
	{
		Name:        "create_streamdeck_page",
		Description: "Creates a brand new Stream Deck page tab.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Title of the new page (e.g. 'Oyun Masası', 'Canlı Yayın', 'Kodlama')",
				},
			},
			"required": []string{"title"},
		},
	},
	{
		Name:        "rename_streamdeck_page",
		Description: "Renames an existing Stream Deck page.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"page_id": map[string]interface{}{
					"type":        "integer",
					"description": "The page ID to rename",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "New title for the page",
				},
			},
			"required": []string{"page_id", "title"},
		},
	},
	{
		Name:        "delete_streamdeck_page",
		Description: "Deletes a Stream Deck page and all buttons belonging to it.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"page_id": map[string]interface{}{
					"type":        "integer",
					"description": "The page ID to delete",
				},
			},
			"required": []string{"page_id"},
		},
	},
	{
		Name:        "clear_streamdeck_page",
		Description: "Clears all buttons on a specific page.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"page_id": map[string]interface{}{
					"type":        "integer",
					"description": "The page ID to clear (defaults to active page if omitted)",
				},
			},
		},
	},
	{
		Name:        "apply_streamdeck_template",
		Description: "Applies a curated studio template to a page ('pro_studio', 'media_lounge', 'gaming_obs', 'developer', 'system_control').",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"template": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"pro_studio", "media_lounge", "gaming_obs", "developer", "system_control"},
					"description": "Template design name",
				},
				"page": map[string]interface{}{
					"type":        "integer",
					"description": "Page index to apply the template to (defaults to active page)",
				},
			},
			"required": []string{"template"},
		},
	},
	{
		Name:        "streamdeck_set_volume",
		Description: "Controls master PC volume ('up', 'down', 'mute', or 'set' with a percentage 0-100).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"set", "up", "down", "mute"},
					"description": "Volume action to perform",
				},
				"level": map[string]interface{}{
					"type":        "number",
					"description": "Volume percentage level (0 to 100), used when action is 'set'",
				},
			},
			"required": []string{"action"},
		},
	},
	{
		Name:        "streamdeck_set_brightness",
		Description: "Controls PC display brightness percentage (0-100).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"level": map[string]interface{}{
					"type":        "integer",
					"description": "Brightness percentage level (0 to 100)",
				},
			},
			"required": []string{"level"},
		},
	},
	{
		Name:        "list_installed_apps",
		Description: "Fetches list of all installed Windows applications and desktop shortcuts with paths.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "send_pc_notification",
		Description: "Displays a desktop toast/balloon notification on the PC.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Notification title",
				},
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Notification body text",
				},
			},
			"required": []string{"title", "message"},
		},
	},
	{
		Name:        "sync_clipboard",
		Description: "Sets the PC clipboard text and syncs it with connected mobile devices.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{
					"type":        "string",
					"description": "The text to copy to the PC clipboard",
				},
			},
			"required": []string{"text"},
		},
	},
	{
		Name:        "get_system_stats",
		Description: "Retrieves instantaneous real-time PC CPU usage and RAM usage percentages.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "setup_usb_reverse",
		Description: "Sets up ADB reverse port forwarding over physical USB cable for 1ms ultra-low latency.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "capture_screen",
		Description: "Captures the primary display and stores it as a PNG file on the PC, returning the saved path.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_active_window",
		Description: "Returns the title and executable name (e.g. 'chrome.exe') of the currently focused window.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_network_stats",
		Description: "Returns real-time download/upload throughput of every active adapter plus the adapter list.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "net_speed",
		Description: "Returns the instantaneous aggregate download and upload speed in kbps.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "pomodoro_toggle",
		Description: "Starts or stops the built-in Pomodoro focus timer (25 minutes work / 5 minutes break) and broadcasts its state.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "set_autostart",
		Description: "Enables or disables launching BigPocket automatically with Windows (HKCU Run registry entry).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"enable": map[string]interface{}{
					"type":        "boolean",
					"description": "true to start BigPocket with Windows, false to disable",
				},
			},
			"required": []string{"enable"},
		},
	},
}

func handleMCPEndpoint(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Password")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Unauthorized"})
		return
	}

	if r.Method == http.MethodGet {
		// If client requests SSE stream
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			flusher, ok := w.(http.Flusher)
			if ok {
				fmt.Fprintf(w, "event: endpoint\ndata: /mcp\n\n")
				flusher.Flush()
			}
			<-r.Context().Done()
			return
		}

		// Regular GET: return server discovery info
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name":        "bigpocket-mcp",
			"version":     AppVersion,
			"status":      "running",
			"transports":  []string{"http", "sse", "stdio"},
			"endpoint":    "http://127.0.0.1:8085/mcp",
			"tools_count": len(mcpToolsList),
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &MCPError{Code: -32700, Message: "Parse error", Data: err.Error()},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch req.Method {
	case "initialize":
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{
						"listChanged": false,
					},
				},
				"serverInfo": map[string]interface{}{
					"name":    "bigpocket-streamdeck-mcp",
					"version": AppVersion,
				},
			},
		})

	case "notifications/initialized":
		w.WriteHeader(http.StatusNoContent)

	case "ping":
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{},
		})

	case "tools/list":
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": mcpToolsList,
			},
		})

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			json.NewEncoder(w).Encode(MCPResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &MCPError{Code: -32602, Message: "Invalid params", Data: err.Error()},
			})
			return
		}

		resStr, isErr := executeInternalMCPTool(params.Name, params.Arguments)
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": resStr,
					},
				},
				"isError": isErr,
			},
		})

	default:
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &MCPError{Code: -32601, Message: "Method not found: " + req.Method},
		})
	}
}

func buildStreamDeckButton(id int, defaultPage int, args map[string]interface{}) StreamDeckButton {
	targetPage := defaultPage
	if pFloat, ok := args["page"].(float64); ok {
		targetPage = int(pFloat)
	}

	newBtn := StreamDeckButton{
		ID:         id,
		Label:      "Özel Buton",
		Type:       "hotkey",
		Value:      "ctrl+c",
		Icon:       "custom",
		Page:       targetPage,
		ColSpan:    1,
		RowSpan:    1,
		GridRow:    gridAutoPlacement,
		GridCol:    gridAutoPlacement,
		Color:      "",
		BgStyle:    "default",
		FontFamily: "mono",
	}

	if preset, ok := args["preset"].(string); ok && preset != "" {
		switch preset {
		case "clock":
			newBtn.Label = "Saat & Tarih"
			newBtn.Type = "clock_widget"
			newBtn.Icon = "clock"
			newBtn.ColSpan = 2
		case "clock_analog":
			newBtn.Label = "Kadran"
			newBtn.Type = "clock_widget"
			newBtn.Icon = "clock"
			newBtn.ClockStyle = "analog"
		case "clock_seconds":
			newBtn.Label = "Canlı Dijital"
			newBtn.Type = "clock_widget"
			newBtn.Icon = "clock"
			newBtn.ClockStyle = "digital_seconds"
			newBtn.ColSpan = 2
		case "clock_digital_clean":
			newBtn.Label = "Sade Saat"
			newBtn.Type = "clock_widget"
			newBtn.Icon = "clock"
			newBtn.ClockStyle = "digital_clean"
			newBtn.ColSpan = 1
		case "mini_clock":
			newBtn.Label = "Saat"
			newBtn.Type = "clock_widget"
			newBtn.Icon = "clock"
			newBtn.ClockStyle = "apple_stacked"
		case "telemetry_combo":
			newBtn.Label = "Donanım Telemetri"
			newBtn.Type = "live_info"
			newBtn.Value = "combo"
			newBtn.Icon = "cpu"
			newBtn.ColSpan = 2
		case "cpu":
			newBtn.Label = "CPU Yükü"
			newBtn.Type = "live_info"
			newBtn.Value = "cpu"
			newBtn.Icon = "cpu"
		case "ram":
			newBtn.Label = "RAM Bellek"
			newBtn.Type = "live_info"
			newBtn.Value = "ram"
			newBtn.Icon = "ram"
		case "volume":
			newBtn.Label = "Ana Ses"
			newBtn.Type = "volume_slider"
			newBtn.Value = "master_volume"
			newBtn.Icon = "volume"
			newBtn.ColSpan = 2
		case "app_session":
			newBtn.Label = "Uygulama Sesi"
			newBtn.Type = "app_volume_slider"
			newBtn.Icon = "app_session"
			newBtn.ColSpan = 2
		case "volume_up":
			newBtn.Label = "Ses +"
			newBtn.Type = "hotkey"
			newBtn.Value = "volumeup"
			newBtn.Icon = "volup"
		case "volume_down":
			newBtn.Label = "Ses -"
			newBtn.Type = "hotkey"
			newBtn.Value = "volumedown"
			newBtn.Icon = "voldown"
		case "brightness":
			newBtn.Label = "Parlaklık"
			newBtn.Type = "brightness_slider"
			newBtn.Value = "display_brightness"
			newBtn.Icon = "bright"
			newBtn.ColSpan = 2
		case "media":
			newBtn.Label = "Medya Masası"
			newBtn.Type = "media_widget"
			newBtn.Value = "playpause"
			newBtn.Icon = "play"
			newBtn.ColSpan = 2
		case "media_next":
			newBtn.Label = "Sonraki Parça"
			newBtn.Type = "hotkey"
			newBtn.Value = "nexttrack"
			newBtn.Icon = "play"
		case "media_prev":
			newBtn.Label = "Önceki Parça"
			newBtn.Type = "hotkey"
			newBtn.Value = "prevtrack"
			newBtn.Icon = "play"
		case "toggle_mic":
			newBtn.Label = "Mikrofon"
			newBtn.Type = "toggle"
			newBtn.Value = "mic_mute"
			newBtn.Icon = "mic"
		case "toggle_mute":
			newBtn.Label = "Sesi Kapat"
			newBtn.Type = "toggle"
			newBtn.Value = "sound_mute"
			newBtn.Icon = "voldown"
		case "toggle_monitor":
			newBtn.Label = "Sanal Ekran"
			newBtn.Type = "toggle"
			newBtn.Value = "virtual_monitor"
			newBtn.Icon = "custom"
		case "app_taskmgr":
			newBtn.Label = "Görev Yöneticisi"
			newBtn.Type = "command"
			newBtn.Value = "taskmgr.exe"
			newBtn.Icon = "task"
		case "app_calc":
			newBtn.Label = "Hesap Makinesi"
			newBtn.Type = "command"
			newBtn.Value = "calc.exe"
			newBtn.Icon = "calc"
		case "app_lock":
			newBtn.Label = "PC Kilitle"
			newBtn.Type = "command"
			newBtn.Value = "rundll32.exe user32.dll,LockWorkStation"
			newBtn.Icon = "lock"
		case "app_terminal":
			newBtn.Label = "Terminal"
			newBtn.Type = "command"
			newBtn.Value = "cmd.exe"
			newBtn.Icon = "terminal"
		case "app_code":
			newBtn.Label = "VS Code"
			newBtn.Type = "command"
			newBtn.Value = "code"
			newBtn.Icon = "terminal"
		case "app_notepad":
			newBtn.Label = "Not Defteri"
			newBtn.Type = "command"
			newBtn.Value = "notepad.exe"
			newBtn.Icon = "copy"
		case "app_files", "app_explorer":
			newBtn.Label = "Dosya Gezgini"
			newBtn.Type = "command"
			newBtn.Value = "explorer.exe"
			newBtn.Icon = "folder"
		case "app_desktop":
			newBtn.Label = "Masaüstü"
			newBtn.Type = "hotkey"
			newBtn.Value = "meta+d"
			newBtn.Icon = "desktop"
		case "app_settings":
			newBtn.Label = "Windows Ayarlar"
			newBtn.Type = "hotkey"
			newBtn.Value = "meta+i"
			newBtn.Icon = "custom"
		case "app_snippet_tool":
			newBtn.Label = "Ekran Kırp"
			newBtn.Type = "hotkey"
			newBtn.Value = "meta+shift+s"
			newBtn.Icon = "custom"
		case "system_refresh":
			newBtn.Label = "Yenile"
			newBtn.Type = "hotkey"
			newBtn.Value = "f5"
			newBtn.Icon = "refresh"
		case "app_steam":
			newBtn.Label = "Steam"
			newBtn.Type = "command"
			newBtn.Value = "steam.exe"
			newBtn.Icon = "custom"
		case "app_browser":
			newBtn.Label = "Web Tarayıcı"
			newBtn.Type = "command"
			newBtn.Value = "explorer.exe https://google.com"
			newBtn.Icon = "web"
		case "app_url":
			newBtn.Label = "Web Sitesi"
			newBtn.Type = "url"
			newBtn.Value = "https://google.com"
			newBtn.Icon = "link"
		case "app_youtube":
			newBtn.Label = "YouTube"
			newBtn.Type = "url"
			newBtn.Value = "https://youtube.com"
			newBtn.Icon = "play"
		case "app_chatgpt":
			newBtn.Label = "ChatGPT"
			newBtn.Type = "url"
			newBtn.Value = "https://chatgpt.com"
			newBtn.Icon = "web"
		case "app_discord":
			newBtn.Label = "Discord"
			newBtn.Type = "hotkey"
			newBtn.Value = "ctrl+shift+m"
			newBtn.Icon = "mic"
		case "app_spotify":
			newBtn.Label = "Spotify"
			newBtn.Type = "url"
			newBtn.Value = "https://open.spotify.com"
			newBtn.Icon = "music"
		}
	}

	if label, ok := args["label"].(string); ok && label != "" {
		newBtn.Label = label
	}
	if bType, ok := args["type"].(string); ok && bType != "" {
		newBtn.Type = bType
	}
	if val, ok := args["value"].(string); ok && val != "" {
		newBtn.Value = val
	}
	if icon, ok := args["icon"].(string); ok && icon != "" {
		newBtn.Icon = icon
	}
	if colSpanFloat, ok := args["col_span"].(float64); ok && colSpanFloat >= 1 {
		newBtn.ColSpan = int(colSpanFloat)
	}
	if rowSpanFloat, ok := args["row_span"].(float64); ok && rowSpanFloat >= 1 {
		newBtn.RowSpan = int(rowSpanFloat)
	}
	if color, ok := args["color"].(string); ok && color != "" {
		newBtn.Color = color
	}
	if bgStyle, ok := args["bg_style"].(string); ok && bgStyle != "" {
		newBtn.BgStyle = bgStyle
	}
	if clockStyle, ok := args["clock_style"].(string); ok && clockStyle != "" {
		newBtn.ClockStyle = clockStyle
	}
	if fontFamily, ok := args["font_family"].(string); ok && fontFamily != "" {
		newBtn.FontFamily = fontFamily
	}
	if clockFontSize, ok := args["clock_font_size"].(string); ok && clockFontSize != "" {
		newBtn.ClockFontSize = clockFontSize
	}
	// Explicit bento cell, if the caller pinned one; -1 (or omitted) stays automatic.
	if gridRowFloat, ok := args["grid_row"].(float64); ok {
		newBtn.GridRow = int(gridRowFloat)
	}
	if gridColFloat, ok := args["grid_col"].(float64); ok {
		newBtn.GridCol = int(gridColFloat)
	}

	return newBtn
}

func executeInternalMCPTool(name string, args map[string]interface{}) (string, bool) {
	switch name {
	case "get_system_status":
		cfg := loadConfig()
		ips := []string{}
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
					ips = append(ips, ipnet.IP.String())
				}
			}
		}
		res := map[string]interface{}{
			"version":            AppVersion,
			"http_port":          8085,
			"screen_stream_port": 8086,
			"audio_stream_port":  8084,
			"ip_addresses":       ips,
			"streamdeck_layout":  fmt.Sprintf("%dx%d", cfg.StreamDeckRows, cfg.StreamDeckCols),
			"total_buttons":      len(cfg.StreamDeckButtons),
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return string(b), false

	case "get_streamdeck_config":
		cfg := loadConfig()
		b, _ := json.MarshalIndent(cfg, "", "  ")
		return string(b), false

	case "get_streamdeck_presets":
		data := map[string]interface{}{
			"types":            []string{"hotkey", "command", "url", "text_snippet", "toggle", "volume_slider", "app_volume_slider", "brightness_slider", "live_info", "clock_widget", "media_widget"},
			"clock_styles":     []string{"apple_lockscreen", "apple_stacked", "analog", "digital_seconds", "digital_clean"},
			"clock_font_sizes": []string{"auto", "compact", "regular", "large", "huge"},
			"font_families":    []string{"mono", "sans", "lcd", "serif", "tech"},
			"bg_styles":        []string{"default", "transparent", "glass", "outline"},
			"templates":        []string{"pro_studio", "media_lounge", "gaming_obs", "developer", "system_control"},
			"presets": []string{
				"clock", "clock_analog", "clock_seconds", "clock_digital_clean", "mini_clock",
				"telemetry_combo", "cpu", "ram", "volume", "volume_up", "volume_down", "brightness",
				"media", "media_next", "media_prev",
				"toggle_mic", "toggle_mute", "toggle_monitor", "app_taskmgr", "app_calc",
				"app_lock", "app_terminal", "app_code", "app_notepad", "app_files", "app_desktop",
				"app_settings", "app_snippet_tool", "system_refresh", "app_steam", "app_browser",
				"app_url", "app_youtube", "app_chatgpt", "app_discord", "app_spotify",
			},
		}
		b, _ := json.MarshalIndent(data, "", "  ")
		return string(b), false

	case "set_streamdeck_button":
		btnIDFloat, ok := args["button_id"].(float64)
		if !ok {
			return "Error: button_id must be an integer", true
		}
		btnID := int(btnIDFloat)
		cfg := loadConfig()
		found := false
		for i := range cfg.StreamDeckButtons {
			if cfg.StreamDeckButtons[i].ID == btnID {
				b := &cfg.StreamDeckButtons[i]
				if label, ok := args["label"].(string); ok && label != "" {
					b.Label = label
				}
				if bType, ok := args["type"].(string); ok && bType != "" {
					b.Type = bType
				}
				if val, ok := args["value"].(string); ok {
					b.Value = val
				}
				if icon, ok := args["icon"].(string); ok {
					b.Icon = icon
				}
				if pageFloat, ok := args["page"].(float64); ok {
					b.Page = int(pageFloat)
				}
				if colSpanFloat, ok := args["col_span"].(float64); ok && colSpanFloat >= 1 {
					b.ColSpan = int(colSpanFloat)
				}
				if rowSpanFloat, ok := args["row_span"].(float64); ok && rowSpanFloat >= 1 {
					b.RowSpan = int(rowSpanFloat)
				}
				if color, ok := args["color"].(string); ok && color != "" {
					b.Color = color
				}
				if bgStyle, ok := args["bg_style"].(string); ok && bgStyle != "" {
					b.BgStyle = bgStyle
				}
				if clockStyle, ok := args["clock_style"].(string); ok && clockStyle != "" {
					b.ClockStyle = clockStyle
				}
				if fontFamily, ok := args["font_family"].(string); ok && fontFamily != "" {
					b.FontFamily = fontFamily
				}
				if clockFontSize, ok := args["clock_font_size"].(string); ok && clockFontSize != "" {
					b.ClockFontSize = clockFontSize
				}
				if gridRowFloat, ok := args["grid_row"].(float64); ok {
					b.GridRow = int(gridRowFloat)
				}
				if gridColFloat, ok := args["grid_col"].(float64); ok {
					b.GridCol = int(gridColFloat)
				}
				found = true
				break
			}
		}
		if !found {
			newBtn := StreamDeckButton{
				ID:         btnID,
				Label:      "Yeni Buton",
				Type:       "hotkey",
				Value:      "ctrl+c",
				Icon:       "custom",
				Page:       cfg.ActivePage,
				ColSpan:    1,
				RowSpan:    1,
				GridRow:    gridAutoPlacement,
				GridCol:    gridAutoPlacement,
				Color:      "",
				BgStyle:    "default",
				FontFamily: "mono",
			}
			if label, ok := args["label"].(string); ok {
				newBtn.Label = label
			}
			if bType, ok := args["type"].(string); ok {
				newBtn.Type = bType
			}
			if val, ok := args["value"].(string); ok {
				newBtn.Value = val
			}
			if icon, ok := args["icon"].(string); ok {
				newBtn.Icon = icon
			}
			if pageFloat, ok := args["page"].(float64); ok {
				newBtn.Page = int(pageFloat)
			}
			if colSpanFloat, ok := args["col_span"].(float64); ok {
				newBtn.ColSpan = int(colSpanFloat)
			}
			if rowSpanFloat, ok := args["row_span"].(float64); ok {
				newBtn.RowSpan = int(rowSpanFloat)
			}
			if color, ok := args["color"].(string); ok {
				newBtn.Color = color
			}
			if bgStyle, ok := args["bg_style"].(string); ok {
				newBtn.BgStyle = bgStyle
			}
			if clockStyle, ok := args["clock_style"].(string); ok {
				newBtn.ClockStyle = clockStyle
			}
			if fontFamily, ok := args["font_family"].(string); ok {
				newBtn.FontFamily = fontFamily
			}
			if clockFontSize, ok := args["clock_font_size"].(string); ok {
				newBtn.ClockFontSize = clockFontSize
			}
			if gridRowFloat, ok := args["grid_row"].(float64); ok {
				newBtn.GridRow = int(gridRowFloat)
			}
			if gridColFloat, ok := args["grid_col"].(float64); ok {
				newBtn.GridCol = int(gridColFloat)
			}
			cfg.StreamDeckButtons = append(cfg.StreamDeckButtons, newBtn)
		}
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Successfully configured button #%d", btnID), false

	case "add_streamdeck_button":
		cfg := loadConfig()
		nextID := 0
		for _, b := range cfg.StreamDeckButtons {
			if b.ID >= nextID {
				nextID = b.ID + 1
			}
		}
		newBtn := buildStreamDeckButton(nextID, cfg.ActivePage, args)
		cfg.StreamDeckButtons = append(cfg.StreamDeckButtons, newBtn)
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Successfully added button #%d: [%s] (%s: %s, size: %dx%d, page: %d)", newBtn.ID, newBtn.Label, newBtn.Type, newBtn.Value, newBtn.ColSpan, newBtn.RowSpan, newBtn.Page), false

	case "create_custom_widget":
		return executeInternalMCPTool("add_streamdeck_button", args)

	case "batch_create_streamdeck_buttons":
		buttonsRaw, ok := args["buttons"].([]interface{})
		if !ok || len(buttonsRaw) == 0 {
			return "Error: buttons array is required and cannot be empty", true
		}
		cfg := loadConfig()
		targetPage := cfg.ActivePage
		if pFloat, ok := args["page"].(float64); ok {
			targetPage = int(pFloat)
		}
		if clearFirst, ok := args["clear_page_first"].(bool); ok && clearFirst {
			filtered := make([]StreamDeckButton, 0)
			for _, b := range cfg.StreamDeckButtons {
				if b.Page != targetPage {
					filtered = append(filtered, b)
				}
			}
			cfg.StreamDeckButtons = filtered
		}
		nextID := 0
		for _, b := range cfg.StreamDeckButtons {
			if b.ID >= nextID {
				nextID = b.ID + 1
			}
		}

		createdCount := 0
		for _, raw := range buttonsRaw {
			btnMap, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			newBtn := buildStreamDeckButton(nextID, targetPage, btnMap)
			cfg.StreamDeckButtons = append(cfg.StreamDeckButtons, newBtn)
			nextID++
			createdCount++
		}
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Successfully batch created %d button(s) on page %d", createdCount, targetPage), false

	case "duplicate_streamdeck_button":
		btnIDFloat, ok := args["button_id"].(float64)
		if !ok {
			return "Error: button_id must be an integer", true
		}
		btnID := int(btnIDFloat)
		cfg := loadConfig()
		var srcBtn *StreamDeckButton
		for i := range cfg.StreamDeckButtons {
			if cfg.StreamDeckButtons[i].ID == btnID {
				srcBtn = &cfg.StreamDeckButtons[i]
				break
			}
		}
		if srcBtn == nil {
			return fmt.Sprintf("Error: button ID %d not found", btnID), true
		}
		nextID := 0
		for _, b := range cfg.StreamDeckButtons {
			if b.ID >= nextID {
				nextID = b.ID + 1
			}
		}
		cloned := *srcBtn
		cloned.ID = nextID
		// The copy must not land on the source's cell, so it goes back to auto
		// placement and the bento packer finds it the first free slot.
		cloned.GridRow = gridAutoPlacement
		cloned.GridCol = gridAutoPlacement
		cloned.gridRowSet = false
		cloned.gridColSet = false
		if pFloat, ok := args["target_page"].(float64); ok {
			cloned.Page = int(pFloat)
		}
		cfg.StreamDeckButtons = append(cfg.StreamDeckButtons, cloned)
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Successfully duplicated button #%d to new button #%d (page %d)", btnID, cloned.ID, cloned.Page), false

	case "delete_streamdeck_button":
		btnIDFloat, ok := args["button_id"].(float64)
		if !ok {
			return "Error: button_id must be an integer", true
		}
		btnID := int(btnIDFloat)
		cfg := loadConfig()
		filtered := make([]StreamDeckButton, 0, len(cfg.StreamDeckButtons))
		found := false
		for _, b := range cfg.StreamDeckButtons {
			if b.ID == btnID {
				found = true
				continue
			}
			filtered = append(filtered, b)
		}
		if !found {
			return fmt.Sprintf("Error: button ID %d not found", btnID), true
		}
		cfg.StreamDeckButtons = filtered
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Successfully deleted button #%d", btnID), false

	case "swap_streamdeck_buttons":
		id1Float, ok1 := args["button_id_1"].(float64)
		id2Float, ok2 := args["button_id_2"].(float64)
		if !ok1 || !ok2 {
			return "Error: button_id_1 and button_id_2 must be integers", true
		}
		id1 := int(id1Float)
		id2 := int(id2Float)
		cfg := loadConfig()
		idx1, idx2 := -1, -1
		for i := range cfg.StreamDeckButtons {
			if cfg.StreamDeckButtons[i].ID == id1 {
				idx1 = i
			}
			if cfg.StreamDeckButtons[i].ID == id2 {
				idx2 = i
			}
		}
		if idx1 == -1 || idx2 == -1 {
			return "Error: one or both buttons not found", true
		}
		first, second := cfg.StreamDeckButtons[idx1], cfg.StreamDeckButtons[idx2]
		// The bento packer positions a card by its cell, so swapping the slice order
		// alone would no longer move a pinned card, while swapping coordinates alone
		// could not move two auto-placed cards. Do both so the two widgets really
		// trade slots — identity and cell — in every combination.
		first.GridRow, second.GridRow = second.GridRow, first.GridRow
		first.GridCol, second.GridCol = second.GridCol, first.GridCol
		cfg.StreamDeckButtons[idx1], cfg.StreamDeckButtons[idx2] = second, first
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Swapped buttons #%d and #%d", id1, id2), false

	case "create_streamdeck_page":
		title, _ := args["title"].(string)
		if title == "" {
			title = "Yeni Sayfa"
		}
		cfg := loadConfig()
		newPageID := len(cfg.StreamDeckPages)
		for _, p := range cfg.StreamDeckPages {
			if p.ID >= newPageID {
				newPageID = p.ID + 1
			}
		}
		cfg.StreamDeckPages = append(cfg.StreamDeckPages, StreamDeckPage{
			ID:    newPageID,
			Title: title,
		})
		cfg.ActivePage = newPageID
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Successfully created page #%d: '%s'", newPageID, title), false

	case "rename_streamdeck_page":
		pageIDFloat, ok := args["page_id"].(float64)
		if !ok {
			return "Error: page_id must be an integer", true
		}
		title, _ := args["title"].(string)
		if title == "" {
			return "Error: title cannot be empty", true
		}
		cfg := loadConfig()
		found := false
		for i := range cfg.StreamDeckPages {
			if cfg.StreamDeckPages[i].ID == int(pageIDFloat) {
				cfg.StreamDeckPages[i].Title = title
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("Error: page ID %d not found", int(pageIDFloat)), true
		}
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Renamed page #%d to '%s'", int(pageIDFloat), title), false

	case "delete_streamdeck_page":
		pageIDFloat, ok := args["page_id"].(float64)
		if !ok {
			return "Error: page_id must be an integer", true
		}
		pageID := int(pageIDFloat)
		cfg := loadConfig()
		if len(cfg.StreamDeckPages) <= 1 {
			return "Error: cannot delete the only remaining page", true
		}
		newPages := make([]StreamDeckPage, 0)
		for _, p := range cfg.StreamDeckPages {
			if p.ID != pageID {
				newPages = append(newPages, p)
			}
		}
		cfg.StreamDeckPages = newPages
		newBtns := make([]StreamDeckButton, 0)
		for _, b := range cfg.StreamDeckButtons {
			if b.Page != pageID {
				newBtns = append(newBtns, b)
			}
		}
		cfg.StreamDeckButtons = newBtns
		if cfg.ActivePage == pageID {
			cfg.ActivePage = cfg.StreamDeckPages[0].ID
		}
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Deleted page #%d and its buttons", pageID), false

	case "clear_streamdeck_page":
		pageID := 0
		if pFloat, ok := args["page_id"].(float64); ok {
			pageID = int(pFloat)
		} else {
			cfg := loadConfig()
			pageID = cfg.ActivePage
		}
		cfg := loadConfig()
		newBtns := make([]StreamDeckButton, 0)
		for _, b := range cfg.StreamDeckButtons {
			if b.Page != pageID {
				newBtns = append(newBtns, b)
			}
		}
		cfg.StreamDeckButtons = newBtns
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Cleared all buttons on page #%d", pageID), false

	case "apply_streamdeck_template":
		tplKey, _ := args["template"].(string)
		if tplKey == "" {
			return "Error: template is required ('pro_studio', 'media_lounge', 'gaming_obs', 'developer', 'system_control')", true
		}
		cfg := loadConfig()
		targetPage := cfg.ActivePage
		if pFloat, ok := args["page"].(float64); ok {
			targetPage = int(pFloat)
		}
		filtered := make([]StreamDeckButton, 0)
		for _, b := range cfg.StreamDeckButtons {
			if b.Page != targetPage {
				filtered = append(filtered, b)
			}
		}
		nextID := 0
		for _, b := range filtered {
			if b.ID >= nextID {
				nextID = b.ID + 1
			}
		}
		var tplButtons []StreamDeckButton
		switch tplKey {
		case "pro_studio":
			tplButtons = []StreamDeckButton{
				{ID: nextID, Page: targetPage, Label: "Saat & Tarih", Type: "clock_widget", Icon: "clock", ColSpan: 2, RowSpan: 1, Color: ""},
				{ID: nextID + 1, Page: targetPage, Label: "Ana Ses", Type: "volume_slider", Value: "master_volume", Icon: "volume", ColSpan: 2, RowSpan: 1, Color: ""},
				{ID: nextID + 2, Page: targetPage, Label: "CPU Yükü", Type: "live_info", Value: "cpu", Icon: "cpu", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 3, Page: targetPage, Label: "RAM Bellek", Type: "live_info", Value: "ram", Icon: "ram", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 4, Page: targetPage, Label: "Mikrofon", Type: "toggle", Value: "mic_mute", Icon: "mic", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 5, Page: targetPage, Label: "PC Kilitle", Type: "command", Value: "rundll32.exe user32.dll,LockWorkStation", Icon: "lock", ColSpan: 1, RowSpan: 1, Color: ""},
			}
		case "media_lounge":
			tplButtons = []StreamDeckButton{
				{ID: nextID, Page: targetPage, Label: "Ana Ses", Type: "volume_slider", Value: "master_volume", Icon: "volume", ColSpan: 2, RowSpan: 1, Color: ""},
				{ID: nextID + 1, Page: targetPage, Label: "Medya Masası", Type: "media_widget", Value: "playpause", Icon: "play", ColSpan: 2, RowSpan: 1, Color: ""},
				{ID: nextID + 2, Page: targetPage, Label: "Sesi Kapat", Type: "toggle", Value: "sound_mute", Icon: "voldown", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 3, Page: targetPage, Label: "Spotify", Type: "url", Value: "https://open.spotify.com", Icon: "music", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 4, Page: targetPage, Label: "YouTube", Type: "url", Value: "https://youtube.com", Icon: "play", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 5, Page: targetPage, Label: "Parlaklık", Type: "brightness_slider", Value: "display_brightness", Icon: "bright", ColSpan: 1, RowSpan: 1, Color: ""},
			}
		case "gaming_obs":
			tplButtons = []StreamDeckButton{
				{ID: nextID, Page: targetPage, Label: "Discord Mute", Type: "hotkey", Value: "ctrl+shift+m", Icon: "mic", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 1, Page: targetPage, Label: "Discord Sağır", Type: "hotkey", Value: "ctrl+shift+d", Icon: "voldown", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 2, Page: targetPage, Label: "Ekran Alıntısı", Type: "hotkey", Value: "meta+shift+s", Icon: "task", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 3, Page: targetPage, Label: "Game Bar", Type: "hotkey", Value: "meta+g", Icon: "custom", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 4, Page: targetPage, Label: "Ana Ses", Type: "volume_slider", Value: "master_volume", Icon: "volume", ColSpan: 2, RowSpan: 1, Color: ""},
				{ID: nextID + 5, Page: targetPage, Label: "Donanım Telemetri", Type: "live_info", Value: "combo", Icon: "cpu", ColSpan: 2, RowSpan: 1, Color: ""},
			}
		case "developer":
			tplButtons = []StreamDeckButton{
				{ID: nextID, Page: targetPage, Label: "Terminal", Type: "command", Value: "cmd.exe", Icon: "terminal", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 1, Page: targetPage, Label: "Görev Yöneticisi", Type: "command", Value: "taskmgr.exe", Icon: "task", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 2, Page: targetPage, Label: "Hesap Makinesi", Type: "command", Value: "calc.exe", Icon: "calc", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 3, Page: targetPage, Label: "PC Kilitle", Type: "command", Value: "rundll32.exe user32.dll,LockWorkStation", Icon: "lock", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 4, Page: targetPage, Label: "Saat & Tarih", Type: "clock_widget", Icon: "clock", ColSpan: 2, RowSpan: 1, Color: ""},
				{ID: nextID + 5, Page: targetPage, Label: "GitHub", Type: "url", Value: "https://github.com", Icon: "link", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 6, Page: targetPage, Label: "Masaüstü", Type: "hotkey", Value: "meta+d", Icon: "desktop", ColSpan: 1, RowSpan: 1, Color: ""},
			}
		default:
			tplButtons = []StreamDeckButton{
				{ID: nextID, Page: targetPage, Label: "Donanım Telemetri", Type: "live_info", Value: "combo", Icon: "cpu", ColSpan: 2, RowSpan: 1, Color: ""},
				{ID: nextID + 1, Page: targetPage, Label: "Sanal Ekran", Type: "toggle", Value: "virtual_monitor", Icon: "custom", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 2, Page: targetPage, Label: "Mikrofon", Type: "toggle", Value: "mic_mute", Icon: "mic", ColSpan: 1, RowSpan: 1, Color: ""},
				{ID: nextID + 3, Page: targetPage, Label: "Parlaklık", Type: "brightness_slider", Value: "display_brightness", Icon: "bright", ColSpan: 2, RowSpan: 1, Color: ""},
				{ID: nextID + 4, Page: targetPage, Label: "Ana Ses", Type: "volume_slider", Value: "master_volume", Icon: "volume", ColSpan: 2, RowSpan: 1, Color: ""},
			}
		}
		// Template widgets are laid out by the bento packer, not pinned to a cell:
		// a zero-value coordinate would read as an explicit (0, 0) and pile every
		// template card onto the top-left slot.
		for i := range tplButtons {
			tplButtons[i].GridRow = gridAutoPlacement
			tplButtons[i].GridCol = gridAutoPlacement
		}
		cfg.StreamDeckButtons = append(filtered, tplButtons...)
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Applied template '%s' to page #%d (%d widgets created)", tplKey, targetPage, len(tplButtons)), false

	case "streamdeck_set_brightness":
		levelFloat, ok := args["level"].(float64)
		if !ok {
			return "Error: level must be a number (0-100)", true
		}
		lvl := int(levelFloat)
		if lvl < 0 {
			lvl = 0
		}
		if lvl > 100 {
			lvl = 100
		}
		adjustDisplayBrightness(lvl)
		broadcast(map[string]interface{}{
			"type":  "brightness_changed",
			"level": lvl,
		})
		return fmt.Sprintf("Display brightness set to %d%%", lvl), false

	case "set_grid_layout":
		rowsFloat, ok1 := args["rows"].(float64)
		colsFloat, ok2 := args["cols"].(float64)
		if !ok1 || !ok2 {
			return "Error: rows and cols must be numbers", true
		}
		rows := int(rowsFloat)
		cols := int(colsFloat)
		if rows < 1 || rows > 6 || cols < 1 || cols > 8 {
			return "Error: rows must be 1-6, cols must be 1-8", true
		}
		cfg := loadConfig()
		cfg.StreamDeckRows = rows
		cfg.StreamDeckCols = cols
		saveConfig(cfg)
		broadcast(map[string]interface{}{
			"type": "config_update",
			"rows": rows,
			"cols": cols,
		})
		return fmt.Sprintf("Successfully updated grid layout to %dx%d (%d slots)", rows, cols, rows*cols), false

	case "trigger_button":
		btnIDFloat, ok := args["button_id"].(float64)
		if !ok {
			return "Error: button_id must be an integer", true
		}
		btnID := int(btnIDFloat)
		cfg := loadConfig()
		var targetBtn *StreamDeckButton
		for i := range cfg.StreamDeckButtons {
			if cfg.StreamDeckButtons[i].ID == btnID {
				targetBtn = &cfg.StreamDeckButtons[i]
				break
			}
		}
		if targetBtn == nil {
			return fmt.Sprintf("Error: button ID %d not found", btnID), true
		}
		if targetBtn.Type == "hotkey" {
			if !runSpecialStreamDeckAction(targetBtn.Value) {
				simulateHotkey(targetBtn.Value)
			}
		} else if targetBtn.Type == "command" {
			go func(cmd string) { _ = launchCommandNative(cmd) }(targetBtn.Value)
		}
		return fmt.Sprintf("Triggered button #%d (%s: %s)", btnID, targetBtn.Type, targetBtn.Value), false

	case "list_installed_apps":
		apps := getInstalledApps()
		b, _ := json.MarshalIndent(apps, "", "  ")
		return string(b), false

	case "send_pc_notification":
		title, _ := args["title"].(string)
		msg, _ := args["message"].(string)
		showNotification(title, msg)
		return "Notification dispatched successfully", false

	case "sync_clipboard":
		text, _ := args["text"].(string)
		lastClipboardText = text
		clipboard.WriteAll(text)
		broadcast(map[string]interface{}{
			"type": "clipboard_sync",
			"text": text,
		})
		return fmt.Sprintf("Clipboard synced: %d characters", len(text)), false

	case "streamdeck_set_volume":
		action, _ := args["action"].(string)
		if action == "" {
			action = "set"
		}
		level := float64(50)
		if lvl, ok := args["level"].(float64); ok {
			level = lvl
		}
		adjustMasterVolume(action, level)
		broadcast(map[string]interface{}{
			"type":   "volume_changed",
			"action": action,
			"level":  level,
		})
		return fmt.Sprintf("Master volume action executed: %s (level: %.0f%%)", action, level), false

	case "streamdeck_toggle_button":
		btnIDFloat, ok := args["button_id"].(float64)
		if !ok {
			return "Error: button_id must be an integer", true
		}
		btnID := int(btnIDFloat)
		cfg := loadConfig()
		var targetBtnIndex = -1
		for i, b := range cfg.StreamDeckButtons {
			if b.ID == btnID {
				targetBtnIndex = i
				break
			}
		}
		if targetBtnIndex == -1 {
			return fmt.Sprintf("Error: button ID %d not found", btnID), true
		}
		btn := &cfg.StreamDeckButtons[targetBtnIndex]
		btn.State = !btn.State
		saveConfig(cfg)
		broadcast(map[string]interface{}{
			"type":      "button_state_changed",
			"button_id": btn.ID,
			"state":     btn.State,
			"page":      btn.Page,
		})
		switch btn.Value {
		case "mic_mute":
			simulateHotkey("ctrl+shift+m")
		case "speaker_mute", "sound_mute":
			adjustMasterVolume("mute", 0)
		case "screen_toggle", "virtual_monitor":
			setVirtualMonitor(btn.State)
		default:
			if strings.Contains(btn.Value, "+") {
				simulateHotkey(btn.Value)
			} else if btn.Value != "" && !runSpecialStreamDeckAction(btn.Value) {
				go func(cmd string) { _ = launchCommandNative(cmd) }(btn.Value)
			}
		}
		return fmt.Sprintf("Toggled button #%d [%s] to state: %v", btn.ID, btn.Label, btn.State), false

	case "streamdeck_switch_page":
		var targetPage int
		if pNum, ok := args["page"].(float64); ok {
			targetPage = int(pNum)
		} else if pStr, ok := args["page"].(string); ok {
			fmt.Sscanf(pStr, "%d", &targetPage)
		}
		cfg := loadConfig()
		cfg.ActivePage = targetPage
		saveConfig(cfg)
		broadcast(map[string]interface{}{
			"type":        "page_switched",
			"active_page": targetPage,
			"buttons":     cfg.StreamDeckButtons,
		})
		return fmt.Sprintf("Switched active page to: %d", targetPage), false

	case "get_system_stats":
		stats := buildSystemStatsPayload()
		delete(stats, "type")
		b, _ := json.MarshalIndent(stats, "", "  ")
		return string(b), false

	case "setup_usb_reverse":
		ok, text := setupAdbReverse()
		broadcast(map[string]interface{}{
			"type":    "usb_status",
			"success": ok,
			"message": text,
		})
		return text, !ok

	case "capture_screen":
		path, err := captureScreenToPNG()
		if err != nil {
			return "Error: " + err.Error(), true
		}
		b, _ := json.MarshalIndent(map[string]interface{}{"success": true, "path": path}, "", "  ")
		return string(b), false

	case "get_active_window":
		title, process := getActiveWindowProcessName()
		b, _ := json.MarshalIndent(map[string]interface{}{
			"title":   title,
			"process": process,
		}, "", "  ")
		return string(b), false

	case "get_network_stats":
		down, up := getNetworkSpeedKbps()
		res := map[string]interface{}{
			"down_kbps":  down,
			"up_kbps":    up,
			"down_mbps":  float64(down) / 1000.0,
			"up_mbps":    float64(up) / 1000.0,
			"interfaces": networkInterfaceSummary(),
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return string(b), false

	case "net_speed":
		down, up := getNetworkSpeedKbps()
		res := map[string]interface{}{
			"down_kbps": down,
			"up_kbps":   up,
			"down_mbps": float64(down) / 1000.0,
			"up_mbps":   float64(up) / 1000.0,
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return string(b), false

	case "pomodoro_toggle":
		state := togglePomodoro()
		delete(state, "type")
		b, _ := json.MarshalIndent(state, "", "  ")
		return string(b), false

	case "set_autostart":
		enable, _ := args["enable"].(bool)
		if err := setAutoStart(enable); err != nil {
			return "Error: " + err.Error(), true
		}
		updateTrayTooltip(trayTooltipText())
		state := "disabled"
		if enable {
			state = "enabled"
		}
		return "Windows autostart " + state + " successfully", false

	default:
		return "Unknown tool: " + name, true
	}
}

// ----------------- REST API Handlers -----------------
func handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Content-Type", "application/json")
	if !isAuthorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	cfg := loadConfig()
	ips := []string{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"version":            AppVersion,
		"status":             "running",
		"http_port":          8085,
		"screen_stream_port": 8086,
		"audio_stream_port":  8084,
		"ips":                ips,
		"grid":               fmt.Sprintf("%dx%d", cfg.StreamDeckRows, cfg.StreamDeckCols),
		"buttons_count":      len(cfg.StreamDeckButtons),
	})
}

func handleAPIStreamDeck(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Content-Type", "application/json")
	if !isAuthorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	cfg := loadConfig()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"rows":    cfg.StreamDeckRows,
		"cols":    cfg.StreamDeckCols,
		"buttons": cfg.StreamDeckButtons,
	})
}

func handleAPIStreamDeckPages(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	cfg := loadConfig()
	if r.Method == http.MethodPost {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			var targetPage int
			if pNum, ok := payload["page"].(float64); ok {
				targetPage = int(pNum)
			} else if pStr, ok := payload["page"].(string); ok {
				fmt.Sscanf(pStr, "%d", &targetPage)
			}
			cfg.ActivePage = targetPage
			saveConfig(cfg)
			broadcast(map[string]interface{}{
				"type":        "page_switched",
				"active_page": targetPage,
				"buttons":     cfg.StreamDeckButtons,
			})
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"active_page": cfg.ActivePage,
		"pages":       cfg.StreamDeckPages,
	})
}

func handleAPIStreamDeckVolume(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resStr, isErr := executeInternalMCPTool("streamdeck_set_volume", payload)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": !isErr,
		"message": resStr,
	})
}

func handleAPISystemStats(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	stats := buildSystemStatsPayload()
	stats["success"] = true
	stats["cpu_percent"] = stats["cpu"]
	stats["ram_percent"] = stats["ram"]
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func handleAPIUsbForward(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	ok, text := setupAdbReverse()
	broadcast(map[string]interface{}{
		"type":    "usb_status",
		"success": ok,
		"message": text,
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": ok,
		"message": text,
	})
}

func handleAPIStreamDeckTrigger(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	btnIDFloat, ok := payload["button_id"].(float64)
	if !ok {
		http.Error(w, "Missing button_id", http.StatusBadRequest)
		return
	}
	resStr, isErr := executeInternalMCPTool("trigger_button", map[string]interface{}{
		"button_id": btnIDFloat,
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": !isErr,
		"message": resStr,
	})
}

func handleAPIStreamDeckButton(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		cfg := loadConfig()
		q := r.URL.Query()
		if idStr := q.Get("id"); idStr != "" {
			var id int
			fmt.Sscanf(idStr, "%d", &id)
			for _, b := range cfg.StreamDeckButtons {
				if b.ID == id {
					json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "button": b})
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Button not found"})
			return
		}

		if pageStr := q.Get("page"); pageStr != "" {
			var page int
			fmt.Sscanf(pageStr, "%d", &page)
			pageButtons := make([]StreamDeckButton, 0)
			for _, b := range cfg.StreamDeckButtons {
				if b.Page == page {
					pageButtons = append(pageButtons, b)
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "buttons": pageButtons, "page": page})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "buttons": cfg.StreamDeckButtons, "active_page": cfg.ActivePage})
		return
	}

	if r.Method == http.MethodDelete {
		var id int
		if idStr := r.URL.Query().Get("id"); idStr != "" {
			fmt.Sscanf(idStr, "%d", &id)
		} else {
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
				if f, ok := payload["button_id"].(float64); ok {
					id = int(f)
				}
			}
		}
		resStr, isErr := executeInternalMCPTool("delete_streamdeck_button", map[string]interface{}{"button_id": float64(id)})
		if isErr {
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": !isErr, "message": resStr})
		return
	}

	if r.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Check if payload is a JSON array (batch add)
		var arrPayload []interface{}
		if err := json.Unmarshal(bodyBytes, &arrPayload); err == nil && len(arrPayload) > 0 {
			resStr, isErr := executeInternalMCPTool("batch_create_streamdeck_buttons", map[string]interface{}{
				"buttons": arrPayload,
			})
			if isErr {
				w.WriteHeader(http.StatusBadRequest)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"success": !isErr, "message": resStr})
			return
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		action, _ := payload["action"].(string)
		var resStr string
		var isErr bool

		switch action {
		case "add", "create":
			resStr, isErr = executeInternalMCPTool("add_streamdeck_button", payload)
		case "batch":
			resStr, isErr = executeInternalMCPTool("batch_create_streamdeck_buttons", payload)
		case "duplicate":
			resStr, isErr = executeInternalMCPTool("duplicate_streamdeck_button", payload)
		case "delete":
			resStr, isErr = executeInternalMCPTool("delete_streamdeck_button", payload)
		default:
			// If button_id is provided, set/update button; otherwise add button
			if _, hasID := payload["button_id"]; hasID {
				resStr, isErr = executeInternalMCPTool("set_streamdeck_button", payload)
			} else {
				resStr, isErr = executeInternalMCPTool("add_streamdeck_button", payload)
			}
		}

		if isErr {
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": !isErr,
			"message": resStr,
		})
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func handleAPIStreamDeckPresets(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	resStr, _ := executeInternalMCPTool("get_streamdeck_presets", nil)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(resStr))
}

func handleAPIStreamDeckLayout(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resStr, isErr := executeInternalMCPTool("set_grid_layout", payload)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": !isErr,
		"message": resStr,
	})
}

func handleAPIApps(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Content-Type", "application/json")
	if !isAuthorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	apps := getInstalledApps()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"apps":    apps,
	})
}

func handleAPIClipboard(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		text, _ := clipboard.ReadAll()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"text":    text,
		})
		return
	}
	if r.Method == http.MethodPost {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		text, _ := payload["text"].(string)
		executeInternalMCPTool("sync_clipboard", map[string]interface{}{"text": text})
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Clipboard updated",
		})
		return
	}
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func handleAPINotify(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title, _ := payload["title"].(string)
	msg, _ := payload["message"].(string)
	showNotification(title, msg)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Notification dispatched",
	})
}

// ----------------- Autostart, active window, network, screen capture, pomodoro -----------------

func handleAPIAutostart(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !checkAuth(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodPost {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		enable, _ := payload["enable"].(bool)
		if err := setAutoStart(enable); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		updateTrayTooltip(trayTooltipText())
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "enabled": enable})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "enabled": isAutoStartEnabled()})
}

// handleAPIWeather reports the live weather for ?city= (default: the configured city)
// through the keyless Open-Meteo geocoding + forecast APIs. A successful lookup also
// becomes the persisted default city.
func handleAPIWeather(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}

	city := resolveWeatherCity(r.URL.Query().Get("city"))
	snapshot := getWeather(city)
	if snapshot.OK {
		if cfg := loadConfig(); cfg.WeatherCity != city {
			cfg.WeatherCity = city
			saveConfig(cfg)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      snapshot.OK,
		"weather_city": snapshot.City,
		"weather_temp": snapshot.Temp,
		"weather_code": snapshot.Code,
		"weather_desc": snapshot.Desc,
		"weather_wind": snapshot.Wind,
	})
}

// handleAPINowPlaying reports the media title/artist grabbed from the open player
// windows (Spotify, VLC, Media Player, browsers) with native Win32 calls only.
func handleAPINowPlaying(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}

	title, artist, playing, app := getNowPlaying()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"media_title":   title,
		"media_artist":  artist,
		"media_playing": playing,
		"media_app":     app,
	})
}

func handleAPIActiveWindow(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Content-Type", "application/json")
	if !isAuthorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	title, process := getActiveWindowProcessName()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"title":   title,
		"process": process,
	})
}

func handleAPINetwork(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	down, up := getNetworkSpeedKbps()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"down_kbps":  down,
		"up_kbps":    up,
		"down_mbps":  float64(down) / 1000.0,
		"up_mbps":    float64(up) / 1000.0,
		"interfaces": networkInterfaceSummary(),
	})
}

func handleAPIScreenCapture(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	path, err := captureScreenToPNG()
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"path":    path,
		"file":    filepath.Base(path),
	})
}

func handleAPIPomodoro(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	w.Header().Set("Content-Type", "application/json")

	// GET /api/pomodoro reports the state, POST /api/pomodoro[/toggle] flips it.
	if r.Method == http.MethodPost || strings.HasSuffix(r.URL.Path, "/toggle") {
		state := togglePomodoro()
		state["success"] = true
		json.NewEncoder(w).Encode(state)
		return
	}
	state := pomodoroStatus()
	state["success"] = true
	json.NewEncoder(w).Encode(state)
}

// captureScreenToPNG grabs the primary display and writes it to the app data folder.
func captureScreenToPNG() (string, error) {
	if screenshot.NumActiveDisplays() == 0 {
		return "", fmt.Errorf("no active display found")
	}
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return "", err
	}
	targetDir := filepath.Join(appDataDir, "Screenshots")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", err
	}
	outPath := filepath.Join(targetDir, fmt.Sprintf("bigpocket_%s.png", time.Now().Format("20060102_150405")))
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return "", err
	}
	return outPath, nil
}

// Check admin helper
func checkIfAdmin() bool {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	isUserAnAdmin := shell32.NewProc("IsUserAnAdmin")
	ret, _, _ := isUserAnAdmin.Call()
	return ret != 0
}

// Virtual Monitor Driver installers
func installDriver() (bool, string) {
	if !checkIfAdmin() {
		return false, "Yönetici yetkisi gerekmektedir."
	}
	os.MkdirAll(driverDir, 0755)

	installer64 := filepath.Join(driverDir, "deviceinstaller64.exe")
	if _, err := os.Stat(installer64); os.IsNotExist(err) {
		// Download driver ZIP
		fmt.Println("Downloading usbmmidd driver...")
		resp, err := http.Get("https://www.amyuni.com/downloads/usbmmidd.zip")
		if err != nil {
			return false, "Sürücü indirilemedi: " + err.Error()
		}
		defer resp.Body.Close()

		zipData, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, "Dosya okuma hatası: " + err.Error()
		}

		// Unzip in driverDir
		r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
		if err != nil {
			return false, "Zip açılamadı: " + err.Error()
		}
		for _, f := range r.File {
			fpath, err := safeZipEntryPath(driverDir, f.Name)
			if err != nil {
				continue
			}
			if f.FileInfo().IsDir() {
				os.MkdirAll(fpath, os.ModePerm)
				continue
			}
			os.MkdirAll(filepath.Dir(fpath), os.ModePerm)
			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				return false, "Dosya çıkartılamadı: " + err.Error()
			}
			rc, err := f.Open()
			if err != nil {
				outFile.Close()
				return false, "Zip okuma hatası: " + err.Error()
			}
			io.Copy(outFile, rc)
			outFile.Close()
			rc.Close()
		}
	}

	// Install the driver using deviceinstaller64.exe
	cmd := execCommandHidden(installer64, "install", "usbmmidd.inf", "usbmmidd")
	cmd.Dir = driverDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, "Kurulum hatası: " + string(out)
	}
	return true, "Sürücü başarıyla yüklendi."
}

func setVirtualMonitor(enable bool) (bool, string) {
	if !checkIfAdmin() {
		return false, "Yönetici yetkisi gerekmektedir."
	}
	installer64 := filepath.Join(driverDir, "deviceinstaller64.exe")
	if _, err := os.Stat(installer64); os.IsNotExist(err) {
		success, msg := installDriver()
		if !success {
			return false, msg
		}
	}

	val := "0"
	if enable {
		val = "1"
	}

	cmd := execCommandHidden(installer64, "enableidd", val)
	cmd.Dir = driverDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, "Ekran sürücüsü kontrol hatası: " + string(out)
	}
	return true, "Monitör durumu güncellendi."
}

type InstalledApp struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func getInstalledApps() []InstalledApp {
	var apps []InstalledApp
	seen := make(map[string]bool)

	programData := os.Getenv("ProgramData")
	appData := os.Getenv("APPDATA")

	scanDirs := []string{}
	if programData != "" {
		scanDirs = append(scanDirs, filepath.Join(programData, "Microsoft", "Windows", "Start Menu", "Programs"))
	}
	if appData != "" {
		scanDirs = append(scanDirs, filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs"))
	}

	for _, dir := range scanDirs {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.ToLower(filepath.Ext(path)) == ".lnk" {
				name := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
				if seen[name] || name == "Uninstall" || strings.Contains(strings.ToLower(name), "uninstall") {
					return nil
				}
				seen[name] = true
				apps = append(apps, InstalledApp{
					Name: name,
					Path: path,
				})
			}
			return nil
		})
	}
	return apps
}

func checkMicDriverExists() bool {
	if _, err := os.Stat(`C:\Windows\System32\drivers\vbcable_x64.sys`); err == nil {
		return true
	}
	if _, err := os.Stat(`C:\Windows\System32\drivers\vbcable_xp64.sys`); err == nil {
		return true
	}
	if _, err := os.Stat(`C:\Program Files\VB\CABLE`); err == nil {
		return true
	}
	return false
}

func installMicDriver() (bool, string) {
	if !checkIfAdmin() {
		return false, "Yönetici yetkisi gerekmektedir."
	}
	micDriverDir := filepath.Join(appDataDir, "vbcable")
	os.MkdirAll(micDriverDir, 0755)

	setupExe := filepath.Join(micDriverDir, "VBCABLE_Setup_x64.exe")
	if _, err := os.Stat(setupExe); os.IsNotExist(err) {
		// First try local installation directory
		localVBCableDir := filepath.Join(exeDir, "VBCABLE")
		localSetupExe := filepath.Join(localVBCableDir, "VBCABLE_Setup_x64.exe")
		if _, err := os.Stat(localSetupExe); err == nil {
			fmt.Println("Installing VB-Cable from local directory...")
			err = copyDir(localVBCableDir, micDriverDir)
			if err != nil {
				return false, "Yerel sürücü dosyaları kopyalanamadı: " + err.Error()
			}
		} else {
			// Fallback to downloading
			fmt.Println("Downloading VB-Cable driver...")
			resp, err := http.Get("https://download.vb-audio.com/Download_CABLE/VBCABLE_Driver_Pack43.zip")
			if err != nil {
				return false, "Sürücü indirilemedi: " + err.Error()
			}
			defer resp.Body.Close()

			zipData, err := io.ReadAll(resp.Body)
			if err != nil {
				return false, "Dosya okuma hatası: " + err.Error()
			}

			r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
			if err != nil {
				return false, "Zip açılamadı: " + err.Error()
			}
			for _, f := range r.File {
				fpath, err := safeZipEntryPath(micDriverDir, f.Name)
				if err != nil {
					continue
				}
				if f.FileInfo().IsDir() {
					os.MkdirAll(fpath, os.ModePerm)
					continue
				}
				os.MkdirAll(filepath.Dir(fpath), os.ModePerm)
				outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
				if err != nil {
					return false, "Dosya çıkartılamadı: " + err.Error()
				}
				rc, err := f.Open()
				if err != nil {
					outFile.Close()
					return false, "Zip okuma hatası: " + err.Error()
				}
				io.Copy(outFile, rc)
				outFile.Close()
				rc.Close()
			}
		}
	}

	cmd := execCommandHidden(setupExe, "-i", "-h")
	cmd.Dir = micDriverDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, "Kurulum hatası: " + string(out) + " - " + err.Error()
	}
	return true, "Sanal Mikrofon Sürücüsü (VB-Cable) başarıyla kuruldu!\n\nÖNEMLİ: Kurulumdan sonra Windows varsayılan ses çıkış aygıtını (Hoparlör) 'CABLE Input' olarak değiştirmiş olabilir. Eğer bilgisayarınızdan ses gelmiyorsa:\n1. Görev çubuğundaki ses simgesine sağ tıklayın.\n2. Ses Ayarları'nı açın.\n3. Çıkış cihazı (Hoparlör/Kulaklık) olarak kendi cihazınızı tekrar seçin."
}

func copyDir(src string, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			err = os.MkdirAll(dstPath, 0755)
			if err != nil {
				return err
			}
			err = copyDir(srcPath, dstPath)
			if err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			err = os.WriteFile(dstPath, data, 0755)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func getUSBTetheringIP() string {
	return getUSBTetheringIPNative()
}

func execCommandHidden(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
