package main

// ----------------- Auto Update (GitHub Releases) -----------------
//
// Release workflow (for the maintainer):
//   1. Bump AppVersion below (and versionName/versionCode in android/app/build.gradle.kts).
//   2. Create a GitHub Release with tag "vX.Y.Z" and write the changelog in the release body
//      (Markdown). This text is shown to users as "update notes".
//   3. Attach assets:
//        - bigpocket-windows.zip  (BigPocket.exe + frontend/ folder)  OR a plain .exe
//        - bigpocket.apk          (Android)

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	AppVersion = "v1.5.0-beta"
	// GitHubRepo is "owner/repo" of the public repository that publishes releases.
	GitHubRepo = "benyigiteren/bigpocket"
)

type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type GitHubRelease struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	Prerelease  bool           `json:"prerelease"`
	Assets      []ReleaseAsset `json:"assets"`
}

type UpdateInfo struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Available      bool   `json:"available"`
	Title          string `json:"title"`
	Notes          string `json:"notes"`
	URL            string `json:"url"`
	PublishedAt    string `json:"published_at"`
	DownloadURL    string `json:"download_url"`
	Error          string `json:"error,omitempty"`
}

var (
	updateMu       sync.Mutex
	updateCache    *UpdateInfo
	updateCachedAt time.Time
	updateProgress = map[string]interface{}{"state": "idle", "percent": 0}
)

// parseSemVer parses "v1.4.2-beta.1" into numeric segments [1, 4, 2] and pre-release tag "beta.1".
func parseSemVer(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, "-", 2)
	numStr := parts[0]
	pre := ""
	if len(parts) > 1 {
		pre = parts[1]
	}

	numParts := strings.Split(numStr, ".")
	nums := make([]int, 3)
	for i := 0; i < len(numParts) && i < 3; i++ {
		nums[i], _ = strconv.Atoi(numParts[i])
	}
	return nums, pre
}

// compareVersions returns 1 if a>b, -1 if a<b, 0 if equal.
// Follows SemVer 2.0: numeric precedence first; when numbers are equal, stable > prerelease.
func compareVersions(a, b string) int {
	numsA, preA := parseSemVer(a)
	numsB, preB := parseSemVer(b)

	for i := range 3 {
		if numsA[i] > numsB[i] {
			return 1
		}
		if numsA[i] < numsB[i] {
			return -1
		}
	}

	// Numbers are equal:
	// A stable release (empty pre) is GREATER than a beta/prerelease!
	if preA == "" && preB != "" {
		return 1 // e.g. 1.4.2 > 1.4.2-beta
	}
	if preA != "" && preB == "" {
		return -1 // e.g. 1.4.2-beta < 1.4.2
	}
	if preA == "" && preB == "" {
		return 0
	}

	// Both are prereleases:
	if preA == preB {
		return 0
	}
	return strings.Compare(preA, preB)
}

