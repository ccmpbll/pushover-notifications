// The message a per-type test button sends: one type's own rule (title, text,
// priority, sound and picture) filled in with sample values, so a person can
// see what that notification looks like on their phone before a real print
// sends it.
package main

import (
	"context"
	"maps"
	"slices"
	"sort"
	"time"
)

// testPrinter is a printer the host knows.
type testPrinter struct {
	ID     string
	Name   string
	Serial string
}

// testPrint is the print a printer last had, from the host's record.
type testPrint struct {
	Display string
	Path    string
}

// testKit is what a test reaches outside this module: the printers, a
// printer's last print, and the two kinds of picture. Any of them may be nil,
// and the test then goes without it.
type testKit struct {
	printers  func(ctx context.Context) ([]testPrinter, error)
	lastPrint func(ctx context.Context, id string) (testPrint, bool)
	thumbnail func(ctx context.Context, s Settings, deviceID, filePath string) ([]byte, string, error)
	snapshot  func(ctx context.Context, s Settings, camera string) ([]byte, string, error)
}

// testReport is what the test did, for the person who pressed the button.
type testReport struct {
	// Printer is the printer the sample was written for.
	Printer string `json:"printer"`
	// Picture is the picture that went with the message: none, thumbnail or camera.
	Picture string `json:"picture"`
	// Problem is why a picture the rule asks for did not go with it.
	Problem string `json:"picture_problem,omitempty"`
}

// sampleFile is the file name a test uses when no printer has a print on record.
const (
	sampleFile    = "test-print.bgcode"
	samplePrinter = "Test printer"
)

// compose builds the message of one kind. The type is sent even when it is
// switched off, because the point is to see it. s is not changed.
func (k testKit) compose(ctx context.Context, s Settings, kind string, now time.Time) (Message, testReport) {
	rule := s.Rules[kind]
	rule.On = true
	s.Rules = maps.Clone(s.Rules)
	s.Rules[kind] = rule

	var printers []testPrinter
	if k.printers != nil {
		printers, _ = k.printers(ctx)
	}

	chosen, image, imageType, problem := k.pick(ctx, s, rule.Image, printers)

	name, file := samplePrinter, sampleFile
	if chosen != nil {
		name = printerName(chosen.Name)

		if k.lastPrint != nil {
			if last, ok := k.lastPrint(ctx, chosen.ID); ok && fileOf(last.Display, last.Path) != "" {
				file = fileOf(last.Display, last.Path)
			}
		}
	}

	m, _ := build(s, kind, facts{vals: sampleValues(s, kind, name, file, now), at: now})
	m.Title = clip("Test: "+m.Title, maxTitle)

	report := testReport{Printer: name, Picture: imageNone, Problem: problem}

	if image != nil {
		m.Image, m.ImageType = image, imageType
		report.Picture = rule.Image
	}

	return m, report
}

// pick chooses the printer a test is written for, and gets the picture the
// rule asks for. A picture that cannot be got is a problem to report and not a
// reason to send nothing.
func (k testKit) pick(ctx context.Context, s Settings, mode string, printers []testPrinter) (chosen *testPrinter, image []byte, mediaType, problem string) {
	first := func() *testPrinter {
		if len(printers) == 0 {
			return nil
		}

		return &printers[0]
	}

	switch mode {
	case imageCamera:
		serials := make([]string, 0, len(s.Cameras))
		for serial, camera := range s.Cameras {
			if camera != "" {
				serials = append(serials, serial)
			}
		}

		sort.Strings(serials)

		if len(serials) == 0 {
			return first(), nil, "", "no printer has a camera chosen, in the Printers section"
		}

		serial := serials[0]
		chosen = &testPrinter{Name: serial, Serial: serial}

		if i := slices.IndexFunc(printers, func(p testPrinter) bool { return p.Serial == serial }); i >= 0 {
			chosen = &printers[i]
		}

		if k.snapshot == nil {
			return chosen, nil, "", "pictures are not available"
		}

		data, mt, err := k.snapshot(ctx, s, s.Cameras[serial])
		if err != nil {
			return chosen, nil, "", err.Error()
		}

		return chosen, data, mt, ""
	case imageThumbnail:
		if k.thumbnail == nil || k.lastPrint == nil {
			return first(), nil, "", "pictures are not available"
		}

		for i := range printers {
			last, ok := k.lastPrint(ctx, printers[i].ID)
			if !ok || last.Path == "" {
				continue
			}

			data, mt, err := k.thumbnail(ctx, s, printers[i].ID, last.Path)
			if err != nil {
				return &printers[i], nil, "", err.Error()
			}

			return &printers[i], data, mt, ""
		}

		return first(), nil, "", "no printer has a print file to take a thumbnail from"
	default:
		return first(), nil, "", ""
	}
}

// sampleValues is a value for every tag a message type may use except the
// power ones. Those come from a real print, so a line that uses them is left
// out of a test, as it is of a print with no plug.
func sampleValues(s Settings, kind, printer, file string, now time.Time) map[string]string {
	loc := s.location()

	vals := map[string]string{
		"printer":      printer,
		"file":         file,
		"started":      clock(now.Add(-83*time.Minute), loc),
		"ended":        clock(now, loc),
		"progress":     percent(42),
		"time_printed": duration(83*60 + 0),
		"filament":     filament(12300),
		"dialog_title": "Filament runout",
		"dialog_text":  "Load filament, then press Continue.",
	}

	switch kind {
	case "finished":
		vals["outcome"] = "finished"
	case "cancelled":
		vals["outcome"] = "cancelled"
	default:
		vals["outcome"] = "failed"
	}

	return vals
}
