package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"golang.org/x/term"
)

var (
	suggestionRegexp = regexp.MustCompile("(?s)```suggestion(?:[^\\n]*)\\n(.*?)```")
	codeFenceRegexp  = regexp.MustCompile("(?s)```(?:[[:alnum:]_+.-]*)\\n(.*?)```")
)

type prRef struct {
	Owner  string
	Repo   string
	Number int
}

type reviewComment struct {
	ID           int64  `json:"id"`
	NodeID       string `json:"node_id"`
	Path         string `json:"path"`
	Line         *int   `json:"line"`
	StartLine    *int   `json:"start_line"`
	OriginalLine *int   `json:"original_line"`
	Body         string `json:"body"`
	DiffHunk     string `json:"diff_hunk"`
	HTMLURL      string `json:"html_url"`
	User         struct {
		Login string `json:"login"`
	} `json:"user"`
	Thread reviewThreadMetadata `json:"-"`
}

type outputComment struct {
	ID               int64    `json:"id"`
	NodeID           string   `json:"node_id"`
	Author           string   `json:"author"`
	Path             string   `json:"path"`
	Line             int      `json:"line"`
	Body             string   `json:"body"`
	Summary          string   `json:"summary"`
	HasSuggestion    bool     `json:"has_suggestion"`
	SuggestionBlocks []string `json:"suggestion_blocks,omitempty"`
	CodeBlocks       []string `json:"code_blocks,omitempty"`
	URL              string   `json:"url"`
	ThreadID         string   `json:"thread_id,omitempty"`
	Resolved         bool     `json:"resolved"`
	Outdated         bool     `json:"outdated"`
	ReviewID         *int64   `json:"review_id,omitempty"`
	ReviewNodeID     string   `json:"review_node_id,omitempty"`
	ReplyToID        *int64   `json:"reply_to_id,omitempty"`
	ReplyToNodeID    string   `json:"reply_to_node_id,omitempty"`
}

type reviewThreadMetadata struct {
	ThreadID      string
	Resolved      bool
	Outdated      bool
	ReviewID      *int64
	ReviewNodeID  string
	ReplyToID     *int64
	ReplyToNodeID string
}

type cachedCommentRef struct {
	ID int64 `json:"id"`
	PR prRef `json:"pr"`
}

type cacheFile struct {
	Comments []cachedCommentRef `json:"comments"`
}

type diffTarget struct {
	PR     prRef
	ID     int64
	NodeID string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "diff":
		return runDiff(args[1:])
	case "show":
		return runShow(args[1:])
	default:
		return runList(args)
	}
}

func runList(args []string) error {
	parsed, wantJSON, suggestedOnly, err := parseListArgs(args)
	if err != nil {
		return err
	}

	client, err := api.DefaultRESTClient()
	if err != nil {
		return err
	}

	comments, err := listReviewComments(client, parsed)
	if err != nil {
		return err
	}

	if wantJSON {
		graphQLClient, err := api.DefaultGraphQLClient()
		if err != nil {
			return err
		}

		threadMetadata, err := listReviewThreadMetadata(graphQLClient, parsed)
		if err != nil {
			return err
		}
		mergeThreadMetadata(comments, threadMetadata)
	}

	items := make([]outputComment, 0, len(comments))
	for _, comment := range comments {
		item := toOutputComment(comment)
		if suggestedOnly && !item.HasSuggestion {
			continue
		}
		items = append(items, item)
	}

	_ = writeCommentCache(parsed, comments)

	if wantJSON {
		return printJSON(items)
	}
	printCommentTable(items)
	return nil
}

