package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
)

// ----------------- Screen Grabber & Streamer -----------------

const (
	// screenStreamFPS is the target frame rate of the live screen stream.
	screenStreamFPS = 30
	// screenStreamFrameGap is the frame budget/ticker interval (~33ms).
	screenStreamFrameGap = time.Second / screenStreamFPS
	// screenStreamWriteBudget caps how long a single frame may occupy the
	// socket write path before the client is considered dead/congested.
	screenStreamWriteBudget = 45 * time.Millisecond
	// screenStreamLayoutTTL is how long the monitor count/bounds are cached.
	// Win32 display enumeration is expensive, so it is never done per frame.
	screenStreamLayoutTTL = 2 * time.Second
	// screenStreamPoolMaxChunk prevents a single oversized frame from pinning
	// a huge backing array inside the shared buffer pool forever.
	screenStreamPoolMaxChunk = 4 << 20 // 4 MiB
)

// screenLayout is the cached geometry of the streamed monitor.
type screenLayout struct {
	monitors int
	monIndex int
	bounds   image.Rectangle
}

var (
	screenLayoutLock  sync.RWMutex
	screenLayoutCache screenLayout
	screenLayoutUntil time.Time
)

// cachedScreenLayout returns the monitor layout used by the streamer, refreshing
// NumActiveDisplays/GetDisplayBounds at most once every screenStreamLayoutTTL.
// The second bool reports whether at least one display was found.
func cachedScreenLayout() (screenLayout, bool) {
	now := time.Now()

	screenLayoutLock.RLock()
	if now.Before(screenLayoutUntil) {
		cached := screenLayoutCache
		screenLayoutLock.RUnlock()
		return cached, cached.monitors > 0
	}
	screenLayoutLock.RUnlock()

	screenLayoutLock.Lock()
	defer screenLayoutLock.Unlock()
	// Another goroutine may have refreshed while we waited for the lock.
	if now.Before(screenLayoutUntil) {
		return screenLayoutCache, screenLayoutCache.monitors > 0
	}

	numMonitors := screenshot.NumActiveDisplays()
	if numMonitors == 0 {
		screenLayoutCache = screenLayout{}
		screenLayoutUntil = now.Add(screenStreamLayoutTTL)
		return screenLayout{}, false
	}

	// Use 2nd monitor if exists, otherwise primary.
	monIndex := 0
	if numMonitors > 1 {
		monIndex = 1
	}

	screenLayoutCache = screenLayout{
		monitors: numMonitors,
		monIndex: monIndex,
		bounds:   screenshot.GetDisplayBounds(monIndex),
	}
	screenLayoutUntil = now.Add(screenStreamLayoutTTL)
	return screenLayoutCache, true
}

// jpegFramePool recycles the JPEG encode buffers so the streamer allocates no
// per-frame scratch memory (which previously produced hundreds of KB of GC
// pressure per frame at 30 FPS).
var jpegFramePool = sync.Pool{
	New: func() interface{} { return new(bytes.Buffer) },
}

func acquireFrameBuffer() *bytes.Buffer {
	buf := jpegFramePool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

func releaseFrameBuffer(buf *bytes.Buffer) {
	if buf.Cap() > screenStreamPoolMaxChunk {
		return // let an oversized buffer be collected instead of pinning it
	}
	jpegFramePool.Put(buf)
}

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
		if !isAuthorizedStreamIP(conn.RemoteAddr()) {
			fmt.Println("Rejected unauthorized screen stream connection from", conn.RemoteAddr())
			conn.Close()
			continue
		}
		go handleScreenStream(conn)
	}
}

// ----------------- Mouse Cursor Renderer for Second Screen -----------------
type CURSORINFO struct {
	CbSize      uint32
	Flags       uint32
	HCursor     uintptr
	PtScreenPos struct{ X, Y int32 }
}

