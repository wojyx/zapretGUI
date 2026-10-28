package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.10.5", "1.10.4", true}, {"1.10.4", "1.10.4", false}, {"1.9.9", "1.10.0", false},
		{"2.0", "1.99.99", true}, {"1.10.4.1", "1.10.4", true}, {"v1.11.0\n", "1.10.4", true},
		{"garbage", "1.0", false}, {"1.0", "garbage", false}, {"", "1.0", false},
		{"1.10.4", "1.10.4.0", false},
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSortStrategies(t *testing.T) {
	names := []string{"general (SIMPLE FAKE)", "general (ALT10)", "general (ALT2)", "general (ALT)", "general", "general (EXP)", "general (ALT13)", "general (FAKE TLS AUTO ALT2)", "general (FAKE TLS AUTO)"}
	var list []Strategy
	for _, n := range names {
		list = append(list, Strategy{Name: n})
	}
	sortStrategies(list)
	var got []string
	for _, s := range list {
		got = append(got, s.Name)
	}
	want := "general|general (ALT)|general (ALT2)|general (ALT10)|general (ALT13)|general (EXP)|general (FAKE TLS AUTO)|general (FAKE TLS AUTO ALT2)|general (SIMPLE FAKE)"
	if strings.Join(got, "|") != want {
		t.Fatalf("order:\n got %s\nwant %s", strings.Join(got, "|"), want)
	}
}

