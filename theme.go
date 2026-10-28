package main

import "math"

// RGB is a colour as 0xRRGGBB.
type RGB uint32

// COLORREF returns the Win32 byte order (0x00BBGGRR).
func (c RGB) COLORREF() uintptr {
	r, g, b := uintptr(c>>16&0xFF), uintptr(c>>8&0xFF), uintptr(c&0xFF)
	return r | g<<8 | b<<16
}

// Palette holds every colour the window uses.
type Palette struct {
	Bg, Panel                        RGB // window / log field
	Text, Muted, Disabled            RGB
	Border                           RGB
	Button, ButtonDown, ButtonDimmed RGB
	Accent                           RGB // focus ring, checked box
	OK, Warn, Idle                   RGB // status line
}

var lightPalette = Palette{
	Bg: 0xFFFFFF, Panel: 0xFFFFFF,
	Text: 0x111827, Muted: 0x6B7280, Disabled: 0x9CA3AF,
	Border: 0xC4C9D0,
	Button: 0xF3F4F6, ButtonDown: 0xE5E7EB, ButtonDimmed: 0xF9FAFB,
	Accent: 0x2563EB,
	OK:     0x15803D, Warn: 0xB45309, Idle: 0x6B7280,
}

var darkPalette = Palette{
	Bg: 0x202124, Panel: 0x2B2D31,
	Text: 0xE8EAED, Muted: 0x9AA0A6, Disabled: 0x7C8187,
	Border: 0x4A4D52,
	Button: 0x34373C, ButtonDown: 0x474B52, ButtonDimmed: 0x2A2C30,
	Accent: 0x4C8DF6,
	OK:     0x4ADE80, Warn: 0xFBBF24, Idle: 0x9AA0A6,
}

// Theme settings stored in config.json.
const (
	themeAuto  = "auto"
	themeDark  = "dark"
	themeLight = "light"
)

var themeOrder = []string{themeAuto, themeDark, themeLight}

func validTheme(s string) bool { return indexOf(themeOrder, s) >= 0 }

// nextTheme cycles auto -> dark -> light -> auto.
func nextTheme(s string) string {
	i := max(indexOf(themeOrder, s), 0) // unknown value is treated like "auto"
	return themeOrder[(i+1)%len(themeOrder)]
}

func themeLabel(s string) string {
	switch s {
	case themeDark:
		return "Тема: Тёмная"
	case themeLight:
		return "Тема: Светлая"
	}
	return "Тема: Авто"
}

// resolveTheme turns the setting into "use dark colours?"; auto follows Windows.
func resolveTheme(setting string, systemDark bool) bool {
	switch setting {
	case themeDark:
		return true
	case themeLight:
		return false
	}
	return systemDark
}

func paletteFor(dark bool) Palette {
	if dark {
		return darkPalette
	}
	return lightPalette
}

// contrast is the WCAG contrast ratio between two colours.
func contrast(a, b RGB) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c RGB) float64 {
	ch := func(v uint32) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(uint32(c>>16&0xFF)) + 0.7152*ch(uint32(c>>8&0xFF)) + 0.0722*ch(uint32(c&0xFF))
}
