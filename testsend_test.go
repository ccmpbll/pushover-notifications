package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
)

var testNow = time.Date(2026, 11, 21, 15, 30, 0, 0, time.UTC)

func kitWith(printers ...testPrinter) testKit {
	return testKit{
		printers: func(context.Context, int) ([]testPrinter, error) { return printers, nil },
		lastPrint: func(_ context.Context, id string) (testPrint, bool) {
			if id == "d1" {
				return testPrint{Display: "benchy.bgcode", Path: "/usb/benchy.bgcode"}, true
			}

			return testPrint{}, false
		},
		thumbnail: func(_ context.Context, _ Settings, id, path string) ([]byte, string, error) {
			return []byte("thumb:" + id + ":" + path), "image/png", nil
		},
		snapshot: func(_ context.Context, _ Settings, camera string) ([]byte, string, error) {
			return []byte("snap:" + camera), "image/jpeg", nil
		},
	}
}

func withRule(t *testing.T, kind string, edit func(*Rule)) Settings {
	t.Helper()

	s := defaults(t)
	rule := s.Rules[kind]
	edit(&rule)
	s.Rules[kind] = rule

	return s
}

func TestATestOfAKindUsesItsRuleAndSaysItIsATest(t *testing.T) {
	s := withRule(t, "finished", func(r *Rule) { r.Priority = 1; r.Sound = "siren" })

	m, report := kitWith(testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"}).compose(context.Background(), s, "finished", testNow)

	if !strings.HasPrefix(m.Title, "Test: ") || !strings.Contains(m.Title, "MK4S") {
		t.Fatalf("title %q", m.Title)
	}

	if m.Priority != 1 || m.Sound != "siren" {
		t.Fatalf("priority %d sound %q", m.Priority, m.Sound)
	}

	// The default finished text has lines that need real values: the sample
	// ones are there, and the power line, which has none, is left out.
	if !strings.Contains(m.Body, "benchy.bgcode") || !strings.Contains(m.Body, "Printed for 1h 23m") || strings.Contains(m.Body, "Used") {
		t.Fatalf("body %q", m.Body)
	}

	if report.Printer != "MK4S" || report.Picture != imageNone {
		t.Fatalf("report %+v", report)
	}
}

func TestATestIsSentEvenWhenItsKindIsSwitchedOff(t *testing.T) {
	s := withRule(t, "cancelled", func(r *Rule) { r.On = false })

	m, _ := kitWith().compose(context.Background(), s, "cancelled", testNow)
	if m.Title == "" || m.Body == "" {
		t.Fatalf("%+v", m)
	}
}

func TestATestKnowsNoPrinterAndStillSaysSomethingSensible(t *testing.T) {
	m, report := kitWith().compose(context.Background(), defaults(t), "started", testNow)

	if !strings.Contains(m.Title, "Test printer") || !strings.Contains(m.Body, "test-print.bgcode") {
		t.Fatalf("%+v", m)
	}

	if report.Printer != "Test printer" {
		t.Fatalf("%+v", report)
	}
}

func TestEveryKindHasSampleValuesForEveryTagItMayUse(t *testing.T) {
	for _, kind := range kinds {
		s := withRule(t, kind, func(r *Rule) {
			var all []string

			for _, tag := range tagsFor[kind] {
				if tag == "energy" || tag == "cost" {
					continue
				}

				all = append(all, "{"+tag+"}")
			}

			r.Title = "T " + strings.Join(all, " ")
			r.Body = strings.Join(all, "\n")
		})

		m, _ := kitWith().compose(context.Background(), s, kind, testNow)

		if strings.Contains(m.Title+m.Body, "{") {
			t.Errorf("%s: a tag is left over: %q %q", kind, m.Title, m.Body)
		}

		// A line with a tag that has no value is left out, so every line must be there.
		if got := strings.Count(m.Body, "\n") + 1; got != len(tagsFor[kind])-countPower(tagsFor[kind]) {
			t.Errorf("%s: %d lines for %v: %q", kind, got, tagsFor[kind], m.Body)
		}
	}
}

func countPower(tags []string) int {
	n := 0

	for _, tag := range tags {
		if tag == "energy" || tag == "cost" {
			n++
		}
	}

	return n
}

func TestACameraTestAttachesThePictureOfAPrintersCamera(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"S2": "cam-two", "S1": "cam-one"}

	m, report := kitWith(
		testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"},
		testPrinter{ID: "d2", Name: "Core One", Serial: "S2"},
	).compose(context.Background(), s, "started", testNow)

	// The first serial in order, so the choice does not change between runs.
	if string(m.Image) != "snap:cam-one" || m.ImageType != "image/jpeg" {
		t.Fatalf("image %q %q", m.Image, m.ImageType)
	}

	if report.Picture != imageCamera || report.Printer != "MK4S" || !strings.Contains(m.Title, "MK4S") {
		t.Fatalf("%+v %q", report, m.Title)
	}
}

