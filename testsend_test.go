package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 21, 15, 30, 0, 0, time.UTC)

func kitWith(printers ...testPrinter) testKit {
	return testKit{
		printers: func(context.Context) ([]testPrinter, error) { return printers, nil },
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

	if m.Image != nil || report.Picture != imageNone || report.Problem != "Prusa Connect answered 401" {
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
