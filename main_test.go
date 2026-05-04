package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePRURL(t *testing.T) {
	t.Parallel()

	got, err := parsePRURL("https://github.com/octo/example/pull/42")
	if err != nil {
		t.Fatalf("parsePRURL returned error: %v", err)
	}

	want := prRef{
		Owner:  "octo",
		Repo:   "example",
		Number: 42,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePRURL mismatch: got %+v want %+v", got, want)
	}
}

func TestExtractSuggestions(t *testing.T) {
	t.Parallel()

	body := "note\n```suggestion\nfmt.Println(\"hello\")\n```\ntext\n```suggestion:-0+2\nreturn nil\n```"
	got := extractSuggestions(body)
	want := []string{"fmt.Println(\"hello\")", "return nil"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractSuggestions mismatch: got %#v want %#v", got, want)
	}
}

func TestSummarizeReplacesSuggestionBlock(t *testing.T) {
	t.Parallel()

	body := "please update this\n```suggestion\nnew line\n```\nand keep behavior"
	got := summarize(body)
	want := "please update this [suggestion] and keep behavior"
	if got != want {
		t.Fatalf("summarize mismatch: got %q want %q", got, want)
	}
}

func TestFormatCommentForTable(t *testing.T) {
	t.Parallel()

	body := "first line\n\nsecond\tline  with   spaces"
	got := formatCommentForTable(body)
	want := "first line second line with spaces"
	if got != want {
		t.Fatalf("formatCommentForTable mismatch: got %q want %q", got, want)
	}
}

func TestWrapCommentForTable(t *testing.T) {
	t.Parallel()

	got := wrapCommentForTable("alpha beta gamma delta", 10)
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected wrapped text to contain newline, got %q", got)
	}
	if strings.Join(strings.Fields(strings.ReplaceAll(got, "\n", " ")), " ") != "alpha beta gamma delta" {
		t.Fatalf("expected wrapped text to preserve content, got %q", got)
	}
}

func TestCommentColumnWidth(t *testing.T) {
	t.Parallel()

	got := commentColumnWidth(120)
	if got != 24 {
		t.Fatalf("unexpected narrow width: got %d want 24", got)
	}

	got = commentColumnWidth(180)
	if got != 52 {
		t.Fatalf("unexpected wide width: got %d want 52", got)
	}
}

func TestLinkText(t *testing.T) {
	t.Parallel()

	got := linkText("https://example.com", "label")
	if !strings.Contains(got, "label") {
		t.Fatalf("expected hyperlink text to include label, got %q", got)
	}
}

func TestCommentChangedFilesURL(t *testing.T) {
	t.Parallel()

	comment := reviewComment{
		ID:      3179628813,
		HTMLURL: "https://github.com/rokuosan/al/pull/1#discussion_r3179628813",
	}

	got := commentChangedFilesURL(comment)
	want := "https://github.com/rokuosan/al/pull/1/changes#r3179628813"
	if got != want {
		t.Fatalf("commentChangedFilesURL mismatch: got %q want %q", got, want)
	}
}

func TestParseCommentTargetArgs(t *testing.T) {
	t.Parallel()

	target, wantJSON, err := parseCommentTargetArgs([]string{
		"https://github.com/octo/example/pull/42",
		"123456",
		"--json",
	})
	if err != nil {
		t.Fatalf("parseCommentTargetArgs returned error: %v", err)
	}

	if target.PR.Owner != "octo" || target.PR.Repo != "example" || target.PR.Number != 42 {
		t.Fatalf("unexpected PR ref: %+v", target.PR)
	}
	if target.ID != 123456 {
		t.Fatalf("unexpected comment ID: %d", target.ID)
	}
	if !wantJSON {
		t.Fatal("expected wantJSON to be true")
	}
}

func TestParseCommentTargetArgsWithNodeID(t *testing.T) {
	t.Parallel()

	target, wantJSON, err := parseCommentTargetArgs([]string{
		"PRRC_kwDOSNCMs869hUkN",
	})
	if err != nil {
		t.Fatalf("parseCommentTargetArgs returned error: %v", err)
	}

	if target.NodeID != "PRRC_kwDOSNCMs869hUkN" {
		t.Fatalf("unexpected node ID: %q", target.NodeID)
	}
	if target.ID != 0 {
		t.Fatalf("expected numeric ID to be empty, got %d", target.ID)
	}
	if wantJSON {
		t.Fatal("expected wantJSON to be false")
	}
}
