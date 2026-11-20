package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // so a zone name loads in a container with no zone files
	"unicode"
)

// kinds are the four things that can be told about a print, in the order the
// settings list them. Each has three keys: notify_<kind>, priority_<kind> and
// sound_<kind>.
var kinds = []string{"started", "finished", "failed", "cancelled", "attention", "lost"}

const (
	minPriority = -2
	maxPriority = 1
)

var (
	// keyPattern is what Pushover's application tokens and user keys look like,
	// loosely: letters and digits. Pushover says whether one is real.
	keyPattern   = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
	soundPattern = regexp.MustCompile(`^[a-z]{1,20}$`)
)

// maxCameras is how many printers can have a camera, one pair of settings each.
const maxCameras = 8

// Pictures a message can carry.
const (
	imageNone      = "none"
	imageThumbnail = "thumbnail"
	imageCamera    = "camera"
)

// Rule is what to do about one kind of event.
type Rule struct {
	On       bool
	Priority int
	// Sound is a Pushover sound name. Empty is the person's own choice.
	Sound string
	// Title and Body are templates, see template.go.
	Title, Body string
	// Image is none, thumbnail (of the print file) or camera.
	Image string
}

// Settings is what an operator writes under this instance's config block. The
// host merges it over the defaults module.yaml declares and sends the result on
// plugin.configure. The zero value sends nothing.
type Settings struct {
	// Token is the Pushover application token and User the user or group key.
	Token, User string
	Rules       map[string]Rule
	// Muted are the serials of printers nobody is told about.
	Muted map[string]bool
	// ConnectURL and ConnectToken are how the module asks Prusa Connect for a
	// picture: the server's address, and a read-only API token.
	ConnectURL, ConnectToken string
	// Cameras is the camera chosen for a printer, by serial.
	Cameras map[string]string
	// Timezone is the zone {started} and {ended} are written in, such as
	// Europe/Berlin. Empty is the zone the module runs in, which in a container
	// is usually UTC.
	Timezone string
}

// location is the zone to write times in. The name was checked by validate; one
// that cannot be loaded falls back to UTC rather than stopping a message.
func (s Settings) location() *time.Location {
	if s.Timezone == "" {
		return time.Local
	}

	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.UTC
	}

	return loc
}

// connectPorts are the ports the manifest lets the module connect to for
// connect_url. A test holds this list to module.yaml.
//
//nolint:gochecknoglobals // a fixed list.
var connectPorts = []int{80, 443, 8080, 8443}

// defaultRules is what a kind does when nobody says otherwise. The manifest
// declares the same values, and a test holds the two together.
func defaultRules() map[string]Rule {
	return map[string]Rule{
		"started":   {On: true, Priority: -1, Title: "{printer}: print started", Body: "{file}", Image: imageNone},
		"finished":  {On: true, Priority: 0, Title: "{printer}: print finished", Body: "{file}\nPrinted for {time_printed}\n{filament} of filament\nUsed {energy}", Image: imageNone},
		"failed":    {On: true, Priority: 1, Title: "{printer}: print {outcome}", Body: "{file}\nStopped at {progress}", Image: imageNone},
		"cancelled": {On: false, Priority: 0, Title: "{printer}: print {outcome}", Body: "{file}\nStopped at {progress}", Image: imageNone},
		"attention": {On: true, Priority: 1, Title: "{printer} needs you: {dialog_title}", Body: "{dialog_text}\n{file}", Image: imageNone},
		"lost":      {On: true, Priority: 1, Title: "{printer} went offline during a print", Body: "{file}\nLast seen at {progress}", Image: imageNone},
	}
}

func (s Settings) configured() bool { return s.Token != "" && s.User != "" }

func (s Settings) muted(serial string) bool { return serial != "" && s.Muted[serial] }

var (
	cameraKey = regexp.MustCompile(`^camera([0-9]+)_(printer|name)$`)
	// cameraName is what Connect calls a camera. It goes into a URL path, so
	// the characters that would change the path are refused.
	cameraBad = regexp.MustCompile(`[/\\?#%\x00-\x1f\x7f]`)
)

const (
	maxCameraName = 64
	maxTokenChars = 512
)

