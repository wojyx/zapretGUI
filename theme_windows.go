//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	uxtheme  = syscall.NewLazyDLL("uxtheme.dll")
	ntdll    = syscall.NewLazyDLL("ntdll.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procSetWindowTheme        = uxtheme.NewProc("SetWindowTheme")
	procRtlGetVersion         = ntdll.NewProc("RtlGetVersion")
	procGetProcAddress        = kernel32.NewProc("GetProcAddress")

	procFillRect       = user32.NewProc("FillRect")
	procDrawTextW      = user32.NewProc("DrawTextW")
	procDrawFocusRect  = user32.NewProc("DrawFocusRect")
	procGetWindowTextW = user32.NewProc("GetWindowTextW")
	procGetClientRect  = user32.NewProc("GetClientRect")
	procRedrawWindow   = user32.NewProc("RedrawWindow")
	procInflateRect    = user32.NewProc("InflateRect")

	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procCreatePen        = gdi32.NewProc("CreatePen")
	procSelectObject     = gdi32.NewProc("SelectObject")
	procRoundRect        = gdi32.NewProc("RoundRect")
	procMoveToEx         = gdi32.NewProc("MoveToEx")
	procLineTo           = gdi32.NewProc("LineTo")
	procSetBkColor       = gdi32.NewProc("SetBkColor")
	procGetStockObject   = gdi32.NewProc("GetStockObject")
)

const (
	wmEraseBkgnd      = 0x0014
	wmSettingChange   = 0x001A
	wmDrawItem        = 0x002B
	wmCtlColorEdit    = 0x0133
	wmCtlColorListbox = 0x0134

	bsOwnerDraw = 0x0000000B

	odsSelected    = 0x0001
	odsDisabled    = 0x0004
	odsFocus       = 0x0010
	odsNoFocusRect = 0x0200

	dtLeft       = 0x0000
	dtCenter     = 0x0001
	dtVCenter    = 0x0004
	dtSingleLine = 0x0020
	dtNoPrefix   = 0x0800
	dtEllipsis   = 0x8000

	bkOpaque = 2

	psSolid   = 0
	nullBrush = 5

	rdwInvalidate  = 0x0001
	rdwErase       = 0x0004
	rdwFrame       = 0x0400
	rdwAllChildren = 0x0080

	dwmUseImmersiveDarkModeOld = 19 // Windows 10 1809-1909
	dwmUseImmersiveDarkMode    = 20 // Windows 10 2004+, Windows 11

	buildDarkFrame = 17763 // Windows 10 1809: dark title bar, AllowDarkModeForApp
	buildDarkCtrl  = 18362 // Windows 10 1903: DarkMode_* control themes, SetPreferredAppMode
)

// DRAWITEMSTRUCT (identical layout on amd64: uint32 x5, pad, pointers, RECT, ULONG_PTR).
type drawItem struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	Hwnd       uintptr
	HDC        uintptr
	Rc         rect
	ItemData   uintptr
}

// asDrawItem reinterprets the LPARAM of WM_DRAWITEM (go vet rejects a direct uintptr->Pointer cast).
func asDrawItem(lparam uintptr) *drawItem {
	return (*drawItem)(*(*unsafe.Pointer)(unsafe.Pointer(&lparam)))
}

// ---- system queries ---------------------------------------------------------------

func windowsBuild() int {
	var v struct {
		Size                          uint32
		Major, Minor, Build, Platform uint32
		CSD                           [128]uint16
	}
	v.Size = uint32(unsafe.Sizeof(v))
	if r, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&v))); r != 0 {
		return 0
	}
	return int(v.Build)
}

