package inferencecompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func checkMode(ctx context.Context, client *http.Client, options Options, mode string) ModeEvidence {
	evidence := ModeEvidence{Mode: mode, Status: "failed"}
	runID, terminal, err := startMode(ctx, client, options, mode)
	if err != nil {
		evidence.Detail = err.Error()
		return evidence
	}
	if mode == "multi_agent" && terminal == domain.RunWaitingForUser {
		terminal, err = continueMulti(ctx, client, options, runID)
		if err != nil {
			evidence.Detail = err.Error()
			return evidence
		}
	}
	replay, err := fetchReplay(ctx, client, options, runID)
	if err != nil {
		evidence.Detail = err.Error()
		return evidence
	}
	evidence.RunID, evidence.TerminalState, evidence.TotalTokens = runID, replay.Run.Status, replay.UsageLedger.Totals.TotalTokens
	for _, event := range replay.RunEvents {
		if event.Type == domain.EventModelRouteDecided && event.Payload["outcome"] == "selected" {
			evidence.SelectedRoute, _ = event.Payload["selected_route_id"].(string)
			break
		}
	}
	if terminal != domain.RunCompleted || replay.Run.Status != domain.RunCompleted || evidence.SelectedRoute != options.RouteID || evidence.TotalTokens <= 0 {
		evidence.Detail = fmt.Sprintf("terminal=%s replay=%s route=%q tokens=%d", terminal, replay.Run.Status, evidence.SelectedRoute, evidence.TotalTokens)
		return evidence
	}
	if replay.RuntimeSnapshot == nil || !snapshotContainsRoute(replay.RuntimeSnapshot, options) {
		evidence.Detail = "Replay did not retain the expected route identity"
		return evidence
	}
	evidence.Status = "passed"
	return evidence
}

