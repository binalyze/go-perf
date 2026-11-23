package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeTicketMode(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"", ticketModeRegression, true},
		{"regression", ticketModeRegression, true},
		{"ANY", ticketModeAny, true},
		{"max", ticketModeMax, true},
		{"nope", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizeTicketMode(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("normalizeTicketMode(%q)=%q,%v want %q,%v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestShouldFileTicket(t *testing.T) {
	okAI := &AIAnalysis{Verdict: "ok"}
	regAI := &AIAnalysis{Verdict: "regression"}
	warnAI := &AIAnalysis{Verdict: "warning"}
	highAI := &AIAnalysis{
		Verdict: "warning",
		Findings: []AIFinding{
			{ID: "1", Class: "regression", Severity: "high", Title: "hot"},
		},
	}
	mediumAI := &AIAnalysis{
		Verdict: "warning",
		Findings: []AIFinding{
			{ID: "1", Class: "improvement", Severity: "medium", Title: "better"},
		},
	}
	historyReg := &HistoryAnalysis{
		Metrics: []MetricStat{{Name: "duration_ms", Gates: true, Label: "regressed"}},
	}
	cmpReg := &ComparisonResult{BaselineAvailable: true, HasRegression: true}

	cases := []struct {
		name       string
		mode       string
		ai         *AIAnalysis
		history    *HistoryAnalysis
		comparison *ComparisonResult
		want       bool
	}{
		{"any always", ticketModeAny, okAI, nil, nil, true},
		{"regression ok", ticketModeRegression, okAI, nil, nil, false},
		{"regression warn", ticketModeRegression, warnAI, nil, nil, false},
		{"regression verdict", ticketModeRegression, regAI, nil, nil, true},
		{"regression history fallback", ticketModeRegression, nil, historyReg, nil, true},
		{"regression comparison fallback", ticketModeRegression, nil, nil, cmpReg, true},
		{"max medium only", ticketModeMax, mediumAI, nil, nil, false},
		{"max high", ticketModeMax, highAI, nil, nil, true},
		{"max no ai", ticketModeMax, nil, historyReg, cmpReg, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldFileTicket(tc.mode, tc.ai, tc.history, tc.comparison)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestReportAttachmentName(t *testing.T) {
	meta := &RunMetadata{ProductName: "Drone"}
	now := time.Date(2026, 11, 16, 10, 3, 0, 0, time.UTC)
	got := reportAttachmentName(meta, "perf-report.html", now)
	want := "drone-20261116T100300Z-report.html"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	custom := reportAttachmentName(meta, "/tmp/drone-custom-report.html", now)
	if custom != "drone-custom-report.html" {
		t.Fatalf("custom name = %q", custom)
	}
}

func TestReadJiraEnvAliases(t *testing.T) {
	env := map[string]string{
		"JIRA_API_USER":     "bot@example.com",
		"JIRA_API_PASSWORD": "secret-token",
		"JIRA_PROJECT_KEY":  "FAST",
	}
	got := readJiraEnv(func(k string) string { return env[k] })
	if !got.configured() {
		t.Fatal("expected configured")
	}
	if got.email != "bot@example.com" || got.token != "secret-token" || got.projectKey != "FAST" {
		t.Fatalf("unexpected env: %+v", got)
	}
	if got.baseURL != defaultJiraBaseURL {
		t.Fatalf("baseURL = %q", got.baseURL)
	}
}

func TestReadJiraEnvUnresolvedMacros(t *testing.T) {
	env := map[string]string{
		"JIRA_EMAIL":       "$(JIRA_EMAIL)",
		"JIRA_API_TOKEN":   "$(JIRA_API_TOKEN)",
		"JIRA_PROJECT_KEY": "$(JIRA_PROJECT_KEY)",
	}
	got := readJiraEnv(func(k string) string { return env[k] })
	if got.configured() {
		t.Fatalf("unresolved macros must count as missing: %+v", got)
	}
}

func TestMaybeFileJiraTicketMissingCredentials(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeAny,
		Meta: &RunMetadata{ProductName: "Drone"},
	}, func(string) string { return "" }, server.Client())
	if calls != 0 {
		t.Fatalf("expected no HTTP calls, got %d", calls)
	}
}

func TestMaybeFileJiraTicketRegressionOKNoCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	env := map[string]string{
		"JIRA_BASE_URL":    server.URL,
		"JIRA_EMAIL":       "bot@example.com",
		"JIRA_API_TOKEN":   "secret-token",
		"JIRA_PROJECT_KEY": "FAST",
	}
	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeRegression,
		Meta: &RunMetadata{ProductName: "Drone"},
		AI:   &AIAnalysis{Verdict: "ok"},
	}, func(k string) string { return env[k] }, server.Client())
	if calls != 0 {
		t.Fatalf("expected no HTTP calls for ok verdict, got %d", calls)
	}
}