func resolveReleaseDownloadURL(rel GitHubRelease) string {
	// 1. Prioritize full installer executable
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, ".exe") && (strings.Contains(n, "windows") || strings.Contains(n, "setup") || strings.Contains(n, "installer")) {
			return a.BrowserDownloadURL
		}
	}
	// 2. Fall back to zip bundle
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, ".zip") && strings.Contains(n, "win") {
			return a.BrowserDownloadURL
		}
	}
	// 3. Fall back to bare exe
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, ".exe") {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

func checkForUpdate(force bool, forceBeta ...bool) *UpdateInfo {
	updateMu.Lock()
	defer updateMu.Unlock()
	if !force && updateCache != nil && time.Since(updateCachedAt) < 30*time.Minute {
		return updateCache
	}

	curVer := strings.TrimPrefix(AppVersion, "v")
	info := &UpdateInfo{CurrentVersion: curVer}
	client := &http.Client{Timeout: 10 * time.Second}

	isBeta := loadConfig().BetaChannel
	if len(forceBeta) > 0 {
		isBeta = forceBeta[0]
	}
	var targetURL string
	if isBeta {
		targetURL = "https://api.github.com/repos/" + GitHubRepo + "/releases?per_page=5"
	} else {
		targetURL = "https://api.github.com/repos/" + GitHubRepo + "/releases/latest"
	}

	req, _ := http.NewRequest("GET", targetURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "BigPocket-Updater")
	resp, err := client.Do(req)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		info.Error = fmt.Sprintf("GitHub API status %d", resp.StatusCode)
		return info
	}

	var candidates []GitHubRelease
	if isBeta {
		if err := json.NewDecoder(resp.Body).Decode(&candidates); err != nil {
			info.Error = err.Error()
			return info
		}
	} else {
		var single GitHubRelease
		if err := json.NewDecoder(resp.Body).Decode(&single); err != nil {
			info.Error = err.Error()
			return info
		}
		candidates = []GitHubRelease{single}
	}

	if len(candidates) == 0 {
		info.Error = "No releases found"
		return info
	}

	// Select the highest version available
	var bestRelease *GitHubRelease
	for i := range candidates {
		rel := &candidates[i]
		if bestRelease == nil || compareVersions(rel.TagName, bestRelease.TagName) > 0 {
			bestRelease = rel
		}
	}

	if bestRelease != nil {
		rel := *bestRelease
		info.LatestVersion = strings.TrimPrefix(rel.TagName, "v")
		info.Title = rel.Name
		info.Notes = rel.Body
		info.URL = rel.HTMLURL
		info.PublishedAt = rel.PublishedAt
		info.DownloadURL = resolveReleaseDownloadURL(rel)
		info.Available = compareVersions(info.LatestVersion, AppVersion) > 0 && info.DownloadURL != ""
	}

	updateCache = info
	updateCachedAt = time.Now()
	return info
}

func setUpdateProgress(state string, percent int, msg string) {
	updateMu.Lock()
	updateProgress = map[string]interface{}{"state": state, "percent": percent, "message": msg}
	updateMu.Unlock()
}

type progressWriter struct {
	total, done int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.total > 0 {
		setUpdateProgress("downloading", int(p.done*100/p.total), "")
	}
	return len(b), nil
}

