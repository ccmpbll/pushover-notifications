package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/busapi"
	"github.com/prusa3d/prusa-connect/sdk/event"
)

// queued stands in for the outbox. It does each piece of work as it is put, as
// the outbox would a moment later, so a test reads the message at once; with
// hold set it keeps the work back until release, to show what the stream
// callback itself does and does not do.
type queued struct {
	mu   sync.Mutex
	got  []Message
	full bool
	hold bool
	held []work
}

func (q *queued) put(w work) bool {
	q.mu.Lock()

	if q.full {
		q.mu.Unlock()

		return false
	}

	if q.hold {
		q.held = append(q.held, w)
		q.mu.Unlock()

		return true
	}

	q.mu.Unlock()
	q.run(w)

	return true
}

func (q *queued) run(w work) {
	if m, ok := w(context.Background()); ok {
		q.mu.Lock()
		q.got = append(q.got, m)
		q.mu.Unlock()
	}
}

// release does the work that was held back, in order.
func (q *queued) release() {
	q.mu.Lock()
	held := q.held
	q.held, q.hold = nil, false
	q.mu.Unlock()

	for _, w := range held {
		q.run(w)
	}
}

func (q *queued) titles() []string {
	q.mu.Lock()
	defer q.mu.Unlock()

	var titles []string
	for _, m := range q.got {
		titles = append(titles, m.Title)
	}

	return titles
}

func (q *queued) last() Message {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.got[len(q.got)-1]
}

func use(t *testing.T, s Settings) {
	t.Helper()

	held.Store(&s)
	t.Cleanup(func() { held.Store(nil) })
}

