//go:build windows

package main

import (
	"log"
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetStdHandle               = kernel32.NewProc("GetStdHandle")
)

const (
	stdOutputHandle = uintptr(0xFFFFFFF5) // STD_OUTPUT_HANDLE = -11
	enableVTProcessing = 0x0004
)

// enableWindowsVT asks the console to interpret ANSI escape sequences.
// Without this, modern terminals like Windows Terminal already do; older
// hosts (cmd.exe) silently drop the codes and your colors vanish.
func enableWindowsVT() {
	handle, _, _ := procGetStdHandle.Call(stdOutputHandle)
	if handle == 0 || handle == ^uintptr(0) {
		return
	}
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return
	}
	mode |= enableVTProcessing
	r, _, _ = procSetConsoleMode.Call(handle, uintptr(mode))
	if r == 0 {
		log.Println("vt: could not enable virtual terminal processing")
	}
}
