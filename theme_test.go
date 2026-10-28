package main

import "testing"

func TestThemeCycleAndResolve(t *testing.T) {
	if nextTheme(themeAuto) != themeDark || nextTheme(themeDark) != themeLight || nextTheme(themeLight) != themeAuto {
		t.Fatal("cycle must be auto -> dark -> light -> auto")
	}
	if nextTheme("garbage") != themeDark {
		t.Fatal("unknown value must restart the cycle")
	}
	cases := []struct {
		setting    string
		systemDark bool
		want       bool
	}{
		{themeAuto, true, true}, {themeAuto, false, false},
		{themeDark, false, true}, {themeLight, true, false}, {"", true, true},
	}
	for _, c := range cases {
		if got := resolveTheme(c.setting, c.systemDark); got != c.want {
			t.Errorf("resolveTheme(%q,%v)=%v want %v", c.setting, c.systemDark, got, c.want)
		}
	}
	for _, s := range themeOrder {
		if themeLabel(s) == "" || !validTheme(s) {
			t.Errorf("bad theme %q", s)
		}
	}
}

func TestCOLORREFOrder(t *testing.T) {
	if got := RGB(0x112233).COLORREF(); got != 0x332211 {
		t.Fatalf("COLORREF = %#x, want 0x332211", got)
	}
}

// Readability is checked numerically (WCAG contrast) for both palettes.
func TestPaletteContrast(t *testing.T) {
	for name, p := range map[string]Palette{"light": lightPalette, "dark": darkPalette} {
		checks := []struct {
			what string
			a, b RGB
			min  float64
		}{
			{"text on window", p.Text, p.Bg, 7},
			{"text on log field", p.Text, p.Panel, 7},
			{"text on button", p.Text, p.Button, 7},
			{"text on pressed button", p.Text, p.ButtonDown, 4.5},
			{"muted text on window", p.Muted, p.Bg, 4.5},
			{"status OK on window", p.OK, p.Bg, 4.5},
			{"status warning on window", p.Warn, p.Bg, 4.5},
			{"status idle on window", p.Idle, p.Bg, 4.5},
			{"focus/check accent on window", p.Accent, p.Bg, 3},
			{"button edge on window", p.Border, p.Bg, 1.3},
		}
		for _, c := range checks {
			if r := contrast(c.a, c.b); r < c.min {
				t.Errorf("%s palette: %s contrast %.2f < %.1f", name, c.what, r, c.min)
			}
		}
	}
	if contrast(darkPalette.Bg, lightPalette.Bg) < 10 {
		t.Error("the two themes must actually differ")
	}
}
