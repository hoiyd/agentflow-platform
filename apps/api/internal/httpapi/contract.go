package httpapi

import (
	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
)

func chatRequestFromContract(input apicontract.ChatRequest) domain.ChatRequest {
	request := domain.ChatRequest{Message: input.Message}
	if input.ConversationId != nil {
		request.ConversationID = *input.ConversationId
	}
	if input.AgentId != nil {
		request.AgentID = *input.AgentId
	}
	if input.Mode != nil {
		request.Mode = string(*input.Mode)
	}
	if input.Executor != nil {
		request.Executor = string(*input.Executor)
	}
	request.CompletionContract = completionContractFromInput(input.CompletionContract)
	return request
}

func completionContractFromInput(input *apicontract.CompletionContractInput) *domain.CompletionContract {
	if input == nil {
		return nil
	}
	contract := &domain.CompletionContract{
		SubjectType: string(input.SubjectType),
		Policy: domain.VerificationPolicy{
			Mode:        domain.VerificationPolicyMode(input.Policy.Mode),
			MaxAttempts: input.Policy.MaxAttempts,
			OnExhausted: domain.VerificationFailureAction(input.Policy.OnExhausted),
		},
		Verifiers: make([]domain.VerifierSpec, 0, len(input.Verifiers)),
	}
	for _, item := range input.Verifiers {
		spec := domain.VerifierSpec{
			ID: item.Id, Type: domain.VerifierType(item.Type), Required: item.Required, Config: item.Config,
		}
		if item.Version != nil {
			spec.Version = *item.Version
		}
		if item.TimeoutMs != nil {
			spec.TimeoutMS = *item.TimeoutMs
		}
		contract.Verifiers = append(contract.Verifiers, spec)
	}
	return contract
}
