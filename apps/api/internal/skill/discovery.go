package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

// LoadRoots trusts immediate SKILL.md package directories inside operator-owned
// roots. It never searches repositories, follows package links or grants Tools.
func LoadRoots(directories []string) (*Catalog, error) {
	if len(directories) > MaxSkills {
		return nil, skillError("skill_limit_exceeded", "Too many trusted Skill roots")
	}
	catalog := &Catalog{packages: map[string]domain.SkillSnapshot{}}
	for _, directory := range directories {
		if err := discoverRoot(catalog, directory); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func discoverRoot(catalog *Catalog, directory string) error {
	return visitRoot(directory, func(root *os.Root, name string) error {
		item, err := loadPackage(root, name)
		if err != nil {
			return err
		}
		return catalog.add(item)
	})
}

// visitRoot is shared by startup and read-only Skill checks; discovery semantics
// must not depend on the caller or installer used to populate the root.
func visitRoot(directory string, visit func(*os.Root, string) error) error {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return skillError("skill_unavailable", fmt.Sprintf("Trusted Skill directory is unavailable: %q; check the API working directory and install Skills in the repository root's .agents/skills directory", absolute))
	}
	defer root.Close()
	if _, err := root.Lstat("SKILL.md"); err == nil {
		return skillError("skill_invalid_package", fmt.Sprintf("TRUSTED_SKILL_DIRS expects a parent installation directory, not the individual package %q", absolute))
	} else if !os.IsNotExist(err) {
		return skillError("skill_unavailable", "Trusted Skill root cannot be inspected")
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return skillError("skill_unavailable", "Trusted Skill root cannot be read")
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return skillError("skill_invalid_resource", "Trusted Skill package entries cannot be symbolic links")
		}
		if !entry.IsDir() {
			continue
		}
		if _, err := root.Lstat(filepath.Join(entry.Name(), "SKILL.md")); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return skillError("skill_invalid_package", "Trusted Skill package cannot be inspected")
		}
		// Open the child through its root handle, not a newly resolved absolute path.
		child, err := root.OpenRoot(entry.Name())
		if err != nil {
			return skillError("skill_unavailable", "Trusted Skill package is unavailable within its installation root")
		}
		err = visit(child, entry.Name())
		child.Close()
		if err != nil {
			return fmt.Errorf("Skill %q in %q: %w", entry.Name(), absolute, err)
		}
	}
	return nil
}