func drawMouseCursor(img *image.RGBA, bounds image.Rectangle) {
	user32 := syscall.NewLazyDLL("user32.dll")
	getCursorInfo := user32.NewProc("GetCursorInfo")

	var ci CURSORINFO
	ci.CbSize = uint32(unsafe.Sizeof(ci))
	ret, _, _ := getCursorInfo.Call(uintptr(unsafe.Pointer(&ci)))
	if ret == 0 || ci.Flags&1 == 0 { // 1 = CURSOR_SHOWING
		return
	}

	cx := int(ci.PtScreenPos.X) - bounds.Min.X
	cy := int(ci.PtScreenPos.Y) - bounds.Min.Y

	if cx < 0 || cy < 0 || cx >= bounds.Dx() || cy >= bounds.Dy() {
		return
	}

	// Crisp high-visibility pointer arrow (15px)
	arrowBody := [][]int{
		{0, 0}, {0, 1}, {0, 2}, {0, 3}, {0, 4}, {0, 5}, {0, 6}, {0, 7}, {0, 8}, {0, 9}, {0, 10}, {0, 11}, {0, 12}, {0, 13}, {0, 14},
		{1, 1}, {1, 2}, {1, 3}, {1, 4}, {1, 5}, {1, 6}, {1, 7}, {1, 8}, {1, 9}, {1, 10}, {1, 11}, {1, 12},
		{2, 2}, {2, 3}, {2, 4}, {2, 5}, {2, 6}, {2, 7}, {2, 8}, {2, 9}, {2, 10},
		{3, 3}, {3, 4}, {3, 5}, {3, 6}, {3, 7}, {3, 8}, {3, 9}, {3, 10}, {3, 11},
		{4, 4}, {4, 5}, {4, 6}, {4, 7}, {4, 8}, {4, 9}, {4, 12}, {4, 13},
		{5, 5}, {5, 6}, {5, 7}, {5, 8}, {5, 13}, {5, 14},
		{6, 6}, {6, 7}, {6, 8}, {6, 14}, {6, 15},
		{7, 7}, {7, 8},
	}
	arrowOutline := [][]int{
		{-1, -1}, {-1, 0}, {-1, 1}, {-1, 2}, {-1, 3}, {-1, 4}, {-1, 5}, {-1, 6}, {-1, 7}, {-1, 8}, {-1, 9}, {-1, 10}, {-1, 11}, {-1, 12}, {-1, 13}, {-1, 14}, {-1, 15},
		{0, 15}, {1, 13}, {2, 11}, {3, 12}, {4, 14}, {5, 15}, {6, 16}, {7, 16}, {8, 15}, {7, 13}, {6, 9}, {7, 9}, {8, 8},
		{7, 6}, {6, 5}, {5, 4}, {4, 3}, {3, 2}, {2, 1}, {1, 0},
	}

	for _, pt := range arrowOutline {
		px, py := cx+pt[0], cy+pt[1]
		if px >= 0 && px < bounds.Dx() && py >= 0 && py < bounds.Dy() {
			img.SetRGBA(px, py, color.RGBA{0, 0, 0, 255})
		}
	}
	for _, pt := range arrowBody {
		px, py := cx+pt[0], cy+pt[1]
		if px >= 0 && px < bounds.Dx() && py >= 0 && py < bounds.Dy() {
			img.SetRGBA(px, py, color.RGBA{255, 255, 255, 255})
		}
	}
}

func handleScreenStream(conn net.Conn) {
	defer func() {
		if rec := recover(); rec != nil {
			logPanic("handleScreenStream", rec)
		}
	}()
	defer conn.Close()
	fmt.Println("Screen stream client connected.")

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
		tcpConn.SetWriteBuffer(128 * 1024)
	}

	// A ticker compensates for the time spent capturing/encoding each frame, so
	// the stream stays locked to ~30 FPS instead of degrading with every frame's
	// work (as the old sleep-based loop did).
	ticker := time.NewTicker(screenStreamFrameGap)
	defer ticker.Stop()

	var lastWriteCost time.Duration

	for range ticker.C {
		// Drop-frame guard: if flushing the previous frame already exceeded the
		// frame budget, the client socket is congested. Skip this frame instead
		// of queueing stale ones, which keeps latency low and never leaves a
		// partially written length prefix on the wire.
		if lastWriteCost > screenStreamFrameGap {
			continue
		}

		layout, ok := cachedScreenLayout()
		if !ok {
			continue
		}

		img, err := screenshot.CaptureRect(layout.bounds)
		if err != nil {
			continue
		}

		// Draw mouse cursor on captured image if inside this monitor!
		drawMouseCursor(img, layout.bounds)

		// Compress image to JPEG in memory with balanced quality.
		out := acquireFrameBuffer()
		err = jpeg.Encode(out, img, &jpeg.Options{Quality: 55})
		if err != nil {
			releaseFrameBuffer(out)
			continue
		}
		data := out.Bytes()
		length := int32(len(data))

		// Short deadline to prevent lag queues (drop frame if socket buffer is congested).
		writeStart := time.Now()
		conn.SetWriteDeadline(writeStart.Add(screenStreamWriteBudget))

		// Send size prefix (4-byte big endian) followed by the frame payload.
		sendErr := binary.Write(conn, binary.BigEndian, length)
		if sendErr == nil {
			_, sendErr = conn.Write(data)
		}
		lastWriteCost = time.Since(writeStart)

		// Release only after the payload has been handed to the socket, since
		// data aliases the pooled buffer's backing array.
		releaseFrameBuffer(out)

		if sendErr != nil {
			break
		}
	}
	fmt.Println("Screen stream client disconnected.")
}
