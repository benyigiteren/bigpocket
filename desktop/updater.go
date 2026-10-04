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
	"time"
)

const (
	AppVersion = "1.0"
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

// compareVersions returns 1 if a>b, -1 if a<b, 0 if equal. Accepts "v1.2.3" style.
func compareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(strings.TrimSpace(a), "v"), ".")
	pb := strings.Split(strings.TrimPrefix(strings.TrimSpace(b), "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.SplitN(pa[i], "-", 2)[0])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.SplitN(pb[i], "-", 2)[0])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func checkForUpdate(force bool) *UpdateInfo {
	updateMu.Lock()
	defer updateMu.Unlock()
	if !force && updateCache != nil && time.Since(updateCachedAt) < 30*time.Minute {
		return updateCache
	}

	info := &UpdateInfo{CurrentVersion: AppVersion}
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+GitHubRepo+"/releases/latest", nil)
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
	var rel GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		info.Error = err.Error()
		return info
	}

	info.LatestVersion = strings.TrimPrefix(rel.TagName, "v")
	info.Title = rel.Name
	info.Notes = rel.Body
	info.URL = rel.HTMLURL
	info.PublishedAt = rel.PublishedAt
	// Prefer a zip bundle (exe + frontend), fall back to a bare exe.
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, ".zip") && strings.Contains(n, "win") {
			info.DownloadURL = a.BrowserDownloadURL
			break
		}
	}
	if info.DownloadURL == "" {
		for _, a := range rel.Assets {
			if strings.HasSuffix(strings.ToLower(a.Name), ".exe") {
				info.DownloadURL = a.BrowserDownloadURL
				break
			}
		}
	}
	info.Available = compareVersions(info.LatestVersion, AppVersion) > 0 && info.DownloadURL != ""

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

// installUpdate downloads the release asset, swaps files next to the executable and restarts.
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

	tmpFile, err := os.CreateTemp("", "bigpocket-update-*")
	if err != nil {
		fail(err)
		return
	}
	tmpPath := tmpFile.Name()
	pw := &progressWriter{total: resp.ContentLength}
	_, err = io.Copy(io.MultiWriter(tmpFile, pw), resp.Body)
	tmpFile.Close()
	if err != nil {
		fail(err)
		return
	}
	defer os.Remove(tmpPath)

	setUpdateProgress("installing", 100, "")
	exePath, err := os.Executable()
	if err != nil {
		fail(err)
		return
	}
	dir := filepath.Dir(exePath)
	oldPath := exePath + ".old"
	os.Remove(oldPath)

	// A running exe on Windows can be renamed but not overwritten.
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
	cmd := exec.Command(exePath, "--after-update")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		restore()
		fail(err)
		return
	}
	time.Sleep(300 * time.Millisecond)
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
		if name == "" || strings.Contains(name, "..") {
			continue
		}
		target := filepath.Join(destDir, filepath.FromSlash(name))
		if strings.HasSuffix(strings.ToLower(name), ".exe") && !strings.Contains(name, "/") {
			// Any top-level exe becomes the main executable.
			target = filepath.Join(destDir, exeName)
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
		json.NewEncoder(w).Encode(checkForUpdate(r.URL.Query().Get("force") == "1"))
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
