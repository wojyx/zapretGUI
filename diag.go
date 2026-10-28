package main

import (
	"regexp"
	"strings"
	"unicode"
)

// Diagnostics: the parsing and the decisions are portable (and tested); collecting the
// facts (services, processes, registry) lives in diag_windows.go.

type DiagLevel int

const (
	DiagOK DiagLevel = iota
	DiagInfo
	DiagWarn
	DiagBad
)

type DiagLine struct {
	Level DiagLevel
	Text  string
}

func (l DiagLine) String() string {
	switch l.Level {
	case DiagOK:
		return "[OK] " + l.Text
	case DiagWarn:
		return "[?] " + l.Text
	case DiagBad:
		return "[X] " + l.Text
	}
	return l.Text
}

type svcInfo struct{ Name, Display string }

var (
	scStateRe   = regexp.MustCompile(`\b\d\s+(RUNNING|STOPPED|START_PENDING|STOP_PENDING|PAUSED|CONTINUE_PENDING|PAUSE_PENDING)\b`)
	scHeaderRe  = regexp.MustCompile(`^[^:]{2,40}:\s*(\S.*)$`)
	cyrillicRe  = regexp.MustCompile(`[\x{0400}-\x{04FF}]`)
	processName = regexp.MustCompile(`^"([^"]+)"`)
)

// parseScQuery splits `sc query` output into services. The first two lines of a block are
// "<NAME label>: x" and "<DISPLAY label>: y" in every Windows language, so no label is matched.
func parseScQuery(out string) []svcInfo {
	var res []svcInfo
	for _, block := range regexp.MustCompile(`\r?\n\s*\r?\n`).Split(out, -1) {
		var lines []string
		for _, l := range strings.Split(block, "\n") {
			if l = strings.TrimSpace(strings.TrimRight(l, "\r")); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) < 2 {
			continue
		}
		m1, m2 := scHeaderRe.FindStringSubmatch(lines[0]), scHeaderRe.FindStringSubmatch(lines[1])
		if m1 == nil || m2 == nil {
			continue
		}
		res = append(res, svcInfo{Name: strings.TrimSpace(m1[1]), Display: strings.TrimSpace(m2[1])})
	}
	return res
}

// parseScState returns RUNNING, STOPPED, ... from `sc query <name>` or "" when absent.
func parseScState(out string) string {
	if m := scStateRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// parseTasklist returns lower-case image names from `tasklist /FO CSV /NH`.
func parseTasklist(out string) map[string]bool {
	set := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if m := processName.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			set[strings.ToLower(m[1])] = true
		}
	}
	return set
}

func hasCyrillic(s string) bool { return cyrillicRe.MatchString(s) }

func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func pathInside(path, dir string) bool {
	if dir == "" {
		return false
	}
	p := strings.ToLower(strings.ReplaceAll(path, "/", `\`)) + `\`
	d := strings.ToLower(strings.TrimRight(strings.ReplaceAll(dir, "/", `\`), `\`)) + `\`
	return strings.HasPrefix(p, d)
}

// pathChecks mirrors the "Cyrillic path" and "OneDrive" checks of service.bat.
func pathChecks(root, oneDrive string) []DiagLine {
	out := []DiagLine{{DiagInfo, "Программа установлена в: " + root}}
	if hasCyrillic(root) {
		out = append(out, DiagLine{DiagWarn, "В пути есть кириллица (имя пользователя?). Если обход не работает — это возможная причина"})
	} else {
		out = append(out, DiagLine{DiagOK, "Путь без кириллицы"})
	}
	if pathInside(root, oneDrive) {
		out = append(out, DiagLine{DiagBad, "Папка внутри OneDrive — синхронизация может блокировать файлы"})
	} else {
		out = append(out, DiagLine{DiagOK, "Папка не в OneDrive"})
	}
	return out
}

type conflictRule struct {
	Title string
	Level DiagLevel
	Match func(s svcInfo) bool
	Hint  string
}

func contains(s svcInfo, parts ...string) bool {
	n, d := strings.ToLower(s.Name), strings.ToLower(s.Display)
	for _, p := range parts {
		if !strings.Contains(n, p) && !strings.Contains(d, p) {
			return false
		}
	}
	return true
}

// Same lists as in service.bat.
var serviceRules = []conflictRule{
	{"Killer", DiagBad, func(s svcInfo) bool { return contains(s, "killer") }, "Службы Killer конфликтуют с zapret"},
	{"Intel Connectivity Network Service", DiagBad, func(s svcInfo) bool { return contains(s, "intel", "connectivity", "network") }, "Эта служба Intel конфликтует с zapret"},
	{"Check Point", DiagBad, func(s svcInfo) bool { return contains(s, "tracsrvwrapper") || contains(s, "epwd") }, "Check Point конфликтует с zapret — попробуйте удалить"},
	{"SmartByte", DiagBad, func(s svcInfo) bool { return contains(s, "smartbyte") }, "SmartByte конфликтует с zapret — удалите или отключите в services.msc"},
	{"VPN", DiagWarn, func(s svcInfo) bool { return contains(s, "vpn") || contains(s, "happservice") }, "Найдены службы VPN. Некоторые VPN конфликтуют с zapret — отключите их"},
}

// serviceChecks applies the rules to the list of running services.
func serviceChecks(services []svcInfo) []DiagLine {
	var out []DiagLine
	for _, r := range serviceRules {
		var found []string
		for _, s := range services {
			if r.Match(s) {
				found = append(found, s.Name)
			}
		}
		if len(found) == 0 {
			out = append(out, DiagLine{DiagOK, r.Title + ": конфликтов нет"})
			continue
		}
		if len(found) > 4 {
			found = append(found[:4], "…")
		}
		out = append(out, DiagLine{r.Level, r.Hint + " (" + strings.Join(found, ", ") + ")"})
	}
	return out
}

// otherBypassServices are services of other bypass tools that fight for WinDivert.
var otherBypassServices = []string{"GoodbyeDPI", "discordfix_zapret", "winws1", "winws2"}

func processChecks(procs map[string]bool) []DiagLine {
	if procs["adguardsvc.exe"] {
		return []DiagLine{{DiagBad, "Найден процесс Adguard — он может мешать Discord"}}
	}
	return []DiagLine{{DiagOK, "Adguard не найден"}}
}
