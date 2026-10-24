package main

import (
	"bytes"
	"testing"
)

func raster(rows int, fill func(row int) byte) []byte {
	b := []byte{0x1d, 'v', '0', 0, 48, 0, byte(rows), byte(rows >> 8)}
	for r := range rows {
		b = append(b, bytes.Repeat([]byte{fill(r)}, 48)...)
	}
	return b
}

// rejoin puts split pieces back into one raster's rows, checking each piece's
// header on the way.
func rejoin(t *testing.T, segs []segment) []byte {
	t.Helper()
	var rows []byte
	for _, s := range segs {
		if !s.sync {
			continue
		}
		h := s.data[:8]
		n := int(h[6]) | int(h[7])<<8
		if !bytes.Equal(h[:6], []byte{0x1d, 'v', '0', 0, 48, 0}) || n < 1 || n > pieceRows || len(s.data) != 8+48*n {
			t.Fatalf("bad piece header %x for %d bytes", h, len(s.data))
		}
		rows = append(rows, s.data[8:]...)
	}
	return rows
}

func TestSplitLeavesTextAlone(t *testing.T) {
	job := []byte("\x1b@\x1bE\x01hello\x1bE\x00\n\x1bd\x03")
	segs := split(job)
	if len(segs) != 1 || segs[0].sync || !bytes.Equal(segs[0].data, job) {
		t.Errorf("text job split into %+v", segs)
	}
}

// A strip: reset, one 1,322-row raster (the picture and its blank margin),
// feed. Pieces of 128 rows, the last of 42, all synced; the rows come back
// in order, unchanged.
func TestSplitStrip(t *testing.T) {
	const rows = 1322
	img := raster(rows, func(r int) byte { return byte(r) })
	job := append(append([]byte("\x1b@"), img...), "\x1bd\x03"...)

	segs := split(job)

	want := 1 + (rows+pieceRows-1)/pieceRows + 1
	if len(segs) != want {
		t.Fatalf("%d segments, want %d", len(segs), want)
	}
	if !bytes.Equal(segs[0].data, []byte("\x1b@")) || segs[0].sync {
		t.Errorf("first segment %q, want the reset, unsynced", segs[0].data)
	}
	if last := segs[len(segs)-1]; !bytes.Equal(last.data, []byte("\x1bd\x03")) || last.sync {
		t.Errorf("last segment %q, want the feed, unsynced", last.data)
	}
	if got := rejoin(t, segs); !bytes.Equal(got, img[8:]) {
		t.Error("the pieces don't add up to the picture")
	}
	if n := segs[len(segs)-2].data[6]; n != rows%pieceRows {
		t.Errorf("last piece has %d rows, want %d", n, rows%pieceRows)
	}
}

// A photo receipt: header text, a raster whose bytes include 0x1d 'v' '0'
// (which must not be taken for a command), footer text.
func TestSplitPhotoReceipt(t *testing.T) {
	img := raster(300, func(r int) byte { return []byte{0x1d, 'v', '0'}[r%3] })
	head, foot := []byte("\x1b@\x1ba\x01FAX\n"), []byte("bye\n\x1bd\x03")
	job := append(append(append([]byte{}, head...), img...), foot...)

	segs := split(job)

	if !bytes.Equal(segs[0].data, head) || !bytes.Equal(segs[len(segs)-1].data, foot) {
		t.Error("the text around the picture changed")
	}
	if got := rejoin(t, segs); !bytes.Equal(got, img[8:]) {
		t.Error("the pieces don't add up to the picture")
	}
	if len(segs) != 2+3 {
		t.Errorf("%d segments, want text, 3 pieces, text", len(segs))
	}
}

// A raster that claims more rows than the job holds is passed through whole,
// not cut where it might misread the bytes.
func TestSplitLeavesShortRasterAlone(t *testing.T) {
	img := raster(10, func(int) byte { return 0xff })
	img[6] = 200 // claims 200 rows, has 10
	job := append([]byte("\x1b@"), img...)

	segs := split(job)
	if len(segs) != 1 || !bytes.Equal(segs[0].data, job) {
		t.Errorf("short raster split into %d segments", len(segs))
	}
}
