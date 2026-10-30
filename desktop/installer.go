//go:build ignore

// BigPocket Kurulum Sihirbazı (Setup Wizard) — tamamen yerel Win32 API'leri ile.
//
// Harici UI bağımlılığı yoktur: pencere, denetimler (common controls), çizim (GDI),
// kısayol oluşturma (IShellLink), kabuk bildirimleri ve kayıt defteri işlemleri
// doğrudan Windows API'leri üzerinden yapılır (bkz. AGENTS.md §2.1 — AV dostu).
//
// Derleme:
//
//	go build -ldflags "-H windowsgui -s -w" -o BigPocket-Setup.exe installer.go
//
// Kullanım:
//
//	BigPocket-Setup.exe              → çok adımlı kurulum sihirbazı (GUI)
//	BigPocket-Setup.exe /silent      → sessiz kurulum (varsayılan ayarlar)
//	BigPocket-Setup.exe /uninstall   → kaldırma onayı + temizlik
//	uninstall.exe                    → kurulum dizinine kopyalanan kaldırıcı adı
package main

import (
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

//go:embed BigPocket.exe usbmmidd VBCABLE logo.ico frontend
var embedFS embed.FS

// ------------------------------- Kimlik & geometri -------------------------------

const (
	appName    = "BigPocket"
	appVersion = "1.5.0"
	publisher  = "benyigiteren"

	wizardClassName = "BigPocketSetupWizard"

	clientWidth  = 540
	clientHeight = 390

	headerHeight = 100
	bottomBarTop = 342
	marginX      = 32
	contentWidth = clientWidth - 2*marginX
)

// ------------------------------- Win32 sabitleri -------------------------------

const (
	wsOverlapped   = 0x00000000
	wsMinimizeBox  = 0x00020000
	wsGroup        = 0x00020000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsBorder       = 0x00800000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsClipChildren = 0x02000000

	wsExClientEdge = 0x00000200

	bsPushButton   = 0x00000000
	bsAutoCheckBox = 0x00000003
	bsAutoRadio    = 0x00000009
	bsOwnerDraw    = 0x0000000B

	esAutoHScroll = 0x00000080

	ssLeft  = 0x00000000
	ssRight = 0x00000002
	ssIcon  = 0x00000003

	pbsSmooth = 0x00000001
)

const (
	wmCreate   = 0x0001
	wmDestroy  = 0x0002
	wmPaint    = 0x000F
	wmClose    = 0x0010
	wmDrawItem = 0x002B
	wmSetFont  = 0x0030
	wmSetIcon  = 0x0080
	wmKeyDown  = 0x0100
	wmCommand  = 0x0111

	wmCtlColorEdit   = 0x0133
	wmCtlColorBtn    = 0x0135
	wmCtlColorStatic = 0x0138

	wmAppProgress  = 0x8000 + 1
	wmAppInstallGo = 0x8000 + 2
	wmAppDone      = 0x8000 + 3

	bmSetCheck = 0x00F1
	bmGetCheck = 0x00F0
	bmClick    = 0x00F5

	stmSetImage = 0x0172

	pbmSetPos      = 0x0402
	pbmSetRange32  = 0x0406
	pbmSetBarColor = 0x0409
	pbmSetBkColor  = 0x2001

	swHide         = 0
	swShowNormal   = 1
	swShow         = 5
	swpNoSize      = 0x0001
	swpNoZOrder    = 0x0004
	spiGetWorkArea = 0x0030

	bstUnchecked = 0
	bstChecked   = 1

	dtCenter     = 0x00000001
	dtVCenter    = 0x00000004
	dtSingleLine = 0x00000020
)

const (
	mbIconError    = 0x00000010
	mbIconQuestion = 0x00000020
	mbIconWarning  = 0x00000030
	mbIconInfo     = 0x00000040
	mbYesNo        = 0x00000004

	idYes = 6
)

// ------------------------------- Denetim kimlikleri -------------------------------

const (
	idLogo = 5000 + iota
	idTitle
	idSubtitle

	idRadioAll
	idRadioMe
	idNoteAll
	idNoteMe
	idScopeHint

	idFolderLabel
	idEditDir
	idBtnBrowse
	idSpaceInfo

	idCheckFirst
	idCheckSecond
	idCheckThird
	idCheckFourth
	idCheckFifth

	idStatusText
	idProgress
	idPercentText

	idDoneText
	idChkLaunch

	idBtnBack
	idBtnNext
	idBtnInstall
	idBtnFinish
)

// Bileşen onay kutularının sayısı.
const idCheckCount = idCheckFifth - idCheckFirst + 1

// Sihirbaz sayfaları (0 → 4).
const (
	pageWelcome = iota
	pageFolder
	pageComponents
	pageProgress
	pageDone
	pageCount
)

var pageTitles = [pageCount]string{
	"BigPocket Kurulum Sihirbazına Hoş Geldiniz",
	"Hedef Kurulum Klasörü",
	"Ek Seçenekler ve Bileşenler",
	"Kurulum",
	"BigPocket Kurulumu Başarıyla Tamamlandı!",
}

var pageSubtitles = [pageCount]string{
	"BigPocket'i nasıl kurmak istediğinizi seçin.",
	"BigPocket'in kurulacağı klasörü belirtin.",
	"Kurulacak bileşenleri ve kısayolları seçin.",
	"BigPocket bilgisayarınıza kuruluyor, lütfen bekleyin...",
	"Kurulum tamamlandı.",
}

// Kurulum adımları (ilerleme sayfasının durum metinleri).
var installSteps = []string{
	"Kurulum hazırlanıyor...",
	"Dosyalar kopyalanıyor...",
	"Sürücüler yapılandırılıyor...",
	"Kısayollar oluşturuluyor...",
	"Başlangıç ayarları uygulanıyor...",
	"Kayıt defteri güncelleniyor...",
	"Kurulum tamamlanıyor...",
}

const (
	stepPreparing = iota
	stepCopying
	stepDrivers
	stepShortcuts
	stepStartup
	stepRegistry
	stepFinishing
)

// ------------------------------- Win32 tipleri -------------------------------

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

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

type msgStruct struct {
	Hwnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type paintStruct struct {
	Hdc         syscall.Handle
	FErase      int32
	RcPaint     rect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type drawItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   syscall.Handle
	Hdc        syscall.Handle
	RcItem     rect
	ItemData   uintptr
}

type initCommonControlsEx struct {
	Size uint32
	ICC  uint32
}

type browseInfo struct {
	HwndOwner      syscall.Handle
	PidlRoot       uintptr
	PszDisplayName *uint16
	LpszTitle      *uint16
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	IImage         int32
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// ------------------------------- Win32 prosedürleri -------------------------------

var (
	libUser32   = syscall.NewLazyDLL("user32.dll")
	libGdi32    = syscall.NewLazyDLL("gdi32.dll")
	libShell32  = syscall.NewLazyDLL("shell32.dll")
	libKernel32 = syscall.NewLazyDLL("kernel32.dll")
	libComctl32 = syscall.NewLazyDLL("comctl32.dll")
	libOle32    = syscall.NewLazyDLL("ole32.dll")
	libDwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	pRegisterClassExW      = libUser32.NewProc("RegisterClassExW")
	pCreateWindowExW       = libUser32.NewProc("CreateWindowExW")
	pDefWindowProcW        = libUser32.NewProc("DefWindowProcW")
	pGetMessageW           = libUser32.NewProc("GetMessageW")
	pTranslateMessage      = libUser32.NewProc("TranslateMessage")
	pDispatchMessageW      = libUser32.NewProc("DispatchMessageW")
	pPostQuitMessage       = libUser32.NewProc("PostQuitMessage")
	pPostMessageW          = libUser32.NewProc("PostMessageW")
	pSendMessageW          = libUser32.NewProc("SendMessageW")
	pDestroyWindow         = libUser32.NewProc("DestroyWindow")
	pShowWindow            = libUser32.NewProc("ShowWindow")
	pSetWindowPos          = libUser32.NewProc("SetWindowPos")
	pSetWindowTextW        = libUser32.NewProc("SetWindowTextW")
	pGetWindowTextW        = libUser32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW  = libUser32.NewProc("GetWindowTextLengthW")
	pGetWindowRect         = libUser32.NewProc("GetWindowRect")
	pGetClientRect         = libUser32.NewProc("GetClientRect")
	pAdjustWindowRectEx    = libUser32.NewProc("AdjustWindowRectEx")
	pGetSystemMetrics      = libUser32.NewProc("GetSystemMetrics")
	pSystemParametersInfoW = libUser32.NewProc("SystemParametersInfoW")
	pLoadImageW            = libUser32.NewProc("LoadImageW")
	pLoadCursorW           = libUser32.NewProc("LoadCursorW")
	pMessageBoxW           = libUser32.NewProc("MessageBoxW")
	pInvalidateRect        = libUser32.NewProc("InvalidateRect")
	pGetFocus              = libUser32.NewProc("GetFocus")
	pBeginPaint            = libUser32.NewProc("BeginPaint")
	pEndPaint              = libUser32.NewProc("EndPaint")
	pFillRect              = libUser32.NewProc("FillRect")
	pDrawTextW             = libUser32.NewProc("DrawTextW")
	pSetForegroundWindow   = libUser32.NewProc("SetForegroundWindow")

	gCreateSolidBrush = libGdi32.NewProc("CreateSolidBrush")
	gCreateFontW      = libGdi32.NewProc("CreateFontW")
	gSetTextColor     = libGdi32.NewProc("SetTextColor")
	gSetBkColor       = libGdi32.NewProc("SetBkColor")
	gSetBkMode        = libGdi32.NewProc("SetBkMode")
	gSelectObject     = libGdi32.NewProc("SelectObject")
	gGetStockObject   = libGdi32.NewProc("GetStockObject")
	gRoundRect        = libGdi32.NewProc("RoundRect")

	sShellExecuteW        = libShell32.NewProc("ShellExecuteW")
	sSHChangeNotify       = libShell32.NewProc("SHChangeNotify")
	sIsUserAnAdmin        = libShell32.NewProc("IsUserAnAdmin")
	sSHBrowseForFolderW   = libShell32.NewProc("SHBrowseForFolderW")
	sSHGetPathFromIDListW = libShell32.NewProc("SHGetPathFromIDListW")

	kGetModuleHandleW    = libKernel32.NewProc("GetModuleHandleW")
	kGetDiskFreeSpaceExW = libKernel32.NewProc("GetDiskFreeSpaceExW")

	cInitCommonControlsEx = libComctl32.NewProc("InitCommonControlsEx")

	oCoInitializeEx   = libOle32.NewProc("CoInitializeEx")
	oCoUninitialize   = libOle32.NewProc("CoUninitialize")
	oCoCreateInstance = libOle32.NewProc("CoCreateInstance")
	oCoTaskMemFree    = libOle32.NewProc("CoTaskMemFree")

	dDwmSetWindowAttribute = libDwmapi.NewProc("DwmSetWindowAttribute")
)

// COM arayüz metot indeksleri (IUnknown'ın 3 slotundan sonra).
// IShellLinkW indeksleri yerel shell32 nesnesi üzerinde ampirik olarak doğrulandı
// (bkz. win32_native.go — aynı indeksler üretimde kullanılıyor).
const (
	iunknownQueryInterface = 0
	iunknownRelease        = 2

	islSetDescription      = 7
	islSetWorkingDirectory = 9
	islSetShowCmd          = 15
	islSetIconLocation     = 17
	islSetPath             = 20

	ipfSave = 6
)

var (
	clsidShellLink = guid{0x00021401, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidShellLinkW  = guid{0x000214F9, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidPersistFile = guid{0x0000010B, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

// ------------------------------- Kayıt defteri yolları -------------------------------

const runKeyPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`

func uninstallKeyPath() string {
	return `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\BigPocket`
}

// ------------------------------- Tema -------------------------------

func rgb(r, g, b uint32) uintptr { return uintptr(r | g<<8 | b<<16) }

var (
	colBody         = rgb(22, 23, 28)
	colHeader       = rgb(31, 33, 44)
	colEdit         = rgb(36, 38, 48)
	colText         = rgb(238, 240, 246)
	colMuted        = rgb(150, 154, 168)
	colAccent       = rgb(101, 111, 245)
	colAccentDim    = rgb(70, 76, 160)
	colSecondary    = rgb(48, 51, 63)
	colSecondaryDim = rgb(37, 39, 49)
	colBorder       = rgb(58, 62, 76)
	colAccentLine   = rgb(101, 111, 245)
)

var (
	brBody         uintptr
	brHeader       uintptr
	brEdit         uintptr
	brAccent       uintptr
	brAccentDim    uintptr
	brSecondary    uintptr
	brSecondaryDim uintptr
	brBorder       uintptr
	brAccentLine   uintptr

	stockNullPen uintptr

	fontTitle  uintptr
	fontBody   uintptr
	fontSmall  uintptr
	fontButton uintptr
)

func createTheme() {
	brBody = createBrush(colBody)
	brHeader = createBrush(colHeader)
	brEdit = createBrush(colEdit)
	brAccent = createBrush(colAccent)
	brAccentDim = createBrush(colAccentDim)
	brSecondary = createBrush(colSecondary)
	brSecondaryDim = createBrush(colSecondaryDim)
	brBorder = createBrush(colBorder)
	brAccentLine = createBrush(colAccentLine)

	stockNullPen, _, _ = gGetStockObject.Call(8) // NULL_PEN

	fontTitle = createFont(-22, 600, "Segoe UI Semibold")
	fontBody = createFont(-15, 400, "Segoe UI")
	fontSmall = createFont(-13, 400, "Segoe UI")
	fontButton = createFont(-15, 600, "Segoe UI Semibold")
}

func createBrush(color uintptr) uintptr {
	h, _, _ := gCreateSolidBrush.Call(color)
	return h
}

func createFont(height, weight int32, face string) uintptr {
	facePtr, _ := syscall.UTF16PtrFromString(face)
	h, _, _ := gCreateFontW.Call(
		uintptr(height), 0, 0, 0,
		uintptr(weight), 0, 0, 0,
		1,       // DEFAULT_CHARSET (Türkçe karakterler)
		0, 0, 5, // CLEARTYPE_QUALITY
		0,
		uintptr(unsafe.Pointer(facePtr)),
	)
	runtime.KeepAlive(facePtr)
	return h
}

// ------------------------------- Global durum -------------------------------

type installConfig struct {
	dir       string
	allUsers  bool
	desktop   bool
	startMenu bool
	autostart bool
	vdd       bool
	vcable    bool
	launch    bool
}

type wizardState struct {
	hwnd       uintptr
	page       int
	cfg        installConfig
	installing bool
	autoStart  bool

	errMu      sync.Mutex
	installErr string
}

var ui wizardState

var (
	hInstance uintptr
	mainHwnd  uintptr

	hwndLogo     uintptr
	hwndTitle    uintptr
	hwndSubtitle uintptr

	hwndRadioAll  uintptr
	hwndRadioMe   uintptr
	hwndNoteAll   uintptr
	hwndNoteMe    uintptr
	hwndScopeHint uintptr

	hwndFolderLabel uintptr
	hwndEditDir     uintptr
	hwndBrowse      uintptr
	hwndSpaceInfo   uintptr

	hwndChecks [idCheckCount]uintptr

	hwndStatusText uintptr
	hwndProgress   uintptr
	hwndPercent    uintptr

	hwndDoneText  uintptr
	hwndChkLaunch uintptr

	hwndBtnBack    uintptr
	hwndBtnNext    uintptr
	hwndBtnInstall uintptr
	hwndBtnFinish  uintptr
)

var (
	headerControls = map[uintptr]bool{}
	mutedControls  = map[uintptr]bool{}
)

// ------------------------------- Komut satırı -------------------------------

type cliOptions struct {
	raw map[string]string
}

func parseArgs(args []string) cliOptions {
	o := cliOptions{raw: map[string]string{}}
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		name, value, _ := strings.Cut(strings.TrimLeft(arg, "-/"), "=")
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if name == "" {
			continue
		}
		if value == "" {
			value = "1"
		}
		o.raw[name] = value
	}
	return o
}

func (o cliOptions) has(names ...string) bool {
	for _, n := range names {
		if _, ok := o.raw[n]; ok {
			return true
		}
	}
	return false
}

func (o cliOptions) value(names ...string) string {
	for _, n := range names {
		if v, ok := o.raw[n]; ok {
			return v
		}
	}
	return ""
}

func (o cliOptions) boolFlag(def bool, names ...string) bool {
	for _, n := range names {
		if v, ok := o.raw[n]; ok {
			return isTrue(v)
		}
	}
	return def
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "evet", "on", "y":
		return true
	}
	return false
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func configFromOptions(o cliOptions, wizard bool) installConfig {
	allUsers := true
	if !wizard && !isAdmin() {
		allUsers = false
	}
	if scope := o.value("scope"); scope != "" {
		allUsers = strings.EqualFold(scope, "all")
	}

	dir := cleanDir(o.value("dir", "path", "target"))
	if dir == "" {
		dir = defaultDir(allUsers)
	}

	return installConfig{
		dir:       dir,
		allUsers:  allUsers,
		desktop:   o.boolFlag(true, "desktop"),
		startMenu: o.boolFlag(true, "startmenu", "start-menu"),
		autostart: o.boolFlag(true, "autostart"),
		vdd:       o.boolFlag(wizard, "vdd"),
		vcable:    o.boolFlag(wizard, "vcable"),
		launch:    o.boolFlag(true, "launch"),
	}
}

// ------------------------------- Giriş noktası -------------------------------

func main() {
	runtime.LockOSThread()

	hInst, _, _ := kGetModuleHandleW.Call(0)
	hInstance = hInst

	opts := parseArgs(os.Args[1:])

	if opts.has("uninstall", "u") || strings.EqualFold(filepath.Base(selfPath()), "uninstall.exe") {
		runUninstaller(opts.has("silent", "s"))
		return
	}

	if opts.has("silent", "s") {
		runSilentInstall(opts)
		return
	}

	runWizard(opts)
}

func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

func runSilentInstall(o cliOptions) {
	cfg := configFromOptions(o, false)
	if err := performInstall(cfg, nil); err != nil {
		MessageBox("BigPocket Kurulum", "Kurulum başarısız oldu:\n\n"+err.Error(), mbIconError)
		return
	}
	if cfg.launch {
		launchApp(cfg.dir)
	}
}

// ------------------------------- Sihirbaz penceresi -------------------------------

func runWizard(o cliOptions) {
	createTheme()
	initCommonControls()

	ui.cfg = configFromOptions(o, true)
	ui.page = pageWelcome
	ui.autoStart = o.has("autoinstall")

	if !registerWizardClass() {
		MessageBox("BigPocket Kurulum", "Kurulum penceresi kaydedilemedi.", mbIconError)
		return
	}

	hwnd := createWizardWindow()
	if hwnd == 0 {
		MessageBox("BigPocket Kurulum", "Kurulum penceresi oluşturulamadı.", mbIconError)
		return
	}

	centerWindow(hwnd)
	applyDarkTitleBar(hwnd)
	pShowWindow.Call(hwnd, swShow)
	pSetForegroundWindow.Call(hwnd)

	if ui.autoStart {
		// Yönetici izni ile yeniden başlatıldı: doğrudan kurulum sayfasına geç.
		pPostMessageW.Call(hwnd, wmAppInstallGo, 0, 0)
	}

	messageLoop()
}

func registerWizardClass() bool {
	className, _ := syscall.UTF16PtrFromString(wizardClassName)
	cursor, _, _ := pLoadCursorW.Call(0, 32512) // IDC_ARROW

	wc := wndClassEx{
		Style:         0x0001 | 0x0002, // CS_VREDRAW | CS_HREDRAW
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     syscall.Handle(hInstance),
		HCursor:       syscall.Handle(cursor),
		HbrBackground: syscall.Handle(brBody),
		LpszClassName: className,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))

	r, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	return r != 0
}

func createWizardWindow() uintptr {
	className, _ := syscall.UTF16PtrFromString(wizardClassName)
	title, _ := syscall.UTF16PtrFromString("BigPocket Kurulum")

	style := uint32(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox | wsClipChildren)
	r := rect{0, 0, clientWidth, clientHeight}
	pAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), uintptr(style), 0, 0)
	w := r.Right - r.Left
	h := r.Bottom - r.Top

	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(style),
		0, 0, uintptr(w), uintptr(h),
		0, 0, hInstance, 0,
	)
	return hwnd
}

func centerWindow(hwnd uintptr) {
	var wr rect
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))

	var wa rect
	r, _, _ := pSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
	if r == 0 {
		cx, _, _ := pGetSystemMetrics.Call(0)
		cy, _, _ := pGetSystemMetrics.Call(1)
		wa = rect{0, 0, int32(cx), int32(cy)}
	}

	w := wr.Right - wr.Left
	h := wr.Bottom - wr.Top
	x := wa.Left + (wa.Right-wa.Left-w)/2
	y := wa.Top + (wa.Bottom-wa.Top-h)/2

	pSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder)
}

func messageLoop() {
	var msg msgStruct
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// ------------------------------- Pencere yordamı -------------------------------

func wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	defer func() { _ = recover() }()

	switch uint32(msg) {
	case wmCreate:
		mainHwnd = hwnd
		ui.hwnd = hwnd
		buildUI(hwnd)
		syncControlsFromConfig()
		showPage(ui.page)
		return 0

	case wmCommand:
		onCommand(uint32(wParam&0xFFFF), uint32((wParam>>16)&0xFFFF))
		return 0

	case wmCtlColorStatic, wmCtlColorBtn:
		return controlColor(wParam, lParam)

	case wmCtlColorEdit:
		setBkMode(wParam, 1)
		setTextColor(wParam, colText)
		setBkColor(wParam, colEdit)
		return brEdit

	case wmDrawItem:
		drawOwnerButton(lParam)
		return 1

	case wmPaint:
		paintWindow(hwnd)
		return 0

	case wmKeyDown:
		onKeyDown(wParam)
		return 0

	case wmAppProgress:
		updateProgress(int(wParam), int(lParam))
		return 0

	case wmAppInstallGo:
		if ui.installing {
			return 0
		}
		ui.installing = true
		showPage(pageProgress)
		go runInstallWork()
		return 0

	case wmAppDone:
		finishInstallRun(wParam != 0)
		return 0

	case wmClose:
		if ui.installing {
			return 0
		}
		if ui.page == pageFolder || ui.page == pageComponents {
			if !askConfirm("BigPocket Kurulum", "Kurulumdan çıkmak istediğinize emin misiniz?", mbIconQuestion) {
				return 0
			}
		}
		pDestroyWindow.Call(hwnd)
		return 0

	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

func controlColor(hdc, ctl uintptr) uintptr {
	setBkMode(hdc, 1) // TRANSPARENT

	switch {
	case headerControls[ctl]:
		setTextColor(hdc, colText)
		setBkColor(hdc, colHeader)
		return brHeader
	case mutedControls[ctl]:
		setTextColor(hdc, colMuted)
		setBkColor(hdc, colBody)
		return brBody
	default:
		setTextColor(hdc, colText)
		setBkColor(hdc, colBody)
		return brBody
	}
}

func paintWindow(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc rect
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))

	fillRect(hdc, rect{0, 0, rc.Right, headerHeight}, brHeader)
	fillRect(hdc, rect{0, headerHeight - 1, rc.Right, headerHeight}, brBorder)
	fillRect(hdc, rect{0, headerHeight, rc.Right, rc.Bottom}, brBody)
	fillRect(hdc, rect{marginX, bottomBarTop - 1, rc.Right - marginX, bottomBarTop}, brBorder)
	// Üst kenarda ince aksan şeridi.
	fillRect(hdc, rect{0, 0, rc.Right, 3}, brAccentLine)

	pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
}

func drawOwnerButton(lParam uintptr) {
	dis := (*drawItemStruct)(unsafe.Pointer(lParam))
	if dis == nil || dis.CtlType != 4 { // ODT_BUTTON
		return
	}

	hdc := uintptr(dis.Hdc)
	r := dis.RcItem
	r.Right--
	r.Bottom--

	primary := dis.CtlID == idBtnNext || dis.CtlID == idBtnInstall || dis.CtlID == idBtnFinish
	pressed := dis.ItemState&0x0001 != 0 // ODS_SELECTED
	disabled := dis.ItemState&0x0004 != 0

	switch {
	case primary && disabled:
		roundRectFill(hdc, r, brAccentDim, 8)
	case primary && pressed:
		roundRectFill(hdc, r, brAccentDim, 8)
	case primary:
		roundRectFill(hdc, r, brAccent, 8)
	default:
		fill := brSecondary
		if disabled || pressed {
			fill = brSecondaryDim
		}
		roundRectFill(hdc, r, brBorder, 8)
		roundRectFill(hdc, rect{r.Left + 1, r.Top + 1, r.Right - 1, r.Bottom - 1}, fill, 7)
	}

	setBkMode(hdc, 1)
	setTextColor(hdc, colText)
	oldFont := selectObject(hdc, fontButton)
	text := getWindowText(uintptr(dis.HwndItem))
	if text != "" {
		if tp, err := syscall.UTF16PtrFromString(text); err == nil {
			target := r
			pDrawTextW.Call(hdc,
				uintptr(unsafe.Pointer(tp)),
				uintptr(0xFFFFFFFF), // -1: null sonlandırılmış
				uintptr(unsafe.Pointer(&target)),
				dtCenter|dtVCenter|dtSingleLine,
			)
			runtime.KeepAlive(tp)
		}
	}
	selectObject(hdc, oldFont)
}

// ------------------------------- Arayüz kurulumu -------------------------------

func buildUI(hwnd uintptr) {
	// --- Başlık bandı ---
	hwndLogo = createControl("STATIC", "", wsChild|ssIcon, 0, 24, 34, 32, 32, idLogo)
	markHeader(hwndLogo)

	hwndTitle = createControl("STATIC", "", wsChild|ssLeft, 0, 68, 14, 448, 62, idTitle)
	markHeader(hwndTitle)
	setFont(hwndTitle, fontTitle)

	hwndSubtitle = createControl("STATIC", "", wsChild|ssLeft, 0, 68, 78, 448, 20, idSubtitle)
	markHeader(hwndSubtitle)
	setFont(hwndSubtitle, fontSmall)

	// --- Sayfa 1: Hoş geldiniz & kullanıcı seçimi ---
	hwndRadioAll = createControl("BUTTON", "Tüm Kullanıcılar için Kur (Önerilen)",
		wsChild|wsTabStop|wsGroup|bsAutoRadio, 0, marginX, 118, contentWidth, 22, idRadioAll)
	setFont(hwndRadioAll, fontBody)

	hwndNoteAll = createControl("STATIC", defaultDir(true)+"  —  yönetici izni gerekir",
		wsChild|ssLeft, 0, marginX+22, 142, contentWidth-22, 18, idNoteAll)
	markMuted(hwndNoteAll)
	setFont(hwndNoteAll, fontSmall)

	hwndRadioMe = createControl("BUTTON", "Yalnızca Benim İçin Kur",
		wsChild|wsTabStop|bsAutoRadio, 0, marginX, 176, contentWidth, 22, idRadioMe)
	setFont(hwndRadioMe, fontBody)

	hwndNoteMe = createControl("STATIC", `%LOCALAPPDATA%\Programs\BigPocket  —  yönetici izni gerektirmez`,
		wsChild|ssLeft, 0, marginX+22, 200, contentWidth-22, 18, idNoteMe)
	markMuted(hwndNoteMe)
	setFont(hwndNoteMe, fontSmall)

	hwndScopeHint = createControl("STATIC",
		"Kurulum kapsamını seçtikten sonra \"İleri\" düğmesine tıklayarak devam edin.",
		wsChild|ssLeft, 0, marginX, 254, contentWidth, 20, idScopeHint)
	markMuted(hwndScopeHint)
	setFont(hwndScopeHint, fontSmall)

	// --- Sayfa 2: Hedef klasör ---
	hwndFolderLabel = createControl("STATIC",
		"BigPocket aşağıdaki klasöre kurulacak:",
		wsChild|ssLeft, 0, marginX, 108, contentWidth, 20, idFolderLabel)
	markMuted(hwndFolderLabel)
	setFont(hwndFolderLabel, fontBody)

	hwndEditDir = createControl("EDIT", "",
		wsChild|wsTabStop|wsGroup|wsBorder|esAutoHScroll, wsExClientEdge,
		marginX, 134, contentWidth-124, 28, idEditDir)
	setFont(hwndEditDir, fontBody)

	hwndBrowse = createControl("BUTTON", "Gözat...",
		wsChild|wsTabStop|wsBorder|bsOwnerDraw|bsPushButton, 0,
		marginX+contentWidth-112, 134, 112, 28, idBtnBrowse)
	setFont(hwndBrowse, fontButton)

	hwndSpaceInfo = createControl("STATIC", "",
		wsChild|ssLeft, 0, marginX, 176, contentWidth, 44, idSpaceInfo)
	markMuted(hwndSpaceInfo)
	setFont(hwndSpaceInfo, fontSmall)

	// --- Sayfa 3: Bileşenler ---
	checkLabels := [idCheckCount]string{
		"Masaüstü simgesi oluştur",
		"Başlat menüsü kısayolu oluştur",
		"Windows ile birlikte otomatik başlat",
		"Sanal 2. Monitör Sürücüsünü Yükle (usbmmidd)",
		"Sanal Mikrofon Sürücüsünü Yükle (VB-Cable)",
	}
	checkIDs := [idCheckCount]int{idCheckFirst, idCheckSecond, idCheckThird, idCheckFourth, idCheckFifth}
	y := int32(112)
	for i := range idCheckCount {
		hwndChecks[i] = createControl("BUTTON", checkLabels[i],
			wsChild|wsTabStop|wsGroup|bsAutoCheckBox, 0, marginX, y, contentWidth, 22, checkIDs[i])
		setFont(hwndChecks[i], fontBody)
		y += 30
	}

	// --- Sayfa 4: Kurulum & ilerleme ---
	hwndStatusText = createControl("STATIC", installSteps[stepPreparing],
		wsChild|ssLeft, 0, marginX, 148, contentWidth, 20, idStatusText)
	setFont(hwndStatusText, fontBody)

	hwndProgress = createControl("msctls_progress32", "",
		wsChild|pbsSmooth, 0, marginX, 182, contentWidth, 18, idProgress)

	hwndPercent = createControl("STATIC", "%0 tamamlandı",
		wsChild|ssRight, 0, marginX, 206, contentWidth, 20, idPercentText)
	markMuted(hwndPercent)
	setFont(hwndPercent, fontSmall)

	// --- Sayfa 5: Kurulum tamamlandı ---
	hwndDoneText = createControl("STATIC", "",
		wsChild|ssLeft, 0, marginX, 112, contentWidth, 76, idDoneText)
	setFont(hwndDoneText, fontBody)

	hwndChkLaunch = createControl("BUTTON", "BigPocket'i Şimdi Başlat",
		wsChild|wsTabStop|wsGroup|bsAutoCheckBox, 0, marginX, 200, contentWidth, 22, idChkLaunch)
	setFont(hwndChkLaunch, fontBody)

	// --- Alt düğme çubuğu ---
	hwndBtnBack = createControl("BUTTON", "< Geri",
		wsChild|wsTabStop|wsGroup|bsOwnerDraw|bsPushButton, 0,
		clientWidth-16-142-8-100, 350, 100, 32, idBtnBack)
	setFont(hwndBtnBack, fontButton)

	hwndBtnNext = createControl("BUTTON", "İleri >",
		wsChild|wsTabStop|bsOwnerDraw|bsPushButton, 0,
		clientWidth-16-142, 350, 142, 32, idBtnNext)
	setFont(hwndBtnNext, fontButton)

	hwndBtnInstall = createControl("BUTTON", "Kur >",
		wsChild|wsTabStop|bsOwnerDraw|bsPushButton, 0,
		clientWidth-16-142, 350, 142, 32, idBtnInstall)
	setFont(hwndBtnInstall, fontButton)

	hwndBtnFinish = createControl("BUTTON", "Bitir",
		wsChild|wsTabStop|bsOwnerDraw|bsPushButton, 0,
		clientWidth-16-142, 350, 142, 32, idBtnFinish)
	setFont(hwndBtnFinish, fontButton)

	// Başlık bandı her sayfada görünür kalır.
	show(hwndLogo, true)
	show(hwndTitle, true)
	show(hwndSubtitle, true)

	loadLogo(hwnd)
}

func createControl(class, text string, style uint32, exStyle uint32, x, y, w, h int32, id int) uintptr {
	cls, _ := syscall.UTF16PtrFromString(class)
	txt, _ := syscall.UTF16PtrFromString(text)
	hwnd, _, _ := pCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(txt)),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		mainHwnd, uintptr(id), hInstance, 0,
	)
	return hwnd
}

func markHeader(hwnd uintptr) {
	if hwnd != 0 {
		headerControls[hwnd] = true
	}
}

func markMuted(hwnd uintptr) {
	if hwnd != 0 {
		mutedControls[hwnd] = true
	}
}

func loadLogo(hwnd uintptr) {
	path, err := extractLogoFile()
	if err != nil {
		return
	}
	defer os.Remove(path)

	pathPtr, _ := syscall.UTF16PtrFromString(path)
	const imageIcon = 1
	const lrLoadFromFile = 0x0010

	big, _, _ := pLoadImageW.Call(0, uintptr(unsafe.Pointer(pathPtr)), imageIcon, 32, 32, lrLoadFromFile)
	small, _, _ := pLoadImageW.Call(0, uintptr(unsafe.Pointer(pathPtr)), imageIcon, 16, 16, lrLoadFromFile)
	runtime.KeepAlive(pathPtr)

	if big != 0 {
		sendMessage(hwnd, wmSetIcon, 1, big) // ICON_BIG
		if hwndLogo != 0 {
			sendMessage(hwndLogo, stmSetImage, 1, big) // IMAGE_ICON
		}
	}
	if small != 0 {
		sendMessage(hwnd, wmSetIcon, 0, small) // ICON_SMALL
	}
}

func extractLogoFile() (string, error) {
	data, err := embedFS.ReadFile("logo.ico")
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "bigpocket_logo_*.ico")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func applyDarkTitleBar(hwnd uintptr) {
	if err := dDwmSetWindowAttribute.Find(); err != nil {
		return
	}
	value := int32(1)
	r, _, _ := dDwmSetWindowAttribute.Call(hwnd, 20, // DWMWA_USE_IMMERSIVE_DARK_MODE
		uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
	if r != 0 {
		dDwmSetWindowAttribute.Call(hwnd, 19, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
	}
}

// ------------------------------- Denetim durumu -------------------------------

func syncControlsFromConfig() {
	sendMessage(hwndRadioAll, bmSetCheck, checkValue(ui.cfg.allUsers), 0)
	sendMessage(hwndRadioMe, bmSetCheck, checkValue(!ui.cfg.allUsers), 0)

	if ui.cfg.dir == "" {
		ui.cfg.dir = defaultDir(ui.cfg.allUsers)
	}
	setWindowText(hwndEditDir, ui.cfg.dir)

	values := [idCheckCount]bool{
		ui.cfg.desktop,
		ui.cfg.startMenu,
		ui.cfg.autostart,
		ui.cfg.vdd,
		ui.cfg.vcable,
	}
	for i := range idCheckCount {
		sendMessage(hwndChecks[i], bmSetCheck, checkValue(values[i]), 0)
	}

	sendMessage(hwndChkLaunch, bmSetCheck, bstChecked, 0)
	sendMessage(hwndProgress, pbmSetRange32, 0, 100)
	sendMessage(hwndProgress, pbmSetBkColor, 0, colEdit)
	sendMessage(hwndProgress, pbmSetBarColor, 0, colAccent)
	updateProgress(stepPreparing, 0)
}

func checkValue(checked bool) uintptr {
	if checked {
		return bstChecked
	}
	return bstUnchecked
}

func isChecked(hwnd uintptr) bool {
	return sendMessage(hwnd, bmGetCheck, 0, 0) == bstChecked
}

func showPage(page int) {
	ui.page = page

	if page >= 0 && page < pageCount {
		setWindowText(hwndTitle, pageTitles[page])
		setWindowText(hwndSubtitle, pageSubtitles[page])
	}

	// İçerik denetimleri
	show(hwndRadioAll, page == pageWelcome)
	show(hwndRadioMe, page == pageWelcome)
	show(hwndNoteAll, page == pageWelcome)
	show(hwndNoteMe, page == pageWelcome)
	show(hwndScopeHint, page == pageWelcome)

	show(hwndFolderLabel, page == pageFolder)
	show(hwndEditDir, page == pageFolder)
	show(hwndBrowse, page == pageFolder)
	show(hwndSpaceInfo, page == pageFolder)

	for i := range idCheckCount {
		show(hwndChecks[i], page == pageComponents)
	}

	show(hwndStatusText, page == pageProgress)
	show(hwndProgress, page == pageProgress)
	show(hwndPercent, page == pageProgress)

	show(hwndDoneText, page == pageDone)
	show(hwndChkLaunch, page == pageDone)

	// Alt düğmeler
	show(hwndBtnBack, page == pageFolder || page == pageComponents)
	show(hwndBtnNext, page == pageWelcome || page == pageFolder)
	show(hwndBtnInstall, page == pageComponents)
	show(hwndBtnFinish, page == pageDone)

	if page == pageFolder {
		refreshSpaceInfo()
	}
	if page == pageProgress {
		updateProgress(stepPreparing, 0)
	}
	if page == pageDone {
		setWindowText(hwndDoneText, finishSummary(ui.cfg))
	}

	invalidate(ui.hwnd)
}

func invalidate(hwnd uintptr) {
	if hwnd != 0 {
		pInvalidateRect.Call(hwnd, 0, 1)
	}
}

// ------------------------------- Sihirbaz akışı -------------------------------

func onCommand(id uint32, code uint32) {
	if code != 0 { // yalnızca BN_CLICKED
		return
	}

	switch int(id) {
	case idBtnBack:
		goBack()
	case idBtnNext:
		goNext()
	case idBtnInstall:
		startInstall()
	case idBtnFinish:
		finishWizard()
	case idBtnBrowse:
		browseForFolder()
	case idRadioAll:
		ui.cfg.allUsers = true
	case idRadioMe:
		ui.cfg.allUsers = false
	}
}

func goBack() {
	switch ui.page {
	case pageFolder:
		showPage(pageWelcome)
	case pageComponents:
		showPage(pageFolder)
	}
}

func goNext() {
	switch ui.page {
	case pageWelcome:
		ui.cfg.allUsers = isChecked(hwndRadioAll)
		current := cleanDir(getWindowText(hwndEditDir))
		if current == "" || current == defaultDir(true) || current == defaultDir(false) {
			ui.cfg.dir = defaultDir(ui.cfg.allUsers)
			setWindowText(hwndEditDir, ui.cfg.dir)
		}
		showPage(pageFolder)

	case pageFolder:
		target := cleanDir(getWindowText(hwndEditDir))
		if target == "" {
			MessageBox("BigPocket Kurulum", "Lütfen bir hedef klasör seçin.", mbIconWarning)
			return
		}
		ui.cfg.dir = target
		setWindowText(hwndEditDir, target)
		if free := freeSpaceFor(target); free > 0 && free < requiredBytes()+16*1024*1024 {
			msg := fmt.Sprintf("Seçilen sürücüde yeterli boş alan olmayabilir.\n\n"+
				"Gerekli alan: %s\nKullanılabilir alan: %s\n\nYine de devam edilsin mi?",
				formatBytes(requiredBytes()), formatBytes(free))
			if !askConfirm("Yetersiz Disk Alanı", msg, mbIconWarning) {
				return
			}
		}
		showPage(pageComponents)

	case pageComponents:
		startInstall()
	}
}

func browseForFolder() {
	start := cleanDir(getWindowText(hwndEditDir))
	chosen := chooseFolder(ui.hwnd, "BigPocket kurulum klasörünü seçin", start)
	if chosen == "" {
		return
	}
	ui.cfg.dir = cleanDir(chosen)
	setWindowText(hwndEditDir, ui.cfg.dir)
	refreshSpaceInfo()
}

func refreshSpaceInfo() {
	dir := cleanDir(getWindowText(hwndEditDir))
	if dir == "" {
		dir = defaultDir(isChecked(hwndRadioAll))
	}
	free := freeSpaceFor(dir)
	text := fmt.Sprintf("Gerekli alan: %s     •     Kullanılabilir disk alanı: %s",
		formatBytes(requiredBytes()), formatBytes(free))
	if free == 0 {
		text = fmt.Sprintf("Gerekli alan: %s     •     Kullanılabilir disk alanı: bilinmiyor",
			formatBytes(requiredBytes()))
	}
	if free > 0 && free < requiredBytes() {
		text += "\nUyarı: Seçilen sürücüde yeterli boş alan yok!"
	}
	setWindowText(hwndSpaceInfo, text)
}

func readComponentSelection() {
	ui.cfg.desktop = isChecked(hwndChecks[0])
	ui.cfg.startMenu = isChecked(hwndChecks[1])
	ui.cfg.autostart = isChecked(hwndChecks[2])
	ui.cfg.vdd = isChecked(hwndChecks[3])
	ui.cfg.vcable = isChecked(hwndChecks[4])
}

func startInstall() {
	readComponentSelection()
	ui.cfg.allUsers = isChecked(hwndRadioAll)

	target := cleanDir(getWindowText(hwndEditDir))
	if target == "" {
		target = defaultDir(ui.cfg.allUsers)
	}
	ui.cfg.dir = target

	if needsElevation(target) {
		if err := shellExecuteElevated(buildAutoArgs(ui.cfg)); err != nil {
			MessageBox("Yönetici İzni Gerekli",
				"Seçilen klasöre kurulum yapabilmek için yönetici izni gerekiyor.\n\n"+
					"Alternatif: \"Yalnızca Benim İçin Kur\" seçeneği ile yönetici izni olmadan kurabilirsiniz.\n\n"+
					err.Error(), mbIconWarning)
			return
		}
		pDestroyWindow.Call(ui.hwnd)
		return
	}

	if ui.installing {
		return
	}
	ui.installing = true
	showPage(pageProgress)
	go runInstallWork()
}

func runInstallWork() {
	err := performInstall(ui.cfg, func(step, pct int) {
		if ui.hwnd != 0 {
			pPostMessageW.Call(ui.hwnd, wmAppProgress, uintptr(step), uintptr(pct))
		}
	})

	ui.errMu.Lock()
	if err != nil {
		ui.installErr = err.Error()
	} else {
		ui.installErr = ""
	}
	ui.errMu.Unlock()

	if ui.hwnd != 0 {
		pPostMessageW.Call(ui.hwnd, wmAppDone, boolUintptr(err != nil), 0)
	}
}

func finishInstallRun(failed bool) {
	ui.installing = false

	if failed {
		ui.errMu.Lock()
		msg := ui.installErr
		ui.errMu.Unlock()
		if msg == "" {
			msg = "Bilinmeyen bir hata oluştu."
		}
		MessageBox("BigPocket Kurulum", "Kurulum tamamlanamadı:\n\n"+msg, mbIconError)
		showPage(pageComponents)
		return
	}

	showPage(pageDone)
}

func finishWizard() {
	if isChecked(hwndChkLaunch) {
		launchApp(ui.cfg.dir)
	}
	tellShellToRefresh()
	if ui.hwnd != 0 {
		pDestroyWindow.Call(ui.hwnd)
	}
}

func onKeyDown(vk uintptr) {
	switch vk {
	case 0x1B: // VK_ESCAPE
		pSendMessageW.Call(ui.hwnd, wmClose, 0, 0)
	case 0x0D: // VK_RETURN
		primaryAction()
	case 0x20: // VK_SPACE
		focus, _, _ := pGetFocus.Call()
		if isWizardButton(focus) {
			sendMessage(focus, bmClick, 0, 0)
		}
	}
}

func primaryAction() {
	switch ui.page {
	case pageWelcome, pageFolder:
		goNext()
	case pageComponents:
		startInstall()
	case pageDone:
		finishWizard()
	}
}

func isWizardButton(hwnd uintptr) bool {
	switch hwnd {
	case hwndBtnBack, hwndBtnNext, hwndBtnInstall, hwndBtnFinish, hwndBrowse:
		return hwnd != 0
	}
	return false
}

func finishSummary(cfg installConfig) string {
	parts := []string{fmt.Sprintf("BigPocket, %s klasörüne başarıyla kuruldu.", cfg.dir)}
	switch {
	case cfg.desktop && cfg.startMenu:
		parts = append(parts, "Masaüstü ve Başlat menüsü kısayolları oluşturuldu.")
	case cfg.desktop:
		parts = append(parts, "Masaüstü kısayolu oluşturuldu.")
	case cfg.startMenu:
		parts = append(parts, "Başlat menüsü kısayolu oluşturuldu.")
	}
	if cfg.autostart {
		parts = append(parts, "BigPocket, Windows ile birlikte otomatik olarak başlatılacak.")
	}
	if cfg.vdd {
		parts = append(parts, "Sanal 2. monitör sürücüsü (usbmmidd) yüklendi.")
	}
	if cfg.vcable {
		parts = append(parts, "Sanal mikrofon sürücüsü (VB-Cable) yüklendi.")
	}
	return strings.Join(parts, " ")
}

func updateProgress(step, pct int) {
	if step >= 0 && step < len(installSteps) {
		setWindowText(hwndStatusText, installSteps[step])
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	sendMessage(hwndProgress, pbmSetPos, uintptr(pct), 0)
	setWindowText(hwndPercent, fmt.Sprintf("%%%d tamamlandı", pct))
}

// ------------------------------- Kurulum motoru -------------------------------

func performInstall(cfg installConfig, report func(step, pct int)) error {
	notify := func(step, pct int) {
		if report != nil {
			report(step, pct)
		}
	}

	notify(stepPreparing, 3)
	killRunningApp()

	if err := os.MkdirAll(cfg.dir, 0755); err != nil {
		return fmt.Errorf("kurulum klasörü oluşturulamadı (%s): %w", cfg.dir, err)
	}

	notify(stepCopying, 6)
	total := requiredBytes()
	if total == 0 {
		total = 1
	}
	var copied uint64
	err := extractEmbedTree(embedFS, ".", cfg.dir, func(n int64) {
		copied += uint64(n)
		pct := 6 + int(float64(copied)/float64(total)*49)
		if pct > 55 {
			pct = 55
		}
		notify(stepCopying, pct)
	})
	if err != nil {
		return fmt.Errorf("uygulama dosyaları kopyalanamadı: %w", err)
	}

	// Kaldırıcıyı (bu kurulumun kendisi) kurulum klasörüne kopyala.
	if self := selfPath(); self != "" {
		destUninstaller := filepath.Join(cfg.dir, "uninstall.exe")
		os.Remove(destUninstaller)
		if err := copyFile(self, destUninstaller); err != nil {
			return fmt.Errorf("kaldırıcı kopyalanamadı: %w", err)
		}
	}

	notify(stepDrivers, 58)
	if cfg.vdd {
		installVirtualDisplay(cfg.dir)
	}
	notify(stepDrivers, 65)
	if cfg.vcable {
		installVirtualCable(cfg.dir)
	}
	notify(stepDrivers, 70)

	notify(stepShortcuts, 72)
	if err := createAppShortcuts(cfg); err != nil {
		return fmt.Errorf("kısayollar oluşturulamadı: %w", err)
	}
	notify(stepShortcuts, 78)

	notify(stepStartup, 80)
	applyAutostart(cfg)
	notify(stepStartup, 84)

	notify(stepRegistry, 86)
	if err := registerApplication(cfg); err != nil {
		return fmt.Errorf("kayıt defteri güncellenemedi: %w", err)
	}
	notify(stepRegistry, 92)

	notify(stepFinishing, 96)
	tellShellToRefresh()
	notify(stepFinishing, 100)

	return nil
}

func extractEmbedTree(fs embed.FS, srcDir, destDir string, onBytes func(int64)) error {
	entries, err := fs.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := entry.Name()
		if srcDir != "." {
			srcPath = srcDir + "/" + entry.Name()
		}

		// Kurulum kaynağı ve eski kaldırıcı kopyalanmaz.
		switch strings.ToLower(entry.Name()) {
		case "installer.go", "uninstall.exe", "bigpocket-setup.exe":
			continue
		}

		destPath := filepath.Join(destDir, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
			if err := extractEmbedTree(fs, srcPath, destPath, onBytes); err != nil {
				return err
			}
			continue
		}

		data, err := fs.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(destPath, data, 0755); err != nil {
			return err
		}
		if onBytes != nil {
			onBytes(int64(len(data)))
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

var (
	requiredOnce sync.Once
	requiredSize uint64
)

func requiredBytes() uint64 {
	requiredOnce.Do(func() {
		var walk func(dir string)
		walk = func(dir string) {
			entries, err := embedFS.ReadDir(dir)
			if err != nil {
				return
			}
			for _, entry := range entries {
				path := entry.Name()
				if dir != "." {
					path = dir + "/" + entry.Name()
				}
				if entry.IsDir() {
					walk(path)
					continue
				}
				if info, err := entry.Info(); err == nil {
					requiredSize += uint64(info.Size())
				}
			}
		}
		walk(".")
	})
	return requiredSize
}

// ------------------------------- Sürücü bileşenleri -------------------------------

func installVirtualDisplay(dir string) {
	sub := filepath.Join(dir, "usbmmidd")
	installer := deviceInstallerPath(sub)
	if installer == "" {
		return
	}
	runProcess(sub, 2*time.Minute, installer, "install", filepath.Join(sub, "usbmmidd.inf"), "usbmmidd")
	runProcess(sub, time.Minute, installer, "enableidd", "1")
}

func installVirtualCable(dir string) {
	sub := filepath.Join(dir, "VBCABLE")
	installer := virtualCableSetupPath(sub)
	if installer == "" {
		return
	}
	runProcess(sub, 3*time.Minute, installer, "-i", "-h")
}

func deviceInstallerPath(dir string) string {
	names := []string{"deviceinstaller64.exe", "deviceinstaller.exe"}
	if runtime.GOARCH == "386" {
		names[0], names[1] = names[1], names[0]
	}
	for _, name := range names {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func virtualCableSetupPath(dir string) string {
	names := []string{"VBCABLE_Setup_x64.exe", "VBCABLE_Setup.exe"}
	if runtime.GOARCH == "386" {
		names[0], names[1] = names[1], names[0]
	}
	for _, name := range names {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func runProcess(dir string, timeout time.Duration, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		return
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

// ------------------------------- Kısayollar -------------------------------

func createAppShortcuts(cfg installConfig) error {
	target := filepath.Join(cfg.dir, "BigPocket.exe")
	// Simge konumu: uygulama .exe'si (indeks 0 — SetIconLocation ayrı indeks alır).
	icon := target

	if cfg.desktop {
		for _, dir := range desktopDirs(cfg.allUsers) {
			if err := os.MkdirAll(dir, 0755); err != nil {
				continue
			}
			link := filepath.Join(dir, "BigPocket.lnk")
			os.Remove(link)
			if err := createShortcut(target, link, cfg.dir, icon, appName); err != nil {
				if cfg.allUsers {
					// Tüm kullanıcılar için masaüstü klasörüne erişemiyorsak kişisel klasöre düş.
					if personal := desktopDirs(false); len(personal) > 0 {
						_ = createShortcut(target, filepath.Join(personal[0], "BigPocket.lnk"), cfg.dir, icon, appName)
					}
				}
			}
		}
	} else {
		removeShortcutsFrom(desktopDirs(!cfg.allUsers))
	}

	if cfg.startMenu {
		for _, dir := range startMenuDirs(cfg.allUsers) {
			if err := os.MkdirAll(dir, 0755); err != nil {
				continue
			}
			link := filepath.Join(dir, "BigPocket.lnk")
			os.Remove(link)
			_ = createShortcut(target, link, cfg.dir, icon, appName)
		}
	} else {
		removeShortcutsFrom(startMenuDirs(!cfg.allUsers))
	}

	return nil
}

func desktopDirs(allUsers bool) []string {
	if allUsers {
		if public := strings.TrimSpace(os.Getenv("PUBLIC")); public != "" {
			return []string{filepath.Join(public, "Desktop")}
		}
		return nil
	}

	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(home, "Desktop")}
	if oneDrive := strings.TrimSpace(os.Getenv("OneDrive")); oneDrive != "" {
		dirs = append(dirs, filepath.Join(oneDrive, "Desktop"))
	}
	return dirs
}

func startMenuDirs(allUsers bool) []string {
	base := ""
	if allUsers {
		base = os.Getenv("ProgramData")
	} else {
		base = os.Getenv("APPDATA")
	}
	if strings.TrimSpace(base) == "" {
		return nil
	}
	return []string{filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs")}
}

func removeShortcutsFrom(dirs []string) {
	for _, dir := range dirs {
		os.Remove(filepath.Join(dir, "BigPocket.lnk"))
	}
}

// createShortcut, IShellLinkW + IPersistFile kullanarak .lnk dosyası üretir (PowerShell yok).
// iconPath simge dosyasının yoludur (indeks her zaman 0 — ".exe,0" biçimi kullanılmaz).
func createShortcut(target, linkPath, workDir, iconPath, description string) error {
	// COM dairesi (apartment) iş parçacığına bağlıdır: çağrılar boyunca aynı OS
	// iş parçacığında kalınmalı (kurulum bu fonksiyonu worker goroutine'den çağırır).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	initialized := comInitialize()
	if initialized {
		defer oCoUninitialize.Call()
	}

	var link uintptr
	if hr := coCreateInstance(&clsidShellLink, &iidShellLinkW, &link); hr != 0 || link == 0 {
		return fmt.Errorf("IShellLink oluşturulamadı (0x%08X)", hr)
	}
	defer comRelease(link)

	targetPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	comCall(link, islSetPath, uintptr(unsafe.Pointer(targetPtr)))

	var workDirPtr, iconPtr, descPtr, linkPtr *uint16
	if workDir != "" {
		workDirPtr, _ = syscall.UTF16PtrFromString(workDir)
		comCall(link, islSetWorkingDirectory, uintptr(unsafe.Pointer(workDirPtr)))
	}
	if iconPath != "" {
		iconPtr, _ = syscall.UTF16PtrFromString(iconPath)
		comCall(link, islSetIconLocation, uintptr(unsafe.Pointer(iconPtr)), 0)
	}
	if description != "" {
		descPtr, _ = syscall.UTF16PtrFromString(description)
		comCall(link, islSetDescription, uintptr(unsafe.Pointer(descPtr)))
	}
	comCall(link, islSetShowCmd, swShowNormal)

	var file uintptr
	riid := iidPersistFile
	if hr := comCall(link, iunknownQueryInterface,
		uintptr(unsafe.Pointer(&riid)), uintptr(unsafe.Pointer(&file))); hr != 0 || file == 0 {
		return fmt.Errorf("IPersistFile alınamadı (0x%08X)", hr)
	}
	defer comRelease(file)

	linkPtr, err = syscall.UTF16PtrFromString(linkPath)
	if err != nil {
		return err
	}
	hr := comCall(file, ipfSave, uintptr(unsafe.Pointer(linkPtr)), 1)

	runtime.KeepAlive(targetPtr)
	runtime.KeepAlive(workDirPtr)
	runtime.KeepAlive(iconPtr)
	runtime.KeepAlive(descPtr)
	runtime.KeepAlive(linkPtr)

	if hr != 0 {
		return fmt.Errorf("kısayol kaydedilemedi (0x%08X)", hr)
	}
	return nil
}

func comInitialize() bool {
	hr, _, _ := oCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	return hr == 0 || hr == 1              // S_OK | S_FALSE
}

func coCreateInstance(clsid, iid *guid, out *uintptr) uint32 {
	r, _, _ := oCoCreateInstance.Call(
		uintptr(unsafe.Pointer(clsid)),
		0,
		1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(out)),
	)
	return uint32(r)
}

func comRelease(this uintptr) {
	if this != 0 {
		comCall(this, iunknownRelease)
	}
}

func comCall(this uintptr, method int, args ...uintptr) uintptr {
	if this == 0 {
		return 0
	}
	vtbl := *(*uintptr)(unsafe.Pointer(this))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(method)*unsafe.Sizeof(uintptr(0))))

	call := make([]uintptr, 0, len(args)+1)
	call = append(call, this)
	call = append(call, args...)

	r, _, _ := syscall.SyscallN(fn, call...)
	return r
}

func tellShellToRefresh() {
	const shcneAssocChanged = 0x08000000
	const shcnfIDList = 0x0000
	sSHChangeNotify.Call(shcneAssocChanged, shcnfIDList, 0, 0)
}

// ------------------------------- Kayıt defteri & başlangıç -------------------------------

func applyAutostart(cfg installConfig) {
	exe := filepath.Join(cfg.dir, "BigPocket.exe")

	if !cfg.autostart {
		removeAutostart()
		return
	}

	value := `"` + exe + `"`
	if cfg.allUsers {
		_ = setStringValue(registry.LOCAL_MACHINE, runKeyPath, appName, value)
		deleteStringValue(registry.CURRENT_USER, runKeyPath, appName)
	} else {
		_ = setStringValue(registry.CURRENT_USER, runKeyPath, appName, value)
		deleteStringValue(registry.LOCAL_MACHINE, runKeyPath, appName)
	}
}

func removeAutostart() {
	deleteStringValue(registry.CURRENT_USER, runKeyPath, appName)
	deleteStringValue(registry.LOCAL_MACHINE, runKeyPath, appName)
}

func setStringValue(root registry.Key, path, name, value string) error {
	k, _, err := registry.CreateKey(root, path, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func deleteStringValue(root registry.Key, path, name string) {
	k, err := registry.OpenKey(root, path, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.DeleteValue(name)
}

func registerApplication(cfg installConfig) error {
	root := registry.CURRENT_USER
	if cfg.allUsers {
		root = registry.LOCAL_MACHINE
	}

	k, _, err := registry.CreateKey(root, uninstallKeyPath(), registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()

	exe := filepath.Join(cfg.dir, "BigPocket.exe")
	uninstaller := filepath.Join(cfg.dir, "uninstall.exe")

	values := map[string]string{
		"DisplayName":          appName,
		"DisplayVersion":       appVersion,
		"Publisher":            publisher,
		"InstallLocation":      cfg.dir,
		"UninstallString":      `"` + uninstaller + `"`,
		"QuietUninstallString": `"` + uninstaller + `" /silent`,
		"DisplayIcon":          exe + ",0",
		"InstallSource":        selfPath(),
	}
	for name, value := range values {
		if err := k.SetStringValue(name, value); err != nil {
			return err
		}
	}

	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	_ = k.SetDWordValue("EstimatedSize", uint32(requiredBytes()/1024))

	// Diğer kapsamdaki eski kayıtları temizle.
	if cfg.allUsers {
		registry.DeleteKey(registry.CURRENT_USER, uninstallKeyPath())
	} else {
		registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKeyPath())
	}

	return nil
}

func readInstallLocation() (string, bool, bool) {
	candidates := []struct {
		root     registry.Key
		allUsers bool
	}{
		{registry.LOCAL_MACHINE, true},
		{registry.CURRENT_USER, false},
	}

	for _, c := range candidates {
		k, err := registry.OpenKey(c.root, uninstallKeyPath(), registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		dir, _, err := k.GetStringValue("InstallLocation")
		k.Close()
		if err == nil && strings.TrimSpace(dir) != "" {
			return cleanDir(dir), c.allUsers, true
		}
	}
	return "", false, false
}

func removeUninstallEntries() {
	registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKeyPath())
	registry.DeleteKey(registry.CURRENT_USER, uninstallKeyPath())
}

// ------------------------------- Kaldırıcı -------------------------------

func runUninstaller(silent bool) {
	dir, allUsers, found := readInstallLocation()
	if !found {
		if self := selfPath(); self != "" {
			dir = filepath.Dir(self)
		}
		allUsers = isUnderProgramFiles(dir)
	}
	if dir == "" {
		dir = defaultDir(allUsers)
	}

	if needsElevation(dir) {
		if err := shellExecuteElevated("--uninstall --elevated"); err != nil {
			MessageBox("BigPocket Kaldırma",
				"Kaldırma işlemi için yönetici izni gerekiyor.\n\n"+err.Error(), mbIconError)
		}
		return
	}

	if !silent {
		if !askConfirm("BigPocket Kaldırma",
			"BigPocket programını ve tüm bileşenlerini kaldırmak istediğinize emin misiniz?", mbIconQuestion) {
			return
		}
	}

	// Çalışan uygulamayı kapat (dosya kilitlerini serbest bırak).
	execCommandHidden("taskkill", "/f", "/t", "/im", "BigPocket.exe").Run()
	time.Sleep(700 * time.Millisecond)

	removeDriverComponents(dir)
	removeShortcuts()
	removeAutostart()
	removeUninstallEntries()
	tellShellToRefresh()

	scheduleDirectoryRemoval(dir)

	if !silent {
		MessageBox("BigPocket Kaldırma", "BigPocket başarıyla kaldırıldı.", mbIconInfo)
	}
}

func removeDriverComponents(dir string) {
	if sub := filepath.Join(dir, "usbmmidd"); dirExists(sub) {
		if installer := deviceInstallerPath(sub); installer != "" {
			runProcess(sub, time.Minute, installer, "enableidd", "0")
			runProcess(sub, time.Minute, installer, "stop", "usbmmidd")
			runProcess(sub, time.Minute, installer, "remove", "usbmmidd.inf")
		}
	}

	if sub := filepath.Join(dir, "VBCABLE"); dirExists(sub) {
		if installer := virtualCableSetupPath(sub); installer != "" {
			runProcess(sub, 3*time.Minute, installer, "-u", "-h")
		}
	}
}

func removeShortcuts() {
	removeShortcutsFrom(desktopDirs(true))
	removeShortcutsFrom(desktopDirs(false))
	removeShortcutsFrom(startMenuDirs(true))
	removeShortcutsFrom(startMenuDirs(false))
}

func scheduleDirectoryRemoval(dir string) {
	script := fmt.Sprintf(`@echo off
setlocal
set /a tries=0
:retry
timeout /t 1 /nobreak > NUL
rmdir /s /q "%s" > NUL 2>&1
if not exist "%s" goto done
set /a tries+=1
if %%tries%% lss 40 goto retry
:done
del "%%~f0"
`, dir, dir)

	f, err := os.CreateTemp("", "bigpocket_cleanup_*.bat")
	if err != nil {
		return
	}
	name := f.Name()
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		os.Remove(name)
		return
	}
	f.Close()

	_ = execCommandHidden("cmd", "/c", name).Start()
}

// ------------------------------- Yardımcılar -------------------------------

func defaultDir(allUsers bool) string {
	if allUsers {
		base := strings.TrimSpace(os.Getenv("ProgramFiles"))
		if base == "" {
			base = `C:\Program Files`
		}
		return filepath.Join(base, appName)
	}

	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	return filepath.Join(base, "Programs", appName)
}

func cleanDir(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if cleaned := filepath.Clean(p); cleaned != "" && cleaned != "." {
		return cleaned
	}
	return p
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func formatBytes(n uint64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d bayt", n)
	}
}

func isUnderProgramFiles(path string) bool {
	p := strings.ToLower(filepath.Clean(path))
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		base := strings.ToLower(strings.TrimSpace(os.Getenv(env)))
		if base != "" && strings.HasPrefix(p, base) {
			return true
		}
	}
	return false
}

func needsElevation(dir string) bool {
	if isAdmin() {
		return false
	}
	if isUnderProgramFiles(dir) {
		return true
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return true
	}
	probe := filepath.Join(dir, ".bigpocket_write_test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return true
	}
	f.Close()
	os.Remove(probe)
	return false
}

func isAdmin() bool {
	r, _, _ := sIsUserAnAdmin.Call()
	return r != 0
}

func shellExecuteElevated(args string) error {
	self := selfPath()
	if self == "" {
		return fmt.Errorf("kurulum dosyası yolu bulunamadı")
	}

	verb, _ := syscall.UTF16PtrFromString("runas")
	exe, _ := syscall.UTF16PtrFromString(self)
	params, _ := syscall.UTF16PtrFromString(args)
	workDir, _ := syscall.UTF16PtrFromString(filepath.Dir(self))

	r, _, _ := sShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(exe)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(workDir)),
		swShowNormal,
	)

	runtime.KeepAlive(verb)
	runtime.KeepAlive(exe)
	runtime.KeepAlive(params)
	runtime.KeepAlive(workDir)

	if r <= 32 {
		return fmt.Errorf("yönetici izni alınamadı (kod %d)", r)
	}
	return nil
}

func buildAutoArgs(cfg installConfig) string {
	scope := "user"
	if cfg.allUsers {
		scope = "all"
	}

	parts := []string{
		"--elevated",
		"--autoinstall",
		"--scope=" + scope,
		"--dir=" + quoteArg(cfg.dir),
		fmt.Sprintf("--desktop=%d", boolInt(cfg.desktop)),
		fmt.Sprintf("--startmenu=%d", boolInt(cfg.startMenu)),
		fmt.Sprintf("--autostart=%d", boolInt(cfg.autostart)),
		fmt.Sprintf("--vdd=%d", boolInt(cfg.vdd)),
		fmt.Sprintf("--vcable=%d", boolInt(cfg.vcable)),
		fmt.Sprintf("--launch=%d", boolInt(cfg.launch)),
	}
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

func launchApp(dir string) {
	exe := filepath.Join(dir, "BigPocket.exe")
	if _, err := os.Stat(exe); err != nil {
		return
	}

	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(exe)
	workDir, _ := syscall.UTF16PtrFromString(dir)

	if isAdmin() {
		// Yönetici olarak kurulumdan sonra uygulamayı normal (yükseltilmemiş) yetkiyle başlat.
		explorer, _ := syscall.UTF16PtrFromString("explorer.exe")
		arg, _ := syscall.UTF16PtrFromString(`"` + exe + `"`)
		sShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)),
			uintptr(unsafe.Pointer(explorer)), uintptr(unsafe.Pointer(arg)),
			uintptr(unsafe.Pointer(workDir)), swShowNormal)
		runtime.KeepAlive(explorer)
		runtime.KeepAlive(arg)
	} else {
		sShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)),
			uintptr(unsafe.Pointer(file)), 0,
			uintptr(unsafe.Pointer(workDir)), swShowNormal)
	}

	runtime.KeepAlive(verb)
	runtime.KeepAlive(file)
	runtime.KeepAlive(workDir)
}

func killRunningApp() {
	execCommandHidden("taskkill", "/f", "/t", "/im", "BigPocket.exe").Run()
	time.Sleep(1200 * time.Millisecond)
}

func execCommandHidden(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

func initCommonControls() {
	icc := initCommonControlsEx{
		Size: uint32(unsafe.Sizeof(initCommonControlsEx{})),
		ICC:  0x00000020, // ICC_PROGRESS_CLASS
	}
	cInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
}

func chooseFolder(owner uintptr, title, initial string) string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	initialized := comInitialize()
	if initialized {
		defer oCoUninitialize.Call()
	}

	display := make([]uint16, 260)
	titlePtr, _ := syscall.UTF16PtrFromString(title)

	const (
		bifReturnOnlyFSDirs = 0x00000001
		bifEditBox          = 0x00000010
		bifNewDialogStyle   = 0x00000040
	)

	bi := browseInfo{
		HwndOwner:      syscall.Handle(owner),
		PszDisplayName: &display[0],
		LpszTitle:      titlePtr,
		UlFlags:        bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle,
	}

	pidl, _, _ := sSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	runtime.KeepAlive(titlePtr)
	if pidl == 0 {
		return ""
	}
	defer oCoTaskMemFree.Call(pidl)

	buf := make([]uint16, 260)
	ok, _, _ := sSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(buf)
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func freeSpaceFor(path string) uint64 {
	root := existingPath(path)
	if root == "" {
		return 0
	}

	pathPtr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0
	}

	var freeToCaller, total, totalFree uint64
	r, _, _ := kGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	runtime.KeepAlive(pathPtr)
	if r == 0 {
		return 0
	}
	return freeToCaller
}

func existingPath(path string) string {
	p := cleanDir(path)
	for p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ""
		}
		p = parent
	}
	return ""
}

// ------------------------------- Küçük Win32 sarmalayıcıları -------------------------------

func sendMessage(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if hwnd == 0 {
		return 0
	}
	r, _, _ := pSendMessageW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func setWindowText(hwnd uintptr, text string) {
	if hwnd == 0 {
		return
	}
	p, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}

func getWindowText(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func setFont(hwnd, font uintptr) {
	if hwnd == 0 || font == 0 {
		return
	}
	sendMessage(hwnd, wmSetFont, font, 1)
}

func show(hwnd uintptr, visible bool) {
	if hwnd == 0 {
		return
	}
	if visible {
		pShowWindow.Call(hwnd, swShow)
	} else {
		pShowWindow.Call(hwnd, swHide)
	}
}

func fillRect(hdc uintptr, r rect, brush uintptr) {
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), brush)
}

func roundRectFill(hdc uintptr, r rect, brush uintptr, radius int32) {
	oldBrush := selectObject(hdc, brush)
	oldPen := selectObject(hdc, stockNullPen)
	gRoundRect.Call(hdc,
		uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom),
		uintptr(radius), uintptr(radius),
	)
	selectObject(hdc, oldBrush)
	selectObject(hdc, oldPen)
}

func selectObject(hdc, obj uintptr) uintptr {
	if hdc == 0 || obj == 0 {
		return 0
	}
	r, _, _ := gSelectObject.Call(hdc, obj)
	return r
}

func setTextColor(hdc, color uintptr) { gSetTextColor.Call(hdc, color) }
func setBkColor(hdc, color uintptr)   { gSetBkColor.Call(hdc, color) }
func setBkMode(hdc, mode uintptr)     { gSetBkMode.Call(hdc, mode) }

func MessageBox(title, text string, style uintptr) int {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	textPtr, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}

	owner := mainHwnd
	r, _, _ := pMessageBoxW.Call(
		owner,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		style,
	)
	runtime.KeepAlive(titlePtr)
	runtime.KeepAlive(textPtr)
	return int(r)
}

func askConfirm(title, text string, icon uintptr) bool {
	return MessageBox(title, text, mbYesNo|icon) == idYes
}

func boolUintptr(v bool) uintptr {
	if v {
		return 1
	}
	return 0
}
