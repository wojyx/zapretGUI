//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Actions of the "Дополнительно" tab. Every action that takes time runs through runBusy,
// which keeps the rest of the window disabled until it is done.

const (
	wmAppSvc     = wmApp + 9
	wmAppBalloon = wmApp + 10
)

// effective applies the chosen fake packets to a strategy.
func (a *App) effective(s Strategy, cfg Config) Strategy {
	avail := listFakes(a.ctl.binDir)
	for _, name := range []string{cfg.DiscordFake, cfg.GameFake} {
		if err := fakeChoiceError(name, avail); err != nil {
			a.logf("%v", err)
		}
	}
	return withFakes(s, cfg.DiscordFake, cfg.GameFake, avail)
}

func (a *App) runBusy(f func()) {
	if !atomic.CompareAndSwapInt32(&a.state, stateIdle, stateBusy) {
		return
	}
	a.refresh()
	a.safeGo(func() {
		defer func() {
			atomic.StoreInt32(&a.state, stateIdle)
			a.post(wmAppState, 0, 0)
		}()
		f()
	})
}

func (a *App) logDiag(lines []DiagLine) {
	for _, l := range lines {
		a.logf("%s", l.String())
	}
}

func (a *App) confirm(text string) bool {
	return askYesNo(a.hwnd, text, "Zapret GUI")
}

// ---- fake packets ------------------------------------------------------------------------

func (a *App) fillFakeCombos() {
	a.fakeNames = listFakes(a.ctl.binDir)
	for _, c := range []uintptr{a.cFakeDiscord, a.cFakeGame} {
		sendMsg(c, cbResetContent, 0, 0)
		sendMsgStr(c, cbAddString, 0, "По умолчанию")
		for _, n := range a.fakeNames {
			sendMsgStr(c, cbAddString, 0, n)
		}
	}
	sel := func(name string) uintptr {
		if i := indexOf(a.fakeNames, name); i >= 0 {
			return uintptr(i + 1)
		}
		return 0
	}
	sendMsg(a.cFakeDiscord, cbSetCurSel, sel(a.cfg.DiscordFake), 0)
	sendMsg(a.cFakeGame, cbSetCurSel, sel(a.cfg.GameFake), 0)
}

func (a *App) readFakeCombo(c uintptr) string {
	i := int(int32(sendMsg(c, cbGetCurSel, 0, 0)))
	if i >= 1 && i-1 < len(a.fakeNames) {
		return a.fakeNames[i-1]
	}
	return ""
}

// ---- Windows service -------------------------------------------------------------------------

// refreshService asks the service manager off the UI thread and posts the result.
func (a *App) refreshService() {
	a.safeGo(func() {
		text, running := serviceDescription()
		a.mu.Lock()
		a.svcText, a.svcOK = text, running
		a.mu.Unlock()
		a.post(wmAppSvc, 0, 0)
	})
}

func (a *App) applyServiceLabel() {
	a.mu.Lock()
	text, ok := a.svcText, a.svcOK
	a.mu.Unlock()
	a.svcShownOK = ok
	if text == "" {
		text = "Служба Windows: проверяю…"
	}
	setText(a.lService, text)
	procInvalidateRect.Call(a.lService, 0, 1)
}

func (a *App) onServiceInstall() {
	a.readSettings()
	cfg := a.cfg
	s := a.effective(a.currentStrategy(), cfg)
	a.runBusy(func() {
		a.logf("Устанавливаю службу Windows: %s …", s.Name)
		if err := a.ctl.installService(s, cfg.GameFilter, cfg.IPSet); err != nil {
			a.logf("Ошибка: %s", firstLine(err))
		} else {
			a.logf("Служба «%s» установлена и работает. Защита включается при старте Windows, окно для этого не нужно.", serviceName)
			a.logf("Чтобы применить другую стратегию или настройки, нажмите «Установить службу» ещё раз.")
		}
		a.refreshService()
	})
}

func (a *App) onServiceRemove() {
	a.runBusy(func() {
		if scState(serviceName) == "" {
			a.logf("Служба «%s» не установлена.", serviceName)
		} else {
			a.logf("Удаляю службу «%s» …", serviceName)
			a.ctl.removeService()
			a.logf("Служба удалена.")
		}
		a.refreshService()
	})
}

// ---- lists -----------------------------------------------------------------------------------

