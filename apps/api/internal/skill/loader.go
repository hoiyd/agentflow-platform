package skill

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"agentflow-platform/apps/api/internal/domain"
)

func LoadDirectories(directories []string) (*Catalog, error) {
	if len(directories) > MaxSkills {
		return nil, skillError("skill_limit_exceeded", "Too many trusted Skill directories")
	}
	catalog := &Catalog{packages: map[string]domain.SkillSnapshot{}}
	for _, directory := range directories {
		item, err := loadDirectory(directory)
		if err != nil {
			return nil, err
		}
		if err := catalog.add(item); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func (c *Catalog) add(item domain.SkillSnapshot) error {
	if _, exists := c.packages[item.Name]; exists {
		return skillError("skill_name_conflict", fmt.Sprintf("Trusted Skill name %q is duplicated", item.Name))
	}
	if len(c.packages) >= MaxSkills {
		return skillError("skill_limit_exceeded", "Too many trusted Skill packages")
	}
	c.packages[item.Name] = item
	return nil
}

func loadDirectory(directory string) (domain.SkillSnapshot, error) {
	root, err := openPackageDirectory(directory)
	if err != nil {
		return domain.SkillSnapshot{}, err
	}
	defer root.Close()
	return loadPackage(root, filepath.Base(filepath.Clean(directory)))
}

func openPackageDirectory(directory string) (*os.Root, error) {
	directory = filepath.Clean(directory)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return nil, skillError("skill_unavailable", "Skill package directory is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, skillError("skill_invalid_resource", "Skill package directory cannot be a symbolic link")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, skillError("skill_unavailable", "Skill package directory cannot be opened")
	}
	return root, nil
}

func loadPackage(root *os.Root, name string) (domain.SkillSnapshot, error) {
	document, err := readDocument(root, name)
	if err != nil {
		return domain.SkillSnapshot{}, err
	}
	return loadDocument(root, document)
}

// document retains frontmatter for operator diagnostics; only Snapshot fields
// are runtime capabilities. Unknown fields never grant permissions.
type document struct {
	Name         string               `yaml:"name"`
	Description  string               `yaml:"description"`
	Metadata     map[string]string    `yaml:"metadata"`
	Fields       map[string]yaml.Node `yaml:",inline"`
	Instructions string               `yaml:"-"`
}

func readDocument(root *os.Root, name string) (document, error) {
	data, err := readText(root, "SKILL.md", maxInstructionsBytes+4096)
	if err != nil {
		return document{}, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return document{}, skillError("skill_invalid_package", "SKILL.md requires YAML frontmatter")
	}
	parts := strings.SplitN(text[4:], "\n---\n", 2)
	if len(parts) != 2 {
		return document{}, skillError("skill_invalid_package", "SKILL.md frontmatter is not terminated")
	}
	var header document
	decoder := yaml.NewDecoder(bytes.NewBufferString(parts[0]))
	if err := decoder.Decode(&header); err != nil {
		return document{}, skillError("skill_invalid_package", "SKILL.md has invalid YAML metadata")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return document{}, skillError("skill_invalid_package", "SKILL.md has multiple YAML documents")
	}
	if header.Name != name {
		return document{}, skillError("skill_invalid_package", "Skill name must match its directory name")
	}
	header.Instructions = strings.TrimSpace(parts[1])
	return header, nil
}

func loadDocument(root *os.Root, header document) (domain.SkillSnapshot, error) {
	item := domain.SkillSnapshot{Name: header.Name, Description: header.Description, Instructions: header.Instructions, RequiredTools: strings.Fields(header.Metadata["agentflow-required-tools"])}
	for _, subdir := range []string{"references", "assets"} {
		err := fs.WalkDir(root.FS(), subdir, func(path string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, fs.ErrNotExist) && path == subdir {
				return nil
			}
			if walkErr != nil {
				return skillError("skill_invalid_resource", "Skill resources cannot be inspected")
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return skillError("skill_invalid_resource", "Skill resources cannot be symbolic links")
			}
			if entry.IsDir() {
				return nil
			}
			if !SupportedResourcePath(path) {
				return skillError("skill_invalid_resource", "Only references/assets text resources are supported")
			}
			if len(item.Resources) >= maxResources {
				return skillError("skill_limit_exceeded", "Too many Skill resources")
			}
			content, err := readText(root, path, maxResourceBytes)
			if err != nil {
				return err
			}
			item.Resources = append(item.Resources, domain.SkillResource{Path: path, Hash: contentHash(string(content)), Content: string(content)})
			return nil
		})
		if err != nil {
			return domain.SkillSnapshot{}, err
		}
	}
	slices.SortFunc(item.Resources, func(a, b domain.SkillResource) int { return strings.Compare(a.Path, b.Path) })
	item.Hash = packageHash(item)
	if err := ValidateFrozen([]domain.SkillSnapshot{item}); err != nil {
		return domain.SkillSnapshot{}, err
	}
	return item, nil
}

func readText(root *os.Root, path string, limit int) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, skillError("skill_invalid_resource", "Skill file is missing or not a regular file")
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, skillError("skill_invalid_resource", "Skill file cannot be opened within its trusted root")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, skillError("skill_invalid_resource", "Skill file is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit+1)))
	if err != nil {
		return nil, skillError("skill_invalid_resource", "Skill file cannot be read")
	}
	if err := validateText(string(data), limit); err != nil {
		return nil, err
	}
	return data, nil
}
