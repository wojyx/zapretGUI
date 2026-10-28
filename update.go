package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Update source: the same repository the bundled files come from.
var (
	updateVersionURL = "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/.service/version.txt"
	updateZipURL     = "https://github.com/Flowseal/zapret-discord-youtube/archive/refs/heads/main.zip"
)

const (
	maxZipBytes  = 80 << 20
	maxFileBytes = 30 << 20

	versionMarker   = "installed-version.txt"
	strategiesFile  = "strategies.json"
	ipsetFullUpdate = "ipset-all-full.txt"
)

var versionRe = regexp.MustCompile(`^\d+(\.\d+)*$`)

// ---- versions -----------------------------------------------------------------

func parseVersion(s string) ([]int, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if !versionRe.MatchString(s) {
		return nil, false
	}
	var out []int
	for _, p := range strings.Split(s, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// versionNewer reports whether a is strictly newer than b. Unparsable input is never newer.
func versionNewer(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < len(va) || i < len(vb); i++ {
		var x, y int
		if i < len(va) {
			x = va[i]
		}
		if i < len(vb) {
			y = vb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// ---- .bat -> winws arguments (port of tools/gen_strategies.py) ------------------

var (
	batVarRe  = regexp.MustCompile(`%([^%\s]*)%`)
	batMetaRe = regexp.MustCompile(`[\^!&|<>]`)
	binRefRe  = regexp.MustCompile(`%BIN%([^"\s]+)`)
)

var allowedBatVars = map[string]bool{"BIN": true, "LISTS": true, "GameFilterTCP": true, "GameFilterUDP": true, "GameFilter": true}

func strategyArgsFromBat(text string) (string, error) {
	text = strings.TrimPrefix(text, "\xEF\xBB\xBF")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.Contains(l, `winws.exe"`) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("нет строки запуска winws.exe")
	}
	parts := []string{strings.TrimSpace(strings.SplitN(lines[start], `winws.exe"`, 2)[1])}
	i := start
	for strings.HasSuffix(strings.TrimRight(lines[i], " \t"), "^") {
		last := len(parts) - 1
		parts[last] = strings.TrimSpace(strings.TrimSuffix(strings.TrimRight(parts[last], " \t"), "^"))
		i++
		if i >= len(lines) {
			return "", fmt.Errorf("обрыв строки продолжения (^)")
		}
		parts = append(parts, strings.TrimSpace(lines[i]))
	}
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	args := strings.Join(nonEmpty, " ")

	for _, m := range batVarRe.FindAllStringSubmatch(args, -1) {
		if !allowedBatVars[m[1]] {
			return "", fmt.Errorf("неподдерживаемая переменная %%%s%%", m[1])
		}
	}
	// `^!` is the batch escape for a literal `!`; anything else cmd would interpret is rejected.
	if batMetaRe.MatchString(strings.ReplaceAll(args, "^!", "")) {
		return "", fmt.Errorf("неожиданный управляющий символ cmd")
	}
	args = strings.ReplaceAll(args, "^!", "!")
	for _, loc := range regexp.MustCompile(`%(?:BIN|LISTS)%`).FindAllStringIndex(args, -1) {
		if loc[0] == 0 || args[loc[0]-1] != '"' {
			return "", fmt.Errorf("путь без кавычек возле: %.40s", args[loc[0]:])
		}
	}
	if !strings.Contains(args, "--wf-tcp") && !strings.Contains(args, "--wf-udp") {
		return "", fmt.Errorf("нет фильтра WinDivert (--wf-*)")
	}
	return args, nil
}

// naturalTokens splits "general (ALT10)" into alternating text / number tokens.
func naturalTokens(name string) []string {
	name = strings.ToLower(strings.ReplaceAll(name, ")", ""))
	var out []string
	cur := strings.Builder{}
	digit := false
	flush := func() { out = append(out, cur.String()); cur.Reset() }
	for _, r := range name {
		isD := r >= '0' && r <= '9'
		if isD != digit {
			flush()
			digit = isD
		}
		cur.WriteRune(r)
	}
	flush()
	if digit { // keep strict alternation text, number, text, ...
		out = append(out, "")
	}
	return out
}

func naturalLess(a, b string) bool {
	ta, tb := naturalTokens(a), naturalTokens(b)
	for i := 0; i < len(ta) && i < len(tb); i++ {
		if ta[i] == tb[i] {
			continue
		}
		if i%2 == 1 {
			x, _ := strconv.Atoi(ta[i])
			y, _ := strconv.Atoi(tb[i])
			if x != y {
				return x < y
			}
			continue
		}
		return ta[i] < tb[i]
	}
	return len(ta) < len(tb)
}

func sortStrategies(list []Strategy) {
	sort.SliceStable(list, func(i, j int) bool { return naturalLess(list[i].Name, list[j].Name) })
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name == "general" && list[j].Name != "general" })
}

// ---- reading the update archive ---------------------------------------------------

type updatePayload struct {
	Version    string
	Strategies []Strategy
	// Files maps a path relative to the data dir ("bin/winws.exe", "lists/list-general.txt",
	// ipset-all-full.txt, ...) to its contents.
	Files map[string][]byte
}

var binExts = map[string]bool{".exe": true, ".dll": true, ".sys": true, ".bin": true}

func readZipFile(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxFileBytes {
		return nil, fmt.Errorf("%s: файл слишком большой", f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("%s: файл слишком большой", f.Name)
	}
	return data, nil
}

// parseUpdateZip accepts a GitHub source archive (single top-level folder) and extracts
// only a fixed whitelist of files. It validates everything before anything touches the disk.
func parseUpdateZip(data []byte) (*updatePayload, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("архив повреждён: %w", err)
	}
	p := &updatePayload{Files: map[string][]byte{}}
	bats := map[string]string{}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			return nil, fmt.Errorf("небезопасный путь в архиве: %q", f.Name)
		}
		i := strings.Index(name, "/")
		if i < 0 {
			continue
		}
		rel := path.Clean(name[i+1:]) // drop the "<repo>-main/" prefix

		switch {
		case strings.HasPrefix(rel, "bin/") && strings.Count(rel, "/") == 1 && binExts[strings.ToLower(path.Ext(rel))]:
			b, err := readZipFile(f)
			if err != nil {
				return nil, err
			}
			p.Files[rel] = b
		case strings.HasPrefix(rel, "lists/") && strings.Count(rel, "/") == 1 && !strings.Contains(rel, "-user"):
			base := path.Base(rel)
			switch {
			case base == "ipset-all.txt.backup":
				b, err := readZipFile(f)
				if err != nil {
					return nil, err
				}
				p.Files[ipsetFullUpdate] = b
			case base == "ipset-exclude.txt" || (strings.HasPrefix(base, "list-") && strings.HasSuffix(base, ".txt")):
				b, err := readZipFile(f)
				if err != nil {
					return nil, err
				}
				p.Files[rel] = b
			}
		case strings.HasPrefix(rel, "general") && strings.HasSuffix(rel, ".bat") && !strings.Contains(rel, "/"):
			b, err := readZipFile(f)
			if err != nil {
				return nil, err
			}
			bats[strings.TrimSuffix(rel, ".bat")] = string(b)
		case rel == ".service/version.txt":
			b, err := readZipFile(f)
			if err != nil {
				return nil, err
			}
			p.Version = strings.TrimSpace(string(b))
		case rel == "LICENSE.txt":
			b, err := readZipFile(f)
			if err != nil {
				return nil, err
			}
			p.Files["LICENSE-zapret-discord-youtube.txt"] = b
		}
	}

	if _, ok := parseVersion(p.Version); !ok {
		return nil, fmt.Errorf("в архиве нет корректной версии (.service/version.txt)")
	}
	for _, need := range []string{"bin/winws.exe", "bin/WinDivert.dll", "bin/WinDivert64.sys", "bin/cygwin1.dll"} {
		b := p.Files[need]
		if len(b) < 1024 {
			return nil, fmt.Errorf("в архиве нет файла %s", need)
		}
		if !strings.HasSuffix(need, ".sys") && string(b[:2]) != "MZ" {
			return nil, fmt.Errorf("%s не похож на исполняемый файл Windows", need)
		}
	}
	if len(p.Files[ipsetFullUpdate]) == 0 {
		return nil, fmt.Errorf("в архиве нет полного списка ipset")
	}

	for name, text := range bats {
		args, err := strategyArgsFromBat(text)
		if err != nil {
			return nil, fmt.Errorf("%s.bat: %w", name, err)
		}
		for _, m := range binRefRe.FindAllStringSubmatch(args, -1) {
			if _, ok := p.Files["bin/"+m[1]]; !ok {
				return nil, fmt.Errorf("%s.bat ссылается на отсутствующий файл bin/%s", name, m[1])
			}
		}
		p.Strategies = append(p.Strategies, Strategy{Name: name, Args: args})
	}
	if len(p.Strategies) == 0 {
		return nil, fmt.Errorf("в архиве нет стратегий (general*.bat)")
	}
	sortStrategies(p.Strategies)
	return p, nil
}

