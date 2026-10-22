package runtimehost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (h *Host) Wait(ctx context.Context, id string) (domain.Run, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, found, err := h.Storage.GetRun(id)
		if err != nil {
			return run, err
		}
		if !found {
			return run, fmt.Errorf("evaluation Run not found")
		}
		switch run.Status {
		case domain.RunCompleted, domain.RunFailed, domain.RunFailedRecoverable, domain.RunCanceled, domain.RunWaitingForUser:
			// Uncertain usage intentionally stays reserved; callers stop suite admission.
			return run, nil
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *Host) Request(ctx context.Context, method, path string, input, output any) error {
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, h.api.URL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("X-Workspace-ID", h.Workspace)
	req.Header.Set("Content-Type", "application/json")
	response, err := h.api.Client().Do(req)
	if err != nil {
		return fmt.Errorf("evaluation HTTP command interrupted")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("evaluation HTTP %s %s status=%d", method, path, response.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(output)
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}
