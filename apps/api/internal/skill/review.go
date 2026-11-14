package skill

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A manifest authorizes canonical runtime content, not every ignored file in an
// installation, a repository revision, or the safety of its natural language.
type reviewManifest struct {
	SchemaVersion string `json:"schema_version"`
	Approvals     []struct {
		Name string `json:"name"`
		Hash string `json:"hash"`
	} `json:"approvals"`
}

// LoadReviewedRoots optionally gates new Skill snapshots on operator-approved
// content hashes. Guards protect operator configuration from model-writable roots.
func LoadReviewedRoots(roots []string, manifest string, guards ...func(string) error) (*Catalog, error) {
	catalog, err := LoadRoots(roots)
	if err != nil || manifest == "" {
		return catalog, err
	}
	if err = validateReviewLocation(roots, manifest); err != nil {
		return nil, err
	}
	catalog.reviewRoots = append([]string(nil), roots...)
	catalog.reviewManifest = manifest
	catalog.reviewGuards = append([]func(string) error(nil), guards...)
	_, err = catalog.reviewedView()
	if err != nil {
		return nil, err
	}
	return catalog, nil
}

func validateReviewLocation(roots []string, manifest string) error {
	target, err := filepath.EvalSymlinks(manifest)
	if err != nil {
		return skillError("skill_review_manifest_invalid", "Skill review manifest is unavailable")
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	for _, root := range roots {
		base, err := filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
		base, err = filepath.Abs(base)
		if err != nil {
			return err
		}
		if target == base || strings.HasPrefix(target, base+string(os.PathSeparator)) {
			return skillError("skill_review_manifest_invalid", "Review manifest must be outside Skill installation roots")
		}
	}
	return nil
}

var reviewHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func readReviews(path string) (map[string]string, error) {
	fail := func() (map[string]string, error) {
		return nil, skillError("skill_review_manifest_invalid", "Skill review manifest is missing or invalid; operator review required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fail()
	}
	file, err := os.Open(path)
	if err != nil {
		return fail()
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return fail()
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return fail()
	}
	var manifest reviewManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.SchemaVersion != "skill-reviews-v1" || manifest.Approvals == nil || len(manifest.Approvals) > 256 {
		return fail()
	}
	reviews := map[string]string{}
	for _, entry := range manifest.Approvals {
		if !validName(entry.Name) || !reviewHashPattern.MatchString(entry.Hash) || reviews[entry.Name] != "" {
			return fail()
		}
		reviews[entry.Name] = entry.Hash
	}
	return reviews, nil
}

// Re-read on new bindings/Runs, not during a frozen Run. Snapshot validation
// remains self-contained; revoking Tool authority is a separate live policy.
func (c *Catalog) reviewedView() (*Catalog, error) {
	if c == nil || c.reviewManifest == "" {
		return c, nil
	}
	for _, guard := range c.reviewGuards {
		if err := guard(c.reviewManifest); err != nil {
			return nil, skillError("skill_review_manifest_invalid", "Skill review manifest is not a protected operator file")
		}
	}
	if err := validateReviewLocation(c.reviewRoots, c.reviewManifest); err != nil {
		return nil, err
	}
	reviews, err := readReviews(c.reviewManifest)
	if err != nil {
		return nil, err
	}
	current, err := LoadRoots(c.reviewRoots)
	if err != nil {
		return nil, skillError("skill_review_catalog_invalid", "Skill packages changed or became invalid; operator review required")
	}
	current.blocked = map[string]string{}
	for name, item := range current.packages {
		if reviews[name] == "" {
			current.blocked[name] = "skill_review_required"
		} else if reviews[name] != item.Hash {
			current.blocked[name] = "skill_review_changed"
		}
	}
	return current, nil
}
