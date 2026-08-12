// Package failure defines the common, transport-neutral failure contract used
// by runtime subsystems and observability projections.
package failure

import (
	"context"
	"errors"
	"strings"
)

type Category string

const (
	CategoryCanceled       Category = "canceled"
	CategoryTimeout        Category = "timeout"
	CategoryAvailability   Category = "availability"
	CategoryAuthentication Category = "authentication"
	CategoryQuota          Category = "quota"
	CategoryValidation     Category = "validation"
	CategoryNotFound       Category = "not_found"
	CategoryCapacity       Category = "capacity"
	CategoryExecution      Category = "execution"
	CategoryInternal       Category = "internal"
)

const (
	CodeCanceled     = "canceled"
	CodeTimeout      = "timeout"
	CodeUnclassified = "unclassified"
)

// Info is the stable cross-subsystem projection of a failure. Details must be
// safe for traces: credentials, request bodies, and raw provider responses do
// not belong here.
type Info struct {
	Code      string
	Source    string
	Category  Category
	Retryable bool
	Operation string
	Details   map[string]any
}

// Classified is implemented by subsystem errors without replacing their
// existing concrete types or error strings.
type Classified interface {
	error
	FailureInfo() Info
}

type staticError struct {
	message string
	info    Info
}

func (e *staticError) Error() string     { return e.message }
func (e *staticError) FailureInfo() Info { return e.info }

// New creates a stable sentinel that supports both errors.Is identity checks
// and the common failure contract.
func New(code, source string, category Category, retryable bool, message string) error {
	return &staticError{
		message: message,
		info:    Info{Code: code, Source: source, Category: category, Retryable: retryable},
	}
}

// Describe extracts structured failure information through wrapped errors and
// provides deterministic classifications for context and unknown errors.
func Describe(err error) Info {
	if err == nil {
		return Info{}
	}
	var classified Classified
	if errors.As(err, &classified) {
		return normalize(classified.FailureInfo())
	}
	if errors.Is(err, context.Canceled) {
		return Info{Code: CodeCanceled, Source: "context", Category: CategoryCanceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Info{Code: CodeTimeout, Source: "context", Category: CategoryTimeout, Retryable: true}
	}
	return Info{Code: CodeUnclassified, Source: "application", Category: CategoryInternal}
}

// Fields returns the canonical event payload fields for err.
func Fields(err error) map[string]any {
	if err == nil {
		return nil
	}
	info := Describe(err)
	fields := map[string]any{
		"error_kind":     info.Code,
		"error_source":   info.Source,
		"error_category": string(info.Category),
		"retryable":      info.Retryable,
	}
	if info.Operation != "" {
		fields["operation"] = info.Operation
	}
	for key, value := range info.Details {
		if _, reserved := fields[key]; !reserved {
			fields[key] = value
		}
	}
	return fields
}

// Merge returns a copy of payload enriched with the canonical fields for err.
func Merge(payload map[string]any, err error) map[string]any {
	result := make(map[string]any, len(payload)+5)
	for key, value := range payload {
		result[key] = value
	}
	for key, value := range Fields(err) {
		result[key] = value
	}
	return result
}

func normalize(info Info) Info {
	info.Code = strings.TrimSpace(info.Code)
	info.Source = strings.TrimSpace(info.Source)
	info.Operation = strings.TrimSpace(info.Operation)
	if info.Code == "" {
		info.Code = CodeUnclassified
	}
	if info.Source == "" {
		info.Source = "application"
	}
	if info.Category == "" {
		info.Category = CategoryInternal
	}
	if len(info.Details) > 0 {
		copy := make(map[string]any, len(info.Details))
		for key, value := range info.Details {
			copy[key] = value
		}
		info.Details = copy
	}
	return info
}
