#!/usr/bin/env python3
"""Extract winws.exe arguments from every general*.bat of the zapret repo.

Usage: python3 -I gen_strategies.py <repo_dir> <out_strategies.json>

The .bat files are the single source of truth for strategies. We join the
`^` line continuations, keep only the argument string and verify that the
only batch variables left are the ones the Go program substitutes at runtime.
"""
import json
import re
import sys
from pathlib import Path

ALLOWED_VARS = {"BIN", "LISTS", "GameFilterTCP", "GameFilterUDP", "GameFilter"}


def natural_key(name: str):
    # drop ")" so that "general (ALT)" sorts before "general (ALT2)"
    return [int(t) if t.isdigit() else t.lower() for t in re.split(r"(\d+)", name.replace(")", ""))]


def extract_args(path: Path) -> str:
    lines = path.read_text(encoding="utf-8-sig").replace("\r\n", "\n").split("\n")
    start = next((i for i, l in enumerate(lines) if 'winws.exe"' in l), None)
    if start is None:
        raise ValueError(f"{path.name}: no winws.exe launch line")
    first = lines[start]
    parts = [first.split('winws.exe"', 1)[1].strip()]
    i = start
    while lines[i].rstrip().endswith("^"):
        parts[-1] = parts[-1].rstrip()[:-1].rstrip()
        i += 1
        parts.append(lines[i].strip())
    args = " ".join(p for p in parts if p)

    for var in re.findall(r"%([^%\s]*)%", args):
        if var not in ALLOWED_VARS:
            raise ValueError(f"{path.name}: unsupported variable %{var}%")
    # `^!` is the batch escape for a literal `!` (what winws really receives);
    # anything else that cmd would interpret is treated as an error.
    if re.search(r"[\^!&|<>]", args.replace("^!", "")):
        raise ValueError(f"{path.name}: unexpected batch metacharacter in args")
    args = args.replace("^!", "!")
    # every %BIN%/%LISTS% must sit inside a quoted value (paths may contain spaces)
    for m in re.finditer(r"%(?:BIN|LISTS)%", args):
        if args[m.start() - 1] != '"':
            raise ValueError(f"{path.name}: unquoted path variable near: {args[m.start()-20:m.end()+20]}")
    if "--wf-tcp" not in args and "--wf-udp" not in args:
        raise ValueError(f"{path.name}: no WinDivert filter (--wf-*) found")
    return args


def main() -> None:
    repo, out = Path(sys.argv[1]), Path(sys.argv[2])
    bats = sorted(repo.glob("general*.bat"), key=lambda p: natural_key(p.stem))
    # plain "general" first, then the rest in natural order
    bats.sort(key=lambda p: (p.stem != "general",))
    strategies = [{"name": b.stem, "args": extract_args(b)} for b in bats]
    out.write_text(json.dumps(strategies, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    print(f"{len(strategies)} strategies -> {out}")
    for s in strategies:
        print("  -", s["name"], f"({len(s['args'])} chars)")


if __name__ == "__main__":
    main()
