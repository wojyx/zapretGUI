//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	idStrategy  = 101
	idGame      = 102
	idIPSet     = 103
	idStart     = 104
	idStop      = 105
	idAuto      = 106
	idAutostart = 107
	idFolder    = 108
	idExit      = 109
	idLog       = 110
	idUpdate    = 111
	idTheme     = 112
	idTabMain   = 113
	idTabAdv    = 114

	idFakeDiscord  = 120
	idFakeGame     = 121
	idCheckUpd     = 122
	idSvcInstall   = 123
	idSvcRemove    = 124
	idDiag         = 125
	idIPSetUpdate  = 126
	idHostsApply   = 127
	idHostsClear   = 128
	idDiscordCache = 129

	cbResetContent = 0x014B

	idTrayShow   = 201
	idTrayToggle = 202
	idTrayExit   = 203

	wmAppLog      = wmApp + 1
	wmAppState    = wmApp + 2
	wmAppSelect   = wmApp + 3
	wmAppQuit     = wmApp + 4
	wmTray        = wmApp + 5
	wmAppShow     = wmApp + 6
	wmAppAutoFail = wmApp + 7
	wmAppReload   = wmApp + 8

	windowClass = "ZapretGUIWindow"

	clientW = 500
	clientH = 504

	stateIdle = 0
	stateBusy = 1
	stateAuto = 2
)

// App is the main window. All widget access happens on the UI thread;
// background goroutines talk to it with PostMessage and a locked log buffer.
type App struct {
	root, cfgPath string
	hInst, hwnd   uintptr
	dpi           int

	fontNormal, fontBold, fontTitle, fontSmall uintptr
	icoBig, icoSmall, icoOn, icoOff            uintptr

	cStrategy, cGame, cIPSet   uintptr
	bStart, bStop, bAuto       uintptr
	bFolder, bExit, cAutostart uintptr
	bUpdate, bTheme            uintptr

	// tabs and the "Дополнительно" page
	bTabMain, bTabAdv                      uintptr
	cFakeDiscord, cFakeGame, cCheckUpd     uintptr
	bSvcInstall, bSvcRemove, bDiag         uintptr
	bIPSetUpdate, bHostsApply, bHostsClear uintptr
	bDiscordCache, lService                uintptr
	pageMain, pageAdv, advInteractive      []uintptr
	page                                   int
	checks                                 map[uintptr]*bool // owner-drawn check boxes -> their state
	updChecked                             bool
	fakeNames                              []string
	svcText                                string // guarded by mu, written by background goroutines
	svcOK, svcShownOK                      bool
	balloonText                            string // guarded by mu
	eLog, lStatus, lFooter                 uintptr

	cfg         Config
	strategies  []Strategy
	ctl         *Controller
	startHidden bool

	state int32 // stateIdle / stateBusy / stateAuto

	mu         sync.Mutex
	autoCancel context.CancelFunc
	reload     []Strategy // strategies delivered by an update, applied on the UI thread
	quitFlag   int32      // set atomically by quit(); background work must not restart winws after it

	logMu    sync.Mutex
	pending  []string
	logLines int

	pal            Palette
	dark           bool
	brBg, brPanel  uintptr // brushes of the current palette
	statusColor    RGB
	autoChecked    bool // state of the owner-drawn autostart check box
	quitting       bool
	balloonShown   bool
	taskbarCreated uint32
}

func newApp(root string, cfg Config, strategies []Strategy, startHidden bool) *App {
	a := &App{
		root:        root,
		cfgPath:     filepath.Join(root, "config.json"),
		cfg:         cfg,
		strategies:  strategies,
		ctl:         newController(root),
		startHidden: startHidden,
	}
	a.ctl.onExit = func(name string, err error, tail string) {
		a.logf("winws.exe неожиданно завершился (стратегия «%s»): %v", name, err)
		if t := strings.TrimSpace(tail); t != "" {
			a.logf("%s", t)
		}
		a.post(wmAppState, 0, 0)
	}
	return a
}

func (a *App) s(v int) int { return v * a.dpi / 96 }

func writeCrash(root string, r any) {
	text := fmt.Sprintf("%s\n%v\n\n%s", time.Now().Format(time.RFC3339), r, debug.Stack())
	_ = os.WriteFile(filepath.Join(root, "crash.log"), []byte(text), 0o644)
}

// ---- window creation ------------------------------------------------------------