func (a *App) onIPSetUpdate() {
	a.readSettings()
	cfg := a.cfg
	a.runBusy(func() {
		a.logf("Скачиваю актуальный список IP…")
		data, n, err := fetchIPSetList(context.Background())
		if err != nil {
			a.logf("Не удалось обновить список IP: %s", firstLine(err))
			return
		}
		if err := os.WriteFile(filepath.Join(a.root, ipsetDownloadedFile), data, 0o644); err != nil {
			a.logf("Не удалось сохранить список: %s", firstLine(err))
			return
		}
		a.logf("Список IP обновлён: %d записей.", n)
		if cfg.IPSet != "loaded" {
			a.logf("Сейчас режим ipset — «%s»; список применится, когда выберете «Загруженный список».", ipsetLabels[max(indexOf(ipsetModes, cfg.IPSet), 0)])
			return
		}
		if err := writeIPSet(a.ctl.listsDir, "loaded"); err != nil {
			a.logf("Не удалось применить список: %s", firstLine(err))
			return
		}
		a.applyListsToRunning(cfg)
	})
}

// applyListsToRunning restarts whatever is running so that it re-reads the lists.
func (a *App) applyListsToRunning(cfg Config) {
	switch {
	case a.ctl.Running():
		s := a.effective(a.currentStrategyFor(cfg), cfg)
		if err := a.ctl.Start(s, cfg.GameFilter, cfg.IPSet); err != nil {
			a.logf("Ошибка перезапуска: %s", firstLine(err))
		} else {
			a.logf("Перезапущено со свежим списком.")
		}
	case serviceActive():
		a.logf("Список записан. Чтобы служба его подхватила, нажмите «Установить службу» ещё раз.")
	}
}

func (a *App) currentStrategyFor(cfg Config) Strategy {
	for _, s := range a.strategies {
		if s.Name == cfg.Strategy {
			return s
		}
	}
	return a.strategies[0]
}

func (a *App) onHostsUpdate() {
	a.runBusy(func() {
		a.logf("Скачиваю записи для hosts…")
		entries, err := fetchHostsEntries(context.Background())
		if err != nil {
			a.logf("Не удалось получить записи hosts: %s", firstLine(err))
			return
		}
		msg := "Добавить в файл hosts блок Zapret GUI?\n\n" + summarizeHosts(entries) +
			"\n\nЭти записи направляют перечисленные имена на фиксированные адреса. Остальное содержимое hosts не изменится, " +
			"резервная копия — hosts.zapret-backup, убрать блок можно кнопкой «Очистить hosts»."
		if !a.confirm(msg) {
			a.logf("Обновление hosts отменено.")
			return
		}
		if err := applyHostsBlock(entries); err != nil {
			a.logf("Не удалось изменить hosts: %s", firstLine(err))
			return
		}
		a.logf("Файл hosts обновлён: %d записей.", len(entries))
	})
}

func (a *App) onHostsClear() {
	a.runBusy(func() {
		removed, err := removeHostsBlockFromFile()
		switch {
		case err != nil:
			a.logf("Не удалось изменить hosts: %s", firstLine(err))
		case !removed:
			a.logf("В hosts нет блока Zapret GUI — убирать нечего.")
		default:
			a.logf("Блок Zapret GUI удалён из hosts.")
		}
	})
}

// ---- Discord cache ---------------------------------------------------------------------------

func (a *App) onDiscordCache() {
	a.runBusy(func() {
		if !a.confirm("Закрыть Discord (Stable, PTB, Canary, Development) и очистить его кэш?\n\nУдаляются папки Cache, Code Cache и GPUCache. Вход в аккаунт и сообщения не затрагиваются.") {
			a.logf("Очистка кэша Discord отменена.")
			return
		}
		a.logDiag(clearDiscordCache())
	})
}

// ---- diagnostics -----------------------------------------------------------------------------

func (a *App) onDiagnostics() {
	a.runBusy(a.runDiagnostics)
}

