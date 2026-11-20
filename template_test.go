package main

import "testing"

func TestRenderFillsTagsAndKeepsPlainLines(t *testing.T) {
	got := render("{file}\nPrinted for {time_printed}\nGood job", map[string]string{"file": "a.gcode", "time_printed": "1h 5m"})
	if want := "a.gcode\nPrinted for 1h 5m\nGood job"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

func TestALineWithAnEmptyOrMissingTagIsLeftOut(t *testing.T) {
	vals := map[string]string{"file": "a.gcode", "filament": ""}

	got := render("{file}\n{filament} of filament\nUsed {energy}\nDone", vals)
	if want := "a.gcode\nDone"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

func TestBlankLinesAndTrailingSpacesAreTidied(t *testing.T) {
	got := render("  {file}  \r\n\r\n\n{printer}   \r\n", map[string]string{"file": "a", "printer": "B"})
	if want := "a\nB"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

func TestAValueIsNeverExpandedAgain(t *testing.T) {
	got := render("{file}", map[string]string{"file": "{printer}", "printer": "SECRET"})
	if got != "{printer}" {
		t.Errorf("%q: a file name that looks like a tag was expanded", got)
	}
}

func TestCheckTemplateKnowsEachTypesTags(t *testing.T) {
	good := map[string]string{
		"started":   "{printer}: {file} at {started}",
		"finished":  "{energy} {cost} {time_printed} {filament} {outcome} {ended}",
		"failed":    "{progress}",
		"cancelled": "{progress}",
		"attention": "{dialog_title} {dialog_text} {progress} {file}",
		"lost":      "{progress} {file} {printer}",
	}

	for kind, tpl := range good {
		if err := checkTemplate(kind, tpl); err != nil {
			t.Errorf("%s: %q refused: %v", kind, tpl, err)
		}
	}

	bad := map[string]string{
		"started":   "{energy}",
		"finished":  "{progress}",
		"attention": "{energy}",
		"lost":      "{dialog_text}",
	}

	for kind, tpl := range bad {
		if err := checkTemplate(kind, tpl); err == nil {
			t.Errorf("%s: %q accepted", kind, tpl)
		}
	}

	for _, tpl := range []string{"{", "}", "{{file}}", "{file", "file}", "{ file }", "{}", "{File}"} {
		if err := checkTemplate("started", tpl); err == nil {
			t.Errorf("%q accepted", tpl)
		}
	}
}

func TestEveryTagOfEveryTypeHasADescriptionForThePage(t *testing.T) {
	for kind, tags := range tagsFor {
		for _, tag := range tags {
			if _, ok := tagNames[tag]; !ok {
				t.Errorf("%s: tag %s is not described", kind, tag)
			}
		}
	}
}