// UnmarshalJSON reads the flat keys the host sends. A key that is not one of
// the module's, or a value of the wrong shape, is refused by name, so a typo is
// found at start and not when a print ends.
func (s *Settings) UnmarshalJSON(data []byte) error {
	raw, err := flatten(data)
	if err != nil {
		return err
	}

	*s = Settings{Rules: defaultRules(), Muted: map[string]bool{}, Cameras: map[string]string{}}

	slots := map[int]*[2]string{}

	for key, value := range raw {
		value = strings.TrimSpace(value)

		switch key {
		case "pushover_token":
			s.Token = value
		case "pushover_user":
			s.User = value
		case "connect_url":
			s.ConnectURL = strings.TrimRight(value, "/")
		case "connect_token":
			s.ConnectToken = value
		case "timezone":
			s.Timezone = value
		case "muted_printers":
			for serial := range strings.SplitSeq(value, ",") {
				if serial = strings.TrimSpace(serial); serial != "" {
					s.Muted[serial] = true
				}
			}
		default:
			if match := cameraKey.FindStringSubmatch(key); match != nil {
				if err := readCameraSlot(slots, match, value); err != nil {
					return err
				}

				continue
			}

			if err := s.rule(key, value); err != nil {
				return err
			}
		}
	}

	return s.pairCameras(slots)
}

// flatten reads the settings as text. The host sends each in the type the
// manifest declares for it, so a bool is true or false and an int is a number;
// every value is read back into the text the settings are written in. Anything
// that is not text, a bool, a number or null is refused by key.
func flatten(data []byte) (map[string]string, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("the settings are not a flat map of values: %w", err)
	}

	out := make(map[string]string, len(raw))

	for key, value := range raw {
		switch typed := value.(type) {
		case nil:
			out[key] = ""
		case string:
			out[key] = typed
		case bool:
			out[key] = strconv.FormatBool(typed)
		case float64:
			out[key] = strconv.FormatFloat(typed, 'f', -1, 64)
		default:
			return nil, fmt.Errorf("%s is not text, a number or true or false", key)
		}
	}

	return out, nil
}

func readCameraSlot(slots map[int]*[2]string, match []string, value string) error {
	slot, err := strconv.Atoi(match[1])
	if err != nil || slot < 1 || slot > maxCameras {
		return fmt.Errorf("camera%s_%s names a camera slot outside 1 to %d", match[1], match[2], maxCameras)
	}

	if slots[slot] == nil {
		slots[slot] = &[2]string{}
	}

	if match[2] == "printer" {
		slots[slot][0] = value
	} else {
		slots[slot][1] = value
	}

	return nil
}

// pairCameras turns the slots into the camera of each printer. A slot with
// only one of its two settings, or a printer given two cameras, is refused.
func (s *Settings) pairCameras(slots map[int]*[2]string) error {
	for slot := 1; slot <= maxCameras; slot++ {
		held := slots[slot]
		if held == nil || (held[0] == "" && held[1] == "") {
			continue
		}

		if held[0] == "" || held[1] == "" {
			return fmt.Errorf("camera%d needs both camera%d_printer and camera%d_name, or neither", slot, slot, slot)
		}

		if len([]rune(held[1])) > maxCameraName || cameraBad.MatchString(held[1]) {
			return fmt.Errorf("camera%d_name is not a camera name: up to %d characters, without / ? # %%", slot, maxCameraName)
		}

		if _, again := s.Cameras[held[0]]; again {
			return fmt.Errorf("camera%d_printer repeats a printer that already has a camera", slot)
		}

		s.Cameras[held[0]] = held[1]
	}

	return nil
}

// rule reads one notify_, priority_, sound_, title_, body_ or image_ key.
func (s *Settings) rule(key, value string) error {
	field, kind, ok := strings.Cut(key, "_")
	if !ok || !isKind(kind) {
		return fmt.Errorf("%q is not a setting of this module", key)
	}

	rule := s.Rules[kind]

	switch field {
	case "notify":
		on, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s is %q: write true or false", key, value)
		}

		rule.On = on
	case "priority":
		n, err := strconv.Atoi(value)
		if err != nil || n < minPriority || n > maxPriority {
			return fmt.Errorf("%s is %q: a whole number from %d to %d", key, value, minPriority, maxPriority)
		}

		rule.Priority = n
	case "sound":
		if value != "" && !soundPattern.MatchString(value) {
			return fmt.Errorf("%s is %q: a Pushover sound name in lowercase letters, or empty", key, value)
		}

		rule.Sound = value
	case "title":
		rule.Title = value
	case "body":
		rule.Body = value
	case "image":
		if value != imageNone && value != imageThumbnail && value != imageCamera {
			return fmt.Errorf("%s is %q: write none, thumbnail or camera", key, value)
		}

		rule.Image = value
	default:
		return fmt.Errorf("%q is not a setting of this module", key)
	}

	s.Rules[kind] = rule

	return nil
}

