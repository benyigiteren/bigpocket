package main

import (
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

//go:embed BigPocket.exe usbmmidd VBCABLE logo.ico frontend
var embedFS embed.FS

const (
	appName      = "BigPocket"
	installPath  = `C:\Program Files\BigPocket`
	registryPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\BigPocket`
)

func main() {
	// Request admin permissions automatically
	ensureAdmin()

	// Determine if running as uninstaller
	exePath, _ := os.Executable()
	exeName := strings.ToLower(filepath.Base(exePath))
	isUninstall := exeName == "uninstall.exe" || (len(os.Args) > 1 && (os.Args[1] == "--uninstall" || os.Args[1] == "/uninstall"))

	if isUninstall {
		runUninstaller()
	} else {
		runInstaller()
	}
}

// ----------------- Installer -----------------
func runInstaller() {
	// Kill any running BigPocket.exe processes to release file locks
	execCommandHidden("taskkill", "/f", "/t", "/im", "BigPocket.exe").Run()
	time.Sleep(2 * time.Second)

	// 1. Create installation directory
	err := os.MkdirAll(installPath, 0755)
	if err != nil {
		MessageBox("Hata", "Kurulum dizini oluşturulamadı: "+err.Error(), 0x10) // MB_ICONERROR
		return
	}

	// 2. Extract embedded files recursively
	err = extractEmbedFS(embedFS, ".", installPath)
	if err != nil {
		MessageBox("Hata", "Dosyalar çıkartılamadı: "+err.Error(), 0x10)
		return
	}

	// 3. Copy this installer executable to installPath as uninstall.exe
	selfPath, err := os.Executable()
	if err == nil {
		destUninstaller := filepath.Join(installPath, "uninstall.exe")
		copyFile(selfPath, destUninstaller)
	}

	// 4. Create Shortcuts (Desktop & Start Menu)
	targetExe := filepath.Join(installPath, "BigPocket.exe")
	logoIco := filepath.Join(installPath, "logo.ico")
	
	homeDir, _ := os.UserHomeDir()
	desktopShortcut := filepath.Join(homeDir, "Desktop", "BigPocket.lnk")
	createShortcut(targetExe, desktopShortcut, installPath, logoIco)
 
	startMenuDir := filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "Start Menu", "Programs")
	os.MkdirAll(startMenuDir, 0755)
	startMenuShortcut := filepath.Join(startMenuDir, "BigPocket.lnk")
	createShortcut(targetExe, startMenuShortcut, installPath, logoIco)

	// 5. Register in Windows Programs & Features (Registry)
	registerUninstall()

	// 6. Launch installed application
	cmd := execCommandHidden("cmd", "/c", "start", "", targetExe)
	cmd.Dir = installPath
	cmd.Start()

	// 7. Inform user
	MessageBox("BigPocket Kurulumu", "BigPocket başarıyla bilgisayarınıza kuruldu!\nMasaüstü ve Başlat menüsü kısayolları oluşturuldu.", 0x40) // MB_ICONINFORMATION
}

// ----------------- Uninstaller -----------------
func runUninstaller() {
	// 1. Ask for confirmation
	ret := MessageBox("BigPocket Kaldırma", "BigPocket programını bilgisayarınızdan kaldırmak istediğinize emin misiniz?", 0x24) // MB_YESNO | MB_ICONQUESTION
	if ret != 6 { // IDYES is 6
		return
	}

	// 2. Kill running processes
	execCommandHidden("taskkill", "/f", "/im", "BigPocket.exe").Run()

	// 3. Delete registry entry
	registry.DeleteKey(registry.LOCAL_MACHINE, registryPath)

	// 4. Delete shortcuts
	homeDir, _ := os.UserHomeDir()
	os.Remove(filepath.Join(homeDir, "Desktop", "BigPocket.lnk"))
	os.Remove(filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "Start Menu", "Programs", "BigPocket.lnk"))

	// 5. Create background cleanup batch file to delete installer files and itself
	tempBatch := filepath.Join(os.Getenv("TEMP"), "bigpocket_cleanup.bat")
	batchContent := fmt.Sprintf(`@echo off
timeout /t 2 /nobreak > NUL
rmdir /s /q "%s"
del "%%~f0"
`, installPath)

	os.WriteFile(tempBatch, []byte(batchContent), 0755)

	// Run batch file in background
	execCommandHidden("cmd", "/c", tempBatch).Start()

	// 6. Inform user
	MessageBox("BigPocket Kaldırma", "BigPocket başarıyla kaldırıldı.", 0x40)
}

// ----------------- Helper Functions -----------------
func extractEmbedFS(fs embed.FS, srcDir, destDir string) error {
	entries, err := fs.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := srcDir + "/" + entry.Name()
		if srcDir == "." {
			srcPath = entry.Name()
		}
		
		// Skip compiling setup/installer itself from copying
		if entry.Name() == "setup" || entry.Name() == "installer.go" {
			continue
		}

		destPath := filepath.Join(destDir, entry.Name())
		if entry.IsDir() {
			err = os.MkdirAll(destPath, 0755)
			if err != nil {
				return err
			}
			err = extractEmbedFS(fs, srcPath, destPath)
			if err != nil {
				return err
			}
		} else {
			data, err := fs.ReadFile(srcPath)
			if err != nil {
				return err
			}
			err = os.WriteFile(destPath, data, 0755)
			if err != nil {
				return err
			}
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

	_, err = io.Copy(out, in)
	return err
}

func createShortcut(targetPath, shortcutPath, workingDir, iconPath string) {
	psCmd := fmt.Sprintf(
		`$WshShell = New-Object -ComObject WScript.Shell; $Shortcut = $WshShell.CreateShortcut("%s"); $Shortcut.TargetPath = "%s"; $Shortcut.WorkingDirectory = "%s"; $Shortcut.IconLocation = "%s"; $Shortcut.Save()`,
		strings.ReplaceAll(shortcutPath, `\`, `\\`),
		strings.ReplaceAll(targetPath, `\`, `\\`),
		strings.ReplaceAll(workingDir, `\`, `\\`),
		strings.ReplaceAll(iconPath, `\`, `\\`),
	)
	execCommandHidden("powershell", "-Command", psCmd).Run()
}

func registerUninstall() {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, registryPath, registry.ALL_ACCESS)
	if err != nil {
		return
	}
	defer k.Close()

	k.SetStringValue("DisplayName", "BigPocket")
	k.SetStringValue("DisplayVersion", "1.0.0")
	k.SetStringValue("Publisher", "Ygt")
	k.SetStringValue("UninstallString", filepath.Join(installPath, "uninstall.exe"))
	k.SetStringValue("DisplayIcon", filepath.Join(installPath, "BigPocket.exe"))
}

func MessageBox(title, text string, style uintptr) int {
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	textPtr, _ := syscall.UTF16PtrFromString(text)
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	ret, _, _ := messageBox.Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), style)
	return int(ret)
}

func checkIfAdmin() bool {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	isUserAnAdmin := shell32.NewProc("IsUserAnAdmin")
	ret, _, _ := isUserAnAdmin.Call()
	return ret != 0
}

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

func execCommandHidden(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
