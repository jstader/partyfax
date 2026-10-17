# The printer end, on a Raspberry Pi Zero W

`partyfax-agent` does what the ESP32-S3 firmware in `firmware/` does, as a
Linux service: it dials out to `wss://partyfax.nilanet.live/mqtt`, waits for
jobs, checks for paper, writes the server's ESC/POS to the printer, and acks.
The server can't tell it from the firmware.

The printer goes on the Pi's **USB** port (the inner micro-USB, not `PWR`),
through an OTG adapter or cable. Linux's `usblp` driver shows it as
`/dev/usb/lp0`. That keeps the fast USB path the S3 firmware uses, with no
9600 baud serial.

## 1. Get the binary

Every push to `main` builds it. Open the latest
[ci run](https://github.com/jstader/partyfax/actions/workflows/ci.yml) and
download the `partyfax-agent` artifact. It arrives as a zip, so unzip it and
`chmod +x` the binary.

To build it yourself instead, on any machine with Go or Docker:

```bash
./pi/build.sh
```

Either way you get `partyfax-agent`, a static ARMv6 binary.

## 2. Install on the Pi

Raspberry Pi OS Lite (32-bit). The Zero W v1.1 is ARMv6, so use the
32-bit image, not 64-bit.

```bash
scp pi/partyfax-agent pi/partyfax-agent.service pi/agent.env.example <pi>:
```

Then on the Pi:

```bash
sudo install -m 0755 partyfax-agent /usr/local/bin/
sudo useradd --system --no-create-home --groups lp partyfax
sudo install -d -m 0750 -g partyfax /etc/partyfax
sudo install -m 0640 -g partyfax agent.env.example /etc/partyfax/agent.env
sudo nano /etc/partyfax/agent.env   # PARTYFAX_MQTT_PASS = PRINTER_MQTT_PASSWORD from the server's .env
sudo install -m 0644 partyfax-agent.service /etc/systemd/system/
```

## 3. Check the printer alone

Plug the printer in, switch it on, and check that `ls -l /dev/usb/lp0` shows
group `lp`. Then:

```bash
sudo -u partyfax /usr/local/bin/partyfax-agent -selftest
```

It should print a short receipt and log either `paper present` or
`paper sensor did not answer`. The second means the printer can't report
status over USB, and every job prints without a paper check.

## 4. Run it

```bash
sudo systemctl enable --now partyfax-agent
journalctl -u partyfax-agent -f
```

Look for `connected, subscribed, announced online`. The site's printer
indicator then goes online.

## Notes

- **Wi-Fi power saving** on the Zero W drops idle connections. If the agent
  keeps reconnecting, turn it off: `sudo iw wlan0 set power_save off`, made
  permanent through NetworkManager (`wifi.powersave = 2`).
- **Timeouts** are the firmware's: 10 s for the printer to appear, 20 s per
  job. `backend/internal/timeouts` depends on them, so change both together.
- **The camera** is free for other uses. The agent only touches the printer
  and the network.
