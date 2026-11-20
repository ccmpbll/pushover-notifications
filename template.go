package main

import (
	"fmt"
	"regexp"
	"strings"
)

// tagNames says what each tag means, in the words the settings page shows.
//
//nolint:gochecknoglobals // a fixed table.
var tagNames = map[string]string{
	"printer":      "Printer name",
	"file":         "File name",
	"outcome":      "How it ended",
	"progress":     "Percent done",
	"time_printed": "Time printed",
	"filament":     "Filament used",
	"energy":       "Power used",
	"cost":         "Power cost",
	"started":      "Start time",
	"ended":        "End time",
	"dialog_title": "Dialog title",
	"dialog_text":  "Dialog text",
}

// tagsFor is the tags each message type may use. A tag outside its list has no
// value for that type, so the page offers no button for it and a save with it
// is refused.
//
//nolint:gochecknoglobals // a fixed table.
var tagsFor = map[string][]string{
	"started":   {"printer", "file", "started"},
	"finished":  {"printer", "file", "outcome", "time_printed", "filament", "energy", "cost", "started", "ended"},
	"failed":    {"printer", "file", "outcome", "progress", "time_printed", "filament", "energy", "cost", "started", "ended"},
	"cancelled": {"printer", "file", "outcome", "progress", "time_printed", "filament", "energy", "cost", "started", "ended"},
	"attention": {"printer", "file", "progress", "dialog_title", "dialog_text"},
	"lost":      {"printer", "file", "progress"},
}

var (
	tagPattern = regexp.MustCompile(`\{[a-z_]+\}`)
	anyBrace   = regexp.MustCompile(`[{}]`)
)

// checkTemplate says whether text can be a template for a message type: every
// tag in it is one of the type's, and no brace is left over. An error never
// holds more of the text than a tag's name.
func checkTemplate(kind, text string) error {
	allowed := tagsFor[kind]

	for _, tag := range tagPattern.FindAllString(text, -1) {
		name := tag[1 : len(tag)-1]
		if !contains(allowed, name) {
			return fmt.Errorf("{%s} is not a tag of this message: use %s", name, tagList(allowed))
		}
	}

	if anyBrace.MatchString(tagPattern.ReplaceAllString(text, "")) {
		return fmt.Errorf("a { or } is not part of a tag: a tag is a lowercase name in braces, such as {file}")
	}

	return nil
}

func contains(list []string, item string) bool {
	for _, have := range list {
		if have == item {
			return true
		}
	}

	return false
}

func tagList(tags []string) string {
	braced := make([]string, len(tags))
	for i, tag := range tags {
		braced[i] = "{" + tag + "}"
	}

	return strings.Join(braced, " ")
}

// render fills a template. A line with a tag that has no value is left out, so
// "{filament} of filament" vanishes for a printer that did not say, and a blank
// line is never sent. A value is put in as it is and never read for tags.
func render(text string, vals map[string]string) string {
	var out []string

	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		empty := false

		filled := tagPattern.ReplaceAllStringFunc(line, func(tag string) string {
			value := vals[tag[1:len(tag)-1]]
			if value == "" {
				empty = true
			}

			return value
		})

		if filled = strings.TrimSpace(filled); filled != "" && !empty {
			out = append(out, filled)
		}
	}

	return strings.Join(out, "\n")
}