func TestACameraTestWithNoCameraChosenSaysSoAndSendsTheText(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })

	m, report := kitWith(testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"}).compose(context.Background(), s, "started", testNow)

	if m.Image != nil || report.Picture != imageNone || !strings.Contains(report.Problem, "camera") {
		t.Fatalf("%+v image %v", report, m.Image)
	}

	if m.Title == "" {
		t.Fatal("no text was sent")
	}
}

func TestACameraThatDoesNotAnswerIsReportedWithItsReason(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"S1": "cam-one"}

	kit := kitWith(testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"})
	kit.snapshot = func(context.Context, Settings, string) ([]byte, string, error) {
		return nil, "", errors.New("Prusa Connect answered 401")
	}

	m, report := kit.compose(context.Background(), s, "started", testNow)

	if m.Image != nil || report.Picture != imageNone || report.Problem != "the camera cam-one of MK4S: Prusa Connect answered 401" {
		t.Fatalf("%+v", report)
	}
}

func TestAThumbnailTestUsesThePrintFileOfAPrinterThatHasOne(t *testing.T) {
	s := withRule(t, "finished", func(r *Rule) { r.Image = imageThumbnail })

	// The first printer has no print on record, the second one does.
	m, report := kitWith(
		testPrinter{ID: "d0", Name: "Idle", Serial: "S0"},
		testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"},
	).compose(context.Background(), s, "finished", testNow)

	if string(m.Image) != "thumb:d1:/usb/benchy.bgcode" || m.ImageType != "image/png" {
		t.Fatalf("image %q", m.Image)
	}

	if report.Picture != imageThumbnail || report.Printer != "MK4S" {
		t.Fatalf("%+v", report)
	}
}

func TestAThumbnailTestWithNoPrintFileSaysSo(t *testing.T) {
	s := withRule(t, "finished", func(r *Rule) { r.Image = imageThumbnail })

	m, report := kitWith(testPrinter{ID: "d0", Name: "Idle", Serial: "S0"}).compose(context.Background(), s, "finished", testNow)

	if m.Image != nil || !strings.Contains(report.Problem, "print") {
		t.Fatalf("%+v", report)
	}
}

func TestATestWithoutPictureSupportStillSends(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"S1": "cam-one"}

	m, report := (testKit{}).compose(context.Background(), s, "started", testNow)

	if m.Title == "" || m.Image != nil || report.Problem == "" {
		t.Fatalf("%+v", report)
	}
}

func TestTheTestDoesNotChangeTheSettingsItWasGiven(t *testing.T) {
	s := withRule(t, "cancelled", func(r *Rule) { r.On = false })

	kitWith().compose(context.Background(), s, "cancelled", testNow)

	if s.Rules["cancelled"].On {
		t.Fatal("the rule was switched on in the caller's settings")
	}
}

