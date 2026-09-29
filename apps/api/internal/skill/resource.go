package skill

import (
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

func ReadResource(item domain.SkillSnapshot, path string, offset, limit int) (domain.SkillResourcePage, error) {
	if !validResourcePath(path) {
		return domain.SkillResourcePage{}, skillError("skill_resource_denied", "Resource path must be a relative package text path")
	}
	if err := ValidateFrozen([]domain.SkillSnapshot{item}); err != nil {
		return domain.SkillResourcePage{}, err
	}
	for _, resource := range item.Resources {
		if resource.Path != path {
			continue
		}
		if offset < 0 || offset >= len(resource.Content) || !utf8.RuneStart(resource.Content[offset]) || limit < 1 || limit > MaxPageBytes {
			return domain.SkillResourcePage{}, skillError("skill_invalid_page", "Resource page offset or limit is invalid")
		}
		end := min(len(resource.Content), offset+limit)
		for end > offset && end < len(resource.Content) && !utf8.RuneStart(resource.Content[end]) {
			end--
		}
		if end == offset {
			return domain.SkillResourcePage{}, skillError("skill_invalid_page", "Resource page limit is too small for a UTF-8 character")
		}
		return domain.SkillResourcePage{Name: item.Name, Path: path, Hash: resource.Hash, Content: resource.Content[offset:end], Offset: offset, NextOffset: end, TotalBytes: len(resource.Content), Truncated: end < len(resource.Content)}, nil
	}
	return domain.SkillResourcePage{}, skillError("skill_resource_unavailable", "Resource is not in the frozen Skill package")
}
