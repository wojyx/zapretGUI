#!/usr/bin/env bash
# Rebuilds dist/ZapretGUI.exe, optionally from a fresh checkout of
# https://github.com/Flowseal/zapret-discord-youtube
#
#   ./build.sh                 # rebuild with the bundled strategies/binaries
#   ./build.sh /path/to/repo   # refresh strategies, binaries and lists from a checkout first
#
# Needs: Go 1.22+, Python 3 with Pillow (only for the icons). Works on Linux,
# macOS and Windows (Git Bash); no C compiler, no Windows SDK.
set -euo pipefail
cd "$(dirname "$0")"

if [[ $# -ge 1 ]]; then
  repo="$1"
  python3 -I tools/gen_strategies.py "$repo" strategies.json
  cp "$repo"/bin/* assets/bin/
  cp "$repo"/lists/list-*.txt "$repo"/lists/ipset-exclude.txt assets/lists/
  cp "$repo"/lists/ipset-all.txt.backup assets/lists/ipset-all-full.txt
  cp "$repo"/LICENSE.txt assets/LICENSE-zapret-discord-youtube.txt
  tr -d '\r\n' < "$repo"/.service/version.txt > assets/VERSION
fi

python3 -I tools/mkrsrc.py rsrc_windows_amd64.syso
go test ./...
mkdir -p dist
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w -H windowsgui" -o dist/ZapretGUI.exe .
echo "built dist/ZapretGUI.exe"