// systemUsesDark reads "Apps use dark mode" from the registry.
func systemUsesDark() bool {
	const key = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	var h syscall.Handle
	if syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, utf16p(key), 0, syscall.KEY_READ, &h) != nil {
		return false
	}
	defer syscall.RegCloseKey(h)
	var typ, size uint32 = 0, 4
	var val uint32
	if syscall.RegQueryValueEx(h, utf16p("AppsUseLightTheme"), nil, &typ, (*byte)(unsafe.Pointer(&val)), &size) != nil || typ != syscall.REG_DWORD {
		return false
	}
	return val == 0
}

// setPreferredAppMode makes native popup menus (the tray menu) follow the theme.
// It calls the uxtheme exports 135 (SetPreferredAppMode) and 136 (FlushMenuThemes),
// which have no names; they exist from Windows 10 1809 on, so older builds are skipped.
func setPreferredAppMode(mode uintptr) {
	if windowsBuild() < buildDarkFrame {
		return
	}
	h, err := syscall.LoadLibrary("uxtheme.dll")
	if err != nil {
		return
	}
	if set, _, _ := procGetProcAddress.Call(uintptr(h), 135); set != 0 {
		syscall.SyscallN(set, mode)
	}
	if flush, _, _ := procGetProcAddress.Call(uintptr(h), 136); flush != 0 {
		syscall.SyscallN(flush)
	}
}

// ---- applying a theme ---------------------------------------------------------------

func (a *App) deleteThemeBrushes() {
	for _, b := range []uintptr{a.brBg, a.brPanel} {
		if b != 0 {
			procDeleteObject.Call(b)
		}
	}
}

func (a *App) setControlTheme(h uintptr, dark bool, class string) {
	if windowsBuild() < buildDarkCtrl {
		return
	}
	if dark {
		procSetWindowTheme.Call(h, uintptr(unsafe.Pointer(utf16p(class))), uintptr(unsafe.Pointer(utf16p("ComboBox"))))
		return
	}
	procSetWindowTheme.Call(h, 0, 0) // back to the default look
}

// applyTheme resolves the configured theme and repaints the whole window (UI thread only).
func (a *App) applyTheme() {
	dark := resolveTheme(a.cfg.Theme, systemUsesDark())
	a.dark = dark
	a.pal = paletteFor(dark)

	a.deleteThemeBrushes()
	b1, _, _ := procCreateSolidBrush.Call(a.pal.Bg.COLORREF())
	b2, _, _ := procCreateSolidBrush.Call(a.pal.Panel.COLORREF())
	a.brBg, a.brPanel = b1, b2

	mode := uintptr(1) // AllowDark: menus follow Windows
	switch a.cfg.Theme {
	case themeDark:
		mode = 2 // ForceDark
	case themeLight:
		mode = 3 // ForceLight
	}
	setPreferredAppMode(mode)

	if windowsBuild() >= buildDarkFrame {
		var v int32
		if dark {
			v = 1
		}
		hr, _, _ := procDwmSetWindowAttribute.Call(a.hwnd, dwmUseImmersiveDarkMode, uintptr(unsafe.Pointer(&v)), 4)
		if hr != 0 {
			procDwmSetWindowAttribute.Call(a.hwnd, dwmUseImmersiveDarkModeOld, uintptr(unsafe.Pointer(&v)), 4)
		}
	}

	for _, c := range []uintptr{a.cStrategy, a.cGame, a.cIPSet, a.cFakeDiscord, a.cFakeGame} {
		a.setControlTheme(c, dark, "DarkMode_CFD")
	}
	a.setControlTheme(a.eLog, dark, "DarkMode_Explorer") // scroll bar of the log

	setText(a.bTheme, themeLabel(a.cfg.Theme))
	a.refresh() // status colour depends on the palette
	procRedrawWindow.Call(a.hwnd, 0, 0, rdwInvalidate|rdwErase|rdwFrame|rdwAllChildren)
}

// onSystemThemeChanged follows Windows when the setting is "auto".
func (a *App) onSystemThemeChanged() {
	if a.cfg.Theme == themeAuto && systemUsesDark() != a.dark {
		a.applyTheme()
	}
}