func (a *App) run(initWarnings []string) error {
	a.hInst, _, _ = procGetModuleHandleW.Call(0)

	dc, _, _ := procGetDC.Call(0)
	dpi, _, _ := procGetDeviceCaps.Call(dc, logPixelsX)
	procReleaseDC.Call(0, dc)
	a.dpi = int(dpi)
	if a.dpi < 96 {
		a.dpi = 96
	}

	a.fontNormal = createFont(9, a.dpi, false)
	a.fontBold = createFont(11, a.dpi, true)
	a.fontTitle = createFont(15, a.dpi, true)
	a.fontSmall = createFont(8, a.dpi, false)

	a.icoBig = loadIconResource(a.hInst, 1, getSystemMetrics(smCxIcon), getSystemMetrics(smCyIcon))
	sx, sy := getSystemMetrics(smCxSmIcon), getSystemMetrics(smCySmIcon)
	a.icoSmall = loadIconResource(a.hInst, 1, sx, sy)
	a.icoOn = loadIconResource(a.hInst, 2, sx, sy)
	a.icoOff = loadIconResource(a.hInst, 3, sx, sy)

	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	brush, _, _ := procGetSysColorBrush.Call(colorWindow)
	wc := wndClassEx{
		WndProc:    syscall.NewCallback(a.wndProc),
		Instance:   a.hInst,
		Icon:       a.icoBig,
		Cursor:     cursor,
		Background: brush,
		ClassName:  utf16p(windowClass),
		IconSm:     a.icoSmall,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, e := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassEx: %v", e)
	}

	const style = wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox
	rc := rect{0, 0, int32(a.s(clientW)), int32(a.s(clientH))}
	procAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&rc)), style, 0, 0)
	w, h := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
	x := (getSystemMetrics(smCxScreen) - w) / 2
	y := (getSystemMetrics(smCyScreen) - h) / 2

	a.hwnd = createWindowEx(0, windowClass, "Zapret GUI — Discord и YouTube", style, x, y, w, h, 0, 0, a.hInst)
	if a.hwnd == 0 {
		return fmt.Errorf("не удалось создать окно")
	}
	a.taskbarCreated = uint32(registerMessage("TaskbarCreated"))

	a.createControls()
	a.fillControls()
	a.trayAdd()
	a.applyTheme() // sets palette, brushes, dark title bar and refreshes the status line
	a.showPage(0)

	a.logf("Готово. Версия zapret-discord-youtube: %s.", installedVersion(a.root))
	for _, w := range initWarnings {
		a.logf("Предупреждение: %s", w)
	}
	if a.cfg.Autostart {
		a.safeGo(func() { _ = setAutostart(a.root, true) }) // keep the task pointing at this exe
	}

	if a.startHidden {
		a.logf("Автозапуск: включаю «%s».", a.currentStrategy().Name)
		a.onStart()
	} else {
		showWindow(a.hwnd, swShow)
		a.logf("Выберите стратегию и нажмите «Запустить» — или «Подобрать стратегию».")
	}

	a.startupUpdateCheck()

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if ok, _, _ := procIsDialogMessageW.Call(a.hwnd, uintptr(unsafe.Pointer(&m))); ok != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func registerMessage(name string) uintptr {
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(utf16p(name))))
	return r
}

func (a *App) child(class, text string, style, ex uint32, x, y, w, h int, id, font uintptr) uintptr {
	hw := createWindowEx(ex, class, text, style|wsChild|wsVisible, a.s(x), a.s(y), a.s(w), a.s(h), a.hwnd, id, a.hInst)
	sendMsg(hw, wmSetFont, font, 1)
	return hw
}

