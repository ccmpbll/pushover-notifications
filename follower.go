package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/busapi"
	"github.com/prusa3d/prusa-connect/sdk/event"
)

const (
	firstRetry = 2 * time.Second
	lastRetry  = 30 * time.Second

	// seenLimit is how many events are remembered, so a replay after the stream
	// drops does not send a message twice. Far more than a print farm ends in
	// the time a stream takes to come back.
	seenLimit = 256
)

// subscribeFunc opens the host's event stream, as busapi.Subscribe does on the
// module's session. It is a field so a test can feed windows without one.
type subscribeFunc func(ctx context.Context, p busapi.SubscribeParams, onWindow func(busapi.Window) error) error

// follower reads the events of every printer the module may observe and
// queues one message for each the settings ask for.
type follower struct {
	subscribe subscribeFunc
	// lookup answers a device's name and serial. A device it cannot name is
	// still reported, as "Printer".
	lookup func(ctx context.Context, deviceID string) (name, serial string, err error)
	// current answers the print a device has on its bed: the file and how far it
	// got. It reads the host's record, so it answers for a printer that is off.
	current func(ctx context.Context, deviceID string) (file string, progress float64, ok bool)
	// energy answers what a printer's plugs have used in all. Nil, or not ok,
	// means there are no power figures.
	energy func(ctx context.Context, serial string) (powerReading, bool)
	// thumbnail and snapshot fetch a picture from Prusa Connect.
	thumbnail func(ctx context.Context, s Settings, deviceID, path string) ([]byte, string, error)
	snapshot  func(ctx context.Context, s Settings, camera string) ([]byte, string, error)
	// put queues the work behind a message and says whether there was room.
	put    func(work) bool
	log    *slog.Logger
	cursor string
	seen   map[string]struct{}
	order  []string
	// jobs is the print each device is running, by device id, from the started
	// event to the settled one. dialogs is the dialog each device last showed.
	jobs    map[string]*openJob
	dialogs map[string]string
	// devices is what the host last said of each device, for when it cannot.
	devices map[string]device
}

// device is a printer's name and serial.
type device struct{ name, serial string }

// openJob is what is remembered of a print while it runs.
type openJob struct {
	id, path, file string
	// base is what the printer's plugs had used when the print began.
	base    powerReading
	hasBase bool
	// thumb is the picture inside the print file, fetched while the printer was
	// there to be asked.
	thumb     []byte
	thumbType string
	// lost is set once the printer going offline was reported.
	lost bool
	// ready is closed when the start work has taken the baseline and tried for
	// the thumbnail. Work for the end of the print, or a dialog, waits for it:
	// the outbox runs work at once and the start's is not always done first.
	ready  chan struct{}
	finish func()
	// mu guards base, hasBase, thumb and thumbType, which pieces of work running
	// at once may fill and read.
	mu sync.Mutex
}

// readyLimit is the longest work waits for a print's start work.
//
//nolint:gochecknoglobals // a test shortens it.
var readyLimit = 30 * time.Second

// newOpenJob is a print that has begun.
func newOpenJob(id, path, file string) *openJob {
	open := &openJob{id: id, path: path, file: file, ready: make(chan struct{})}
	open.finish = sync.OnceFunc(func() { close(open.ready) })

	return open
}

// wait holds work back until the start work is done, for as long as it is
// allowed to take. A print with no start work is not waited for.
func (o *openJob) wait(ctx context.Context) {
	if o == nil {
		return
	}

	select {
	case <-o.ready:
	case <-ctx.Done():
	case <-time.After(readyLimit):
	}
}

