package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

func (f *browserProvider) structuredOutputResponse(w http.ResponseWriter, input browserModelRequest, task string) bool {
	if !strings.Contains(task, "structured-output-gate") {
		return false
	}
	system := ""
	observations := 0
	for _, message := range input.Messages {
		if message.Role == "system" {
			system += message.Content
		}
		if message.Role == "tool" {
			observations++
		}
	}
	constrained := strings.Contains(system, "OUTPUT_CONTRACT")
	if constrained {
		if input.ResponseFormat["type"] != "json_schema" {
			f.reject(w, "final report omitted response_format")
			return true
		}
		definition, _ := input.ResponseFormat["json_schema"].(map[string]any)
		schema, _ := definition["schema"].(map[string]any)
		if definition["strict"] != true || schema["type"] != "object" || schema["additionalProperties"] != false {
			f.reject(w, "final report has wrong frozen schema")
			return true
		}
	} else if input.ResponseFormat != nil {
		f.reject(w, "internal stage constrained by final report schema")
		return true
	}
	content, reason := "structured-output-gate candidate from authorized material.", "stop"
	var calls []any
	hasCalculator := false
	for _, tool := range input.Tools {
		hasCalculator = hasCalculator || tool.Function.Name == "calculator"
	}
	if hasCalculator && observations == 0 {
		content, reason = "", "tool_calls"
		calls = []any{map[string]any{"id": "report-calculation", "type": "function", "function": map[string]any{"name": "calculator", "arguments": `{"expression":"2+3"}`}}}
	} else if constrained {
		content = `{"summary":"Project Alpha","facts":["Uses Go","5 deployments"]}`
		switch {
		case strings.Contains(task, "-invalid"):
			content = `{"summary":`
		case strings.Contains(task, "-mismatch"):
			content = `{"summary":"Alpha"}`
		case strings.Contains(task, "-cutoff"):
			content = `{"summary":`
			reason = "length"
		case strings.Contains(task, "-refusal"):
			content = ""
			reason = "content_filter"
		case strings.Contains(task, "-partial"):
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"{\"}}]}\n\n")
			return true
		}
	} else if strings.Contains(system, "You are the Decide stage") {
		content = `{"decision":"stop","reason":"ready","final_answer":"structured-output-gate candidate from authorized material."}`
	}
	response := browserCompletion(content, reason, calls, nil)
	if constrained && strings.Contains(task, "-refusal") {
		response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["refusal"] = "Request refused by fixture policy"
	}
	if input.Stream {
		stream, err := modelstream.Completion(response)
		if err != nil {
			f.reject(w, err.Error())
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	} else {
		_ = json.NewEncoder(w).Encode(response)
	}
	return true
}