func (a *App) createControls() {
	const off = 34 // room for the tab bar
	main := func(h uintptr) uintptr { a.pageMain = append(a.pageMain, h); return h }
	adv := func(h uintptr) uintptr { a.pageAdv = append(a.pageAdv, h); return h }
	advBtn := func(h uintptr) uintptr { a.advInteractive = append(a.advInteractive, h); return adv(h) }
	btn := uint32(bsOwnerDraw | wsTabStop)
	combo := uint32(cbsDropDownList | wsVScroll | wsTabStop)

	a.child("STATIC", "Zapret · Discord и YouTube", ssCenterImage, 0, 20, 14, 340, 32, 0, a.fontTitle)
	a.bTheme = a.child("BUTTON", "", btn, 0, 370, 16, 110, 28, idTheme, a.fontNormal)
	a.bTabMain = a.child("BUTTON", "Основное", btn, 0, 20, 50, 120, 26, idTabMain, a.fontNormal)
	a.bTabAdv = a.child("BUTTON", "Дополнительно", btn, 0, 146, 50, 140, 26, idTabAdv, a.fontNormal)

	// ---- page 1: main ----
	main(a.child("STATIC", "Стратегия", ssCenterImage, 0, 20, 62+off, 140, 24, 0, a.fontNormal))
	a.cStrategy = main(a.child("COMBOBOX", "", combo, 0, 170, 62+off, 310, 300, idStrategy, a.fontNormal))
	main(a.child("STATIC", "Игровой фильтр", ssCenterImage, 0, 20, 96+off, 140, 24, 0, a.fontNormal))
	a.cGame = main(a.child("COMBOBOX", "", combo, 0, 170, 96+off, 310, 200, idGame, a.fontNormal))
	main(a.child("STATIC", "Список IP (ipset)", ssCenterImage, 0, 20, 130+off, 140, 24, 0, a.fontNormal))
	a.cIPSet = main(a.child("COMBOBOX", "", combo, 0, 170, 130+off, 310, 200, idIPSet, a.fontNormal))

	a.lStatus = main(a.child("STATIC", "", ssCenterImage, 0, 20, 174+off, 460, 32, 0, a.fontBold))

	a.bStart = main(a.child("BUTTON", "Запустить", btn, 0, 20, 214+off, 145, 36, idStart, a.fontNormal))
	a.bStop = main(a.child("BUTTON", "Остановить", btn, 0, 175, 214+off, 145, 36, idStop, a.fontNormal))
	a.bAuto = main(a.child("BUTTON", "Подобрать стратегию", btn, 0, 330, 214+off, 150, 36, idAuto, a.fontNormal))

	a.cAutostart = main(a.child("BUTTON", "Запускать вместе с Windows (в трее, защита включается сама)", btn, 0, 20, 262+off, 460, 24, idAutostart, a.fontNormal))

	// ---- page 2: advanced (same area, hidden until its tab is chosen) ----
	const base = 62 + off
	adv(a.child("STATIC", "Fake для Discord (UDP)", ssCenterImage, 0, 20, base, 150, 24, 0, a.fontNormal))
	a.cFakeDiscord = advBtn(a.child("COMBOBOX", "", combo, 0, 170, base, 310, 200, idFakeDiscord, a.fontNormal))
	adv(a.child("STATIC", "Fake для игр (UDP)", ssCenterImage, 0, 20, base+32, 150, 24, 0, a.fontNormal))
	a.cFakeGame = advBtn(a.child("COMBOBOX", "", combo, 0, 170, base+32, 310, 200, idFakeGame, a.fontNormal))
	a.cCheckUpd = advBtn(a.child("BUTTON", "Проверять обновления при запуске (только сообщить, без установки)", btn, 0, 20, base+64, 460, 24, idCheckUpd, a.fontNormal))
	a.lService = adv(a.child("STATIC", "Служба Windows: проверяю…", ssCenterImage, 0, 20, base+94, 460, 22, 0, a.fontNormal))
	a.bSvcInstall = advBtn(a.child("BUTTON", "Установить службу", btn, 0, 20, base+118, 145, 30, idSvcInstall, a.fontNormal))
	a.bSvcRemove = advBtn(a.child("BUTTON", "Удалить службу", btn, 0, 175, base+118, 145, 30, idSvcRemove, a.fontNormal))
	a.bDiag = advBtn(a.child("BUTTON", "Диагностика", btn, 0, 330, base+118, 150, 30, idDiag, a.fontNormal))
	a.bIPSetUpdate = advBtn(a.child("BUTTON", "Обновить список IP", btn, 0, 20, base+154, 145, 30, idIPSetUpdate, a.fontNormal))
	a.bHostsApply = advBtn(a.child("BUTTON", "Обновить hosts", btn, 0, 175, base+154, 145, 30, idHostsApply, a.fontNormal))
	a.bHostsClear = advBtn(a.child("BUTTON", "Очистить hosts", btn, 0, 330, base+154, 150, 30, idHostsClear, a.fontNormal))
	a.bDiscordCache = advBtn(a.child("BUTTON", "Очистить кэш Discord", btn, 0, 20, base+190, 145, 30, idDiscordCache, a.fontNormal))
	for _, h := range a.pageAdv {
		showWindow(h, swHide)
	}

	// ---- shared ----
	a.eLog = a.child("EDIT", "", esMultiline|esAutoVScroll|esReadOnly|wsVScroll|wsTabStop, wsExClientEdge, 20, 296+off, 460, 108, idLog, a.fontNormal)
	sendMsg(a.eLog, emSetLimitText, 200000, 0)

	a.bFolder = a.child("BUTTON", "Папка со списками", btn, 0, 20, 414+off, 170, 30, idFolder, a.fontNormal)
	a.bUpdate = a.child("BUTTON", "Проверить обновления", btn, 0, 200, 414+off, 160, 30, idUpdate, a.fontNormal)
	a.bExit = a.child("BUTTON", "Выход", btn, 0, 370, 414+off, 110, 30, idExit, a.fontNormal)

	a.lFooter = a.child("STATIC", fmt.Sprintf("Zapret GUI %s · основан на Flowseal/zapret-discord-youtube %s", appVersion, installedVersion(a.root)), ssCenter, 0, 20, 450+off, 460, 16, 0, a.fontSmall)

	a.checks = map[uintptr]*bool{a.cAutostart: &a.autoChecked, a.cCheckUpd: &a.updChecked}
}

