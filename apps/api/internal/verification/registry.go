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

// Artifact is verifier output that can be persisted independently from the
// compact evidence summary. Verifiers may return zero or multiple artifacts.
type Artifact struct {
	Kind        string
	MediaType   string
	Content     string
	ContentHash string
	ByteSize    int
	Truncated   bool
}

type Result struct {
	Status    domain.VerificationStatus
	Summary   string
	ExitCode  *int
	Details   map[string]any
	Artifacts []Artifact
}

type Verifier interface {
	Type() domain.VerifierType
	Version() string
	// NormalizeConfig owns verifier-specific defaults and validation. The normalized
	// config is frozen into the CompletionContract before a Run starts.
	NormalizeConfig(*domain.VerifierSpec) error
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
		textConstraintsVerifier{},
		citationVerifier{},
	}
	registry := &Registry{verifiers: make(map[domain.VerifierType]Verifier, len(items)), maxArtifactBytes: options.MaxArtifactBytes}
	for _, item := range items {
		if err := registry.Register(item); err != nil {
			panic(err)
		}
	}
	return registry
}

// Register adds a verifier implementation without requiring changes to the
// contract, engine, or domain model. Registration is expected during startup,
// before the Registry is shared with an Engine.
func (r *Registry) Register(verifier Verifier) error {
	if r == nil {
		return errors.New("verification registry is nil")
	}
	if verifier == nil {
		return errors.New("verifier is nil")
	}
	verifierType := verifier.Type()
	if strings.TrimSpace(string(verifierType)) == "" {
		return errors.New("verifier type is required")
	}
	if strings.TrimSpace(verifier.Version()) == "" {
		return fmt.Errorf("verifier %s version is required", verifierType)
	}
	if r.verifiers == nil {
		r.verifiers = make(map[domain.VerifierType]Verifier)
	}
	if _, exists := r.verifiers[verifierType]; exists {
		return fmt.Errorf("verifier type %s is already registered", verifierType)
	}
	r.verifiers[verifierType] = verifier
	return nil
}

func (r *Registry) Resolve(verifierType domain.VerifierType) (Verifier, bool) {
	if r == nil {
		return nil, false
	}
	item, ok := r.verifiers[verifierType]
	return item, ok
}

func (r *Registry) Types() []domain.VerifierType {
	if r == nil {
		return nil
	}
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
	summary = strings.TrimSpace(summary)
	return Result{
		Status:  domain.VerificationBlocked,
		Summary: summary,
		Artifacts: []Artifact{{
			Kind: "diagnostic", MediaType: "text/plain; charset=utf-8",
			Content: summary, ByteSize: len(summary),
		}},
	}
}
