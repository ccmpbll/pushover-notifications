package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/event"
)

var base = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func settled(t *testing.T, s event.JobSettled) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

func started(t *testing.T, s event.JobStarted) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

func defaults(t *testing.T) Settings {
	t.Helper()

	s, err := parseSettings(t, `{"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`"}`)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestAStartedPrintNamesThePrinterAndTheFile(t *testing.T) {
	m, ok, err := compose(defaults(t), "Core One", event.TypeDeviceJobStarted,
		started(t, event.JobStarted{JobID: "j1", DisplayName: "benchy.bgcode", Path: "/usb/benchy.bgcode", Started: base}), nil)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}

	if m.Title != "Core One: print started" || m.Body != "benchy.bgcode" || m.Priority != -1 {
		t.Errorf("%+v", m)
	}

	if !m.Time.Equal(base) {
		t.Errorf("time %v, want the start", m.Time)
	}
}

func TestTheFileFallsBackToThePathAndIsLeftOutWhenThereIsNone(t *testing.T) {
	m, _, _ := compose(defaults(t), "MK4", event.TypeDeviceJobStarted, started(t, event.JobStarted{Path: "/usb/dir/part.bgcode"}), nil)
	if m.Body != "part.bgcode" {
		t.Errorf("body %q", m.Body)
	}

	// Nothing to say about the file: the body falls back to the title, because
	// Pushover refuses an empty message.
	m, _, _ = compose(defaults(t), "MK4", event.TypeDeviceJobStarted, started(t, event.JobStarted{}), nil)
	if m.Body != m.Title {
		t.Errorf("body %q, want the title %q", m.Body, m.Title)
	}
}

func TestAFinishedPrintSaysHowLongAndHowMuchFilament(t *testing.T) {
	m, ok, err := compose(defaults(t), "Core One", event.TypeDeviceJobSettled, settled(t, event.JobSettled{
		DisplayName: "benchy.bgcode", Started: base, Ended: base.Add(time.Hour),
		Outcome: event.JobFinished, Progress: 100,
		TimePrintingS: 4980, TimePrintingKnown: true, FilamentUsedMM: 4200, FilamentUsedKnown: true,
	}), nil)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}

	if m.Title != "Core One: print finished" {
		t.Errorf("title %q", m.Title)
	}

	if m.Body != "benchy.bgcode\nPrinted for 1h 23m\n4.2 m of filament" {
		t.Errorf("body %q", m.Body)
	}

	if !m.Time.Equal(base.Add(time.Hour)) {
		t.Errorf("time %v, want the end", m.Time)
	}
}

func TestThePowerAPrintUsedIsAddedWhenKnown(t *testing.T) {
	m, _, _ := compose(defaults(t), "Core One", event.TypeDeviceJobSettled, settled(t, event.JobSettled{
		DisplayName: "a.gcode", Outcome: event.JobFinished,
	}), map[string]string{"energy": "0.42 kWh", "cost": "$0.06"})

	if m.Body != "a.gcode\nUsed 0.42 kWh" {
		t.Errorf("body %q", m.Body)
	}
}

func TestUnknownTimeAndFilamentAreLeftOutNotWrittenAsZero(t *testing.T) {
	cases := []struct {
		data event.JobSettled
		want string
	}{
		{event.JobSettled{DisplayName: "a.gcode", Outcome: event.JobFinished}, "a.gcode"},
		{event.JobSettled{DisplayName: "a.gcode", Outcome: event.JobFinished, TimePrintingS: 90, TimePrintingKnown: true}, "a.gcode\nPrinted for 1m"},
		{event.JobSettled{DisplayName: "a.gcode", Outcome: event.JobFinished, FilamentUsedMM: 850, FilamentUsedKnown: true}, "a.gcode\n850 mm of filament"},
	}

	for _, c := range cases {
		m, _, _ := compose(defaults(t), "MK4", event.TypeDeviceJobSettled, settled(t, c.data), nil)
		if m.Body != c.want {
			t.Errorf("body %q, want %q", m.Body, c.want)
		}
	}
}

func TestAFailedPrintSaysWhereItStoppedAndUsesItsPriority(t *testing.T) {
	m, ok, err := compose(defaults(t), "Core One", event.TypeDeviceJobSettled, settled(t, event.JobSettled{
		DisplayName: "benchy.bgcode", Outcome: event.JobFailed, Progress: 63.4, MachineState: "ERROR",
	}), nil)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}

	if m.Title != "Core One: print failed" || m.Body != "benchy.bgcode\nStopped at 63%" || m.Priority != 1 {
		t.Errorf("%+v", m)
	}
}

func TestAnUnknownEndIsReportedAsAFailureThatSaysWhy(t *testing.T) {
	m, ok, err := compose(defaults(t), "Core One", event.TypeDeviceJobSettled, settled(t, event.JobSettled{
		DisplayName: "x.gcode", Outcome: event.JobUnknown, Progress: 10,
	}), nil)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}

	if m.Title != "Core One: print ended without a result" || m.Priority != 1 {
		t.Errorf("%+v", m)
	}

	if !strings.Contains(m.Body, "Stopped at 10%") {
		t.Errorf("body %q", m.Body)
	}
}

