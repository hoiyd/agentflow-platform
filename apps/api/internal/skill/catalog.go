package skill

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool"
)

const (
	MaxSkills            = 8
	maxInstructionsBytes = 8192
	maxResourceBytes     = 8192
	maxPackageBytes      = 32768
	maxResources         = 16
	MaxPageBytes         = 4096
	LoadToolName         = "skill_load"
	ReadToolName         = "skill_read"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Catalog struct {
	packages map[string]domain.SkillSnapshot
}

func (c *Catalog) List() []domain.SkillMetadata {
	items := []domain.SkillMetadata{}
	if c != nil {
		for _, item := range c.packages {
			items = append(items, Metadata(item))
		}
	}
	slices.SortFunc(items, func(a, b domain.SkillMetadata) int { return strings.Compare(a.Name, b.Name) })
	return items
}

func Metadata(item domain.SkillSnapshot) domain.SkillMetadata {
	return domain.SkillMetadata{Name: item.Name, Description: item.Description, Hash: item.Hash, RequiredTools: append([]string(nil), item.RequiredTools...)}
}

func (c *Catalog) Freeze(names, effectiveTools []string) ([]domain.SkillSnapshot, error) {
	items := []domain.SkillSnapshot{}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if c == nil {
			return nil, skillError("skill_unavailable", "Skill catalog is not configured")
		}
		item, ok := c.packages[name]
		if !ok {
			return nil, skillError("skill_unavailable", fmt.Sprintf("Skill %q is not in the trusted catalog", name))
		}
		for _, dependency := range item.RequiredTools {
			if !slices.Contains(effectiveTools, dependency) {
				return nil, skillError("skill_dependency_missing", fmt.Sprintf("Skill %q requires available allowed Tool %q", name, dependency))
			}
		}
		item.RequiredTools = append([]string(nil), item.RequiredTools...)
		item.Resources = append([]domain.SkillResource(nil), item.Resources...)
		items = append(items, item)
	}
	return items, nil
}

func ValidateFrozen(items []domain.SkillSnapshot) error {
	if len(items) > MaxSkills {
		return skillError("skill_limit_exceeded", "Too many frozen Skills")
	}
	seen := map[string]bool{}
	for _, item := range items {
		if err := redaction.ValidateValue(item); err != nil {
			return skillError("skill_sensitive_content", "Skill package contains credential material")
		}
		if !validName(item.Name) || seen[item.Name] || strings.TrimSpace(item.Description) == "" || utf8.RuneCountInString(item.Description) > 1024 {
			return skillError("skill_invalid_package", "Invalid or duplicate Skill metadata")
		}
		seen[item.Name] = true
		if err := validateText(item.Instructions, maxInstructionsBytes); err != nil {
			return err
		}
		total := len(item.Instructions)
		paths := map[string]bool{}
		for _, resource := range item.Resources {
			if !validResourcePath(resource.Path) || paths[resource.Path] || resource.Hash != contentHash(resource.Content) {
				return skillError("skill_invalid_resource", "Invalid frozen Skill resource identity")
			}
			paths[resource.Path] = true
			if err := validateText(resource.Content, maxResourceBytes); err != nil {
				return err
			}
			total += len(resource.Content)
		}
		if len(item.Resources) > maxResources || total > maxPackageBytes {
			return skillError("skill_limit_exceeded", "Skill package exceeds its resource limit")
		}
		if item.Hash != packageHash(item) {
			return skillError("skill_content_changed", "Frozen Skill hash does not match its content")
		}
	}
	return nil
}

func validName(name string) bool { return len(name) <= 64 && namePattern.MatchString(name) }
func validResourcePath(value string) bool {
	if len(value) > 256 || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) || !strings.HasPrefix(value, "references/") && !strings.HasPrefix(value, "assets/") || path.Clean(value) != value {
		return false
	}
	switch path.Ext(value) {
	case ".md", ".txt", ".json", ".yaml", ".yml", ".csv":
		return true
	}
	return false
}
func validateText(content string, maxBytes int) error {
	if len(content) > maxBytes {
		return skillError("skill_limit_exceeded", "Skill text exceeds its byte limit")
	}
	if strings.TrimSpace(content) == "" || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return skillError("skill_invalid_package", "Skill content must be non-empty UTF-8 text")
	}
	if err := redaction.ValidateValue(content); err != nil {
		return skillError("skill_sensitive_content", "Skill content contains credential material")
	}
	return nil
}
func contentHash(content string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(content))) }
func packageHash(item domain.SkillSnapshot) string {
	item.Hash = ""
	encoded, _ := json.Marshal(item)
	return contentHash(string(encoded))
}
func skillError(code, message string) error {
	return &tool.ExecutionError{Code: tool.ErrorCode(code), Message: message}
}