// installUpdate downloads the release asset, swaps files or runs installer silently with UAC elevation if needed.
func installUpdate(downloadURL string) {
	fail := func(err error) {
		fmt.Println("Update failed:", err)
		setUpdateProgress("error", 0, err.Error())
	}

	setUpdateProgress("downloading", 0, "")
	resp, err := http.Get(downloadURL)
	if err != nil {
		fail(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail(fmt.Errorf("download status %d", resp.StatusCode))
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		fail(err)
		return
	}
	dir := filepath.Dir(exePath)

	isInstaller := strings.HasSuffix(strings.ToLower(downloadURL), ".exe") &&
		(strings.Contains(strings.ToLower(downloadURL), "windows") || strings.Contains(strings.ToLower(downloadURL), "setup") || strings.Contains(strings.ToLower(downloadURL), "installer"))

	// Create temp file for download
	tmpExt := ".zip"
	if strings.HasSuffix(strings.ToLower(downloadURL), ".exe") {
		tmpExt = ".exe"
	}
	tmpFile, err := os.CreateTemp("", "bigpocket-update-*"+tmpExt)
	if err != nil {
		fail(err)
		return
	}
	tmpPath := tmpFile.Name()
	pw := &progressWriter{total: resp.ContentLength}
	_, err = io.Copy(io.MultiWriter(tmpFile, pw), resp.Body)
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		fail(err)
		return
	}

	setUpdateProgress("installing", 100, "")

	// Case 1: If downloading full installer (bigpocket-windows.exe), run it silently via ShellExecute with "runas"
	// This requests elevation only once for the update, installs cleanly into Program Files, and restarts asInvoker (no shield badge!).
	if isInstaller {
		setUpdateProgress("restarting", 100, "")
		shell32 := syscall.NewLazyDLL("shell32.dll")
		shellExecute := shell32.NewProc("ShellExecuteW")

		verbPtr, _ := syscall.UTF16PtrFromString("runas")
		installerPtr, _ := syscall.UTF16PtrFromString(tmpPath)
		argsPtr, _ := syscall.UTF16PtrFromString("/silent")
		dirPtr, _ := syscall.UTF16PtrFromString(os.TempDir())

		ret, _, _ := shellExecute.Call(
			0,
			uintptr(unsafe.Pointer(verbPtr)),
			uintptr(unsafe.Pointer(installerPtr)),
			uintptr(unsafe.Pointer(argsPtr)),
			uintptr(unsafe.Pointer(dirPtr)),
			1, // SW_SHOWNORMAL
		)
		if ret <= 32 {
			fail(fmt.Errorf("güncelleyici yönetici izni alamadı"))
			return
		}
		time.Sleep(500 * time.Millisecond)
		cleanupOnExit()
		os.Exit(0)
		return
	}

	// Case 2: Zip or Bare Exe - Test if current directory is writable (e.g. C:\Program Files)
	testFile := filepath.Join(dir, ".test_write")
	writeErr := os.WriteFile(testFile, []byte("test"), 0644)
	if writeErr != nil {
		// Directory is write-protected (e.g., C:\Program Files\BigPocket as standard user).
		// We elevate via a clean one-shot batch script in Temp to apply the update and relaunch!
		os.Remove(testFile)
		var batContent string
		if strings.HasSuffix(strings.ToLower(downloadURL), ".zip") {
			batContent = fmt.Sprintf(`@echo off
timeout /t 1 /nobreak >nul
taskkill /f /im BigPocket.exe >nul 2>&1
powershell -Command "Expand-Archive -Path '%s' -DestinationPath '%s' -Force"
del /f /q "%s" >nul 2>&1
start "" "%s" --after-update
del "%%~f0"
`, tmpPath, dir, tmpPath, exePath)
		} else {
			batContent = fmt.Sprintf(`@echo off
timeout /t 1 /nobreak >nul
taskkill /f /im BigPocket.exe >nul 2>&1
copy /y "%s" "%s" >nul
del /f /q "%s" >nul 2>&1
start "" "%s" --after-update
del "%%~f0"
`, tmpPath, exePath, tmpPath, exePath)
		}
		// Unique, owner-only temp file: a predictable %TEMP% path could be hijacked
		// by another local process (symlink/replace race).
		batFile, err := os.CreateTemp("", "bp_update_elevated_*.bat")
		if err != nil {
			fail(err)
			return
		}
		batPath := batFile.Name()
		if _, err := batFile.WriteString(batContent); err != nil {
			batFile.Close()
			os.Remove(batPath)
			fail(err)
			return
		}
		if err := batFile.Close(); err != nil {
			os.Remove(batPath)
			fail(err)
			return
		}

		setUpdateProgress("restarting", 100, "")
		shell32 := syscall.NewLazyDLL("shell32.dll")
		shellExecute := shell32.NewProc("ShellExecuteW")
		verbPtr, _ := syscall.UTF16PtrFromString("runas")
		cmdPtr, _ := syscall.UTF16PtrFromString("cmd.exe")
		argsPtr, _ := syscall.UTF16PtrFromString("/c \"" + batPath + "\"")
		tmpDirPtr, _ := syscall.UTF16PtrFromString(os.TempDir())

		ret, _, _ := shellExecute.Call(
			0,
			uintptr(unsafe.Pointer(verbPtr)),
			uintptr(unsafe.Pointer(cmdPtr)),
			uintptr(unsafe.Pointer(argsPtr)),
			uintptr(unsafe.Pointer(tmpDirPtr)),
			0, // SW_HIDE
		)
		if ret <= 32 {
			fail(fmt.Errorf("güncelleme için yönetici onayı verilmedi"))
			return
		}
		time.Sleep(500 * time.Millisecond)
		cleanupOnExit()
		os.Exit(0)
		return
	}
	os.Remove(testFile)

	// Directory is writable (e.g. running from user profile or dev folder)
	oldPath := exePath + ".old"
	os.Remove(oldPath)

	if err := os.Rename(exePath, oldPath); err != nil {
		fail(err)
		return
	}
	restore := func() { os.Rename(oldPath, exePath) }

	if strings.HasSuffix(strings.ToLower(downloadURL), ".zip") {
		if err := extractUpdateZip(tmpPath, dir, filepath.Base(exePath)); err != nil {
			restore()
			fail(err)
			return
		}
	} else {
		if err := copyFile(tmpPath, exePath); err != nil {
			restore()
			fail(err)
			return
		}
	}

	setUpdateProgress("restarting", 100, "")
	verbPtr, _ := syscall.UTF16PtrFromString("open")
	exePtr, _ := syscall.UTF16PtrFromString(exePath)
	argPtr, _ := syscall.UTF16PtrFromString("--after-update")
	dirPtr, _ := syscall.UTF16PtrFromString(dir)
	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecute := shell32.NewProc("ShellExecuteW")
	ret, _, _ := shellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(exePtr)),
		uintptr(unsafe.Pointer(argPtr)),
		uintptr(unsafe.Pointer(dirPtr)),
		1, // SW_SHOWNORMAL
	)
	if ret <= 32 {
		cmd := exec.Command(exePath, "--after-update")
		cmd.Dir = dir
		if err := cmd.Start(); err != nil {
			restore()
			fail(err)
			return
		}
	}
	time.Sleep(500 * time.Millisecond)
	cleanupOnExit()
	os.Exit(0)
}

