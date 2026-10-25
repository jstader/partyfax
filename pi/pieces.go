package main

import (
	"bytes"
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// Pictures are sent in pieces, each waited out before the next. A strip is
// one raster command of about 63 KB, and sent whole it is one long USB
// transfer that the printer holds back with flow control for the seconds it
// takes to print. Partway through some of those the Pi's port gave up
// ("disabled by hub (EMI?)") and the strip stopped. In pieces of pieceRows
// rows, each followed by a GS r 1 query that the printer answers only once
// it has printed everything before it, the Pi never sits in a long
// transfer. Measured on paper: no visible joins, about 2.7 s more a strip.
const pieceRows = 128

// How long to wait for a piece's answer. A piece prints in well under a
// second.
const pieceWait = 5 * time.Second

var (
	rasterCmd  = []byte{0x1d, 'v', '0'}
	paperQuery = []byte{0x1d, 'r', 1} // GS r 1: paper sensor status
)

// A segment of a job: bytes to send, and whether to wait for the printer to
// finish them before sending more.
type segment struct {
	data []byte
	sync bool
}

// split cuts a job's raster images (GS v 0 m xL xH yL yH d...) into pieces
// of at most pieceRows rows, each its own raster command, to be synced
// after. Everything else -- reset, text, feeds -- passes through as it is.
// The search for the next raster starts after the previous one's data, so
// image bytes are never read as commands; text and command parameters never
// contain GS (0x1d) followed by "v0". A raster that claims more data than
// the job has, or one in a mode other than normal, is left alone.
func split(job []byte) []segment {
	var out []segment
	rest := job

	for {
		i := bytes.Index(rest, rasterCmd)
		if i < 0 || len(rest) < i+8 {
			break
		}
		h := rest[i : i+8]
		mode, perRow, rows := h[3], int(h[4])|int(h[5])<<8, int(h[6])|int(h[7])<<8
		end := i + 8 + perRow*rows
		if mode != 0 || perRow == 0 || rows == 0 || end > len(rest) {
			break
		}

		if i > 0 {
			out = append(out, segment{data: rest[:i]})
		}
		data := rest[i+8 : end]
		for start := 0; start < rows; start += pieceRows {
			n := min(pieceRows, rows-start)
			piece := append([]byte{0x1d, 'v', '0', 0, byte(perRow), byte(perRow >> 8), byte(n), byte(n >> 8)},
				data[start*perRow:(start+n)*perRow]...)
			out = append(out, segment{data: piece, sync: true})
		}
		rest = rest[end:]
	}

	if len(rest) > 0 {
		out = append(out, segment{data: rest})
	}
	return out
}

// After this many pieces the printer didn't answer, it is taken not to answer
// queries at all, and the rest of the job goes out without waiting.
const maxMisses = 3

// printJob sends a job in segments, waiting after each picture piece until the
// printer has printed it.
func (p *usblp) printJob(job []byte, deadline time.Time) error {
	misses, piece := 0, 0

	for _, s := range split(job) {
		if err := p.write(s.data, deadline); err != nil {
			return err
		}
		if !s.sync || misses >= maxMisses {
			continue
		}
		piece++

		// A late answer to the last query must not be taken for this one's.
		p.discardInput()
		if err := p.write(paperQuery, deadline); err != nil {
			return err
		}
		if p.answered(min(pieceWait, time.Until(deadline))) {
			continue
		}
		if time.Now().After(deadline) {
			return errTimeout
		}
		misses++
		logger.Warn("printer didn't answer after a piece", "piece", piece, "misses", misses)
		if misses == maxMisses {
			logger.Warn("sending the rest of the job without waiting between pieces")
		}
	}

	return nil
}

// answered waits up to wait for the printer to send a byte. usblp can report
// data ready and then have none to read (EAGAIN, or zero bytes); that is not
// an answer, so it waits on rather than giving up.
func (p *usblp) answered(wait time.Duration) bool {
	until := time.Now().Add(wait)
	buf := make([]byte, 1)

	for {
		if p.wait(unix.POLLIN, until) != nil {
			return false
		}
		n, err := unix.Read(p.fd, buf)
		switch {
		case n == 1:
			return true
		case err == nil, errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
			time.Sleep(5 * time.Millisecond)
		default:
			return false
		}
	}
}
