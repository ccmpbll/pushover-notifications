package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/event"
)

const (
	goodToken = "azGDORePK8gMaC0QOYAMyEEuzJnyUi"
	goodUser  = "uQiRzpo4DXghDmr9QzzfQu27cmVRsG"
)

func parseSettings(t *testing.T, raw string) (Settings, error) {
	t.Helper()

	var s Settings

	err := json.Unmarshal([]byte(raw), &s)

	return s, err
}

func TestEmptySettingsAreValidAndSendNothing(t *testing.T) {
	s, err := parseSettings(t, `{}`)
	if err != nil {
		t.Fatal(err)
	}

	if err = s.validate(); err != nil {
		t.Fatalf("empty settings are refused: %v", err)
	}

	if s.configured() {
		t.Fatal("empty settings count as configured")
	}
}

func TestDefaultsAreStartedFinishedAndFailedOn(t *testing.T) {
	s, err := parseSettings(t, `{"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`"}`)
	if err != nil {
		t.Fatal(err)
	}

	if !s.configured() {
		t.Fatal("token and user given, not configured")
	}

	want := map[string]Rule{
		"started":   {On: true, Priority: -1},
		"finished":  {On: true, Priority: 0},
		"failed":    {On: true, Priority: 1},
		"cancelled": {On: false, Priority: 0},
		"attention": {On: true, Priority: 1},
		"lost":      {On: true, Priority: 1},
	}

	for kind, rule := range want {
		got := s.Rules[kind]
		if got.On != rule.On || got.Priority != rule.Priority {
			t.Errorf("%s: on %v priority %d, want on %v priority %d", kind, got.On, got.Priority, rule.On, rule.Priority)
		}

		if got.Title == "" || got.Body == "" || got.Image != "none" {
			t.Errorf("%s: no default text or picture: %+v", kind, got)
		}
	}
}

func TestEveryKeyIsRead(t *testing.T) {
	s, err := parseSettings(t, `{
		"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`",
		"notify_started":"false","notify_cancelled":"true",
		"priority_failed":"-2","sound_finished":"magic","sound_failed":"siren",
		"muted_printers":" AAA1 , BBB2 ,,"
	}`)
	if err != nil {
		t.Fatal(err)
	}

	if s.Rules["started"].On || !s.Rules["cancelled"].On {
		t.Error("notify flags were not read")
	}

	if s.Rules["failed"].Priority != -2 || s.Rules["finished"].Sound != "magic" || s.Rules["failed"].Sound != "siren" {
		t.Errorf("priority or sound not read: %+v", s.Rules)
	}

	if !s.muted("AAA1") || !s.muted("BBB2") || s.muted("") || s.muted("CCC3") {
		t.Errorf("muted printers wrong: %v", s.Muted)
	}
}

func TestSettingsThatAreRefused(t *testing.T) {
	cases := map[string]string{
		"unknown key":          `{"pushover_tokn":"x"}`,
		"not a flat map":       `{"pushover_token":5}`,
		"token without user":   `{"pushover_token":"` + goodToken + `"}`,
		"user without token":   `{"pushover_user":"` + goodUser + `"}`,
		"token with a space":   `{"pushover_token":"abc def","pushover_user":"` + goodUser + `"}`,
		"token too long":       `{"pushover_token":"` + strings.Repeat("a", 65) + `","pushover_user":"` + goodUser + `"}`,
		"notify not a bool":    `{"notify_started":"maybe"}`,
		"priority too high":    `{"priority_failed":"2"}`,
		"priority too low":     `{"priority_started":"-3"}`,
		"priority not a whole": `{"priority_failed":"1.5"}`,
		"sound with capitals":  `{"sound_failed":"Siren"}`,
		"sound too long":       `{"sound_failed":"` + strings.Repeat("a", 21) + `"}`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := parseSettings(t, raw)
			if err == nil {
				err = s.validate()
			}

			if err == nil {
				t.Fatalf("%s was accepted", raw)
			}
		})
	}
}

func TestAnErrorNeverQuotesTheToken(t *testing.T) {
	secret := "secret value with spaces"

	s, err := parseSettings(t, `{"pushover_token":"`+secret+`","pushover_user":"`+goodUser+`"}`)
	if err == nil {
		err = s.validate()
	}

	if err == nil {
		t.Fatal("a bad token was accepted")
	}

	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("the error quotes the token: %v", err)
	}
}

func TestTextAndPictureKeysAreRead(t *testing.T) {
	s, err := parseSettings(t, `{
		"title_finished":"Done: {file}","body_finished":"{file}\nTook {time_printed}",
		"image_finished":"camera","title_attention":"{printer}: {dialog_title}",
		"connect_url":"http://127.0.0.1:8080","connect_token":"abc"
	}`)
	if err != nil {
		t.Fatal(err)
	}

	got := s.Rules["finished"]
	if got.Title != "Done: {file}" || got.Body != "{file}\nTook {time_printed}" || got.Image != "camera" {
		t.Errorf("%+v", got)
	}

	if s.Rules["attention"].Title != "{printer}: {dialog_title}" || s.Rules["attention"].Body == "" {
		t.Errorf("a key not written lost its default: %+v", s.Rules["attention"])
	}

	if err = s.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTextAndPictureSettingsThatAreRefused(t *testing.T) {
	cases := map[string]string{
		"unknown tag in a title":   `{"title_started":"{printer}: {nope}"}`,
		"a tag of another type":    `{"title_started":"{printer} {time_printed}"}`,
		"a stray brace":            `{"body_started":"{file} {"}`,
		"a tag with capitals":      `{"body_started":"{File}"}`,
		"an empty title":           `{"title_started":"  "}`,
		"a title that is too long": `{"title_started":"` + strings.Repeat("a", 251) + `"}`,
		"a message too long":       `{"body_started":"` + strings.Repeat("a", 1025) + `"}`,
		"an unknown picture":       `{"image_started":"gif"}`,
		"a picture for a lost one": `{"image_lost":"camera","connect_url":"http://127.0.0.1:8080","connect_token":"abc"}`,
		"an unknown kind":          `{"title_paused":"x"}`,
		"an unknown field":         `{"colour_started":"x"}`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := parseSettings(t, raw)
			if err == nil {
				err = s.validate()
			}

			if err == nil {
				t.Fatalf("%s was accepted", raw)
			}
		})
	}
}

func TestACameraPerPrinterAndTheConnectAddress(t *testing.T) {
	s, err := parseSettings(t, `{
		"connect_url":"http://127.0.0.1:8080/","connect_token":"tok_abcDEF123",
		"camera1_printer":"SER-A","camera1_name":"bench-top",
		"camera3_printer":"SER-B","camera3_name":"mk4s cam",
		"image_finished":"thumbnail","image_failed":"camera"
	}`)
	if err != nil {
		t.Fatal(err)
	}

	if err = s.validate(); err != nil {
		t.Fatal(err)
	}

	if s.Cameras["SER-A"] != "bench-top" || s.Cameras["SER-B"] != "mk4s cam" || len(s.Cameras) != 2 {
		t.Errorf("%v", s.Cameras)
	}

	if s.ConnectURL != "http://127.0.0.1:8080" {
		t.Errorf("the address is %q, want it without the trailing slash", s.ConnectURL)
	}

	if s.Rules["finished"].Image != "thumbnail" || s.Rules["failed"].Image != "camera" {
		t.Errorf("%+v", s.Rules)
	}
}

func TestPicturesNeedTheConnectAddressAndToken(t *testing.T) {
	for _, raw := range []string{
		`{"image_finished":"thumbnail"}`,
		`{"image_finished":"camera","connect_url":"http://127.0.0.1:8080"}`,
		`{"image_finished":"camera","connect_token":"abc"}`,
	} {
		s, err := parseSettings(t, raw)
		if err == nil {
			err = s.validate()
		}

		if err == nil {
			t.Errorf("%s was accepted", raw)
		}
	}

	// No picture asked for, so no address is needed, and one may still be given.
	s, err := parseSettings(t, `{"connect_url":"http://127.0.0.1:8080","connect_token":"abc"}`)
	if err == nil {
		err = s.validate()
	}

	if err != nil {
		t.Errorf("an address with no picture asked for: %v", err)
	}
}

func TestCameraAndConnectSettingsThatAreRefused(t *testing.T) {
	cases := map[string]string{
		"a printer without a camera":   `{"camera1_printer":"SER-A"}`,
		"a camera without a printer":   `{"camera1_name":"bench-top"}`,
		"a slot past the last":         `{"camera9_printer":"A","camera9_name":"c"}`,
		"a slash in a camera name":     `{"camera1_printer":"A","camera1_name":"a/b"}`,
		"a camera name that is long":   `{"camera1_printer":"A","camera1_name":"` + strings.Repeat("a", 65) + `"}`,
		"the same printer twice":       `{"camera1_printer":"A","camera1_name":"x","camera2_printer":"A","camera2_name":"y"}`,
		"an unknown camera field":      `{"camera1_colour":"A"}`,
		"a scheme that is not http":    `{"connect_url":"ftp://127.0.0.1"}`,
		"no host":                      `{"connect_url":"http:///x"}`,
		"a password in the address":    `{"connect_url":"http://user:hunter2@127.0.0.1:8080"}`,
		"a query in the address":       `{"connect_url":"http://127.0.0.1:8080/?a=b"}`,
		"a space in the address":       `{"connect_url":"http://127.0.0.1 :8080"}`,
		"a token with a space":         `{"connect_token":"a b"}`,
		"a token that is far too long": `{"connect_token":"` + strings.Repeat("a", 513) + `"}`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := parseSettings(t, raw)
			if err == nil {
				err = s.validate()
			}

			if err == nil {
				t.Fatalf("%s was accepted", raw)
			}
		})
	}
}

func TestAnErrorAboutTheAddressOrTokenNeverQuotesThem(t *testing.T) {
	for _, raw := range []string{
		`{"connect_url":"ftp://user:hunter2@cam.lan/s.jpg"}`,
		`{"connect_url":"http://user:hunter2@127.0.0.1:8080"}`,
		`{"connect_token":"hunter2 with a space"}`,
	} {
		s, err := parseSettings(t, raw)
		if err == nil {
			err = s.validate()
		}

		if err == nil {
			t.Fatalf("%s accepted", raw)
		}

		if strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("the error quotes a credential: %v", err)
		}
	}
}

func TestASwitchedOffMessageNeedsNoConnectAddressForItsPicture(t *testing.T) {
	s, err := parseSettings(t, `{"notify_finished":"false","image_finished":"thumbnail"}`)
	if err == nil {
		err = s.validate()
	}

	if err != nil {
		t.Fatalf("a picture on a message that is off was refused: %v", err)
	}
}

// The host sends a setting in the type the manifest declares for it: a bool as
// true or false, an int as a number. Found by running the module on a real host.
func TestTypedValuesFromTheHostAreRead(t *testing.T) {
	s, err := parseSettings(t, `{
		"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`",
		"notify_started":false,"notify_cancelled":true,
		"priority_failed":-2,"priority_started":1,
		"sound_finished":"magic","connect_url":null
	}`)
	if err != nil {
		t.Fatal(err)
	}

	if s.Rules["started"].On || !s.Rules["cancelled"].On || s.Rules["failed"].Priority != -2 || s.Rules["started"].Priority != 1 {
		t.Errorf("%+v", s.Rules)
	}

	if s.Rules["finished"].Sound != "magic" || s.ConnectURL != "" {
		t.Errorf("%+v %q", s.Rules["finished"], s.ConnectURL)
	}

	if err = s.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAValueOfAnotherShapeIsStillRefused(t *testing.T) {
	for _, raw := range []string{
		`{"notify_started":[true]}`,
		`{"notify_started":{"on":true}}`,
		`{"priority_failed":1.5}`,
		`{"priority_failed":"1.5"}`,
		`{"priority_failed":9007199254740993}`,
	} {
		s, err := parseSettings(t, raw)
		if err == nil {
			err = s.validate()
		}

		if err == nil {
			t.Errorf("%s was accepted", raw)
		}
	}
}

func TestTheTimeZoneIsCheckedAndUsed(t *testing.T) {
	s, err := parseSettings(t, `{"timezone":"America/New_York"}`)
	if err == nil {
		err = s.validate()
	}

	if err != nil {
		t.Fatal(err)
	}

	if got := clock(base, s.location()); got != "05:00" {
		t.Errorf("10:00 UTC in New York is %q, want 05:00", got)
	}

	for _, zone := range []string{"Mars/Base", "new york", "../etc/passwd"} {
		s, err = parseSettings(t, `{"timezone":"`+zone+`"}`)
		if err == nil {
			err = s.validate()
		}

		if err == nil {
			t.Errorf("%q was accepted", zone)
		}
	}

	empty, _ := parseSettings(t, `{}`)
	if empty.location() != time.Local {
		t.Error("no time zone should mean the zone the module runs in")
	}
}

func TestAMessageWritesItsTimesInTheChosenZone(t *testing.T) {
	s, err := parseSettings(t, `{"pushover_token":"`+goodToken+`","pushover_user":"`+goodUser+`","timezone":"Asia/Tokyo","body_finished":"Ended {ended}"}`)
	if err != nil {
		t.Fatal(err)
	}

	m, _, _ := compose(s, "Core One", event.TypeDeviceJobSettled, settled(t, event.JobSettled{
		DisplayName: "a", Outcome: event.JobFinished, Ended: base,
	}), nil)

	if m.Body != "Ended 19:00" {
		t.Errorf("body %q, want 10:00 UTC written as 19:00 in Tokyo", m.Body)
	}
}

func TestTheConnectAddressMustUseAPortTheManifestAllows(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1", "https://connect.lan", "http://127.0.0.1:8080", "https://c.lan:8443", "http://c.lan:80"} {
		s, err := parseSettings(t, `{"connect_url":"`+ok+`"}`)
		if err == nil {
			err = s.validate()
		}

		if err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}

	for _, bad := range []string{"http://127.0.0.1:18080", "https://c.lan:22", "http://c.lan:443x"} {
		s, err := parseSettings(t, `{"connect_url":"`+bad+`"}`)
		if err == nil {
			err = s.validate()
		}

		if err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}
