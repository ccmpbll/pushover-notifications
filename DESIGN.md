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

## Open questions, check before building

1. **Images.** Whether a module can read a camera snapshot or a plate thumbnail, and under which permission. The docs describe a module pushing frames *to* a camera (`operate`), not reading them. If it cannot, messages go out without an image at first.
2. **Settings page.** The tasmota-plugs setup page saves by restarting the extension. Check whether options can change without a restart before copying that, since a notification module should not drop events while it restarts. The resume cursor covers a short gap.
3. **Per-printer rules.** One global setup, or per printer (a printer that is only a test rig should not page anyone)? Start global with an exclude list.
4. **Plug alerts.** Tasmota Plugs could publish an event when it cuts power (see its ROADMAP). Whether another module may subscribe to another module's own-prefix events is unverified.
5. **Message templates.** Which fields a template may use (printer name, file, outcome, time printed, filament, percent). Keep PrintSpy's defaults.

## First version

1. Subscribe to started and settled for every printer the module may observe.
2. Send Pushover messages for started, finished and failed, with priority and sound settings.
3. Secrets and egress as above, a test-message button on a small settings page.
4. CI and release workflow copied from tasmota-plugs.

Then, in order: progress checkpoints, printer errors, images, templates, plug alerts.
