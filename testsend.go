// The message a per-type test button sends: one type's own rule (title, text,
// priority, sound and picture) filled in with sample values, so a person can
// see what that notification looks like on their phone before a real print
// sends it.
package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
)

// testPrinter is a printer the host knows.
type testPrinter struct {
	ID     string
	Name   string
	Serial string
	// Away is a printer the host has seen and that is off or out of reach, so
	// its camera, which the printer powers, is not expected to answer.
	Away bool
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
	// budget is how long the pictures of one test may take in all. Zero is
	// pictureBudget. It is for the fetching only: the send has its own time.
	budget time.Duration

	// printers reads the printers the host knows, at most most of them, or every
	// one when most is zero.
	printers  func(ctx context.Context, most int) ([]testPrinter, error)
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

// pictureBudget is the time a test spends getting pictures, however many
// cameras it tries. Past it the rest are not tried, so a fleet of cameras that
// are all down cannot use up the time the message itself needs.
const pictureBudget = 20 * time.Second

// collectPages is the most pages of printers a test reads, so a host that never
// stops answering is not followed for ever.
const collectPages = 20

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

	// Only a picture needs to look through the printers. A row with none needs
	// one, for the name in the sample text.
	most := 1
	if rule.Image != imageNone {
		most = 0
	}

	var printers []testPrinter
	if k.printers != nil {
		printers, _ = k.printers(ctx, most)
	}

	budget := k.budget
	if budget <= 0 {
		budget = pictureBudget
	}

	pictures, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	got := k.pick(pictures, s, rule.Image, printers)

	name, file := samplePrinter, sampleFile
	if got.printer != nil {
		name = printerName(got.printer.Name)

		// A thumbnail test has read the printer's print already.
		if !got.hasPrint && k.lastPrint != nil {
			got.print, got.hasPrint = k.lastPrint(ctx, got.printer.ID)
		}

		if got.hasPrint && fileOf(got.print.Display, got.print.Path) != "" {
			file = fileOf(got.print.Display, got.print.Path)
		}
	}

	m, _ := build(s, kind, facts{vals: sampleValues(s, kind, name, file, now), at: now})
	m.Title = clip("Test: "+m.Title, maxTitle)

	report := testReport{Printer: name, Picture: imageNone, Problem: got.problem}

	if got.image != nil {
		m.Image, m.ImageType = got.image, got.mediaType
		report.Picture = rule.Image
	}

	return m, report
}

// picked is the printer a test is written for and the picture the rule asked
// for. A picture that cannot be got is a problem to report and not a reason to
// send nothing. print is the printer's print when the choice had to read it.
type picked struct {
	printer   *testPrinter
	print     testPrint
	hasPrint  bool
	image     []byte
	mediaType string
	problem   string
}

func (k testKit) pick(ctx context.Context, s Settings, mode string, printers []testPrinter) picked {
	var first *testPrinter
	if len(printers) > 0 {
		first = &printers[0]
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

		// The cameras of printers that are on come first: a printer that is off
		// has a camera that is off, and trying it first only costs the wait.
		// A printer the host did not list is unknown, not off: it goes last, and
		// is not said to be off.
		listed := func(serial string) int {
			return slices.IndexFunc(printers, func(p testPrinter) bool { return p.Serial == serial })
		}
		away := func(serial string) bool {
			i := listed(serial)

			return i < 0 || printers[i].Away
		}

		sort.SliceStable(serials, func(a, b int) bool { return !away(serials[a]) && away(serials[b]) })

		if len(serials) == 0 {
			return picked{printer: first, problem: "no printer has a camera chosen, in the Printers section"}
		}

		if k.snapshot == nil {
			return picked{printer: first, problem: "pictures are not available"}
		}

		// Every camera is tried in serial order. The first that answers goes with
		// the message, and each that did not is named with its printer, so a
		// camera that is down is not hidden by one that works.
		var failed []string

		for _, serial := range serials {
			if ctx.Err() != nil {
				failed = append(failed, "the pictures ran out of time before every camera was tried")

				break
			}

			printer := &testPrinter{Name: serial, Serial: serial}

			if i := listed(serial); i >= 0 {
				printer = &printers[i]
			}

			data, mt, err := k.snapshot(ctx, s, s.Cameras[serial])
			if err != nil {
				note := ""
				if i := listed(serial); i >= 0 && printers[i].Away {
					note = " (the printer is off)"
				}

				failed = append(failed, fmt.Sprintf("the camera %s of %s%s: %s", s.Cameras[serial], printerName(printer.Name), note, err))

				continue
			}

			return picked{printer: printer, image: data, mediaType: mt, problem: strings.Join(failed, "; ")}
		}

		return picked{printer: first, problem: strings.Join(failed, "; ")}
	case imageThumbnail:
		if k.thumbnail == nil || k.lastPrint == nil {
			return picked{printer: first, problem: "pictures are not available"}
		}

		for i := range printers {
			if ctx.Err() != nil {
				return picked{printer: first, problem: "the pictures ran out of time before a print file was found"}
			}

			last, ok := k.lastPrint(ctx, printers[i].ID)
			if !ok || last.Path == "" {
				continue
			}

			data, mt, err := k.thumbnail(ctx, s, printers[i].ID, last.Path)
			if err != nil {
				return picked{printer: &printers[i], print: last, hasPrint: true, problem: err.Error()}
			}

			return picked{printer: &printers[i], print: last, hasPrint: true, image: data, mediaType: mt}
		}

		return picked{printer: first, problem: "no printer has a print file to take a thumbnail from"}
	default:
		return picked{printer: first}
	}
}

// lastPrintOf is the print of a printer a test can use: the one with a file
// path (the current print, else the last), and failing that one with only a
// name, which gives the sample its file name but no thumbnail.
func lastPrintOf(jobs deviceapi.Jobs) (testPrint, bool) {
	candidates := []*deviceapi.Job{jobs.Current, jobs.Last}

	for _, job := range candidates {
		if job != nil && job.Path != "" {
			return testPrint{Display: job.DisplayName, Path: job.Path}, true
		}
	}

	for _, job := range candidates {
		if job != nil && job.DisplayName != "" {
			return testPrint{Display: job.DisplayName}, true
		}
	}

	return testPrint{}, false
}

// collectPrinters reads every page of the printers the host knows. list reads
// one page from offset, at most limit long, and says how many printers there
// are in all. A page that fails keeps what was read before it. most stops the
// reading at that many printers, and zero reads every one.
func collectPrinters(ctx context.Context, most int, list func(ctx context.Context, offset, limit int) (items []testPrinter, total int, err error)) ([]testPrinter, error) {
	limit := deviceapi.MaxPageLimit
	if most > 0 {
		limit = min(limit, most)
	}

	var all []testPrinter

	for range collectPages {
		items, total, err := list(ctx, len(all), limit)
		if err != nil {
			return all, err
		}

		all = append(all, items...)

		if len(items) == 0 || len(all) >= total || (most > 0 && len(all) >= most) {
			break
		}
	}

	return all, nil
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
		"time_printed": duration(83 * 60),
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
