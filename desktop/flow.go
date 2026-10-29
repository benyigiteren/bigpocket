package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kbinani/screenshot"
)

// ----------------- BigPocket Local Flow (WhatsApp-Style Sharing) -----------------
// Enables instant, zero-configuration local messaging, link sharing, and file drops
// between PC and Phone over high-speed local Wi-Fi.

type FlowMessage struct {
	ID        string `json:"id"`
	Sender    string `json:"sender"`    // "pc" or "phone"
	Type      string `json:"type"`      // "text", "link", "file", "image"
	Text      string `json:"text"`      // text content or description
	FileName  string `json:"file_name,omitempty"`
	FileSize  int64  `json:"file_size,omitempty"`
	FileURL   string `json:"file_url,omitempty"` // relative URL to download (e.g. "/download/foo.png")
	Timestamp int64  `json:"timestamp"`
}

var (
	flowMutex    sync.RWMutex
	flowMessages []FlowMessage
	flowInitOnce sync.Once
)

func getFlowHistoryFilePath() string {
	return filepath.Join(sharedDir, "flow_history.json")
}

func loadFlowHistory() {
	flowMutex.Lock()
	defer flowMutex.Unlock()

	path := getFlowHistoryFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		// Default welcome message
		flowMessages = []FlowMessage{
			{
				ID:        fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
				Sender:    "pc",
				Type:      "text",
				Text:      "👋 BigPocket Akış'a hoş geldiniz! Bilgisayar ve telefonunuz arasında mesaj, bağlantı ve dosyaları tek tıkla paylaşabilirsiniz.",
				Timestamp: time.Now().UnixMilli(),
			},
		}
		return
	}

	var loaded []FlowMessage
	if err := json.Unmarshal(data, &loaded); err == nil && len(loaded) > 0 {
		flowMessages = loaded
	} else {
		flowMessages = []FlowMessage{}
	}
}

func saveFlowHistoryLocked() {
	path := getFlowHistoryFilePath()
	// Keep last 100 messages
	if len(flowMessages) > 100 {
		flowMessages = flowMessages[len(flowMessages)-100:]
	}
	data, err := json.MarshalIndent(flowMessages, "", "  ")
	if err == nil {
		_ = os.WriteFile(path, data, 0644)
	}
}

func addFlowMessage(msg FlowMessage) FlowMessage {
	flowMutex.Lock()
	defer flowMutex.Unlock()

	if msg.ID == "" {
		msg.ID = fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), len(flowMessages))
	}
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixMilli()
	}

	// Detect links automatically
	if msg.Type == "text" && (strings.HasPrefix(msg.Text, "http://") || strings.HasPrefix(msg.Text, "https://")) {
		msg.Type = "link"
	}

	flowMessages = append(flowMessages, msg)
	saveFlowHistoryLocked()

	// Broadcast to all connected WebSockets
	broadcast(map[string]interface{}{
		"type":    "flow_new",
		"message": msg,
	})

	return msg
}

func getFlowMessages() []FlowMessage {
	flowMutex.RLock()
	defer flowMutex.RUnlock()
	res := make([]FlowMessage, len(flowMessages))
	copy(res, flowMessages)
	return res
}

