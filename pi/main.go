// Command partyfax-agent is the printer end of fax, for a Raspberry Pi Zero W
// with the thermal printer on its USB OTG port. It replaces upstream's ESP32-S3
// firmware, as a Linux process: subscribe to the device's
// job topic over MQTT-over-websockets, check for paper, write the ESC/POS the
// server rendered to the printer, and ack. The server needs no change to talk
// to it; the topics, payloads and acks are the firmware's.
//
// Configuration is the environment (see agent.env.example), so the password
// stays out of ps and shell history.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Kept from the original firmware. backend/internal/timeouts waits for
// jobDeadline plus a margin, so a printer that gives up says why before the
// dispatcher stops listening. Change them together.
const (
	// How long a job waits for the printer to appear: long enough for one
	// switched on as the job arrives to enumerate.
	attachWait = 10 * time.Second
	// The most one job may take, arrival to ack, the attach wait included.
	jobDeadline = 20 * time.Second
	// Matches the broker's max_packet_size. A square photo is about 19 KB.
	maxJob = 32 * 1024
)

// What an ack says. Codes rather than prose: the dispatcher switches on them.
var (
	online       = []byte(`{"online":true}`)
	offline      = []byte(`{"online":false}`)
	ackOK        = []byte(`{"ok":true}`)
	ackNoPaper   = []byte(`{"ok":false,"error":"no_paper"}`)
	ackNoConfirm = []byte(`{"ok":false,"error":"no_confirmation"}`)
)

var logger = slog.New(slog.NewTextHandler(os.Stderr, nil))

// version is the commit the binary was built from, set by ci.yml with -X.
var version = "dev"

type config struct {
	url, user, pass, device, printer string
	// A Cloudflare Access service token. Access sits in front of /mqtt so
	// that only this agent reaches the broker; both empty means no Access.
	accessID, accessSecret string
}

type job struct {
	id      string
	payload []byte
}

func main() {
	selftest := flag.Bool("selftest", false, "print a test receipt on the local printer and exit; no broker needed")
	flag.Parse()

	cfg := config{
		url:     env("PARTYFAX_MQTT_URL", "wss://partyfax.nilanet.live/mqtt"),
		user:    env("PARTYFAX_MQTT_USER", "printer"),
		pass:    os.Getenv("PARTYFAX_MQTT_PASS"),
		device:  env("PARTYFAX_DEVICE_ID", "printer-1"),
		printer: env("PARTYFAX_PRINTER", "/dev/usb/lp0"),

		accessID:     os.Getenv("PARTYFAX_CF_ACCESS_CLIENT_ID"),
		accessSecret: os.Getenv("PARTYFAX_CF_ACCESS_CLIENT_SECRET"),
	}

	if *selftest {
		if err := selfTest(cfg.printer); err != nil {
			logger.Error("self test failed", "err", err)
			os.Exit(1)
		}

		return
	}

	if cfg.pass == "" {
		logger.Error("PARTYFAX_MQTT_PASS is not set")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	run(ctx, cfg)
}

func run(ctx context.Context, cfg config) {
	jobTopic := fmt.Sprintf("fax/%s/job/+", cfg.device)
	stateTopic := fmt.Sprintf("fax/%s/state", cfg.device)
	ackTopic := fmt.Sprintf("fax/%s/ack", cfg.device)

	// Logged first, so a device id that disagrees with the server's is
	// visible even when the connection never comes up. Drift and jobs vanish
	// with no error anywhere.
	logger.Info("starting", "version", version, "broker", cfg.url, "subscribe", jobTopic, "printer", cfg.printer)

	// Small: the dispatcher sends one job and waits for its ack, so more
	// than one waiting means a redelivery, not a queue.
	jobs := make(chan job, 2)

	// paho runs handlers on one goroutine and holds the PUBACK until they
	// return, so this one only hands the job over and never blocks.
	onJob := func(_ mqtt.Client, m mqtt.Message) {
		id := m.Topic()[strings.LastIndex(m.Topic(), "/")+1:]

		if len(m.Payload()) > maxJob {
			logger.Warn("job over the size limit, dropped", "job", id, "bytes", len(m.Payload()))
			return
		}

		select {
		case jobs <- job{id: id, payload: m.Payload()}:
		default:
			logger.Warn("busy, dropped job", "job", id)
		}
	}

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.url).
		SetClientID("fax-"+cfg.device).
		SetUsername(cfg.user).
		SetPassword(cfg.pass).
		// The broker must NOT hold jobs while we are away, or reconnecting
		// would print a backlog nobody is waiting for any more.
		SetCleanSession(true).
		SetKeepAlive(30*time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetMaxReconnectInterval(30*time.Second).
		// Published by the broker if we vanish without saying goodbye.
		SetBinaryWill(stateTopic, offline, 1, true).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			logger.Warn("broker connection lost", "err", err)
		}).
		// Subscriptions do not survive a reconnect, so every connect
		// subscribes again. paho runs this on its own goroutine, so waiting
		// on tokens here is safe.
		SetOnConnectHandler(func(c mqtt.Client) {
			if t := c.Subscribe(jobTopic, 1, onJob); t.Wait() && t.Error() != nil {
				// Online but deaf is the worst state to be in. Dropping
				// the connection makes paho try the whole thing again.
				logger.Error("subscribe failed; reconnecting", "err", t.Error())
				c.Disconnect(0)
				return
			}

			c.Publish(stateTopic, 1, true, online).Wait()
			logger.Info("connected, subscribed, announced online")
		})

	// Sent on the websocket upgrade, which is the request Access checks.
	if cfg.accessID != "" && cfg.accessSecret != "" {
		opts.SetHTTPHeaders(http.Header{
			"CF-Access-Client-Id":     {cfg.accessID},
			"CF-Access-Client-Secret": {cfg.accessSecret},
		})
		logger.Info("using a Cloudflare Access service token")
	}

	// paho retries forever and says nothing about why an attempt failed:
	// a refused Access token or a wrong password looked like silence.
	mqtt.ERROR = slog.NewLogLogger(logger.Handler(), slog.LevelError)

	client := mqtt.NewClient(opts)
	client.Connect()

	for {
		select {
		case <-ctx.Done():
			// Say so ourselves rather than leave it to the will, which the
			// broker only sends after the keepalive runs out.
			client.Publish(stateTopic, 1, true, offline).WaitTimeout(2 * time.Second)
			client.Disconnect(500)
			logger.Info("stopped")

			return

		case j := <-jobs:
			reply := handle(cfg.printer, j)

			// Not retained: a retained ack would replay on every reconnect.
			t := client.Publish(ackTopic+"/"+j.id, 1, false, reply)
			if !t.WaitTimeout(5*time.Second) || t.Error() != nil {
				logger.Error("could not ack", "job", j.id, "err", t.Error())
			}
		}
	}
}

