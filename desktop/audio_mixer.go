package main

// ----------------- Per-App Audio Sessions (Core Audio / WASAPI) -----------------
//
// Windows exposes one "audio session" per application rendering sound to the
// default endpoint. This file enumerates those sessions through the Core Audio
// COM API (IMMDeviceEnumerator -> IAudioSessionManager2 -> IAudioSessionEnumerator)
// and drives per-application volume with ISimpleAudioVolume, i.e. exactly what the
// Windows Volume Mixer does. Everything talks to native COM/Win32: no PowerShell
// and no cmd.exe wrappers (AGENTS.md §2.1).

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// AudioSessionInfo is the JSON shape of one per-application volume channel.
type AudioSessionInfo struct {
	PID     uint32  `json:"pid"`    // owning process id (0 = system sounds)
	Name    string  `json:"name"`   // lower-cased executable name, e.g. "chrome.exe"
	Title   string  `json:"title"`  // friendly label shown in the UI
	Volume  float64 `json:"volume"` // 0-100
	Muted   bool    `json:"muted"`  //
	Active  bool    `json:"active"` // session is currently rendering audio
	System  bool    `json:"system"` // System Sounds session (no owning app)
	ExePath string  `json:"exe_path,omitempty"`
}

// walkAudioSessions initialises COM on the calling goroutine, opens the default
// render endpoint, and invokes fn once per audio session. Each session control is
// released right after fn returns, so callbacks must not retain the pointer.
func walkAudioSessions(fn func(asc *wca.IAudioSessionControl) error) error {
	// COM apartments are per-thread, so the whole walk must stay on one thread
	// (same pattern as the other COM helpers in win32_native.go).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// RPC_E_CHANGED_MODE (0x80010106) means COM is already initialised on this
	// thread with another model, which is still usable.
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		if oleErr, ok := err.(*ole.OleError); !ok || oleErr.Code() != 0x80010106 {
			return fmt.Errorf("CoInitializeEx: %w", err)
		}
	}
	defer ole.CoUninitialize()

	var deviceEnumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator, &deviceEnumerator); err != nil {
		return fmt.Errorf("MMDeviceEnumerator: %w", err)
	}
	defer deviceEnumerator.Release()

	var device *wca.IMMDevice
	if err := deviceEnumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EMultimedia, &device); err != nil {
		return fmt.Errorf("default render endpoint: %w", err)
	}
	defer device.Release()

	var sessionManager *wca.IAudioSessionManager2
	if err := device.Activate(wca.IID_IAudioSessionManager2, wca.CLSCTX_ALL, nil, &sessionManager); err != nil {
		return fmt.Errorf("IAudioSessionManager2: %w", err)
	}
	defer sessionManager.Release()

	var sessions *wca.IAudioSessionEnumerator
	if err := sessionManager.GetSessionEnumerator(&sessions); err != nil {
		return fmt.Errorf("session enumerator: %w", err)
	}
	defer sessions.Release()

	var count int
	if err := sessions.GetCount(&count); err != nil {
		return fmt.Errorf("session count: %w", err)
	}

	for i := range count {
		var session *wca.IAudioSessionControl
		if err := sessions.GetSession(i, &session); err != nil || session == nil {
			continue
		}
		cbErr := fn(session)
		session.Release()
		if cbErr != nil {
			return cbErr
		}
	}
	return nil
}

// audioSessionFacts reads the identifying facts of one session. ok is false for
// expired sessions, which are no longer usable.
func audioSessionFacts(asc *wca.IAudioSessionControl) (AudioSessionInfo, bool) {
	var info AudioSessionInfo

	var state uint32
	if err := asc.GetState(&state); err == nil {
		if state == wca.AudioSessionStateExpired {
			return info, false
		}
		info.Active = state == wca.AudioSessionStateActive
	}

	var displayName string
	_ = asc.GetDisplayName(&displayName)

	var control2 *wca.IAudioSessionControl2
	if err := asc.PutQueryInterface(wca.IID_IAudioSessionControl2, &control2); err == nil && control2 != nil {
		var pid uint32
		_ = control2.GetProcessId(&pid)
		info.PID = pid
		if control2.IsSystemSoundsSession() == nil {
			info.System = true
		}
		var identifier string
		if control2.GetSessionIdentifier(&identifier) == nil {
			info.ExePath = exePathFromSessionIdentifier(identifier)
		}
		control2.Release()
	}

	if info.PID != 0 && uint32(os.Getpid()) != info.PID {
		info.Name = processNameByPID(info.PID)
	}
	if info.ExePath != "" {
		if base := strings.ToLower(filepath.Base(info.ExePath)); base != "" && base != "." {
			info.Name = base
		}
	}
	if info.System || info.PID == 0 {
		info.System = true
		info.Name = "system"
	}
	if info.Name == "" {
		info.Name = fmt.Sprintf("pid-%d", info.PID)
	}

	// The mixer exposes per-session volume through the same COM object.
	var volume *wca.ISimpleAudioVolume
	if err := asc.PutQueryInterface(wca.IID_ISimpleAudioVolume, &volume); err == nil && volume != nil {
		var level float32
		if volume.GetMasterVolume(&level) == nil {
			info.Volume = math.Round(float64(level) * 100)
		}
		var muted bool
		if volume.GetMute(&muted) == nil {
			info.Muted = muted
		}
		volume.Release()
	}

	if info.System {
		info.Title = "Sistem Sesleri"
	} else if strings.EqualFold(displayName, info.Name) || displayName == "" {
		info.Title = prettyAppName(info.Name)
	} else {
		info.Title = displayName
	}
	return info, true
}

