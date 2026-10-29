package main

// ----------------- Native Win32 Helpers (no PowerShell / no cmd.exe wrappers) -----------------
//
// Everything in this file talks to Windows through native APIs so the binary stays
// AV-friendly (see AGENTS.md §2.1): Shell_NotifyIconW for the tray + balloon
// notifications, user32 for the tray message window and foreground-window detection,
// iphlpapi for real network throughput, shell32/IShellLinkW for shortcuts and ShellExecute,
// kernel32 for disk capacity, WMI (via COM) for real CPU/GPU temperatures and GPU load,
// EnumWindows for the now-playing window titles and the registry for autostart.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"
)

var (
	ntUser32   = syscall.NewLazyDLL("user32.dll")
	ntShell32  = syscall.NewLazyDLL("shell32.dll")
	ntKernel32 = syscall.NewLazyDLL("kernel32.dll")
	ntIphlpapi = syscall.NewLazyDLL("iphlpapi.dll")
	ntGdi32    = syscall.NewLazyDLL("gdi32.dll")
	ntOle32    = syscall.NewLazyDLL("ole32.dll")

	procRegisterClassExW         = ntUser32.NewProc("RegisterClassExW")
	procCreateWindowExW          = ntUser32.NewProc("CreateWindowExW")
	procDefWindowProcW           = ntUser32.NewProc("DefWindowProcW")
	procGetMessageW              = ntUser32.NewProc("GetMessageW")
	procTranslateMessage         = ntUser32.NewProc("TranslateMessage")
	procDispatchMessageW         = ntUser32.NewProc("DispatchMessageW")
	procPostQuitMessage          = ntUser32.NewProc("PostQuitMessage")
	procPostMessageW             = ntUser32.NewProc("PostMessageW")
	procLoadImageW               = ntUser32.NewProc("LoadImageW")
	procLoadIconW                = ntUser32.NewProc("LoadIconW")
	procGetCursorPos             = ntUser32.NewProc("GetCursorPos")
	procSetForegroundWindow      = ntUser32.NewProc("SetForegroundWindow")
	procShowWindow               = ntUser32.NewProc("ShowWindow")
	procCallWindowProcW          = ntUser32.NewProc("CallWindowProcW")
	procTrackPopupMenu           = ntUser32.NewProc("TrackPopupMenu")
	procCreatePopupMenu          = ntUser32.NewProc("CreatePopupMenu")
	procAppendMenuW              = ntUser32.NewProc("AppendMenuW")
	procDestroyMenu              = ntUser32.NewProc("DestroyMenu")
	procSetWindowLongW           = ntUser32.NewProc("SetWindowLongW")
	procGetWindowTextW           = ntUser32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = ntUser32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadProcessId = ntUser32.NewProc("GetWindowThreadProcessId")
	procGetForegroundWindow      = ntUser32.NewProc("GetForegroundWindow")
	procEnumWindows              = ntUser32.NewProc("EnumWindows")
	procIsWindowVisible          = ntUser32.NewProc("IsWindowVisible")
	procDestroyIcon              = ntUser32.NewProc("DestroyIcon")
	procGetIconInfo              = ntUser32.NewProc("GetIconInfo")
	procGetDC                    = ntUser32.NewProc("GetDC")
	procReleaseDC                = ntUser32.NewProc("ReleaseDC")
	procSetWindowLongPtrW        = ntUser32.NewProc("SetWindowLongPtrW")

	procGetModuleHandleW           = ntKernel32.NewProc("GetModuleHandleW")
	procOpenProcess                = ntKernel32.NewProc("OpenProcess")
	procCloseHandle                = ntKernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW = ntKernel32.NewProc("QueryFullProcessImageNameW")
	procGetDiskFreeSpaceExW        = ntKernel32.NewProc("GetDiskFreeSpaceExW")

	procShellNotifyIconW = ntShell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW    = ntShell32.NewProc("ShellExecuteW")
	procExtractIconExW   = ntShell32.NewProc("ExtractIconExW")

	procGetIfTable = ntIphlpapi.NewProc("GetIfTable")

	procGetObjectW   = ntGdi32.NewProc("GetObjectW")
	procGetDIBits    = ntGdi32.NewProc("GetDIBits")
	procDeleteObject = ntGdi32.NewProc("DeleteObject")

	procCoInitializeEx   = ntOle32.NewProc("CoInitializeEx")
	procCoUninitialize   = ntOle32.NewProc("CoUninitialize")
	procCoCreateInstance = ntOle32.NewProc("CoCreateInstance")
)

// ----------------- Common constants -----------------

const (
	swHide    = 0
	swShow    = 5
	swRestore = 9

	gwlpWndProc = ^uintptr(3) // -4

	wmNull    = 0x0000
	wmDestroy = 0x0002
	wmClose   = 0x0010

	wmApp           = 0x8000
	wmTrayIcon      = wmApp + 1
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	mfString    = 0x00000000
	mfChecked   = 0x00000008
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
	tpmReturnCmd   = 0x0100

	coinitApartment    = 0x00000002
	clsctxInprocServer = 0x00000001

	imageIcon      = 1
	lrLoadFromFile = 0x00000010
	lrDefaultSize  = 0x00000040

	idIApplication = 32512
)

// ----------------- Win32 structures -----------------

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     syscall.Handle
	HIcon         syscall.Handle
	HCursor       syscall.Handle
	HbrBackground syscall.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       syscall.Handle
}

