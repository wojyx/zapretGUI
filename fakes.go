package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The strategies reference two "active" fake packets. Like "Replace active fakes" in
// service.bat, the user can point them at any other .bin of the bin folder. Instead of
// overwriting ACTIVE_*.bin (which an update or a restart would restore anyway) the
// choice is substituted into the winws arguments, so "default" is always one click away.
const (
	activeDiscordFile = "ACTIVE_DISCORD_UDP.bin"
	activeGameFile    = "ACTIVE_GAME_UDP.bin"
)

var fakeNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\-]*$`)

// listFakes returns the names (without ".bin") of every selectable fake packet.
func listFakes(binDir string) []string {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.EqualFold(filepath.Ext(n), ".bin") || strings.HasPrefix(strings.ToUpper(n), "ACTIVE_") {
			continue
		}
		base := strings.TrimSuffix(n, filepath.Ext(n))
		if fakeNameRe.MatchString(base) {
			names = append(names, base)
		}
	}
	sort.Strings(names)
	return names
}

// withFakes returns the strategy with the chosen fakes substituted. Unknown or empty
// names leave the default in place (a name that disappeared after an update is ignored).
func withFakes(s Strategy, discord, game string, available []string) Strategy {
	ok := func(name string) bool { return name != "" && indexOf(available, name) >= 0 }
	if ok(discord) {
		s.Args = strings.ReplaceAll(s.Args, "%BIN%"+activeDiscordFile, "%BIN%"+discord+".bin")
	}
	if ok(game) {
		s.Args = strings.ReplaceAll(s.Args, "%BIN%"+activeGameFile, "%BIN%"+game+".bin")
	}
	return s
}

// fakeChoiceError explains why a saved choice cannot be used (shown in the log).
func fakeChoiceError(name string, available []string) error {
	if name == "" || indexOf(available, name) >= 0 {
		return nil
	}
	return fmt.Errorf("fake-файл «%s» не найден, использую стандартный", name)
}