// Run follows the stream until ctx ends. A failed subscription is retried with
// a growing wait, and a restart resumes from the last cursor, so a gap is
// reported by the host as Resync and not lost silently.
func (f *follower) Run(ctx context.Context) {
	params := busapi.SubscribeParams{Types: []string{
		event.TypeDeviceJobStarted, event.TypeDeviceJobSettled,
		event.TypeDeviceIssuesChanged, event.TypeDevicePresenceChanged, event.TypeDeviceDeparted,
	}}

	for wait := firstRetry; ; {
		params.Resume = f.cursor
		read := false

		err := f.subscribe(ctx, params, func(w busapi.Window) error {
			read = true

			return f.window(ctx, w)
		})

		select {
		case <-ctx.Done():
			return
		default:
			f.log.WarnContext(ctx, "the event stream ended, trying again", "error", err, "wait", wait)
		}

		if read {
			wait = firstRetry
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		wait = min(wait+wait, lastRetry)
	}
}

// window handles each event of one window and keeps the cursor.
func (f *follower) window(ctx context.Context, w busapi.Window) error {
	if w.Resync {
		f.log.Warn("the stream lost continuity: events may have been missed")
	}

	for _, raw := range w.Events {
		f.handle(ctx, raw)
	}

	f.cursor = w.Cursor

	return nil
}

func (f *follower) handle(ctx context.Context, raw json.RawMessage) {
	current := settings()
	if !current.configured() {
		return
	}

	var envelope event.Envelope
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}

	origin, err := event.ParseOrigin(envelope.Subject)
	if err != nil || origin.Kind() != event.KindDevice {
		return
	}

	switch envelope.Type {
	case event.TypeDeviceJobStarted, event.TypeDeviceJobSettled:
		f.job(ctx, current, envelope, origin.ID())
	case event.TypeDeviceIssuesChanged:
		f.issues(ctx, current, envelope, origin.ID())
	case event.TypeDevicePresenceChanged:
		var presence struct {
			Connected bool `json:"connected"`
		}

		if json.Unmarshal(envelope.Data, &presence) == nil && !presence.Connected {
			f.offline(ctx, current, origin.ID())
		}
	case event.TypeDeviceDeparted:
		f.offline(ctx, current, origin.ID())
	}
}

// who is a device's name and serial. A device is looked up each time, so a
// renamed printer is called by its new name; when the host cannot answer, what
// was last learned is used, so a printer that is muted stays muted. A device
// that was never answered for is called "Printer" and cannot be muted by serial.
func (f *follower) who(ctx context.Context, deviceID string) (name, serial string) {
	name, serial, err := f.lookup(ctx, deviceID)
	if err != nil {
		f.log.WarnContext(ctx, "the printer could not be named", "device", deviceID, "error", err)

		if known, ok := f.devices[deviceID]; ok {
			return known.name, known.serial
		}

		return "", ""
	}

	if f.devices == nil {
		f.devices = map[string]device{}
	}

	f.devices[deviceID] = device{name: name, serial: serial}

	return name, serial
}

// queue puts work behind a message, or says the queue is full. Everything that
// asks another server for something is in that work and not here, because this
// runs in the callback of the host's event stream and a slow call would hold the
// whole stream up.
func (f *follower) queue(ctx context.Context, w work, what string) bool {
	if f.put(w) {
		return true
	}

	f.log.WarnContext(ctx, "the notification queue is full, a message was dropped", "message", what)

	return false
}

// job handles a started or a settled event.
func (f *follower) job(ctx context.Context, s Settings, envelope event.Envelope, deviceID string) {
	var ids struct {
		JobID string `json:"job_id"`
	}

	if err := json.Unmarshal(envelope.Data, &ids); err != nil {
		f.log.WarnContext(ctx, "an event could not be read", "type", envelope.Type, "error", err)

		return
	}

	key := envelope.Type + "/" + ids.JobID
	if ids.JobID != "" && f.known(key) {
		return
	}

	name, serial := f.who(ctx, deviceID)

	var (
		task work
		open *openJob
	)

	if envelope.Type == event.TypeDeviceJobStarted {
		task, open = f.started(ctx, s, envelope, ids.JobID, deviceID, name, serial)
	} else {
		task = f.settled(ctx, s, envelope, ids.JobID, deviceID, name, serial)
	}

	if task == nil || !f.queue(ctx, task, key) {
		// A start that is never queued never does its work, so what waits on it
		// must not.
		if open != nil {
			open.finish()
		}

		return
	}

	if ids.JobID != "" {
		f.remember(key)
	}
}