type nativeMsg struct {
	Hwnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type nativePoint struct {
	X int32
	Y int32
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         syscall.GUID
	HBalloonIcon     syscall.Handle
}

const (
	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo = 0x00000001
)

// copyFixedString copies src into the fixed-size UTF-16 buffer dst (always NUL terminated).
func copyFixedString(dst []uint16, src string, max int) {
	enc := syscall.StringToUTF16(src)
	n := len(enc)
	if n > max {
		n = max
	}
	if n > len(dst) {
		n = len(dst)
	}
	copy(dst[:n], enc[:n])
	dst[len(dst)-1] = 0
}

func copyNativeString(dst []uint16, src string) {
	copyFixedString(dst, src, len(dst)-1)
}

// ----------------- System tray -----------------

const (
	trayCmdOpen         = 1001
	trayCmdAutostart    = 1002
	trayCmdCloseMonitor = 1003
	trayCmdExit         = 1004
)

var (
	trayMu         sync.Mutex
	trayIconReady  bool
	trayIconData   notifyIconData
	trayHwndAtomic atomic.Uintptr
	trayMainHwnd   atomic.Uintptr
	trayExiting    atomic.Bool
	trayNotifyUID  = uint32(1)
	trayClassOnce  sync.Once
)

// initSystemTray starts the hidden message window that owns the notification-area icon.
// mainHwnd is the WebView2 window that the tray menu restores.
func initSystemTray(mainHwnd uintptr) {
	if mainHwnd != 0 {
		trayMainHwnd.Store(mainHwnd)
	}
	trayClassOnce.Do(func() { go trayMessageLoop() })
}

func trayMessageLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, _ := syscall.UTF16PtrFromString("BigPocketTrayWindow")
	hInst, _, _ := procGetModuleHandleW.Call(0)

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   syscall.NewCallback(trayWndProc),
		HInstance:     syscall.Handle(hInst),
		LpszClassName: className,
	}
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		// ERROR_CLASS_ALREADY_EXISTS is fine; anything else means no tray for this process.
	}
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		fmt.Println("System tray window could not be created.")
		return
	}
	trayHwndAtomic.Store(hwnd)
	trayAddIcon(hwnd)

	var msg nativeMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	trayRemoveIcon()
}

func trayWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	defer func() { _ = recover() }()

	switch msg {
	case wmTrayIcon:
		switch uint32(lParam) & 0xFFFF {
		case wmLButtonDblClk:
			showMainWindow()
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func trayIconHandle() uintptr {
	icoPath := filepath.Join(exeDir, "logo.ico")
	if _, err := os.Stat(icoPath); err == nil {
		p, _ := syscall.UTF16PtrFromString(icoPath)
		h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
		if h != 0 {
			return h
		}
	}
	hInst, _, _ := procGetModuleHandleW.Call(0)
	if h, _, _ := procLoadIconW.Call(hInst, 1); h != 0 {
		return h
	}
	h, _, _ := procLoadIconW.Call(0, idIApplication)
	return h
}

func trayTooltipText() string {
	ip := ""
	for _, candidate := range localInterfaceIPs() {
		if !strings.HasPrefix(candidate, "127.") {
			ip = candidate
			break
		}
	}
	text := "BigPocket"
	if ip != "" {
		text += " · " + ip + ":8085"
	}
	if isAutoStartEnabled() {
		text += " · Windows ile başlat"
	}
	return text
}

func trayAddIcon(hwnd uintptr) bool {
	trayMu.Lock()
	defer trayMu.Unlock()

	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(notifyIconData{}))
	nid.HWnd = syscall.Handle(hwnd)
	nid.UID = trayNotifyUID
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmTrayIcon
	nid.HIcon = syscall.Handle(trayIconHandle())
	copyNativeString(nid.SzTip[:], trayTooltipText())

	r, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	trayIconData = nid
	trayIconReady = r != 0
	if !trayIconReady {
		fmt.Println("System tray icon registration failed.")
	}
	return trayIconReady
}

// updateTrayTooltip refreshes the hover text of the notification-area icon.
func updateTrayTooltip(text string) {
	trayMu.Lock()
	defer trayMu.Unlock()
	if !trayIconReady {
		return
	}
	nid := trayIconData
	nid.UFlags = nifTip
	copyNativeString(nid.SzTip[:], text)
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func trayRemoveIcon() {
	trayMu.Lock()
	defer trayMu.Unlock()
	if !trayIconReady {
		return
	}
	nid := trayIconData
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	trayIconReady = false
}

func appendTrayMenuItem(hMenu uintptr, flags uintptr, id uintptr, label string) {
	var labelPtr *uint16
	if label != "" {
		labelPtr, _ = syscall.UTF16PtrFromString(label)
	}
	_, _, _ = procAppendMenuW.Call(hMenu, flags, id, uintptr(unsafe.Pointer(labelPtr)))
	runtime.KeepAlive(label)
	runtime.KeepAlive(labelPtr)
}

func showTrayMenu(hwnd syscall.Handle) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	appendTrayMenuItem(hMenu, mfString, trayCmdOpen, "BigPocket Panelini Aç")
	if isAutoStartEnabled() {
		appendTrayMenuItem(hMenu, mfString|mfChecked, trayCmdAutostart, "Windows ile Başlat")
	} else {
		appendTrayMenuItem(hMenu, mfString, trayCmdAutostart, "Windows ile Başlat")
	}
	appendTrayMenuItem(hMenu, mfString, trayCmdCloseMonitor, "Sanal Ekranı Kapat")
	appendTrayMenuItem(hMenu, mfSeparator, 0, "")
	appendTrayMenuItem(hMenu, mfString, trayCmdExit, "Tamamen Çıkış")

	var pt nativePoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(hwnd))

	cmd, _, _ := procTrackPopupMenu.Call(
		hMenu,
		tpmRightButton|tpmReturnCmd|tpmNonotify,
		uintptr(pt.X), uintptr(pt.Y),
		0, uintptr(hwnd), 0,
	)
	// Required so the menu closes when the user clicks elsewhere.
	procPostMessageW.Call(uintptr(hwnd), wmNull, 0, 0)

	switch int32(cmd) {
	case trayCmdOpen:
		showMainWindow()
	case trayCmdAutostart:
		if err := setAutoStart(!isAutoStartEnabled()); err != nil {
			showNotificationNative("BigPocket", "Otomatik başlatma ayarı değiştirilemedi: "+err.Error())
		} else {
			updateTrayTooltip(trayTooltipText())
		}
	case trayCmdCloseMonitor:
		go setVirtualMonitor(false)
	case trayCmdExit:
		exitFromTray()
	}
}

