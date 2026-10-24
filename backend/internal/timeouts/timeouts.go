// Package timeouts is every wait on the print path, in the order they nest,
// so the chain reads and is tested in one place: the firmware's own deadline,
// then the dispatcher waiting for its ack, then the gateway waiting for the
// dispatcher, then the servers' write timeout around both.
//
// They are flat, not sized to the payload, and generous: pictures are sent
// with the printer heating few dots at a time (render.gentleHeat), so a dark
// photobooth strip can take the better part of a minute. The ceiling over
// all of them is Cloudflare's: it ends any request at 100 seconds.
package timeouts

import "time"

// Firmware is how long the printer end may take to ack a job: the Pi agent's
// jobDeadline (pi/main.go), plus two seconds of slack for a transfer caught
// mid-flight. Change them together.
const Firmware = 60*time.Second + 2*time.Second

// Ack is how long the dispatcher waits for the firmware's answer: longer than
// the firmware's own limit, so a printer that gives up gets to say why rather
// than leaving the dispatcher to guess.
const Ack = Firmware + 3*time.Second

// Print is how long the gateway waits for the dispatcher: its Ack plus three
// seconds for the hops in between.
const Print = Ack + 3*time.Second

// Write is for the gateway's and dispatcher's HTTP servers. It must exceed
// Print, or a print legitimately waiting on the printer is cut off before it
// can answer.
const Write = 75 * time.Second