// started remembers the print and makes the work for its message: what the
// plugs have used so far, the picture inside the file if any message wants it,
// then the message. The print is returned so the caller can release anything
// waiting on it if the work is never queued. Nil work means nothing to do.
func (f *follower) started(ctx context.Context, s Settings, envelope event.Envelope, jobID, deviceID, name, serial string) (work, *openJob) {
	if s.muted(serial) {
		return nil, nil
	}

	var started event.JobStarted
	if json.Unmarshal(envelope.Data, &started) != nil {
		return nil, nil
	}

	kind, facts, err := jobFacts(name, envelope.Type, envelope.Data, s.location())
	if err != nil {
		f.log.WarnContext(ctx, "an event could not be turned into a message", "type", envelope.Type, "error", err)

		return nil, nil
	}

	open := newOpenJob(jobID, started.Path, fileOf(started.DisplayName, started.Path))

	if f.jobs == nil {
		f.jobs = map[string]*openJob{}
	}

	f.jobs[deviceID] = open

	return func(ctx context.Context) (Message, bool) {
		// Whatever happens, release the work waiting on this one.
		defer open.finish()

		if f.energy != nil {
			base, ok := f.energy(ctx, serial)

			open.mu.Lock()
			open.base, open.hasBase = base, ok
			open.mu.Unlock()
		}

		if wantsThumbnail(s) {
			f.fetchThumbnail(ctx, s, open, deviceID)
		}

		// The message itself, and a camera picture, can take a while and nothing
		// waits for them.
		open.finish()

		return f.message(ctx, s, kind, facts, deviceID, serial, open)
	}, open
}

// settled makes the work for the message about a print that ended, with the
// power it used when that can be known. The print is forgotten here and not in
// the work, because the work runs later and a new print may have started.
func (f *follower) settled(ctx context.Context, s Settings, envelope event.Envelope, jobID, deviceID, name, serial string) work {
	open := f.jobs[deviceID]
	if open != nil && open.id != jobID {
		open = nil
	}

	delete(f.jobs, deviceID)

	if s.muted(serial) {
		return nil
	}

	kind, facts, err := jobFacts(name, envelope.Type, envelope.Data, s.location())
	if err != nil {
		f.log.WarnContext(ctx, "an event could not be turned into a message", "type", envelope.Type, "error", err)

		return nil
	}

	return func(ctx context.Context) (Message, bool) {
		open.wait(ctx)
		f.addEnergy(ctx, &facts, open, serial)

		return f.message(ctx, s, kind, facts, deviceID, serial, open)
	}
}

// message writes a message and fetches its picture. It runs in the outbox.
func (f *follower) message(ctx context.Context, s Settings, kind string, facts facts, deviceID, serial string, open *openJob) (Message, bool) {
	message, ok := build(s, kind, facts)
	if !ok {
		return Message{}, false
	}

	message.Image, message.ImageType = f.picture(ctx, s, s.Rules[kind], deviceID, serial, open)

	return message, true
}

// wantsThumbnail says whether any message that is switched on carries the
// picture inside the print file.
func wantsThumbnail(s Settings) bool {
	for _, rule := range s.Rules {
		if rule.On && rule.Image == imageThumbnail {
			return true
		}
	}

	return false
}

func (f *follower) fetchThumbnail(ctx context.Context, s Settings, open *openJob, deviceID string) {
	open.mu.Lock()
	defer open.mu.Unlock()

	if open.thumb != nil || open.path == "" || f.thumbnail == nil {
		return
	}

	data, mediaType, err := f.thumbnail(ctx, s, deviceID, open.path)
	if err != nil {
		f.log.WarnContext(ctx, "no thumbnail", "device", deviceID, "error", err)

		return
	}

	open.thumb, open.thumbType = data, mediaType
}

// addEnergy puts the power the print used into the tags, when it can be known.
func (f *follower) addEnergy(ctx context.Context, facts *facts, open *openJob, serial string) {
	if f.energy == nil || open == nil {
		return
	}

	open.mu.Lock()
	base, has := open.base, open.hasBase
	open.mu.Unlock()

	if !has {
		return
	}

	end, ok := f.energy(ctx, serial)
	if !ok {
		return
	}

	maps.Copy(facts.vals, energyBetween(base, end))
}