func extractUpdateZip(zipPath, destDir, exeName string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Strip a single top-level folder if the zip has one.
	prefix := ""
	if len(r.File) > 0 {
		first := strings.SplitN(r.File[0].Name, "/", 2)[0] + "/"
		all := true
		for _, f := range r.File {
			if !strings.HasPrefix(f.Name, first) {
				all = false
				break
			}
		}
		if all {
			prefix = first
		}
	}

	foundExe := false
	for _, f := range r.File {
		name := strings.TrimPrefix(f.Name, prefix)
		if name == "" {
			continue
		}
		// safeZipEntryPath rejects absolute paths and any "../" escape (Zip Slip).
		target, err := safeZipEntryPath(destDir, name)
		if err != nil {
			continue
		}
		baseName := filepath.Base(name)
		if strings.EqualFold(baseName, exeName) || strings.EqualFold(baseName, "bigpocket.exe") {
			exeTarget, err := safeJoinPath(destDir, exeName)
			if err != nil {
				return err
			}
			target = exeTarget
			foundExe = true
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0755)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	if !foundExe {
		return fmt.Errorf("zip does not contain an .exe")
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

// cleanupOldUpdate removes leftovers of a previous update and, when restarted by the
// updater, waits for the old process to release its ports.
func cleanupOldUpdate() {
	for _, a := range os.Args[1:] {
		if a == "--after-update" {
			time.Sleep(2 * time.Second)
		}
	}
	if exePath, err := os.Executable(); err == nil {
		os.Remove(exePath + ".old")
	}
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func setupUpdateRoutes() {
	http.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"version": AppVersion, "repo": GitHubRepo})
	})

	http.HandleFunc("/api/update/check", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		force := r.URL.Query().Get("force") == "1"
		qBeta := r.URL.Query().Get("beta")
		var isBeta bool
		if qBeta == "1" || qBeta == "true" {
			isBeta = true
		} else if qBeta == "0" || qBeta == "false" {
			isBeta = false
		} else {
			isBeta = loadConfig().BetaChannel
		}
		json.NewEncoder(w).Encode(checkForUpdate(force, isBeta))
	})

	http.HandleFunc("/api/update/progress", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		updateMu.Lock()
		defer updateMu.Unlock()
		json.NewEncoder(w).Encode(updateProgress)
	})

	// Installing is only allowed from the local desktop UI.
	http.HandleFunc("/api/update/install", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || !isLocalRequest(r) {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Forbidden"})
			return
		}
		info := checkForUpdate(false)
		if !info.Available {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "No update available"})
			return
		}
		go installUpdate(info.DownloadURL)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	})
}