func TestMaybeFileJiraTicketMaxMediumNoCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	env := map[string]string{
		"JIRA_BASE_URL":    server.URL,
		"JIRA_EMAIL":       "bot@example.com",
		"JIRA_API_TOKEN":   "secret-token",
		"JIRA_PROJECT_KEY": "FAST",
	}
	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeMax,
		Meta: &RunMetadata{ProductName: "Drone"},
		AI: &AIAnalysis{
			Verdict: "warning",
			Findings: []AIFinding{
				{ID: "1", Class: "improvement", Severity: "medium", Title: "better", Location: AILocation{File: "a.go"}},
			},
		},
	}, func(k string) string { return env[k] }, server.Client())
	if calls != 0 {
		t.Fatalf("expected no HTTP calls for medium-only max, got %d", calls)
	}
}

func TestMaybeFileJiraTicketRegressionCreateThenComment(t *testing.T) {
	var searchCount, createCount, commentCount, attachCount int
	openKey := ""
	attachments := map[string][]string{}
	comments := map[string][]string{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "bot@example.com" || pass != "secret-token" {
			t.Errorf("bad auth")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.Contains(pass, "secret") && strings.Contains(r.URL.RawQuery, "secret-token") {
			t.Errorf("token leaked in URL")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
			searchCount++
			issues := []any{}
			if openKey != "" {
				issues = append(issues, map[string]string{"key": openKey})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": issues})
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue":
			createCount++
			var body struct {
				Fields struct {
					Summary string   `json:"summary"`
					Labels  []string `json:"labels"`
				} `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create: %v", err)
			}
			if body.Fields.Summary != "Perf regression: Drone" {
				t.Errorf("summary = %q", body.Fields.Summary)
			}
			if !containsAll(body.Fields.Labels, labelPerfRegression, "drone") {
				t.Errorf("labels = %v", body.Fields.Labels)
			}
			openKey = "FAST-100"
			_ = json.NewEncoder(w).Encode(map[string]string{"key": openKey})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comment"):
			commentCount++
			data, _ := io.ReadAll(r.Body)
			comments[openKey] = append(comments[openKey], string(data))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comment"):
			list := []any{}
			for _, c := range comments[openKey] {
				list = append(list, map[string]any{"body": json.RawMessage(c)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"comments": list})
		case r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "fields=attachment"):
			atts := []any{}
			for _, name := range attachments[openKey] {
				atts = append(atts, map[string]string{"filename": name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"fields": map[string]any{"attachment": atts},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attachments"):
			attachCount++
			if r.Header.Get("X-Atlassian-Token") != "no-check" {
				t.Errorf("missing X-Atlassian-Token")
			}
			attachments[openKey] = append(attachments[openKey], "drone-20261116T100300Z-report.html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	reportDir := t.TempDir()
	reportPath := filepath.Join(reportDir, "perf-report.html")
	if err := os.WriteFile(reportPath, []byte("<html>ok</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"JIRA_BASE_URL":    server.URL,
		"JIRA_EMAIL":       "bot@example.com",
		"JIRA_API_TOKEN":   "secret-token",
		"JIRA_PROJECT_KEY": "FAST",
	}
	getenv := func(k string) string { return env[k] }
	now := time.Date(2026, 11, 16, 10, 3, 0, 0, time.UTC)
	ai := &AIAnalysis{
		Verdict:  "regression",
		Headline: "slower",
		Findings: []AIFinding{
			{ID: "1", Class: "regression", Severity: "high", Title: "allocs", Location: AILocation{File: "x.go"}, FixPrompt: "fix it"},
		},
	}
	meta := &RunMetadata{ProductName: "Drone", ProductCommitID: "abc123"}

	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeRegression, Meta: meta, AI: ai, BuildID: "42",
		BuildURL: "https://example/build/42", ReportPath: reportPath, Now: now,
	}, getenv, server.Client())
	if createCount != 1 || searchCount != 1 || attachCount != 1 {
		t.Fatalf("first run: search=%d create=%d attach=%d", searchCount, createCount, attachCount)
	}

	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeRegression, Meta: meta, AI: ai, BuildID: "43",
		BuildURL: "https://example/build/43", ReportPath: reportPath, Now: now,
	}, getenv, server.Client())
	if createCount != 1 || commentCount != 1 {
		t.Fatalf("second run: create=%d comment=%d", createCount, commentCount)
	}

	// Same build id should skip comment.
	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeRegression, Meta: meta, AI: ai, BuildID: "43",
		BuildURL: "https://example/build/43", ReportPath: reportPath, Now: now,
	}, getenv, server.Client())
	if commentCount != 1 {
		t.Fatalf("expected comment skip, commentCount=%d", commentCount)
	}
}

func TestMaybeFileJiraTicketMaxHighCreates(t *testing.T) {
	var createBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue":
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			_ = json.NewEncoder(w).Encode(map[string]string{"key": "FAST-200"})
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"fields": map[string]any{"attachment": []any{}}})
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	env := map[string]string{
		"JIRA_BASE_URL":    server.URL,
		"JIRA_EMAIL":       "bot@example.com",
		"JIRA_API_TOKEN":   "secret-token",
		"JIRA_PROJECT_KEY": "FAST",
	}
	ai := &AIAnalysis{
		Verdict: "warning",
		Findings: []AIFinding{
			{ID: "1", Class: "regression", Severity: "high", Title: "critical hot", Location: AILocation{File: "hot.go"}},
			{ID: "2", Class: "improvement", Severity: "medium", Title: "medium only", Location: AILocation{File: "m.go"}},
		},
	}
	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeMax,
		Meta: &RunMetadata{ProductName: "Galileo"},
		AI:   ai,
	}, func(k string) string { return env[k] }, server.Client())

	desc, _ := json.Marshal(createBody["fields"].(map[string]any)["description"])
	if !strings.Contains(string(desc), "critical hot") {
		t.Fatalf("description missing high finding: %s", desc)
	}
	if strings.Contains(string(desc), "medium only") {
		t.Fatalf("description should not include medium finding: %s", desc)
	}
}

func TestMaybeFileJiraTicketAnyAlwaysCreates(t *testing.T) {
	var createCount, searchCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
			searchCount++
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": []any{
				map[string]string{"key": "FAST-1"},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue":
			createCount++
			var body struct {
				Fields struct {
					Summary string   `json:"summary"`
					Labels  []string `json:"labels"`
				} `json:"fields"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !strings.HasPrefix(body.Fields.Summary, "Perf report: Drone ") {
				t.Errorf("summary = %q", body.Fields.Summary)
			}
			if !containsAll(body.Fields.Labels, labelPerfAny, "drone") {
				t.Errorf("labels = %v", body.Fields.Labels)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"key": "FAST-300"})
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"fields": map[string]any{"attachment": []any{}}})
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	env := map[string]string{
		"JIRA_BASE_URL":    server.URL,
		"JIRA_EMAIL":       "bot@example.com",
		"JIRA_API_TOKEN":   "secret-token",
		"JIRA_PROJECT_KEY": "FAST",
	}
	now := time.Date(2026, 11, 16, 10, 3, 0, 0, time.UTC)
	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeAny,
		Meta: &RunMetadata{ProductName: "Drone"},
		AI:   &AIAnalysis{Verdict: "ok"},
		Now:  now,
	}, func(k string) string { return env[k] }, server.Client())
	if createCount != 1 {
		t.Fatalf("createCount=%d", createCount)
	}
	if searchCount != 0 {
		t.Fatalf("any must not search, searchCount=%d", searchCount)
	}

	// Second any still creates even when an unresolved issue exists.
	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeAny,
		Meta: &RunMetadata{ProductName: "Drone"},
		AI:   &AIAnalysis{Verdict: "ok"},
		Now:  now,
	}, func(k string) string { return env[k] }, server.Client())
	if createCount != 2 {
		t.Fatalf("expected second create, createCount=%d", createCount)
	}
	if searchCount != 0 {
		t.Fatalf("any must not search on second run, searchCount=%d", searchCount)
	}
}

