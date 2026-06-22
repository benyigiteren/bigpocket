package main

import (
	"fmt"
	"math"
	"syscall"
	"time"
	"unsafe"
)

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
)

func main() {
	winmm := syscall.NewLazyDLL("winmm.dll")
	waveOutOpen := winmm.NewProc("waveOutOpen")
	waveOutPrepareHeader := winmm.NewProc("waveOutPrepareHeader")
	waveOutWrite := winmm.NewProc("waveOutWrite")
	waveOutUnprepareHeader := winmm.NewProc("waveOutUnprepareHeader")
	waveOutClose := winmm.NewProc("waveOutClose")

	// 44100 Hz, 16-bit Mono PCM
	wfx := WAVEFORMATEX{
		FormatTag:      1, // WAVE_FORMAT_PCM
		Channels:       1,
		SamplesPerSec:  44100,
		BitsPerSample:  16,
		BlockAlign:     2, // Channels * BitsPerSample / 8
		AvgBytesPerSec: 44100 * 2,
		Size:           0,
	}

	var hwo uintptr
	// Open default output device
	ret, _, _ := waveOutOpen.Call(
		uintptr(unsafe.Pointer(&hwo)),
		WAVE_MAPPER,
		uintptr(unsafe.Pointer(&wfx)),
		0, 0, 0,
	)
	if ret != 0 {
		fmt.Printf("waveOutOpen failed with code: %d\n", ret)
		return
	}
	defer waveOutClose.Call(hwo)
	fmt.Println("waveOutOpen success!")

	// Generate 1 second of sine wave (440Hz)
	duration := 1.0
	numSamples := int(44100 * duration)
	data := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / 44100.0
		data[i] = int16(math.Sin(2*math.Pi*440.0*t) * 16000.0)
	}

	// Convert to byte slice
	byteData := *(*[]byte)(unsafe.Pointer(&struct {
		addr uintptr
		len  int
		cap  int
	}{
		addr: uintptr(unsafe.Pointer(&data[0])),
		len:  numSamples * 2,
		cap:  numSamples * 2,
	}))

	var hdr WAVEHDR
	hdr.Data = uintptr(unsafe.Pointer(&byteData[0]))
	hdr.BufferLength = uint32(len(byteData))

	// Prepare
	ret, _, _ = waveOutPrepareHeader.Call(hwo, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr))
	if ret != 0 {
		fmt.Printf("waveOutPrepareHeader failed: %d\n", ret)
		return
	}

	// Play
	fmt.Println("Playing sine wave...")
	ret, _, _ = waveOutWrite.Call(hwo, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr))
	if ret != 0 {
		fmt.Printf("waveOutWrite failed: %d\n", ret)
		return
	}

	// Wait for playback to finish
	for {
		if hdr.Flags&WHDR_DONE != 0 {
			fmt.Println("Playback finished!")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	waveOutUnprepareHeader.Call(hwo, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr))
	fmt.Println("Done!")
}
