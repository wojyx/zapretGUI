//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

func init() {
	// Win32 windows belong to the thread that created them.
	runtime.LockOSThread()
}

func dataDir() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(base, "ZapretGUI")
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "ZapretGUI-data")
	}
	return `C:\ZapretGUI`
}

func main() {
	root := dataDir()
	_ = os.MkdirAll(root, 0o755)

	defer func() {
		if r := recover(); r != nil {
			writeCrash(root, r)
			messageBox(0, fmt.Sprintf("Критическая ошибка: %v\n\nПодробности: %s", r, filepath.Join(root, "crash.log")), "Zapret GUI", mbOK|mbIconError)
		}
	}()

	startHidden := false
	for _, arg := range os.Args[1:] {
		if arg == "--autostart" {
			startHidden = true
		}
	}

	// One instance only: a second launch just brings the first one forward.
	_, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(utf16p(`Global\ZapretGUI_SingleInstance`))))
	if errno, ok := err.(syscall.Errno); ok && errno == errorAlreadyExists {
		if h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(utf16p(windowClass))), 0); h != 0 {
			postMsg(h, wmAppShow, 0, 0)
		}
		return
	}

	strategies, err := loadStrategiesFrom(root)
	if err != nil {
		messageBox(0, err.Error(), "Zapret GUI", mbOK|mbIconError)
		return
	}

	warnings, err := ensureAssets(root)
	if err != nil {
		messageBox(0, fmt.Sprintf("%v\n\nВозможно, файлы удалил антивирус. Добавьте папку %s в исключения и запустите программу снова.", err, root), "Zapret GUI", mbOK|mbIconError)
		return
	}

	cfg := loadConfig(filepath.Join(root, "config.json"))
	app := newApp(root, cfg, strategies, startHidden)
	if err := app.run(warnings); err != nil {
		messageBox(0, err.Error(), "Zapret GUI", mbOK|mbIconError)
	}
}
