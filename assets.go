package main

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Everything winws.exe needs (binaries, WinDivert driver, fake packets,
// domain lists) is embedded, so the program is a single .exe.
//
//go:embed assets
var assetsFS embed.FS

const (
	ipsetPlaceholder = "203.0.113.113/32\n"
	ipsetFullAsset   = "assets/lists/ipset-all-full.txt"
	licenseAsset     = "assets/LICENSE-zapret-discord-youtube.txt"
)

// Files created once and never overwritten: they belong to the user.
// Contents are taken from :load_user_lists of service.bat.
var userListDefaults = map[string]string{
	"ipset-exclude-user.txt": "203.0.113.113/32\n",
	"list-general-user.txt":  "# Never leave this file empty\ndomain.example.abc\n",
	"list-exclude-user.txt":  "domain.example.abc\n",
}

func embeddedVersion() string {
	data, err := assetsFS.ReadFile("assets/VERSION")
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(data))
}

func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ensureAssets unpacks the bundled files into root. Non-fatal problems
// (for example a driver file locked by a running copy) are returned as
// warnings; only a missing winws.exe is an error.
func ensureAssets(root string) (warnings []string, err error) {
	binDir := filepath.Join(root, "bin")
	listsDir := filepath.Join(root, "lists")

	extract := func(srcDir, dstDir string, skip map[string]bool) {
		entries, e := fs.ReadDir(assetsFS, srcDir)
		if e != nil {
			warnings = append(warnings, e.Error())
			return
		}
		for _, ent := range entries {
			if ent.IsDir() || skip[ent.Name()] {
				continue
			}
			data, e := assetsFS.ReadFile(srcDir + "/" + ent.Name())
			if e != nil {
				warnings = append(warnings, e.Error())
				continue
			}
			if e := writeIfChanged(filepath.Join(dstDir, ent.Name()), data); e != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", ent.Name(), e))
			}
		}
	}

	if updatedActive(root) {
		// A complete update is installed: its files win over the embedded (older) ones.
	} else {
		// No update, or an interrupted/older one: restore the known-good embedded files
		// and drop leftovers of a partial update so that nothing is mixed.
		extract("assets/bin", binDir, nil)
		extract("assets/lists", listsDir, map[string]bool{"ipset-all-full.txt": true})
		for _, f := range []string{versionMarker, strategiesFile, ipsetFullUpdate} {
			_ = os.Remove(filepath.Join(root, f))
		}
	}

	if lic, e := assetsFS.ReadFile(licenseAsset); e == nil {
		_ = writeIfChanged(filepath.Join(root, "LICENSE-zapret-discord-youtube.txt"), lic)
	}

	if e := os.MkdirAll(listsDir, 0o755); e != nil {
		return warnings, e
	}
	for name, content := range userListDefaults {
		p := filepath.Join(listsDir, name)
		if _, e := os.Stat(p); os.IsNotExist(e) {
			if e := os.WriteFile(p, []byte(content), 0o644); e != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", name, e))
			}
		}
	}
	if p := filepath.Join(listsDir, "ipset-all.txt"); !exists(p) {
		_ = os.WriteFile(p, []byte(ipsetPlaceholder), 0o644)
	}

	if !exists(filepath.Join(binDir, "winws.exe")) {
		return warnings, fmt.Errorf("не удалось распаковать winws.exe в %s", binDir)
	}
	return warnings, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeIPSet applies the ipset mode exactly like :ipset_switch of service.bat:
// none = placeholder address, any = empty file, loaded = the full list.
func writeIPSet(listsDir, mode string) error {
	var data []byte
	switch mode {
	case "loaded":
		// priority: list downloaded by the user > list delivered by a program update > embedded
		root := filepath.Dir(listsDir)
		var err error
		var full []byte
		for _, name := range []string{ipsetDownloadedFile, ipsetFullUpdate} {
			if full, err = os.ReadFile(filepath.Join(root, name)); err == nil && len(full) > 0 {
				break
			}
			full = nil
		}
		if full == nil {
			if full, err = assetsFS.ReadFile(ipsetFullAsset); err != nil {
				return err
			}
		}
		data = full
	case "any":
		data = []byte{}
	default:
		data = []byte(ipsetPlaceholder)
	}
	return writeIfChanged(filepath.Join(listsDir, "ipset-all.txt"), data)
}
