package fortune

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/jstader/partyfax/backend/internal/doc"
)

// A slip is a border of cookies and lucky coins, the fortune, the lucky
// numbers, a Chinese lesson, and the border again. It is mostly outlines and
// text, on purpose: dark pictures are what knock the printer off USB (see
// AGENTS.md), and a slip of thin lines is nowhere near that.

var (
	//go:embed fonts/NotoSansSC-fortunes-500.ttf
	text500TTF []byte
	//go:embed fonts/NotoSansSC-fortunes-700.ttf
	text700TTF []byte
	//go:embed fonts/VT323.ttf
	pixelTTF []byte

	text500 = mustParse(text500TTF)
	text700 = mustParse(text700TTF)
	pixel   = mustParse(pixelTTF)
)

// mustParse may panic: these are files built into the program, and a test
// reads them, so a bad one never gets as far as running.
func mustParse(ttf []byte) *sfnt.Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(fmt.Sprintf("fortune: bad embedded font: %v", err))
	}

	return f
}

// The two symbols of the border. '#' is black; drawn at twice this size.
var (
	// A folded cookie with its slip of paper hanging out.
	cookie = []string{
		"......######...........",
		"....##......##.........",
		"...#..........#........",
		"..#.....##.....#.......",
		".#.....#..#.....#......",
		".#....#....#....#......",
		"#....#......#....#.....",
		"#...#........#...#.....",
		"#..#..........#..#.....",
		".##....########.##.....",
		".......#..............#",
		".......#.###.####.....#",
		".......#..............#",
		".......################",
	}
	// A lucky coin with its square hole.
	coin = []string{
		"...####...",
		".##....##.",
		".#......#.",
		"#..####..#",
		"#..#..#..#",
		"#..#..#..#",
		"#..####..#",
		".#......#.",
		".##....##.",
		"...####...",
	}
	// The pattern along the top and bottom: cookies with coins between.
	pattern = [][]string{cookie, coin, cookie, coin, cookie, coin, cookie}
)

const (
	width      = doc.PhotoWidth
	symbolDots = 2               // dots per '#' of a symbol
	borderH    = 14 * symbolDots // the cookie's height
	pad        = 14              // above the top border, below the site name
	textW      = width - 40      // fortune and lesson lines wrap to this
	siteName   = "fortunes.nilanet.live"
	footerH    = 18 // the site name's line
	footerGap  = 8  // between the bottom border and the site name
)

// inner says which side of a border its dashed rule runs along.
type inner int

const (
	ruleBelow inner = iota
	ruleAbove
)

// fill blackens a rectangle of the slip.
func fill(img *image.Gray, x, y, w, h int) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), image.Black, image.Point{}, draw.Src)
}

// drawSymbol draws a symbol with its top left corner at x, y.
func drawSymbol(img *image.Gray, rows []string, x, y int) {
	for j, row := range rows {
		for i, c := range row {
			if c == '#' {
				fill(img, x+i*symbolDots, y+j*symbolDots, symbolDots, symbolDots)
			}
		}
	}
}

// drawBorder draws one border with its top at y: the symbols spread evenly
// across the paper, centred on a common middle line, and a dashed rule along
// the inner side.
func drawBorder(img *image.Gray, y int, side inner) {
	used := 0
	for _, rows := range pattern {
		used += len(rows[0]) * symbolDots
	}
	gap := (width - 16 - used) / (len(pattern) - 1)
	x := (width - used - gap*(len(pattern)-1) + 1) / 2

	for _, rows := range pattern {
		drawSymbol(img, rows, x, y+(borderH-len(rows)*symbolDots)/2)
		x += len(rows[0])*symbolDots + gap
	}

	ruleY := y - 7
	if side == ruleBelow {
		ruleY = y + borderH + 6
	}
	for dx := 8; dx+6 <= width-8; dx += 10 {
		fill(img, dx, ruleY, 6, 1)
	}
}

func newFace(f *sfnt.Font, size float64) (font.Face, error) {
	// At 72 dpi a point is a dot. No hinting: the letters keep the shapes
	// they were drawn with, and the threshold below settles the edges.
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
}

// wrap breaks words into lines no wider than maxW in the face.
func wrap(face font.Face, s string, maxW int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		next := word
		if line != "" {
			next = line + " " + word
		}
		if line != "" && font.MeasureString(face, next).Ceil() > maxW {
			lines = append(lines, line)
			line = word
		} else {
			line = next
		}
	}
	if line != "" {
		lines = append(lines, line)
	}

	return lines
}

// item is one entry of the slip, top to bottom: a line of text in a band h
// dots tall, a dashed rule, or a gap of h dots.
type item struct {
	text string
	face font.Face
	rule bool
	h    int
}