func isKind(kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}

	return false
}

// validate says whether the module can run on these settings. Empty is valid:
// the module then starts and sends nothing. A message never quotes a credential
// or an address, so none is written to the log.
func (s Settings) validate() error {
	if (s.Token == "") != (s.User == "") {
		return errors.New("pushover_token and pushover_user go together: set both or neither")
	}

	if s.Token != "" && !keyPattern.MatchString(s.Token) {
		return errors.New("pushover_token is not a Pushover application token: letters and digits, up to 64")
	}

	if s.User != "" && !keyPattern.MatchString(s.User) {
		return errors.New("pushover_user is not a Pushover user key: letters and digits, up to 64")
	}

	if err := s.validateConnect(); err != nil {
		return err
	}

	return s.validateRules()
}

func (s Settings) validateConnect() error {
	if s.ConnectURL != "" {
		parsed, err := url.Parse(s.ConnectURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(s.ConnectURL, " \t\r\n") {
			return errors.New("connect_url is not an address of Prusa Connect: http:// or https://, a host, and no password, query or fragment")
		}
	}

	if err := s.validatePort(); err != nil {
		return err
	}

	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return errors.New("timezone is not a time zone name such as Europe/Berlin or America/New_York")
		}
	}

	if s.ConnectToken != "" && (len(s.ConnectToken) > maxTokenChars || strings.ContainsFunc(s.ConnectToken, unicode.IsSpace) || strings.ContainsFunc(s.ConnectToken, unicode.IsControl)) {
		return errors.New("connect_token is not an API token: no spaces, up to 512 characters")
	}

	return nil
}

// validatePort refuses an address on a port the manifest does not let the module
// connect to. The host would block the connection and every picture would fail
// with nothing to say why.
func (s Settings) validatePort() error {
	if s.ConnectURL == "" {
		return nil
	}

	parsed, err := url.Parse(s.ConnectURL)
	if err != nil {
		return nil // validateConnect has said so.
	}

	port := parsed.Port()
	if port == "" {
		port = "80"
		if parsed.Scheme == "https" {
			port = "443"
		}
	}

	number, err := strconv.Atoi(port)
	if err != nil || !slices.Contains(connectPorts, number) {
		return fmt.Errorf("connect_url uses a port this extension may not connect to: use one of %s", portList(connectPorts))
	}

	return nil
}

func portList(ports []int) string {
	text := make([]string, len(ports))
	for i, port := range ports {
		text[i] = strconv.Itoa(port)
	}

	return strings.Join(text, ", ")
}

func (s Settings) validateRules() error {
	if s.Rules == nil {
		return nil
	}

	wantsPicture := false

	for _, kind := range kinds {
		rule := s.Rules[kind]

		if strings.TrimSpace(rule.Title) == "" {
			return fmt.Errorf("title_%s cannot be empty", kind)
		}

		if len([]rune(rule.Title)) > maxTitle {
			return fmt.Errorf("title_%s is longer than %d characters", kind, maxTitle)
		}

		if len([]rune(rule.Body)) > maxBody {
			return fmt.Errorf("body_%s is longer than %d characters", kind, maxBody)
		}

		if err := checkTemplate(kind, rule.Title); err != nil {
			return fmt.Errorf("title_%s: %w", kind, err)
		}

		if err := checkTemplate(kind, rule.Body); err != nil {
			return fmt.Errorf("body_%s: %w", kind, err)
		}

		if rule.Image != imageNone {
			if kind == "lost" {
				return errors.New("image_lost must be none: the printer is gone, so there is nothing to take a picture of")
			}

			wantsPicture = rule.On || wantsPicture
		}
	}

	if wantsPicture && (s.ConnectURL == "" || s.ConnectToken == "") {
		return errors.New("a picture needs connect_url and connect_token, so the module can ask Prusa Connect for it")
	}

	return nil
}