func TestAThumbnailTestSkipsAPrinterWhosePrintHasNoFilePath(t *testing.T) {
	s := withRule(t, "finished", func(r *Rule) { r.Image = imageThumbnail })

	kit := kitWith(
		testPrinter{ID: "d0", Name: "NoPath", Serial: "S0"},
		testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"},
	)
	inner := kit.lastPrint
	kit.lastPrint = func(ctx context.Context, id string) (testPrint, bool) {
		if id == "d0" {
			return testPrint{Display: "something"}, true
		}

		return inner(ctx, id)
	}

	m, report := kit.compose(context.Background(), s, "finished", testNow)
	if report.Printer != "MK4S" || string(m.Image) != "thumb:d1:/usb/benchy.bgcode" {
		t.Fatalf("%+v %q", report, m.Image)
	}
}

func TestThePrintATestUsesIsTheOneWithAFilePath(t *testing.T) {
	current := &deviceapi.Job{DisplayName: "running"}
	last := &deviceapi.Job{DisplayName: "done", Path: "/usb/done.bgcode"}

	// The current print has a name and no path, the last one has both: the path
	// wins, so a thumbnail can be had.
	got, ok := lastPrintOf(deviceapi.Jobs{Current: current, Last: last})
	if !ok || got.Path != "/usb/done.bgcode" || got.Display != "done" {
		t.Fatalf("%+v %v", got, ok)
	}

	// With no path anywhere the name is still the sample's file name.
	got, ok = lastPrintOf(deviceapi.Jobs{Current: current, Last: &deviceapi.Job{}})
	if !ok || got.Display != "running" || got.Path != "" {
		t.Fatalf("%+v %v", got, ok)
	}

	if _, ok = lastPrintOf(deviceapi.Jobs{}); ok {
		t.Fatal("a printer with no print has one")
	}

	// The current print is preferred when it has a path of its own.
	got, _ = lastPrintOf(deviceapi.Jobs{
		Current: &deviceapi.Job{Path: "/usb/now.bgcode"},
		Last:    last,
	})
	if got.Path != "/usb/now.bgcode" {
		t.Fatalf("%+v", got)
	}
}

func TestAPrintersPrintIsReadOnceForAThumbnailTest(t *testing.T) {
	s := withRule(t, "finished", func(r *Rule) { r.Image = imageThumbnail })

	kit := kitWith(testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"})
	inner := kit.lastPrint
	calls := 0
	kit.lastPrint = func(ctx context.Context, id string) (testPrint, bool) {
		calls++

		return inner(ctx, id)
	}

	kit.compose(context.Background(), s, "finished", testNow)

	if calls != 1 {
		t.Fatalf("the print was read %d times", calls)
	}
}

func TestACameraThatIsDownDoesNotHideOneThatWorks(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"S1": "cam-one", "S2": "cam-two"}

	kit := kitWith(
		testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"},
		testPrinter{ID: "d2", Name: "Core One", Serial: "S2"},
	)
	kit.snapshot = func(_ context.Context, _ Settings, camera string) ([]byte, string, error) {
		if camera == "cam-one" {
			return nil, "", errors.New("Prusa Connect answered HTTP 502")
		}

		return []byte("snap:" + camera), "image/jpeg", nil
	}

	m, report := kit.compose(context.Background(), s, "started", testNow)

	// The second camera's picture goes, with its printer's name, and the first
	// camera's failure is still said.
	if string(m.Image) != "snap:cam-two" || report.Printer != "Core One" || report.Picture != imageCamera {
		t.Fatalf("%+v image %q", report, m.Image)
	}

	if !strings.Contains(report.Problem, "cam-one") || !strings.Contains(report.Problem, "MK4S") || !strings.Contains(report.Problem, "502") {
		t.Fatalf("the failed camera is not named: %q", report.Problem)
	}
}

func TestWhenEveryCameraIsDownEachIsNamed(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"S1": "cam-one", "S2": "cam-two"}

	kit := kitWith(
		testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"},
		testPrinter{ID: "d2", Name: "Core One", Serial: "S2"},
	)
	kit.snapshot = func(context.Context, Settings, string) ([]byte, string, error) {
		return nil, "", errors.New("down")
	}

	m, report := kit.compose(context.Background(), s, "started", testNow)

	if m.Image != nil || report.Picture != imageNone {
		t.Fatalf("%+v", report)
	}

	for _, want := range []string{"cam-one of MK4S", "cam-two of Core One"} {
		if !strings.Contains(report.Problem, want) {
			t.Errorf("%q is not in %q", want, report.Problem)
		}
	}
}