// useRaw runs the test on the settings a host would send for raw, over the
// token and user key every test needs.
func useRaw(t *testing.T, raw string) {
	t.Helper()

	s, err := parseSettings(t, `{"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`"`+raw+`}`)
	if err != nil {
		t.Fatal(err)
	}

	if err = s.validate(); err != nil {
		t.Fatal(err)
	}

	use(t, s)
}

const picturesOn = `,"connect_url":"http://127.0.0.1:8080","connect_token":"tok","cameras":"{\"SER-ABC\":\"bench-top\"}"`

type rig struct {
	f      *follower
	q      *queued
	logs   *bytes.Buffer
	thumbs []string
	snaps  []string
}

func newFollower(t *testing.T) *rig {
	t.Helper()

	r := &rig{q: &queued{}, logs: &bytes.Buffer{}}

	r.f = &follower{
		log: slog.New(slog.NewTextHandler(r.logs, nil)),
		lookup: func(_ context.Context, id string) (string, string, error) {
			switch id {
			case "abc":
				return "Core One", "SER-ABC", nil
			case "rig":
				return "Test rig", "SER-RIG", nil
			}

			return "", "", errors.New("no such device")
		},
		put: r.q.put,
		thumbnail: func(_ context.Context, _ Settings, id, path string) ([]byte, string, error) {
			r.thumbs = append(r.thumbs, id+" "+path)

			return []byte("THUMB"), "image/png", nil
		},
		snapshot: func(_ context.Context, _ Settings, camera string) ([]byte, string, error) {
			r.snaps = append(r.snaps, camera)

			return []byte("SNAP"), "image/jpeg", nil
		},
	}

	use(t, defaults(t))

	return r
}

func frame(t *testing.T, kind, device string, data json.RawMessage) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(event.Envelope{Type: kind, Subject: "device:" + device, Data: data})
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

func start(t *testing.T, id, device string) json.RawMessage {
	t.Helper()

	return frame(t, event.TypeDeviceJobStarted, device, started(t, event.JobStarted{JobID: id, DisplayName: "benchy.bgcode", Path: "/usb/benchy.bgcode", Started: base}))
}

func settle(t *testing.T, id, device, outcome string) json.RawMessage {
	t.Helper()

	return frame(t, event.TypeDeviceJobSettled, device, settled(t, event.JobSettled{
		JobID: id, DisplayName: "benchy.bgcode", Path: "/usb/benchy.bgcode", Started: base, Ended: base.Add(time.Hour),
		Outcome: outcome, Progress: 63,
	}))
}

func dialogFrame(t *testing.T, device, id, title string) json.RawMessage {
	t.Helper()

	data := `{"device_id":"` + device + `","issues":[{"reason":"awaiting_operator","severity":"error"}],"dialog":{"id":` + id + `,"title":"` + title + `","text":"Load filament and continue.","code":"10"}}`

	return frame(t, event.TypeDeviceIssuesChanged, device, json.RawMessage(data))
}

func clearedFrame(t *testing.T, device string) json.RawMessage {
	t.Helper()

	return frame(t, event.TypeDeviceIssuesChanged, device, json.RawMessage(`{"device_id":"`+device+`","issues":[]}`))
}

func presenceFrame(t *testing.T, device string, connected bool) json.RawMessage {
	t.Helper()

	data := `{"device_id":"` + device + `","driver":"x","connected":false}`
	if connected {
		data = `{"device_id":"` + device + `","driver":"x","connected":true}`
	}

	return frame(t, event.TypeDevicePresenceChanged, device, json.RawMessage(data))
}

func (r *rig) feed(events ...json.RawMessage) {
	_ = r.f.window(context.Background(), busapi.Window{Events: events})
}

func TestFollowerSubscribesToTheEventsItNeedsAndResumesFromTheCursor(t *testing.T) {
	r := newFollower(t)

	var (
		calls   []busapi.SubscribeParams
		ctx, cn = context.WithCancel(context.Background())
	)
	defer cn()

	r.f.subscribe = func(_ context.Context, p busapi.SubscribeParams, on func(busapi.Window) error) error {
		calls = append(calls, p)

		if len(calls) == 1 {
			_ = on(busapi.Window{Events: []json.RawMessage{start(t, "j1", "abc")}, Cursor: "c1"})

			return errors.New("stream dropped")
		}

		cn()

		return nil
	}

	done := make(chan struct{})

	go func() { r.f.Run(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(firstRetry + 3*time.Second):
		t.Fatal("the follower did not stop when its context ended")
	}

	for _, want := range []string{
		event.TypeDeviceJobStarted, event.TypeDeviceJobSettled, event.TypeDeviceIssuesChanged,
		event.TypeDevicePresenceChanged, event.TypeDeviceDeparted,
	} {
		if !contains(calls[0].Types, want) {
			t.Errorf("not subscribed to %s: %v", want, calls[0].Types)
		}
	}

	if len(calls) != 2 || calls[1].Resume != "c1" {
		t.Errorf("calls %+v, want a second one that resumes from c1", calls)
	}
}

func TestAStartedPrintBecomesAMessageNamingThePrinter(t *testing.T) {
	r := newFollower(t)

	_ = r.f.window(context.Background(), busapi.Window{Events: []json.RawMessage{start(t, "j1", "abc")}, Cursor: "c1"})

	if got := r.q.titles(); len(got) != 1 || got[0] != "Core One: print started" {
		t.Fatalf("queued %v", got)
	}

	if r.f.cursor != "c1" {
		t.Errorf("cursor %q", r.f.cursor)
	}
}

func TestAMutedPrinterIsNotReportedByItsSerial(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"muted_printers":"SER-RIG"`)

	r.feed(start(t, "j1", "rig"), start(t, "j2", "abc"))

	if got := r.q.titles(); len(got) != 1 || got[0] != "Core One: print started" {
		t.Fatalf("queued %v, want only the printer that is not muted", got)
	}
}

func TestNothingIsQueuedWhileUnconfigured(t *testing.T) {
	r := newFollower(t)

	// The defaults say every message is on, so only the missing credentials
	// stop them.
	empty, err := parseSettings(t, `{}`)
	if err != nil {
		t.Fatal(err)
	}

	use(t, empty)

	r.feed(start(t, "j1", "abc"), dialogFrame(t, "abc", "1", "Runout"))

	if got := r.q.titles(); len(got) != 0 {
		t.Fatalf("queued %v", got)
	}
}

func TestTheSameEventSeenTwiceIsSentOnce(t *testing.T) {
	r := newFollower(t)

	w := busapi.Window{Events: []json.RawMessage{start(t, "j1", "abc")}}

	_ = r.f.window(context.Background(), w)
	_ = r.f.window(context.Background(), w)

	if got := r.q.titles(); len(got) != 1 {
		t.Fatalf("queued %v after a replay", got)
	}

	// The end of the same print is a different message.
	r.feed(settle(t, "j1", "abc", event.JobFinished))

	if got := r.q.titles(); len(got) != 2 {
		t.Fatalf("queued %v, want the start and the end", got)
	}
}

func TestADeviceTheHostCannotNameStillGetsAMessage(t *testing.T) {
	r := newFollower(t)

	r.feed(start(t, "j1", "unknown"))

	if got := r.q.titles(); len(got) != 1 || got[0] != "Printer: print started" {
		t.Fatalf("queued %v", got)
	}

	if !strings.Contains(r.logs.String(), "no such device") {
		t.Errorf("the failed lookup was not logged: %s", r.logs)
	}
}

func TestAnEventItCannotReadIsSkippedAndTheRestStillGo(t *testing.T) {
	r := newFollower(t)

	r.feed(
		json.RawMessage(`not json`),
		frame(t, event.TypeDeviceJobStarted, "abc", json.RawMessage(`"not an object"`)),
		frame(t, "com.prusa.connect.device.arrived", "abc", json.RawMessage(`{}`)),
		start(t, "j1", "abc"),
	)

	if got := r.q.titles(); len(got) != 1 {
		t.Fatalf("queued %v", got)
	}
}

func TestAFullQueueIsLoggedAndTheWindowIsStillAccepted(t *testing.T) {
	r := newFollower(t)
	r.q.full = true

	if err := r.f.window(context.Background(), busapi.Window{Events: []json.RawMessage{start(t, "j1", "abc")}, Cursor: "c2"}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(r.logs.String(), "queue is full") {
		t.Errorf("not logged: %s", r.logs)
	}

	if r.f.cursor != "c2" {
		t.Errorf("cursor %q", r.f.cursor)
	}
}

func TestALostStreamIsWarnedAbout(t *testing.T) {
	r := newFollower(t)

	_ = r.f.window(context.Background(), busapi.Window{Resync: true})

	if !strings.Contains(r.logs.String(), "missed") {
		t.Errorf("no warning: %s", r.logs)
	}
}

func TestTheSeenListIsBounded(t *testing.T) {
	r := newFollower(t)

	for i := range seenLimit + 50 {
		r.f.remember("k" + strings.Repeat("x", i))
	}

	if len(r.f.seen) > seenLimit {
		t.Fatalf("%d keys kept, limit %d", len(r.f.seen), seenLimit)
	}
}

// Power.

func TestThePowerAPrintUsedIsTheDifferenceBetweenItsStartAndItsEnd(t *testing.T) {
	r := newFollower(t)

	totals := []float64{10, 10.42}
	price := 0.14

	r.f.energy = func(_ context.Context, serial string) (powerReading, bool) {
		if serial != "SER-ABC" {
			t.Errorf("asked about %q", serial)
		}

		reading := powerReading{kWh: totals[0], plugs: 1, price: &price, currency: "$"}
		totals = totals[1:]

		return reading, true
	}

	useRaw(t, `,"body_finished":"{file}\nUsed {energy} ({cost})"`)

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))

	if got := r.q.last().Body; got != "benchy.bgcode\nUsed 0.42 kWh ($0.06)" {
		t.Errorf("body %q", got)
	}
}

func TestWithoutTheTasmotaModuleThePowerLinesAreLeftOut(t *testing.T) {
	r := newFollower(t)
	r.f.energy = func(context.Context, string) (powerReading, bool) { return powerReading{}, false }

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))

	if got := r.q.last().Body; strings.Contains(got, "Used") {
		t.Errorf("body %q", got)
	}
}

func TestAPrintWhoseStartWasNotSeenHasNoPowerFigure(t *testing.T) {
	r := newFollower(t)

	r.f.energy = func(context.Context, string) (powerReading, bool) { return powerReading{kWh: 5, plugs: 1}, true }

	r.feed(settle(t, "j9", "abc", event.JobFinished))

	if got := r.q.last().Body; strings.Contains(got, "Used") {
		t.Errorf("body %q: a figure with no start to compare to", got)
	}
}

// Pictures.

func TestAThumbnailIsFetchedWhenThePrintStartsAndSentWithTheEnd(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"image_finished":"thumbnail"`+picturesOn)

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))

	if len(r.thumbs) != 1 || r.thumbs[0] != "abc /usb/benchy.bgcode" {
		t.Fatalf("thumbnails asked for: %v, want one, at the start, while the printer is there", r.thumbs)
	}

	if m := r.q.last(); string(m.Image) != "THUMB" || m.ImageType != "image/png" {
		t.Errorf("%q %q", m.Image, m.ImageType)
	}

	// The started message asked for no picture.
	if r.q.got[0].Image != nil {
		t.Errorf("the started message carries a picture")
	}
}