// showPage switches between the "Основное" and "Дополнительно" tabs.
func (a *App) showPage(n int) {
	a.page = n
	for i, group := range [][]uintptr{a.pageMain, a.pageAdv} {
		cmd := swHide
		if i == n {
			cmd = swShow
		}
		for _, h := range group {
			showWindow(h, cmd)
		}
	}
	procInvalidateRect.Call(a.bTabMain, 0, 1)
	procInvalidateRect.Call(a.bTabAdv, 0, 1)
	if n == 1 {
		a.refreshService()
	}
}

func (a *App) fillControls() {
	for _, s := range a.strategies {
		sendMsgStr(a.cStrategy, cbAddString, 0, s.Name)
	}
	for _, l := range gameLabels {
		sendMsgStr(a.cGame, cbAddString, 0, l)
	}
	for _, l := range ipsetLabels {
		sendMsgStr(a.cIPSet, cbAddString, 0, l)
	}
	sel := 0
	for i, s := range a.strategies {
		if s.Name == a.cfg.Strategy {
			sel = i
		}
	}
	sendMsg(a.cStrategy, cbSetCurSel, uintptr(sel), 0)
	sendMsg(a.cGame, cbSetCurSel, uintptr(max(indexOf(gameModes, a.cfg.GameFilter), 0)), 0)
	sendMsg(a.cIPSet, cbSetCurSel, uintptr(max(indexOf(ipsetModes, a.cfg.IPSet), 0)), 0)
	a.autoChecked = a.cfg.Autostart
	a.updChecked = a.cfg.CheckUpdates
	a.fillFakeCombos()
}

// ---- window procedure ---------------------------------------------------------------

