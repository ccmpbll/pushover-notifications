package main

import (
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/event"
)

// Message is one notification, ready to send.
type Message struct {
	Title    string
	Body     string
	Priority int
	Sound    string
	// Time is when the thing happened, so a message delayed by a retry still
	// shows the right time. Zero leaves it to Pushover.
	Time time.Time
	// Image is a picture to attach, and ImageType its media type. Nil is none.
	Image     []byte
	ImageType string
}

// facts are what a message about one event can say: the value of each tag, and
// when it happened.
type facts struct {
	vals map[string]string
	at   time.Time
}

// compose turns one job event into a message, for the printer it is about.
// extra are tags the event does not carry, such as the power a print used. ok
// is false when the settings say this kind is not worth a message, or the event
// is not one this module follows. An error is a body that cannot be read.
func compose(s Settings, printer, eventType string, data json.RawMessage, extra map[string]string) (m Message, ok bool, err error) {
	kind, f, err := jobFacts(printer, eventType, data, s.location())
	if err != nil || kind == "" {
		return Message{}, false, err
	}

	for tag, value := range extra {
		f.vals[tag] = value
	}

	m, ok = build(s, kind, f)

	return m, ok, nil
}

// jobFacts reads a started or settled event. kind is the message type it falls
// under, or "" for an event this module does not follow. A print that ended
// without saying how falls under "failed" and says so: a power cut looks like
// that.
func jobFacts(printer, eventType string, data json.RawMessage, loc *time.Location) (kind string, f facts, err error) {
	f.vals = map[string]string{"printer": printerName(printer)}

	switch eventType {
	case event.TypeDeviceJobStarted:
		var started event.JobStarted
		if err = json.Unmarshal(data, &started); err != nil {
			return "", facts{}, fmt.Errorf("the started event is unreadable: %w", err)
		}

		f.vals["file"] = fileOf(started.DisplayName, started.Path)
		f.vals["started"] = clock(started.Started, loc)
		f.at = started.Started

		return "started", f, nil
	case event.TypeDeviceJobSettled:
		var settled event.JobSettled
		if err = json.Unmarshal(data, &settled); err != nil {
			return "", facts{}, fmt.Errorf("the settled event is unreadable: %w", err)
		}

		f.vals["file"] = fileOf(settled.DisplayName, settled.Path)
		f.vals["started"] = clock(settled.Started, loc)
		f.vals["ended"] = clock(settled.Ended, loc)
		f.vals["progress"] = percent(settled.Progress)
		f.at = settled.Ended

		if settled.TimePrintingKnown {
			f.vals["time_printed"] = duration(settled.TimePrintingS)
		}

		if settled.FilamentUsedKnown {
			f.vals["filament"] = filament(settled.FilamentUsedMM)
		}

		switch settled.Outcome {
		case event.JobFinished:
			f.vals["outcome"] = "finished"

			return "finished", f, nil
		case event.JobCancelled:
			f.vals["outcome"] = "cancelled"

			return "cancelled", f, nil
		case event.JobFailed:
			f.vals["outcome"] = "failed"

			return "failed", f, nil
		default:
			f.vals["outcome"] = "ended without a result"

			return "failed", f, nil
		}
	default:
		return "", facts{}, nil
	}
}

// build writes the message for one kind from the rule's templates. ok is false
// when the kind is switched off. A title or a body that comes out empty falls
// back to the default text, then to the title, because Pushover refuses a
// message with nothing in it.
func build(s Settings, kind string, f facts) (Message, bool) {
	rule := s.Rules[kind]
	if !rule.On {
		return Message{}, false
	}

	fallback := defaultRules()[kind]

	title := render(rule.Title, f.vals)
	if title == "" {
		title = render(fallback.Title, f.vals)
	}

	if title == "" {
		title = printerName(f.vals["printer"])
	}

	body := render(rule.Body, f.vals)
	if body == "" {
		body = render(fallback.Body, f.vals)
	}

	if body == "" {
		body = title
	}

	return Message{
		Title:    clip(title, maxTitle),
		Body:     clip(body, maxBody),
		Priority: rule.Priority,
		Sound:    rule.Sound,
		Time:     f.at,
	}, true
}

// Pushover's limits, in characters.
const (
	maxTitle = 250
	maxBody  = 1024
)

func printerName(name string) string {
	if name == "" {
		return "Printer"
	}

	return name
}

// fileOf is the name a person knows the print by. Empty means there is none, so
// a line that names the file is left out.
func fileOf(display, filePath string) string {
	switch {
	case display != "":
		return display
	case filePath != "":
		return path.Base(filePath)
	default:
		return ""
	}
}

// percent is "63%", or empty for a print that has not got going.
func percent(progress float64) string {
	if progress < 0.5 {
		return ""
	}

	return fmt.Sprintf("%.0f%%", progress)
}

// clock is a time of day in the person's time zone. Empty for a zero time.
func clock(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}

	return t.In(loc).Format("15:04")
}

// duration is a length of time the way a person says it: "1h 23m". Seconds are
// shown only under a minute.
func duration(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", max(seconds, 0))
	}

	minutes := seconds / 60
	hours, rest := minutes/60, minutes%60

	switch {
	case hours == 0:
		return fmt.Sprintf("%dm", rest)
	case rest == 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dh %dm", hours, rest)
	}
}

func filament(mm float64) string {
	if mm >= 1000 {
		return fmt.Sprintf("%.1f m", mm/1000)
	}

	return fmt.Sprintf("%.0f mm", mm)
}

// clip cuts text to n characters, not bytes, so a multi-byte name is not split.
func clip(text string, n int) string {
	if runes := []rune(text); len(runes) > n {
		return string(runes[:n-1]) + "…"
	}

	return text
}