func TestACancelledPrintIsSilentByDefaultAndSentWhenAsked(t *testing.T) {
	data := settled(t, event.JobSettled{DisplayName: "x", Outcome: event.JobCancelled})

	if _, ok, err := compose(defaults(t), "MK4", event.TypeDeviceJobSettled, data, nil); err != nil || ok {
		t.Fatalf("ok=%v err=%v, want skipped", ok, err)
	}

	s, err := parseSettings(t, `{"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`","notify_cancelled":"true"}`)
	if err != nil {
		t.Fatal(err)
	}

	m, ok, err := compose(s, "MK4", event.TypeDeviceJobSettled, data, nil)
	if err != nil || !ok || m.Title != "MK4: print cancelled" {
		t.Fatalf("%+v ok=%v err=%v", m, ok, err)
	}
}

func TestAPrintThatStoppedAtTheStartDoesNotSayItStoppedAtZero(t *testing.T) {
	m, _, _ := compose(defaults(t), "MK4", event.TypeDeviceJobSettled, settled(t, event.JobSettled{
		DisplayName: "a.gcode", Outcome: event.JobFailed, Progress: 0.2,
	}), nil)

	if m.Body != "a.gcode" {
		t.Errorf("body %q", m.Body)
	}
}

func TestYourOwnTextIsUsed(t *testing.T) {
	s, err := parseSettings(t, `{"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`",
		"title_finished":"Done! {file}","body_finished":"Took {time_printed}\nEnded {ended}\nOn {printer}"}`)
	if err != nil {
		t.Fatal(err)
	}

	m, _, _ := compose(s, "Core One", event.TypeDeviceJobSettled, settled(t, event.JobSettled{
		DisplayName: "a.gcode", Outcome: event.JobFinished, TimePrintingS: 3600, TimePrintingKnown: true, Ended: base,
	}), nil)

	want := "Took 1h\nEnded " + base.Local().Format("15:04") + "\nOn Core One"
	if m.Title != "Done! a.gcode" || m.Body != want {
		t.Errorf("%q / %q, want %q", m.Title, m.Body, want)
	}
}

func TestATitleThatComesOutEmptyFallsBackToTheDefault(t *testing.T) {
	s, err := parseSettings(t, `{"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`","title_started":"{file}"}`)
	if err != nil {
		t.Fatal(err)
	}

	m, _, _ := compose(s, "Core One", event.TypeDeviceJobStarted, started(t, event.JobStarted{}), nil)
	if m.Title != "Core One: print started" {
		t.Errorf("title %q", m.Title)
	}
}

func TestAnUnnamedPrinterAndALongTextAreHandled(t *testing.T) {
	m, _, _ := compose(defaults(t), "", event.TypeDeviceJobStarted, started(t, event.JobStarted{DisplayName: "x"}), nil)
	if m.Title != "Printer: print started" {
		t.Errorf("title %q", m.Title)
	}

	long := strings.Repeat("é", 2000)

	m, _, _ = compose(defaults(t), long, event.TypeDeviceJobStarted, started(t, event.JobStarted{DisplayName: long}), nil)

	if n := len([]rune(m.Title)); n > 250 {
		t.Errorf("title is %d characters, Pushover takes 250", n)
	}

	if n := len([]rune(m.Body)); n > 1024 {
		t.Errorf("body is %d characters, Pushover takes 1024", n)
	}
}

func TestAnEventThatIsNotOursOrNotJSONIsNotSent(t *testing.T) {
	if _, ok, err := compose(defaults(t), "x", "com.prusa.connect.device.arrived", json.RawMessage(`{}`), nil); ok || err != nil {
		t.Errorf("ok=%v err=%v for a type this module does not follow", ok, err)
	}

	if _, ok, err := compose(defaults(t), "x", event.TypeDeviceJobStarted, json.RawMessage(`not json`), nil); ok || err == nil {
		t.Errorf("ok=%v err=%v for a body that is not JSON", ok, err)
	}
}

func TestADurationReadsLikeAPerson(t *testing.T) {
	cases := map[int64]string{0: "0s", 45: "45s", 60: "1m", 90: "1m", 3600: "1h", 3660: "1h 1m", 86400 + 3600: "25h"}

	for seconds, want := range cases {
		if got := duration(seconds); got != want {
			t.Errorf("%d s: %q, want %q", seconds, got, want)
		}
	}
}

func TestABodyThatComesOutEmptyFallsBackToTheDefaultBody(t *testing.T) {
	s, err := parseSettings(t, `{"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`","body_finished":"Took {time_printed}"}`)
	if err != nil {
		t.Fatal(err)
	}

	m, _, _ := compose(s, "Core One", event.TypeDeviceJobSettled, settled(t, event.JobSettled{DisplayName: "a.gcode", Outcome: event.JobFinished}), nil)
	if m.Body != "a.gcode" {
		t.Errorf("body %q", m.Body)
	}
}
