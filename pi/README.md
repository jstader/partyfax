# Setting up the Pi

The Raspberry Pi Zero W sits next to the printer. It connects out to
`partyfax.nilanet.live` over Wi-Fi, waits for prints, and sends them to the
printer over USB. This guide goes from a blank SD card to a working printer
in about half an hour, most of it waiting.

## What you need

- **Raspberry Pi Zero W** (v1.1)
- **A microSD card**, 8 GB or bigger
- **A computer with an SD card slot** (or a cheap USB card reader)
- **A micro-USB power supply** for the Pi, 5 V and at least 1.2 A. A phone
  charger is fine.
- **A USB OTG adapter**, micro-USB (male) to USB-A (female), to plug the
  printer into the Pi
- **The thermal printer**, with its own power supply and USB cable
- **Your 2.4 GHz Wi-Fi name and password.** The Zero W can't see 5 GHz
  networks.
- **The printer's broker password:** `PRINTER_MQTT_PASSWORD` in the server's
  `.env`. Get it now with:

  ```bash
  ssh root@<container> grep PRINTER_MQTT_PASSWORD /root/partyfax/.env
  ```

## 1. Put Raspberry Pi OS on the SD card

1. Install **Raspberry Pi Imager** on your computer from
   [raspberrypi.com/software](https://www.raspberrypi.com/software/) and open
   it. On a Mac with Homebrew you can run `brew install --cask raspberry-pi-imager`.
2. Put the SD card in your computer.
3. **Choose Device:** *Raspberry Pi Zero*. The Zero W is listed under that
   name.
4. **Choose OS:** *Raspberry Pi OS (other)*, then **Raspberry Pi OS Lite
   (32-bit)**. Lite has no desktop, which the Pi doesn't need, and the Zero W
   can only run the 32-bit version.
5. **Choose Storage:** your SD card. Check that it's the card and not some
   other drive: Imager erases it.
6. Imager now asks whether to customise the OS. **Say yes** (on older
   versions, click *Edit Settings*). This is what lets the Pi join Wi-Fi and
   accept SSH with no screen or keyboard:
   - **Hostname:** `partyfax-pi`
   - **Username and password:** a username such as `pi` and a strong
     password. You won't log in with it, but `sudo` asks for it.
   - **Wi-Fi:** your 2.4 GHz network name and password, and your country. The
     country matters: Wi-Fi stays off without it.
   - **Time zone:** yours.
   - **SSH:** turn it on and choose **public-key authentication only**.
     Imager can fill in the key from your computer. If you don't have one
     yet, make one first with `ssh-keygen -t ed25519` in a terminal, then
     click *Add SSH key*. Keys can't be guessed the way passwords can.
   - Leave Raspberry Pi Connect off.
7. Write the card and wait for it to finish and verify. Imager says when it's
   safe to remove the card.

## 2. First boot

1. Put the SD card in the Pi.
2. Plug the power supply into the port labelled **PWR**, the one at the end
   of the board.
3. Wait about **five minutes**. The first boot resizes the card and joins
   Wi-Fi, and the Zero W is slow. The green light flickers while it works.
4. From your computer, log in:

   ```bash
   ssh pi@partyfax-pi.local
   ```

   Use the username you chose if it isn't `pi`. Answer `yes` to the
   fingerprint question. There's no password prompt: your key logs you in.

**If `partyfax-pi.local` isn't found:** give the Pi a few more minutes, then
check that it shows up in your router's list of connected devices. If it
does, `ssh` to the IP address listed there instead. If it never appears, the
Wi-Fi details were probably wrong (5 GHz network, typo, or missing country).
Re-flash the card and try again.

## 3. Install the agent

Still logged in to the Pi, run this one line:

```bash
curl -fsSL https://raw.githubusercontent.com/jstader/partyfax/main/pi/install.sh | sudo bash
```

`sudo` asks for the password you set in Imager. The installer then downloads
the agent and asks for the **printer's broker password** (paste it; nothing
shows as you type). It also locks the Pi down: SSH by key only, automatic
security updates (rebooting at 04:00 only when an update needs it), and
Bluetooth off. Last, it sets the agent to start on every boot and, if the
printer is already plugged in, offers to print a test receipt.

## 4. Connect the printer

1. Plug the OTG adapter into the Pi's **other** micro-USB port, labelled
   **USB**, in the middle of the board. Plug the printer's USB cable into the
   adapter.
2. Switch the printer on.
3. Check the agent sees it:

   ```bash
   journalctl -u partyfax-agent -f
   ```

   You should see `connected, subscribed, announced online`. Press Ctrl+C to
   stop watching.
4. Open [partyfax.nilanet.live](https://partyfax.nilanet.live) and print
   something. It should come out within a few seconds.

Reboot the Pi once (`sudo reboot`) so the Wi-Fi power-saving change takes
effect. After that it runs on its own: unplugging and replugging the Pi or
the printer is fine.

## Updating

Run the same install line again. It downloads the latest agent built from
`main`, keeps your password, and restarts the service.

The installer also asks, once, for a **Cloudflare Access service token**
(Client ID and Client Secret). That lets the Pi through when Access guards
the broker; see "Cloudflare Access on /mqtt" in `deploy/README.md`. Press
Enter to skip it if Access isn't set up. The first log line
after a restart shows the version (the commit it was built from).

## When something's wrong

| What you see | What to do |
|---|---|
| The site says the printer is offline | `journalctl -u partyfax-agent -n 30` on the Pi. `not authorized` means a wrong password: `sudo nano /etc/partyfax/agent.env`, fix it, then `sudo systemctl restart partyfax-agent`. |
| Online, but prints say "did not confirm" | The printer isn't attached. Check that it's on and plugged into **USB**, not PWR, and that `ls /dev/usb/lp0` finds it. |
| `ls /dev/usb/lp0` finds nothing | Run `lsusb` with the printer on. If the printer isn't listed, try another cable or OTG adapter: some cables only carry power. |
| Keeps disconnecting and reconnecting | Weak Wi-Fi or power saving. Make sure you rebooted after installing, and move the Pi closer to the router. |
| Test print: "paper sensor did not answer" | Harmless. The printer can't report its paper level, so jobs print without that check. |

To print a test receipt yourself:

```bash
sudo systemctl stop partyfax-agent
sudo -u partyfax partyfax-agent -selftest
sudo systemctl start partyfax-agent
```

## How it works

`partyfax-agent` (this directory, Go) does the job of upstream's ESP32-S3
firmware: it dials `wss://partyfax.nilanet.live/mqtt`, subscribes to
`fax/printer-1/job/+`, checks for paper with `DLE EOT 4`, writes the
server's ESC/POS to `/dev/usb/lp0`, and acks. The server can't tell the two
apart. The timeouts (10 s for the printer to appear,
20 s per job) are what `backend/internal/timeouts` expects.

Settings live in `/etc/partyfax/agent.env`. Only the password is required;
see `agent.env.example` for the rest. To build the binary yourself instead
of downloading it, run `./pi/build.sh` on any machine with Go or Docker.
