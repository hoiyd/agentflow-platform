// Package fallback supplies the restricted Go installer when Vercel Skills CLI cannot be used.
// It remains operator-only and never changes runtime trust.
package fallback

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/skill"
)

type Options struct {
	Repo, Path, Ref, Destination string
	Apply                        bool
	Timeout                      time.Duration
}

type File struct {
	Path       string `json:"path"`
	SourcePath string `json:"source_path"`
	GitBlob    string `json:"git_blob"`
	SHA256     string `json:"sha256"`
	Bytes      int    `json:"bytes"`
}

type Ignored struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Report struct {
	SchemaVersion             string    `json:"schema_version"`
	Status                    string    `json:"status"`
	Repo                      string    `json:"repo"`
	SourcePath                string    `json:"source_path"`
	Commit                    string    `json:"commit"`
	SourceTree                string    `json:"source_tree"`
	Name                      string    `json:"name"`
	PackageHash               string    `json:"package_hash"`
	FilesHash                 string    `json:"files_hash"`
	Directory                 string    `json:"directory"`
	Files                     []File    `json:"files"`
	Ignored                   []Ignored `json:"ignored"`
	Warnings                  []string  `json:"warnings"`
	CreatedAt                 time.Time `json:"created_at"`
	TrustConfigurationChanged bool      `json:"trust_configuration_changed"`
}

func Run(ctx context.Context, opts Options) (Report, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	defer transport.CloseIdleConnections()
	return run(ctx, opts, &http.Client{Transport: transport})
}

func run(ctx context.Context, opts Options, client *http.Client) (Report, error) {
	var report Report
	if !repoPattern.MatchString(opts.Repo) ||
		!fs.ValidPath(opts.Path) || opts.Path == "." || strings.ContainsAny(opts.Path, "\\\x00\r\n") ||
		len(opts.Path) > 256 || strings.Count(opts.Path, "/") > 8 ||
		(opts.Ref != "" && strings.TrimSpace(opts.Ref) == "") || len(opts.Ref) > 256 || strings.ContainsAny(opts.Ref, "\x00\r\n") ||
		opts.Destination == "" || strings.ContainsAny(opts.Destination, "\x00\r\n") ||
		opts.Timeout <= 0 || opts.Timeout > 2*time.Minute {
		return report, fmt.Errorf("require owner/repo, relative package path, valid optional revision, existing destination and timeout in (0, 2m]")
	}
	destination, err := filepath.Abs(opts.Destination)
	if err != nil {
		return report, err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return report, fmt.Errorf("installation root must already exist: %w", err)
	}
	defer root.Close()
	name := path.Base(opts.Path)
	if !repoPattern.MatchString("package/" + name) {
		return report, fmt.Errorf("invalid package directory name")
	}
	if _, err := root.Lstat(name); !os.IsNotExist(err) {
		return report, fmt.Errorf("package destination already exists or cannot be inspected")
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	g := newGitHub(opts.Repo, client)
	// Resolve mutable refs once; preview, publication and the receipt use this immutable commit.
	commit, err := g.commit(ctx, opts.Ref)
	if err != nil {
		return report, err
	}
	current, err := g.tree(ctx, commit)
	if err != nil {
		return report, err
	}
	repoTree := current
	for _, segment := range strings.Split(opts.Path, "/") {
		index := slices.IndexFunc(current.Entries, func(e treeEntry) bool { return e.Path == segment })
		if index < 0 {
			return report, fmt.Errorf("package directory not found at selected commit")
		}
		current, err = g.child(ctx, current.Entries[index])
		if err != nil {
			return report, err
		}
	}
	report = Report{SchemaVersion: "skill-install-v1", Status: "preview", Repo: opts.Repo, SourcePath: opts.Path, Commit: commit, SourceTree: current.SHA, Name: name, Directory: filepath.Join(destination, name), Files: []File{}, Ignored: []Ignored{}, Warnings: []string{}, CreatedAt: time.Now().UTC()}
	contents := map[string][]byte{}
	add := func(filePath, sourcePath string, item treeEntry) error {
		content, err := g.blob(ctx, item)
		if err != nil {
			return err
		}
		if !utf8.Valid(content) || strings.ContainsRune(string(content), 0) {
			return fmt.Errorf("selected files must be UTF-8 text")
		}
		if err := redaction.ValidateValue(string(content)); err != nil {
			return fmt.Errorf("selected file contains credential material")
		}
		contents[filePath] = content
		report.Files = append(report.Files, File{Path: filePath, SourcePath: sourcePath, GitBlob: item.SHA, SHA256: fmt.Sprintf("%x", sha256.Sum256(content)), Bytes: len(content)})
		return nil
	}
	var walk func(tree, string, int) error
	walk = func(directory tree, prefix string, depth int) error {
		if depth > 8 {
			return fmt.Errorf("Skill resource directory depth exceeded")
		}
		for _, item := range directory.Entries {
			filePath := path.Join(prefix, item.Path)
			if len(filePath) > 256 {
				return fmt.Errorf("Skill package path limit exceeded")
			}
			if item.Mode == "120000" || item.Mode == "160000" {
				return fmt.Errorf("Skill package links and submodules are not supported")
			}
			if item.Type == "tree" && (filePath == "references" || filePath == "assets" || strings.HasPrefix(filePath, "references/") || strings.HasPrefix(filePath, "assets/")) {
				child, err := g.child(ctx, item)
				if err != nil {
					return err
				}
				if err := walk(child, filePath, depth+1); err != nil {
					return err
				}
				continue
			}
			if filePath == "SKILL.md" || skill.SupportedResourcePath(filePath) || prefix == "" && licenseName(filePath) {
				if err := add(filePath, path.Join(opts.Path, filePath), item); err != nil {
					return err
				}
			} else {
				report.Ignored = append(report.Ignored, Ignored{Path: filePath, Reason: "not supported by the text-only Loader; directory contents not traversed"})
			}
		}
		return nil
	}
	if err := walk(current, "", 0); err != nil {
		return report, err
	}
	hasLicense := slices.ContainsFunc(report.Files, func(f File) bool { return licenseName(f.Path) })
	if !hasLicense {
		for _, item := range repoTree.Entries {
			if licenseName(item.Path) {
				if err := add(item.Path, item.Path, item); err != nil {
					return report, err
				}
				hasLicense = true
				break
			}
		}
	}
	if !hasLicense {
		report.Warnings = append(report.Warnings, "No package or repository License found; permission to redistribute is unknown.")
	}
	if len(report.Ignored) > 0 {
		report.Warnings = append(report.Warnings, "Unsupported content is omitted; review the Skill for dependencies on omitted files.")
	}
	report.Warnings = append(report.Warnings, "Review instructions and resources before configuring this package's parent installation root in TRUSTED_SKILL_DIRS and restarting the API; no Agent bindings or Tool permissions are granted.")
	slices.SortFunc(report.Files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(report.Ignored, func(a, b Ignored) int { return strings.Compare(a.Path, b.Path) })
	encoded, _ := json.Marshal(report.Files)
	report.FilesHash = fmt.Sprintf("%x", sha256.Sum256(encoded))
	return stage(ctx, root, opts.Apply, contents, report)
}

func licenseName(name string) bool {
	switch name {
	case "LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING":
		return true
	}
	return false
}
