package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // logos may be JPEG
	"image/png"
	"math"
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

// logoStoreSize keeps enough detail for the large preview under the table to be drawn from more
// pixels than it shows, so every drawn pixel is a clean average.
const logoStoreSize = 128

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

// resample scales img to w×h. Each target pixel averages exactly the source area under it (partial
// source pixels count by how much they overlap), weighted by alpha and in linear light, so edges
// and thin lines blend smoothly instead of being picked or dropped. When a pixel stands for many
// source pixels (heavy shrinking, e.g. a logo drawn one line tall) a plain average turns stripes
// of colour into mud, so it takes the average of the colour that covers most of the area instead.
func resample(img image.Image, w, h int) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	sx, sy := float64(b.Dx())/float64(w), float64(b.Dy())/float64(h)
	dominant := sx*sy >= 9
	type acc struct{ r, g, bl, a float64 }
	for y := 0; y < h; y++ {
		fy0, fy1 := float64(y)*sy, float64(y+1)*sy
		for x := 0; x < w; x++ {
			fx0, fx1 := float64(x)*sx, float64(x+1)*sx
			var all acc
			var area float64
			groups := map[int]*acc{}
			for py := int(fy0); py < int(math.Ceil(fy1)) && py < b.Dy(); py++ {
				oy := math.Min(fy1, float64(py+1)) - math.Max(fy0, float64(py))
				for px := int(fx0); px < int(math.Ceil(fx1)) && px < b.Dx(); px++ {
					ov := oy * (math.Min(fx1, float64(px+1)) - math.Max(fx0, float64(px)))
					if ov <= 0 {
						continue
					}
					area += ov
					c := color.NRGBAModel.Convert(img.At(b.Min.X+px, b.Min.Y+py)).(color.NRGBA)
					wa := ov * float64(c.A) / 255
					if wa == 0 {
						continue
					}
					r, g, bl := srgbToLinear[c.R]*wa, srgbToLinear[c.G]*wa, srgbToLinear[c.B]*wa
					all.r, all.g, all.bl, all.a = all.r+r, all.g+g, all.bl+bl, all.a+wa
					if dominant && c.A >= 128 {
						k := int(c.R>>5)<<6 | int(c.G>>5)<<3 | int(c.B>>5) // 8 levels a channel
						if groups[k] == nil {
							groups[k] = &acc{}
						}
						gr := groups[k]
						gr.r, gr.g, gr.bl, gr.a = gr.r+r, gr.g+g, gr.bl+bl, gr.a+wa
					}
				}
			}
			if all.a == 0 || area == 0 {
				continue
			}
			pick := all
			for _, gr := range groups {
				if gr.a > pick.a || pick == all {
					pick = *gr
				}
			}
			out.SetNRGBA(x, y, color.NRGBA{linearToSRGB(pick.r / pick.a), linearToSRGB(pick.g / pick.a),
				linearToSRGB(pick.bl / pick.a), uint8(math.Round(math.Min(1, all.a/area) * 255))})
		}
	}
	return out
}

var srgbToLinear = func() (t [256]float64) {
	for i := range t {
		c := float64(i) / 255
		if c <= 0.04045 {
			t[i] = c / 12.92
		} else {
			t[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return
}()

func linearToSRGB(v float64) uint8 {
	v = math.Max(0, math.Min(1, v))
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return uint8(math.Round(v * 255))
}

// trimLogo crops a logo to what's drawn: transparent edges, and a plain background border (all four
// corners the same colour) such as the white square around a round logo. Small logos then use every
// pixel they get for the logo itself.
func trimLogo(img image.Image) image.Image {
	b := img.Bounds()
	at := func(x, y int) color.NRGBA { return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA) }
	corner := at(b.Min.X, b.Min.Y)
	plainBG := corner.A >= 250
	for _, c := range []color.NRGBA{at(b.Max.X-1, b.Min.Y), at(b.Min.X, b.Max.Y-1), at(b.Max.X-1, b.Max.Y-1)} {
		if !near(c, corner) {
			plainBG = false
		}
	}
	empty := func(c color.NRGBA) bool { return c.A < 32 || (plainBG && near(c, corner)) }
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if !empty(at(x, y)) {
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
		}
	}
	if x1 < x0 || (x1-x0+1)*(y1-y0+1) == b.Dx()*b.Dy() {
		return img // nothing drawn, or nothing to crop
	}
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return img
	}
	return sub.SubImage(image.Rect(x0, y0, x1+1, y1+1))
}

// near reports whether two colours look the same (each channel within 24).
func near(a, b color.NRGBA) bool {
	d := func(p, q uint8) bool { return max(p, q)-min(p, q) <= 24 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A, b.A)
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
	img = trimLogo(img)
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