func TestTheCameraOfAPrinterThatIsOffIsNotTriedWhileAnotherAnswers(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"S1": "cam-one", "S2": "cam-two"}

	kit := kitWith(
		testPrinter{ID: "d1", Name: "MK4S", Serial: "S1", Away: true},
		testPrinter{ID: "d2", Name: "Core One", Serial: "S2"},
	)

	var asked []string

	inner := kit.snapshot
	kit.snapshot = func(ctx context.Context, st Settings, camera string) ([]byte, string, error) {
		asked = append(asked, camera)

		return inner(ctx, st, camera)
	}

	m, report := kit.compose(context.Background(), s, "started", testNow)

	// S1 sorts first, but its printer is off, so the Core One's camera is asked
	// first and answers, and the off one is not asked and not mentioned.
	if string(m.Image) != "snap:cam-two" || report.Problem != "" || len(asked) != 1 {
		t.Fatalf("%+v asked %v", report, asked)
	}
}

func TestAnOffPrintersCameraIsTriedLastAndSaysThePrinterIsOff(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"S1": "cam-one"}

	kit := kitWith(testPrinter{ID: "d1", Name: "MK4S", Serial: "S1", Away: true})
	kit.snapshot = func(context.Context, Settings, string) ([]byte, string, error) {
		return nil, "", errors.New("502")
	}

	_, report := kit.compose(context.Background(), s, "started", testNow)

	if !strings.Contains(report.Problem, "MK4S (the printer is off)") {
		t.Fatalf("%q", report.Problem)
	}
}

func TestACameraForAPrinterTheHostDoesNotListIsTriedLastAndDoesNotBreakTheTest(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"GONE": "cam-gone", "S2": "cam-two"}

	m, report := kitWith(testPrinter{ID: "d2", Name: "Core One", Serial: "S2"}).compose(context.Background(), s, "started", testNow)

	// GONE sorts first, but it is not a printer the host has, so the listed
	// printer's camera comes first and answers.
	if string(m.Image) != "snap:cam-two" || report.Printer != "Core One" {
		t.Fatalf("%+v", report)
	}
}

func TestAnUnlistedPrinterIsNotCalledOffWhenTheListIsMissing(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{"S1": "cam-one"}

	kit := testKit{
		printers: func(context.Context, int) ([]testPrinter, error) { return nil, errors.New("no list") },
		snapshot: func(context.Context, Settings, string) ([]byte, string, error) { return nil, "", errors.New("502") },
	}

	_, report := kit.compose(context.Background(), s, "started", testNow)

	if strings.Contains(report.Problem, "off") || !strings.Contains(report.Problem, "cam-one") {
		t.Fatalf("%q", report.Problem)
	}
}

func TestTheCamerasStopWhenThePictureBudgetIsSpentAndTheSendStillHasItsOwnTime(t *testing.T) {
	s := withRule(t, "started", func(r *Rule) { r.Image = imageCamera })
	s.Cameras = map[string]string{}

	for i := range 6 {
		s.Cameras[fmt.Sprintf("S%d", i)] = fmt.Sprintf("cam-%d", i)
	}

	asked := 0
	kit := testKit{
		budget: 50 * time.Millisecond,
		snapshot: func(ctx context.Context, _ Settings, _ string) ([]byte, string, error) {
			asked++
			<-ctx.Done()

			return nil, "", ctx.Err()
		},
	}

	started := time.Now()
	_, report := kit.compose(context.Background(), s, "started", testNow)

	// The first camera waits out the budget, and the rest are not tried.
	if asked != 1 || time.Since(started) > 2*time.Second {
		t.Fatalf("asked %d in %v", asked, time.Since(started))
	}

	if !strings.Contains(report.Problem, "ran out of time") {
		t.Fatalf("%q", report.Problem)
	}
}

