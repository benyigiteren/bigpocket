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
	LPTR        = 0x0040
)

func main() {
	winmm := syscall.NewLazyDLL("winmm.dll")
	waveOutOpen := winmm.NewProc("waveOutOpen")
	waveOutPrepareHeader := winmm.NewProc("waveOutPrepareHeader")
	waveOutWrite := winmm.NewProc("waveOutWrite")
	waveOutUnprepareHeader := winmm.NewProc("waveOutUnprepareHeader")
	waveOutClose := winmm.NewProc("waveOutClose")

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	localAlloc := kernel32.NewProc("LocalAlloc")
	localFree := kernel32.NewProc("LocalFree")
	rtlMoveMemory := kernel32.NewProc("RtlMoveMemory")

	// 44100 Hz, 16-bit Mono PCM
	wfx := WAVEFORMATEX{
		FormatTag:      1, // WAVE_FORMAT_PCM
		Channels:       1,
		SamplesPerSec:  44100,
		BitsPerSample:  16,
		BlockAlign:     2,
		AvgBytesPerSec: 44100 * 2,
		Size:           0,
	}

	var hwo uintptr
	ret, _, _ := waveOutOpen.Call(
		uintptr(unsafe.Pointer(&hwo)),
		WAVE_MAPPER,
		uintptr(unsafe.Pointer(&wfx)),
		0, 0, 0,
	)
	if ret != 0 {
		fmt.Printf("waveOutOpen failed: %d\n", ret)
		return
	}
	defer waveOutClose.Call(hwo)
	fmt.Println("waveOutOpen success!")

	// Generate 0.5s of sine wave (440Hz)
	duration := 0.5
	numSamples := int(44100 * duration)
	data := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / 44100.0
		data[i] = int16(math.Sin(2*math.Pi*440.0*t) * 16000.0)
	}

	dataSize := numSamples * 2

	// Allocate out-of-GC memory for header and data
	hdrPtr, _, _ := localAlloc.Call(LPTR, unsafe.Sizeof(WAVEHDR{}))
	pcmPtr, _, _ := localAlloc.Call(LPTR, uintptr(dataSize))

	// Copy to PCM pointer
	rtlMoveMemory.Call(pcmPtr, uintptr(unsafe.Pointer(&data[0])), uintptr(dataSize))

	hdr := (*WAVEHDR)(unsafe.Pointer(hdrPtr))
	hdr.Data = pcmPtr
	hdr.BufferLength = uint32(dataSize)

	// Prepare
	ret, _, _ = waveOutPrepareHeader.Call(hwo, hdrPtr, unsafe.Sizeof(WAVEHDR{}))
	if ret != 0 {
		fmt.Printf("waveOutPrepareHeader failed: %d\n", ret)
		return
	}

	// Play
	fmt.Println("Playing sine wave from unmanaged memory...")
	ret, _, _ = waveOutWrite.Call(hwo, hdrPtr, unsafe.Sizeof(WAVEHDR{}))
	if ret != 0 {
		fmt.Printf("waveOutWrite failed: %d\n", ret)
		return
	}

	// Wait
	for {
		if hdr.Flags&WHDR_DONE != 0 {
			fmt.Println("Playback finished!")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	waveOutUnprepareHeader.Call(hwo, hdrPtr, unsafe.Sizeof(WAVEHDR{}))
	localFree.Call(hdrPtr)
	localFree.Call(pcmPtr)
	fmt.Println("Memory freed successfully!")
}