func TestAThumbnailThatFailedAtTheStartIsTriedOnceMoreAtTheEnd(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"image_finished":"thumbnail"`+picturesOn)

	calls := 0
	r.f.thumbnail = func(context.Context, Settings, string, string) ([]byte, string, error) {
		calls++
		if calls == 1 {
			return nil, "", errors.New("the printer did not answer")
		}

		return []byte("LATE"), "image/png", nil
	}

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))

	if calls != 2 || string(r.q.last().Image) != "LATE" {
		t.Errorf("calls %d image %q", calls, r.q.last().Image)
	}
}

func TestACameraPictureIsTakenWhenTheMessageIsMade(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"image_failed":"camera"`+picturesOn)

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFailed))

	if len(r.snaps) != 1 || r.snaps[0] != "bench-top" {
		t.Fatalf("snapshots asked for: %v", r.snaps)
	}

	if m := r.q.last(); string(m.Image) != "SNAP" || m.ImageType != "image/jpeg" {
		t.Errorf("%q %q", m.Image, m.ImageType)
	}
}

func TestAPrinterWithNoCameraGetsTheMessageWithoutAPicture(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"image_failed":"camera"`+picturesOn)

	r.feed(start(t, "j1", "rig"), settle(t, "j1", "rig", event.JobFailed))

	if len(r.snaps) != 0 {
		t.Errorf("asked for %v", r.snaps)
	}

	if m := r.q.last(); m.Image != nil || m.Title != "Printer: print failed" && m.Title != "Test rig: print failed" {
		t.Errorf("%+v", m)
	}
}

func TestAPictureThatCannotBeFetchedNeverStopsTheMessage(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"image_failed":"camera"`+picturesOn)

	r.f.snapshot = func(context.Context, Settings, string) ([]byte, string, error) {
		return nil, "", errors.New("Prusa Connect answered HTTP 502, so there is no picture")
	}

	r.feed(settle(t, "j1", "abc", event.JobFailed))

	if m := r.q.last(); m.Image != nil || m.Title != "Core One: print failed" {
		t.Errorf("%+v", m)
	}

	if !strings.Contains(r.logs.String(), "no picture") {
		t.Errorf("not logged: %s", r.logs)
	}
}

