//go:build windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const serviceName = "zapret" // same name as service.bat, so both tools see the same service

// runOutput runs a helper tool hidden and returns its combined output.
func runOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// ---- services -------------------------------------------------------------------------

func scState(name string) string {
	out, _ := runOutput("sc", "query", name)
	return parseScState(out)
}

func serviceActive() bool {
	st := scState(serviceName)
	return st == "RUNNING" || st == "START_PENDING"
}

func listRunningServices() []svcInfo {
	out, _ := runOutput("sc", "query")
	return parseScQuery(out)
}

func runningProcesses() map[string]bool {
	out, _ := runOutput("tasklist", "/FO", "CSV", "/NH")
	return parseTasklist(out)
}

// serviceStrategy returns the strategy name stored at install time ("" if unknown).
func serviceStrategy() string {
	out, err := runOutput("reg", "query", `HKLM\System\CurrentControlSet\Services\`+serviceName, "/v", "zapret-discord-youtube")
	if err != nil {
		return ""
	}
	if i := strings.Index(out, "REG_SZ"); i >= 0 {
		return strings.TrimSpace(out[i+len("REG_SZ"):])
	}
	return ""
}

// serviceDescription is the one-line status shown on the Advanced tab.
func serviceDescription() (text string, running bool) {
	st := scState(serviceName)
	switch st {
	case "":
		return "Служба Windows: не установлена", false
	case "RUNNING", "START_PENDING":
		if n := serviceStrategy(); n != "" {
			return "Служба Windows: работает · " + n, true
		}
		return "Служба Windows: работает", true
	case "STOP_PENDING":
		return "Служба Windows: останавливается (возможен конфликт — запустите диагностику)", false
	}
	return "Служба Windows: установлена, остановлена", false
}

// installService mirrors :service_install of service.bat: it registers winws.exe with the
// chosen arguments as an auto-start service, so protection works before anyone logs in.
func (c *Controller) installService(s Strategy, gameMode, ipsetMode string) error {
	c.Stop() // our own child would fight the service for the driver
	killWinws()

	_ = runHidden("netsh", "interface", "tcp", "set", "global", "timestamps=enabled")
	if err := writeIPSet(c.listsDir, ipsetMode); err != nil {
		return fmt.Errorf("не удалось записать список ipset: %w", err)
	}

	_ = runHidden("sc", "stop", serviceName)
	_ = runHidden("sc", "delete", serviceName)
	time.Sleep(600 * time.Millisecond)

	cmdLine := buildCmdLine(s, c.binDir, c.listsDir, gameMode)
	if out, err := runOutput("sc", "create", serviceName, "binPath=", cmdLine, "DisplayName=", "zapret", "start=", "auto"); err != nil {
		return fmt.Errorf("sc create: %s", firstLine(fmt.Errorf("%s", strings.TrimSpace(out))))
	}
	_ = runHidden("sc", "description", serviceName, "Zapret DPI bypass software")
	_ = runHidden("reg", "add", `HKLM\System\CurrentControlSet\Services\`+serviceName, "/v", "zapret-discord-youtube", "/t", "REG_SZ", "/d", s.Name, "/f")

	// "sc start" can report error 1053 for a program that is not a native service although
	// the process is running, so judge by the actual state, as service.bat's status does.
	_ = runHidden("sc", "start", serviceName)
	for i := 0; i < 16; i++ {
		time.Sleep(500 * time.Millisecond)
		if serviceActive() || runningProcesses()["winws.exe"] {
			return nil
		}
	}
	return fmt.Errorf("служба создана, но winws.exe не запустился (проверьте антивирус и диагностику)")
}

// removeService mirrors :service_remove of service.bat.
func (c *Controller) removeService() {
	_ = runHidden("sc", "stop", serviceName)
	time.Sleep(500 * time.Millisecond)
	_ = runHidden("sc", "delete", serviceName)
	killWinws()
	removeDriver()
}

// ---- hosts file -------------------------------------------------------------------------

func hostsFilePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "drivers", "etc", "hosts")
}

func readHostsFile() (string, error) {
	data, err := os.ReadFile(hostsFilePath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("файл hosts в кодировке UTF-16 — не трогаю его")
	}
	return string(data), nil
}

func writeHostsFile(text string) error {
	path := hostsFilePath()
	_ = os.Chmod(path, 0o666) // clears the read-only attribute some tools set
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("%w (антивирус или защита системы могут блокировать запись в hosts)", err)
	}
	_ = runHidden("ipconfig", "/flushdns")
	return nil
}

// applyHostsBlock writes the managed block; a backup of the original file is made once.
func applyHostsBlock(entries []string) error {
	cur, err := readHostsFile()
	if err != nil {
		return err
	}
	if backup := hostsFilePath() + ".zapret-backup"; !exists(backup) {
		if err := os.WriteFile(backup, []byte(cur), 0o644); err != nil {
			return fmt.Errorf("не удалось сделать резервную копию hosts: %w", err)
		}
	}
	return writeHostsFile(mergeHostsBlock(cur, entries))
}

// removeHostsBlockFromFile reports whether there was a block to remove.
func removeHostsBlockFromFile() (bool, error) {
	cur, err := readHostsFile()
	if err != nil {
		return false, err
	}
	next, found := removeHostsBlock(cur)
	if !found {
		return false, nil
	}
	return true, writeHostsFile(next)
}

// ---- Discord cache ------------------------------------------------------------------------

type discordEdition struct{ Process, Name, Dir string }

var discordEditions = []discordEdition{
	{"Discord.exe", "Discord", "discord"},
	{"DiscordPTB.exe", "Discord PTB", "discordptb"},
	{"DiscordCanary.exe", "Discord Canary", "discordcanary"},
	{"DiscordDevelopment.exe", "Discord Development", "discorddevelopment"},
}

// clearDiscordCache mirrors :clear_discord_cache of service.bat and returns report lines.
func clearDiscordCache() []DiagLine {
	var out []DiagLine
	appData := os.Getenv("APPDATA")
	procs := runningProcesses()
	found := false
	for _, ed := range discordEditions {
		dir := filepath.Join(appData, ed.Dir)
		if appData == "" || !exists(dir) {
			continue
		}
		found = true
		if procs[strings.ToLower(ed.Process)] {
			if err := runHidden("taskkill", "/F", "/IM", ed.Process); err != nil {
				out = append(out, DiagLine{DiagBad, ed.Name + " запущен, закрыть не удалось"})
			} else {
				out = append(out, DiagLine{DiagOK, ed.Name + " закрыт"})
				time.Sleep(700 * time.Millisecond)
			}
		}
		for _, sub := range []string{"Cache", "Code Cache", "GPUCache"} {
			p := filepath.Join(dir, sub)
			if !exists(p) {
				continue
			}
			if err := os.RemoveAll(p); err != nil || exists(p) {
				out = append(out, DiagLine{DiagBad, "Не удалось удалить " + p})
			} else {
				out = append(out, DiagLine{DiagOK, "Удалено: " + p})
			}
		}
	}
	if !found {
		out = append(out, DiagLine{DiagWarn, "Установки Discord не найдены"})
	}
	return out
}

// ---- registry facts --------------------------------------------------------------------------

func regOpen(root syscall.Handle, path string) (syscall.Handle, bool) {
	var h syscall.Handle
	return h, syscall.RegOpenKeyEx(root, utf16p(path), 0, syscall.KEY_READ, &h) == nil
}

func regDWORD(h syscall.Handle, name string) (uint32, bool) {
	var typ, size uint32 = 0, 4
	var val uint32
	if syscall.RegQueryValueEx(h, utf16p(name), nil, &typ, (*byte)(unsafe.Pointer(&val)), &size) != nil || typ != syscall.REG_DWORD {
		return 0, false
	}
	return val, true
}

func regString(h syscall.Handle, name string) (string, bool) {
	var typ uint32
	buf := make([]uint16, 512)
	size := uint32(len(buf) * 2)
	if syscall.RegQueryValueEx(h, utf16p(name), nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size) != nil || typ != syscall.REG_SZ {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

func regSubkeys(h syscall.Handle) []string {
	var names []string
	for i := uint32(0); i < 512; i++ {
		buf := make([]uint16, 256)
		n := uint32(len(buf))
		if syscall.RegEnumKeyEx(h, i, &buf[0], &n, nil, nil, nil, nil) != nil {
			break
		}
		names = append(names, syscall.UTF16ToString(buf[:n]))
	}
	return names
}

// systemProxy reports the WinINET proxy setting.
func systemProxy() (enabled bool, server string) {
	h, ok := regOpen(syscall.HKEY_CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`)
	if !ok {
		return false, ""
	}
	defer syscall.RegCloseKey(h)
	if v, ok := regDWORD(h, "ProxyEnable"); !ok || v == 0 {
		return false, ""
	}
	server, _ = regString(h, "ProxyServer")
	return true, server
}

