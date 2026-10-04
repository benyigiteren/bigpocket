package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/atotto/clipboard"
	"github.com/gorilla/websocket"
	"github.com/jchv/go-webview2"
	"github.com/kbinani/screenshot"
)

// Global States & Configurations
var (
	exePath, _          = os.Executable()
	exeDir              = filepath.Dir(exePath)
	appDataDir          = filepath.Join(os.Getenv("APPDATA"), "BigPocket")
	sharedDir           = filepath.Join(appDataDir, "SharedFiles")
	configFile          = filepath.Join(appDataDir, "config.json")
	driverDir           = filepath.Join(exeDir, "usbmmidd")
	latestCameraFrame   []byte
	latestCameraLock    sync.RWMutex
	websocketClients    = make(map[*websocket.Conn]bool)
	websocketClientsMu  sync.Mutex
	lastClipboardText   string
	upgrader            = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

type StreamDeckButton struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`  // hotkey or command
	Value string `json:"value"` // keybind or script
	Icon  string `json:"icon"`  // Custom SVG path or local image
}

type Config struct {
	Theme             string             `json:"theme"`
	Password          string             `json:"password"`
	StreamDeckRows    int                `json:"stream_deck_rows"`
	StreamDeckCols    int                `json:"stream_deck_cols"`
	StreamDeckButtons []StreamDeckButton `json:"stream_deck_buttons"`
}

var defaultConfig = Config{
	Theme:          "pro",
	Password:       "", // Default is no password
	StreamDeckRows: 2,
	StreamDeckCols: 4,
	StreamDeckButtons: []StreamDeckButton{
		{ID: 0, Label: "Mute Mic", Type: "hotkey", Value: "f20", Icon: ""},
		{ID: 1, Label: "Vol Up", Type: "hotkey", Value: "volumeup", Icon: ""},
		{ID: 2, Label: "Vol Down", Type: "hotkey", Value: "volumedown", Icon: ""},
		{ID: 3, Label: "Play/Pause", Type: "hotkey", Value: "playpause", Icon: ""},
		{ID: 4, Label: "Calc", Type: "command", Value: "calc.exe", Icon: ""},
		{ID: 5, Label: "Browser", Type: "command", Value: "cmd /c start https://google.com", Icon: ""},
		{ID: 6, Label: "Lock PC", Type: "command", Value: "rundll32.exe user32.dll,LockWorkStation", Icon: ""},
		{ID: 7, Label: "Task Manager", Type: "hotkey", Value: "ctrl+shift+esc", Icon: ""},
	},
}

func main() {
	// Remove leftovers from a previous self-update (and wait for old process if restarted)
	cleanupOldUpdate()

	// Request admin permissions automatically
	ensureAdmin()

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



	// Load or create config
	loadConfig()

	// Start servers in goroutines
	go startCameraServer()
	go startAudioServer()
	go startScreenStreamServer()
	go startWebSocketServer()
	go startClipboardPolling()
	go startDiscovery()

	// Handle HTTP Routing
	setupHttpRoutes()
	setupUpdateRoutes()

	// Start HTTP Server in background goroutine
	go func() {
		fmt.Println("BigPocket Backend is running on port 8085...")
		err := http.ListenAndServe("0.0.0.0:8085", nil)
		if err != nil {
			fmt.Println("HTTP server error:", err)
		}
	}()

	// Let the server spin up, then open Native WebView2 window
	time.Sleep(500 * time.Millisecond)
	
	w := webview2.New(false)
	if w != nil {
		defer w.Destroy()
		w.SetTitle("BigPocket")
		w.SetSize(1100, 750, webview2.HintNone)
		setWindowIcon(uintptr(w.Window()))
		w.Navigate("http://localhost:8085")
		w.Run()
		
		// Clean exit when WebView2 window is closed
		fmt.Println("WebView2 window closed. Exiting server...")
		cleanupOnExit()
		os.Exit(0)
	} else {
		// Fallback to default browser
		fmt.Println("WebView2 runtime not available, falling back to default browser...")
		execCommandHidden("cmd", "/c", "start", "http://localhost:8085").Start()
		
		// Keep the process alive
		select {}
	}
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
		hIcon = ret
	}

	if hIcon != 0 {
		// WM_SETICON = 0x0080, ICON_SMALL = 0, ICON_BIG = 1
		sendMessage.Call(hwnd, 0x0080, 0, hIcon)
		sendMessage.Call(hwnd, 0x0080, 1, hIcon)
	}
}

// ----------------- Desktop Shortcut Helper -----------------
func createDesktopShortcut() {
	homeDir, _ := os.UserHomeDir()
	shortcutPath := filepath.Join(homeDir, "Desktop", "BigPocket.lnk")
	if _, err := os.Stat(shortcutPath); os.IsNotExist(err) {
		exePath, err := os.Executable()
		if err != nil {
			return
		}
		exeDir := filepath.Dir(exePath)
		logoPath := filepath.Join(exeDir, "logo.ico")
		
		iconLocation := exePath
		if _, err := os.Stat(logoPath); err == nil {
			iconLocation = logoPath
		}
		
		// Powershell COM interface to create .lnk shortcut with explicit IconLocation
		psCmd := fmt.Sprintf(
			`$WshShell = New-Object -ComObject WScript.Shell; $Shortcut = $WshShell.CreateShortcut("%s"); $Shortcut.TargetPath = "%s"; $Shortcut.WorkingDirectory = "%s"; $Shortcut.IconLocation = "%s"; $Shortcut.Save()`,
			strings.ReplaceAll(shortcutPath, `\`, `\\`),
			strings.ReplaceAll(exePath, `\`, `\\`),
			strings.ReplaceAll(exeDir, `\`, `\\`),
			strings.ReplaceAll(iconLocation, `\`, `\\`),
		)
		execCommandHidden("powershell", "-Command", psCmd).Start()
		fmt.Println("Desktop shortcut created automatically:", shortcutPath)
	}
}

// ----------------- Cleanup Resources -----------------
func cleanupOnExit() {
	fmt.Println("Cleaning up virtual monitor driver...")
	if checkIfAdmin() {
		setVirtualMonitor(false)
	}
}

// ----------------- Configurations -----------------
func loadConfig() Config {
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		saveConfig(defaultConfig)
		return defaultConfig
	}
	file, err := os.ReadFile(configFile)
	if err != nil {
		return defaultConfig
	}
	var cfg Config
	err = json.Unmarshal(file, &cfg)
	if err != nil {
		return defaultConfig
	}
	if cfg.StreamDeckRows <= 0 || cfg.StreamDeckCols <= 0 {
		cfg.StreamDeckRows = 2
		cfg.StreamDeckCols = 4
		saveConfig(cfg)
	}
	return cfg
}

func saveConfig(cfg Config) {
	if cfg.StreamDeckRows <= 0 {
		cfg.StreamDeckRows = 2
	}
	if cfg.StreamDeckCols <= 0 {
		cfg.StreamDeckCols = 4
	}
	expectedSize := cfg.StreamDeckRows * cfg.StreamDeckCols
	if len(cfg.StreamDeckButtons) < expectedSize {
		for i := len(cfg.StreamDeckButtons); i < expectedSize; i++ {
			cfg.StreamDeckButtons = append(cfg.StreamDeckButtons, StreamDeckButton{
				ID:    i,
				Label: fmt.Sprintf("Buton %d", i+1),
				Type:  "hotkey",
				Value: "",
				Icon:  "",
			})
		}
	} else if len(cfg.StreamDeckButtons) > expectedSize {
		cfg.StreamDeckButtons = cfg.StreamDeckButtons[:expectedSize]
	}

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
		go handleAudioConnection(conn)
	}
}

func handleAudioConnection(conn net.Conn) {
	defer conn.Close()
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
					hdr := (*WAVEHDR)(unsafe.Pointer(qh.hdrPtr))
					if hdr.Flags&WHDR_DONE != 0 || time.Since(qh.time) > 4*time.Second {
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
		pcmPtr, _, _ := localAlloc.Call(LPTR, uintptr(n))

		// Copy data to unmanaged memory
		rtlMoveMemory.Call(pcmPtr, uintptr(unsafe.Pointer(&readBuf[0])), uintptr(n))

		hdr := (*WAVEHDR)(unsafe.Pointer(hdrPtr))
		hdr.Data = pcmPtr
		hdr.BufferLength = uint32(n)

		// Prepare & Play
		ret, _, _ = waveOutPrepareHeader.Call(hwo, hdrPtr, unsafe.Sizeof(WAVEHDR{}))
		if ret == 0 {
			ret, _, _ = waveOutWrite.Call(hwo, hdrPtr, unsafe.Sizeof(WAVEHDR{}))
			if ret == 0 {
				mu.Lock()
				activeHeaders = append(activeHeaders, queuedHeader{
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
		go handleCameraConnection(conn)
	}
}

func handleCameraConnection(conn net.Conn) {
	defer conn.Close()
	fmt.Println("Camera client connected.")

	for {
		// Read 4-byte length
		var length int32
		err := binary.Read(conn, binary.BigEndian, &length)
		if err != nil {
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

// ----------------- Screen Grabber & Streamer -----------------
func startScreenStreamServer() {
	l, err := net.Listen("tcp", "0.0.0.0:8086")
	if err != nil {
		fmt.Println("Screen stream server bind error:", err)
		return
	}
	defer l.Close()
	fmt.Println("Screen Stream Server running on port 8086...")

	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		go handleScreenStream(conn)
	}
}

func handleScreenStream(conn net.Conn) {
	defer conn.Close()
	fmt.Println("Screen stream client connected.")

	for {
		numMonitors := screenshot.NumActiveDisplays()
		if numMonitors == 0 {
			time.Sleep(1 * time.Second)
			continue
		}

		// Use 2nd monitor if exists, otherwise primary
		monIndex := 0
		if numMonitors > 1 {
			monIndex = 1
		}

		bounds := screenshot.GetDisplayBounds(monIndex)
		img, err := screenshot.CaptureRect(bounds)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Compress image to JPEG in memory
		var out bytes.Buffer
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 50})
		if err != nil {
			continue
		}
		data := out.Bytes()
		length := int32(len(data))

		// Send size prefix (4-byte big endian)
		err = binary.Write(conn, binary.BigEndian, length)
		if err != nil {
			break
		}

		// Send data
		_, err = conn.Write(data)
		if err != nil {
			break
		}

		time.Sleep(40 * time.Millisecond) // ~25 FPS
	}
	fmt.Println("Screen stream client disconnected.")
}

// ----------------- WebSocket Controls -----------------
func startWebSocketServer() {
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		cfg := loadConfig()
		authenticated := false
		remoteHost, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		isLocal := (err == nil && (remoteHost == "127.0.0.1" || remoteHost == "::1" || remoteHost == "localhost"))
		if cfg.Password == "" || isLocal {
			authenticated = true
		}

		// If authentication is required, send request
		if !authenticated {
			authReq, _ := json.Marshal(map[string]interface{}{"type": "auth_required"})
			conn.WriteMessage(websocket.TextMessage, authReq)
			
			// Close connection if not authenticated in 5 seconds
			go func(c *websocket.Conn) {
				time.Sleep(5 * time.Second)
				if !authenticated {
					fmt.Println("WebSocket authentication timed out. Closing connection.")
					c.Close()
				}
			}(conn)
		} else {
			// No password required, send success immediately
			authSuccess, _ := json.Marshal(map[string]interface{}{"type": "auth_success"})
			conn.WriteMessage(websocket.TextMessage, authSuccess)
		}

		websocketClientsMu.Lock()
		websocketClients[conn] = true
		websocketClientsMu.Unlock()
		fmt.Println("WebSocket client connected.")

		// Broadcast connection success to all
		broadcast(map[string]interface{}{"type": "client_connected"})

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(msg, &payload); err == nil {
				mType, _ := payload["type"].(string)
				if !authenticated {
					if mType == "auth_login" {
						pw, _ := payload["password"].(string)
						if pw == cfg.Password {
							authenticated = true
							authSuccess, _ := json.Marshal(map[string]interface{}{"type": "auth_success"})
							conn.WriteMessage(websocket.TextMessage, authSuccess)
						} else {
							authFail, _ := json.Marshal(map[string]interface{}{"type": "auth_failed", "message": "Wrong password"})
							conn.WriteMessage(websocket.TextMessage, authFail)
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

		websocketClientsMu.Lock()
		delete(websocketClients, conn)
		websocketClientsMu.Unlock()
		fmt.Println("WebSocket client disconnected.")
		broadcast(map[string]interface{}{"type": "client_disconnected"})
	})
}

func broadcast(msg interface{}) {
	payload, _ := json.Marshal(msg)
	websocketClientsMu.Lock()
	defer websocketClientsMu.Unlock()
	for conn := range websocketClients {
		conn.WriteMessage(websocket.TextMessage, payload)
	}
}

func handleWebSocketControl(msg map[string]interface{}) {
	mType, ok := msg["type"].(string)
	if !ok {
		return
	}

	user32 := syscall.NewLazyDLL("user32.dll")
	mouseEvent := user32.NewProc("mouse_event")
	keybdEvent := user32.NewProc("keybd_event")

	switch mType {
	case "mouse_move":
		dx := int32(msg["dx"].(float64))
		dy := int32(msg["dy"].(float64))
		// MOUSEEVENTF_MOVE = 0x0001
		mouseEvent.Call(0x0001, uintptr(dx), uintptr(dy), 0, 0)

	case "mouse_click":
		btn := msg["button"].(string)
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
		dy := int32(msg["dy"].(float64))
		// MOUSEEVENTF_WHEEL = 0x0800
		mouseEvent.Call(0x0800, 0, 0, uintptr(dy), 0)

	case "keyboard_input":
		text := msg["text"].(string)
		typeUnicodeText(text)

	case "keyboard_key":
		key := msg["key"].(string)
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
		btnID := int(msg["button_id"].(float64))
		cfg := loadConfig()
		var targetBtn *StreamDeckButton
		for _, b := range cfg.StreamDeckButtons {
			if b.ID == btnID {
				targetBtn = &b
				break
			}
		}
		if targetBtn != nil {
			if targetBtn.Type == "hotkey" {
				simulateHotkey(targetBtn.Value)
			} else if targetBtn.Type == "command" {
				// Run command asynchronously so it does not block the WebSocket
				go execCommandHidden("cmd", "/c", targetBtn.Value).Start()
			}
		}

	case "clipboard_sync":
		text := msg["text"].(string)
		lastClipboardText = text
		clipboard.WriteAll(text)

	case "notification":
		title, _ := msg["title"].(string)
		text, _ := msg["text"].(string)
		appName, _ := msg["app"].(string)
		showNotification(title, fmt.Sprintf("[%s] %s", appName, text))

	case "monitor_touch":
		action := msg["action"].(string)
		rx := msg["x"].(float64) // 0 to 1
		ry := msg["y"].(float64) // 0 to 1

		numMonitors := screenshot.NumActiveDisplays()
		if numMonitors == 0 {
			return
		}

		monIndex := 0
		if numMonitors > 1 {
			monIndex = 1
		}
		bounds := screenshot.GetDisplayBounds(monIndex)

		px := bounds.Min.X + int(rx*float64(bounds.Dx()))
		py := bounds.Min.Y + int(ry*float64(bounds.Dy()))

		// Move cursor to absolute position
		// Wait, user32 SetCursorPos:
		setCursorPos := user32.NewProc("SetCursorPos")
		setCursorPos.Call(uintptr(px), uintptr(py))

		if action == "down" {
			mouseEvent.Call(0x0002, 0, 0, 0, 0) // LEFTDOWN
		} else if action == "up" {
			mouseEvent.Call(0x0004, 0, 0, 0, 0) // LEFTUP
		} else if action == "click" {
			mouseEvent.Call(0x0002, 0, 0, 0, 0)
			mouseEvent.Call(0x0004, 0, 0, 0, 0)
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
	keybdEvent.Call(0x11, 0, 0, 0)        // Ctrl Down
	keybdEvent.Call(0x56, 0, 0, 0)        // V Down
	keybdEvent.Call(0x56, 0, 0x0002, 0)   // V Up
	keybdEvent.Call(0x11, 0, 0x0002, 0)   // Ctrl Up

	// Restore clipboard after a small delay
	go func() {
		time.Sleep(150 * time.Millisecond)
		if oldText != "" {
			clipboard.WriteAll(oldText)
		}
	}()
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
	// Simple powershell balloon toast notification (Cgo-free and compatible)
	psCmd := fmt.Sprintf(
		`[void] [System.Reflection.Assembly]::LoadWithPartialName("System.Windows.Forms"); $n = New-Object System.Windows.Forms.NotifyIcon; $n.Icon = [System.Drawing.SystemIcons]::Information; $n.BalloonTipIcon = "Info"; $n.BalloonTipText = "%s"; $n.BalloonTipTitle = "%s"; $n.Visible = $True; $n.ShowBalloonTip(5000);`,
		strings.ReplaceAll(message, `"`, `\"`),
		strings.ReplaceAll(title, `"`, `\"`),
	)
	execCommandHidden("powershell", "-Command", psCmd).Start()
}

