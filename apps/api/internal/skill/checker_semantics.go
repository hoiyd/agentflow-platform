package skill

import (
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"agentflow-platform/apps/api/internal/domain"
)

var argumentPlaceholder = regexp.MustCompile(`\$ARGUMENTS(?:\[[0-9]+\])?|\$[0-9]+|\$\{CLAUDE_[A-Z_]+\}`)
var commandInjection = regexp.MustCompile("!`[^`\n]+`")

func checkSemantics(root *os.Root, doc document, item domain.SkillSnapshot, result *PackageCheck) {
	add := func(code, category, severity, file, message, hint string) {
		result.add(diagnostic(code, category, severity, file, message, hint))
	}
	for _, key := range sortedFields(doc) {
		var issue Diagnostic
		switch key {
		case "license", "compatibility":
			issue = diagnostic("skill_metadata_ignored", "semantics", "info", "SKILL.md", "License/compatibility metadata is descriptive, not enforced by the runtime.", "Review licensing and environment requirements manually; no dependency is installed.")
		default:
			issue = diagnostic("skill_semantics_unsupported", "semantics", "error", "SKILL.md", "A frontmatter extension has unsupported runtime semantics.", "Remove or adapt behavioral fields such as allowed-tools, disable-model-invocation, context, model or hooks; do not assume they are enforced.")
		}
		if len(key) <= 64 && namePattern.MatchString(key) {
			if safe := safeRelativePath(key); safe != "." {
				issue.Field = safe
			}
		}
		result.add(issue)
	}
	if utf8.RuneCountInString(doc.Description) > 512 {
		add("skill_description_long", "authoring", "warning", "SKILL.md", "Description is longer than the checker's 512-character advice threshold.", "Keep activation criteria concise; the runtime hard limit remains 1024 characters.")
	}
	for _, phrase := range []string{"all tasks", "any task", "everything", "anything"} {
		if strings.Contains(strings.ToLower(doc.Description), phrase) {
			add("skill_description_broad", "authoring", "warning", "SKILL.md", "Description contains a broad activation phrase.", "Describe specific tasks and activation conditions; this heuristic is not a safety review.")
			break
		}
	}
	if _, err := root.Lstat("scripts"); err == nil {
		add("skill_scripts_ignored", "semantics", "warning", "scripts", "Scripts are neither traversed nor executed.", "Review script dependencies manually; API Tools do not provide shell execution.")
	}
	files := []domain.SkillResource{{Path: "SKILL.md", Content: item.Instructions}}
	files = append(files, item.Resources...)
	for _, file := range files {
		if argumentPlaceholder.MatchString(file.Content) {
			add("skill_arguments_unsupported", "semantics", "error", file.Path, "Recognizable argument/environment substitution is unsupported.", "Provide concrete instructions; the loader does not substitute invocation arguments or host environment values.")
		}
		if commandInjection.MatchString(file.Content) {
			add("skill_command_unsupported", "semantics", "error", file.Path, "Dynamic command injection syntax is unsupported.", "Use an explicitly registered and authorized Tool; no embedded command is executed.")
		}
		if file.Path != "SKILL.md" && path.Ext(file.Path) != ".md" {
			continue
		}
		checkLinks(root, file, item.Resources, result)
	}
}

func checkLinks(root *os.Root, file domain.SkillResource, resources []domain.SkillResource, result *PackageCheck) {
	source := []byte(file.Content)
	doc := goldmark.DefaultParser().Parse(text.NewReader(source))
	links := 0
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var target string
		switch node := node.(type) {
		case *ast.Link:
			target = string(node.Destination)
		case *ast.Image:
			target = string(node.Destination)
		default:
			return ast.WalkContinue, nil
		}
		links++
		if links > 64 {
			result.add(diagnostic("skill_check_limit_exceeded", "resource", "error", file.Path, "Markdown exceeds 64 recognizable links in one file.", "Split references into smaller files and recheck; remaining links were not inspected."))
			return ast.WalkStop, nil
		}
		location, err := url.Parse(target)
		if err == nil && (location.Scheme == "https" || location.Scheme == "http" || location.Scheme == "mailto" || location.Scheme == "" && location.Host != "") {
			return ast.WalkContinue, nil
		}
		if err == nil && location.Path == "" && location.Scheme == "" {
			return ast.WalkContinue, nil
		}
		code, resourcePath := "", ""
		if err != nil || location.Scheme != "" || strings.HasPrefix(location.Path, "/") || strings.ContainsAny(location.Path, "\\\x00") {
			code = "skill_resource_denied"
		} else {
			resourcePath = path.Clean(path.Join(path.Dir(file.Path), location.Path))
			if resourcePath == ".." || strings.HasPrefix(resourcePath, "../") {
				code = "skill_resource_denied"
			} else {
				info, err := root.Lstat(resourcePath)
				switch {
				case os.IsNotExist(err):
					code = "skill_resource_unavailable"
				case err != nil || !info.Mode().IsRegular():
					code = "skill_resource_denied"
				case !SupportedResourcePath(resourcePath):
					code = "skill_resource_not_frozen"
				default:
					found := false
					for _, item := range resources {
						if item.Path == resourcePath {
							found = true
							break
						}
					}
					if !found {
						code = "skill_resource_not_frozen"
					}
				}
			}
		}
		if code != "" {
			issue := diagnostic(code, "resource", "error", file.Path, "A local Markdown link is missing, denied or outside frozen text resources.", "Use an existing package-relative references/assets text file; no external URL is fetched.")
			if safe := safeRelativePath(resourcePath); safe != "." {
				issue.ResourcePath = safe
			}
			result.add(issue)
		}
		return ast.WalkContinue, nil
	})
}
