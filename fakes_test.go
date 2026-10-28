package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListFakes(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"quic_initial_www_google_com.bin", "stun.bin", "ACTIVE_DISCORD_UDP.bin", "active_game_udp.bin", "winws.exe", "weird name.bin", "tls_clienthello_4pda_to.BIN"} {
		os.WriteFile(filepath.Join(dir, n), []byte{1}, 0o644)
	}
	os.Mkdir(filepath.Join(dir, "sub.bin"), 0o755)
	got := strings.Join(listFakes(dir), ",")
	if got != "quic_initial_www_google_com,stun,tls_clienthello_4pda_to" {
		t.Fatalf("listFakes = %s", got)
	}
	if listFakes(filepath.Join(dir, "missing")) != nil {
		t.Fatal("missing dir must give no fakes")
	}
}

func TestWithFakes(t *testing.T) {
	list, _ := loadStrategies()
	s := list[0]
	if !strings.Contains(s.Args, "%BIN%"+activeDiscordFile) || !strings.Contains(s.Args, "%BIN%"+activeGameFile) {
		t.Fatal("test premise: general uses both ACTIVE files")
	}
	avail := []string{"stun", "quic_initial_www_google_com"}

	got := withFakes(s, "stun", "quic_initial_www_google_com", avail)
	if strings.Contains(got.Args, activeDiscordFile) || strings.Contains(got.Args, activeGameFile) {
		t.Error("ACTIVE files must be replaced")
	}
	if !strings.Contains(got.Args, `--dpi-desync-fake-discord="%BIN%stun.bin"`) {
		t.Error("discord fake not substituted")
	}
	if !strings.Contains(got.Args, `--dpi-desync-fake-unknown-udp="%BIN%quic_initial_www_google_com.bin"`) {
		t.Error("game fake not substituted")
	}
	if s.Args != list[0].Args {
		t.Error("the original strategy must not be modified")
	}

	// defaults and unknown names (also path tricks) change nothing
	for _, name := range []string{"", "nope", `..\evil`, "../evil", "stun.bin"} {
		if g := withFakes(s, name, name, avail); g.Args != s.Args {
			t.Errorf("name %q must be ignored", name)
		}
	}
	// the result still expands to a valid command line
	cl := buildCmdLine(got, `C:\z\bin`, `C:\z\lists`, "off")
	if strings.Contains(cl, "%") || !strings.Contains(cl, `"C:\z\bin\stun.bin"`) {
		t.Errorf("bad command line: %.200s", cl)
	}
	if fakeChoiceError("stun", avail) != nil || fakeChoiceError("", avail) != nil || fakeChoiceError("gone", avail) == nil {
		t.Error("fakeChoiceError")
	}
}

func TestEmbeddedFakesAreListed(t *testing.T) {
	root := t.TempDir()
	if _, err := ensureAssets(root); err != nil {
		t.Fatal(err)
	}
	fakes := listFakes(filepath.Join(root, "bin"))
	for _, want := range []string{"quic_initial_www_google_com", "stun", "tls_clienthello_www_google_com"} {
		if indexOf(fakes, want) < 0 {
			t.Errorf("%s missing from %v", want, fakes)
		}
	}
	for _, f := range fakes {
		if strings.HasPrefix(strings.ToUpper(f), "ACTIVE_") {
			t.Errorf("ACTIVE file listed: %s", f)
		}
	}
}