func initFlow() {
	flowInitOnce.Do(func() {
		loadFlowHistory()

		// Register HTTP routes
		http.HandleFunc("/api/flow", func(w http.ResponseWriter, r *http.Request) {
			if !checkAuth(w, r) {
				return
			}
			setCORS(w, r)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}

			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success":  true,
					"messages": getFlowMessages(),
				})
				return
			}

			if r.Method == http.MethodPost {
				var payload struct {
					Sender   string `json:"sender"`
					Type     string `json:"type"`
					Text     string `json:"text"`
					FileName string `json:"file_name"`
					FileSize int64  `json:"file_size"`
					FileURL  string `json:"file_url"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if payload.Sender == "" {
					payload.Sender = "pc"
				}
				if payload.Type == "" {
					payload.Type = "text"
				}

				msg := addFlowMessage(FlowMessage{
					Sender:   payload.Sender,
					Type:     payload.Type,
					Text:     payload.Text,
					FileName: payload.FileName,
					FileSize: payload.FileSize,
					FileURL:  payload.FileURL,
				})

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"message": msg,
				})
				return
			}

			if r.Method == http.MethodDelete {
				flowMutex.Lock()
				flowMessages = []FlowMessage{}
				saveFlowHistoryLocked()
				flowMutex.Unlock()

				broadcast(map[string]interface{}{
					"type": "flow_cleared",
				})

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
				})
				return
			}

			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		})

		// Endpoint to capture PC screenshot and drop directly into Flow
		http.HandleFunc("/api/flow/screenshot", func(w http.ResponseWriter, r *http.Request) {
			if !checkAuth(w, r) {
				return
			}
			setCORS(w, r)
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			if screenshot.NumActiveDisplays() == 0 {
				http.Error(w, "Ekran bulunamadı", http.StatusInternalServerError)
				return
			}
			bounds := screenshot.GetDisplayBounds(0)
			img, err := screenshot.CaptureRect(bounds)
			if err != nil {
				http.Error(w, "Ekran yakalanamadı: "+err.Error(), http.StatusInternalServerError)
				return
			}

			baseName := fmt.Sprintf("ekran_%s.png", time.Now().Format("20060102_150405"))
			dstPath := filepath.Join(sharedDir, baseName)
			f, err := os.Create(dstPath)
			if err != nil {
				http.Error(w, "Dosya oluşturulamadı: "+err.Error(), http.StatusInternalServerError)
				return
			}
			defer f.Close()

			if err := png.Encode(f, img); err != nil {
				http.Error(w, "Resim kodlanamadı: "+err.Error(), http.StatusInternalServerError)
				return
			}

			fi, _ := f.Stat()
			var size int64 = 0
			if fi != nil {
				size = fi.Size()
			}

			msg := addFlowMessage(FlowMessage{
				Sender:    "pc",
				Type:      "image",
				Text:      "📸 Ekran Görüntüsü",
				FileName:  baseName,
				FileSize:  size,
				FileURL:   "/download/" + baseName,
				Timestamp: time.Now().UnixMilli(),
			})

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": msg,
			})
		})

		// Dedicated Flow upload endpoint that accepts any file/image from PC or Phone
		http.HandleFunc("/api/flow/upload", func(w http.ResponseWriter, r *http.Request) {
			if !checkAuth(w, r) {
				return
			}
			setCORS(w, r)
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			sender := r.URL.Query().Get("sender")
			if sender == "" {
				sender = "pc"
			}

			file, header, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "Dosya alınamadı: "+err.Error(), http.StatusBadRequest)
				return
			}
			defer file.Close()

			dstPath, err := safeSharedFilePath(header.Filename)
			if err != nil {
				http.Error(w, "Geçersiz dosya adı: "+err.Error(), http.StatusBadRequest)
				return
			}
			fileName := filepath.Base(dstPath)
			out, err := os.Create(dstPath)
			if err != nil {
				http.Error(w, "Dosya kaydedilemedi: "+err.Error(), http.StatusInternalServerError)
				return
			}
			defer out.Close()

			n, err := io.Copy(out, file)
			if err != nil {
				http.Error(w, "Dosya yazılamadı: "+err.Error(), http.StatusInternalServerError)
				return
			}

			ext := strings.ToLower(filepath.Ext(fileName))
			msgType := "file"
			if isAllowedIconExtension(ext) {
				msgType = "image"
			}

			msg := addFlowMessage(FlowMessage{
				Sender:    sender,
				Type:      msgType,
				Text:      fileName,
				FileName:  fileName,
				FileSize:  n,
				FileURL:   "/download/" + fileName,
				Timestamp: time.Now().UnixMilli(),
			})

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": msg,
			})
		})
	})
}