func TestAMessageThatAsksForNoPictureFetchesNone(t *testing.T) {
	r := newFollower(t)

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))

	if len(r.thumbs)+len(r.snaps) != 0 {
		t.Errorf("fetched %v %v", r.thumbs, r.snaps)
	}
}

// Printer problems.

func TestADialogOnThePrinterBecomesAMessageOnce(t *testing.T) {
	r := newFollower(t)
	r.f.current = func(_ context.Context, id string) (string, float64, bool) { return "benchy.bgcode", 41, true }

	r.feed(dialogFrame(t, "abc", "7", "Filament runout"), dialogFrame(t, "abc", "7", "Filament runout"))

	if got := r.q.titles(); len(got) != 1 || got[0] != "Core One needs you: Filament runout" {
		t.Fatalf("queued %v", got)
	}

	m := r.q.last()
	if m.Body != "Load filament and continue.\nbenchy.bgcode" || m.Priority != 1 {
		t.Errorf("%+v", m)
	}
}

func TestANewDialogAfterTheOldOneWasAnsweredIsAnotherMessage(t *testing.T) {
	r := newFollower(t)

	r.feed(dialogFrame(t, "abc", "7", "Filament runout"), clearedFrame(t, "abc"), dialogFrame(t, "abc", "8", "Filament runout"))

	if got := r.q.titles(); len(got) != 2 {
		t.Fatalf("queued %v", got)
	}

	// The same id after it was cleared is a new dialog too.
	r.feed(clearedFrame(t, "abc"), dialogFrame(t, "abc", "8", "Filament runout"))

	if got := r.q.titles(); len(got) != 3 {
		t.Fatalf("queued %v", got)
	}
}