// zhFace is the Chinese line's face: 64 dots tall unless that's too wide
// for the paper, as a lesson of five characters is.
func zhFace(zh string) (font.Face, int, error) {
	const full = 64
	face, err := newFace(text500, full)
	if err != nil {
		return nil, 0, err
	}

	wide := font.MeasureString(face, zh).Ceil()
	if wide <= textW {
		return face, full, nil
	}
	size := full * textW / wide
	face, err = newFace(text500, float64(size))

	return face, size, err
}

// Compose draws the slip, as tall as its fortune and no taller.
func Compose(s Slip) (*image.Paletted, error) {
	body := func(size float64) (font.Face, error) { return newFace(text500, size) }

	fortuneFace, err := body(25)
	if err != nil {
		return nil, err
	}
	noteFace, err := body(19)
	if err != nil {
		return nil, err
	}
	pinyinFace, err := body(24)
	if err != nil {
		return nil, err
	}
	numbersFace, err := newFace(text700, 26)
	if err != nil {
		return nil, err
	}
	meaningFace, err := newFace(text700, 22)
	if err != nil {
		return nil, err
	}
	labelFace, err := newFace(pixel, 22)
	if err != nil {
		return nil, err
	}
	siteFace, err := newFace(pixel, 18)
	if err != nil {
		return nil, err
	}
	zh, zhSize, err := zhFace(s.Lesson.ZH)
	if err != nil {
		return nil, err
	}

	numbers := make([]string, len(s.Numbers))
	for i, n := range s.Numbers {
		numbers[i] = strconv.Itoa(n)
	}

	items := []item{{h: pad + borderH + 22}}
	for _, l := range wrap(fortuneFace, s.Fortune, textW) {
		items = append(items, item{text: l, face: fortuneFace, h: 34})
	}
	items = append(items,
		item{h: 18}, item{rule: true, h: 1}, item{h: 14},
		item{text: "LUCKY NUMBERS", face: labelFace, h: 22},
		item{h: 4},
		item{text: strings.Join(numbers, "   "), face: numbersFace, h: 34},
		item{h: 12}, item{rule: true, h: 1}, item{h: 14},
		item{text: "LEARN CHINESE", face: labelFace, h: 22},
		item{h: 4},
		item{text: s.Lesson.ZH, face: zh, h: zhSize * 5 / 4},
		item{text: s.Lesson.Pinyin, face: pinyinFace, h: 32},
		item{text: s.Lesson.EN, face: meaningFace, h: 30},
	)
	for _, l := range wrap(noteFace, s.Lesson.Note, textW) {
		items = append(items, item{text: l, face: noteFace, h: 25})
	}
	items = append(items,
		item{h: 22 + borderH + footerGap},
		item{text: siteName, face: siteFace, h: footerH},
		item{h: pad},
	)

	h := 0
	for _, it := range items {
		h += it.h
	}

	img := image.NewGray(image.Rect(0, 0, width, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	y := 0
	for _, it := range items {
		switch {
		case it.rule:
			for dx := 40; dx+6 <= width-40; dx += 12 {
				fill(img, dx, y, 6, 1)
			}
		case it.face != nil:
			drawCentred(img, it.face, it.text, y+it.h/2)
		}
		y += it.h
	}
	drawBorder(img, pad, ruleBelow)
	drawBorder(img, h-pad-footerH-footerGap-borderH, ruleAbove)

	return threshold(img), nil
}

// drawCentred draws a line of text centred on the paper, with the middle of
// its line height on row mid.
func drawCentred(img *image.Gray, face font.Face, s string, mid int) {
	m := face.Metrics()
	d := font.Drawer{Dst: img, Src: image.Black, Face: face}
	d.Dot = fixed.Point26_6{
		X: (fixed.I(width) - d.MeasureString(s)) / 2,
		Y: fixed.I(mid) + (m.Ascent-m.Descent)/2,
	}
	d.DrawString(s)
}

// threshold turns the slip 1-bit: it's black on white, bar the antialiased
// edges of the letters.
func threshold(g *image.Gray) *image.Paletted {
	out := image.NewPaletted(g.Bounds(), color.Palette{color.White, color.Black})
	for y := g.Rect.Min.Y; y < g.Rect.Max.Y; y++ {
		for x := g.Rect.Min.X; x < g.Rect.Max.X; x++ {
			if g.GrayAt(x, y).Y < 128 {
				out.SetColorIndex(x, y, 1)
			}
		}
	}

	return out
}

// PNG is the slip as the PNG the print path takes.
func PNG(s Slip) ([]byte, error) {
	img, err := Compose(s)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
