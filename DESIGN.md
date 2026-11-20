# Design

A Prusa Connect service module (Go process, optional Svelte UI) that turns print events into Pushover messages. It replaces the notifications in PrintSpy, which sent them from its own poll loop. Sibling of [tasmota-plugs](https://github.com/ccmpbll/tasmota-plugs); the module layout, CI and release workflow there are the template.

## Why a separate extension

Print notifications are not tied to smart plugs. Anyone running Connect could use them. Connect's built-in `core/webhook` sink only posts raw events to one URL, with no formatting and no Pushover support, so it does not replace this.

## What PrintSpy did (the scope to match)

- Messages for: print started, progress checkpoints at chosen percentages, finished, failed, and printer error.
- Per event: priority, sound, and an image (camera snapshot, plate thumbnail, or none).
- Custom title and message templates.
- Verified live on 2026-07-10. Notes: `notes/printspy-notifications.md` in the workspace repo (service comparison and why Pushover was chosen).

## Connect facts it builds on

From the Connect docs in `.core/docs` (see `writing-a-module.md`, `event-types.md`):

- **Events to follow.** `com.prusa.connect.device.job.started` (job id, file, display name, start time) and `com.prusa.connect.device.job.settled` (outcome `finished`/`failed`/`cancelled`/`unknown`, time printed, filament used, last progress). Both arrive through `busapi.Subscribe`; `tasmota-plugs/follower.go` is a working example, including the resume cursor and retry.
- **Progress checkpoints.** Progress is telemetry at `/job/progress`, not in the started frame. Checkpoints mean following telemetry (`device.telemetry.sampled`, conflated and at-most-once) or reading it on a timer. Not yet decided which.
- **Printer errors and dialogs.** `device.issues.changed` carries the whole issue list, worst first, and any dialog the printer waits on.
- **Settings.** `module.yaml` `options` with `type: secret` for the Pushover app token and user key. The host masks a secret in listings and shows it empty in a form.
- **Network.** One `egress` entry: host `api.pushover.net`, port 443. List exactly what is sent.
- **Permission.** `observe` to read printer state and events.
- **Release.** Same marketplace flow as tasmota-plugs. The id must not contain "prusa". It will be `com.github.aux.ccmpbll.pushover-notifications`.

## Still open

1. **Per-printer rules.** One global setup with a mute list for now. Per-printer text or priority is not built.
2. **Plug alerts.** Tasmota Plugs could publish an event when it cuts power. Whether another module may subscribe to another module's own-prefix events is unverified.
3. **Progress checkpoints** (for example at 50%) mean following telemetry, which is conflated and at-most-once. Not decided.

## Decisions and findings (2026-11-19)

From the Connect docs and SDK in `.core`. Most of it is not tried on a real host yet.

**Printer problems.** `device.issues.changed` carries a dialog when a printer stops and waits for a person: how a firmware error, a filament runout or a question reaches us (`Title`, `Text`, `Code`). A machine that loses its connection in the middle of a print never sends a settled event, so "went offline during a print" is built from the started event plus `device.presence.changed` or `device.departed`, once per print. Never alert on offline alone: a printer switched off overnight, or by Tasmota Plugs on purpose, reads as departed.

**Text tags.** A title and a message per type, with tags (`{printer}`, `{file}`, `{outcome}`, `{progress}`, `{time_printed}`, `{filament}`, `{energy}`, `{cost}`, `{started}`, `{ended}`, `{dialog_title}`, `{dialog_text}`). A line is left out when a tag in it has no value. An unknown tag is refused when saving. Tests hold the page's defaults and tag lists to `module.yaml` and `template.go`.

**Power used.** A module may call another module's routes (`calls:` and `suggest:` in the manifest). The follower reads each plug linked to the printer from Tasmota Plugs (`GET /plugs`, `total_kwh`) when a print starts and when it ends, and takes the difference. The page offers `{energy}` and `{cost}` only when that extension is installed. Not yet checked: that a module calling as itself may read those routes.

**Pictures.** A module has no wire call that returns a picture, but Connect's HTTP API does: `GET /api/v1/devices/{id}/files/thumbnail?path=...` (the picture inside a print file, held by the server and prefetched) and `GET /api/v1/cameras/{name}/snapshot`. The module calls them with an API token and the server's address (`connect_url` and `connect_token`, one egress entry), so camera passwords and push cameras need no special handling. The thumbnail is fetched when a print starts, while the printer is there to ask. A camera is chosen per printer on the page, and the page can list cameras because Connect's camera settings carry the printer each watches. Checked on a real host (2026-11-19): the camera route works with a `read` token and a write is refused; the thumbnail route answers 403 to a `read` token, even naming the file's size and modified time, and needs `operate`, so a thumbnail means a token that can also control printers. The page says so. A job's `Path` comes with a leading slash (`/usb/tangle.bgcode`), which is stripped.

**Settings.** The module's settings are its resource config. Saving writes them through the host and the module restarts, as in Tasmota Plugs. A credential reads back as `xxxxx`, and sending that back keeps the stored one. Events during the restart are lost unless the cursor is kept somewhere.

## Review fixes (2026-11-19)

Slow work is queued as a function the outbox runs, not done in the event-stream callback. The outbox runs that work a few pieces at a time and sends the results in queue order, so a send being retried never delays a power reading; the end of a print waits for its start's reading. A device's name and serial are remembered, so a muted printer stays muted when the host cannot answer. A dialog with no id is still reported once. `{started}` and `{ended}` use a time zone setting. The Connect address must use a port the manifest allows (80, 443, 8080, 8443), checked on the page and in the module.

## Built

Everything above except the release workflow, an install script and a run against a real host. Messages go out through a queue apart from the event stream, are retried on a temporary failure, and a replayed event is sent once.

Next: check it on a real host, progress checkpoints, remembering the stream cursor across a restart, per-printer rules.