func TestADialogOnAMutedPrinterOrWhileSwitchedOffSaysNothing(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"muted_printers":"SER-RIG"`)

	r.feed(dialogFrame(t, "rig", "1", "x"))

	if got := r.q.titles(); len(got) != 0 {
		t.Fatalf("queued %v", got)
	}

	useRaw(t, `,"notify_attention":"false"`)

	r.feed(dialogFrame(t, "abc", "1", "x"))

	if got := r.q.titles(); len(got) != 0 {
		t.Fatalf("queued %v", got)
	}
}

func TestAPrinterThatDropsOffInTheMiddleOfAPrintIsReportedOnce(t *testing.T) {
	r := newFollower(t)
	r.f.current = func(context.Context, string) (string, float64, bool) { return "benchy.bgcode", 41, true }

	r.feed(start(t, "j1", "abc"), presenceFrame(t, "abc", false), presenceFrame(t, "abc", false))

	got := r.q.titles()
	if len(got) != 2 || got[1] != "Core One went offline during a print" {
		t.Fatalf("queued %v", got)
	}

	if m := r.q.last(); m.Body != "benchy.bgcode\nLast seen at 41%" || m.Priority != 1 || m.Image != nil {
		t.Errorf("%+v", m)
	}
}

func TestADepartureCountsAsGoingOfflineToo(t *testing.T) {
	r := newFollower(t)

	r.feed(start(t, "j1", "abc"), frame(t, event.TypeDeviceDeparted, "abc", json.RawMessage(`{"device_id":"abc","driver":"x","reason":"closed"}`)))

	if got := r.q.titles(); len(got) != 2 || got[1] != "Core One went offline during a print" {
		t.Fatalf("queued %v", got)
	}
}

func TestAPrinterThatIsOfflineWithNoPrintRunningSaysNothing(t *testing.T) {
	r := newFollower(t)

	r.feed(presenceFrame(t, "abc", false))

	// A print that ended before it went offline, such as a power-off after the job.
	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished), presenceFrame(t, "abc", false))

	for _, title := range r.q.titles() {
		if strings.Contains(title, "offline") {
			t.Fatalf("queued %v", r.q.titles())
		}
	}
}

func TestComingBackOnlineIsNotAMessage(t *testing.T) {
	r := newFollower(t)

	r.feed(start(t, "j1", "abc"), presenceFrame(t, "abc", true))

	if got := r.q.titles(); len(got) != 1 {
		t.Fatalf("queued %v", got)
	}
}

func TestAnotherPrintsStartIsNotABaselineForThisOne(t *testing.T) {
	r := newFollower(t)

	r.f.energy = func(context.Context, string) (powerReading, bool) { return powerReading{kWh: 5, plugs: 1}, true }

	r.feed(start(t, "j1", "abc"), settle(t, "j2", "abc", event.JobFinished))

	if got := r.q.last().Body; strings.Contains(got, "Used") {
		t.Errorf("body %q: the baseline belonged to another print", got)
	}
}

func TestAPictureOnASwitchedOffMessageIsNotFetchedAtTheStart(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"notify_finished":"false","image_finished":"thumbnail"`+picturesOn)

	r.feed(start(t, "j1", "abc"))

	if len(r.thumbs) != 0 {
		t.Errorf("fetched %v for a message that will not be sent", r.thumbs)
	}
}

