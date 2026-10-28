package main

import (
	"strings"
	"unicode/utf16"
)

const autostartTask = "ZapretGUI"

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// autostartTaskXML describes a logon task that runs the program elevated
// without a UAC prompt. XML is used instead of plain schtasks switches because
// only XML can allow starting on battery power (laptops) and lift the
// 72-hour execution limit.
func autostartTaskXML(exe, userID string) string {
	userTag := ""
	if userID != "" {
		userTag = "<UserId>" + xmlEscape(userID) + "</UserId>"
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Zapret GUI: запуск при входе в Windows</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled>` + userTag + `<Delay>PT10S</Delay></LogonTrigger></Triggers>
  <Principals><Principal id="Author">` + userTag + `<LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author"><Exec><Command>` + xmlEscape(exe) + `</Command><Arguments>--autostart</Arguments></Exec></Actions>
</Task>
`
}

// utf16LEWithBOM is the encoding schtasks /XML expects.
func utf16LEWithBOM(s string) []byte {
	u := utf16.Encode([]rune(s))
	buf := make([]byte, 0, 2+len(u)*2)
	buf = append(buf, 0xFF, 0xFE)
	for _, v := range u {
		buf = append(buf, byte(v), byte(v>>8))
	}
	return buf
}