func checkRunCancellation(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	payload, _ := json.Marshal(map[string]any{
		"workspace_id": options.WorkspaceID, "agent_id": options.AgentID, "mode": "autonomous",
		"message": "Write a detailed compatibility report with evidence and limitations.",
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, options.AgentFlowBaseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-ID", options.WorkspaceID)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	if response.StatusCode/100 != 2 {
		defer response.Body.Close()
		return responseError(response)
	}
	var runID string
	var runStarted bool
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event domain.RunEvent
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
			continue
		}
		if event.RunID != "" {
			runID = event.RunID
		}
		if event.Type == domain.EventRunStarted && runID != "" {
			runStarted = true
			break
		}
	}
	readErr := scanner.Err()
	_ = response.Body.Close()
	if readErr != nil {
		return readErr
	}
	if runID == "" || !runStarted {
		return errors.New("AgentFlow stream omitted run.started before cancellation")
	}
	modelActive := false
	for ctx.Err() == nil {
		replay, err := fetchReplay(ctx, client, options, runID)
		if err != nil {
			return err
		}
		modelActive = false
		for _, event := range replay.RunEvents {
			switch event.Type {
			case domain.EventModelStarted:
				modelActive = true
			case domain.EventModelCompleted, domain.EventModelFailed:
				modelActive = false
			}
		}
		if modelActive {
			break
		}
		if replay.Run.Status != domain.RunRunning {
			return fmt.Errorf("Run reached %s before a model request could be canceled", replay.Run.Status)
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !modelActive {
		return fmt.Errorf("model request did not start before cancellation deadline: %w", ctx.Err())
	}
	cancelRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, options.AgentFlowBaseURL+"/api/runs/"+url.PathEscape(runID)+"/cancel", nil)
	if err != nil {
		return err
	}
	cancelRequest.Header.Set("X-Workspace-ID", options.WorkspaceID)
	cancelResponse, err := client.Do(cancelRequest)
	if err != nil {
		return err
	}
	_ = cancelResponse.Body.Close()
	if cancelResponse.StatusCode/100 != 2 {
		return responseError(cancelResponse)
	}
	var awaitingEvents bool
	for ctx.Err() == nil {
		replay, err := fetchReplay(ctx, client, options, runID)
		if err != nil {
			return err
		}
		if replay.Run.Status == domain.RunCanceled {
			var requested, canceled bool
			openStages := map[string]bool{}
			for _, event := range replay.RunEvents {
				requested = requested || event.Type == domain.EventRunCancelRequested
				canceled = canceled || event.Type == domain.EventRunCanceled
				switch event.Type {
				case domain.EventStageStarted:
					openStages[event.StageID] = true
				case domain.EventStageCompleted, domain.EventStageFailed:
					delete(openStages, event.StageID)
				}
			}
			if requested && canceled {
				if len(openStages) != 0 {
					return fmt.Errorf("canceled Run has %d stage.started events without terminal events", len(openStages))
				}
				return probe(ctx, client, options, apiKey)
			}
			awaitingEvents = true
		}
		if replay.Run.Status == domain.RunCompleted || replay.Run.Status == domain.RunFailed {
			return fmt.Errorf("Run ended as %s instead of canceled", replay.Run.Status)
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	if awaitingEvents {
		return errors.New("canceled Run Replay omitted cancellation lifecycle events")
	}
	return ctx.Err()
}

func startMode(ctx context.Context, client *http.Client, options Options, mode string) (string, domain.RunStatus, error) {
	message := "Reply briefly with compatibility evidence."
	if mode == "multi_agent" {
		message = "Research the compatibility evidence, compare sources, and identify evidence gaps."
	} else if mode == "autonomous" {
		message = "The local model returned a response and token counts. Summarize those two observed facts in one sentence. All facts required are in this request."
	}
	payload := map[string]any{"workspace_id": options.WorkspaceID, "agent_id": options.AgentID, "message": message, "mode": mode}
	return runSSERequest(ctx, client, options, http.MethodPost, "/api/chat", payload)
}

func continueMulti(ctx context.Context, client *http.Client, options Options, runID string) (domain.RunStatus, error) {
	_, status, err := runSSERequest(ctx, client, options, http.MethodPost, "/api/runs/"+url.PathEscape(runID)+"/continue", map[string]any{
		"plan":                 "Produce a brief compatibility response.",
		"routing_requirements": map[string]any{"prohibited_tools": []string{"calculator", "get_current_time"}},
	})
	return status, err
}

func runSSERequest(ctx context.Context, client *http.Client, options Options, method, path string, payload any) (string, domain.RunStatus, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, method, options.AgentFlowBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-ID", options.WorkspaceID)
	response, err := client.Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return "", "", responseError(response)
	}
	var runID string
	var status domain.RunStatus
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var chunk domain.ChatChunk
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &chunk) == nil {
			if chunk.RunID != "" {
				runID = chunk.RunID
			}
			if chunk.Status != "" {
				status = domain.RunStatus(chunk.Status)
			}
			if chunk.Type == "error" {
				return runID, status, fmt.Errorf("AgentFlow run failed: %s", chunk.Error)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return runID, status, err
	}
	if runID == "" || status == "" {
		return runID, status, errors.New("AgentFlow stream omitted run terminal evidence")
	}
	return runID, status, nil
}

func fetchReplay(ctx context.Context, client *http.Client, options Options, runID string) (domain.RunReplay, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, options.AgentFlowBaseURL+"/api/runs/"+url.PathEscape(runID)+"/replay", nil)
	if err != nil {
		return domain.RunReplay{}, err
	}
	request.Header.Set("X-Workspace-ID", options.WorkspaceID)
	response, err := client.Do(request)
	if err != nil {
		return domain.RunReplay{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return domain.RunReplay{}, responseError(response)
	}
	var replay domain.RunReplay
	err = json.NewDecoder(response.Body).Decode(&replay)
	return replay, err
}

func snapshotContainsRoute(snapshot *domain.RuntimeSnapshot, options Options) bool {
	for _, route := range snapshot.ModelRouting.Routes {
		if route.ID == options.RouteID && route.Model == options.Target.Model && route.Endpoint == options.Target.BaseURL {
			return true
		}
	}
	return false
}