// handle prints one job and says how it went, in the firmware's words.
func handle(path string, j job) []byte {
	// One clock for the whole job, so a missing or stalled printer costs at
	// most jobDeadline however many steps it would otherwise have stalled.
	deadline := time.Now().Add(jobDeadline)

	if err := waitAttached(path, attachWait); err != nil {
		logger.Warn("job not printed, no printer", "job", j.id)
		return ackNoConfirm
	}

	p, err := openPrinter(path)
	if err != nil {
		logger.Warn("job not printed", "job", j.id, "err", err)
		return ackNoConfirm
	}
	defer p.Close()

	// Measured now, not cached: a browser is waiting on this answer. Silence
	// is not "no paper", so an unanswered query prints.
	if paper, ok := p.hasPaper(); ok && !paper {
		logger.Warn("job refused, no paper", "job", j.id)
		return ackNoPaper
	}

	logger.Info("printing", "job", j.id, "bytes", len(j.payload))

	switch err := p.write(j.payload, deadline); err {
	case nil:
		logger.Info("printed", "job", j.id)
		return ackOK
	case errNoPaper:
		logger.Warn("ran out of paper mid-job", "job", j.id)
		return ackNoPaper
	default:
		logger.Warn("could not print", "job", j.id, "err", err)
		return ackNoConfirm
	}
}

// selfTest prints a short receipt straight to the printer, to check the USB
// side before the broker is involved.
func selfTest(path string) error {
	if err := waitAttached(path, attachWait); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	p, err := openPrinter(path)
	if err != nil {
		return err
	}
	defer p.Close()

	switch paper, ok := p.hasPaper(); {
	case !ok:
		logger.Info("paper sensor did not answer; printing anyway")
	case !paper:
		return errNoPaper
	default:
		logger.Info("paper present")
	}

	host, _ := os.Hostname()
	receipt := []byte("\x1b@" + // initialise
		"\x1ba\x01" + // centre
		"partyfax self test\n" +
		host + "\n" +
		time.Now().Format(time.DateTime) + "\n\n\n\n")

	if err := p.write(receipt, time.Now().Add(jobDeadline)); err != nil {
		return err
	}

	logger.Info("printed a test receipt")

	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}