func runDiff(args []string) error {
	target, wantJSON, err := parseCommentTargetArgs(args)
	if err != nil {
		return err
	}

	comment, err := fetchComment(target)
	if err != nil {
		return err
	}

	suggestions := extractSuggestions(comment.Body)
	codeBlocks := extractCodeBlocks(comment.Body)
	if len(suggestions) == 0 && len(codeBlocks) == 0 {
		return errors.New("no API-visible suggestion/code blocks found for this comment")
	}

	if wantJSON {
		return printJSON(struct {
			Comment       outputComment `json:"comment"`
			Suggestions   []string      `json:"suggestions,omitempty"`
			SuggestedCode []string      `json:"suggested_code,omitempty"`
		}{
			Comment:       toOutputComment(comment),
			Suggestions:   suggestions,
			SuggestedCode: codeBlocks,
		})
	}

	printCommentDiff(comment, suggestions, codeBlocks)
	return nil
}

func runShow(args []string) error {
	target, wantJSON, err := parseCommentTargetArgs(args)
	if err != nil {
		return err
	}

	comment, err := fetchComment(target)
	if err != nil {
		return err
	}

	if wantJSON {
		return printJSON(struct {
			Comment  outputComment `json:"comment"`
			DiffHunk string        `json:"diff_hunk"`
		}{
			Comment:  toOutputComment(comment),
			DiffHunk: comment.DiffHunk,
		})
	}

	printCommentDetail(comment)
	return nil
}

func parseListArgs(args []string) (prRef, bool, bool, error) {
	var parsed prRef
	var prURL string
	wantJSON := false
	suggestedOnly := false

	for _, arg := range args {
		switch arg {
		case "--json":
			wantJSON = true
		case "--suggested":
			suggestedOnly = true
		default:
			if strings.HasPrefix(arg, "-") {
				return prRef{}, false, false, fmt.Errorf("unknown flag: %s", arg)
			}
			if prURL != "" {
				return prRef{}, false, false, errors.New("expected a single PR URL")
			}
			prURL = arg
		}
	}

	if prURL == "" {
		return prRef{}, false, false, usageError()
	}

	parsed, err := parsePRURL(prURL)
	if err != nil {
		return prRef{}, false, false, err
	}

	return parsed, wantJSON, suggestedOnly, nil
}

func parseCommentTargetArgs(args []string) (diffTarget, bool, error) {
	var target diffTarget
	var prURL string
	var idRaw string
	var err error
	wantJSON := false

	for _, arg := range args {
		switch arg {
		case "--json":
			wantJSON = true
		default:
			if strings.HasPrefix(arg, "-") {
				return diffTarget{}, false, fmt.Errorf("unknown flag: %s", arg)
			}
			if prURL == "" {
				prURL = arg
				continue
			}
			if idRaw == "" {
				idRaw = arg
				continue
			}
			return diffTarget{}, false, errors.New("expected: gh prv <show|diff> <PR URL> <COMMENT_ID>")
		}
	}

	if idRaw == "" && prURL != "" {
		idRaw = prURL
		prURL = ""
	}

	if idRaw == "" {
		return diffTarget{}, false, errors.New("expected: gh prv <show|diff> <COMMENT_ID|NODE_ID>\n   or: gh prv <show|diff> <PR URL> <COMMENT_ID>")
	}

	if isReviewCommentNodeID(idRaw) {
		return diffTarget{NodeID: idRaw}, wantJSON, nil
	}

	if prURL != "" {
		target.PR, err = parsePRURL(prURL)
		if err != nil {
			return diffTarget{}, false, err
		}
	}

	id, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil {
		return diffTarget{}, false, fmt.Errorf("invalid comment ID %q", idRaw)
	}
	target.ID = id

	if prURL == "" {
		target.PR, err = lookupCachedPR(id)
		if err != nil {
			return diffTarget{}, false, err
		}
	}

	return target, wantJSON, nil
}

func parsePRURL(raw string) (prRef, error) {
	const prefix = "https://github.com/"
	if !strings.HasPrefix(raw, prefix) {
		return prRef{}, fmt.Errorf("unsupported PR URL: %s", raw)
	}

	parts := strings.Split(strings.TrimPrefix(raw, prefix), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return prRef{}, fmt.Errorf("unsupported PR URL: %s", raw)
	}

	number, err := strconv.Atoi(parts[3])
	if err != nil {
		return prRef{}, fmt.Errorf("invalid PR number in URL: %s", raw)
	}

	return prRef{
		Owner:  parts[0],
		Repo:   parts[1],
		Number: number,
	}, nil
}

