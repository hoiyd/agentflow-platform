package memory

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type recallProfileKey struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

type RecallProfiles map[recallProfileKey]float64

func LoadRecallProfiles(path string) (RecallProfiles, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var config struct {
		Profiles []struct {
			recallProfileKey
			MinSimilarity float64 `json:"min_similarity"`
		} `json:"profiles"`
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data")
	}
	if config.Profiles == nil {
		return nil, fmt.Errorf("profiles array is required")
	}
	profiles := make(RecallProfiles, len(config.Profiles))
	for i, profile := range config.Profiles {
		key := profile.recallProfileKey.normalized()
		if key.Provider == "" || key.Model == "" || key.Dimensions <= 0 {
			return nil, fmt.Errorf("profiles[%d] requires provider, model and positive dimensions", i)
		}
		if profile.MinSimilarity <= 0 || profile.MinSimilarity > 1 {
			return nil, fmt.Errorf("profiles[%d].min_similarity must be greater than 0 and at most 1", i)
		}
		if _, exists := profiles[key]; exists {
			return nil, fmt.Errorf("profiles[%d] is a duplicate profile", i)
		}
		profiles[key] = profile.MinSimilarity
	}
	return profiles, nil
}

func (key recallProfileKey) normalized() recallProfileKey {
	key.Provider = strings.TrimSpace(key.Provider)
	key.Model = strings.TrimSpace(key.Model)
	if key.Provider == "ollama" {
		key.Model = strings.TrimSuffix(key.Model, ":latest")
	}
	return key
}
