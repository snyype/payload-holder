package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // logos may be JPEG
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Client logos: a top-level folder can have a PNG/JPEG logo instead of an emoji. payload keeps a
// small copy (at most logoStoreSize pixels a side, PNG) in vault/ssh-logos/<id>.png and draws it in
// the terminal with "▀" half blocks — each character cell shows two pixels, top one as the text
// colour and bottom one as the background — so it works in any terminal with colour, no image
// protocol needed. Transparent pixels show the terminal's own background.

const logoStoreSize = 48

func sshLogoDir() string {
	return filepath.Join(storeDir(), "vault", "ssh-logos")
}

func sshLogoPath(file string) string {
	return filepath.Join(sshLogoDir(), filepath.Base(file))
}

// validLogoFile accepts the names payload gives logos (<hex id>.png).
func validLogoFile(name string) bool {
	id, ok := strings.CutSuffix(name, ".png")
	return ok && len(id) >= 6 && len(id) <= 32 && strings.Trim(id, "0123456789abcdef") == ""
}

// importLogo reads an image file, shrinks it and stores it as the folder's logo; it returns the
// stored file name.
func importLogo(src, id string) (string, error) {
	f, err := os.Open(strings.Trim(strings.TrimSpace(src), `"'`))
	if err != nil {
		return "", err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("not a PNG or JPEG image (%v)", err)
	}
	b := img.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 {
		return "", errors.New("the image is empty")
	}
	scale := min(1, float64(logoStoreSize)/float64(max(b.Dx(), b.Dy())))
	small := resample(img, max(1, int(float64(b.Dx())*scale+0.5)), max(1, int(float64(b.Dy())*scale+0.5)))
	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		return "", err
	}
	if err := os.MkdirAll(sshLogoDir(), 0o755); err != nil {
		return "", err
	}
	name := id + ".png"
	return name, os.WriteFile(sshLogoPath(name), buf.Bytes(), 0o644)
}

// checkLogoFile is the form's check for a logo path.
func checkLogoFile(src string) error {
	src = strings.Trim(strings.TrimSpace(src), `"'`)
	if src == "" {
		return errors.New("enter the path of a PNG or JPEG file")
	}
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("can't open it: %v", err)
	}
	defer f.Close()
	if _, _, err := image.DecodeConfig(f); err != nil {
		return errors.New("not a PNG or JPEG image")
	}
	return nil
}

// resample scales img to w×h by averaging the source pixels under each target pixel (alpha-weighted).
func resample(img image.Image, w, h int) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+max((y+1)*b.Dy()/h, y*b.Dy()/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+max((x+1)*b.Dx()/w, x*b.Dx()/w+1)
			var r, g, bl, a, n float64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
					af := float64(c.A)
					r += float64(c.R) * af
					g += float64(c.G) * af
					bl += float64(c.B) * af
					a += af
					n++
				}
			}
			if a == 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(bl / a), uint8(a / n)})
		}
	}
	return out
}

// logoLines draws a logo cols characters wide and rows lines tall (2·rows pixels), keeping its shape
// centred; nil if it can't be read.
func logoLines(file string, cols, rows int) []string {
	f, err := os.Open(sshLogoPath(file))
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil
	}
	b := img.Bounds()
	// Fit into cols×(2·rows) pixels, keeping the aspect ratio.
	pw, ph := cols, 2*rows
	scale := min(float64(pw)/float64(b.Dx()), float64(ph)/float64(b.Dy()))
	w, h := max(1, int(float64(b.Dx())*scale+0.5)), max(1, int(float64(b.Dy())*scale+0.5))
	small := resample(img, w, h)
	ox, oy := (pw-w)/2, (ph-h)/2
	at := func(x, y int) (color.NRGBA, bool) {
		x, y = x-ox, y-oy
		if x < 0 || y < 0 || x >= w || y >= h {
			return color.NRGBA{}, false
		}
		c := small.NRGBAAt(x, y)
		return c, c.A >= 96
	}
	hex := func(c color.NRGBA) lipgloss.Color {
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B))
	}
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		var sb strings.Builder
		for x := 0; x < cols; x++ {
			top, okTop := at(x, 2*r)
			bot, okBot := at(x, 2*r+1)
			switch {
			case okTop && okBot:
				sb.WriteString(lipgloss.NewStyle().Foreground(hex(top)).Background(hex(bot)).Render("▀"))
			case okTop:
				sb.WriteString(lipgloss.NewStyle().Foreground(hex(top)).Render("▀"))
			case okBot:
				sb.WriteString(lipgloss.NewStyle().Foreground(hex(bot)).Render("▄"))
			default:
				sb.WriteString(" ")
			}
		}
		lines[r] = sb.String()
	}
	return lines
}
