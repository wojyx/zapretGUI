package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

const appVersion = "1.3"

// strategies.json is generated from the general*.bat files of
// Flowseal/zapret-discord-youtube by tools/gen_strategies.py.
//
//go:embed strategies.json
var strategiesJSON []byte

// Strategy is one winws.exe argument set (one general*.bat).
// Args still contain %BIN%, %LISTS%, %GameFilterTCP% and %GameFilterUDP%.
type Strategy struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

func loadStrategies() ([]Strategy, error) {
	var list []Strategy
	if err := json.Unmarshal(strategiesJSON, &list); err != nil {
		return nil, fmt.Errorf("strategies.json: %w", err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("strategies.json is empty")
	}
	return list, nil
}

const gameRange = "1024-65535"

// gameFilterPorts mirrors :game_switch_status of service.bat.
// "12" is the value the original scripts use for "filter disabled".
func gameFilterPorts(mode string) (tcp, udp string) {
	tcp, udp = "12", "12"
	switch mode {
	case "all":
		tcp, udp = gameRange, gameRange
	case "tcp":
		tcp = gameRange
	case "udp":
		udp = gameRange
	}
	return tcp, udp
}

// buildCmdLine returns the exact Windows command line for winws.exe.
// The quoting of the .bat files is kept as is, so paths with spaces
// (a user name with a space in %LOCALAPPDATA%) keep working.
func buildCmdLine(s Strategy, binDir, listsDir, gameMode string) string {
	tcp, udp := gameFilterPorts(gameMode)
	gameFilter := tcp
	if gameMode == "udp" {
		gameFilter = udp
	}
	r := strings.NewReplacer(
		"%BIN%", binDir+`\`,
		"%LISTS%", listsDir+`\`,
		"%GameFilterTCP%", tcp,
		"%GameFilterUDP%", udp,
		"%GameFilter%", gameFilter,
	)
	return `"` + binDir + `\winws.exe" ` + r.Replace(s.Args)
}

// Option lists shown in the UI; the index of a value is the index of its label.
var (
	gameModes  = []string{"off", "all", "tcp", "udp"}
	gameLabels = []string{"Выключен", "TCP и UDP", "Только TCP", "Только UDP"}

	ipsetModes  = []string{"none", "loaded", "any"}
	ipsetLabels = []string{"Отключён (по умолчанию)", "Загруженный список", "Все адреса (any)"}
)

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}
