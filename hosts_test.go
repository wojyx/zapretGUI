package main

import (
	"strings"
	"testing"
)

func TestParseHostsEntries(t *testing.T) {
	good := "140.82.121.3 github.com\r\n\r\n# comment\n2606:50c0:8000::154   Raw.GitHubUserContent.com\n140.82.121.3 github.com\n"
	got, err := parseHostsEntries([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	want := "140.82.121.3 github.com|2606:50c0:8000::154 raw.githubusercontent.com"
	if strings.Join(got, "|") != want {
		t.Fatalf("got %v", got)
	}

	bad := map[string]string{
		"empty":         "",
		"only comments": "# nothing\n",
		"loopback":      "127.0.0.1 github.com\n",
		"unspecified":   "0.0.0.0 ads.example.com\n",
		"bad ip":        "999.1.1.1 github.com\n",
		"bad name":      "1.2.3.4 git hub.com\n",
		"no dot":        "1.2.3.4 localhost\n",
		"injection":     "1.2.3.4 a.com ; rm -rf\n",
		"three fields":  "1.2.3.4 a.com b.com\n",
		"binary":        "1.2.3.4 a.com\x00\n",
		"html":          "<html><body>404</body></html>\n",
		"unicode host":  "1.2.3.4 пример.рф\n",
	}
	for name, text := range bad {
		if _, err := parseHostsEntries([]byte(text)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	var many strings.Builder
	for i := 0; i <= maxHostsEntries; i++ {
		many.WriteString("1.2.3.4 h" + strings.Repeat("a", i%5+1) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676)) + ".example.com\n")
	}
	if _, err := parseHostsEntries([]byte(many.String())); err == nil {
		t.Error("too many entries must be rejected")
	}
}

func TestHostsBlockLifecycle(t *testing.T) {
	user := "# my hosts\r\n127.0.0.1 localhost\r\n10.0.0.5 nas.home"
	entries := []string{"140.82.121.3 github.com", "149.154.167.220 my.telegram.org"}

	merged := mergeHostsBlock(user, entries)
	if !strings.HasPrefix(merged, user+"\r\n"+hostsBegin) {
		t.Fatalf("user content must be kept byte for byte, block appended:\n%q", merged)
	}
	if hostsBlockEntries(merged) != 2 {
		t.Fatalf("entries in block: %d", hostsBlockEntries(merged))
	}
	if strings.Contains(strings.ReplaceAll(merged, "\r\n", ""), "\n") {
		t.Error("all line endings of the block must be CRLF")
	}

	// updating replaces the block instead of stacking a second one
	again := mergeHostsBlock(merged, []string{"1.1.1.1 example.org"})
	if strings.Count(again, hostsBegin) != 1 || strings.Contains(again, "github.com") || !strings.Contains(again, "1.1.1.1 example.org") {
		t.Fatalf("block not replaced:\n%q", again)
	}
	if !strings.HasPrefix(again, user) {
		t.Error("user part changed")
	}

	// removing restores the user's text (plus the newline that had to be added)
	removed, found := removeHostsBlock(again)
	if !found || strings.Contains(removed, "Zapret GUI") || strings.TrimRight(removed, "\r\n") != user {
		t.Fatalf("remove failed: %v %q", found, removed)
	}
	if _, found := removeHostsBlock(user); found {
		t.Error("nothing to remove from a clean file")
	}
	if got, _ := removeHostsBlock(user); got != user {
		t.Error("a clean file must stay unchanged")
	}

	// content after the block survives; a damaged block (no end marker) is cut to the end of file
	mid := "a\r\n" + hostsBegin + "\r\n1.2.3.4 x.com\r\n" + hostsEnd + "\r\nb\r\n"
	if got, _ := removeHostsBlock(mid); got != "a\r\nb\r\n" {
		t.Errorf("middle block: %q", got)
	}
	if got, _ := removeHostsBlock("a\r\n" + hostsBegin + "\r\n1.2.3.4 x.com\r\n"); got != "a\r\n" {
		t.Errorf("damaged block: %q", got)
	}

	// empty hosts file
	if m := mergeHostsBlock("", entries); !strings.HasPrefix(m, hostsBegin) {
		t.Errorf("empty file: %q", m)
	}
}

func TestHostsMentionsYouTube(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1 localhost\n":                                  false,
		"1.2.3.4 www.youtube.com\n":                              true,
		"# 1.2.3.4 youtube.com\n":                                false,
		"1.2.3.4 youtu.be\n":                                     true,
		hostsBegin + "\n1.2.3.4 youtube.com\n" + hostsEnd + "\n": false, // our own block is not a hand-made entry
	}
	for text, want := range cases {
		if got := hostsMentionsYouTube(text); got != want {
			t.Errorf("%q: got %v want %v", text, got, want)
		}
	}
}

func TestSummarizeHosts(t *testing.T) {
	s := summarizeHosts([]string{"1.1.1.1 b.com", "1.1.1.1 a.com", "::2 a.com"})
	if !strings.HasPrefix(s, "3 записей для 2 имён: a.com, b.com") {
		t.Fatal(s)
	}
	var many []string
	for i := 0; i < 12; i++ {
		many = append(many, "1.1.1.1 h"+string(rune('a'+i))+".com")
	}
	if !strings.HasSuffix(summarizeHosts(many), "и ещё 4") {
		t.Fatal(summarizeHosts(many))
	}
}