func (a *App) onThemeButton() {
	a.cfg.Theme = nextTheme(a.cfg.Theme)
	_ = saveConfig(a.cfgPath, a.cfg)
	a.applyTheme()
}

// ---- colours for the common controls -----------------------------------------------

// ctlColor answers WM_CTLCOLORSTATIC / EDIT / LISTBOX.
func (a *App) ctlColor(hdc, ctl uintptr, edit bool) uintptr {
	if a.brBg == 0 {
		return 0
	}
	if edit || ctl == a.eLog { // log field and drop-down lists: opaque panel colour
		procSetTextColor.Call(hdc, a.pal.Text.COLORREF())
		procSetBkColor.Call(hdc, a.pal.Panel.COLORREF())
		procSetBkMode.Call(hdc, bkOpaque)
		return a.brPanel
	}
	col := a.pal.Text
	switch ctl {
	case a.lStatus:
		col = a.statusColor
	case a.lFooter:
		col = a.pal.Muted
	case a.lService:
		col = a.pal.Muted
		if a.svcShownOK {
			col = a.pal.OK
		}
	}
	procSetTextColor.Call(hdc, col.COLORREF())
	procSetBkMode.Call(hdc, bkTransparent)
	return a.brBg
}

func (a *App) eraseBackground(hdc uintptr) uintptr {
	if a.brBg == 0 {
		return 0
	}
	var rc rect
	procGetClientRect.Call(a.hwnd, uintptr(unsafe.Pointer(&rc)))
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), a.brBg)
	return 1
}

// ---- owner-drawn buttons ---------------------------------------------------------------

func windowText(h uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func (a *App) fillRect(hdc uintptr, rc *rect, c RGB) {
	br, _, _ := procCreateSolidBrush.Call(c.COLORREF())
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(rc)), br)
	procDeleteObject.Call(br)
}

func (a *App) drawText(hdc uintptr, text string, rc *rect, col RGB, flags uintptr) {
	procSetBkMode.Call(hdc, bkTransparent)
	procSetTextColor.Call(hdc, col.COLORREF())
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(utf16p(text))), ^uintptr(0), uintptr(unsafe.Pointer(rc)), flags|dtSingleLine|dtVCenter|dtNoPrefix)
}

// withPenBrush runs f with the given pen and brush selected (brush 0 = hollow).
func (a *App) withPenBrush(hdc uintptr, pen, fill RGB, hollow bool, width int, f func()) {
	p, _, _ := procCreatePen.Call(psSolid, uintptr(width), pen.COLORREF())
	var br uintptr
	own := false
	if hollow {
		br, _, _ = procGetStockObject.Call(nullBrush)
	} else {
		br, _, _ = procCreateSolidBrush.Call(fill.COLORREF())
		own = true
	}
	oldP, _, _ := procSelectObject.Call(hdc, p)
	oldB, _, _ := procSelectObject.Call(hdc, br)
	f()
	procSelectObject.Call(hdc, oldP)
	procSelectObject.Call(hdc, oldB)
	procDeleteObject.Call(p)
	if own {
		procDeleteObject.Call(br)
	}
}

