package verification

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

const defaultArtifactBytes = 64 * 1024

type Options struct {
	WorkspaceRoot    string
	AllowedCommands  []string
	AllowedHTTPHosts []string
	HTTPClient       *http.Client
	MaxArtifactBytes int
}

type Subject struct {
	Type  string
	Value string
	Hash  string
}

type Result struct {
	Status      domain.VerificationStatus
	Summary     string
	ExitCode    *int
	Output      string
	OutputBytes int
	Truncated   bool
}

type Verifier interface {
	Type() domain.VerifierType
	Version() string
	Verify(context.Context, domain.VerifierSpec, Subject) Result
}

type Registry struct {
	verifiers        map[domain.VerifierType]Verifier
	maxArtifactBytes int
}

func NewRegistry(options Options) *Registry {
	if options.MaxArtifactBytes <= 0 {
		options.MaxArtifactBytes = defaultArtifactBytes
	}
	items := []Verifier{
		newCommandVerifier(options.WorkspaceRoot, options.AllowedCommands, options.MaxArtifactBytes),
		newHTTPVerifier(options.HTTPClient, options.AllowedHTTPHosts, options.MaxArtifactBytes),
		jsonSchemaVerifier{},
	}
	registry := &Registry{verifiers: make(map[domain.VerifierType]Verifier, len(items)), maxArtifactBytes: options.MaxArtifactBytes}
	for _, item := range items {
		registry.verifiers[item.Type()] = item
	}
	return registry
}

func (r *Registry) Resolve(verifierType domain.VerifierType) (Verifier, bool) {
	if r == nil {
		return nil, false
	}
	item, ok := r.verifiers[verifierType]
	return item, ok
}

func (r *Registry) Types() []domain.VerifierType {
	items := make([]domain.VerifierType, 0, len(r.verifiers))
	for item := range r.verifiers {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
	return items
}

type ErrorKind string

const (
	ErrorInvalidContract ErrorKind = "invalid_contract"
	ErrorUnavailable     ErrorKind = "unavailable"
	ErrorTimedOut        ErrorKind = "timed_out"
	ErrorExecution       ErrorKind = "execution"
)

type VerificationError struct {
	Kind       ErrorKind
	VerifierID string
	Message    string
	Cause      error
}

func (e *VerificationError) Error() string {
	if e == nil {
		return ""
	}
	if e.VerifierID == "" {
		return e.Message
	}
	return fmt.Sprintf("verifier %s: %s", e.VerifierID, e.Message)
}

func (e *VerificationError) Unwrap() error { return e.Cause }

func IsKind(err error, kind ErrorKind) bool {
	var target *VerificationError
	return errors.As(err, &target) && target.Kind == kind
}

func blocked(summary string) Result {
	return Result{Status: domain.VerificationBlocked, Summary: strings.TrimSpace(summary), Output: strings.TrimSpace(summary), OutputBytes: len(summary)}
}
