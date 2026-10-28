package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoadStrategies(t *testing.T) {
	list, err := loadStrategies()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 22 {
		t.Fatalf("expected 22 strategies, got %d", len(list))
	}
	if list[0].Name != "general" {
		t.Fatalf("first strategy must be plain general, got %q", list[0].Name)
	}
	seen := map[string]bool{}
	for _, s := range list {
		if seen[s.Name] {
			t.Errorf("duplicate strategy %q", s.Name)
		}
		seen[s.Name] = true
		if !strings.Contains(s.Args, "--wf-") {
			t.Errorf("%s: no WinDivert filter", s.Name)
		}
	}
}

func TestBuildCmdLineSubstitutesEverything(t *testing.T) {
	list, _ := loadStrategies()
	bin := `C:\Users\Иван Петров\AppData\Local\ZapretGUI\bin`
	lists := `C:\Users\Иван Петров\AppData\Local\ZapretGUI\lists`
	leftover := regexp.MustCompile(`%[A-Za-z]+%`)

	for _, mode := range gameModes {
		for _, s := range list {
			cl := buildCmdLine(s, bin, lists, mode)
			if m := leftover.FindString(cl); m != "" {
				t.Fatalf("%s/%s: unexpanded variable %s", s.Name, mode, m)
			}
			if !strings.HasPrefix(cl, `"`+bin+`\winws.exe" --`) {
				t.Fatalf("%s: bad prefix: %.80s", s.Name, cl)
			}
			if strings.Count(cl, `"`)%2 != 0 {
				t.Fatalf("%s: unbalanced quotes", s.Name)
			}
			if strings.Contains(cl, `\"`) {
				t.Fatalf("%s: backslash before a quote would be parsed as an escape", s.Name)
			}
		}
	}
}

func TestBuildCmdLineMatchesGeneralBat(t *testing.T) {
	list, _ := loadStrategies()
	cl := buildCmdLine(list[0], `C:\z\bin`, `C:\z\lists`, "off")
	for _, want := range []string{
		`--wf-tcp=80,443,2053,2083,2087,2096,8443,12 --wf-udp=443,19294-19344,50000-50100,12`,
		`--hostlist="C:\z\lists\list-general.txt"`,
		`--dpi-desync-fake-quic="C:\z\bin\quic_initial_www_google_com.bin"`,
		`--filter-udp=12 --ipset="C:\z\lists\ipset-all.txt"`,
	} {
		if !strings.Contains(cl, want) {
			t.Errorf("command line lacks %q", want)
		}
	}
	cl = buildCmdLine(list[0], `C:\z\bin`, `C:\z\lists`, "all")
	if !strings.Contains(cl, `--wf-tcp=80,443,2053,2083,2087,2096,8443,1024-65535 --wf-udp=443,19294-19344,50000-50100,1024-65535`) {
		t.Errorf("game filter 'all' not applied")
	}
}

func TestFakeTLSAutoUnescapedBang(t *testing.T) {
	list, _ := loadStrategies()
	for _, s := range list {
		if strings.Contains(s.Args, "^") {
			t.Errorf("%s: batch escape leaked into args", s.Name)
		}
	}
}

func TestGameFilterPorts(t *testing.T) {
	cases := map[string][2]string{
		"off": {"12", "12"}, "all": {gameRange, gameRange},
		"tcp": {gameRange, "12"}, "udp": {"12", gameRange}, "bogus": {"12", "12"},
	}
	for mode, want := range cases {
		tcp, udp := gameFilterPorts(mode)
		if tcp != want[0] || udp != want[1] {
			t.Errorf("%s: got %s/%s want %s/%s", mode, tcp, udp, want[0], want[1])
		}
	}
}

