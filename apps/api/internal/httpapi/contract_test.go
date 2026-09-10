package httpapi

import (
	"testing"

	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
)

func TestChatRequestFromContractPreservesExecutionInputs(t *testing.T) {
	workspaceID := "workspace-1"
	conversationID := "conversation-1"
	agentID := "agent-1"
	mode := apicontract.MultiAgent
	version := "v2"
	timeout := int64(2500)
	input := apicontract.ChatRequest{
		WorkspaceId: &workspaceID, ConversationId: &conversationID, AgentId: &agentID,
		Message: "review this", Mode: &mode,
		CompletionContract: &apicontract.CompletionContractInput{
			SubjectType: apicontract.CompletionContractInputSubjectTypeRunOutput,
			Policy: apicontract.VerificationPolicyInput{
				Mode: apicontract.AllMustPass, MaxAttempts: 2,
				OnExhausted: apicontract.VerificationPolicyInputOnExhaustedWaitingForUser,
			},
			Verifiers: []apicontract.VerifierSpecInput{{
				Id: "citations", Type: apicontract.Citation, Required: true,
				Version: &version, TimeoutMs: &timeout, Config: map[string]any{"minimum": 1},
			}},
		},
	}

	request := chatRequestFromContract(input)
	if request.WorkspaceID != workspaceID || request.ConversationID != conversationID || request.AgentID != agentID ||
		request.Message != input.Message || request.Mode != string(mode) {
		t.Fatalf("request fields changed at contract boundary: %#v", request)
	}
	if request.CompletionContract == nil || request.CompletionContract.SubjectType != "run_output" ||
		request.CompletionContract.Policy.Mode != domain.VerificationAllMustPass ||
		request.CompletionContract.Policy.OnExhausted != domain.VerificationWaitForUser ||
		len(request.CompletionContract.Verifiers) != 1 || request.CompletionContract.Verifiers[0].Version != version ||
		request.CompletionContract.Verifiers[0].TimeoutMS != timeout {
		t.Fatalf("completion contract changed at boundary: %#v", request.CompletionContract)
	}
}
