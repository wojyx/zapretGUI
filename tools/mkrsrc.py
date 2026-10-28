#!/usr/bin/env python3
"""Generate rsrc_windows_amd64.syso (manifest + icons) for the Go linker.

Usage: python3 -I mkrsrc.py <out.syso> [preview_dir]

Resource IDs:
  RT_GROUP_ICON 1 = blue  (application / window icon)
  RT_GROUP_ICON 2 = green (tray: protection running)
  RT_GROUP_ICON 3 = gray  (tray: stopped)
  RT_MANIFEST   1 = requireAdministrator + Common Controls v6 + system DPI aware

The .syso layout (one .rsrc section + ADDR32NB relocations on every
IMAGE_RESOURCE_DATA_ENTRY) is the same one akavel/rsrc produces, which the Go
linker is known to accept.
"""
import io
import struct
import sys
from pathlib import Path

from PIL import Image, ImageDraw

MANIFEST = """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="ZapretGUI" version="1.0.0.0" processorArchitecture="amd64"/>
  <description>Zapret GUI</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="requireAdministrator" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true</dpiAware>
    </windowsSettings>
  </application>
</assembly>
"""

SIZES = [16, 24, 32, 48, 64, 256]


def draw_icon(color: str) -> Image.Image:
    """Rounded square with a white shield and a coloured Z, drawn at 4x then reduced."""
    n = 1024
    img = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    d.rounded_rectangle([40, 40, n - 40, n - 40], radius=220, fill=color)
    # shield
    shield = [(512, 150), (800, 250), (800, 540), (512, 880), (224, 540), (224, 250)]
    d.polygon(shield, fill=(255, 255, 255, 255))
    # Z
    z = [(380, 330), (644, 330), (380, 590), (644, 590)]
    d.line(z, fill=color, width=80, joint="curve")
    for x, y in (z[0], z[1], z[2], z[3]):
        d.ellipse([x - 40, y - 40, x + 40, y + 40], fill=color)
    return img


def dib_frame(img: Image.Image) -> bytes:
    """32bpp BGRA DIB (bottom-up) + 1bpp AND mask, as stored in RT_ICON."""
    w, h = img.size
    px = img.load()
    header = struct.pack("<IiiHHIIiiII", 40, w, h * 2, 1, 32, 0, w * h * 4, 0, 0, 0, 0)
    pixels = bytearray()
    for y in range(h - 1, -1, -1):
        for x in range(w):
            r, g, b, a = px[x, y]
            pixels += bytes((b, g, r, a))
    row_bytes = ((w + 31) // 32) * 4
    mask = bytearray()
    for y in range(h - 1, -1, -1):
        row = bytearray(row_bytes)
        for x in range(w):
            if px[x, y][3] < 128:
                row[x // 8] |= 0x80 >> (x % 8)
        mask += row
    return header + bytes(pixels) + bytes(mask)


def build_icon_group(color: str, first_icon_id: int):
    """Return (group_resource_bytes, {icon_id: frame_bytes})."""
    base = draw_icon(color)
    entries, frames = [], {}
    for i, s in enumerate(SIZES):
        im = base.resize((s, s), Image.LANCZOS)
        if s == 256:
            buf = io.BytesIO()
            im.save(buf, "PNG")
            data = buf.getvalue()
        else:
            data = dib_frame(im)
        icon_id = first_icon_id + i
        frames[icon_id] = data
        entries.append(struct.pack("<BBBBHHIH", 0 if s == 256 else s, 0 if s == 256 else s, 0, 0, 1, 32, len(data), icon_id))
    group = struct.pack("<HHH", 0, 1, len(entries)) + b"".join(entries)
    return group, frames


def align(n: int, a: int = 8) -> int:
    return (n + a - 1) & ~(a - 1)


def build_rsrc_section(resources: dict):
    """resources: {type: {id: bytes}} -> (section bytes, [offsets of data entries])."""
    types = sorted(resources)
    # ---- layout -------------------------------------------------------------
    off = 16 + 8 * len(types)  # root directory
    type_dir_off, lang_dir_off, entry_off = {}, {}, {}
    for t in types:
        type_dir_off[t] = off
        off += 16 + 8 * len(resources[t])
    for t in types:
        for rid in sorted(resources[t]):
            lang_dir_off[(t, rid)] = off
            off += 16 + 8
    for t in types:
        for rid in sorted(resources[t]):
            entry_off[(t, rid)] = off
            off += 16
    blob_off = {}
    for t in types:
        for rid in sorted(resources[t]):
            off = align(off)
            blob_off[(t, rid)] = off
            off += len(resources[t][rid])
    total = align(off)

    # ---- emit ---------------------------------------------------------------
    out = bytearray(total)

    def put(pos: int, data: bytes):
        out[pos:pos + len(data)] = data

    def directory(pos: int, entries):
        put(pos, struct.pack("<IIHHHH", 0, 0, 0, 0, 0, len(entries)))
        for i, (ident, target, is_dir) in enumerate(entries):
            put(pos + 16 + 8 * i, struct.pack("<II", ident, target | (0x80000000 if is_dir else 0)))

    directory(0, [(t, type_dir_off[t], True) for t in types])
    relocs = []
    for t in types:
        directory(type_dir_off[t], [(rid, lang_dir_off[(t, rid)], True) for rid in sorted(resources[t])])
        for rid in sorted(resources[t]):
            directory(lang_dir_off[(t, rid)], [(0x0409, entry_off[(t, rid)], False)])
            data = resources[t][rid]
            put(entry_off[(t, rid)], struct.pack("<IIII", blob_off[(t, rid)], len(data), 0, 0))
            put(blob_off[(t, rid)], data)
            relocs.append(entry_off[(t, rid)])
    return bytes(out), relocs


def build_syso(resources: dict) -> bytes:
    section, relocs = build_rsrc_section(resources)
    raw_ptr = 20 + 40
    reloc_ptr = raw_ptr + len(section)
    sym_ptr = reloc_ptr + 10 * len(relocs)
    file_header = struct.pack("<HHIIIHH", 0x8664, 1, 0, sym_ptr, 1, 0, 0)
    section_header = struct.pack(
        "<8sIIIIIIHHI", b".rsrc", 0, 0, len(section), raw_ptr, reloc_ptr, 0, len(relocs), 0, 0xC0000040
    )
    reloc_table = b"".join(struct.pack("<IIH", r, 0, 3) for r in relocs)  # IMAGE_REL_AMD64_ADDR32NB
    symbol = struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)
    string_table = struct.pack("<I", 4)
    return file_header + section_header + section + reloc_table + symbol + string_table


def main() -> None:
    out = Path(sys.argv[1])
    preview = Path(sys.argv[2]) if len(sys.argv) > 2 else None

    resources = {3: {}, 14: {}, 24: {}}
    for gid, color in ((1, "#2563EB"), (2, "#16A34A"), (3, "#6B7280")):
        group, frames = build_icon_group(color, gid * 100)
        resources[14][gid] = group
        resources[3].update(frames)
        if preview:
            preview.mkdir(parents=True, exist_ok=True)
            draw_icon(color).resize((256, 256), Image.LANCZOS).save(preview / f"icon{gid}.png")
    resources[24][1] = MANIFEST.encode("utf-8")

    out.write_bytes(build_syso(resources))
    print(f"wrote {out} ({out.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