// Events exactly as a real host sent them (a demo Connect server with simulated
// printers, testdata/), so the shapes this module reads are the shapes it gets.
func TestEventsAsARealHostSendsThemAreUnderstood(t *testing.T) {
	read := func(name string) json.RawMessage {
		raw, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}

		return raw
	}

	r := newFollower(t)

	// The demo's device id is not one the rig knows, so the printer is "Printer".
	r.feed(read("job-started.json"), read("job-settled.json"))

	got := r.q.titles()
	if len(got) != 2 || got[0] != "Printer: print started" || got[1] != "Printer: print failed" {
		t.Fatalf("queued %v", got)
	}

	if r.q.got[0].Body != "tangle.bgcode" {
		t.Errorf("started body %q", r.q.got[0].Body)
	}

	if body := r.q.last().Body; !strings.HasPrefix(body, "tangle.bgcode\nStopped at 76%") || r.q.last().Priority != 1 {
		t.Errorf("settled body %q priority %d", body, r.q.last().Priority)
	}
}

func TestNothingSlowHappensInTheStreamCallback(t *testing.T) {
	r := newFollower(t)
	r.q.hold = true
	useRaw(t, `,"image_finished":"thumbnail","image_failed":"camera"`+picturesOn)

	calls := 0
	count := func() { calls++ }

	r.f.energy = func(context.Context, string) (powerReading, bool) {
		count()
		return powerReading{kWh: 1, plugs: 1}, true
	}
	r.f.current = func(context.Context, string) (string, float64, bool) { count(); return "f", 1, true }
	r.f.thumbnail = func(context.Context, Settings, string, string) ([]byte, string, error) {
		count()

		return []byte("T"), "image/png", nil
	}
	r.f.snapshot = func(context.Context, Settings, string) ([]byte, string, error) {
		count()

		return []byte("S"), "image/jpeg", nil
	}

	r.feed(
		start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFailed),
		dialogFrame(t, "abc", "1", "x"), start(t, "j2", "abc"), presenceFrame(t, "abc", false),
	)

	if calls != 0 {
		t.Fatalf("%d slow calls were made while reading the stream", calls)
	}

	if len(r.q.held) != 5 {
		t.Fatalf("%d pieces of work held, want one for each of the two starts, the end, the dialog and the offline", len(r.q.held))
	}

	r.q.release()

	if calls == 0 || len(r.q.titles()) != 5 {
		t.Errorf("after release: %d calls, messages %v", calls, r.q.titles())
	}
}

func TestPrintsWorkRunsInTheOrderTheyHappened(t *testing.T) {
	r := newFollower(t)
	r.q.hold = true

	totals := []float64{10, 10.5}

	r.f.energy = func(context.Context, string) (powerReading, bool) {
		reading := powerReading{kWh: totals[0], plugs: 1}
		totals = totals[1:]

		return reading, true
	}

	useRaw(t, `,"body_finished":"Used {energy}"`)

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))
	r.q.release()

	if got := r.q.last().Body; got != "Used 0.50 kWh" {
		t.Errorf("body %q", got)
	}
}

