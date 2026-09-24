package domain

import (
	"errors"
	"math"
)

// GenerationPolicy freezes the parameters sent for answer streams and
// non-stream completions (including Tool decisions). An absent top_p leaves
// the backend default unspecified.
type GenerationPolicy struct {
	AnswerStream GenerationProfile `json:"answer_stream"`
	Completion   GenerationProfile `json:"completion"`
}

type GenerationProfile struct {
	Temperature *float64 `json:"temperature"`
	TopP        *float64 `json:"top_p,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
}

func DefaultGenerationPolicy() GenerationPolicy {
	answerStream, completion := 0.4, 0.2
	return GenerationPolicy{
		AnswerStream: GenerationProfile{Temperature: &answerStream},
		Completion:   GenerationProfile{Temperature: &completion},
	}
}

func (p GenerationPolicy) Validate(seedSupported bool) error {
	for _, profile := range []GenerationProfile{p.AnswerStream, p.Completion} {
		if profile.Temperature == nil || math.IsNaN(*profile.Temperature) || math.IsInf(*profile.Temperature, 0) || *profile.Temperature < 0 || *profile.Temperature > 2 {
			return errors.New("temperature must be between 0 and 2")
		}
		if profile.TopP != nil && (math.IsNaN(*profile.TopP) || math.IsInf(*profile.TopP, 0) || *profile.TopP <= 0 || *profile.TopP > 1) {
			return errors.New("top_p must be greater than 0 and at most 1")
		}
		if profile.Seed != nil && !seedSupported {
			return errors.New("seed requires explicit route capability")
		}
	}
	return nil
}

func (p GenerationPolicy) Clone() GenerationPolicy {
	cloneProfile := func(profile GenerationProfile) GenerationProfile {
		if profile.Temperature != nil {
			value := *profile.Temperature
			profile.Temperature = &value
		}
		if profile.TopP != nil {
			value := *profile.TopP
			profile.TopP = &value
		}
		if profile.Seed != nil {
			value := *profile.Seed
			profile.Seed = &value
		}
		return profile
	}
	return GenerationPolicy{AnswerStream: cloneProfile(p.AnswerStream), Completion: cloneProfile(p.Completion)}
}
