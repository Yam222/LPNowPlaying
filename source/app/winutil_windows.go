//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetDpiForSystem  = user32.NewProc("GetDpiForSystem")
	procMessageBoxW      = user32.NewProc("MessageBoxW")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procFindWindowW      = user32.NewProc("FindWindowW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
)

func systemDPI() uint32 {
	if procGetDpiForSystem.Find() == nil {
		if r, _, _ := procGetDpiForSystem.Call(); r > 0 {
			return uint32(r)
		}
	}
	return 96
}

// 작업 표시줄을 뺀 화면 높이(px)
func screenHeight() int {
	r, _, _ := procGetSystemMetrics.Call(17) // SM_CYFULLSCREEN
	return int(r)
}

func messageBox(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(appTitle)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x40) // MB_ICONINFORMATION
}

func focusExisting() {
	cls, _ := windows.UTF16PtrFromString("webview")
	title, _ := windows.UTF16PtrFromString(appTitle)
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)))
	if h != 0 {
		procShowWindow.Call(h, 9) // SW_RESTORE
		procSetForeground.Call(h)
	}
}

func openURL(target string) {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(target)
	_ = windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

func hiddenCmd(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}

func setClipboard(text string) error {
	u, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	size := uintptr(len(u) * 2)
	h, _, err := procGlobalAlloc.Call(0x0002, size) // GMEM_MOVEABLE
	if h == 0 {
		return err
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return err
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u)), u)
	procGlobalUnlock.Call(h)
	if r, _, err := procSetClipboardData.Call(13, h); r == 0 { // CF_UNICODETEXT
		procGlobalFree.Call(h)
		return err
	}
	return nil
}