// secureDNSConfigured looks for any DNS-over-HTTPS server (DohFlags > 0), like service.bat does.
func secureDNSConfigured() bool {
	const base = `SYSTEM\CurrentControlSet\Services\Dnscache\InterfaceSpecificParameters`
	visited := 0
	var walk func(path string, depth int) bool
	walk = func(path string, depth int) bool {
		if depth > 5 || visited > 600 {
			return false
		}
		visited++
		h, ok := regOpen(syscall.HKEY_LOCAL_MACHINE, path)
		if !ok {
			return false
		}
		if v, ok := regDWORD(h, "DohFlags"); ok && v > 0 {
			syscall.RegCloseKey(h)
			return true
		}
		subs := regSubkeys(h)
		syscall.RegCloseKey(h)
		for _, s := range subs {
			if walk(path+`\`+s, depth+1) {
				return true
			}
		}
		return false
	}
	return walk(base, 0)
}

// ---- message box with an answer ------------------------------------------------------------------

const (
	mbYesNo         = 0x00000004
	mbIconQuestion  = 0x00000020
	mbSetForeground = 0x00010000
	idYes           = 6
)

func askYesNo(owner uintptr, text, title string) bool {
	r, _, _ := procMessageBoxW.Call(owner, uintptr(unsafe.Pointer(utf16p(text))), uintptr(unsafe.Pointer(utf16p(title))), mbYesNo|mbIconQuestion|mbSetForeground)
	return r == idYes
}
