package icon

import (
	"bytes"
	"image/png"
	"testing"
)

func TestIcons(t *testing.T) {
	tmpl, err := png.Decode(bytes.NewReader(Template()))
	if err != nil || tmpl.Bounds().Dx() != 64 {
		t.Fatalf("template: %v %v", tmpl.Bounds(), err)
	}
	// Black on transparent: the corner is empty, the ring is drawn.
	if _, _, _, a := tmpl.At(0, 0).RGBA(); a != 0 {
		t.Error("template corner not transparent")
	}
	inked := 0
	for y := range 64 {
		for x := range 64 {
			if r, g, b, a := tmpl.At(x, y).RGBA(); a > 0 {
				inked++
				if r != 0 || g != 0 || b != 0 {
					t.Fatalf("template pixel (%d,%d) is not black", x, y)
				}
			}
		}
	}
	if inked < 400 || inked > 2500 {
		t.Errorf("template has %d inked pixels; the glyph looks wrong", inked)
	}
	app, err := png.Decode(bytes.NewReader(App(128)))
	if err != nil || app.Bounds().Dx() != 128 {
		t.Fatalf("app icon: %v", err)
	}
	if _, _, _, a := app.At(64, 4).RGBA(); a != 0 {
		t.Error("app icon not inset")
	}
	if _, _, _, a := app.At(64, 64).RGBA(); a == 0 {
		t.Error("app icon centre is empty")
	}
}
