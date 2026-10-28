package main

import (
	"strings"
	"testing"
)

const scEnglish = "\r\nSERVICE_NAME: BFE\r\nDISPLAY_NAME: Base Filtering Engine\r\n        TYPE               : 20  WIN32_SHARE_PROCESS\r\n        STATE              : 4  RUNNING\r\n                                (STOPPABLE, NOT_PAUSABLE, ACCEPTS_SHUTDOWN)\r\n        WIN32_EXIT_CODE    : 0  (0x0)\r\n\r\nSERVICE_NAME: Killer Network Service x64\r\nDISPLAY_NAME: Killer Network Service\r\n        TYPE               : 10  WIN32_OWN_PROCESS\r\n        STATE              : 4  RUNNING\r\n\r\nSERVICE_NAME: ProtonVPN Service\r\nDISPLAY_NAME: ProtonVPN Service\r\n        STATE              : 4  RUNNING\r\n\r\n"

const scRussian = "\r\nИМЯ_СЛУЖБЫ: SmartByteNetworkService\r\nОТОБРАЖАЕМОЕ_ИМЯ: SmartByte Network Service\r\n        ТИП                : 10  WIN32_OWN_PROCESS\r\n        СОСТОЯНИЕ          : 4  RUNNING\r\n\r\nИМЯ_СЛУЖБЫ: Dnscache\r\nОТОБРАЖАЕМОЕ_ИМЯ: DNS-клиент\r\n        СОСТОЯНИЕ          : 4  RUNNING\r\n"

func TestParseScQuery(t *testing.T) {
	en := parseScQuery(scEnglish)
	if len(en) != 3 || en[0].Name != "BFE" || en[1].Display != "Killer Network Service" || en[2].Name != "ProtonVPN Service" {
		t.Fatalf("english: %+v", en)
	}
	ru := parseScQuery(scRussian)
	if len(ru) != 2 || ru[0].Name != "SmartByteNetworkService" || ru[1].Display != "DNS-клиент" {
		t.Fatalf("russian labels must parse the same way: %+v", ru)
	}
	if len(parseScQuery("")) != 0 || len(parseScQuery("garbage without colon\n\nmore")) != 0 {
		t.Fatal("garbage must give no services")
	}
}

func TestParseScState(t *testing.T) {
	cases := map[string]string{
		scEnglish: "RUNNING",
		"        СОСТОЯНИЕ          : 1  STOPPED":               "STOPPED",
		"STATE : 3  STOP_PENDING":                               "STOP_PENDING",
		"[SC] EnumQueryServicesStatus: OpenService FAILED 1060": "",
		"TYPE : 10  WIN32_OWN_PROCESS":                          "",
	}
	for in, want := range cases {
		if got := parseScState(in); got != want {
			t.Errorf("%.40q: got %q want %q", in, got, want)
		}
	}
}

func TestServiceChecks(t *testing.T) {
	lines := serviceChecks(parseScQuery(scEnglish))
	text := ""
	for _, l := range lines {
		text += l.String() + "\n"
	}
	for _, want := range []string{"[X] Службы Killer конфликтуют", "[?] Найдены службы VPN", "(ProtonVPN Service)", "[OK] SmartByte", "[OK] Check Point"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	ru := serviceChecks(parseScQuery(scRussian))
	found := false
	for _, l := range ru {
		if l.Level == DiagBad && strings.Contains(l.Text, "SmartByte") {
			found = true
		}
	}
	if !found {
		t.Error("SmartByte must be found on a Russian system")
	}
	for _, l := range serviceChecks(nil) {
		if l.Level != DiagOK {
			t.Errorf("no services = no findings, got %v", l)
		}
	}
}

func TestParseTasklistAndProcessChecks(t *testing.T) {
	out := "\"System Idle Process\",\"0\",\"Services\",\"0\",\"8 K\"\r\n\"AdguardSvc.exe\",\"1234\",\"Services\",\"0\",\"20 K\"\r\nИНФОРМАЦИЯ: задачи не найдены\r\n"
	procs := parseTasklist(out)
	if !procs["adguardsvc.exe"] || !procs["system idle process"] || len(procs) != 2 {
		t.Fatalf("%v", procs)
	}
	if processChecks(procs)[0].Level != DiagBad || processChecks(map[string]bool{})[0].Level != DiagOK {
		t.Fatal("adguard decision")
	}
}

func TestPathChecks(t *testing.T) {
	text := func(l []DiagLine) string {
		var s string
		for _, x := range l {
			s += x.String() + "\n"
		}
		return s
	}
	ok := text(pathChecks(`C:\Users\Ivan\AppData\Local\ZapretGUI`, `C:\Users\Ivan\OneDrive`))
	if !strings.Contains(ok, "[OK] Путь без кириллицы") || !strings.Contains(ok, "[OK] Папка не в OneDrive") {
		t.Errorf("clean path:\n%s", ok)
	}
	bad := text(pathChecks(`C:\Users\Иван\OneDrive\Документы\ZapretGUI`, `c:\users\иван\onedrive`))
	if !strings.Contains(bad, "[?] В пути есть кириллица") || !strings.Contains(bad, "[X] Папка внутри OneDrive") {
		t.Errorf("bad path:\n%s", bad)
	}
	if pathInside(`C:\Users\Ivan\OneDrive2\x`, `C:\Users\Ivan\OneDrive`) {
		t.Error("OneDrive2 is not inside OneDrive")
	}
	if pathInside(`C:\x`, "") || !pathInside(`C:\a\b`, `C:\a\`) || !pathInside(`c:/a/b`, `C:\A`) {
		t.Error("pathInside")
	}
	if !isASCII("abc") || isASCII("абв") {
		t.Error("isASCII")
	}
}
