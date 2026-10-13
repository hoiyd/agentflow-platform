package filesystem

import (
	"context"
	"encoding/json"
	"time"

	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

const maxWriteBytes = 64 * 1024

type writeArguments struct {
	Root         string `json:"root"`
	Path         string `json:"path"`
	Content      string `json:"content"`
	ExpectedHash string `json:"expected_hash"`
	OldText      string `json:"old_text"`
	NewText      string `json:"new_text"`
}

func (s *Service) writeBindings() []tool.Binding {
	capability := policy.NormalizeCapability(policy.Capability{
		Scope:      policy.Scope{Resources: []policy.ResourceScope{{Kind: policy.ResourceFilesystem, Name: "project_outputs", Access: policy.AccessWrite}}},
		SideEffect: policy.SideEffectExternalWrite, Reversibility: policy.Irreversible, Audit: policy.AuditFull,
	})
	bindings := []tool.Binding{}
	for _, name := range []string{"file_write", "file_patch"} {
		properties := map[string]any{
			"root":          map[string]any{"type": "string", "pattern": "^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$", "maxLength": 64},
			"path":          map[string]any{"type": "string", "minLength": 1, "maxLength": 180},
			"expected_hash": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
		}
		required := []string{"root", "path"}
		description := "Save a UTF-8 report in an operator-authorized managed output root. Only flat .md/.txt/.json/.csv names are allowed. Omit expected_hash to create only; replacement requires the raw hash returned by file_read. Conflicts never overwrite. No Shell or project source edits."
		if name == "file_write" {
			properties["content"] = map[string]any{"type": "string", "maxLength": maxWriteBytes}
			required = append(required, "content")
		} else {
			description = "Patch exactly one literal occurrence in a UTF-8 report in a managed output root. Read first and pass expected_hash. Missing/ambiguous text or a stale hash is a conflict; no writes occur. No project source edits."
			properties["old_text"] = map[string]any{"type": "string", "minLength": 1, "maxLength": maxWriteBytes}
			properties["new_text"] = map[string]any{"type": "string", "maxLength": maxWriteBytes}
			required = append(required, "expected_hash", "old_text", "new_text")
		}
		parameters := tool.ObjectSchema(properties, required)
		parameters["$comment"] = s.revision + "; filesystem-write-v1"
		operation := name
		binding := tool.Binding{
			Descriptor: tool.Descriptor{Name: name, Description: description, Parameters: parameters, Security: capability, Concurrency: tool.ConcurrencyPolicy{Mode: tool.ConcurrencySerial}, SideEffect: tool.SideEffectPolicy{RetryWithSameKey: true}},
			Policy:     tool.ExecutionPolicy{Timeout: 5 * time.Second, MaxResultBytes: 12000},
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var input writeArguments
				if err := json.Unmarshal(args, &input); err != nil {
					return nil, failure(tool.ErrorInvalidArgs, "Invalid filesystem write arguments")
				}
				return s.write(ctx, operation, input)
			},
			Reconciliation: tool.SideEffectReconciliation{RetryWithSameKey: s.verifyPublished},
		}
		if len(s.roots) == 0 {
			binding.UnavailableReason = "filesystem_unconfigured"
		} else {
			writable := false
			for _, root := range s.roots {
				writable = writable || root.WriteAccess == "managed_output"
			}
			if !writable {
				binding.UnavailableReason = "filesystem_write_unconfigured"
			} else {
				binding.CheckAvailability = s.availability
			}
		}
		if _, ok := s.store.(writeStore); !ok && binding.UnavailableReason == "" {
			binding.UnavailableReason = "filesystem_write_unconfigured"
		}
		bindings = append(bindings, binding)
	}
	return bindings
}
