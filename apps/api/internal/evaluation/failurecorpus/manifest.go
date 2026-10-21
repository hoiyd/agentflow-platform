// Package failurecorpus binds reviewed historical failures to existing owned
// regression tests. It neither executes tests nor scores Agent answers.
package failurecorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	CaseSchema   = "failure-case-v1"
	CorpusSchema = "failure-corpus-v1"
	maxManifest  = 1 << 20
)

var (
	identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	revision   = regexp.MustCompile(`^[a-f0-9]{40}$`)
	digest     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	goPackage  = regexp.MustCompile(`^\./(app|internal)(/[a-z][a-z0-9_]*)*$`)
	goTest     = regexp.MustCompile(`^Test[A-Z][A-Za-z0-9_]*$`)
)

type Evidence struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

type Regression struct {
	Package string `json:"package"`
	Test    string `json:"test"`
}

type Review struct {
	Status     string    `json:"status"`
	Reviewer   string    `json:"reviewer"`
	ReviewedAt time.Time `json:"reviewed_at"`
	Redaction  string    `json:"redaction"`
	Notes      string    `json:"notes"`
}

type Case struct {
	SchemaVersion   string     `json:"schema_version"`
	ID              string     `json:"id"`
	Owner           string     `json:"owner"`
	FailureClass    string     `json:"failure_class"`
	SourceRef       string     `json:"source_ref"`
	SourceRevision  string     `json:"source_revision"`
	Evidence        []Evidence `json:"evidence"`
	InputBoundary   string     `json:"input_boundary"`
	ExpectedOutcome string     `json:"expected_outcome"`
	Regression      Regression `json:"regression"`
	Review          Review     `json:"review"`
}

type Corpus struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Version       string `json:"version"`
	Cases         []Case `json:"cases"`
}

func DecodeCase(reader io.Reader) (Case, error) {
	var item Case
	err := decode(reader, &item)
	return item, err
}

func DecodeCorpus(reader io.Reader) (Corpus, error) {
	var corpus Corpus
	if err := decode(reader, &corpus); err != nil {
		return corpus, err
	}
	if corpus.SchemaVersion != CorpusSchema || !identifier.MatchString(corpus.ID) || !identifier.MatchString(corpus.Version) {
		return corpus, errors.New("failure corpus requires the current schema and identity")
	}
	seen := map[string]bool{}
	for _, item := range corpus.Cases {
		if err := validate(item, true); err != nil {
			return corpus, err
		}
		if seen[item.ID] {
			return corpus, errors.New("duplicate failure case ID")
		}
		seen[item.ID] = true
	}
	return corpus, nil
}

// Promote returns a new corpus; the caller reviews/publishes the JSON through
// Git. A repeated ID is a no-op only when its entire reviewed content agrees.
func Promote(corpus Corpus, item Case) (Corpus, error) {
	if err := validate(item, true); err != nil {
		return corpus, err
	}
	for _, existing := range corpus.Cases {
		if existing.ID == item.ID {
			if hash(existing) != hash(item) {
				return corpus, errors.New("failure ID already has different reviewed content; use a new ID")
			}
			return canonical(corpus), nil
		}
	}
	corpus.Cases = append(slices.Clone(corpus.Cases), item)
	return canonical(corpus), nil
}

func validate(item Case, approved bool) error {
	if item.SchemaVersion != CaseSchema || !identifier.MatchString(item.ID) {
		return errors.New("failure case requires the current schema and a stable ID")
	}
	switch item.Owner {
	case "provider", "tool", "authorization", "rag", "context", "routing", "recovery":
	default:
		return errors.New("failure case owner is unknown")
	}
	if !revision.MatchString(item.SourceRevision) || !safeReference(item.SourceRef) || len(item.Evidence) == 0 || len(item.Evidence) > 10 {
		return errors.New("failure case requires a full source revision, source reference and retained evidence")
	}
	for _, evidence := range item.Evidence {
		if !slices.Contains([]string{"replay", "evaluation", "code_revision"}, evidence.Kind) || !safeReference(evidence.Ref) || !digest.MatchString(evidence.SHA256) {
			return errors.New("failure evidence requires a supported kind, safe reference and SHA-256")
		}
	}
	if !approved {
		return nil
	}
	if !identifier.MatchString(item.FailureClass) || !text(item.InputBoundary) || !text(item.ExpectedOutcome) ||
		!goPackage.MatchString(item.Regression.Package) || !goTest.MatchString(item.Regression.Test) {
		return errors.New("promotion requires a failure class, input boundary, expected outcome and exact Go regression target")
	}
	if item.Review.Status != "approved" || item.Review.Redaction != "confirmed" || !text(item.Review.Reviewer) ||
		!text(item.Review.Notes) || item.Review.ReviewedAt.IsZero() || item.Review.ReviewedAt.After(time.Now().Add(time.Minute)) {
		return errors.New("promotion requires explicit human approval, reviewer, date, notes and confirmed redaction")
	}
	return nil
}

func text(value string) bool { return strings.TrimSpace(value) != "" && len(value) <= 2048 }

func safeReference(value string) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func canonical(corpus Corpus) Corpus {
	corpus.Cases = slices.Clone(corpus.Cases)
	slices.SortFunc(corpus.Cases, func(a, b Case) int { return strings.Compare(a.ID, b.ID) })
	return corpus
}

func hash(value any) string {
	content, _ := json.Marshal(value)
	return checksum(content)
}

func checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func decode(reader io.Reader, target any) error {
	content, err := io.ReadAll(io.LimitReader(reader, maxManifest+1))
	if err != nil || len(content) > maxManifest {
		return errors.New("failure manifest cannot be read or exceeds 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("failure manifest contains invalid JSON or unsupported fields")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("failure manifest must contain exactly one JSON value")
	}
	return nil
}
