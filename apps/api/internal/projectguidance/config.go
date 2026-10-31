// Package projectguidance freezes explicitly trusted project instructions. It
// neither discovers arbitrary instruction files nor grants execution authority.
package projectguidance

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/failure"
)

const (
	Version       = "project-guidance-v1"
	SourceType    = "operator_project_guidance"
	MaxFileBytes  = 16 * 1024
	MaxTotalBytes = 32 * 1024
	MaxTokens     = 4096
)

type rule struct {
	Path  string   `json:"path"`
	Paths []string `json:"paths"`
}

type project struct {
	WorkspaceID string `json:"workspace_id"`
	Root        string `json:"root"`
	Entry       string `json:"entry"`
	Rules       []rule `json:"rules"`
}

type Error struct {
	code, message string
	details       map[string]any
}

func (e *Error) Error() string { return e.message }
func (e *Error) FailureInfo() failure.Info {
	category := failure.CategoryValidation
	if e.code == "project_guidance_budget_exceeded" {
		category = failure.CategoryCapacity
	}
	return failure.Info{Code: e.code, Source: "project_guidance", Category: category, Details: e.details}
}

func problem(code, message string) error { return &Error{code: code, message: message} }
func hash(value string) string           { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }

func validPath(value string) bool {
	return value != "." && value != "" && fs.ValidPath(value) && len(value) <= 1024 && strings.Count(value, "/") <= 16 && !strings.ContainsAny(value, "\\\x00\r\n")
}

func validPattern(value string) bool {
	if !validPath(value) || strings.Contains(value, "**") {
		return false
	}
	_, err := path.Match(value, "")
	return err == nil
}

var rootID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func loadConfig(name string) ([]project, string, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, "", problem("project_guidance_unavailable", "Project instructions configuration is unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return nil, "", problem("project_guidance_invalid", "Project instructions configuration exceeds its read limit")
	}
	var config struct {
		Projects []project `json:"projects"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || len(config.Projects) > 64 {
		return nil, "", problem("project_guidance_invalid", "Invalid project instructions configuration")
	}
	seen := map[string]bool{}
	for _, item := range config.Projects {
		id, err := strconv.ParseInt(item.WorkspaceID, 10, 64)
		key := item.WorkspaceID + ":" + item.Root
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != item.WorkspaceID || !rootID.MatchString(item.Root) || !validPath(item.Entry) || len(item.Rules) > 16 || seen[key] {
			return nil, "", problem("project_guidance_invalid", "Instruction sources require unique Workspace/root identities and one bounded entry file")
		}
		seen[key] = true
		paths := map[string]bool{item.Entry: true}
		for _, rule := range item.Rules {
			if !validPath(rule.Path) || paths[rule.Path] || len(rule.Paths) == 0 || len(rule.Paths) > 16 {
				return nil, "", problem("project_guidance_invalid", "Instruction rules require unique files and bounded path patterns")
			}
			paths[rule.Path] = true
			for _, pattern := range rule.Paths {
				if !validPattern(pattern) {
					return nil, "", problem("project_guidance_invalid", "Instruction patterns must be relative path.Match patterns; recursive globs and imports are unsupported")
				}
			}
		}
	}
	return config.Projects, hash(string(data)), nil
}