func TestAPrinterStaysMutedWhenTheHostCannotNameItAnyMore(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"muted_printers":"SER-RIG"`)

	working := true
	lookup := r.f.lookup

	r.f.lookup = func(ctx context.Context, id string) (string, string, error) {
		if !working {
			return "", "", errors.New("the host did not answer")
		}

		return lookup(ctx, id)
	}

	r.feed(start(t, "j1", "rig"))

	working = false

	r.feed(settle(t, "j1", "rig", event.JobFinished), dialogFrame(t, "rig", "1", "x"), start(t, "j2", "rig"))

	if got := r.q.titles(); len(got) != 0 {
		t.Fatalf("a muted printer was reported while the host could not answer: %v", got)
	}

	// A printer not muted keeps the name it had.
	working = true
	r.feed(start(t, "j3", "abc"))

	working = false

	r.feed(settle(t, "j3", "abc", event.JobFinished))

	if got := r.q.titles(); len(got) != 2 || got[1] != "Core One: print finished" {
		t.Fatalf("queued %v, want the last known name", got)
	}
}

func TestADialogWithNoIdIsStillReportedOnce(t *testing.T) {
	r := newFollower(t)

	noID := frame(t, event.TypeDeviceIssuesChanged, "abc", json.RawMessage(`{"device_id":"abc","issues":[],"dialog":{"title":"Runout","text":"Load it"}}`))

	r.feed(noID, noID)

	if got := r.q.titles(); len(got) != 1 || got[0] != "Core One needs you: Runout" {
		t.Fatalf("queued %v", got)
	}
}

func TestAMutedPrinterThatDropsOffMidPrintIsNotReported(t *testing.T) {
	r := newFollower(t)
	useRaw(t, `,"muted_printers":"SER-RIG"`)

	// The host could not name the printer when the print began, so the print was
	// followed; by the time it drops off the host can, and it is a muted one.
	working := false
	lookup := r.f.lookup

	r.f.lookup = func(ctx context.Context, id string) (string, string, error) {
		if !working {
			return "", "", errors.New("the host did not answer")
		}

		return lookup(ctx, id)
	}

	r.feed(start(t, "j1", "rig"))

	if got := r.q.titles(); len(got) != 1 {
		t.Fatalf("queued %v: the start of an unnamed printer is reported", got)
	}

	working = true

	r.feed(presenceFrame(t, "rig", false))

	if got := r.q.titles(); len(got) != 1 {
		t.Fatalf("queued %v: a muted printer dropped off and was reported", got)
	}
}

func TestAPrintsEndWaitsForItsStartBeforeReadingThePower(t *testing.T) {
	r := newFollower(t)
	r.q.hold = true

	totals := []float64{10, 10.5}

	r.f.energy = func(context.Context, string) (powerReading, bool) {
		reading := powerReading{kWh: totals[0], plugs: 1}
		totals = totals[1:]

		return reading, true
	}

	useRaw(t, `,"body_finished":"Used {energy}"`)

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))

	startWork, endWork := r.q.held[0], r.q.held[1]

	// The two run at once in the outbox; the end must not read the power before
	// the start has taken its baseline, or it reads the baseline's number.
	done := make(chan Message, 1)

	go func() {
		m, _ := endWork(context.Background())
		done <- m
	}()

	select {
	case <-done:
		t.Fatal("the end of the print did its work before the start had taken its reading")
	case <-time.After(30 * time.Millisecond):
	}

	startWork(context.Background())

	select {
	case m := <-done:
		if m.Body != "Used 0.50 kWh" {
			t.Errorf("body %q", m.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the end never finished")
	}
}

func TestAPrintsEndIsNotHeldForAStartThatWasNeverQueued(t *testing.T) {
	r := newFollower(t)

	old := readyLimit
	readyLimit = 3 * time.Second

	t.Cleanup(func() { readyLimit = old })

	r.q.full = true
	r.feed(start(t, "j1", "abc"))
	r.q.full = false

	began := time.Now()

	r.feed(settle(t, "j1", "abc", event.JobFinished))

	if time.Since(began) > time.Second {
		t.Fatalf("the end waited %v for a start that was dropped", time.Since(began))
	}

	if got := r.q.titles(); len(got) != 1 || got[0] != "Core One: print finished" {
		t.Fatalf("queued %v", got)
	}
}

func TestADialogAndAnOfflineAreStampedWhenTheyArriveNotWhenTheyAreSent(t *testing.T) {
	r := newFollower(t)
	r.q.hold = true

	before := time.Now()

	r.feed(start(t, "j1", "abc"), dialogFrame(t, "abc", "1", "x"), presenceFrame(t, "abc", false))

	time.Sleep(40 * time.Millisecond)

	released := time.Now()

	r.q.release()

	for _, m := range r.q.got[1:] {
		if m.Time.Before(before) || m.Time.After(released.Add(-30*time.Millisecond)) {
			t.Errorf("%q is stamped %v, want when it arrived (between %v and %v)", m.Title, m.Time, before, released)
		}
	}

	if len(r.q.got) != 3 {
		t.Fatalf("queued %v", r.q.titles())
	}
}

func TestAPrintsEndDoesNotWaitForTheStartsOwnCameraPicture(t *testing.T) {
	r := newFollower(t)
	r.q.hold = true
	useRaw(t, `,"image_started":"camera","body_finished":"Used {energy}"`+picturesOn)

	totals := []float64{1, 1.25}

	r.f.energy = func(context.Context, string) (powerReading, bool) {
		reading := powerReading{kWh: totals[0], plugs: 1}
		totals = totals[1:]

		return reading, true
	}

	hold := make(chan struct{})

	r.f.snapshot = func(ctx context.Context, _ Settings, _ string) ([]byte, string, error) {
		select {
		case <-hold:
		case <-ctx.Done():
		}

		return []byte("S"), "image/jpeg", nil
	}

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))

	startDone := make(chan struct{})

	go func() { r.q.held[0](context.Background()); close(startDone) }()

	// The start is stuck waiting for the camera. The end only needs its power
	// baseline, which is taken by now, and must not wait for the picture.
	time.Sleep(30 * time.Millisecond)

	got := make(chan Message, 1)

	go func() { m, _ := r.q.held[1](context.Background()); got <- m }()

	select {
	case m := <-got:
		if m.Body != "Used 0.25 kWh" {
			t.Errorf("body %q", m.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("the end waited for the start's camera picture")
	}

	close(hold)
	<-startDone
}

func TestAnEndThatGivesUpWaitingDoesNotRaceTheStartStillWorking(t *testing.T) {
	r := newFollower(t)
	r.q.hold = true

	old := readyLimit
	readyLimit = 20 * time.Millisecond

	t.Cleanup(func() { readyLimit = old })

	release := make(chan struct{})

	calls := 0
	r.f.energy = func(context.Context, string) (powerReading, bool) {
		calls++
		if calls == 1 {
			<-release
		}

		return powerReading{kWh: 1, plugs: 1}, true
	}

	r.feed(start(t, "j1", "abc"), settle(t, "j1", "abc", event.JobFinished))

	done := make(chan struct{})

	go func() { r.q.held[0](context.Background()); close(done) }()

	time.Sleep(10 * time.Millisecond)

	// The end gives up waiting after readyLimit and reads what there is, while
	// the start is still in its power call and will write the baseline later.
	// It runs in its own goroutine and is not waited for before the start is let
	// go, so nothing orders the read before the write but the lock.
	ended := make(chan struct{})

	go func() { r.q.held[1](context.Background()); close(ended) }()

	time.Sleep(100 * time.Millisecond)

	close(release)
	<-done
	<-ended
}