// exePathFromSessionIdentifier extracts "C:\...\app.exe" out of a WASAPI session
// identifier of the shape "{...}.{...}|\Device\HarddiskVolume2\path\app.exe%b{...}".
func exePathFromSessionIdentifier(identifier string) string {
	bar := strings.LastIndex(identifier, "|")
	if bar < 0 || bar+1 >= len(identifier) {
		return ""
	}
	path := identifier[bar+1:]
	if pct := strings.Index(path, "%"); pct >= 0 {
		path = path[:pct]
	}
	path = strings.TrimSpace(path)
	if !strings.ContainsAny(path, `:\`) || !strings.HasSuffix(strings.ToLower(path), ".exe") {
		return ""
	}
	return path
}

// processNameByPID returns the lower-cased executable name owning a process.
func processNameByPID(pid uint32) string {
	if pid == 0 {
		return ""
	}
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)

	exeBuf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(exeBuf))
	if r, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&exeBuf[0])), uintptr(unsafe.Pointer(&size))); r == 0 {
		return ""
	}
	return strings.ToLower(filepath.Base(syscall.UTF16ToString(exeBuf[:size])))
}

// prettyAppName turns "spotify.exe" into "Spotify" for a readable card title.
func prettyAppName(exe string) string {
	name := strings.TrimSuffix(exe, ".exe")
	if name == "" {
		return ""
	}
	if c := name[0]; c >= 'a' && c <= 'z' {
		name = string(c-32) + name[1:]
	}
	return name
}

// listAudioSessions returns the merged per-application view of every live render
// session. Processes that own several sessions (e.g. Chrome) are collapsed into a
// single entry so the UI shows one slider per application.
func listAudioSessions() ([]AudioSessionInfo, error) {
	merged := make(map[string]*AudioSessionInfo)
	var order []string

	err := walkAudioSessions(func(asc *wca.IAudioSessionControl) error {
		info, ok := audioSessionFacts(asc)
		if !ok {
			return nil
		}
		key := info.Name
		if existing, found := merged[key]; found {
			if info.Volume > existing.Volume {
				existing.Volume = info.Volume
			}
			existing.Muted = existing.Muted && info.Muted
			existing.Active = existing.Active || info.Active
			if existing.Title == "" || existing.Title == prettyAppName(existing.Name) {
				if info.Title != "" {
					existing.Title = info.Title
				}
			}
			if existing.ExePath == "" {
				existing.ExePath = info.ExePath
			}
			if !existing.Active {
				existing.PID = info.PID
			}
			return nil
		}
		copied := info
		merged[key] = &copied
		order = append(order, key)
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]AudioSessionInfo, 0, len(order))
	for _, key := range order {
		out = append(out, *merged[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		if out[i].System != out[j].System {
			return !out[i].System
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

// setPerAppVolume drives the volume (0-100) of every session owned by the given
// pid or executable name. It reports how many sessions were touched.
func setPerAppVolume(pid uint32, name string, level float64) (int, error) {
	target := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".exe")
	level = math.Max(0, math.Min(100, level))
	volumeScalar := float32(level / 100.0)
	applied := 0

	err := walkAudioSessions(func(asc *wca.IAudioSessionControl) error {
		info, ok := audioSessionFacts(asc)
		if !ok {
			return nil
		}
		matches := (pid != 0 && info.PID == pid) ||
			(target != "" && strings.TrimSuffix(strings.ToLower(info.Name), ".exe") == target)
		if !matches {
			return nil
		}

		var volume *wca.ISimpleAudioVolume
		if err := asc.PutQueryInterface(wca.IID_ISimpleAudioVolume, &volume); err != nil || volume == nil {
			return nil
		}
		defer volume.Release()
		if err := volume.SetMasterVolume(volumeScalar, nil); err != nil {
			return nil
		}
		if level > 0 && info.Muted {
			_ = volume.SetMute(false, nil)
		}
		applied++
		return nil
	})
	return applied, err
}

// handleAPIAudioSessions serves GET /api/audio_sessions with the live list of
// per-application audio channels.
func handleAPIAudioSessions(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !isAuthorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	sessions, err := listAudioSessions()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"sessions": sessions,
	})
}

// handleSetAppVolumeWS is the WebSocket handler for {"type":"set_app_volume"}.
// It accepts either a "pid" or a "name" plus a "volume" in 0-100 and broadcasts
// the resulting level so every connected client stays in sync.
func handleSetAppVolumeWS(msg map[string]interface{}) {
	pid := uint32(0)
	if p, ok := msg["pid"].(float64); ok && p > 0 {
		pid = uint32(p)
	}
	name, _ := msg["name"].(string)
	level := float64(0)
	if l, ok := msg["volume"].(float64); ok {
		level = l
	}
	if pid == 0 && strings.TrimSpace(name) == "" {
		return
	}

	applied, err := setPerAppVolume(pid, name, level)
	broadcast(map[string]interface{}{
		"type":    "app_volume_changed",
		"pid":     pid,
		"name":    name,
		"volume":  math.Max(0, math.Min(100, level)),
		"applied": applied,
		"success": err == nil && applied > 0,
	})
}