// onDrawItem paints every push button and the check box in the current palette.
func (a *App) onDrawItem(lparam uintptr) uintptr {
	di := asDrawItem(lparam)
	if di == nil || di.CtlType != 4 /* ODT_BUTTON */ || a.brBg == 0 {
		return 0
	}
	hdc, rc := di.HDC, di.Rc
	disabled := di.ItemState&odsDisabled != 0
	pressed := di.ItemState&odsSelected != 0
	focused := di.ItemState&odsFocus != 0 && di.ItemState&odsNoFocusRect == 0
	pal := a.pal
	procSelectObject.Call(hdc, a.fontNormal)
	text := windowText(di.Hwnd)

	a.fillRect(hdc, &rc, pal.Bg) // corners and the check box row

	if state, ok := a.checks[di.Hwnd]; ok {
		a.drawCheckBox(hdc, rc, text, *state, disabled, focused)
		return 1
	}

	fill := pal.Button
	switch {
	case disabled:
		fill = pal.ButtonDimmed
	case pressed:
		fill = pal.ButtonDown
	}
	radius := uintptr(a.s(8))
	edge := pal.Border
	tab := (di.Hwnd == a.bTabMain && a.page == 0) || (di.Hwnd == a.bTabAdv && a.page == 1)
	if tab && !disabled {
		fill = pal.ButtonDown // the open tab
		edge = pal.Accent
	}
	if focused {
		edge = pal.Accent
	}
	a.withPenBrush(hdc, edge, fill, false, 1, func() {
		procRoundRect.Call(hdc, uintptr(rc.Left), uintptr(rc.Top), uintptr(rc.Right-1), uintptr(rc.Bottom-1), radius, radius)
	})
	col := pal.Text
	if disabled {
		col = pal.Disabled
	}
	tr := rc
	if pressed {
		tr.Top++
		tr.Left++
	}
	a.drawText(hdc, text, &tr, col, dtCenter|dtEllipsis)
	return 1
}

func (a *App) drawCheckBox(hdc uintptr, rc rect, text string, checked, disabled, focused bool) {
	pal := a.pal
	size := int32(a.s(16))
	top := rc.Top + (rc.Bottom-rc.Top-size)/2
	box := rect{rc.Left + 1, top, rc.Left + 1 + size, top + size}

	border, fill := pal.Border, pal.Panel
	switch {
	case disabled:
		border, fill = pal.Disabled, pal.ButtonDimmed
	case checked:
		border, fill = pal.Accent, pal.Accent
	}
	radius := uintptr(a.s(4))
	a.withPenBrush(hdc, border, fill, false, 1, func() {
		procRoundRect.Call(hdc, uintptr(box.Left), uintptr(box.Top), uintptr(box.Right), uintptr(box.Bottom), radius, radius)
	})
	if checked {
		tick := RGB(0xFFFFFF)
		if disabled {
			tick = pal.Disabled
		}
		w := int(size)
		x, y := int(box.Left), int(box.Top)
		a.withPenBrush(hdc, tick, tick, true, max(a.s(2), 2), func() {
			procMoveToEx.Call(hdc, uintptr(x+w*22/100), uintptr(y+w*52/100), 0)
			procLineTo.Call(hdc, uintptr(x+w*43/100), uintptr(y+w*72/100))
			procLineTo.Call(hdc, uintptr(x+w*78/100), uintptr(y+w*30/100))
		})
	}

	tr := rect{box.Right + int32(a.s(8)), rc.Top, rc.Right, rc.Bottom}
	col := pal.Text
	if disabled {
		col = pal.Disabled
	}
	a.drawText(hdc, text, &tr, col, dtLeft|dtEllipsis)
	if focused {
		fr := rect{tr.Left - 2, rc.Top + 2, rc.Right - 2, rc.Bottom - 2}
		procDrawFocusRect.Call(hdc, uintptr(unsafe.Pointer(&fr)))
	}
}

// Compile-time layout checks for the hand-written Win32 structures (amd64).
// A wrong size would only show up as a crash on Windows, so fail the build instead.
var (
	_ [64]byte  = [unsafe.Sizeof(drawItem{})]byte{}       // DRAWITEMSTRUCT
	_ [40]byte  = [unsafe.Offsetof(drawItem{}.Rc)]byte{}  // RcItem
	_ [976]byte = [unsafe.Sizeof(notifyIconData{})]byte{} // NOTIFYICONDATAW
	_ [48]byte  = [unsafe.Sizeof(msg{})]byte{}            // MSG
	_ [80]byte  = [unsafe.Sizeof(wndClassEx{})]byte{}     // WNDCLASSEXW
)
