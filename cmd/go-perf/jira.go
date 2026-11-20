package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ticketModeAny        = "any"
	ticketModeRegression = "regression"
	ticketModeMax        = "max"

	labelPerfRegression = "perf-regression"
	labelPerfAny        = "perf-any"

	defaultJiraBaseURL = "https://binalyze.atlassian.net"
)

// jiraClient talks to Jira Cloud REST API v3 with basic auth.
type jiraClient struct {
	baseURL    string
	email      string
	token      string
	httpClient *http.Client
}

type jiraEnv struct {
	baseURL    string
	email      string
	token      string
	projectKey string
}

type jiraTicketInput struct {
	Mode       string
	Meta       *RunMetadata
	AI         *AIAnalysis
	History    *HistoryAnalysis
	Comparison *ComparisonResult
	BuildURL   string
	BuildID    string
	ReportPath string
	Now        time.Time
}

type jiraIssueRef struct {
	Key string `json:"key"`
}

// envOrEmpty returns getenv(key) trimmed, or "" when Azure DevOps left an
// unresolved $(VAR) macro in place because the pipeline variable was unset.
func envOrEmpty(getenv func(string) string, key string) string {
	v := strings.TrimSpace(getenv(key))
	if v == "" || v == "$("+key+")" {
		return ""
	}
	return v
}

// readJiraEnv loads credentials from the environment. JIRA_API_USER and
// JIRA_API_PASSWORD are accepted as aliases used by the Drone release job.
func readJiraEnv(getenv func(string) string) jiraEnv {
	base := strings.TrimRight(envOrEmpty(getenv, "JIRA_BASE_URL"), "/")
	if base == "" {
		base = defaultJiraBaseURL
	}
	email := envOrEmpty(getenv, "JIRA_EMAIL")
	if email == "" {
		email = envOrEmpty(getenv, "JIRA_API_USER")
	}
	token := envOrEmpty(getenv, "JIRA_API_TOKEN")
	if token == "" {
		token = envOrEmpty(getenv, "JIRA_API_PASSWORD")
	}
	return jiraEnv{
		baseURL:    base,
		email:      email,
		token:      token,
		projectKey: envOrEmpty(getenv, "JIRA_PROJECT_KEY"),
	}
}

func (e jiraEnv) configured() bool {
	return e.email != "" && e.token != "" && e.projectKey != ""
}

func newJiraClient(env jiraEnv, client *http.Client) *jiraClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &jiraClient{
		baseURL:    env.baseURL,
		email:      env.email,
		token:      env.token,
		httpClient: client,
	}
}

func normalizeTicketMode(mode string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ticketModeRegression:
		return ticketModeRegression, true
	case ticketModeAny, ticketModeMax:
		return strings.ToLower(strings.TrimSpace(mode)), true
	default:
		return "", false
	}
}

func productLabel(meta *RunMetadata) string {
	if meta == nil || meta.ProductName == "" {
		return "unknown"
	}
	return strings.ToLower(strings.TrimSpace(meta.ProductName))
}

func productDisplayName(meta *RunMetadata) string {
	if meta == nil || meta.ProductName == "" {
		return "Unknown"
	}
	return meta.ProductName
}

func highFindings(ai *AIAnalysis) []AIFinding {
	if ai == nil {
		return nil
	}
	var out []AIFinding
	for _, f := range ai.Findings {
		if strings.EqualFold(f.Severity, "high") {
			out = append(out, f)
		}
	}
	return out
}

func historyHasRegressed(history *HistoryAnalysis) bool {
	if history == nil {
		return false
	}
	for _, m := range history.Metrics {
		if m.Gates && m.Label == "regressed" {
			return true
		}
	}
	return false
}

