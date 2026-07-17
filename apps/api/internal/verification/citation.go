package verification

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type CitationConfig struct {
	MinCitations   int      `json:"min_citations,omitempty"`
	MinUniqueHosts int      `json:"min_unique_hosts,omitempty"`
	RequireHTTPS   bool     `json:"require_https,omitempty"`
	AllowedHosts   []string `json:"allowed_hosts,omitempty"`
	BlockedHosts   []string `json:"blocked_hosts,omitempty"`
}

type citationVerifier struct{}

func (citationVerifier) Type() domain.VerifierType { return domain.VerifierCitation }
func (citationVerifier) Version() string           { return "citation-v1" }

func (citationVerifier) NormalizeConfig(spec *domain.VerifierSpec) error {
	config, err := decodeConfig[CitationConfig](spec)
	if err != nil {
		return err
	}
	if config.MinCitations < 0 || config.MinUniqueHosts < 0 {
		return invalidContract("citation verifier " + spec.ID + " minimums must be non-negative")
	}
	if config.MinCitations == 0 {
		config.MinCitations = 1
	}
	config.AllowedHosts, err = normalizedHosts(config.AllowedHosts)
	if err != nil {
		return invalidContract("citation verifier " + spec.ID + " has invalid allowed_hosts: " + err.Error())
	}
	config.BlockedHosts, err = normalizedHosts(config.BlockedHosts)
	if err != nil {
		return invalidContract("citation verifier " + spec.ID + " has invalid blocked_hosts: " + err.Error())
	}
	for _, host := range config.AllowedHosts {
		if containsString(config.BlockedHosts, host) {
			return invalidContract("citation verifier " + spec.ID + " host is both allowed and blocked: " + host)
		}
	}
	return freezeConfig(spec, config)
}

func (citationVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, subject Subject) Result {
	config, err := decodeConfig[CitationConfig](&spec)
	if err != nil {
		return blocked("citation config is invalid")
	}
	if err := ctx.Err(); err != nil {
		return blocked("citation verification was canceled")
	}

	citations := markdownCitations(subject.Value)
	unique := make(map[string]string, len(citations))
	violations := []string{}
	hosts := make(map[string]bool)
	for _, raw := range citations {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			violations = append(violations, "invalid external citation URL: "+raw)
			continue
		}
		parsed.Fragment = ""
		normalizedURL := parsed.String()
		if _, exists := unique[normalizedURL]; exists {
			continue
		}
		unique[normalizedURL] = raw
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		hosts[host] = true
		if config.RequireHTTPS && parsed.Scheme != "https" {
			violations = append(violations, "citation must use HTTPS: "+raw)
		}
		if len(config.AllowedHosts) > 0 && !hostMatchesAny(host, config.AllowedHosts) {
			violations = append(violations, "citation host is not allowed: "+host)
		}
		if hostMatchesAny(host, config.BlockedHosts) {
			violations = append(violations, "citation host is blocked: "+host)
		}
	}

	uniqueURLs := make([]string, 0, len(unique))
	for normalizedURL := range unique {
		uniqueURLs = append(uniqueURLs, normalizedURL)
	}
	sort.Strings(uniqueURLs)
	uniqueHosts := make([]string, 0, len(hosts))
	for host := range hosts {
		uniqueHosts = append(uniqueHosts, host)
	}
	sort.Strings(uniqueHosts)
	if len(uniqueURLs) < config.MinCitations {
		violations = append(violations, fmt.Sprintf("citation count %d is below minimum %d", len(uniqueURLs), config.MinCitations))
	}
	if len(uniqueHosts) < config.MinUniqueHosts {
		violations = append(violations, fmt.Sprintf("unique host count %d is below minimum %d", len(uniqueHosts), config.MinUniqueHosts))
	}

	details := map[string]any{
		"citation_count":    len(uniqueURLs),
		"unique_host_count": len(uniqueHosts),
		"citations":         uniqueURLs,
		"hosts":             uniqueHosts,
		"violations":        violations,
	}
	status := domain.VerificationPassed
	summary := fmt.Sprintf("citation policy passed with %d citation(s)", len(uniqueURLs))
	if len(violations) > 0 {
		status = domain.VerificationFailed
		summary = fmt.Sprintf("citation policy failed with %d violation(s)", len(violations))
	}
	return Result{Status: status, Summary: summary, Details: details, Artifacts: []Artifact{diagnosticArtifact(details)}}
}

func markdownCitations(value string) []string {
	source := []byte(value)
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	citations := []string{}
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch item := node.(type) {
		case *ast.Link:
			if destination := strings.TrimSpace(string(item.Destination)); isExternalCitation(destination) {
				citations = append(citations, destination)
			}
		case *ast.AutoLink:
			if destination := strings.TrimSpace(string(item.URL(source))); isExternalCitation(destination) {
				citations = append(citations, destination)
			}
		}
		return ast.WalkContinue, nil
	})
	return citations
}

func isExternalCitation(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func normalizedHosts(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if host == "" || strings.ContainsAny(host, "/:@?#") {
			return nil, fmt.Errorf("%q must be a hostname without scheme, port, or path", value)
		}
		if !seen[host] {
			seen[host] = true
			result = append(result, host)
		}
	}
	sort.Strings(result)
	return result, nil
}

func hostMatchesAny(host string, rules []string) bool {
	for _, rule := range rules {
		if host == rule || strings.HasSuffix(host, "."+rule) {
			return true
		}
	}
	return false
}
