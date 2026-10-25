package main

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// Pictures are sent in pieces, each waited out before the next. A strip is
// one raster command of about 63 KB, and partway through some dark ones the
// printer stops for a while (an overheated head cooling, it seems). Sent
// whole, the USB link dropped during that stop ("disabled by hub (EMI?)")
// and the strip ended there. Sent in pieces without waiting, the piece that
// arrived during the stop came out garbled, rows shifted sideways. So after
// each piece of pieceRows rows comes a GS r 1 query, which the printer
// answers only once it has printed everything before it, and nothing more
// is sent until it has: however long it stops, up to the job's deadline.
// Measured on paper: no visible joins, about 2.7 s more a strip.
const pieceRows = 128

// A piece normally prints in about three quarters of a second. Waits longer
// than this are the printer stopping, and are logged, to see how often and
// for how long.
const pauseWorthLogging = 2 * time.Second

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

// printJob sends a job in segments, waiting after each picture piece until the
// printer has printed it. A printer that never answers the query can't print
// pictures this way; this one does.
func (p *usblp) printJob(job []byte, deadline time.Time) error {
	piece := 0

	for _, s := range split(job) {
		if err := p.write(s.data, deadline); err != nil {
			return err
		}
		if !s.sync {
			continue
		}
		piece++

		// A late answer to an earlier query must not be taken for this one's.
		p.discardInput()
		sent := time.Now()
		if err := p.write(paperQuery, deadline); err != nil {
			return err
		}
		if err := p.awaitAnswer(deadline); err != nil {
			logger.Warn("printer stopped answering", "piece", piece, "waited", time.Since(sent).Round(100*time.Millisecond))
			return err
		}
		if waited := time.Since(sent); waited > pauseWorthLogging {
			logger.Info("printer paused", "piece", piece, "for", waited.Round(100*time.Millisecond))
		}
	}

	return nil
}

// awaitAnswer waits until deadline for the printer to send a byte. usblp can
// report data ready and then have none to read (EAGAIN, or zero bytes); that
// is not an answer, so it waits on.
func (p *usblp) awaitAnswer(deadline time.Time) error {
	buf := make([]byte, 1)

	for {
		if err := p.wait(unix.POLLIN, deadline); err != nil {
			return err
		}
		n, err := unix.Read(p.fd, buf)
		switch {
		case n == 1:
			return nil
		case err == nil, errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
			time.Sleep(5 * time.Millisecond)
		case errors.Is(err, unix.ENODEV):
			return errNoPrinter
		default:
			return fmt.Errorf("read: %w", err)
		}
	}
}
