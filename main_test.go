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

	got := linkText("https://example.com", "label", true)
	if !strings.Contains(got, "label") {
		t.Fatalf("expected hyperlink text to include label, got %q", got)
	}
}

func TestLinkTextDisabled(t *testing.T) {
	t.Parallel()

	got := linkText("https://example.com", "label", false)
	if got != "label" {
		t.Fatalf("expected plain label when hyperlinks are disabled, got %q", got)
	}
}

func TestToOutputCommentPreservesHTMLURL(t *testing.T) {
	t.Parallel()

	comment := reviewComment{
		ID:      3179628813,
		HTMLURL: "https://github.com/rokuosan/al/pull/1#discussion_r3179628813",
		Thread: reviewThreadMetadata{
			ThreadID:      "PRRT_kwDO123",
			Resolved:      true,
			Outdated:      false,
			ReviewID:      new(int64(42)),
			ReviewNodeID:  "PRR_kwDO42",
			ReplyToID:     new(int64(7)),
			ReplyToNodeID: "PRRC_kwDO7",
		},
	}

	got := toOutputComment(comment)
	if got.URL != comment.HTMLURL {
		t.Fatalf("expected HTMLURL to be preserved: got %q want %q", got.URL, comment.HTMLURL)
	}
	if got.ThreadID != "PRRT_kwDO123" || !got.Resolved || got.Outdated || got.ReviewID == nil || *got.ReviewID != 42 || got.ReplyToID == nil || *got.ReplyToID != 7 {
		t.Fatalf("expected thread metadata to be preserved in output comment: %+v", got)
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

func TestSupportsHyperlinksForceOverride(t *testing.T) {
	t.Setenv("GH_PRV_FORCE_HYPERLINK", "1")
	if !supportsHyperlinks() {
		t.Fatal("expected force override to enable hyperlinks")
	}

	t.Setenv("GH_PRV_FORCE_HYPERLINK", "0")
	if supportsHyperlinks() {
		t.Fatal("expected force override to disable hyperlinks")
	}
}

func TestSupportsHyperlinksITerm(t *testing.T) {
	if !supportsHyperlinksForEnv(map[string]string{
		"TERM":         "xterm-256color",
		"TERM_PROGRAM": "iTerm.app",
	}) {
		t.Fatal("expected iTerm2 to be treated as hyperlink-capable")
	}
}

func TestMergeThreadMetadata(t *testing.T) {
	t.Parallel()

	comments := []reviewComment{
		{NodeID: "PRRC_a"},
		{NodeID: "PRRC_b"},
	}
	metadata := map[string]reviewThreadMetadata{
		"PRRC_b": {
			ThreadID:     "PRRT_thread",
			Resolved:     true,
			Outdated:     true,
			ReviewID:     new(int64(123)),
			ReviewNodeID: "PRR_123",
		},
	}

	mergeThreadMetadata(comments, metadata)

	if comments[0].Thread.ThreadID != "" {
		t.Fatalf("expected first comment to remain without metadata: %+v", comments[0].Thread)
	}
	if comments[1].Thread.ThreadID != "PRRT_thread" || !comments[1].Thread.Resolved || !comments[1].Thread.Outdated {
		t.Fatalf("expected second comment to receive metadata: %+v", comments[1].Thread)
	}
}

func TestPopulateThreadCommentMetadata(t *testing.T) {
	t.Parallel()

	metadata := make(map[string]reviewThreadMetadata)
	base := reviewThreadMetadata{
		ThreadID: "PRRT_thread",
		Resolved: true,
		Outdated: false,
	}

	populateThreadCommentMetadata(metadata, base, []struct {
		ID                string `json:"id"`
		DatabaseID        int64  `json:"databaseId"`
		PullRequestReview *struct {
			ID         string `json:"id"`
			DatabaseID int64  `json:"databaseId"`
		} `json:"pullRequestReview"`
		ReplyTo *struct {
			ID         string `json:"id"`
			DatabaseID int64  `json:"databaseId"`
		} `json:"replyTo"`
	}{
		{
			ID: "PRRC_a",
			PullRequestReview: &struct {
				ID         string `json:"id"`
				DatabaseID int64  `json:"databaseId"`
			}{ID: "PRR_1", DatabaseID: 1},
			ReplyTo: &struct {
				ID         string `json:"id"`
				DatabaseID int64  `json:"databaseId"`
			}{ID: "PRRC_parent", DatabaseID: 2},
		},
	})

	got, ok := metadata["PRRC_a"]
	if !ok {
		t.Fatal("expected metadata entry for PRRC_a")
	}
	if got.ThreadID != "PRRT_thread" || !got.Resolved || got.Outdated {
		t.Fatalf("unexpected base metadata: %+v", got)
	}
	if got.ReviewID == nil || *got.ReviewID != 1 || got.ReplyToID == nil || *got.ReplyToID != 2 {
		t.Fatalf("expected review/reply metadata to be populated: %+v", got)
	}
}