// runDiagnostics is the port of :service_diagnostics of service.bat.
func (a *App) runDiagnostics() {
	a.logf("— Диагностика —")
	a.logDiag(pathChecks(a.root, os.Getenv("OneDrive")))

	if scState("BFE") == "RUNNING" {
		a.logDiag([]DiagLine{{DiagOK, "Base Filtering Engine работает"}})
	} else {
		a.logDiag([]DiagLine{{DiagBad, "Base Filtering Engine не запущена — без неё zapret не работает"}})
	}
	if on, srv := systemProxy(); on {
		a.logDiag([]DiagLine{{DiagWarn, "Включён системный прокси: " + srv + ". Убедитесь, что он нужен"}})
	} else {
		a.logDiag([]DiagLine{{DiagOK, "Системный прокси не используется"}})
	}
	if err := runHidden("netsh", "interface", "tcp", "set", "global", "timestamps=enabled"); err != nil {
		a.logDiag([]DiagLine{{DiagBad, "Не удалось включить метки времени TCP"}})
	} else {
		a.logDiag([]DiagLine{{DiagOK, "Метки времени TCP включены"}})
	}

	procs := runningProcesses()
	a.logDiag(processChecks(procs))
	a.logDiag(serviceChecks(listRunningServices()))

	if !exists(filepath.Join(a.ctl.binDir, "WinDivert64.sys")) {
		a.logDiag([]DiagLine{{DiagBad, "Нет файла WinDivert64.sys — антивирус мог его удалить"}})
	}
	if secureDNSConfigured() {
		a.logDiag([]DiagLine{{DiagOK, "Защищённый DNS (DoH) настроен"}})
	} else {
		a.logDiag([]DiagLine{{DiagWarn, "Защищённый DNS не найден. Подмена DNS провайдером — частая причина, почему обход не помогает: включите DoH в браузере или в настройках Windows 11"}})
	}

	if hosts, err := readHostsFile(); err == nil {
		if hostsMentionsYouTube(hosts) {
			a.logDiag([]DiagLine{{DiagWarn, "В hosts есть записи youtube.com / youtu.be — они могут мешать доступу к YouTube"}})
		}
		if n := hostsBlockEntries(hosts); n > 0 {
			a.logDiag([]DiagLine{{DiagInfo, fmt.Sprintf("В hosts есть блок Zapret GUI (%d записей)", n)}})
		}
	}

	// A WinDivert driver left behind by a dead bypass blocks the next start.
	winwsRunning := a.ctl.Running() || procs["winws.exe"]
	if st := scState("WinDivert"); !winwsRunning && (st == "RUNNING" || st == "STOP_PENDING") {
		a.logDiag([]DiagLine{{DiagWarn, "winws.exe не запущен, но драйвер WinDivert активен — удаляю"}})
		removeDriver()
		if scState("WinDivert") == "" {
			a.logDiag([]DiagLine{{DiagOK, "Драйвер WinDivert удалён"}})
		} else {
			a.logDiag([]DiagLine{{DiagBad, "Не удалось удалить WinDivert — его мог занять другой обход"}})
		}
	}

	var others []string
	for _, n := range otherBypassServices {
		if scState(n) != "" {
			others = append(others, n)
		}
	}
	if len(others) == 0 {
		a.logDiag([]DiagLine{{DiagOK, "Служб других обходов не найдено"}})
	} else {
		a.logDiag([]DiagLine{{DiagBad, "Найдены службы других обходов: " + strings.Join(others, ", ")}})
		if a.confirm("Найдены службы других обходов блокировок:\n\n" + strings.Join(others, "\n") + "\n\nОни конфликтуют с zapret. Остановить и удалить их?") {
			for _, n := range others {
				_ = runHidden("sc", "stop", n)
				time.Sleep(400 * time.Millisecond)
				if err := runHidden("sc", "delete", n); err != nil {
					a.logDiag([]DiagLine{{DiagBad, "Не удалось удалить службу " + n}})
				} else {
					a.logDiag([]DiagLine{{DiagOK, "Служба " + n + " удалена"}})
				}
			}
			removeDriver()
		}
	}

	text, _ := serviceDescription()
	a.logDiag([]DiagLine{{DiagInfo, text}})
	a.logf("— Диагностика завершена —")
}

// ---- update check at start ---------------------------------------------------------------------

// startupUpdateCheck only tells that a newer version exists; installing stays a deliberate click.
func (a *App) startupUpdateCheck() {
	if !a.cfg.CheckUpdates {
		return
	}
	a.safeGo(func() {
		time.Sleep(3 * time.Second) // let the network come up after logon
		latest, err := fetchLatestVersion(context.Background())
		if err != nil {
			return // a failed check must never get in the way
		}
		cur := installedVersion(a.root)
		if versionNewer(latest, cur) {
			a.logf("Доступна новая версия zapret-discord-youtube: %s (сейчас %s). Нажмите «Проверить обновления».", latest, cur)
			a.mu.Lock()
			a.balloonText = fmt.Sprintf("Доступна версия %s (сейчас %s). Откройте окно и нажмите «Проверить обновления».", latest, cur)
			a.mu.Unlock()
			a.post(wmAppBalloon, 0, 0)
		}
	})
}
