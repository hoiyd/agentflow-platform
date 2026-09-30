package skill

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool"
)

// CheckOptions accepts explicit operator paths, never runtime trust config.
// nil EffectiveTools means unknown; a non-nil empty slice checks against no Tools.
type CheckOptions struct {
	Directories    []string
	Roots          []string
	EffectiveTools []string
}

type Diagnostic struct {
	Code         string `json:"code"`
	Category     string `json:"category"`
	Severity     string `json:"severity"`
	Path         string `json:"path"`
	Message      string `json:"message"`
	Hint         string `json:"hint"`
	Field        string `json:"field,omitempty"`
	ResourcePath string `json:"resource_path,omitempty"`
}

type PackageCheck struct {
	Name             string       `json:"name"`
	Status           string       `json:"status"`
	PackageHash      string       `json:"package_hash,omitempty"`
	DependencyStatus string       `json:"dependency_status"`
	Diagnostics      []Diagnostic `json:"diagnostics"`
}

type CheckReport struct {
	SchemaVersion string         `json:"schema_version"`
	Compatible    bool           `json:"compatible"`
	Packages      []PackageCheck `json:"packages"`
	Diagnostics   []Diagnostic   `json:"diagnostics"`
}

// Check never installs, executes, fetches links, binds Skills or grants trust.
// Compatibility covers recognizable declarations, not all prose dependencies.
func Check(opts CheckOptions) CheckReport {
	report := CheckReport{SchemaVersion: "skill-check-v1", Compatible: true, Packages: []PackageCheck{}, Diagnostics: []Diagnostic{}}
	if len(opts.Directories)+len(opts.Roots) == 0 || len(opts.Directories)+len(opts.Roots) > MaxSkills {
		report.Diagnostics = append(report.Diagnostics, diagnostic("skill_limit_exceeded", "format", "error", ".", "Specify one to eight package directories or installation roots.", "Use --dir for packages or --root for immediate discovery."))
		report.Compatible = false
		return report
	}
	visit := func(root *os.Root, name string) error {
		if len(report.Packages) >= MaxSkills {
			return skillError("skill_limit_exceeded", "Too many Skill packages")
		}
		report.Packages = append(report.Packages, checkPackage(root, name, opts.EffectiveTools))
		return nil
	}
	for _, directory := range opts.Directories {
		root, err := openPackageDirectory(directory)
		if err != nil {
			report.Packages = append(report.Packages, rejectedPackage(filepath.Base(filepath.Clean(directory)), err))
			continue
		}
		err = visit(root, filepath.Base(filepath.Clean(directory)))
		root.Close()
		if err != nil {
			report.Diagnostics = append(report.Diagnostics, loaderDiagnostic(err))
		}
	}
	for _, directory := range opts.Roots {
		if err := visitRoot(directory, visit); err != nil {
			report.Diagnostics = append(report.Diagnostics, loaderDiagnostic(err))
		}
	}
	names := map[string]int{}
	for index := range report.Packages {
		item := &report.Packages[index]
		if item.Name != "" && item.PackageHash != "" {
			if previous, exists := names[item.Name]; exists {
				issue := diagnostic("skill_name_conflict", "format", "error", "SKILL.md", "Package name is duplicated in the checked set.", "Remove the duplicate before configuring runtime roots.")
				item.Status = "rejected"
				report.Packages[previous].Status = "rejected"
				item.add(issue)
				report.Packages[previous].add(issue)
			} else {
				names[item.Name] = index
			}
		}
	}
	for index := range report.Packages {
		item := &report.Packages[index]
		for _, issue := range item.Diagnostics {
			if issue.Severity == "error" {
				if issue.Category == "format" {
					item.Status = "rejected"
					break
				}
				if item.Status == "compatible" {
					item.Status = "incompatible"
				}
			}
		}
		report.Compatible = report.Compatible && item.Status == "compatible"
	}
	report.Compatible = report.Compatible && len(report.Diagnostics) == 0
	return report
}

func checkPackage(root *os.Root, name string, tools []string) PackageCheck {
	doc, err := readDocument(root, name)
	if err != nil {
		return rejectedPackage(name, err)
	}
	item, err := loadDocument(root, doc)
	if err != nil {
		return rejectedPackage(name, err)
	}
	result := PackageCheck{Name: item.Name, Status: "compatible", PackageHash: item.Hash, DependencyStatus: "not_declared", Diagnostics: []Diagnostic{}}
	if len(item.RequiredTools) > 0 {
		result.DependencyStatus = "not_checked"
		if tools == nil {
			result.Diagnostics = append(result.Diagnostics, diagnostic("skill_dependencies_unchecked", "dependency", "warning", "SKILL.md", "Declared Tool dependencies were not checked.", "Pass --tools with the Agent's effective, ready Tool names; this does not grant permissions."))
		} else {
			catalog := &Catalog{packages: map[string]domain.SkillSnapshot{item.Name: item}}
			result.DependencyStatus = "available"
			if _, err := catalog.Freeze([]string{item.Name}, tools); err != nil {
				result.DependencyStatus = "missing"
				result.Diagnostics = append(result.Diagnostics, diagnostic("skill_dependency_missing", "dependency", "error", "SKILL.md", "A declared Tool is absent from the supplied effective set.", "Resolve availability through existing Tool schema/policy checks; do not auto-enable dependencies."))
			}
		}
	}
	checkSemantics(root, doc, item, &result)
	return result
}

func rejectedPackage(name string, err error) PackageCheck {
	if len(name) > 64 || !namePattern.MatchString(name) || redaction.ValidateValue(name) != nil {
		name = ""
	}
	return PackageCheck{Name: name, Status: "rejected", DependencyStatus: "not_checked", Diagnostics: []Diagnostic{loaderDiagnostic(err)}}
}

// A bounded package can still contain many repeated links/extension keys.
// Stop report amplification without ever claiming an incomplete check passed.
func (p *PackageCheck) add(issue Diagnostic) {
	if len(p.Diagnostics) == 64 {
		p.Diagnostics = append(p.Diagnostics, diagnostic("skill_check_limit_exceeded", "semantics", "error", ".", "Skill check reached the 64-diagnostic package limit.", "Resolve reported issues and rerun; remaining diagnostics are omitted."))
	} else if len(p.Diagnostics) < 64 {
		p.Diagnostics = append(p.Diagnostics, issue)
	}
}

func loaderErrorCode(err error) string {
	var failure *tool.ExecutionError
	if errors.As(err, &failure) {
		return string(failure.Code)
	}
	return "skill_unavailable"
}

func loaderDiagnostic(err error) Diagnostic {
	// Never serialize err.Error(): discovery errors can embed absolute paths,
	// package-controlled names, or filesystem errors.
	return diagnostic(loaderErrorCode(err), "format", "error", ".", "The runtime Loader rejected this input.", "Review YAML, name/directory identity, regular UTF-8 resources, credential content and Loader limits.")
}

func diagnostic(code, category, severity, file, message, hint string) Diagnostic {
	return Diagnostic{Code: code, Category: category, Severity: severity, Path: safeRelativePath(file), Message: message, Hint: hint}
}

func safeRelativePath(file string) string {
	if !fs.ValidPath(file) || len(file) > 256 || strings.Contains(file, "\\") || redaction.ValidateValue(file) != nil {
		return "."
	}
	return file
}

func sortedFields(doc document) []string {
	keys := make([]string, 0, len(doc.Fields))
	for key := range doc.Fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
