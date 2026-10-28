//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Minimal hand-written Win32 layer: the program has no third-party
// dependencies, so it is one static executable.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procIsDialogMessageW       = user32.NewProc("IsDialogMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procSendMessageW           = user32.NewProc("SendMessageW")
	procShowWindow             = user32.NewProc("ShowWindow")
	procSetWindowTextW         = user32.NewProc("SetWindowTextW")
	procEnableWindow           = user32.NewProc("EnableWindow")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procFindWindowW            = user32.NewProc("FindWindowW")
	procLoadCursorW            = user32.NewProc("LoadCursorW")
	procLoadIconW              = user32.NewProc("LoadIconW")
	procLoadImageW             = user32.NewProc("LoadImageW")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procAdjustWindowRectEx     = user32.NewProc("AdjustWindowRectEx")
	procMessageBoxW            = user32.NewProc("MessageBoxW")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procGetSysColorBrush       = user32.NewProc("GetSysColorBrush")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	procInvalidateRect         = user32.NewProc("InvalidateRect")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW     = kernel32.NewProc("CreateMutexW")

	procCreateFontW   = gdi32.NewProc("CreateFontW")
	procDeleteObject  = gdi32.NewProc("DeleteObject")
	procGetDeviceCaps = gdi32.NewProc("GetDeviceCaps")
	procSetTextColor  = gdi32.NewProc("SetTextColor")
	procSetBkMode     = gdi32.NewProc("SetBkMode")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
)

// ---- constants --------------------------------------------------------------

const (
	wmNull            = 0x0000
	wmDestroy         = 0x0002
	wmClose           = 0x0010
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	wmSetFont         = 0x0030
	wmCommand         = 0x0111
	wmCtlColorStatic  = 0x0138
	wmLButtonUp       = 0x0202
	wmLButtonDblClk   = 0x0203
	wmRButtonUp       = 0x0205
	wmApp             = 0x8000

	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsVScroll      = 0x00200000
	wsGroup        = 0x00020000
	wsExClientEdge = 0x00000200

	bsPushButton    = 0x00000000
	bsAutoCheckbox  = 0x00000003
	ssCenter        = 0x00000001
	ssCenterImage   = 0x00000200
	cbsDropDownList = 0x0003
	esMultiline     = 0x0004
	esAutoVScroll   = 0x0040
	esReadOnly      = 0x0800

	cbAddString  = 0x0143
	cbGetCurSel  = 0x0147
	cbSetCurSel  = 0x014E
	cbnSelChange = 1
	bnClicked    = 0

	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1

	emSetSel       = 0x00B1
	emReplaceSel   = 0x00C2
	emScrollCaret  = 0x00B7
	emSetLimitText = 0x00C5

	swHide       = 0
	swShow       = 5
	swRestore    = 9
	swShowNormal = 1

	smCxScreen = 0
	smCyScreen = 1
	smCxIcon   = 11
	smCyIcon   = 12
	smCxSmIcon = 49
	smCySmIcon = 50

	imageIcon      = 1
	idcArrow       = 32512
	idiApplication = 32512

	colorWindow   = 5
	bkTransparent = 1
	logPixelsX    = 88

	mfString       = 0x0000
	mfSeparator    = 0x0800
	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020

	mbOK        = 0x00000000
	mbIconError = 0x00000010
	mbIconInfo  = 0x00000040

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10
	niifInfo   = 0x01

	errorAlreadyExists = 183
)

// ---- structures -------------------------------------------------------------

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// NOTIFYICONDATAW (Vista+ layout, 976 bytes on amd64).
type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        guid
	BalloonIcon     uintptr
}

// ---- helpers ----------------------------------------------------------------

func utf16p(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func copyUTF16(dst []uint16, s string) {
	u := syscall.StringToUTF16(s)
	n := copy(dst, u)
	if n == len(dst) {
		dst[n-1] = 0
	}
}

func rgb(r, g, b uint8) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }

func sendMsg(h uintptr, m uint32, w, l uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(h, uintptr(m), w, l)
	return r
}

func sendMsgStr(h uintptr, m uint32, w uintptr, s string) uintptr {
	r, _, _ := procSendMessageW.Call(h, uintptr(m), w, uintptr(unsafe.Pointer(utf16p(s))))
	return r
}

func postMsg(h uintptr, m uint32, w, l uintptr) {
	procPostMessageW.Call(h, uintptr(m), w, l)
}

func setText(h uintptr, s string) {
	procSetWindowTextW.Call(h, uintptr(unsafe.Pointer(utf16p(s))))
}

func enable(h uintptr, on bool) {
	var v uintptr
	if on {
		v = 1
	}
	procEnableWindow.Call(h, v)
}

func showWindow(h uintptr, cmd int) { procShowWindow.Call(h, uintptr(cmd)) }

func isVisible(h uintptr) bool {
	r, _, _ := procIsWindowVisible.Call(h)
	return r != 0
}

func messageBox(owner uintptr, text, title string, flags uint32) {
	procMessageBoxW.Call(owner, uintptr(unsafe.Pointer(utf16p(text))), uintptr(unsafe.Pointer(utf16p(title))), uintptr(flags))
}

func createWindowEx(ex uint32, class, title string, style uint32, x, y, w, h int, parent, menu, inst uintptr) uintptr {
	r, _, _ := procCreateWindowExW.Call(
		uintptr(ex),
		uintptr(unsafe.Pointer(utf16p(class))),
		uintptr(unsafe.Pointer(utf16p(title))),
		uintptr(style),
		uintptr(int32(x)), uintptr(int32(y)), uintptr(int32(w)), uintptr(int32(h)),
		parent, menu, inst, 0)
	return r
}

func getSystemMetrics(i int) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(i))
	return int(int32(r))
}

func createFont(pt, dpi int, bold bool) uintptr {
	weight := 400
	if bold {
		weight = 700
	}
	height := -((pt*dpi + 36) / 72)
	r, _, _ := procCreateFontW.Call(
		uintptr(int32(height)), 0, 0, 0, uintptr(weight), 0, 0, 0,
		1, // DEFAULT_CHARSET
		0, 0,
		5, // CLEARTYPE_QUALITY
		0,
		uintptr(unsafe.Pointer(utf16p("Segoe UI"))))
	return r
}

func loadIconResource(inst uintptr, id, cx, cy int) uintptr {
	r, _, _ := procLoadImageW.Call(inst, uintptr(id), imageIcon, uintptr(cx), uintptr(cy), 0)
	if r == 0 {
		r, _, _ = procLoadIconW.Call(0, idiApplication)
	}
	return r
}

func shellExecute(owner uintptr, verb, file string) {
	procShellExecuteW.Call(owner, uintptr(unsafe.Pointer(utf16p(verb))), uintptr(unsafe.Pointer(utf16p(file))), 0, 0, swShowNormal)
}
