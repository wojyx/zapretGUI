package main

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// hosts file handling. The upstream repository publishes .service/hosts (pinned addresses
// for GitHub and Telegram). service.bat only compares it and asks the user to copy it by
// hand; here the entries go into a clearly marked block that can be removed again, and
// nothing outside the block is ever touched.
const (
	hostsBegin = "# >>> Zapret GUI: managed block, edit via the program >>>"
	hostsEnd   = "# <<< Zapret GUI <<<"

	maxHostsEntries = 300
)

var hostNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?)+$`)

// parseHostsEntries validates a downloaded hosts list and returns normalised "ip name" lines.
// It is deliberately strict: it is data from the network that ends up in a system file.
func parseHostsEntries(data []byte) ([]string, error) {
	if strings.ContainsRune(string(data), 0) {
		return nil, fmt.Errorf("в файле бинарные данные")
	}
	var out []string
	seen := map[string]bool{}
	for i, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 300 {
			return nil, fmt.Errorf("строка %d слишком длинная", i+1)
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("строка %d: ожидается «адрес имя»", i+1)
		}
		ip, err := netip.ParseAddr(f[0])
		if err != nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			return nil, fmt.Errorf("строка %d: недопустимый адрес %q", i+1, f[0])
		}
		if !hostNameRe.MatchString(f[1]) || len(f[1]) > 253 {
			return nil, fmt.Errorf("строка %d: недопустимое имя %q", i+1, f[1])
		}
		entry := ip.String() + " " + strings.ToLower(f[1])
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
		if len(out) > maxHostsEntries {
			return nil, fmt.Errorf("слишком много записей (больше %d)", maxHostsEntries)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("в файле нет записей")
	}
	return out, nil
}

// summarizeHosts gives a short human description for the confirmation dialog.
func summarizeHosts(entries []string) string {
	domains := map[string]bool{}
	for _, e := range entries {
		domains[strings.Fields(e)[1]] = true
	}
	list := make([]string, 0, len(domains))
	for d := range domains {
		list = append(list, d)
	}
	sort.Strings(list)
	shown := list
	if len(shown) > 8 {
		shown = shown[:8]
	}
	s := fmt.Sprintf("%d записей для %d имён: %s", len(entries), len(list), strings.Join(shown, ", "))
	if len(list) > len(shown) {
		s += fmt.Sprintf(" и ещё %d", len(list)-len(shown))
	}
	return s
}

func splitBlock(existing string) (before, block, after string, found bool) {
	b := strings.Index(existing, hostsBegin)
	if b < 0 {
		return existing, "", "", false
	}
	e := strings.Index(existing[b:], hostsEnd)
	if e < 0 { // damaged block: treat everything from the begin marker as ours
		return existing[:b], existing[b:], "", true
	}
	end := b + e + len(hostsEnd)
	for end < len(existing) && (existing[end] == '\r' || existing[end] == '\n') {
		end++
	}
	return existing[:b], existing[b:end], existing[end:], true
}

// removeHostsBlock cuts our block out; the rest of the file stays byte for byte.
func removeHostsBlock(existing string) (string, bool) {
	before, _, after, found := splitBlock(existing)
	if !found {
		return existing, false
	}
	return before + after, true
}

// mergeHostsBlock replaces (or appends) our block. Line endings are CRLF as Windows expects.
func mergeHostsBlock(existing string, entries []string) string {
	rest, _ := removeHostsBlock(existing)
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\r\n"
	}
	var b strings.Builder
	b.WriteString(rest)
	b.WriteString(hostsBegin + "\r\n")
	for _, e := range entries {
		b.WriteString(e + "\r\n")
	}
	b.WriteString(hostsEnd + "\r\n")
	return b.String()
}

// hostsBlockEntries counts the entries of our block (0 = no block).
func hostsBlockEntries(existing string) int {
	_, block, _, found := splitBlock(existing)
	if !found {
		return 0
	}
	n := 0
	for _, l := range strings.Split(block, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			n++
		}
	}
	return n
}

// hostsMentionsYouTube mirrors the diagnostics warning: hand-made youtube entries break access.
func hostsMentionsYouTube(existing string) bool {
	rest, _ := removeHostsBlock(existing)
	for _, l := range strings.Split(rest, "\n") {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.Contains(l, "youtube.com") || strings.Contains(l, "youtu.be") {
			return true
		}
	}
	return false
}
