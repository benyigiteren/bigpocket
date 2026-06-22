package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

type WAVEOUTCAPS struct {
	Mid           uint16
	Pid           uint16
	DriverVersion uint32
	Pname         [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved      uint16
	Support       uint32
}

func main() {
	winmm := syscall.NewLazyDLL("winmm.dll")
	waveOutGetNumDevs := winmm.NewProc("waveOutGetNumDevs")
	waveOutGetDevCaps := winmm.NewProc("waveOutGetDevCapsW")

	numDevs, _, _ := waveOutGetNumDevs.Call()
	fmt.Printf("Number of waveOut devices: %d\n", numDevs)

	for i := uintptr(0); i < numDevs; i++ {
		var caps WAVEOUTCAPS
		ret, _, _ := waveOutGetDevCaps.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
		if ret == 0 {
			name := syscall.UTF16ToString(caps.Pname[:])
			fmt.Printf("Device %d: %s (Channels: %d)\n", i, name, caps.Channels)
		} else {
			fmt.Printf("Device %d: Error getting caps (%d)\n", i, ret)
		}
	}
}