func TestMaybeFileJiraTicketHTTPErrorIsWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()

	env := map[string]string{
		"JIRA_BASE_URL":    server.URL,
		"JIRA_EMAIL":       "bot@example.com",
		"JIRA_API_TOKEN":   "secret-token",
		"JIRA_PROJECT_KEY": "FAST",
	}
	// Must not panic; analyze treats this as a warning.
	maybeFileJiraTicket(jiraTicketInput{
		Mode: ticketModeAny,
		Meta: &RunMetadata{ProductName: "Drone"},
	}, func(k string) string { return env[k] }, server.Client())
}

func TestJiraErrorDetail(t *testing.T) {
	body := []byte(`{"errorMessages":["Issue type is required"],"errors":{"issuetype":"Specify a valid issue type","customfield_10490":"Product-Feature is required."}}`)
	got := jiraErrorDetail(body)
	for _, want := range []string{"Issue type is required", "issuetype: Specify a valid issue type", "customfield_10490: Product-Feature is required."} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail %q missing %q", got, want)
		}
	}
}

func TestCreateIssueIncludesJiraFieldError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errorMessages":[],"errors":{"issuetype":"Specify a valid issue type"}}`))
	}))
	defer server.Close()

	client := newJiraClient(jiraEnv{
		baseURL: server.URL,
		email:   "bot@example.com",
		token:   "secret-token",
	}, server.Client())
	_, err := client.createIssue("FAST", "Perf report: Drone", "nothing notable", []string{"perf-any", "drone"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "issuetype: Specify a valid issue type") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("token leaked into error: %v", err)
	}
}

func TestRedactJiraSecrets(t *testing.T) {
	got := redactJiraSecrets("token secret-token leaked", "secret-token")
	if strings.Contains(got, "secret-token") {
		t.Fatalf("token not redacted: %q", got)
	}
}

func containsAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
