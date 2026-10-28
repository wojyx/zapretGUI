package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bigList(n int) string {
	var b strings.Builder
	b.WriteString("# header\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%d.%d.%d.0/24\n", 1+i/65536, i/256%256, i%256)
	}
	b.WriteString("203.0.113.9\n2001:db8::/32\n")
	return b.String()
}

func TestValidateIPSetList(t *testing.T) {
	n, err := validateIPSetList([]byte(bigList(500)))
	if err != nil || n != 502 {
		t.Fatalf("good list: %d %v", n, err)
	}
	bad := map[string]string{
		"empty":     "",
		"too short": "1.1.1.0/24\n",
		"html":      "<html>" + bigList(200),
		"bad cidr":  bigList(200) + "10.0.0.0/99\n",
		"bad ip":    bigList(200) + "300.1.1.1\n",
		"binary":    bigList(200) + "\x00\x01\n",
	}
	for name, text := range bad {
		if _, err := validateIPSetList([]byte(text)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// the real list from the repository passes the validator (optional)
	if repo := os.Getenv("ZAPRET_REPO"); repo != "" {
		b, err := os.ReadFile(filepath.Join(repo, ".service", "ipset-service.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if n, err := validateIPSetList(b); err != nil || n < 10000 {
			t.Fatalf("real list: %d %v", n, err)
		}
		hb, _ := os.ReadFile(filepath.Join(repo, ".service", "hosts"))
		if e, err := parseHostsEntries(hb); err != nil || len(e) < 10 {
			t.Fatalf("real hosts: %d %v", len(e), err)
		}
	}
}

func TestFetchLists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ipset", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(bigList(300))) })
	mux.HandleFunc("/ipset-bad", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>captive portal</html>")) })
	mux.HandleFunc("/hosts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "" {
			t.Error("hosts request must bypass caches")
		}
		w.Write([]byte("1.2.3.4 github.com\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	oldI, oldH := ipsetListURL, hostsListURL
	defer func() { ipsetListURL, hostsListURL = oldI, oldH }()

	ipsetListURL = srv.URL + "/ipset"
	if b, n, err := fetchIPSetList(context.Background()); err != nil || n != 302 || len(b) == 0 {
		t.Fatalf("fetchIPSetList: %d %v", n, err)
	}
	ipsetListURL = srv.URL + "/ipset-bad"
	if _, _, err := fetchIPSetList(context.Background()); err == nil {
		t.Fatal("captive-portal page must be rejected")
	}
	hostsListURL = srv.URL + "/hosts"
	if e, err := fetchHostsEntries(context.Background()); err != nil || len(e) != 1 {
		t.Fatalf("fetchHostsEntries: %v %v", e, err)
	}
}

func TestDownloadedIPSetPriority(t *testing.T) {
	root := t.TempDir()
	lists := filepath.Join(root, "lists")
	os.MkdirAll(lists, 0o755)
	read := func() string {
		if err := writeIPSet(lists, "loaded"); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(lists, "ipset-all.txt"))
		return string(b)
	}
	if got := read(); len(got) < 100000 {
		t.Fatalf("embedded list expected, got %d bytes", len(got))
	}
	os.WriteFile(filepath.Join(root, ipsetFullUpdate), []byte("5.5.5.0/24\n"), 0o644)
	if got := read(); got != "5.5.5.0/24\n" {
		t.Fatalf("update-delivered list must win over the embedded one: %.30q", got)
	}
	os.WriteFile(filepath.Join(root, ipsetDownloadedFile), []byte("6.6.6.0/24\n"), 0o644)
	if got := read(); got != "6.6.6.0/24\n" {
		t.Fatalf("downloaded list must win: %.30q", got)
	}
	os.WriteFile(filepath.Join(root, ipsetDownloadedFile), nil, 0o644) // empty file is ignored
	if got := read(); got != "5.5.5.0/24\n" {
		t.Fatalf("empty downloaded list must be ignored: %.30q", got)
	}

	// a program update drops the downloaded list, ensureAssets keeps it otherwise
	os.WriteFile(filepath.Join(root, ipsetDownloadedFile), []byte("6.6.6.0/24\n"), 0o644)
	if _, err := ensureAssets(root); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(root, ipsetDownloadedFile)) {
		t.Fatal("ensureAssets must not delete the downloaded list")
	}
	p, err := parseUpdateZip(makeZip(t, "r-main/", goodFiles("99.1.0")))
	if err != nil {
		t.Fatal(err)
	}
	if err := installUpdate(root, p); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(root, ipsetDownloadedFile)) {
		t.Fatal("a program update must replace the downloaded list")
	}
}
