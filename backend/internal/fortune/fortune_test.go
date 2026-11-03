package fortune

import (
	"bytes"
	"image/png"
	"slices"
	"testing"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"

	"github.com/jstader/partyfax/backend/internal/doc"
)

func TestFortunesFitAndAreDifferent(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range fortunes {
		if n := len([]rune(f)); n > maxFortune {
			t.Errorf("%d characters: %s", n, f)
		}
		if seen[f] {
			t.Errorf("in twice: %s", f)
		}
		seen[f] = true
	}
}

func TestLessonsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range lessons {
		if l.ZH == "" || l.Pinyin == "" || l.EN == "" {
			t.Errorf("incomplete: %+v", l)
		}
		for _, c := range l.ZH {
			if !unicode.Is(unicode.Han, c) {
				t.Errorf("%s is not all Chinese characters", l.ZH)
			}
		}
		if n := len([]rune(l.ZH)); n > 6 {
			t.Errorf("%s is too long for one line", l.ZH)
		}
		if seen[l.ZH] {
			t.Errorf("in twice: %s", l.ZH)
		}
		seen[l.ZH] = true
	}
}

// A character missing from the trimmed font would print as an empty box.
func TestFontHasEveryCharacter(t *testing.T) {
	var buf sfnt.Buffer
	check := func(f *sfnt.Font, name, s string) {
		for _, c := range s {
			if c == ' ' {
				continue
			}
			if g, err := f.GlyphIndex(&buf, c); err != nil || g == 0 {
				t.Errorf("%q in %q is not in %s (fonts/README.md)", c, s, name)
			}
		}
	}

	for _, s := range fortunes {
		check(text500, "the text font", s)
	}
	for _, l := range lessons {
		for _, s := range []string{l.ZH, l.Pinyin, l.Note} {
			check(text500, "the text font", s)
		}
		check(text700, "the bold font", l.EN)
	}
	check(text700, "the bold font", "0123456789")
	check(pixel, "the pixel font", "LUCKY NUMBERS LEARN CHINESE "+siteName)
}

func TestLuckyNumbers(t *testing.T) {
	for range 500 {
		n := Pick().Numbers
		if len(n) != 6 || !slices.IsSorted(n) || n[0] < 1 || n[5] > 49 {
			t.Fatalf("bad numbers: %v", n)
		}
		if len(slices.Compact(slices.Clone(n))) != 6 {
			t.Fatalf("a number twice: %v", n)
		}
	}

	// The extremes are reachable, and a repeat is drawn again.
	draws := []int{0, 48, 48, 9, 19, 29, 39}
	i := 0
	got := luckyNumbers(func(int) int { i++; return draws[i-1] })
	if want := []int{1, 10, 20, 30, 40, 49}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func inkShare(t *testing.T, s Slip) float64 {
	t.Helper()

	img, err := Compose(s)
	if err != nil {
		t.Fatal(err)
	}
	ink := 0
	for _, p := range img.Pix {
		ink += int(p)
	}

	return float64(ink) / float64(len(img.Pix))
}

// Every fortune and every lesson on one slip: each with the longest of the
// other, since a slip's height is the sum of the two.
func TestEverySlipFitsThePaper(t *testing.T) {
	longest := lessons[0]
	for _, l := range lessons {
		if len(l.Note) > len(longest.Note) {
			longest = l
		}
	}
	long := fortunes[0]
	for _, f := range fortunes {
		if len(f) > len(long) {
			long = f
		}
	}

	numbers := []int{44, 45, 46, 47, 48, 49}
	check := func(s Slip) {
		t.Helper()
		img, err := Compose(s)
		if err != nil {
			t.Fatal(err)
		}
		if w, h := img.Rect.Dx(), img.Rect.Dy(); w != doc.PhotoWidth || h > doc.FortuneMaxRows {
			t.Errorf("%d x %d for %q", w, h, s.Fortune)
		}
		// Long stretches of dark rows knock the printer off USB (AGENTS.md).
		if share := inkShare(t, s); share > 0.2 {
			t.Errorf("%.0f%% ink for %q", 100*share, s.Fortune)
		}
	}
	for _, f := range fortunes {
		check(Slip{Fortune: f, Numbers: numbers, Lesson: longest})
	}
	for _, l := range lessons {
		check(Slip{Fortune: long, Numbers: numbers, Lesson: l})
	}
}

// What the gateway hands on has to pass the print path's own check.
func TestPNGPassesValidation(t *testing.T) {
	b, err := PNG(Pick())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := doc.ValidateFortune(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dy() != cfg.Height {
		t.Errorf("rows %d, header says %d", img.Bounds().Dy(), cfg.Height)
	}
}

func TestLongChineseStaysOnThePaper(t *testing.T) {
	for _, l := range lessons {
		face, size, err := zhFace(l.ZH)
		if err != nil {
			t.Fatal(err)
		}
		if w := faceWidth(face, l.ZH); w > textW {
			t.Errorf("%s is %d dots wide at size %d", l.ZH, w, size)
		}
	}
}

func faceWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Ceil()
}