func TestStrategyArgsFromBat(t *testing.T) {
	bat := "@echo off\r\nstart \"z\" /min \"%BIN%winws.exe\" --wf-tcp=80,%GameFilterTCP% ^\r\n--a=\"%BIN%x.bin\" --fake-tls=^! ^\r\n--b=\"%LISTS%l.txt\"\r\n"
	got, err := strategyArgsFromBat(bat)
	if err != nil {
		t.Fatal(err)
	}
	want := `--wf-tcp=80,%GameFilterTCP% --a="%BIN%x.bin" --fake-tls=! --b="%LISTS%l.txt"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	bad := map[string]string{
		"no launch":       "@echo off\n",
		"unknown var":     `start "z" "%BIN%winws.exe" --wf-tcp=80 --x=%FOO%`,
		"unquoted path":   `start "z" "%BIN%winws.exe" --wf-tcp=80 --x=%BIN%a.bin`,
		"shell injection": `start "z" "%BIN%winws.exe" --wf-tcp=80 & calc.exe`,
		"no windivert":    `start "z" "%BIN%winws.exe" --foo=1`,
		"dangling caret":  `start "z" "%BIN%winws.exe" --wf-tcp=80 ^`,
	}
	for name, text := range bad {
		if _, err := strategyArgsFromBat(text); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// ---- synthetic archive helpers ------------------------------------------------------------

func fakeBinary(n int) []byte { return append([]byte("MZ"), bytes.Repeat([]byte{0x90}, n)...) }

func goodFiles(version string) map[string][]byte {
	return map[string][]byte{
		"bin/winws.exe":              fakeBinary(2000),
		"bin/WinDivert.dll":          fakeBinary(2000),
		"bin/WinDivert64.sys":        bytes.Repeat([]byte{1}, 2000),
		"bin/cygwin1.dll":            fakeBinary(2000),
		"bin/new_fake.bin":           {1, 2, 3},
		"lists/list-general.txt":     []byte("example.org\n"),
		"lists/ipset-exclude.txt":    []byte("10.0.0.0/8\n"),
		"lists/ipset-all.txt.backup": []byte("1.2.3.0/24\n4.5.6.0/24\n"),
		"lists/ipset-all.txt":        []byte("203.0.113.113/32\n"),
		"general.bat":                []byte("@echo off\nstart \"z\" /min \"%BIN%winws.exe\" --wf-tcp=80,443 ^\n--filter-tcp=443 --dpi-desync-fake-tls=\"%BIN%new_fake.bin\"\n"),
		"general (ALT2).bat":         []byte("@echo off\nstart \"z\" /min \"%BIN%winws.exe\" --wf-udp=443 --x=1\n"),
		"general (ALT).bat":          []byte("@echo off\nstart \"z\" /min \"%BIN%winws.exe\" --wf-udp=443 --x=2\n"),
		".service/version.txt":       []byte(version + "\n"),
		"LICENSE.txt":                []byte("MIT\n"),
		"README.md":                  []byte("ignored"),
		"utils/test zapret.ps1":      []byte("ignored"),
	}
}

func makeZip(t *testing.T, prefix string, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(prefix + name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseUpdateZip(t *testing.T) {
	p, err := parseUpdateZip(makeZip(t, "zapret-discord-youtube-main/", goodFiles("9.9.9")))
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "9.9.9" {
		t.Errorf("version %q", p.Version)
	}
	var names []string
	for _, s := range p.Strategies {
		names = append(names, s.Name)
	}
	if strings.Join(names, "|") != "general|general (ALT)|general (ALT2)" {
		t.Errorf("strategies: %v", names)
	}
	for _, f := range []string{"bin/winws.exe", "bin/new_fake.bin", "lists/list-general.txt", "lists/ipset-exclude.txt", ipsetFullUpdate, "LICENSE-zapret-discord-youtube.txt"} {
		if _, ok := p.Files[f]; !ok {
			t.Errorf("missing %s", f)
		}
	}
	for f := range p.Files {
		if f == "lists/ipset-all.txt" || strings.Contains(f, "README") || strings.Contains(f, "utils") {
			t.Errorf("unexpected file taken from the archive: %s", f)
		}
	}
}

func TestParseUpdateZipRejects(t *testing.T) {
	mut := func(f func(m map[string][]byte)) []byte {
		m := goodFiles("9.9.9")
		f(m)
		return makeZip(t, "repo-main/", m)
	}
	cases := map[string][]byte{
		"not a zip":    []byte("hello"),
		"no winws":     mut(func(m map[string][]byte) { delete(m, "bin/winws.exe") }),
		"winws not PE": mut(func(m map[string][]byte) { m["bin/winws.exe"] = bytes.Repeat([]byte("x"), 3000) }),
		"no driver":    mut(func(m map[string][]byte) { delete(m, "bin/WinDivert64.sys") }),
		"no version":   mut(func(m map[string][]byte) { delete(m, ".service/version.txt") }),
		"bad version":  mut(func(m map[string][]byte) { m[".service/version.txt"] = []byte("<html>") }),
		"no strategies": mut(func(m map[string][]byte) {
			delete(m, "general.bat")
			delete(m, "general (ALT).bat")
			delete(m, "general (ALT2).bat")
		}),
		"bat missing bin": mut(func(m map[string][]byte) { delete(m, "bin/new_fake.bin") }),
		"bat injection": mut(func(m map[string][]byte) {
			m["general.bat"] = []byte("start \"z\" \"%BIN%winws.exe\" --wf-tcp=1 & calc")
		}),
		"no ipset list":  mut(func(m map[string][]byte) { delete(m, "lists/ipset-all.txt.backup") }),
		"path traversal": mut(func(m map[string][]byte) { m["bin/../../evil.exe"] = fakeBinary(10) }),
	}
	for name, data := range cases {
		if _, err := parseUpdateZip(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// ---- install / restore ----------------------------------------------------------------------

func TestInstallUpdateLifecycle(t *testing.T) {
	root := t.TempDir()
	if _, err := ensureAssets(root); err != nil {
		t.Fatal(err)
	}
	if got := installedVersion(root); got != embeddedVersion() {
		t.Fatalf("fresh install must report the embedded version, got %s", got)
	}
	user := filepath.Join(root, "lists", "list-general-user.txt")
	os.WriteFile(user, []byte("mine.example\n"), 0o644)

	newVer := "99.0.1"
	p, err := parseUpdateZip(makeZip(t, "r-main/", goodFiles(newVer)))
	if err != nil {
		t.Fatal(err)
	}
	if err := installUpdate(root, p); err != nil {
		t.Fatal(err)
	}
	if installedVersion(root) != newVer {
		t.Fatalf("version after update: %s", installedVersion(root))
	}
	list, err := loadStrategiesFrom(root)
	if err != nil || len(list) != 3 {
		t.Fatalf("strategies after update: %v %v", len(list), err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "bin", "new_fake.bin")); len(b) != 3 {
		t.Fatalf("new file not installed")
	}

	// a restart must keep the update (embedded files are older) and the user's list
	warn, err := ensureAssets(root)
	if err != nil || len(warn) != 0 {
		t.Fatalf("ensureAssets: %v %v", warn, err)
	}
	if installedVersion(root) != newVer {
		t.Fatalf("restart dropped the update")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "bin", "winws.exe")); len(b) != 2002 {
		t.Fatalf("restart overwrote updated winws.exe with the embedded one (%d bytes)", len(b))
	}
	if b, _ := os.ReadFile(user); string(b) != "mine.example\n" {
		t.Fatalf("user list changed: %q", b)
	}

	// 'loaded' ipset mode uses the list delivered by the update
	lists := filepath.Join(root, "lists")
	if err := writeIPSet(lists, "loaded"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(lists, "ipset-all.txt")); !strings.HasPrefix(string(b), "1.2.3.0/24") {
		t.Fatalf("ipset not taken from update: %.30q", b)
	}

	// interrupted update (marker missing): known-good embedded files come back, nothing is mixed
	os.Remove(filepath.Join(root, versionMarker))
	if _, err := ensureAssets(root); err != nil {
		t.Fatal(err)
	}
	if installedVersion(root) != embeddedVersion() {
		t.Fatalf("expected rollback to embedded version")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "bin", "winws.exe")); len(b) < 100000 {
		t.Fatalf("embedded winws.exe not restored (%d bytes)", len(b))
	}
	for _, f := range []string{strategiesFile, ipsetFullUpdate} {
		if exists(filepath.Join(root, f)) {
			t.Errorf("%s must be removed on rollback", f)
		}
	}
	if list, _ := loadStrategiesFrom(root); len(list) != 22 {
		t.Fatalf("rollback must use embedded strategies, got %d", len(list))
	}

	// an older "update" never wins over the embedded version
	old, _ := parseUpdateZip(makeZip(t, "r-main/", goodFiles("0.0.1")))
	if err := installUpdate(root, old); err != nil {
		t.Fatal(err)
	}
	if installedVersion(root) != embeddedVersion() {
		t.Fatalf("older update must not become active")
	}
}

// ---- network ----------------------------------------------------------------------------------

func TestFetchAndDownload(t *testing.T) {
	zipData := makeZip(t, "repo-main/", goodFiles("77.1.0"))
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("77.1.0\n")) })
	mux.HandleFunc("/junk", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) })
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) { w.Write(zipData) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	oldV, oldZ := updateVersionURL, updateZipURL
	defer func() { updateVersionURL, updateZipURL = oldV, oldZ }()

	updateVersionURL = srv.URL + "/version"
	if v, err := fetchLatestVersion(context.Background()); err != nil || v != "77.1.0" {
		t.Fatalf("fetchLatestVersion: %q %v", v, err)
	}
	updateVersionURL = srv.URL + "/junk"
	if _, err := fetchLatestVersion(context.Background()); err == nil {
		t.Fatal("junk version must be rejected")
	}
	updateVersionURL = srv.URL + "/missing"
	if _, err := fetchLatestVersion(context.Background()); err == nil {
		t.Fatal("404 must be an error")
	}

	updateZipURL = srv.URL + "/zip"
	p, err := downloadUpdate(context.Background())
	if err != nil || p.Version != "77.1.0" {
		t.Fatalf("downloadUpdate: %v %v", p, err)
	}

	// plain http to a non-local host is refused
	updateZipURL = "http://example.com/x.zip"
	if _, err := downloadUpdate(context.Background()); err == nil {
		t.Fatal("non-HTTPS download must be refused")
	}
}

// ---- real repository (optional) ------------------------------------------------------------------

// ZAPRET_REPO=/path/to/clone go test -run Real  — checks the Go parser against the real repository
// and against the strategies.json produced by the Python generator.
func TestRealRepoMatchesEmbedded(t *testing.T) {
	repo := os.Getenv("ZAPRET_REPO")
	if repo == "" {
		t.Skip("set ZAPRET_REPO to a checkout of Flowseal/zapret-discord-youtube")
	}
	files := map[string][]byte{}
	add := func(rel string) {
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		files[rel] = b
	}
	entries, _ := os.ReadDir(repo)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "general") && strings.HasSuffix(e.Name(), ".bat") {
			add(e.Name())
		}
	}
	bins, _ := os.ReadDir(filepath.Join(repo, "bin"))
	for _, e := range bins {
		add("bin/" + e.Name())
	}
	lists, _ := os.ReadDir(filepath.Join(repo, "lists"))
	for _, e := range lists {
		add("lists/" + e.Name())
	}
	add(".service/version.txt")
	add("LICENSE.txt")

	p, err := parseUpdateZip(makeZip(t, "zapret-discord-youtube-main/", files))
	if err != nil {
		t.Fatal(err)
	}
	var want []Strategy
	if err := json.Unmarshal(strategiesJSON, &want); err != nil {
		t.Fatal(err)
	}
	if len(p.Strategies) != len(want) {
		t.Fatalf("got %d strategies, python generator made %d", len(p.Strategies), len(want))
	}
	for i := range want {
		if p.Strategies[i] != want[i] {
			t.Errorf("strategy %d differs: %q vs %q", i, p.Strategies[i].Name, want[i].Name)
		}
	}
	t.Logf("real repo: version %s, %d strategies, %d files", p.Version, len(p.Strategies), len(p.Files))
}