func TestEnsureAssetsAndIPSet(t *testing.T) {
	root := t.TempDir()
	warn, err := ensureAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(warn) != 0 {
		t.Fatalf("unexpected warnings: %v", warn)
	}
	for _, f := range []string{
		"bin/winws.exe", "bin/WinDivert.dll", "bin/WinDivert64.sys", "bin/cygwin1.dll",
		"bin/quic_initial_www_google_com.bin", "bin/tls_clienthello_4pda_to.bin",
		"lists/list-general.txt", "lists/list-google.txt", "lists/ipset-exclude.txt",
		"lists/list-general-user.txt", "lists/list-exclude-user.txt", "lists/ipset-exclude-user.txt",
		"lists/ipset-all.txt", "LICENSE-zapret-discord-youtube.txt",
	} {
		if !exists(filepath.Join(root, filepath.FromSlash(f))) {
			t.Errorf("missing %s", f)
		}
	}
	if exists(filepath.Join(root, "lists", "ipset-all-full.txt")) {
		t.Errorf("the full ipset list must stay inside the exe")
	}

	// user lists survive a re-extraction, shared lists are refreshed
	user := filepath.Join(root, "lists", "list-general-user.txt")
	shared := filepath.Join(root, "lists", "list-general.txt")
	_ = os.WriteFile(user, []byte("my.example.org\n"), 0o644)
	_ = os.WriteFile(shared, []byte("tampered\n"), 0o644)
	if _, err := ensureAssets(root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(user); string(b) != "my.example.org\n" {
		t.Errorf("user list was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(shared); string(b) == "tampered\n" {
		t.Errorf("shared list was not restored")
	}

	lists := filepath.Join(root, "lists")
	ipset := filepath.Join(lists, "ipset-all.txt")
	for mode, check := range map[string]func([]byte) bool{
		"none":   func(b []byte) bool { return string(b) == ipsetPlaceholder },
		"any":    func(b []byte) bool { return len(b) == 0 },
		"loaded": func(b []byte) bool { return len(b) > 100000 && !strings.Contains(string(b), "203.0.113.113") },
	} {
		if err := writeIPSet(lists, mode); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(ipset)
		if !check(b) {
			t.Errorf("ipset mode %s produced %d bytes: %.40q", mode, len(b), b)
		}
	}
}

func TestConfigRoundTripAndSanitising(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	if c := loadConfig(p); c != defaultConfig() {
		t.Fatalf("missing file must give defaults, got %+v", c)
	}
	want := Config{Strategy: "general (ALT2)", GameFilter: "tcp", IPSet: "loaded", Autostart: true, Theme: themeDark}
	if err := saveConfig(p, want); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(p); got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	_ = os.WriteFile(p, []byte(`{"strategy":"x","gameFilter":"zzz","ipset":"qqq","theme":"neon"}`), 0o644)
	if got := loadConfig(p); got.GameFilter != "off" || got.IPSet != "none" || got.Theme != themeAuto {
		t.Fatalf("bad values must be reset: %+v", got)
	}
	_ = os.WriteFile(p, []byte(`{"strategy":"general (ALT)","gameFilter":"udp","ipset":"any","autostart":true}`), 0o644)
	if got := loadConfig(p); got.Theme != themeAuto || got.Strategy != "general (ALT)" || got.GameFilter != "udp" || !got.Autostart {
		t.Fatalf("config of an older version must load with theme auto: %+v", got)
	}
	_ = os.WriteFile(p, []byte(`not json`), 0o644)
	if got := loadConfig(p); got != defaultConfig() {
		t.Fatalf("broken file must give defaults: %+v", got)
	}
}

func TestProbe(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden) // any answer counts
	}))
	defer ok.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	res := probe(context.Background(), []probeTarget{{"ok", ok.URL}, {"dead", deadURL}})
	if res.OK != 1 || res.Total != 2 || len(res.Failed) != 1 || res.Failed[0] != "dead" || res.AllOK() {
		t.Fatalf("unexpected result: %+v", res)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := probe(ctx, []probeTarget{{"ok", ok.URL}}); res.OK != 0 {
		t.Fatalf("cancelled probe must not succeed: %+v", res)
	}
}