// ----------------- System Helpers -----------------

// ----------------- HTTP Auth Helpers -----------------
func isAuthorized(r *http.Request) bool {
	// PC Localhost / Loopback requests never require a password
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if host == "127.0.0.1" || host == "::1" || host == "localhost" {
			return true
		}
	} else if r.RemoteAddr == "127.0.0.1" || r.RemoteAddr == "::1" || r.RemoteAddr == "localhost" {
		return true
	}

	cfg := loadConfig()
	if cfg.Password == "" {
		return true
	}
	pw := r.Header.Get("X-Password")
	if pw == "" {
		pw = r.URL.Query().Get("password")
	}
	return pw == cfg.Password
}

func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if !isAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return false
	}
	return true
}

// ----------------- HTTP Routes -----------------
func setupHttpRoutes() {
	// Serve Logo SVG
	http.HandleFunc("/logo.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		svgPath := filepath.Join(exeDir, "frontend", "logo.svg")
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
	fs := http.FileServer(http.Dir(filepath.Join(exeDir, "frontend")))
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
			"success":  true,
			"ip":       ipAddr,
			"ips":      ips,
			"usb_ip":   usbIP,
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
			var cfg Config
			err := json.NewDecoder(r.Body).Decode(&cfg)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			// Automatically extract icons for command buttons if possible
			for i := range cfg.StreamDeckButtons {
				btn := &cfg.StreamDeckButtons[i]
				if btn.Type == "command" && btn.Value != "" {
					lowerVal := strings.ToLower(btn.Value)
					if strings.HasSuffix(lowerVal, ".exe") || strings.HasSuffix(lowerVal, ".lnk") || strings.Contains(btn.Value, `\`) || strings.Contains(btn.Value, `/`) {
						iconPath, err := extractAppIcon(btn.Value, btn.ID)
						if err == nil && iconPath != "" {
							btn.Icon = "custom:" + iconPath
						}
					}
				}
			}

			saveConfig(cfg)
			broadcast(map[string]interface{}{"type": "config_update"})
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
		filename := strings.TrimPrefix(r.URL.Path, "/download/")
		filePath := filepath.Join(sharedDir, filename)
		http.ServeFile(w, r, filePath)
	})

	// Delete File
	http.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		if r.Method == http.MethodDelete {
			filename := strings.TrimPrefix(r.URL.Path, "/files/")
			filePath := filepath.Join(sharedDir, filename)
			err := os.Remove(filePath)
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

		filePath := filepath.Join(sharedDir, handler.Filename)
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

		file, handler, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		ext := strings.ToLower(filepath.Ext(handler.Filename))
		if ext == "" {
			ext = ".png"
		}

		err = os.MkdirAll(filepath.Join(exeDir, "frontend", "icons"), 0755)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		iconFilename := fmt.Sprintf("button_%s%s", buttonIDStr, ext)
		iconPath := filepath.Join(exeDir, "frontend", "icons", iconFilename)

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
		var buttonID int
		fmt.Sscanf(buttonIDStr, "%d", &buttonID)

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
			broadcast(map[string]interface{}{"type": "config_update"})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true, 
			"icon": "custom:icons/" + iconFilename,
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

		iconPath, err := extractAppIcon(appPath, buttonID)
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
			simulateHotkey(targetBtn.Value)
		} else if targetBtn.Type == "command" {
			go execCommandHidden("cmd", "/c", targetBtn.Value).Start()
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
	http.HandleFunc("/api/streamdeck/trigger", handleAPIStreamDeckTrigger)
	http.HandleFunc("/api/streamdeck/button", handleAPIStreamDeckButton)
	http.HandleFunc("/api/streamdeck/layout", handleAPIStreamDeckLayout)
	http.HandleFunc("/api/apps", handleAPIApps)
	http.HandleFunc("/api/clipboard", handleAPIClipboard)
	http.HandleFunc("/api/notify", handleAPINotify)
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
		Description: "Retrieves current Stream Deck configuration, grid layout (rows, cols), and all buttons.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "set_streamdeck_button",
		Description: "Configures or edits a specific Stream Deck button slot (e.g. set label, hotkey combination, CMD application path, icon).",
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
					"enum":        []string{"hotkey", "command"},
					"description": "Action type: 'hotkey' for keyboard shortcuts, 'command' for launching apps or commands",
				},
				"value": map[string]interface{}{
					"type":        "string",
					"description": "Hotkey string (e.g. 'ctrl+shift+m') or Command/App path (e.g. 'calc.exe')",
				},
				"icon": map[string]interface{}{
					"type":        "string",
					"description": "Unicode icon key (e.g. 'mic', 'desktop', 'volume_up', 'play') or custom image path",
				},
			},
			"required": []string{"button_id", "label", "type", "value"},
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
}

func handleMCPEndpoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

	case "set_streamdeck_button":
		btnIDFloat, ok := args["button_id"].(float64)
		if !ok {
			return "Error: button_id must be an integer", true
		}
		btnID := int(btnIDFloat)
		label, _ := args["label"].(string)
		bType, _ := args["type"].(string)
		val, _ := args["value"].(string)
		icon, _ := args["icon"].(string)

		cfg := loadConfig()
		found := false
		for i := range cfg.StreamDeckButtons {
			if cfg.StreamDeckButtons[i].ID == btnID {
				cfg.StreamDeckButtons[i].Label = label
				cfg.StreamDeckButtons[i].Type = bType
				cfg.StreamDeckButtons[i].Value = val
				cfg.StreamDeckButtons[i].Icon = icon
				found = true
				break
			}
		}
		if !found {
			cfg.StreamDeckButtons = append(cfg.StreamDeckButtons, StreamDeckButton{
				ID:    btnID,
				Label: label,
				Type:  bType,
				Value: val,
				Icon:  icon,
			})
		}
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Successfully configured button #%d: [%s] (%s: %s)", btnID, label, bType, val), false

	case "set_grid_layout":
		rowsFloat, ok1 := args["rows"].(float64)
		colsFloat, ok2 := args["cols"].(float64)
		if !ok1 || !ok2 {
			return "Error: rows and cols must be numbers", true
		}
		rows := int(rowsFloat)
		cols := int(colsFloat)
		if rows < 1 || rows > 5 || cols < 2 || cols > 8 {
			return "Error: rows must be 1-5, cols must be 2-8", true
		}
		cfg := loadConfig()
		cfg.StreamDeckRows = rows
		cfg.StreamDeckCols = cols
		targetTotal := rows * cols
		for len(cfg.StreamDeckButtons) < targetTotal {
			newID := len(cfg.StreamDeckButtons)
			cfg.StreamDeckButtons = append(cfg.StreamDeckButtons, StreamDeckButton{
				ID:    newID,
				Label: fmt.Sprintf("Slot %d", newID+1),
				Type:  "hotkey",
				Value: "",
				Icon:  "",
			})
		}
		saveConfig(cfg)
		broadcast(map[string]interface{}{"type": "config_update"})
		return fmt.Sprintf("Successfully updated grid layout to %dx%d (%d buttons)", rows, cols, targetTotal), false

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
			simulateHotkey(targetBtn.Value)
		} else if targetBtn.Type == "command" {
			go execCommandHidden("cmd", "/c", targetBtn.Value).Start()
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

	default:
		return "Unknown tool: " + name, true
	}
}

// ----------------- REST API Handlers -----------------
func handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

func handleAPIStreamDeckTrigger(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	resStr, isErr := executeInternalMCPTool("set_streamdeck_button", payload)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": !isErr,
		"message": resStr,
	})
}

func handleAPIStreamDeckLayout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
			fpath := filepath.Join(driverDir, f.Name)
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
				fpath := filepath.Join(micDriverDir, f.Name)
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

func extractAppIcon(appPath string, buttonID int) (string, error) {
	iconDir := filepath.Join(exeDir, "frontend", "icons")
	os.MkdirAll(iconDir, 0755)

	iconFilename := fmt.Sprintf("button_%d.png", buttonID)
	outPath := filepath.Join(iconDir, iconFilename)

	appPathClean := strings.ReplaceAll(appPath, `'`, `''`)
	outPathClean := strings.ReplaceAll(outPath, `'`, `''`)

	psScript := fmt.Sprintf(`
Add-Type -AssemblyName System.Drawing
$target = '%s'
if ($target.EndsWith('.lnk')) {
    $sh = New-Object -ComObject WScript.Shell
    $sc = $sh.CreateShortcut($target)
    $target = $sc.TargetPath
}
if ($target.StartsWith('%%')) {
    $target = [System.Environment]::ExpandEnvironmentVariables($target)
}
if ([System.IO.File]::Exists($target)) {
    $icon = [System.Drawing.Icon]::ExtractAssociatedIcon($target)
    $bmp = $icon.ToBitmap()
    $bmp.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
    $icon.Dispose()
    $bmp.Dispose()
    Write-Output "SUCCESS"
} else {
    Write-Output "FILE_NOT_FOUND"
}
`, appPathClean, outPathClean)

	cmd := execCommandHidden("powershell", "-Command", psScript)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	res := strings.TrimSpace(stdout.String())
	if strings.Contains(res, "SUCCESS") {
		return "icons/" + iconFilename, nil
	}
	return "", fmt.Errorf("icon extraction failed: %s", res)
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
	psCmd := `Get-NetAdapter | Where-Object { $_.Status -eq 'Up' -and ($_.InterfaceDescription -like '*NDIS*' -or $_.InterfaceDescription -like '*Apple Mobile Device*' -or $_.InterfaceDescription -like '*Tethering*' -or $_.Name -like '*Tethering*') } | Get-NetIPAddress -AddressFamily IPv4 | Select-Object -ExpandProperty IPAddress`
	cmd := execCommandHidden("powershell", "-Command", psCmd)
	out, err := cmd.Output()
	if err == nil {
		ip := strings.TrimSpace(string(out))
		if ip != "" {
			parts := strings.Split(ip, "\n")
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}

func execCommandHidden(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