func TestCollectPrintersReadsEveryPage(t *testing.T) {
	all := make([]testPrinter, 450)
	for i := range all {
		all[i] = testPrinter{ID: fmt.Sprintf("d%d", i)}
	}

	pages := 0
	got, err := collectPrinters(context.Background(), 0, func(_ context.Context, offset, limit int) ([]testPrinter, int, error) {
		pages++

		return all[offset:min(offset+limit, len(all))], len(all), nil
	})

	if err != nil || len(got) != 450 || pages != 3 {
		t.Fatalf("%d printers in %d pages: %v", len(got), pages, err)
	}

	// A host that never stops answering is not followed for ever.
	_, _ = collectPrinters(context.Background(), 0, func(context.Context, int, int) ([]testPrinter, int, error) {
		pages++

		return []testPrinter{{ID: "x"}}, 1 << 30, nil
	})

	if pages > 3+collectPages {
		t.Fatalf("%d pages", pages)
	}

	// A page that fails after the first keeps what was read.
	calls := 0
	got, err = collectPrinters(context.Background(), 0, func(context.Context, int, int) ([]testPrinter, int, error) {
		calls++
		if calls == 2 {
			return nil, 0, errors.New("late")
		}

		return []testPrinter{{ID: "a"}, {ID: "b"}}, 10, nil
	})

	if err == nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestCollectPrintersStopsAtAnEmptyPageWhateverTheTotalSays(t *testing.T) {
	calls := 0
	got, err := collectPrinters(context.Background(), 0, func(context.Context, int, int) ([]testPrinter, int, error) {
		calls++

		return nil, 10, nil
	})

	if err != nil || len(got) != 0 || calls != 1 {
		t.Fatalf("%d calls: %v %v", calls, got, err)
	}
}

func TestCollectPrintersReadsOnlyAsManyAsAreWanted(t *testing.T) {
	calls, asked := 0, 0

	got, err := collectPrinters(context.Background(), 1, func(_ context.Context, _, limit int) ([]testPrinter, int, error) {
		calls++
		asked = limit

		return []testPrinter{{ID: "a"}}, 450, nil
	})

	if err != nil || len(got) != 1 || calls != 1 || asked != 1 {
		t.Fatalf("%d printers, %d calls, limit %d: %v", len(got), calls, asked, err)
	}
}

func TestARowWithNoPictureReadsOnePrinterAndAPictureRowReadsThemAll(t *testing.T) {
	var wanted []int

	kit := kitWith(testPrinter{ID: "d1", Name: "MK4S", Serial: "S1"})
	inner := kit.printers
	kit.printers = func(ctx context.Context, most int) ([]testPrinter, error) {
		wanted = append(wanted, most)

		return inner(ctx, most)
	}

	kit.compose(context.Background(), defaults(t), "started", testNow)

	s := withRule(t, "finished", func(r *Rule) { r.Image = imageThumbnail })
	kit.compose(context.Background(), s, "finished", testNow)

	if len(wanted) != 2 || wanted[0] != 1 || wanted[1] != 0 {
		t.Fatalf("%v", wanted)
	}
}

func TestAThumbnailTestStopsWhenThePictureBudgetIsSpent(t *testing.T) {
	s := withRule(t, "finished", func(r *Rule) { r.Image = imageThumbnail })

	printers := make([]testPrinter, 6)
	for i := range printers {
		printers[i] = testPrinter{ID: fmt.Sprintf("d%d", i), Name: fmt.Sprintf("P%d", i)}
	}

	reads := 0
	kit := kitWith(printers...)
	kit.budget = 50 * time.Millisecond
	kit.lastPrint = func(ctx context.Context, _ string) (testPrint, bool) {
		reads++

		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}

		return testPrint{}, false
	}

	_, report := kit.compose(context.Background(), s, "finished", testNow)

	// The first read waits out the budget, the rest are not made, and the
	// reason is the time and not a missing print file.
	if reads > 2 || !strings.Contains(report.Problem, "ran out of time") || strings.Contains(report.Problem, "no printer has a print file") {
		t.Fatalf("%d reads: %q", reads, report.Problem)
	}
}
