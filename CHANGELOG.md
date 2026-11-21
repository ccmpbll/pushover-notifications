# Changelog

## Unreleased

- A test for each message type: Send test on its row sends that type as the saved rule writes it (title, text, priority, sound, picture) with sample values, marked Test, even when the type is switched off. A camera picture uses the first printer with a camera chosen, a thumbnail the first printer with a print file on record, and a picture that cannot be got is said so on the row while the text still goes.
- Sends a Pushover message for a print that started, finished, failed or was cancelled, and for a printer that shows a dialog and waits, or goes offline during a print.
- A print that ends without a result is reported as a failure that says so.
- A settings page: account, a test message, and for each message type on or off, priority, sound, title and text with tags and a live preview, and a picture. Printers can be muted and given a camera.
- Pictures from Prusa Connect: the thumbnail inside the print file, or a camera snapshot, using an API token (`read` for a camera, `operate` for a thumbnail).
- With Tasmota Plugs installed, `{energy}` and `{cost}` give the power a print used.
- Messages are sent in order, apart from the event stream, retried on a temporary failure, and never repeated when the stream replays.
- Slow work (pictures, power readings, a printer's current print) runs apart from the host's event stream, a few pieces at a time, and apart from sending: a send being retried never holds up the power reading for the next print, messages still go out in order, and at most a handful wait with their pictures.
- A time zone setting for `{started}` and `{ended}`; it starts as the browser's.
