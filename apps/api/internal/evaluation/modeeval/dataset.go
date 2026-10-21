// Package modeeval measures the existing Single/Multi/Loop orchestration bundle
// on one common task. It does not change ordinary Run Comparison eligibility.
package modeeval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

type Dataset struct {
	SchemaVersion    string                     `json:"schema_version"`
	ID               string                     `json:"id"`
	Version          string                     `json:"version"`
	Task             string                     `json:"task"`
	Material         string                     `json:"material"`
	Expected         map[string]json.RawMessage `json:"expected"`
	ApprovalEdit     string                     `json:"approval_edit"`
	FailureInventory []string                   `json:"failure_inventory"`
}

func loadDataset(path string) (Dataset, string, error) {
	var data Dataset
	file, err := os.Open(path)
	if err != nil {
		return data, "", errors.New("cannot open orchestration dataset")
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 32769))
	if err != nil || len(content) > 32768 {
		return data, "", errors.New("dataset exceeds 32 KiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil || decoder.Decode(new(any)) != io.EOF || data.SchemaVersion != "orchestration-task-v1" || data.ID == "" || data.Version == "" || data.Task == "" || data.Material == "" || len(data.Material) > 8192 || len(data.Expected) != 6 || data.ApprovalEdit == "" || len(data.FailureInventory) < 7 {
		return data, "", errors.New("invalid orchestration-task-v1 dataset")
	}
	for _, field := range []string{"record", "region", "cap", "state", "source", "quote"} {
		if _, ok := data.Expected[field]; !ok {
			return data, "", errors.New("dataset requires all six output fields")
		}
	}
	var quote, source string
	if json.Unmarshal(data.Expected["quote"], &quote) != nil || quote == "" || !strings.Contains(data.Material, quote) || json.Unmarshal(data.Expected["source"], &source) != nil || source != "brief.md" {
		return data, "", errors.New("dataset quote/source must match the frozen material")
	}
	return data, digest(data), nil
}

func digest(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
