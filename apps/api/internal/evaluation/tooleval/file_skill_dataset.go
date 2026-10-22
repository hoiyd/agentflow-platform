package tooleval

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

type FileSkillTask struct {
	ID            string `json:"id"`
	Input         string `json:"input"`
	ExpectedFile  string `json:"expected_file"`
	ExpectedReply string `json:"expected_reply"`
	ExpectSkill   bool   `json:"expect_skill"`
}

type FileSkillDataset struct {
	SchemaVersion    string          `json:"schema_version"`
	ID               string          `json:"id"`
	Version          string          `json:"version"`
	SkillName        string          `json:"skill_name"`
	Material         string          `json:"material"`
	Tasks            []FileSkillTask `json:"tasks"`
	FailureInventory []string        `json:"failure_inventory"`
}

func loadFileSkillDataset(path string) (FileSkillDataset, string, error) {
	var data FileSkillDataset
	file, err := os.Open(path)
	if err != nil {
		return data, "", errors.New("cannot open file/Skill task dataset")
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 32769))
	if err != nil || len(b) > 32768 {
		return data, "", errors.New("file/Skill dataset exceeds 32 KiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil || decoder.Decode(new(any)) != io.EOF || data.SchemaVersion != "file-skill-task-v1" || data.ID == "" || data.Version == "" || data.SkillName == "" || data.Material == "" || len(data.Material) > 8192 || len(data.Tasks) < 2 || len(data.Tasks) > 8 || len(data.FailureInventory) < 10 {
		return data, "", errors.New("invalid file-skill-task-v1 dataset")
	}
	seen := map[string]bool{}
	relevant, neighbor := false, false
	for _, task := range data.Tasks {
		if task.ID == "" || seen[task.ID] || task.Input == "" || task.ExpectedReply == "" || len(task.ExpectedFile) > 8192 || strings.HasPrefix(task.Input, "/skill:") {
			return data, "", errors.New("invalid, duplicate or explicitly invoked file/Skill task")
		}
		seen[task.ID] = true
		if task.ExpectSkill {
			relevant = true
			if task.ExpectedFile == "" {
				return data, "", errors.New("method task requires a persisted file outcome")
			}
		} else {
			neighbor = true
		}
	}
	if !relevant || !neighbor {
		return data, "", errors.New("require relevant and unrelated method tasks")
	}
	canonical, _ := json.Marshal(data)
	return data, digest(string(canonical)), nil
}
