//go:build windows

package tui

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct used by GlobalMemoryStatusEx.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func memStatus() (memoryStatusEx, error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	r1, _, err := proc.Call(uintptr(unsafe.Pointer(&m)))
	if r1 == 0 {
		return m, err
	}
	return m, nil
}

// systemMemoryTotal returns total physical RAM in kilobytes on Windows.
func systemMemoryTotal() (int64, error) {
	m, err := memStatus()
	if err != nil {
		return 0, err
	}
	return int64(m.TotalPhys / 1024), nil
}

// systemMemoryAvail returns available RAM in kilobytes on Windows.
func systemMemoryAvail() (int64, error) {
	m, err := memStatus()
	if err != nil {
		return 0, err
	}
	return int64(m.AvailPhys / 1024), nil
}
