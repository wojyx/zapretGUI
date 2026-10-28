package main

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// Same sources as "Update IPSet List" / "Update Hosts File" of service.bat.
var (
	ipsetListURL = "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/refs/heads/main/.service/ipset-service.txt"
	hostsListURL = "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/refs/heads/main/.service/hosts"
)

const (
	// A freshly downloaded list is kept next to the data dir; it wins over the embedded and
	// the update-delivered copy until the next program update brings a newer bundle.
	ipsetDownloadedFile = "ipset-downloaded.txt"

	maxIPSetBytes   = 20 << 20
	minIPSetEntries = 100
)

// validateIPSetList checks a downloaded list line by line (address or CIDR) and returns the entry count.
func validateIPSetList(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("список пуст")
	}
	if strings.ContainsRune(string(data), 0) {
		return 0, fmt.Errorf("в файле бинарные данные")
	}
	n := 0
	for i, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "/") {
			if _, err := netip.ParsePrefix(line); err != nil {
				return 0, fmt.Errorf("строка %d: %q не похоже на сеть", i+1, line)
			}
		} else if _, err := netip.ParseAddr(line); err != nil {
			return 0, fmt.Errorf("строка %d: %q не похоже на адрес", i+1, line)
		}
		n++
	}
	if n < minIPSetEntries {
		return n, fmt.Errorf("в списке всего %d записей — похоже на обрезанный файл", n)
	}
	return n, nil
}

// fetchIPSetList downloads and validates the upstream IP list.
func fetchIPSetList(ctx context.Context) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	b, err := httpGet(ctx, ipsetListURL, maxIPSetBytes)
	if err != nil {
		return nil, 0, err
	}
	n, err := validateIPSetList(b)
	if err != nil {
		return nil, 0, err
	}
	return b, n, nil
}

// fetchHostsEntries downloads and validates the upstream hosts entries.
func fetchHostsEntries(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, err := httpGet(ctx, hostsListURL+fmt.Sprintf("?t=%d", time.Now().UnixNano()), 1<<20)
	if err != nil {
		return nil, err
	}
	return parseHostsEntries(b)
}
