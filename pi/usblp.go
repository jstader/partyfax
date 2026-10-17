package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// The printer as Linux's usblp driver presents it: /dev/usb/lp0, a character
// device that moves bytes over the printer's bulk endpoints. The Pi Zero's
// OTG port is the USB host, the way the ESP32-S3's was for the firmware.
//
// Everything is non-blocking with poll(2), because usblp has no timeouts of
// its own: a blocking write to a stalled printer never returns. usblp reports
// POLLOUT once the last write's transfer has completed -- the printer has the
// bytes -- and POLLIN once a read has data.

var (
	// errNoPrinter is the device node missing: unplugged, or switched off.
	errNoPrinter = errors.New("no printer attached")
	// errNoPaper is usblp refusing a write because the printer said so.
	errNoPaper = errors.New("printer is out of paper")
	errTimeout = errors.New("printer did not respond in time")
)

type usblp struct {
	fd int
}

// waitAttached polls for the device node, for at most wait. usblp creates it
// when the printer enumerates, so a printer switched on as the job arrives
// shows up here a second or two later.
func waitAttached(path string, wait time.Duration) error {
	deadline := time.Now().Add(wait)

	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}

		if time.Now().After(deadline) {
			return errNoPrinter
		}

		time.Sleep(200 * time.Millisecond)
	}
}

// openPrinter opens the device for one job. Not kept open between jobs: the
// printer may be unplugged in between, and a stale descriptor fails forever.
func openPrinter(path string) (*usblp, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENODEV) {
			return nil, errNoPrinter
		}

		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return &usblp{fd: fd}, nil
}

func (p *usblp) Close() error {
	return unix.Close(p.fd)
}

// wait blocks until the device reports events, or deadline passes.
func (p *usblp) wait(events int16, deadline time.Time) error {
	for {
		ms := time.Until(deadline).Milliseconds()
		if ms <= 0 {
			return errTimeout
		}

		fds := []unix.PollFd{{Fd: int32(p.fd), Events: events}}

		n, err := unix.Poll(fds, int(ms))
		if errors.Is(err, unix.EINTR) {
			continue
		}

		if err != nil {
			return fmt.Errorf("poll: %w", err)
		}

		if n == 0 {
			return errTimeout
		}

		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return errNoPrinter
		}

		return nil
	}
}

// write sends a whole job and returns once the printer has taken every byte,
// or deadline passes. The printer holds bytes back with USB flow control
// while it prints, so taking the last of them means the job is on paper: the
// return is the confirmation, as it was over the ESP32-S3's USB.
func (p *usblp) write(data []byte, deadline time.Time) error {
	for len(data) > 0 {
		if err := p.wait(unix.POLLOUT, deadline); err != nil {
			return err
		}

		n, err := unix.Write(p.fd, data)

		switch {
		case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
			// Poll said writable and the driver disagreed; give it a moment
			// rather than spinning.
			time.Sleep(5 * time.Millisecond)
			continue
		case errors.Is(err, unix.ENOSPC):
			return errNoPaper
		case errors.Is(err, unix.ENODEV):
			return errNoPrinter
		case err != nil:
			return fmt.Errorf("write: %w", err)
		}

		data = data[n:]
	}

	// usblp returns from write once the transfer is submitted, not done.
	// POLLOUT again means the last one has completed.
	return p.wait(unix.POLLOUT, deadline)
}

// readByte returns one byte from the printer, or ok=false if none arrived
// within timeout. A printer that cannot talk back (usblp says so with an
// error) reads as silence.
func (p *usblp) readByte(timeout time.Duration) (b byte, ok bool) {
	if p.wait(unix.POLLIN, time.Now().Add(timeout)) != nil {
		return 0, false
	}

	buf := make([]byte, 1)

	n, err := unix.Read(p.fd, buf)
	if err != nil || n != 1 {
		return 0, false
	}

	return buf[0], true
}

// discardInput drops whatever the printer sent that nobody read, so it is
// not taken for the answer to the next query. Bounded, in case a printer
// talks faster than it can be drained.
func (p *usblp) discardInput() {
	buf := make([]byte, 256)

	for range 64 {
		n, err := unix.Read(p.fd, buf)
		if err != nil || n == 0 {
			return
		}
	}
}

// hasPaper asks with DLE EOT 4, the real-time paper sensor query. ok=false
// is "did not answer", not "no paper": a printer that cannot report status
// should still print.
func (p *usblp) hasPaper() (paper, ok bool) {
	p.discardInput()

	if err := p.write([]byte{0x10, 0x04, 0x04}, time.Now().Add(300*time.Millisecond)); err != nil {
		return false, false
	}

	status, ok := p.readByte(300 * time.Millisecond)
	if !ok {
		return false, false
	}

	// Bits 1 and 4 are set in every status byte. If they are not, this is a
	// stray byte, and guessing from it is worse than admitting we do not know.
	if status&0b0001_0010 != 0b0001_0010 {
		logger.Warn("printer answered with something that is not a status byte", "byte", fmt.Sprintf("%#02x", status))
		return false, false
	}

	// Bits 5 and 6 are the paper-end sensor: 00 present, 11 gone. On the
	// original printer, 0x12 with the roll in, 0x72 with it out.
	return status&0b0110_0000 == 0, true
}
