package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// iconProblem says what is wrong with an app icon file, or "" when it is one the
// App Store takes: a PNG, 1024 by 1024, with no transparency.
func iconProblem(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return "not a PNG: " + err.Error()
	}
	if b := img.Bounds(); b.Dx() != 1024 || b.Dy() != 1024 {
		return fmt.Sprintf("it is %d by %d, want 1024 by 1024", b.Dx(), b.Dy())
	}
	if o, ok := img.(interface{ Opaque() bool }); !ok || !o.Opaque() {
		return "it has transparency"
	}
	return ""
}

// writeIcon writes a PNG of the given size; clear makes one pixel transparent.
func writeIcon(t *testing.T, size int, clear bool) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	if clear {
		img.SetNRGBA(3, 3, color.NRGBA{A: 0})
	}
	p := filepath.Join(t.TempDir(), "icon.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

// The app's icon is made by app/icon (a render, then compose.py). An upload is
// refused for an icon of another size or with any transparency, and a refused
// upload is found only at release, so the asset is checked here.
func TestAppIcon_Is1024SquareWithNoTransparency(t *testing.T) {
	icon := filepath.Join(repoRootForWorkflows(t), "app", "InitechApp", "Assets.xcassets", "AppIcon.appiconset", "AppIcon-1024.png")
	if p := iconProblem(icon); p != "" {
		t.Errorf("the app icon: %s", p)
	}
}

// The check itself: each fault is named, and a good icon passes.
func TestIconProblem_NamesAWrongSizeAndTransparency(t *testing.T) {
	if p := iconProblem(writeIcon(t, 1024, false)); p != "" {
		t.Errorf("a good icon was refused: %s", p)
	}
	if p := iconProblem(writeIcon(t, 512, false)); !strings.Contains(p, "512 by 512") {
		t.Errorf("a 512 icon: %q, want its size named", p)
	}
	if p := iconProblem(writeIcon(t, 1024, true)); p != "it has transparency" {
		t.Errorf("an icon with a clear pixel: %q, want transparency named", p)
	}
	if p := iconProblem(filepath.Join(t.TempDir(), "none.png")); p == "" {
		t.Error("a missing icon passed")
	}
}