// ---- network --------------------------------------------------------------------------

func httpGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://127.0.0.1") {
		return nil, fmt.Errorf("разрешён только HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ZapretGUI/"+appVersion)
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("сервер ответил %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("ответ слишком большой")
	}
	return data, nil
}

// fetchLatestVersion returns the version published in the repository.
func fetchLatestVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	b, err := httpGet(ctx, updateVersionURL, 256)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if _, ok := parseVersion(v); !ok {
		return "", fmt.Errorf("сервер вернул непонятную версию %q", v)
	}
	return strings.TrimPrefix(v, "v"), nil
}

func downloadUpdate(ctx context.Context) (*updatePayload, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	b, err := httpGet(ctx, updateZipURL, maxZipBytes)
	if err != nil {
		return nil, err
	}
	return parseUpdateZip(b)
}

// ---- installed state --------------------------------------------------------------------

// updatedActive tells whether the data dir holds a complete, newer-than-embedded update.
func updatedActive(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, versionMarker))
	if err != nil || !versionNewer(string(b), embeddedVersion()) {
		return false
	}
	return exists(filepath.Join(root, strategiesFile)) && exists(filepath.Join(root, "bin", "winws.exe"))
}

// installedVersion is the version of the files that will actually be used.
func installedVersion(root string) string {
	if updatedActive(root) {
		b, _ := os.ReadFile(filepath.Join(root, versionMarker))
		return strings.TrimSpace(string(b))
	}
	return embeddedVersion()
}

// loadStrategiesFrom prefers strategies installed by an update; the embedded set is the fallback.
func loadStrategiesFrom(root string) ([]Strategy, error) {
	if updatedActive(root) {
		if b, err := os.ReadFile(filepath.Join(root, strategiesFile)); err == nil {
			var list []Strategy
			if json.Unmarshal(b, &list) == nil && len(list) > 0 {
				return list, nil
			}
		}
	}
	return loadStrategies()
}

// installUpdate writes the payload into the data dir. winws.exe must not be running.
// The version marker is removed first and written last, so an interrupted update
// is detected at the next start and the embedded known-good files are restored.
func installUpdate(root string, p *updatePayload) error {
	marker := filepath.Join(root, versionMarker)
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	// the bundle brings its own full IP list, which is at least as new as an earlier download
	_ = os.Remove(filepath.Join(root, ipsetDownloadedFile))
	names := make([]string, 0, len(p.Files))
	for n := range p.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := writeIfChanged(filepath.Join(root, filepath.FromSlash(n)), p.Files[n]); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
	}
	js, err := json.MarshalIndent(p.Strategies, "", " ")
	if err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(root, strategiesFile), js); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(p.Version), 0o644)
}