func (a *App) wndProc(hwnd, m, wparam, lparam uintptr) (ret uintptr) {
	defer func() {
		if r := recover(); r != nil {
			writeCrash(a.root, r)
			ret = 0
		}
	}()

	switch uint32(m) {
	case wmCommand:
		a.onCommand(wparam, lparam)
		return 0
	case wmCtlColorStatic:
		if b := a.ctlColor(wparam, lparam, false); b != 0 {
			return b
		}
	case wmCtlColorEdit, wmCtlColorListbox:
		if b := a.ctlColor(wparam, lparam, true); b != 0 {
			return b
		}
	case wmEraseBkgnd:
		if a.eraseBackground(wparam) != 0 {
			return 1
		}
	case wmDrawItem:
		if a.onDrawItem(lparam) != 0 {
			return 1
		}
	case wmSettingChange:
		a.onSystemThemeChanged()
	case wmAppLog:
		a.flushLog()
		return 0
	case wmAppState:
		a.refresh()
		return 0
	case wmAppSelect:
		sendMsg(a.cStrategy, cbSetCurSel, wparam, 0)
		a.readSettings()
		return 0
	case wmAppReload:
		a.applyReload()
		return 0
	case wmAppSvc:
		a.applyServiceLabel()
		return 0
	case wmAppBalloon:
		a.mu.Lock()
		text := a.balloonText
		a.mu.Unlock()
		if !isVisible(a.hwnd) {
			a.balloon("Zapret GUI", text)
		}
		return 0
	case wmAppAutoFail:
		a.autoChecked = false
		procInvalidateRect.Call(a.cAutostart, 0, 1)
		a.readSettings()
		return 0
	case wmAppShow:
		a.showWindow()
		return 0
	case wmAppQuit:
		a.trayRemove()
		procDestroyWindow.Call(a.hwnd)
		return 0
	case wmTray:
		a.onTray(lparam)
		return 0
	case wmClose:
		if !a.quitting {
			a.hideToTray()
			return 0
		}
	case wmQueryEndSession:
		return 1
	case wmEndSession:
		if wparam != 0 {
			a.ctl.Stop()
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	if a.taskbarCreated != 0 && uint32(m) == a.taskbarCreated {
		a.trayAdd()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, m, wparam, lparam)
	return r
}

func (a *App) onCommand(wparam, lparam uintptr) {
	id := int(wparam & 0xFFFF)
	code := int((wparam >> 16) & 0xFFFF)
	switch id {
	case idStart:
		a.onStart()
	case idStop:
		a.onStop()
	case idAuto:
		a.onAuto()
	case idAutostart:
		a.autoChecked = !a.autoChecked
		procInvalidateRect.Call(a.cAutostart, 0, 1)
		a.onAutostartToggle()
	case idTheme:
		a.onThemeButton()
	case idTabMain:
		a.showPage(0)
	case idTabAdv:
		a.showPage(1)
	case idCheckUpd:
		a.updChecked = !a.updChecked
		procInvalidateRect.Call(a.cCheckUpd, 0, 1)
		a.readSettings()
	case idSvcInstall:
		a.onServiceInstall()
	case idSvcRemove:
		a.onServiceRemove()
	case idDiag:
		a.onDiagnostics()
	case idIPSetUpdate:
		a.onIPSetUpdate()
	case idHostsApply:
		a.onHostsUpdate()
	case idHostsClear:
		a.onHostsClear()
	case idDiscordCache:
		a.onDiscordCache()
	case idUpdate:
		a.onUpdate()
	case idFolder:
		shellExecute(a.hwnd, "open", filepath.Join(a.root, "lists"))
	case idExit, idTrayExit:
		a.quit()
	case idTrayShow:
		a.showWindow()
	case idTrayToggle:
		if a.ctl.Running() {
			a.onStop()
		} else {
			a.onStart()
		}
	case idStrategy, idGame, idIPSet, idFakeDiscord, idFakeGame:
		if code == cbnSelChange {
			a.onSettingsChanged()
		}
	}
}

// ---- actions ------------------------------------------------------------------------

func (a *App) readSettings() {
	if i := int(int32(sendMsg(a.cStrategy, cbGetCurSel, 0, 0))); i >= 0 && i < len(a.strategies) {
		a.cfg.Strategy = a.strategies[i].Name
	}
	if i := int(int32(sendMsg(a.cGame, cbGetCurSel, 0, 0))); i >= 0 && i < len(gameModes) {
		a.cfg.GameFilter = gameModes[i]
	}
	if i := int(int32(sendMsg(a.cIPSet, cbGetCurSel, 0, 0))); i >= 0 && i < len(ipsetModes) {
		a.cfg.IPSet = ipsetModes[i]
	}
	a.cfg.Autostart = a.autoChecked
	a.cfg.CheckUpdates = a.updChecked
	a.cfg.DiscordFake = a.readFakeCombo(a.cFakeDiscord)
	a.cfg.GameFake = a.readFakeCombo(a.cFakeGame)
	_ = saveConfig(a.cfgPath, a.cfg)
}

func (a *App) currentStrategy() Strategy {
	for _, s := range a.strategies {
		if s.Name == a.cfg.Strategy {
			return s
		}
	}
	return a.strategies[0]
}

func (a *App) onSettingsChanged() {
	a.readSettings()
	if a.ctl.Running() && atomic.LoadInt32(&a.state) == stateIdle {
		a.logf("Настройки изменены — перезапускаю.")
		a.onStart()
	}
}

func (a *App) onStart() {
	if !atomic.CompareAndSwapInt32(&a.state, stateIdle, stateBusy) {
		return
	}
	a.readSettings()
	s, game, ipset := a.currentStrategy(), a.cfg.GameFilter, a.cfg.IPSet
	s = a.effective(s, a.cfg)
	a.refresh()
	a.safeGo(func() {
		a.logf("Запуск: %s …", s.Name)
		if err := a.ctl.Start(s, game, ipset); err != nil {
			a.logf("Ошибка: %v", err)
			a.logf("Если ошибка про драйвер или доступ — проверьте, что антивирус не удалил файлы в %s.", a.root)
		} else {
			a.logf("Работает: %s.", s.Name)
		}
		atomic.StoreInt32(&a.state, stateIdle)
		a.post(wmAppState, 0, 0)
	})
}

func (a *App) onStop() {
	if !atomic.CompareAndSwapInt32(&a.state, stateIdle, stateBusy) {
		return
	}
	a.refresh()
	a.safeGo(func() {
		a.ctl.Stop()
		a.logf("Остановлено.")
		atomic.StoreInt32(&a.state, stateIdle)
		a.post(wmAppState, 0, 0)
	})
}

func (a *App) onAutostartToggle() {
	a.readSettings()
	enableTask := a.cfg.Autostart
	a.safeGo(func() {
		if err := setAutostart(a.root, enableTask); err != nil {
			a.logf("Автозапуск не настроен: %v", err)
			if enableTask {
				a.post(wmAppAutoFail, 0, 0)
			}
			return
		}
		if enableTask {
			a.logf("Автозапуск включён: программа стартует при входе в Windows.")
		} else {
			a.logf("Автозапуск выключен.")
		}
	})
}

func (a *App) onAuto() {
	if atomic.LoadInt32(&a.state) == stateAuto {
		a.mu.Lock()
		if a.autoCancel != nil {
			a.autoCancel()
		}
		a.mu.Unlock()
		return
	}
	if !atomic.CompareAndSwapInt32(&a.state, stateIdle, stateAuto) {
		return
	}
	a.readSettings()
	cfg := a.cfg
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.autoCancel = cancel
	a.mu.Unlock()
	a.refresh()
	a.safeGo(func() {
		a.runAutoSelect(ctx, cfg)
		cancel()
		atomic.StoreInt32(&a.state, stateIdle)
		a.post(wmAppState, 0, 0)
	})
}

// onUpdate checks the repository and, if a newer version exists, downloads,
// validates and installs it in one go. A running protection is restarted.
func (a *App) onUpdate() {
	if !atomic.CompareAndSwapInt32(&a.state, stateIdle, stateBusy) {
		return
	}
	a.readSettings()
	cfg := a.cfg
	a.refresh()
	a.safeGo(func() {
		defer func() {
			atomic.StoreInt32(&a.state, stateIdle)
			a.post(wmAppState, 0, 0)
		}()
		a.runUpdate(cfg)
	})
}

func (a *App) runUpdate(cfg Config) {
	ctx := context.Background()
	cur := installedVersion(a.root)
	a.logf("Проверяю обновления (установлена версия %s)…", cur)

	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		a.logf("Не удалось проверить обновления: %s", firstLine(err))
		return
	}
	if !versionNewer(latest, cur) {
		a.logf("Обновлений нет: установлена последняя версия (%s).", cur)
		return
	}
	a.logf("Найдена версия %s — скачиваю…", latest)

	p, err := downloadUpdate(ctx)
	if err != nil {
		a.logf("Не удалось скачать обновление: %s", firstLine(err))
		return
	}
	if !versionNewer(p.Version, cur) {
		a.logf("Скачанный архив содержит версию %s — это не новее установленной, пропускаю.", p.Version)
		return
	}
	if atomic.LoadInt32(&a.quitFlag) != 0 {
		return
	}

	wasRunning, name := a.ctl.Running(), a.ctl.Current()
	a.logf("Устанавливаю %s (%d стратегий)…", p.Version, len(p.Strategies))
	// winws.exe and the driver lock their files: stop the Windows service as well and bring it back after
	if svcWas := serviceActive(); svcWas {
		_ = runHidden("sc", "stop", serviceName)
		time.Sleep(700 * time.Millisecond)
		killWinws()
		removeDriver()
		defer func() {
			_ = runHidden("sc", "start", serviceName)
			a.refreshService()
		}()
	}
	a.ctl.Stop()

	if err := installUpdate(a.root, p); err != nil {
		a.logf("Ошибка установки: %s", firstLine(err))
		if _, e := ensureAssets(a.root); e != nil {
			a.logf("Не удалось восстановить встроенные файлы: %s", firstLine(e))
		} else {
			a.logf("Восстановлены встроенные файлы версии %s.", embeddedVersion())
		}
		return
	}
	a.mu.Lock()
	a.reload = p.Strategies
	a.mu.Unlock()
	a.post(wmAppReload, 0, 0)
	a.logf("Обновлено до версии %s.", p.Version)

	if !wasRunning || atomic.LoadInt32(&a.quitFlag) != 0 {
		return
	}
	s := p.Strategies[0]
	for _, c := range p.Strategies {
		if c.Name == name {
			s = c
		}
	}
	if s.Name != name {
		a.logf("Стратегии «%s» в новой версии нет, включаю «%s».", name, s.Name)
	}
	if err := a.ctl.Start(a.effective(s, cfg), cfg.GameFilter, cfg.IPSet); err != nil {
		a.logf("Ошибка запуска после обновления: %s", firstLine(err))
		return
	}
	a.logf("Работает: %s.", s.Name)
}

// applyReload swaps the strategy list after an update (UI thread only).
func (a *App) applyReload() {
	a.mu.Lock()
	list := a.reload
	a.reload = nil
	a.mu.Unlock()
	if len(list) == 0 {
		return
	}
	a.strategies = list
	sendMsg(a.cStrategy, cbResetContent, 0, 0)
	sel := 0
	for i, s := range list {
		sendMsgStr(a.cStrategy, cbAddString, 0, s.Name)
		if s.Name == a.cfg.Strategy {
			sel = i
		}
	}
	sendMsg(a.cStrategy, cbSetCurSel, uintptr(sel), 0)
	a.fillFakeCombos()
	a.readSettings()
	setText(a.lFooter, fmt.Sprintf("Zapret GUI %s · основан на Flowseal/zapret-discord-youtube %s", appVersion, installedVersion(a.root)))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func firstLine(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

// runAutoSelect tries the strategies one by one and keeps the first one
// that opens every Discord and YouTube endpoint.
func (a *App) runAutoSelect(ctx context.Context, cfg Config) {
	game, ipset := cfg.GameFilter, cfg.IPSet
	if serviceActive() {
		a.logf("Работает служба «%s» — подбор невозможен. Удалите службу на вкладке «Дополнительно».", serviceName)
		return
	}
	a.logf("Подбор стратегии: сначала проверяю доступ без обхода…")
	a.ctl.Stop()
	base := probe(ctx, probeTargets)
	if ctx.Err() != nil {
		a.logf("Подбор отменён.")
		return
	}
	if base.AllOK() {
		a.logf("Все сервисы и так открываются без обхода — подбор не нужен. (Включён VPN или блокировок нет.)")
		return
	}
	a.logf("Без обхода недоступно: %s.", strings.Join(base.Failed, ", "))

	best, bestOK := -1, 0
	for i, s := range a.strategies {
		if ctx.Err() != nil {
			break
		}
		a.logf("[%d/%d] %s", i+1, len(a.strategies), s.Name)
		if err := a.ctl.Start(a.effective(s, cfg), game, ipset); err != nil {
			a.logf("    не запустилась: %s", firstLine(err))
			continue
		}
		if !sleepCtx(ctx, 2*time.Second) {
			break
		}
		res := probe(ctx, probeTargets)
		if ctx.Err() != nil {
			break
		}
		a.logf("    доступно %d из %d", res.OK, res.Total)
		if res.OK > bestOK {
			best, bestOK = i, res.OK
		}
		if res.AllOK() {
			best = i
			break
		}
	}

	if ctx.Err() != nil {
		a.ctl.Stop()
		a.logf("Подбор отменён.")
		return
	}
	if best < 0 {
		a.ctl.Stop()
		a.logf("Ни одна стратегия не помогла. Проверьте, что DNS не подменяется провайдером (включите DNS-over-HTTPS), и попробуйте ещё раз.")
		return
	}
	if a.ctl.Current() != a.strategies[best].Name {
		if err := a.ctl.Start(a.effective(a.strategies[best], cfg), game, ipset); err != nil {
			a.logf("Не удалось запустить лучшую стратегию: %s", firstLine(err))
			return
		}
	}
	a.logf("Выбрана стратегия «%s» — доступно %d из %d.", a.strategies[best].Name, bestOK, len(probeTargets))
	a.post(wmAppSelect, uintptr(best), 0)
}

// ---- quitting -----------------------------------------------------------------------

func (a *App) quit() {
	if a.quitting {
		return
	}
	a.quitting = true
	atomic.StoreInt32(&a.quitFlag, 1)
	for _, h := range []uintptr{a.bStart, a.bStop, a.bAuto, a.bFolder, a.bUpdate, a.bTheme, a.bExit, a.cStrategy, a.cGame, a.cIPSet, a.cAutostart} {
		enable(h, false)
	}
	for _, h := range a.advInteractive {
		enable(h, false)
	}
	setText(a.lStatus, "● Завершение…")
	a.mu.Lock()
	if a.autoCancel != nil {
		a.autoCancel()
	}
	a.mu.Unlock()
	go func() { // plain goroutine: must reach wmAppQuit even after a panic
		defer a.post(wmAppQuit, 0, 0)
		a.ctl.Stop()
	}()
}

func (a *App) showWindow() {
	showWindow(a.hwnd, swShow)
	showWindow(a.hwnd, swRestore)
	procSetForegroundWindow.Call(a.hwnd)
}

func (a *App) hideToTray() {
	showWindow(a.hwnd, swHide)
	if !a.balloonShown {
		a.balloonShown = true
		a.balloon("Zapret GUI продолжает работать", "Окно свёрнуто в трей. Чтобы выйти, нажмите правой кнопкой на значок и выберите «Выход».")
	}
}

// ---- refresh --------------------------------------------------------------------------

func (a *App) refresh() {
	running := a.ctl.Running()
	state := atomic.LoadInt32(&a.state)
	idle := state == stateIdle

	enable(a.bStart, idle && !running)
	enable(a.bStop, idle && running)
	enable(a.bAuto, state != stateBusy)
	enable(a.bUpdate, idle)
	for _, h := range append([]uintptr{a.cStrategy, a.cGame, a.cIPSet, a.cAutostart}, a.advInteractive...) {
		enable(h, idle)
	}
	if state == stateAuto {
		setText(a.bAuto, "Отменить подбор")
	} else {
		setText(a.bAuto, "Подобрать стратегию")
	}

	switch {
	case state == stateAuto:
		a.statusColor = a.pal.Warn
		setText(a.lStatus, "●  Идёт подбор стратегии…")
	case state == stateBusy:
		a.statusColor = a.pal.Warn
		setText(a.lStatus, "●  Подождите…")
	case running:
		a.statusColor = a.pal.OK
		setText(a.lStatus, "●  Работает · "+a.ctl.Current())
	default:
		a.statusColor = a.pal.Idle
		setText(a.lStatus, "●  Остановлено")
	}
	procInvalidateRect.Call(a.lStatus, 0, 1)
	a.trayUpdate()
}

// ---- log ------------------------------------------------------------------------------

func (a *App) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
	a.logMu.Lock()
	a.pending = append(a.pending, line)
	a.logMu.Unlock()
	a.post(wmAppLog, 0, 0)
}

func (a *App) post(m uint32, w, l uintptr) {
	if a.hwnd != 0 {
		postMsg(a.hwnd, m, w, l)
	}
}

func (a *App) flushLog() {
	a.logMu.Lock()
	lines := a.pending
	a.pending = nil
	a.logMu.Unlock()
	for _, l := range lines {
		if a.logLines >= 400 {
			setText(a.eLog, "")
			a.logLines = 0
		}
		sendMsg(a.eLog, emSetSel, ^uintptr(0), ^uintptr(0))
		sendMsgStr(a.eLog, emReplaceSel, 0, l+"\r\n")
		a.logLines++
	}
	sendMsg(a.eLog, emScrollCaret, 0, 0)
}

func (a *App) safeGo(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				writeCrash(a.root, r)
				a.logf("Внутренняя ошибка: %v (подробности в crash.log)", r)
				atomic.StoreInt32(&a.state, stateIdle)
				a.post(wmAppState, 0, 0)
			}
		}()
		f()
	}()
}

