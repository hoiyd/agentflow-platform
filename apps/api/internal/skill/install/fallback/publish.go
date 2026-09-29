package fallback

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"agentflow-platform/apps/api/internal/skill"
)

func stage(ctx context.Context, root *os.Root, apply bool, contents map[string][]byte, report Report) (Report, error) {
	var staging string
	if apply {
		// ponytail: serialize operator installs per root; per-package locks only if CLI throughput matters.
		if err := root.Mkdir(".skill-install.lock", 0700); err != nil {
			return report, fmt.Errorf("installation root is locked; inspect any interrupted installer before removing its lock")
		}
		defer root.Remove(".skill-install.lock")
		staging = ".skill-stage-" + rand.Text()
		if err := root.Mkdir(staging, 0700); err != nil {
			return report, err
		}
		defer root.RemoveAll(staging)
	} else {
		dir, err := os.MkdirTemp("", "agentflow-skill-preview-")
		if err != nil {
			return report, err
		}
		defer os.RemoveAll(dir)
		tempRoot, err := os.OpenRoot(dir)
		if err != nil {
			return report, err
		}
		defer tempRoot.Close()
		root = tempRoot
		staging = ".stage"
		if err := root.Mkdir(staging, 0700); err != nil {
			return report, err
		}
	}
	packagePath := filepath.Join(staging, report.Name)
	if err := root.Mkdir(packagePath, 0700); err != nil {
		return report, err
	}
	for name, content := range contents {
		file := filepath.Join(packagePath, filepath.FromSlash(name))
		if err := root.MkdirAll(filepath.Dir(file), 0700); err != nil {
			return report, err
		}
		if err := writeNew(root, file, content); err != nil {
			return report, err
		}
	}
	catalog, err := skill.LoadDirectories([]string{filepath.Join(root.Name(), packagePath)})
	if err != nil {
		return report, fmt.Errorf("Skill Loader rejected download: %w", err)
	}
	report.PackageHash = catalog.List()[0].Hash
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if !apply {
		return report, nil
	}
	if _, err := root.Lstat(report.Name); !os.IsNotExist(err) {
		return report, fmt.Errorf("package destination already exists or cannot be inspected")
	}
	report.Status = "installed"
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	if err := writeNew(root, filepath.Join(packagePath, "install-receipt.json"), append(encoded, '\n')); err != nil {
		return report, err
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := root.Rename(packagePath, report.Name); err != nil {
		return report, fmt.Errorf("cannot publish Skill: %w", err)
	}
	return report, nil
}

func writeNew(root *os.Root, name string, content []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