// shouldFileTicket decides whether this run warrants a Jira ticket for the mode.
func shouldFileTicket(mode string, ai *AIAnalysis, history *HistoryAnalysis, comparison *ComparisonResult) bool {
	switch mode {
	case ticketModeAny:
		return true
	case ticketModeMax:
		return len(highFindings(ai)) > 0
	case ticketModeRegression:
		if ai != nil {
			return ai.Verdict == "regression"
		}
		if historyHasRegressed(history) {
			return true
		}
		return comparison != nil && comparison.BaselineAvailable && comparison.HasRegression
	default:
		return false
	}
}

func reportAttachmentName(meta *RunMetadata, reportPath string, now time.Time) string {
	stamp := now.UTC().Format("20060102T150405Z")
	product := productLabel(meta)
	base := filepath.Base(reportPath)
	switch base {
	case "", "report.html", "perf-report.html", "latest-report.html":
		return fmt.Sprintf("%s-%s-report.html", product, stamp)
	default:
		return base
	}
}

func plainToADF(text string) map[string]any {
	lines := strings.Split(text, "\n")
	content := make([]any, 0, len(lines))
	for _, line := range lines {
		nodeText := line
		if nodeText == "" {
			nodeText = " "
		}
		content = append(content, map[string]any{
			"type": "paragraph",
			"content": []any{
				map[string]any{"type": "text", "text": nodeText},
			},
		})
	}
	if len(content) == 0 {
		content = []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": " "},
				},
			},
		}
	}
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": content,
	}
}

