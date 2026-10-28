package main

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

func parseTaskXML(t *testing.T, s string) map[string]string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(s))
	// the declaration says UTF-16 (what schtasks wants); in memory it is a Go string
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	values := map[string]string{}
	var path []string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return values
		}
		if err != nil {
			t.Fatalf("task XML is not well-formed: %v\n%s", err, s)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			path = append(path, v.Name.Local)
		case xml.EndElement:
			path = path[:len(path)-1]
		case xml.CharData:
			if text := strings.TrimSpace(string(v)); text != "" {
				values[strings.Join(path, "/")] = text
			}
		}
	}
}

func TestAutostartTaskXML(t *testing.T) {
	exe := `C:\Users\Ivan & Co\Downloads\ZapretGUI.exe`
	got := parseTaskXML(t, autostartTaskXML(exe, `DESKTOP-1\Ivan`))

	want := map[string]string{
		"Task/Actions/Exec/Command":                exe, // `&` must round-trip through escaping
		"Task/Actions/Exec/Arguments":              "--autostart",
		"Task/Principals/Principal/RunLevel":       "HighestAvailable",
		"Task/Principals/Principal/UserId":         `DESKTOP-1\Ivan`,
		"Task/Settings/DisallowStartIfOnBatteries": "false",
		"Task/Settings/StopIfGoingOnBatteries":     "false",
		"Task/Settings/ExecutionTimeLimit":         "PT0S",
		"Task/Triggers/LogonTrigger/Delay":         "PT10S",
		"Task/Triggers/LogonTrigger/UserId":        `DESKTOP-1\Ivan`,
		"Task/Principals/Principal/LogonType":      "InteractiveToken",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	// without a known user the UserId tags are simply omitted
	got = parseTaskXML(t, autostartTaskXML(`C:\z.exe`, ""))
	if _, ok := got["Task/Principals/Principal/UserId"]; ok {
		t.Errorf("UserId must be omitted when unknown")
	}
}

func TestUTF16LEWithBOM(t *testing.T) {
	in := "Zapret: запуск ✓"
	b := utf16LEWithBOM(in)
	if b[0] != 0xFF || b[1] != 0xFE || len(b)%2 != 0 {
		t.Fatalf("bad BOM/length: % x", b[:4])
	}
	u := make([]uint16, 0, len(b)/2-1)
	for i := 2; i < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	if string(utf16.Decode(u)) != in {
		t.Fatalf("round trip failed")
	}
}
