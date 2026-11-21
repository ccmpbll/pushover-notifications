# Pushover Notifications

> An unofficial, community-made extension for [Prusa Connect](https://github.com/prusa3d/prusa-connect).
> Not made, endorsed or supported by Prusa Research.

Sends a [Pushover](https://pushover.net) notification when a print starts, finishes or fails, or when a printer needs you.

## What it does

- Tells you when a print **starts**, **finishes** or **fails**, and optionally when it is **cancelled**. A print that stops without saying how (a power cut looks like that) is reported as a failure that says so.
- Tells you when a printer **shows a dialog and waits for you** (a filament runout, an error screen, a question), and when a printer **goes offline in the middle of a print**.
- You choose the title and text of each message with tags such as `{printer}`, `{file}`, `{time_printed}`, `{filament}` and `{progress}`, with a live preview. A line whose tag has no value is left out.
- Each message has its own Pushover priority and sound, and can carry a picture: the thumbnail inside the print file, or a snapshot from the printer's camera.
- With the Tasmota Plugs extension installed, `{energy}` and `{cost}` give the power a print used.
- Printers can be muted, so a test rig does not page you.

## Setup

Open the Pushover Notifications page and add your application token (from pushover.net/apps) and user key. **Send a test message** checks them, and each message type has its own **Send test**, written from its saved rule with sample values (a camera test uses the first printer that is on and has a camera, a thumbnail test the first printer with a print file). A viewer can read the page, an operator can also send tests, and saving needs an administrator. Saving restarts the extension for a few seconds. Times in a message are written in the time zone on the page, which starts as your browser's.

For pictures, give it the address of your Prusa Connect server (on port 80, 443, 8080 or 8443) and an API token (Settings, API credentials, New credential). Camera pictures need the scope `read`. Thumbnails need `operate`, because Connect asks the printer for the picture, and a token with `operate` can also control your printers, so use a camera picture if you would rather keep the token read-only. Without a token, messages go without pictures.

## Permissions

It asks for `observe`, to read print events, printer names and the current print. It connects to `api.pushover.net` on port 443, and, only if you set it, to your Prusa Connect address. It may call the Tasmota Plugs extension's routes.

## License

MIT, see `LICENSE`.