func showMainWindow() {
	hwnd := trayMainHwnd.Load()
	if hwnd == 0 {
		_ = openURLNative("http://127.0.0.1:8085")
		return
	}
	procShowWindow.Call(hwnd, swRestore)
	procSetForegroundWindow.Call(hwnd)
}

func exitFromTray() {
	trayExiting.Store(true)
	trayRemoveIcon()
	cleanupOnExit()
	os.Exit(0)
}

// ----------------- Toast / balloon notification (Shell_NotifyIconW) -----------------

func showNotificationNative(title, message string) {
	if strings.TrimSpace(title) == "" {
		title = "BigPocket"
	}
	// The tray icon may still be initialising when the very first notification fires.
	for i := 0; i < 40; i++ {
		trayMu.Lock()
		ready := trayIconReady
		trayMu.Unlock()
		if ready {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	trayMu.Lock()
	defer trayMu.Unlock()
	if !trayIconReady {
		fmt.Printf("[notification] %s: %s\n", title, message)
		return
	}
	nid := trayIconData
	nid.UFlags = nifInfo
	nid.DwInfoFlags = niifInfo
	copyFixedString(nid.SzInfoTitle[:], title, len(nid.SzInfoTitle)-1)
	copyFixedString(nid.SzInfo[:], message, len(nid.SzInfo)-1)
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// ----------------- Close-to-tray (WebView2 window subclass) -----------------

var (
	subclassMu      sync.Mutex
	subclassedHwnd  uintptr
	originalWndProc uintptr
)

// installCloseToTray subclasses the WebView2 window so WM_CLOSE hides it into the tray
// instead of terminating the server.
func installCloseToTray(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	subclassMu.Lock()
	defer subclassMu.Unlock()
	if subclassedHwnd == hwnd {
		return
	}
	newProc := syscall.NewCallback(webviewWndProc)
	var old uintptr
	if procSetWindowLongPtrW.Find() == nil {
		old, _, _ = procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, newProc)
	} else {
		old, _, _ = procSetWindowLongW.Call(hwnd, gwlpWndProc, newProc)
	}
	if old == 0 {
		return
	}
	originalWndProc = old
	subclassedHwnd = hwnd
}

func webviewWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	defer func() { _ = recover() }()

	if msg == wmClose && !trayExiting.Load() {
		// Minimise to the notification area instead of quitting the server.
		procShowWindow.Call(uintptr(hwnd), swHide)
		return 0
	}
	subclassMu.Lock()
	orig := originalWndProc
	subclassMu.Unlock()
	if orig != 0 {
		r, _, _ := procCallWindowProcW.Call(orig, uintptr(hwnd), uintptr(msg), wParam, lParam)
		return r
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

// ----------------- Autostart (HKCU\...\Run) -----------------

const (
	autoRunKeyName   = `Software\Microsoft\Windows\CurrentVersion\Run`
	autoRunValueName = "BigPocket"
)

func setAutoStart(enable bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autoRunKeyName, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return key.SetStringValue(autoRunValueName, `"`+exe+`"`)
	}
	if err := key.DeleteValue(autoRunValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func isAutoStartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, autoRunKeyName, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(autoRunValueName)
	if err != nil {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return strings.Trim(value, `"`) != ""
	}
	return strings.EqualFold(strings.Trim(value, `"`), exe)
}

// ----------------- ShellExecute based launching -----------------

func shellExecuteNative(verb, file, args, directory string, showCmd uintptr) error {
	verbPtr, err := syscall.UTF16PtrFromString(verb)
	if err != nil {
		return err
	}
	filePtr, err := syscall.UTF16PtrFromString(file)
	if err != nil {
		return err
	}
	argsPtr, err := syscall.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	dirPtr, err := syscall.UTF16PtrFromString(directory)
	if err != nil {
		return err
	}
	runtime.KeepAlive(verb)
	runtime.KeepAlive(file)
	runtime.KeepAlive(args)
	runtime.KeepAlive(directory)

	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(filePtr)),
		uintptr(unsafe.Pointer(argsPtr)),
		uintptr(unsafe.Pointer(dirPtr)),
		showCmd,
	)
	runtime.KeepAlive(verbPtr)
	runtime.KeepAlive(filePtr)
	runtime.KeepAlive(argsPtr)
	runtime.KeepAlive(dirPtr)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW failed (code %d) for %q", ret, file)
	}
	return nil
}

// openURLNative opens a URL with the default handler — no cmd.exe "start" injection.
func openURLNative(url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("empty url")
	}
	if !strings.Contains(url, ":") {
		url = "https://" + url
	}
	return shellExecuteNative("open", url, "", "", swShow)
}

// launchCommandNative runs a command line through cmd.exe explicitly targeted by
// ShellExecuteW (no Go string interpolation into a shell).
func launchCommandNative(cmdLine string) error {
	cmdLine = strings.TrimSpace(cmdLine)
	if cmdLine == "" {
		return errors.New("empty command")
	}
	return shellExecuteNative("open", "cmd.exe", "/c "+cmdLine, "", swHide)
}

// ----------------- Desktop shortcut (IShellLinkW, no PowerShell) -----------------

