//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

// runHidden runs a helper tool without flashing a console window.
func runHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd.Run()
}

func killWinws() {
	_ = runHidden("taskkill", "/F", "/IM", "winws.exe")
}

// removeDriver mirrors :service_remove of service.bat: after winws.exe is gone
// the WinDivert driver service must be stopped and deleted, otherwise the
// driver file stays locked and the next start may fail.
func removeDriver() {
	for _, name := range []string{"WinDivert", "WinDivert14"} {
		_ = runHidden("sc", "stop", name)
		time.Sleep(250 * time.Millisecond)
		_ = runHidden("sc", "delete", name)
	}
}

// Controller owns the winws.exe process.
type Controller struct {
	binDir, listsDir, logPath string

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	stopping bool
	current  string

	// onExit is called when winws.exe dies without being asked to.
	onExit func(strategy string, err error, logTail string)
}

func newController(root string) *Controller {
	return &Controller{
		binDir:   filepath.Join(root, "bin"),
		listsDir: filepath.Join(root, "lists"),
		logPath:  filepath.Join(root, "winws.log"),
	}
}

func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd != nil
}

func (c *Controller) Current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Start (re)launches winws.exe with the given strategy.
func (c *Controller) Start(s Strategy, gameMode, ipsetMode string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Never kill the winws.exe of the Windows service: two instances fight for the driver.
	if serviceActive() {
		return fmt.Errorf("уже работает служба «%s» — удалите её на вкладке «Дополнительно», затем запускайте из окна", serviceName)
	}

	c.stopLocked()
	killWinws()
	removeDriver()

	if err := writeIPSet(c.listsDir, ipsetMode); err != nil {
		return fmt.Errorf("не удалось записать список ipset: %w", err)
	}
	logFile, err := os.Create(c.logPath)
	if err != nil {
		return err
	}

	cmd := exec.Command(filepath.Join(c.binDir, "winws.exe"))
	cmd.Dir = c.binDir
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       buildCmdLine(s, c.binDir, c.listsDir, gameMode),
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("не удалось запустить winws.exe: %w", err)
	}

	done := make(chan struct{})
	c.cmd, c.done, c.stopping, c.current = cmd, done, false, s.Name
	go func() {
		err := cmd.Wait()
		logFile.Close()
		close(done)
		c.handleExit(cmd, err)
	}()

	// winws.exe exits at once when WinDivert cannot be loaded, the filter is
	// rejected or another instance owns the driver: report that as an error.
	select {
	case <-done:
		c.cmd, c.current = nil, ""
		tail := strings.TrimSpace(readTail(c.logPath, 700))
		if tail == "" {
			tail = "процесс завершился без сообщений (проверьте антивирус и права администратора)"
		}
		return fmt.Errorf("winws.exe сразу завершился: %s", tail)
	case <-time.After(1500 * time.Millisecond):
	}
	return nil
}

func (c *Controller) stopLocked() {
	if c.cmd == nil {
		return
	}
	c.stopping = true
	_ = c.cmd.Process.Kill()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
	}
	c.cmd, c.current = nil, ""
}

// Stop terminates winws.exe and unloads the driver.
func (c *Controller) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
	// The Windows service owns its own winws.exe and the driver: leave them alone,
	// otherwise closing this window would switch the protection off.
	if !serviceActive() {
		killWinws()
		removeDriver()
	}
}

func (c *Controller) handleExit(cmd *exec.Cmd, err error) {
	c.mu.Lock()
	if c.cmd != cmd { // already replaced or stopped on purpose
		c.mu.Unlock()
		return
	}
	name, stopping := c.current, c.stopping
	c.cmd, c.current = nil, ""
	c.mu.Unlock()

	if !serviceActive() {
		removeDriver()
	}
	if !stopping && c.onExit != nil {
		c.onExit(name, err, readTail(c.logPath, 500))
	}
}

func readTail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > n {
		data = data[len(data)-n:]
	}
	return string(data)
}

// ---- autostart via Task Scheduler ----------------------------------------------

// setAutostart registers (or removes) a logon task that runs elevated without
// a UAC prompt. The task is created from XML so that it also starts on
// battery power, which the plain schtasks switches cannot do.
func setAutostart(root string, enable bool) error {
	if !enable {
		_ = runHidden("schtasks", "/Delete", "/TN", autostartTask, "/F")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	userID := ""
	if u, err := user.Current(); err == nil {
		userID = u.Username
	}
	path := filepath.Join(root, "autostart-task.xml")
	if err := os.WriteFile(path, utf16LEWithBOM(autostartTaskXML(exe, userID)), 0o644); err != nil {
		return err
	}
	if err := runHidden("schtasks", "/Create", "/TN", autostartTask, "/XML", path, "/F"); err != nil {
		return fmt.Errorf("schtasks: %w", err)
	}
	return nil
}
