// Package redact removes secrets before they can be stored.
//
// Redaction happens at EXTRACTION, not at publication. Nuggets are written to
// a store that may be synced, backed up, or committed to a repository, so a
// secret that reaches the store has already escaped. By the time a human is
// deciding whether to publish, it is too late.
//
// Placeholders are actionable rather than opaque: an artifact that says
// "<AWS_ACCOUNT_ID — ask operator>" is still a usable tutorial, whereas one
// full of "[REDACTED]" is not.
package redact

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Rule is one secret pattern and what to put in its place.
type Rule struct {
	Name        string
	Pattern     *regexp.Regexp
	Placeholder string
	// Group is the capture group to replace; 0 replaces the whole match.
	Group int
}

// Finding records one redaction, for reporting without echoing the secret.
type Finding struct {
	Rule  string `json:"rule"`
	Count int    `json:"count"`
}

// Result is the outcome of redacting a text.
type Result struct {
	Text     string    `json:"text"`
	Findings []Finding `json:"findings,omitempty"`
	Redacted bool      `json:"redacted"`
}

// rules are ordered most-specific first: a GitHub token should be recognised
// as a GitHub token before a generic high-entropy match claims it.
var rules = []Rule{
	{
		Name:        "github_token",
		Pattern:     regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,255})\b`),
		Placeholder: "<GITHUB_TOKEN — ask operator>",
	},
	{
		Name:        "github_fine_grained",
		Pattern:     regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,255}\b`),
		Placeholder: "<GITHUB_TOKEN — ask operator>",
	},
	{
		Name:        "aws_access_key",
		Pattern:     regexp.MustCompile(`\b((?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16})\b`),
		Placeholder: "<AWS_ACCESS_KEY_ID — ask operator>",
	},
	{
		Name:        "aws_account_id",
		Pattern:     regexp.MustCompile(`\barn:aws[a-z-]*:[a-z0-9-]+:[a-z0-9-]*:(\d{12}):`),
		Placeholder: "<AWS_ACCOUNT_ID>",
		Group:       1,
	},
	{
		Name:        "slack_token",
		Pattern:     regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
		Placeholder: "<SLACK_TOKEN — ask operator>",
	},
	{
		Name:        "openai_key",
		Pattern:     regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`),
		Placeholder: "<API_KEY — ask operator>",
	},
	{
		Name:        "anthropic_key",
		Pattern:     regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`),
		Placeholder: "<API_KEY — ask operator>",
	},
	{
		Name:        "google_api_key",
		Pattern:     regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
		Placeholder: "<GOOGLE_API_KEY — ask operator>",
	},
	{
		Name:        "private_key_block",
		Pattern:     regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
		Placeholder: "<PRIVATE_KEY — ask operator>",
	},
	{
		Name:        "jwt",
		Pattern:     regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
		Placeholder: "<JWT — ask operator>",
	},
	{
		Name:        "connection_string_password",
		Pattern:     regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+):([^\s@]{3,})@`),
		Placeholder: "<PASSWORD>",
		Group:       2,
	},
	{
		// Assignments like API_KEY="...", token: '...', secret=...
		Name:        "assigned_secret",
		Pattern:     regexp.MustCompile(`(?i)\b((?:api[_-]?key|secret|password|passwd|token|credential|auth)[a-z_]*)\s*[:=]\s*["']?([^\s"',;)]{8,})["']?`),
		Placeholder: "<SECRET — ask operator>",
		Group:       2,
	},
	{
		Name:        "bearer_token",
		Pattern:     regexp.MustCompile(`(?i)\b(?:bearer|authorization:\s*bearer)\s+([A-Za-z0-9._~+/=-]{16,})`),
		Placeholder: "<BEARER_TOKEN — ask operator>",
		Group:       1,
	},
	{
		Name:        "hetzner_or_cloudflare_token",
		Pattern:     regexp.MustCompile(`(?i)\b(?:hcloud|cloudflare|cf)[_-]?(?:api[_-]?)?token\s*[:=]\s*["']?([A-Za-z0-9_-]{20,})["']?`),
		Placeholder: "<API_TOKEN — ask operator>",
		Group:       1,
	},
}

// placeholderPattern recognises text that is already redacted, so repeated
// passes do not nest placeholders.
var placeholderPattern = regexp.MustCompile(`<[A-Z_]+(?: — ask operator)?>`)

// Text redacts secrets from a string.
func Text(s string) Result {
	res := Result{Text: s}
	counts := map[string]int{}

	for _, rule := range rules {
		rule := rule
		res.Text = rule.Pattern.ReplaceAllStringFunc(res.Text, func(match string) string {
			if placeholderPattern.MatchString(match) {
				return match
			}

			if rule.Group == 0 {
				counts[rule.Name]++
				return rule.Placeholder
			}

			groups := rule.Pattern.FindStringSubmatch(match)
			if len(groups) <= rule.Group || groups[rule.Group] == "" {
				return match
			}
			// Replace only the captured secret, keeping surrounding context
			// so the sentence still reads.
			counts[rule.Name]++
			return strings.Replace(match, groups[rule.Group], rule.Placeholder, 1)
		})
	}

	for name, n := range counts {
		res.Findings = append(res.Findings, Finding{Rule: name, Count: n})
	}
	sort.Slice(res.Findings, func(i, j int) bool { return res.Findings[i].Rule < res.Findings[j].Rule })
	res.Redacted = len(res.Findings) > 0
	return res
}

// Scan reports what would be redacted without modifying the text. Used to
// warn loudly before anything leaves the machine.
func Scan(s string) []Finding {
	return Text(s).Findings
}

// Summary renders findings for a human, never echoing the secret itself.
func Summary(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fs))
	total := 0
	for _, f := range fs {
		parts = append(parts, fmt.Sprintf("%s x%d", f.Rule, f.Count))
		total += f.Count
	}
	return fmt.Sprintf("%d secret(s) redacted: %s", total, strings.Join(parts, ", "))
}