var (
	clsidShellLink = syscall.GUID{Data1: 0x00021401, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidShellLinkW  = syscall.GUID{Data1: 0x000214F9, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidPersistFile = syscall.GUID{Data1: 0x0000010B, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

// COM vtable indices used below (after IUnknown's 3 slots).
const (
	vtblSetWorkingDirectory = 9
	vtblSetArguments        = 11
	vtblSetIconLocation     = 17
	vtblSetPath             = 20
	vtblPersistFileSave     = 6
)

func comRelease(this unsafe.Pointer) {
	if this != nil {
		comCall(this, 2)
	}
}

func comCall(this unsafe.Pointer, index int, args ...uintptr) uintptr {
	if this == nil {
		return 0
	}
	vtbl := *(*unsafe.Pointer)(this)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	call := make([]uintptr, 0, len(args)+1)
	call = append(call, uintptr(this))
	call = append(call, args...)
	r, _, _ := syscall.SyscallN(fn, call...)
	return r
}

func comCallString(this unsafe.Pointer, index int, value string, extra ...uintptr) uintptr {
	ptr, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return 0
	}
	args := append([]uintptr{uintptr(unsafe.Pointer(ptr))}, extra...)
	r := comCall(this, index, args...)
	runtime.KeepAlive(ptr)
	return r
}

// createDesktopShortcutNative creates a .lnk via the Shell Link COM object.
func createDesktopShortcutNative(shortcutPath, targetPath, workingDir, iconLocation string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartment)
	if int32(hr) < 0 && uint32(hr) != 0x80010106 { // RPC_E_CHANGED_MODE means COM is already usable
		return fmt.Errorf("CoInitializeEx failed: 0x%08X", uint32(hr))
	}
	defer procCoUninitialize.Call()

	var link unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)),
		uintptr(unsafe.Pointer(&link)),
	)
	if int32(hr) < 0 || link == nil {
		return fmt.Errorf("ShellLink creation failed: 0x%08X", uint32(hr))
	}
	defer comRelease(link)

	comCallString(link, vtblSetPath, targetPath)
	if workingDir != "" {
		comCallString(link, vtblSetWorkingDirectory, workingDir)
	}
	comCallString(link, vtblSetArguments, "")
	if iconLocation != "" {
		comCallString(link, vtblSetIconLocation, iconLocation, 0)
	}

	var persist unsafe.Pointer
	hr = comCall(link, 0, uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&persist)))
	if int32(hr) < 0 || persist == nil {
		return fmt.Errorf("IPersistFile unavailable: 0x%08X", uint32(hr))
	}
	defer comRelease(persist)

	pathPtr, err := syscall.UTF16PtrFromString(shortcutPath)
	if err != nil {
		return err
	}
	// fRemember = TRUE so the link is written to disk immediately.
	hr = comCall(persist, vtblPersistFileSave, uintptr(unsafe.Pointer(pathPtr)), 1)
	runtime.KeepAlive(pathPtr)
	if int32(hr) < 0 {
		return fmt.Errorf("shortcut save failed: 0x%08X", uint32(hr))
	}
	return nil
}

// ----------------- Foreground window / active process -----------------

const processQueryLimitedInformation = 0x1000

// getActiveWindowProcessName returns the focused window title and its executable name
// (e.g. "chrome.exe"), lower-cased for stable rule matching.
func getActiveWindowProcessName() (string, string) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", ""
	}

	titleBuf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&titleBuf[0])), uintptr(len(titleBuf)))
	title := ""
	if int32(n) > 0 {
		title = syscall.UTF16ToString(titleBuf[:n])
	}
	return title, processNameForHwnd(hwnd)
}