func listReviewComments(client *api.RESTClient, pr prRef) ([]reviewComment, error) {
	var all []reviewComment

	for page := 1; ; page++ {
		path := fmt.Sprintf("repos/%s/%s/pulls/%d/comments?per_page=100&page=%d", pr.Owner, pr.Repo, pr.Number, page)
		var chunk []reviewComment
		if err := client.Get(path, &chunk); err != nil {
			return nil, err
		}
		if len(chunk) == 0 {
			break
		}
		all = append(all, chunk...)
	}

	return all, nil
}

func listReviewThreadMetadata(client *api.GraphQLClient, pr prRef) (map[string]reviewThreadMetadata, error) {
	const query = `
	query($owner: String!, $repo: String!, $number: Int!, $after: String) {
	  repository(owner: $owner, name: $repo) {
	    pullRequest(number: $number) {
	      reviewThreads(first: 100, after: $after) {
	        nodes {
	          id
	          isResolved
	          isOutdated
	          comments(first: 100) {
	            nodes {
	              id
	              databaseId
	              pullRequestReview {
	                id
	                databaseId
	              }
	              replyTo {
	                id
	                databaseId
	              }
	            }
	            pageInfo {
	              hasNextPage
	              endCursor
	            }
	          }
	        }
	        pageInfo {
	          hasNextPage
	          endCursor
	        }
	      }
	    }
	  }
	}`

	var after *string
	metadata := make(map[string]reviewThreadMetadata)
	for {
		var response struct {
			Repository *struct {
				PullRequest *struct {
					ReviewThreads struct {
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
							IsOutdated bool   `json:"isOutdated"`
							Comments   struct {
								Nodes []struct {
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
								} `json:"nodes"`
								PageInfo struct {
									HasNextPage bool    `json:"hasNextPage"`
									EndCursor   *string `json:"endCursor"`
								} `json:"pageInfo"`
							} `json:"comments"`
						} `json:"nodes"`
						PageInfo struct {
							HasNextPage bool    `json:"hasNextPage"`
							EndCursor   *string `json:"endCursor"`
						} `json:"pageInfo"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}

		variables := map[string]interface{}{
			"owner":  pr.Owner,
			"repo":   pr.Repo,
			"number": pr.Number,
			"after":  after,
		}
		if err := client.Do(query, variables, &response); err != nil {
			return nil, err
		}
		if response.Repository == nil || response.Repository.PullRequest == nil {
			return nil, fmt.Errorf("pull request not found for %s/%s#%d", pr.Owner, pr.Repo, pr.Number)
		}

		for _, thread := range response.Repository.PullRequest.ReviewThreads.Nodes {
			baseMetadata := reviewThreadMetadata{
				ThreadID: thread.ID,
				Resolved: thread.IsResolved,
				Outdated: thread.IsOutdated,
			}
			populateThreadCommentMetadata(metadata, baseMetadata, thread.Comments.Nodes)

			if thread.Comments.PageInfo.HasNextPage {
				if err := appendPaginatedThreadCommentMetadata(client, metadata, baseMetadata, thread.ID, thread.Comments.PageInfo.EndCursor); err != nil {
					return nil, err
				}
			}
		}

		if !response.Repository.PullRequest.ReviewThreads.PageInfo.HasNextPage || response.Repository.PullRequest.ReviewThreads.PageInfo.EndCursor == nil {
			break
		}
		after = response.Repository.PullRequest.ReviewThreads.PageInfo.EndCursor
	}

	return metadata, nil
}

func appendPaginatedThreadCommentMetadata(client *api.GraphQLClient, metadata map[string]reviewThreadMetadata, baseMetadata reviewThreadMetadata, threadID string, after *string) error {
	const query = `
	query($id: ID!, $after: String) {
	  node(id: $id) {
	    ... on PullRequestReviewThread {
	      comments(first: 100, after: $after) {
	        nodes {
	          id
	          databaseId
	          pullRequestReview {
	            id
	            databaseId
	          }
	          replyTo {
	            id
	            databaseId
	          }
	        }
	        pageInfo {
	          hasNextPage
	          endCursor
	        }
	      }
	    }
	  }
	}`

	for {
		var response struct {
			Node *struct {
				Comments struct {
					Nodes []struct {
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
					} `json:"nodes"`
					PageInfo struct {
						HasNextPage bool    `json:"hasNextPage"`
						EndCursor   *string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"comments"`
			} `json:"node"`
		}

		if err := client.Do(query, map[string]interface{}{"id": threadID, "after": after}, &response); err != nil {
			return err
		}
		if response.Node == nil {
			return fmt.Errorf("review thread not found for node ID %q", threadID)
		}

		populateThreadCommentMetadata(metadata, baseMetadata, response.Node.Comments.Nodes)

		if !response.Node.Comments.PageInfo.HasNextPage || response.Node.Comments.PageInfo.EndCursor == nil {
			return nil
		}
		after = response.Node.Comments.PageInfo.EndCursor
	}
}

func populateThreadCommentMetadata(metadata map[string]reviewThreadMetadata, baseMetadata reviewThreadMetadata, comments []struct {
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
}) {
	for _, comment := range comments {
		item := baseMetadata
		if comment.PullRequestReview != nil {
			item.ReviewID = new(int64(comment.PullRequestReview.DatabaseID))
			item.ReviewNodeID = comment.PullRequestReview.ID
		}
		if comment.ReplyTo != nil {
			item.ReplyToID = new(int64(comment.ReplyTo.DatabaseID))
			item.ReplyToNodeID = comment.ReplyTo.ID
		}
		metadata[comment.ID] = item
	}
}

func mergeThreadMetadata(comments []reviewComment, metadata map[string]reviewThreadMetadata) {
	for i := range comments {
		if item, ok := metadata[comments[i].NodeID]; ok {
			comments[i].Thread = item
		}
	}
}

func getReviewComment(client *api.RESTClient, pr prRef, id int64) (reviewComment, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/comments/%d", pr.Owner, pr.Repo, id)
	var comment reviewComment
	if err := client.Get(path, &comment); err != nil {
		return reviewComment{}, err
	}
	return comment, nil
}

func getReviewCommentByNodeID(client *api.GraphQLClient, nodeID string) (reviewComment, error) {
	const query = `
	query($id: ID!) {
	  node(id: $id) {
	    ... on PullRequestReviewComment {
	      databaseId
	      id
	      body
	      diffHunk
	      path
	      line
	      startLine
	      originalLine
	      url
	      author {
	        login
	      }
	    }
	  }
	}`

	var response struct {
		Node *struct {
			DatabaseID   int64  `json:"databaseId"`
			ID           string `json:"id"`
			Body         string `json:"body"`
			DiffHunk     string `json:"diffHunk"`
			Path         string `json:"path"`
			Line         *int   `json:"line"`
			StartLine    *int   `json:"startLine"`
			OriginalLine *int   `json:"originalLine"`
			URL          string `json:"url"`
			Author       *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"node"`
	}

	if err := client.Do(query, map[string]interface{}{"id": nodeID}, &response); err != nil {
		return reviewComment{}, err
	}
	if response.Node == nil {
		return reviewComment{}, fmt.Errorf("review comment not found for node ID %q", nodeID)
	}

	comment := reviewComment{
		ID:           response.Node.DatabaseID,
		NodeID:       response.Node.ID,
		Path:         response.Node.Path,
		Line:         response.Node.Line,
		StartLine:    response.Node.StartLine,
		OriginalLine: response.Node.OriginalLine,
		Body:         response.Node.Body,
		DiffHunk:     response.Node.DiffHunk,
		HTMLURL:      response.Node.URL,
	}
	if response.Node.Author != nil {
		comment.User.Login = response.Node.Author.Login
	}

	return comment, nil
}

func fetchComment(target diffTarget) (reviewComment, error) {
	if target.NodeID != "" {
		client, err := api.DefaultGraphQLClient()
		if err != nil {
			return reviewComment{}, err
		}
		return getReviewCommentByNodeID(client, target.NodeID)
	}

	client, err := api.DefaultRESTClient()
	if err != nil {
		return reviewComment{}, err
	}
	return getReviewComment(client, target.PR, target.ID)
}

func toOutputComment(comment reviewComment) outputComment {
	suggestions := extractSuggestions(comment.Body)
	codeBlocks := extractCodeBlocks(comment.Body)
	return outputComment{
		ID:               comment.ID,
		NodeID:           comment.NodeID,
		Author:           comment.User.Login,
		Path:             comment.Path,
		Line:             commentLine(comment),
		Body:             comment.Body,
		Summary:          summarize(comment.Body),
		HasSuggestion:    len(suggestions) > 0 || len(codeBlocks) > 0,
		SuggestionBlocks: suggestions,
		CodeBlocks:       codeBlocks,
		URL:              comment.HTMLURL,
		ThreadID:         comment.Thread.ThreadID,
		Resolved:         comment.Thread.Resolved,
		Outdated:         comment.Thread.Outdated,
		ReviewID:         comment.Thread.ReviewID,
		ReviewNodeID:     comment.Thread.ReviewNodeID,
		ReplyToID:        comment.Thread.ReplyToID,
		ReplyToNodeID:    comment.Thread.ReplyToNodeID,
	}
}

func commentLine(comment reviewComment) int {
	switch {
	case comment.Line != nil:
		return *comment.Line
	case comment.OriginalLine != nil:
		return *comment.OriginalLine
	case comment.StartLine != nil:
		return *comment.StartLine
	default:
		return 0
	}
}

func extractSuggestions(body string) []string {
	return extractBlocks(body, suggestionRegexp)
}

func extractCodeBlocks(body string) []string {
	suggestionSpans := suggestionRegexp.FindAllStringIndex(body, -1)
	codeMatches := codeFenceRegexp.FindAllStringSubmatchIndex(body, -1)
	if len(codeMatches) == 0 {
		return nil
	}

	blocks := make([]string, 0, len(codeMatches))
	for _, match := range codeMatches {
		if overlapsAny(match[0], match[1], suggestionSpans) {
			continue
		}
		blocks = append(blocks, strings.TrimSuffix(body[match[2]:match[3]], "\n"))
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}

func extractBlocks(body string, pattern *regexp.Regexp) []string {
	matches := pattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}

	suggestions := make([]string, 0, len(matches))
	for _, match := range matches {
		suggestions = append(suggestions, strings.TrimSuffix(match[1], "\n"))
	}
	return suggestions
}

func overlapsAny(start, end int, spans [][]int) bool {
	for _, span := range spans {
		if start < span[1] && span[0] < end {
			return true
		}
	}
	return false
}

func summarize(body string) string {
	body = suggestionRegexp.ReplaceAllString(body, "[suggestion]")
	body = strings.Join(strings.Fields(body), " ")
	const maxLen = 72
	if len(body) <= maxLen {
		return body
	}
	return body[:maxLen-3] + "..."
}

func printCommentTable(items []outputComment) {
	width := stdoutWidth()
	commentWidth := commentColumnWidth(width)
	hyperlinks := supportsHyperlinks()

	tw := table.NewWriter()
	tw.SetStyle(table.StyleRounded)
	tw.Style().Options.DrawBorder = false
	tw.Style().Options.SeparateRows = true
	tw.Style().Box.Left = ""
	tw.Style().Box.Right = ""
	tw.Style().Box.TopLeft = ""
	tw.Style().Box.TopRight = ""
	tw.Style().Box.BottomLeft = ""
	tw.Style().Box.BottomRight = ""
	tw.Style().Box.UnfinishedRow = ""
	tw.SetOutputMirror(os.Stdout)
	tw.AppendHeader(table.Row{"ID", "NODE_ID", "AUTHOR", "FILE", "LINE", "SUGGESTED", "COMMENT"})
	tw.SetColumnConfigs([]table.ColumnConfig{
		{Name: "ID", WidthMax: 12},
		{Name: "NODE_ID", WidthMax: 22, WidthMaxEnforcer: text.WrapSoft},
		{Name: "AUTHOR", WidthMax: 16, WidthMaxEnforcer: text.WrapSoft},
		{Name: "FILE", WidthMax: 24, WidthMaxEnforcer: text.WrapSoft},
		{Name: "LINE", WidthMax: 8, Align: text.AlignRight},
		{Name: "SUGGESTED", WidthMax: 10},
		{Name: "COMMENT", WidthMin: commentWidth, WidthMax: commentWidth},
	})

	for _, item := range items {
		tw.AppendRow(table.Row{
			linkText(item.URL, strconv.FormatInt(item.ID, 10), hyperlinks),
			linkText(item.URL, item.NodeID, hyperlinks),
			item.Author,
			item.Path,
			item.Line,
			yesNo(item.HasSuggestion),
			hyperlinkWrappedText(item.URL, formatCommentForTable(item.Body), commentWidth, hyperlinks),
		})
	}

	_ = tw.Render()
}

func printCommentDetail(comment reviewComment) {
	item := toOutputComment(comment)
	fmt.Printf("ID: %d\n", item.ID)
	fmt.Printf("NodeID: %s\n", item.NodeID)
	fmt.Printf("Author: %s\n", item.Author)
	fmt.Printf("File: %s\n", item.Path)
	if item.Line > 0 {
		fmt.Printf("Line: %d\n", item.Line)
	}
	fmt.Printf("Suggested: %s\n", yesNo(item.HasSuggestion))
	fmt.Printf("URL: %s\n\n", item.URL)

	fmt.Println("Comment:")
	fmt.Println(comment.Body)

	if comment.DiffHunk != "" {
		fmt.Println()
		fmt.Println("Diff Hunk:")
		fmt.Println(comment.DiffHunk)
	}

	suggestions := extractSuggestions(comment.Body)
	for i, suggestion := range suggestions {
		fmt.Println()
		fmt.Printf("Suggestion %d:\n", i+1)
		fmt.Println(suggestion)
	}

	codeBlocks := extractCodeBlocks(comment.Body)
	for i, block := range codeBlocks {
		fmt.Println()
		fmt.Printf("Suggested Code %d:\n", i+1)
		fmt.Println(block)
	}
}

func printCommentDiff(comment reviewComment, suggestions, codeBlocks []string) {
	item := toOutputComment(comment)
	fmt.Printf("ID: %d\n", item.ID)
	fmt.Printf("NodeID: %s\n", item.NodeID)
	fmt.Printf("Author: %s\n", item.Author)
	fmt.Printf("File: %s\n", item.Path)
	if item.Line > 0 {
		fmt.Printf("Line: %d\n", item.Line)
	}
	fmt.Printf("URL: %s\n", item.URL)

	for i, suggestion := range suggestions {
		fmt.Println()
		fmt.Printf("Suggestion %d:\n", i+1)
		fmt.Println(suggestion)
	}

	for i, block := range codeBlocks {
		fmt.Println()
		fmt.Printf("Suggested Code %d:\n", i+1)
		fmt.Println(block)
	}
}

func printJSON(v interface{}) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func usageError() error {
	return errors.New("usage:\n  gh prv <PR URL> [--json] [--suggested]\n  gh prv show <COMMENT_ID|NODE_ID> [--json]\n  gh prv show <PR URL> <COMMENT_ID> [--json]\n  gh prv diff <COMMENT_ID|NODE_ID> [--json]\n  gh prv diff <PR URL> <COMMENT_ID> [--json]")
}

func formatCommentForTable(body string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(body)), " ")
}

func wrapCommentForTable(body string, width int) string {
	return text.WrapSoft(formatCommentForTable(body), width)
}

func linkText(url, label string, enabled bool) string {
	if url == "" || label == "" || !enabled {
		return label
	}
	return text.Hyperlink(url, label)
}

func hyperlinkWrappedText(url, label string, width int, enabled bool) string {
	if label == "" {
		return ""
	}

	lines := strings.Split(wrapCommentForTable(label, width), "\n")
	for i, line := range lines {
		lines[i] = linkText(url, strings.TrimRight(line, " "), enabled)
	}
	return strings.Join(lines, "\n")
}

func commentColumnWidth(totalWidth int) int {
	const fixedColumns = 12 + 22 + 16 + 24 + 8 + 10
	const separatorsAndPadding = 36
	return max(totalWidth-fixedColumns-separatorsAndPadding, 24)
}

func wrapCellContent(value string, width int, soft bool) string {
	if width <= 0 || value == "" {
		return value
	}
	if soft {
		return text.WrapSoft(value, width)
	}
	return text.WrapText(value, width)
}

func stdoutWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 100
	}
	return width
}

func supportsHyperlinks() bool {
	switch strings.ToLower(os.Getenv("GH_PRV_FORCE_HYPERLINK")) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}

	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}

	return supportsHyperlinksForEnv(getenv())
}

func supportsHyperlinksForEnv(env map[string]string) bool {
	if env["TERM"] == "dumb" {
		return false
	}

	if env["WT_SESSION"] != "" || env["KITTY_WINDOW_ID"] != "" || env["WEZTERM_EXECUTABLE"] != "" || env["GHOSTTY_RESOURCES_DIR"] != "" {
		return true
	}

	if v := strings.ToLower(env["TERM_PROGRAM"]); v == "vscode" || v == "wezterm" || v == "ghostty" || v == "hyper" || v == "iterm.app" {
		return true
	}

	if env["VTE_VERSION"] != "" {
		return true
	}

	if strings.Contains(strings.ToLower(env["TERM"]), "xterm-kitty") {
		return true
	}

	return false
}

func getenv() map[string]string {
	return map[string]string{
		"TERM":                  os.Getenv("TERM"),
		"WT_SESSION":            os.Getenv("WT_SESSION"),
		"KITTY_WINDOW_ID":       os.Getenv("KITTY_WINDOW_ID"),
		"WEZTERM_EXECUTABLE":    os.Getenv("WEZTERM_EXECUTABLE"),
		"GHOSTTY_RESOURCES_DIR": os.Getenv("GHOSTTY_RESOURCES_DIR"),
		"TERM_PROGRAM":          os.Getenv("TERM_PROGRAM"),
		"VTE_VERSION":           os.Getenv("VTE_VERSION"),
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func isReviewCommentNodeID(v string) bool {
	return strings.HasPrefix(v, "PRRC_")
}

func writeCommentCache(pr prRef, comments []reviewComment) error {
	cachePath, err := commentCachePath()
	if err != nil {
		return err
	}

	items := make([]cachedCommentRef, 0, len(comments))
	for _, comment := range comments {
		items = append(items, cachedCommentRef{
			ID: comment.ID,
			PR: pr,
		})
	}

	data, err := json.MarshalIndent(cacheFile{Comments: items}, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cachePath, data, 0o600)
}

func lookupCachedPR(id int64) (prRef, error) {
	cachePath, err := commentCachePath()
	if err != nil {
		return prRef{}, err
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return prRef{}, errors.New("comment ID cache not found; run `gh prv <PR URL>` first or pass a PR URL")
		}
		return prRef{}, err
	}

	var cache cacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return prRef{}, fmt.Errorf("failed to read comment cache: %w", err)
	}

	for _, item := range cache.Comments {
		if item.ID == id {
			return item.PR, nil
		}
	}

	return prRef{}, errors.New("comment ID not found in cache; run `gh prv <PR URL>` for the relevant PR or pass a PR URL")
}

func commentCachePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	dir := filepath.Join(cacheDir, "gh-prv")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	return filepath.Join(dir, "comments.json"), nil
}
