package projection

import (
	"cmp"
	"encoding/json"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
)

// BuildSkillEvidence joins existing durable inputs; no independent activation
// state or content capture is needed. Stage proximity is navigation, not causality.
func BuildSkillEvidence(replay domain.RunReplay, records []domain.ModelRequestRecord) []domain.SkillEvidence {
	if replay.RuntimeSnapshot == nil {
		return nil
	}
	snapshot := replay.RuntimeSnapshot
	packages := map[string]domain.SkillSnapshot{}
	references := map[string]string{}
	for _, item := range snapshot.Skills {
		packages[item.Name] = item
		references["skill:"+item.Name+"@"+item.Hash] = item.Name
	}
	bound := map[string]map[string]bool{}
	for _, agent := range append([]domain.RuntimeAgentSnapshot{snapshot.Agent}, snapshot.CandidateAgents...) {
		if bound[agent.ID] == nil {
			bound[agent.ID] = map[string]bool{}
		}
		for _, name := range agent.Skills {
			if _, ok := packages[name]; ok {
				bound[agent.ID][name] = true
			}
		}
	}
	rows := map[string]*domain.SkillEvidence{}
	rowFor := func(agent, stage, name string) *domain.SkillEvidence {
		if !bound[agent][name] {
			return nil
		}
		key := agent + "\x00" + stage + "\x00" + name
		if rows[key] == nil {
			rows[key] = &domain.SkillEvidence{Name: name, Hash: packages[name].Hash, AgentID: agent, StageID: stage, Bound: true, Instructions: "not_observed", Activation: "not_observed", Resources: []domain.SkillResourceEvidence{}, Failures: []domain.SkillFailureEvidence{}}
		}
		return rows[key]
	}
	events := slices.Clone(replay.RunEvents)
	slices.SortStableFunc(events, func(a, b domain.RunEvent) int { return cmp.Compare(a.Sequence, b.Sequence) })
	requests := slices.Clone(records)
	slices.SortStableFunc(requests, func(a, b domain.ModelRequestRecord) int {
		if order := cmp.Compare(a.Envelope.Attempt, b.Envelope.Attempt); order != 0 {
			return order
		}
		if order := a.Envelope.CreatedAt.Compare(b.Envelope.CreatedAt); order != 0 {
			return order
		}
		return cmp.Compare(a.Envelope.ID, b.Envelope.ID)
	})
	requestEvents := map[string]domain.RunEvent{}
	for _, event := range events {
		if event.Type == domain.EventModelRequestPrepared {
			requestEvents[stringPayload(event.Payload, "record_id")] = event
		}
	}
	turnAgents, stageAgents, activation := map[string]string{}, map[string]string{}, map[string]string{}
	for _, event := range events {
		if event.Type == domain.EventTurnStarted {
			turnAgents[event.TurnID] = stringPayload(event.Payload, "agent_id")
		}
		if event.Type == domain.EventStageStarted {
			stageAgents[event.StageID] = stringPayload(event.Payload, "agent_id")
		}
		agent := turnAgents[event.TurnID]
		if agent == "" {
			agent = stageAgents[event.StageID]
		}
		if event.Type == domain.EventContextAssembled {
			var manifest domain.ContextManifest
			if !decodeSkillPayload(event.Payload["manifest"], &manifest) || manifest.RunID != replay.Run.ID {
				continue
			}
			if manifest.AgentID != "" {
				if agent != "" && agent != manifest.AgentID {
					continue
				}
				agent = manifest.AgentID
			}
			for _, entry := range manifest.Entries {
				if entry.Source != "skill_instructions" || !entry.Selected {
					continue
				}
				name, known := references[entry.ReferenceID]
				if !known {
					continue
				}
				row := rowFor(agent, manifest.StageID, name)
				if row == nil {
					continue
				}
				key := agent + "\x00" + name
				if entry.Activation == "explicit" {
					activation[key] = "explicit"
				}
				if activation[key] != "" {
					row.Activation = activation[key]
				}
				for _, record := range requests {
					e := record.Envelope
					if e.ID == "" || manifest.ID == "" || manifest.ModelCallID == "" || e.RunID != replay.Run.ID || e.ContextManifestID != manifest.ID || e.ModelCallID != manifest.ModelCallID || e.StageID != manifest.StageID || e.TurnID != manifest.TurnID {
						continue
					}
					if row.Instructions == "included" {
						break
					}
					row.Instructions, row.ManifestID, row.EventID, row.RequestID = "included", manifest.ID, event.ID, e.ID
					row.FirstSequence, row.EstimatedTokens = event.Sequence, entry.EstimatedTokens
					prepared := requestEvents[e.ID]
					if prepared.RunID == replay.Run.ID && prepared.StageID == manifest.StageID && prepared.TurnID == manifest.TurnID {
						row.FirstRequestSequence = prepared.Sequence
					}
					break
				}
			}
			continue
		}
		tool := stringPayload(event.Payload, "tool_name")
		if (tool != "skill_load" && tool != "skill_read") || (event.Type != domain.EventToolCompleted && event.Type != domain.EventToolFailed) {
			continue
		}
		var result struct {
			Name, Hash, Path string
			AgentID          string `json:"agent_id"`
			Offset           *int
			NextOffset       *int `json:"next_offset"`
			TotalBytes       *int `json:"total_bytes"`
			AlreadyLoaded    bool `json:"already_loaded"`
		}
		decoded := decodeSkillPayload(event.Payload["result"], &result)
		if tool == "skill_load" && result.AgentID != "" {
			if agent != "" && agent != result.AgentID {
				continue
			}
			agent = result.AgentID
		}
		var args struct{ Name, Path string }
		_ = json.Unmarshal([]byte(stringPayload(event.Payload, "arguments")), &args)
		name := result.Name
		if name == "" {
			name = args.Name
		}
		row := rowFor(agent, event.StageID, name)
		if row == nil {
			continue
		}
		if event.Type == domain.EventToolFailed || stringPayload(event.Payload, "error") != "" || event.Payload["truncated"] == true || !decoded {
			code := stringPayload(event.Payload, "error_code")
			if code == "" {
				code = "result_not_observed"
			}
			row.Failures = append(row.Failures, domain.SkillFailureEvidence{Tool: tool, Code: code, Path: args.Path, EventID: event.ID, Sequence: event.Sequence})
			continue
		}
		if tool == "skill_load" {
			if result.Hash == row.Hash {
				turnAgents[event.TurnID] = agent
				key := agent + "\x00" + name
				// Confirmation of a retained method is not evidence of its origin.
				if activation[key] == "" && !result.AlreadyLoaded {
					activation[key] = "model"
				}
				if activation[key] != "" {
					row.Activation = activation[key]
				}
			}
			continue
		}
		if result.Offset == nil || result.NextOffset == nil || result.TotalBytes == nil {
			continue
		}
		for _, resource := range packages[name].Resources {
			if resource.Path == result.Path && resource.Hash == result.Hash && *result.Offset >= 0 && *result.NextOffset >= *result.Offset && *result.NextOffset <= *result.TotalBytes {
				row.Resources = append(row.Resources, domain.SkillResourceEvidence{Path: result.Path, Hash: result.Hash, Offset: *result.Offset, NextOffset: *result.NextOffset, TotalBytes: *result.TotalBytes, EventID: event.ID, Sequence: event.Sequence})
			}
		}
	}
	for agent, names := range bound {
		for name := range names {
			observed := false
			for _, row := range rows {
				if row.AgentID == agent && row.Name == name {
					observed = true
					break
				}
			}
			if !observed {
				rowFor(agent, "", name)
			}
		}
	}
	result := []domain.SkillEvidence{}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		result = append(result, *rows[key])
	}
	return result
}

func decodeSkillPayload(value any, target any) bool {
	encoded, err := json.Marshal(value)
	return err == nil && string(encoded) != "null" && json.Unmarshal(encoded, target) == nil
}