func buildTicketBody(in jiraTicketInput) string {
	var b strings.Builder
	product := productDisplayName(in.Meta)
	if in.Mode == ticketModeAny {
		b.WriteString("Nothing notable was detected in this performance run.\n")
		b.WriteString("This ticket was filed because ticket-mode is any.\n\n")
	}

	if in.AI != nil && in.AI.Headline != "" {
		b.WriteString("Headline: ")
		b.WriteString(in.AI.Headline)
		b.WriteString("\n")
	} else if in.Mode != ticketModeAny {
		b.WriteString("Headline: Performance regression detected (AI analysis unavailable)\n")
	}

	b.WriteString("Product: ")
	b.WriteString(product)
	b.WriteString("\n")
	if in.Meta != nil {
		if in.Meta.ProductCommitID != "" {
			b.WriteString("Commit: ")
			b.WriteString(in.Meta.ProductCommitID)
			b.WriteString("\n")
		}
		if in.Meta.TestCase != "" {
			b.WriteString("Test case: ")
			b.WriteString(in.Meta.TestCase)
			b.WriteString("\n")
		}
	}
	if in.BuildID != "" {
		b.WriteString("Build ID: ")
		b.WriteString(in.BuildID)
		b.WriteString("\n")
	}
	if in.BuildURL != "" {
		b.WriteString("Build: ")
		b.WriteString(in.BuildURL)
		b.WriteString("\n")
	}
	b.WriteString("Mode: ")
	b.WriteString(in.Mode)
	b.WriteString("\n")

	findings := []AIFinding{}
	if in.AI != nil {
		if in.Mode == ticketModeMax {
			findings = highFindings(in.AI)
		} else if in.Mode == ticketModeRegression {
			findings = in.AI.Findings
		}
	}
	if len(findings) > 0 {
		b.WriteString("\nFindings:\n")
		for _, f := range findings {
			b.WriteString("- ")
			b.WriteString(f.Title)
			if f.Location.File != "" {
				b.WriteString(" (")
				b.WriteString(f.Location.File)
				if f.Location.Line > 0 {
					b.WriteString(fmt.Sprintf(":%d", f.Location.Line))
				}
				b.WriteString(")")
			}
			b.WriteString("\n")
			if f.Evidence != "" {
				b.WriteString("  Evidence: ")
				b.WriteString(f.Evidence)
				b.WriteString("\n")
			}
			if f.FixPrompt != "" {
				b.WriteString("  Fix prompt: ")
				b.WriteString(f.FixPrompt)
				b.WriteString("\n")
			}
		}
	}

	if in.History != nil {
		var regressed []string
		for _, m := range in.History.Metrics {
			if m.Gates && m.Label == "regressed" {
				regressed = append(regressed, fmt.Sprintf("%s (+%.1f%% vs median)", m.Name, m.ChangeVsMedianPercent))
			}
		}
		if len(regressed) > 0 {
			b.WriteString("\nRegressed gated metrics:\n")
			for _, line := range regressed {
				b.WriteString("- ")
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}

	if in.ReportPath == "" || !fileExists(in.ReportPath) {
		b.WriteString("\nNo HTML report was produced for this run.\n")
	}
	return b.String()
}

func buildTicketSummary(in jiraTicketInput) string {
	product := productDisplayName(in.Meta)
	if in.Mode == ticketModeAny {
		stamp := in.Now.UTC().Format("20060102T150405Z")
		return fmt.Sprintf("Perf report: %s %s", product, stamp)
	}
	return fmt.Sprintf("Perf regression: %s", product)
}

func (c *jiraClient) doJSON(method, path string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func (c *jiraClient) findOpenIssue(projectKey, product string) (string, error) {
	jql := fmt.Sprintf(
		`project = %s AND labels = %s AND labels = %s AND resolution IS EMPTY ORDER BY created DESC`,
		projectKey, labelPerfRegression, product,
	)
	payload := map[string]any{
		"jql":        jql,
		"fields":     []string{"key"},
		"maxResults": 1,
	}
	data, status, err := c.doJSON(http.MethodPost, "/rest/api/3/search/jql", payload)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("jira search returned HTTP %d", status)
	}
	var parsed struct {
		Issues []jiraIssueRef `json:"issues"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Issues) == 0 {
		return "", nil
	}
	return parsed.Issues[0].Key, nil
}

func (c *jiraClient) createIssue(projectKey, summary, description string, labels []string) (string, error) {
	payload := map[string]any{
		"fields": map[string]any{
			"project":     map[string]string{"key": projectKey},
			"summary":     summary,
			"description": plainToADF(description),
			"issuetype":   map[string]string{"name": "Bug"},
			"labels":      labels,
		},
	}
	data, status, err := c.doJSON(http.MethodPost, "/rest/api/3/issue", payload)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("jira create returned HTTP %d", status)
	}
	var created jiraIssueRef
	if err := json.Unmarshal(data, &created); err != nil {
		return "", err
	}
	if created.Key == "" {
		return "", fmt.Errorf("jira create returned no key")
	}
	return created.Key, nil
}

func (c *jiraClient) addComment(issueKey, text string) error {
	payload := map[string]any{
		"body": plainToADF(text),
	}
	_, status, err := c.doJSON(http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/comment", payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("jira comment returned HTTP %d", status)
	}
	return nil
}

func (c *jiraClient) listAttachments(issueKey string) ([]string, error) {
	data, status, err := c.doJSON(http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"?fields=attachment", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("jira get issue returned HTTP %d", status)
	}
	var parsed struct {
		Fields struct {
			Attachment []struct {
				Filename string `json:"filename"`
			} `json:"attachment"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parsed.Fields.Attachment))
	for _, a := range parsed.Fields.Attachment {
		names = append(names, a.Filename)
	}
	return names, nil
}

func (c *jiraClient) listCommentBodies(issueKey string) ([]string, error) {
	data, status, err := c.doJSON(http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/comment", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("jira list comments returned HTTP %d", status)
	}
	var parsed struct {
		Comments []struct {
			Body json.RawMessage `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(parsed.Comments))
	for _, comment := range parsed.Comments {
		out = append(out, string(comment.Body))
	}
	return out, nil
}

func (c *jiraClient) attachFile(issueKey, filename, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/rest/api/3/issue/"+url.PathEscape(issueKey)+"/attachments", &buf)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "no-check")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jira attach returned HTTP %d: %s", resp.StatusCode, truncateForLog(string(body), 200))
	}
	return nil
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func redactJiraSecrets(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[redacted]")
}

// maybeFileJiraTicket files or updates a Jira issue for the analyze run.
// Missing credentials and Jira HTTP errors are warnings; analyze still exits 0.
func maybeFileJiraTicket(in jiraTicketInput, getenv func(string) string, client *http.Client) {
	mode, ok := normalizeTicketMode(in.Mode)
	if !ok {
		fmt.Printf("[JIRA] WARNING: unknown ticket-mode %q; skipping Jira\n", in.Mode)
		return
	}
	in.Mode = mode
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}

	env := readJiraEnv(getenv)
	if !env.configured() {
		fmt.Printf("[JIRA] WARNING: Jira credentials or project key missing; skipping ticket\n")
		return
	}
	if !shouldFileTicket(mode, in.AI, in.History, in.Comparison) {
		fmt.Printf("[JIRA] ticket-mode=%s: no ticket for this run\n", mode)
		return
	}

	jc := newJiraClient(env, client)
	summary := buildTicketSummary(in)
	body := buildTicketBody(in)
	product := productLabel(in.Meta)

	var issueKey string
	var created bool

	if mode == ticketModeAny {
		key, err := jc.createIssue(env.projectKey, summary, body, []string{labelPerfAny, product})
		if err != nil {
			fmt.Printf("[JIRA] WARNING: create failed: %s\n", redactJiraSecrets(err.Error(), env.token))
			return
		}
		issueKey = key
		created = true
		fmt.Printf("[JIRA] created %s (mode=any)\n", issueKey)
	} else {
		existing, err := jc.findOpenIssue(env.projectKey, product)
		if err != nil {
			fmt.Printf("[JIRA] WARNING: search failed: %s\n", redactJiraSecrets(err.Error(), env.token))
			return
		}
		if existing == "" {
			key, err := jc.createIssue(env.projectKey, summary, body, []string{labelPerfRegression, product})
			if err != nil {
				fmt.Printf("[JIRA] WARNING: create failed: %s\n", redactJiraSecrets(err.Error(), env.token))
				return
			}
			issueKey = key
			created = true
			fmt.Printf("[JIRA] created %s (mode=%s)\n", issueKey, mode)
		} else {
			issueKey = existing
			skipComment := false
			if in.BuildID != "" {
				comments, err := jc.listCommentBodies(issueKey)
				if err != nil {
					fmt.Printf("[JIRA] WARNING: list comments failed: %s\n", redactJiraSecrets(err.Error(), env.token))
				} else {
					needle := "Build ID: " + in.BuildID
					for _, c := range comments {
						if strings.Contains(c, needle) {
							skipComment = true
							break
						}
					}
				}
			}
			if !skipComment {
				if err := jc.addComment(issueKey, body); err != nil {
					fmt.Printf("[JIRA] WARNING: comment failed: %s\n", redactJiraSecrets(err.Error(), env.token))
					return
				}
				fmt.Printf("[JIRA] commented on %s (mode=%s)\n", issueKey, mode)
			} else {
				fmt.Printf("[JIRA] skipped comment on %s; build %s already recorded\n", issueKey, in.BuildID)
			}
		}
	}

	if in.ReportPath != "" && fileExists(in.ReportPath) {
		filename := reportAttachmentName(in.Meta, in.ReportPath, in.Now)
		existingNames, err := jc.listAttachments(issueKey)
		if err != nil {
			fmt.Printf("[JIRA] WARNING: list attachments failed: %s\n", redactJiraSecrets(err.Error(), env.token))
		} else {
			already := false
			for _, name := range existingNames {
				if name == filename {
					already = true
					break
				}
			}
			if already {
				fmt.Printf("[JIRA] skipped attach; %s already on %s\n", filename, issueKey)
			} else if err := jc.attachFile(issueKey, filename, in.ReportPath); err != nil {
				fmt.Printf("[JIRA] WARNING: attach failed: %s\n", redactJiraSecrets(err.Error(), env.token))
			} else {
				fmt.Printf("[JIRA] attached %s to %s\n", filename, issueKey)
			}
		}
	} else if created {
		fmt.Printf("[JIRA] no report file to attach for %s\n", issueKey)
	}
}