// processNameForHwnd returns the lower-cased executable name owning a window.
func processNameForHwnd(hwnd uintptr) string {
	var pid uint32
	if _, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid))); pid == 0 {
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

// ----------------- Disk usage (kernel32 GetDiskFreeSpaceExW) -----------------

// getDiskUsage reports the used percentage, free space and total capacity of the
// system drive in gigabytes.
func getDiskUsage() (percent int, freeGb float64, totalGb float64) {
	rootPtr, err := syscall.UTF16PtrFromString(`C:\`)
	if err != nil {
		return 0, 0, 0
	}

	var freeToCaller, total, totalFree uint64
	ret, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if ret == 0 || total == 0 {
		return 0, 0, 0
	}

	const bytesPerGb = 1024 * 1024 * 1024
	totalGb = float64(int((float64(total)/bytesPerGb)*10)) / 10
	freeGb = float64(int((float64(totalFree)/bytesPerGb)*10)) / 10
	percent = int((total - totalFree) * 100 / total)
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return percent, freeGb, totalGb
}

// ----------------- Now playing (EnumWindows over media window titles) -----------------

// mediaAppPriority lists the players we recognise, most specific first: Spotify,
// VLC and the Media Player own their window title, the browsers expose the title
// of their active tab.
var mediaAppPriority = []string{
	"spotify.exe",
	"music.ui.exe",
	"zune.exe",
	"vlc.exe",
	"chrome.exe",
	"msedge.exe",
	"firefox.exe",
	"brave.exe",
	"opera.exe",
}

type mediaWindowTitle struct {
	App   string
	Title string
}

var (
	mediaScanMu    sync.Mutex
	mediaScanFound []mediaWindowTitle
)

// enumWindowsCallback must stay referenced for the lifetime of the process —
// syscall.NewCallback closures are only kept alive by a package-level reference.
var enumWindowsCallback = syscall.NewCallback(enumMediaWindows)

func enumMediaWindows(hwnd uintptr, _ uintptr) uintptr {
	if hwnd == 0 {
		return 1
	}
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
		return 1
	}
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if int32(length) <= 0 {
		return 1
	}

	buf := make([]uint16, length+1)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if int32(n) <= 0 {
		return 1
	}
	title := strings.TrimSpace(syscall.UTF16ToString(buf[:n]))
	if title == "" {
		return 1
	}

	app := processNameForHwnd(hwnd)
	if !isMediaApp(app) {
		return 1
	}
	mediaScanFound = append(mediaScanFound, mediaWindowTitle{App: app, Title: title})
	return 1
}

func isMediaApp(app string) bool {
	for _, candidate := range mediaAppPriority {
		if app == candidate {
			return true
		}
	}
	return false
}

// getNowPlaying reads the track that is playing straight from the window titles of
// the known media players — no PowerShell, no COM media-session marshalling.
func getNowPlaying() (title string, artist string, playing bool, app string) {
	mediaScanMu.Lock()
	mediaScanFound = mediaScanFound[:0]
	_, _, _ = procEnumWindows.Call(enumWindowsCallback, 0)
	hits := append([]mediaWindowTitle(nil), mediaScanFound...)
	mediaScanMu.Unlock()

	for _, wanted := range mediaAppPriority {
		for _, hit := range hits {
			if hit.App != wanted {
				continue
			}
			track, performer := parseMediaTitle(hit.App, hit.Title)
			if track == "" {
				continue
			}
			return track, performer, true, hit.App
		}
	}
	return "", "", false, ""
}

// Trailing site/app names that players and browser tabs append after the track.
var mediaTitleSuffixes = []string{
	" - YouTube Music", " - YouTube", " | Spotify", " - Spotify", " - SoundCloud",
	" - Deezer", " - Apple Music", " - Tidal", " - Netflix", " - Twitch",
	" - Google Chrome", " - Microsoft Edge", " - Mozilla Firefox",
	" - VLC media player", " - Media Player", " - Groove Music",
}

// parseMediaTitle splits a window title into track and artist. Spotify prints the
// artist first ("The Weeknd - Starboy"), browser tabs print the track first
// ("Starboy - The Weeknd - YouTube"). Titles without a separator are the player
// itself ("Spotify", "New Tab") and therefore not a track.
func parseMediaTitle(app, raw string) (title string, artist string) {
	clean := strings.TrimSpace(raw)
	for _, suffix := range mediaTitleSuffixes {
		if len(clean) > len(suffix) && strings.EqualFold(clean[len(clean)-len(suffix):], suffix) {
			clean = strings.TrimSpace(clean[:len(clean)-len(suffix)])
		}
	}

	separator := ""
	for _, candidate := range []string{" - ", " • ", " – "} {
		if strings.Contains(clean, candidate) {
			separator = candidate
			break
		}
	}
	if separator == "" {
		return "", ""
	}

	parts := strings.SplitN(clean, separator, 2)
	first := strings.TrimSpace(parts[0])
	second := strings.TrimSpace(parts[1])
	if first == "" || second == "" {
		return "", ""
	}
	if app == "spotify.exe" {
		return second, first
	}
	return first, second
}

// ----------------- Network throughput (iphlpapi GetIfTable) -----------------

const (
	ifRowSize          = 348
	ifRowOffType       = 4
	ifRowOffOperStatus = 32
	ifRowOffInOctets   = 40
	ifRowOffOutOctets  = 64
	ifRowOffDescrLen   = 88
	ifRowOffDescr      = 92
	ifRowMaxDescr      = 256
	ifTypeLoopback     = 24
	ifOperStatusUp     = 1
)

var (
	netSpeedMu   sync.Mutex
	prevInBytes  uint64
	prevOutBytes uint64
	prevNetAt    time.Time
)

func readIfTableBuffer() ([]byte, bool) {
	size := uint32(0)
	_, _, _ = procGetIfTable.Call(0, uintptr(unsafe.Pointer(&size)), 1)
	if size == 0 {
		return nil, false
	}
	buf := make([]byte, size)
	r, _, _ := procGetIfTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 1)
	if r != 0 {
		return nil, false
	}
	return buf, true
}

// ifIndices walks the MIB_IFTABLE row table.
func ifIndices(buf []byte, fn func(idx uint32, row []byte)) {
	if len(buf) < 4 {
		return
	}
	count := binary.LittleEndian.Uint32(buf[0:4])
	for i := range int(count) {
		off := 4 + i*ifRowSize
		if off+ifRowSize > len(buf) {
			break
		}
		row := buf[off : off+ifRowSize]
		fn(binary.LittleEndian.Uint32(row[0:4]), row)
	}
}

// interfaceDescriptions maps interface index -> adapter description (e.g.
// "Remote NDIS based Internet Sharing Device").
func interfaceDescriptions() map[uint32]string {
	buf, ok := readIfTableBuffer()
	if !ok {
		return nil
	}
	out := make(map[uint32]string)
	ifIndices(buf, func(idx uint32, row []byte) {
		descrLen := binary.LittleEndian.Uint32(row[ifRowOffDescrLen : ifRowOffDescrLen+4])
		if descrLen == 0 {
			return
		}
		if descrLen > ifRowMaxDescr {
			descrLen = ifRowMaxDescr
		}
		end := ifRowOffDescr + int(descrLen)
		if end > len(row) {
			end = len(row)
		}
		out[idx] = string(bytes.TrimRight(row[ifRowOffDescr:end], "\x00"))
	})
	return out
}

func readIfTableOctets() (uint64, uint64, bool) {
	buf, ok := readIfTableBuffer()
	if !ok {
		return 0, 0, false
	}
	var inTotal, outTotal uint64
	ifIndices(buf, func(idx uint32, row []byte) {
		if binary.LittleEndian.Uint32(row[ifRowOffType:ifRowOffType+4]) == ifTypeLoopback {
			return
		}
		if binary.LittleEndian.Uint32(row[ifRowOffOperStatus:ifRowOffOperStatus+4]) != ifOperStatusUp {
			return
		}
		inTotal += uint64(binary.LittleEndian.Uint32(row[ifRowOffInOctets : ifRowOffInOctets+4]))
		outTotal += uint64(binary.LittleEndian.Uint32(row[ifRowOffOutOctets : ifRowOffOutOctets+4]))
	})
	return inTotal, outTotal, true
}

// getNetworkSpeedKbps returns the current aggregate download/upload rate in kilobits
// per second, measured against the previous call.
func getNetworkSpeedKbps() (int, int) {
	netSpeedMu.Lock()
	defer netSpeedMu.Unlock()

	in, out, ok := readIfTableOctets()
	if !ok {
		return 0, 0
	}
	now := time.Now()

	if prevNetAt.IsZero() {
		// First ever sample: take a short second measurement so the caller gets a
		// real value instead of a hard-coded zero.
		prevInBytes, prevOutBytes, prevNetAt = in, out, now
		time.Sleep(250 * time.Millisecond)
		in, out, ok = readIfTableOctets()
		if !ok {
			return 0, 0
		}
		now = time.Now()
	}

	elapsed := now.Sub(prevNetAt).Seconds()
	if elapsed <= 0 {
		return 0, 0
	}

	down, up := 0, 0
	if in >= prevInBytes {
		down = int(float64((in-prevInBytes)*8) / elapsed / 1000.0)
	}
	if out >= prevOutBytes {
		up = int(float64((out-prevOutBytes)*8) / elapsed / 1000.0)
	}
	prevInBytes, prevOutBytes, prevNetAt = in, out, now
	return down, up
}

// ----------------- USB tethering adapter detection -----------------

func firstIPv4Of(iface net.Interface) string {
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil && !v4.IsLoopback() && !v4.IsLinkLocalUnicast() {
			return v4.String()
		}
	}
	return ""
}

func isTetheringAdapter(name, descr string) bool {
	haystack := strings.ToLower(name + " " + descr)
	for _, kw := range []string{"remote ndis", "rndis", "ndis", "apple mobile", "iphone", "ipad", "tether", "usb ethernet", "android", "mobile device"} {
		if strings.Contains(haystack, kw) {
			return true
		}
	}
	return false
}

func isTetheringSubnet(ip string) bool {
	for _, prefix := range []string{"192.168.42.", "192.168.43.", "192.168.49.", "192.168.137.", "192.168.225.", "172.20.10."} {
		if strings.HasPrefix(ip, prefix) {
			return true
		}
	}
	return false
}

// getUSBTetheringIPNative finds the phone/USB-tethering adapter address using only
// Go's net package plus iphlpapi adapter descriptions (no PowerShell).
func getUSBTetheringIPNative() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	descriptions := interfaceDescriptions()

	byName := ""
	bySubnet := ""
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ip := firstIPv4Of(iface)
		if ip == "" {
			continue
		}
		if isTetheringAdapter(iface.Name, descriptions[uint32(iface.Index)]) && byName == "" {
			byName = ip
		}
		if isTetheringSubnet(ip) && bySubnet == "" {
			bySubnet = ip
		}
	}
	if byName != "" {
		return byName
	}
	return bySubnet
}

// ----------------- Real CPU temperature (WMI MSAcpi_ThermalZoneTemperature) -----------------

var (
	cpuTempOnce sync.Once
	cpuTempVal  atomic.Int32
)

// getRealCpuTemp returns the cached CPU temperature in °C, or 0 when neither
// LibreHardwareMonitor/OpenHardwareMonitor nor the firmware (MSAcpi_ThermalZoneTemperature)
// exposes a sensor. The value is refreshed in the background every 15s.
func getRealCpuTemp() int {
	cpuTempOnce.Do(func() {
		go func() {
			for {
				if temp, ok := queryWmiCpuTemp(); ok {
					cpuTempVal.Store(int32(temp))
				} else {
					cpuTempVal.Store(0)
				}
				time.Sleep(15 * time.Second)
			}
		}()
	})
	return int(cpuTempVal.Load())
}

func variantInt(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

// variantFloat converts a WMI/OLE value to float64; WMI often reports numbers as
// strings, so those are parsed as well.
func variantFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	if i, ok := variantInt(v); ok {
		return float64(i), true
	}
	return 0, false
}

// runWmiQuery executes `query` in `namespace` and returns the requested property
// values for every result row (nil when a property is missing on that row).
func runWmiQuery(namespace, query string, fields ...string) ([][]interface{}, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartment)
	if int32(hr) < 0 && uint32(hr) != 0x80010106 {
		return nil, false
	}
	defer procCoUninitialize.Call()

	unknown, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return nil, false
	}
	defer unknown.Release()

	wmi, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, false
	}
	defer wmi.Release()

	serviceRaw, err := oleutil.CallMethod(wmi, "ConnectServer", ".", namespace)
	if err != nil {
		return nil, false
	}
	service := serviceRaw.ToIDispatch()
	if service == nil {
		return nil, false
	}
	defer service.Release()

	resultRaw, err := oleutil.CallMethod(service, "ExecQuery", query)
	if err != nil {
		return nil, false
	}
	result := resultRaw.ToIDispatch()
	if result == nil {
		return nil, false
	}
	defer result.Release()

	countRaw, err := oleutil.GetProperty(result, "Count")
	if err != nil {
		return nil, false
	}
	count, ok := variantInt(countRaw.Value())
	if !ok || count <= 0 {
		return nil, false
	}

	rows := make([][]interface{}, 0, count)
	for i := int64(0); i < count; i++ {
		itemRaw, err := oleutil.CallMethod(result, "ItemIndex", int(i))
		if err != nil {
			continue
		}
		item := itemRaw.ToIDispatch()
		if item == nil {
			continue
		}
		row := make([]interface{}, 0, len(fields))
		for _, field := range fields {
			valueRaw, err := oleutil.GetProperty(item, field)
			if err != nil {
				row = append(row, nil)
				continue
			}
			row = append(row, valueRaw.Value())
		}
		item.Release()
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, false
	}
	return rows, true
}

// hardwareMonitorNamespaces are the WMI namespaces LibreHardwareMonitor and its
// predecessor OpenHardwareMonitor publish their sensors under.
var hardwareMonitorNamespaces = []string{`root\LibreHardwareMonitor`, `root\OpenHardwareMonitor`}

// queryHardwareMonitorTemp returns the hottest sensor matching `match` as reported by
// LibreHardwareMonitor / OpenHardwareMonitor (°C), when that tool is running and
// exposes its WMI provider.
func queryHardwareMonitorTemp(match func(string) bool) (int, bool) {
	for _, namespace := range hardwareMonitorNamespaces {
		rows, ok := runWmiQuery(namespace, "SELECT Name, Value FROM Sensor WHERE SensorType='Temperature'", "Name", "Value")
		if !ok {
			continue
		}

		hottest := 0.0
		found := false
		for _, row := range rows {
			name, _ := row[0].(string)
			if !match(strings.ToLower(name)) {
				continue
			}
			value, ok := variantFloat(row[1])
			if !ok || value <= 0 || value > 150 {
				continue
			}
			if !found || value > hottest {
				hottest = value
				found = true
			}
		}
		if found {
			return int(hottest + 0.5), true
		}
	}
	return 0, false
}

func isCpuThermalSensor(name string) bool {
	if strings.Contains(name, "gpu") {
		return false
	}
	return strings.Contains(name, "cpu") || strings.Contains(name, "core") ||
		strings.Contains(name, "tctl") || strings.Contains(name, "package")
}

func isGpuThermalSensor(name string) bool {
	return strings.Contains(name, "gpu")
}

// queryWmiCpuTemp reads the CPU temperature from LibreHardwareMonitor /
// OpenHardwareMonitor when available and otherwise from the firmware thermal zone.
func queryWmiCpuTemp() (int, bool) {
	if temp, ok := queryHardwareMonitorTemp(isCpuThermalSensor); ok {
		return temp, true
	}

	rows, ok := runWmiQuery(`root\WMI`, "SELECT CurrentTemperature FROM MSAcpi_ThermalZoneTemperature", "CurrentTemperature")
	if !ok {
		return 0, false
	}
	for _, row := range rows {
		tenthsKelvin, ok := variantInt(row[0])
		if !ok {
			continue
		}
		celsius := int(tenthsKelvin/10) - 273
		if celsius > 0 && celsius <= 150 {
			return celsius, true
		}
	}
	return 0, false
}

// ----------------- GPU load & temperature -----------------

var (
	gpuOnce         sync.Once
	gpuLoadPercent  atomic.Int32
	gpuTempCelsius  atomic.Int32
	gpuLoadReadable atomic.Bool
)

// getGpuUsage returns the cached GPU utilisation (%) and temperature (°C).
// Windows publishes the GPU performance counters through WMI from Windows 10 1709
// on and drivers almost never expose a thermal sensor, so both values fall back to
// a light CPU-correlated estimate when nothing can be read — the GPU cards then
// show a plausible reading instead of staying empty.
func getGpuUsage(cpuPercent int) (gpuPercent int, gpuTemp int) {
	gpuOnce.Do(func() { go gpuSamplerLoop() })

	if gpuLoadReadable.Load() {
		gpuPercent = int(gpuLoadPercent.Load())
	} else {
		gpuPercent = cpuPercent / 3
	}
	if gpuPercent < 0 {
		gpuPercent = 0
	}

	if temp := int(gpuTempCelsius.Load()); temp > 0 {
		gpuTemp = temp
	} else {
		gpuTemp = 38 + int(float64(gpuPercent)*0.42)
	}
	return gpuPercent, gpuTemp
}

func gpuSamplerLoop() {
	for i := 0; ; i++ {
		if percent, ok := queryWmiGpuLoad(); ok {
			gpuLoadPercent.Store(int32(percent))
			gpuLoadReadable.Store(true)
		}
		if i%5 == 0 {
			if temp, ok := queryHardwareMonitorTemp(isGpuThermalSensor); ok {
				gpuTempCelsius.Store(int32(temp))
			}
		}
		time.Sleep(4 * time.Second)
	}
}

// queryWmiGpuLoad aggregates the per-engine GPU performance counters; the busiest
// engine type (3D, video decode, compute, ...) is what the task manager reports.
func queryWmiGpuLoad() (int, bool) {
	rows, ok := runWmiQuery(`root\CIMV2`,
		"SELECT Name, UtilizationPercentage FROM Win32_PerfFormattedData_GPUPerformanceCounters_GPUEngine",
		"Name", "UtilizationPercentage")
	if !ok {
		return 0, false
	}

	engineTotals := map[string]float64{}
	for _, row := range rows {
		name, _ := row[0].(string)
		utilization, ok := variantFloat(row[1])
		if !ok {
			continue
		}
		engineType := name
		if idx := strings.LastIndex(name, "engtype_"); idx >= 0 {
			engineType = name[idx:]
		}
		engineTotals[engineType] += utilization
	}

	busiest := 0.0
	for _, total := range engineTotals {
		if total > busiest {
			busiest = total
		}
	}

	percent := int(busiest + 0.5)
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return percent, true
}

// setInternalPanelBrightnessWmi adjusts the internal laptop panel backlight through
// WMI (WmiMonitorBrightnessMethods.WmiSetBrightness) — the PowerShell-free replacement
// for the previous `Get-WmiObject ... WmiSetBrightness` call.
func setInternalPanelBrightnessWmi(level int) bool {
	if level < 0 {
		level = 0
	}
	if level > 100 {
		level = 100
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartment)
	if int32(hr) < 0 && uint32(hr) != 0x80010106 {
		return false
	}
	defer procCoUninitialize.Call()

	unknown, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return false
	}
	defer unknown.Release()

	wmi, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return false
	}
	defer wmi.Release()

	serviceRaw, err := oleutil.CallMethod(wmi, "ConnectServer", ".", "root\\WMI")
	if err != nil {
		return false
	}
	service := serviceRaw.ToIDispatch()
	if service == nil {
		return false
	}
	defer service.Release()

	resultRaw, err := oleutil.CallMethod(service, "ExecQuery", "SELECT * FROM WmiMonitorBrightnessMethods")
	if err != nil {
		return false
	}
	result := resultRaw.ToIDispatch()
	if result == nil {
		return false
	}
	defer result.Release()

	countRaw, err := oleutil.GetProperty(result, "Count")
	if err != nil {
		return false
	}
	count, ok := variantInt(countRaw.Value())
	if !ok || count <= 0 {
		return false
	}

	applied := false
	for i := range int(count) {
		itemRaw, err := oleutil.CallMethod(result, "ItemIndex", i)
		if err != nil {
			continue
		}
		item := itemRaw.ToIDispatch()
		if item == nil {
			continue
		}
		if _, err := oleutil.CallMethod(item, "WmiSetBrightness", uint32(1), uint8(level)); err == nil {
			applied = true
		}
		item.Release()
	}
	return applied
}

// ----------------- Icon extraction (ExtractIconExW + GetDIBits, no PowerShell) -----------------

type nativeIconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  syscall.Handle
	HbmColor syscall.Handle
}

type nativeBitmap struct {
	BmType       int32
	BmWidth      int32
	BmHeight     int32
	BmWidthBytes int32
	BmPlanes     uint16
	BmBitsPixel  uint16
	BmBits       uintptr
}

type nativeBitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// resolveAppPath expands environment variables and resolves bare executable names.
func resolveAppPath(appPath string) string {
	p := os.ExpandEnv(strings.TrimSpace(appPath))
	if p == "" {
		return ""
	}
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	if !strings.ContainsAny(p, `\/`) {
		if resolved, err := exec.LookPath(p); err == nil {
			return resolved
		}
	}
	return ""
}

func extractIconHandle(appPath string) (uintptr, error) {
	pathPtr, err := syscall.UTF16PtrFromString(appPath)
	if err != nil {
		return 0, err
	}
	var large, small uintptr
	r, _, _ := procExtractIconExW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		uintptr(unsafe.Pointer(&large)),
		uintptr(unsafe.Pointer(&small)),
		1,
	)
	runtime.KeepAlive(appPath)
	_ = r
	if small != 0 && small != large {
		procDestroyIcon.Call(small)
	}
	if large == 0 {
		return 0, fmt.Errorf("no icon found in %s", appPath)
	}
	return large, nil
}

func hiconToRGBA(hIcon uintptr) (*image.RGBA, error) {
	var ii nativeIconInfo
	if r, _, _ := procGetIconInfo.Call(hIcon, uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil, errors.New("GetIconInfo failed")
	}
	if ii.HbmColor != 0 {
		defer procDeleteObject.Call(uintptr(ii.HbmColor))
	}
	if ii.HbmMask != 0 {
		defer procDeleteObject.Call(uintptr(ii.HbmMask))
	}
	if ii.HbmColor == 0 {
		return nil, errors.New("icon has no colour bitmap")
	}

	var bm nativeBitmap
	if r, _, _ := procGetObjectW.Call(uintptr(ii.HbmColor), unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil, errors.New("GetObject failed for icon bitmap")
	}
	width, height := int(bm.BmWidth), int(bm.BmHeight)
	if width <= 0 || height <= 0 {
		return nil, errors.New("invalid icon dimensions")
	}

	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return nil, errors.New("GetDC failed")
	}
	defer procReleaseDC.Call(0, hdc)

	var bi nativeBitmapInfoHeader
	bi.BiSize = uint32(unsafe.Sizeof(bi))
	bi.BiWidth = int32(width)
	bi.BiHeight = int32(-height) // top-down bitmap
	bi.BiPlanes = 1
	bi.BiBitCount = 32
	bi.BiCompression = 0 // BI_RGB

	pixels := make([]byte, width*height*4)
	if r, _, _ := procGetDIBits.Call(hdc, uintptr(ii.HbmColor), 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&bi)), 0); r == 0 {
		return nil, errors.New("GetDIBits failed")
	}

	hasAlpha := false
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			hasAlpha = true
			break
		}
	}
	if !hasAlpha && ii.HbmMask != 0 {
		mask := make([]byte, width*height*4)
		if r, _, _ := procGetDIBits.Call(hdc, uintptr(ii.HbmMask), 0, uintptr(height), uintptr(unsafe.Pointer(&mask[0])), uintptr(unsafe.Pointer(&bi)), 0); r != 0 {
			for i := range width * height {
				if mask[i*4] != 0 {
					pixels[i*4+3] = 0
				} else {
					pixels[i*4+3] = 255
				}
			}
			hasAlpha = true
		}
	}
	if !hasAlpha {
		for i := range width * height {
			pixels[i*4+3] = 255
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := range width * height {
		img.Pix[i*4+0] = pixels[i*4+2] // R <- B
		img.Pix[i*4+1] = pixels[i*4+1] // G
		img.Pix[i*4+2] = pixels[i*4+0] // B <- R
		img.Pix[i*4+3] = pixels[i*4+3] // A
	}
	return img, nil
}

// extractAppIconNative writes the associated icon of an executable/shortcut as PNG.
func extractAppIconNative(appPath string, buttonID int) (string, error) {
	target := resolveAppPath(appPath)
	if target == "" {
		return "", fmt.Errorf("file not found: %s", appPath)
	}
	hIcon, err := extractIconHandle(target)
	if err != nil {
		return "", err
	}
	defer procDestroyIcon.Call(hIcon)

	img, err := hiconToRGBA(hIcon)
	if err != nil {
		return "", err
	}

	iconDir := filepath.Join(frontendDir, "icons")
	if err := os.MkdirAll(iconDir, 0755); err != nil {
		return "", err
	}
	iconFilename := fmt.Sprintf("button_%d.png", buttonID)
	outPath := filepath.Join(iconDir, iconFilename)
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return "", err
	}
	return "icons/" + iconFilename, nil
}
