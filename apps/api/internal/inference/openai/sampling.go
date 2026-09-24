package openai

import "agentflow-platform/apps/api/internal/domain"

func (c *Client) applySampling(body map[string]any, profile domain.GenerationProfile, operation string) error {
	if err := c.validateSampling(operation); err != nil {
		return err
	}
	body["temperature"] = *profile.Temperature
	if profile.TopP == nil {
		delete(body, "top_p")
	} else {
		body["top_p"] = *profile.TopP
	}
	if profile.Seed == nil {
		delete(body, "seed")
	} else {
		body["seed"] = *profile.Seed
	}
	return nil
}

func (c *Client) validateSampling(operation string) error {
	if err := c.generationPolicy.Validate(c.seedSupported); err != nil {
		return &ModelError{Kind: ErrorInvalidRequest, Operation: operation, Message: "invalid generation policy: " + err.Error()}
	}
	return nil
}