// ---- tray -----------------------------------------------------------------------------

func (a *App) trayData(flags uint32) notifyIconData {
	var nid notifyIconData
	nid.Size = uint32(unsafe.Sizeof(nid))
	nid.Wnd = a.hwnd
	nid.ID = 1
	nid.Flags = flags
	nid.CallbackMessage = wmTray
	nid.Icon = a.icoOff
	tip := "Zapret GUI — остановлен"
	if a.ctl.Running() {
		nid.Icon = a.icoOn
		tip = "Zapret GUI — работает: " + a.ctl.Current()
	}
	copyUTF16(nid.Tip[:], tip)
	return nid
}

func (a *App) trayAdd() {
	nid := a.trayData(nifMessage | nifIcon | nifTip)
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func (a *App) trayUpdate() {
	nid := a.trayData(nifMessage | nifIcon | nifTip)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (a *App) trayRemove() {
	nid := a.trayData(0)
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (a *App) balloon(title, text string) {
	nid := a.trayData(nifInfo)
	copyUTF16(nid.InfoTitle[:], title)
	copyUTF16(nid.Info[:], text)
	nid.InfoFlags = niifInfo
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (a *App) onTray(lparam uintptr) {
	switch uint32(lparam & 0xFFFF) {
	case wmLButtonUp:
		if isVisible(a.hwnd) {
			showWindow(a.hwnd, swHide)
		} else {
			a.showWindow()
		}
	case wmLButtonDblClk:
		a.showWindow()
	case wmRButtonUp:
		a.trayMenu()
	}
}

func (a *App) trayMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	toggle := "Запустить"
	if a.ctl.Running() {
		toggle = "Остановить"
	}
	add := func(flags, id uintptr, text string) {
		procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(utf16p(text))))
	}
	add(mfString, idTrayShow, "Открыть окно")
	add(mfString, idTrayToggle, toggle)
	add(mfSeparator, 0, "")
	add(mfString, idTrayExit, "Выход")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(a.hwnd) // required, otherwise the menu does not close on outside click
	procTrackPopupMenu.Call(menu, tpmRightButton|tpmBottomAlign, uintptr(pt.X), uintptr(pt.Y), 0, a.hwnd, 0)
	postMsg(a.hwnd, wmNull, 0, 0)
}
