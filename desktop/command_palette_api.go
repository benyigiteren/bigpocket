package main

// Command palette (Ctrl+K) system action endpoint.
//
// The desktop web UI (desktop/frontend/command_palette.js) executes system level
// actions — volume, lock, virtual monitor, screenshot, clipboard push and app
// launching — through this single whitelisted endpoint. The route is registered
// from init() on purpose: the shared route table in main.go stays untouched.
//
// Every action id below maps to a fixed, hard-coded command; nothing received from
// the client is ever interpolated into a shell, so there is no injection surface.

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/atotto/clipboard"
	"github.com/kbinani/screenshot"
)

func init() {
	http.HandleFunc("/api/palette/action", handleAPICmdkAction)
}

type cmdkActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	Level   int    `json:"level,omitempty"`
}

var (
	modCmdkUser32      = syscall.NewLazyDLL("user32.dll")
	procCmdkLockScreen = modCmdkUser32.NewProc("LockWorkStation")
)

func cmdkOK(message string) cmdkActionResponse {
	return cmdkActionResponse{Success: true, Message: message}
}

func cmdkFail(message string) cmdkActionResponse {
	return cmdkActionResponse{Success: false, Message: message}
}

func handleAPICmdkAction(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Password, X-Client-Id")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(cmdkFail("Yalnızca POST desteklenir."))
		return
	}

	var payload struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(cmdkFail("Geçersiz istek gövdesi."))
		return
	}

	_ = json.NewEncoder(w).Encode(cmdkRunAction(strings.ToLower(strings.TrimSpace(payload.Action))))
}

// cmdkRunAction executes one whitelisted palette action.
func cmdkRunAction(action string) cmdkActionResponse {
	switch action {
	case "volume_up", "volume_down", "volume_mute":
		return cmdkVolume(action[strings.Index(action, "_")+1:])
	case "lock_screen":
		if cmdkLockWorkstation() {
			return cmdkOK("Bilgisayar kilitlendi.")
		}
		return cmdkFail("Ekran kilitlenemedi.")
	case "monitor_toggle":
		// screenshot.NumActiveDisplays() counts real monitors plus the virtual one.
		enable := screenshot.NumActiveDisplays() <= 1
		success, message := setVirtualMonitor(enable)
		if !success {
			return cmdkFail(message)
		}
		if enable {
			return cmdkOK("Sanal monitör açıldı.")
		}
		return cmdkOK("Sanal monitör kapatıldı.")
	case "screenshot":
		path, err := captureScreenToPNG()
		if err != nil {
			return cmdkFail("Ekran görüntüsü alınamadı: " + err.Error())
		}
		return cmdkActionResponse{Success: true, Message: "Ekran görüntüsü kaydedildi.", Path: path}
	case "clipboard_sync":
		text, err := clipboard.ReadAll()
		if err != nil {
			return cmdkFail("Pano okunamadı: " + err.Error())
		}
		if strings.TrimSpace(text) == "" {
			return cmdkFail("Panoda eşitlenecek metin yok.")
		}
		// Mirror startClipboardPolling(): hand the PC clipboard to every connected
		// phone without waiting for the next poll tick.
		lastClipboardText = text
		broadcast(map[string]interface{}{"type": "clipboard_sync", "text": text})
		return cmdkOK("Pano telefona eşitlendi.")
	case "app_taskmgr", "app_calc", "app_notepad", "app_terminal", "app_vscode", "app_browser":
		return cmdkLaunchApp(action)
	}
	return cmdkFail("Bilinmeyen komut.")
}

func cmdkVolume(verb string) cmdkActionResponse {
	level, err := adjustMasterVolume(verb, 0)
	if err != nil {
		return cmdkFail("Ses denetimi başarısız: " + err.Error())
	}
	broadcast(map[string]interface{}{"type": "volume_changed", "action": verb, "level": level})
	switch verb {
	case "up":
		return cmdkActionResponse{Success: true, Message: "Ses açıldı.", Level: level}
	case "down":
		return cmdkActionResponse{Success: true, Message: "Ses kısıldı.", Level: level}
	}
	return cmdkOK("Ses durumu değiştirildi.")
}

func cmdkLockWorkstation() bool {
	ret, _, _ := procCmdkLockScreen.Call()
	return ret != 0
}

// cmdkLaunchApp starts one of the palette's quick applications. Only fixed names and
// paths from the lists below reach ShellExecuteW.
func cmdkLaunchApp(action string) cmdkActionResponse {
	var candidates []string
	var success string
	var missing string

	switch action {
	case "app_taskmgr":
		candidates, success, missing = []string{"taskmgr.exe"}, "Görev Yöneticisi açıldı.", "Görev Yöneticisi bulunamadı."
	case "app_calc":
		candidates, success, missing = []string{"calc.exe"}, "Hesap Makinesi açıldı.", "Hesap Makinesi bulunamadı."
	case "app_notepad":
		candidates, success, missing = []string{"notepad.exe"}, "Not Defteri açıldı.", "Not Defteri bulunamadı."
	case "app_terminal":
		candidates, success, missing = []string{"wt.exe", "cmd.exe"}, "Terminal açıldı.", "Terminal bulunamadı."
	case "app_vscode":
		candidates = []string{
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Microsoft VS Code", "Code.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft VS Code", "Code.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft VS Code", "Code.exe"),
			"code",
			"code.cmd",
		}
		success, missing = "VS Code açıldı.", "VS Code kurulumu bulunamadı."
	case "app_browser":
		if err := openURLNative("https://www.google.com"); err != nil {
			return cmdkFail("Tarayıcı açılamadı: " + err.Error())
		}
		return cmdkOK("Tarayıcı açıldı.")
	default:
		return cmdkFail("Bilinmeyen uygulama komutu.")
	}

	target := cmdkFirstExecutable(candidates)
	if target == "" {
		return cmdkFail(missing)
	}
	if err := shellExecuteNative("open", target, "", "", swShow); err != nil {
		return cmdkFail(missing)
	}
	return cmdkOK(success)
}

// cmdkFirstExecutable returns the first candidate that really exists on this machine.
// Absolute-ish paths are stat'ed, bare names are resolved through PATH.
func cmdkFirstExecutable(candidates []string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if strings.ContainsAny(candidate, `/\:`) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
	}
	return ""
}
