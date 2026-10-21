package main

import (
	"fmt"
	"html/template"
	"net/url"
	"strings"
)

// maxPromptLinkLen is the practical URL length for editor deeplinks.
const maxPromptLinkLen = 8000

// FindingView is one audit finding plus the links the report can render.
// Claude Code's editor extension has no documented URI parameter for a
// prompt, so that action copies the prompt instead of opening a link.
type FindingView struct {
	AIFinding
	Number        int
	CursorURL     template.URL
	CursorWebURL  template.URL
	PromptTooLong bool
}

func findingViews(analysis *AIAnalysis) []FindingView {
	if analysis == nil || len(analysis.Findings) == 0 {
		return nil
	}
	views := make([]FindingView, 0, len(analysis.Findings))
	for i, finding := range analysis.Findings {
		view := FindingView{AIFinding: finding, Number: i + 1}
		appURL, webURL, ok := cursorPromptLinks(finding.FixPrompt)
		if ok {
			view.CursorURL = template.URL(appURL)
			view.CursorWebURL = template.URL(webURL)
		} else if finding.FixPrompt != "" {
			view.PromptTooLong = true
		}
		views = append(views, view)
	}
	return views
}

func cursorPromptLinks(prompt string) (appURL, webURL string, ok bool) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", "", false
	}
	encoded := url.QueryEscape(prompt)
	appURL = "cursor://anysphere.cursor-deeplink/prompt?text=" + encoded
	webURL = "https://cursor.com/link/prompt?text=" + encoded
	if len(appURL) > maxPromptLinkLen {
		return "", "", false
	}
	return appURL, webURL, true
}

func findingIDFor(findings []FindingView, function string) string {
	function = strings.TrimSpace(function)
	if function == "" {
		return ""
	}
	for _, finding := range findings {
		name := finding.Location.Function
		if name == "" {
			continue
		}
		if name == function || strings.Contains(function, name) || strings.Contains(name, function) {
			return finding.ID
		}
	}
	return ""
}

func metricLabelClass(label string) string {
	switch label {
	case "regressed":
		return "badge badge-fail"
	case "creeping", "noisy":
		return "badge badge-warn"
	case "improved":
		return "badge badge-ok"
	default:
		return "badge"
	}
}

// sparklineSVG draws history values plus the current value. The coordinates
// come from numeric series, not from report text.
func sparklineSVG(stat MetricStat) template.HTML {
	values := append(append([]float64{}, stat.History...), stat.Current)
	if len(values) < 2 {
		return ""
	}
	lo, hi := values[0], values[0]
	for _, v := range values[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	span := hi - lo
	if span == 0 {
		span = 1
	}
	const width, height = 120.0, 28.0
	var b strings.Builder
	b.WriteString(`<svg class="spark" viewBox="0 0 120 28" width="120" height="28" aria-hidden="true"><polyline fill="none" stroke="currentColor" stroke-width="1.5" points="`)
	for i, v := range values {
		x := 2 + float64(i)/float64(len(values)-1)*(width-4)
		y := 2 + (height-4)*(1-(v-lo)/span)
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", x, y)
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String())
}