// picture is the picture a rule asks for, or nil. A picture that cannot be got
// is logged and the message goes without it: a missing camera never costs a
// notification.
func (f *follower) picture(ctx context.Context, s Settings, rule Rule, deviceID, serial string, open *openJob) ([]byte, string) {
	switch rule.Image {
	case imageThumbnail:
		if open == nil {
			return nil, ""
		}

		open.wait(ctx)
		f.fetchThumbnail(ctx, s, open, deviceID)

		open.mu.Lock()
		defer open.mu.Unlock()

		return open.thumb, open.thumbType
	case imageCamera:
		camera := s.Cameras[serial]
		if camera == "" || f.snapshot == nil {
			return nil, ""
		}

		data, mediaType, err := f.snapshot(ctx, s, camera)
		if err != nil {
			f.log.WarnContext(ctx, "no camera picture", "camera", camera, "error", err)

			return nil, ""
		}

		return data, mediaType
	default:
		return nil, ""
	}
}

// issues handles what the host has found wrong with a printer. Only a dialog
// is worth a message: the printer has stopped and is waiting for a person.
func (f *follower) issues(ctx context.Context, s Settings, envelope event.Envelope, deviceID string) {
	var change struct {
		Dialog *struct {
			ID    json.RawMessage `json:"id"`
			Title string          `json:"title"`
			Text  string          `json:"text"`
		} `json:"dialog"`
	}

	if err := json.Unmarshal(envelope.Data, &change); err != nil {
		f.log.WarnContext(ctx, "an event could not be read", "type", envelope.Type, "error", err)

		return
	}

	if change.Dialog == nil {
		delete(f.dialogs, deviceID)

		return
	}

	// A dialog with no id is still a dialog: the two-value form tells "never
	// reported" from "reported, with an empty id".
	key := string(change.Dialog.ID)
	if last, seen := f.dialogs[deviceID]; seen && last == key {
		return
	}

	if f.dialogs == nil {
		f.dialogs = map[string]string{}
	}

	f.dialogs[deviceID] = key

	name, serial := f.who(ctx, deviceID)
	if s.muted(serial) {
		return
	}

	vals := map[string]string{
		"printer": printerName(name), "dialog_title": change.Dialog.Title, "dialog_text": change.Dialog.Text,
	}
	open := f.jobs[deviceID]
	at := time.Now()

	f.queue(ctx, func(ctx context.Context) (Message, bool) {
		f.addPrint(ctx, vals, deviceID)

		return f.message(ctx, s, "attention", facts{vals: vals, at: at}, deviceID, serial, open)
	}, "attention")
}

// offline handles a printer that went away. It is reported only while a print
// is running, and once: a machine that loses its connection in the middle of a
// print never sends a settled event, so this is the one way to hear of it. A
// printer that is off with no print running is how it spends the night.
func (f *follower) offline(ctx context.Context, s Settings, deviceID string) {
	open := f.jobs[deviceID]
	if open == nil || open.lost {
		return
	}

	open.lost = true

	name, serial := f.who(ctx, deviceID)
	if s.muted(serial) {
		return
	}

	vals := map[string]string{"printer": printerName(name), "file": open.file}
	at := time.Now()

	f.queue(ctx, func(ctx context.Context) (Message, bool) {
		f.addPrint(ctx, vals, deviceID)

		return f.message(ctx, s, "lost", facts{vals: vals, at: at}, deviceID, serial, nil)
	}, "lost")
}

// addPrint adds the file and how far the print got, from the host's record.
func (f *follower) addPrint(ctx context.Context, vals map[string]string, deviceID string) {
	if f.current == nil {
		return
	}

	if file, progress, ok := f.current(ctx, deviceID); ok {
		if file != "" {
			vals["file"] = file
		}

		vals["progress"] = percent(progress)
	}
}

func (f *follower) known(key string) bool {
	_, ok := f.seen[key]

	return ok
}

// remember keeps key, and forgets the oldest once there are more than
// seenLimit. It says whether the key is new.
func (f *follower) remember(key string) bool {
	if f.known(key) {
		return false
	}

	if f.seen == nil {
		f.seen = map[string]struct{}{}
	}

	f.seen[key] = struct{}{}
	f.order = append(f.order, key)

	if len(f.order) > seenLimit {
		delete(f.seen, f.order[0])
		f.order = f.order[1:]
	}

	return true
}
