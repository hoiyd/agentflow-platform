package httpapi

import (
	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
)

func chatRequestFromContract(input apicontract.ChatRequest) domain.ChatRequest {
	request := domain.ChatRequest{Message: input.Message}
	if input.ProjectContext != nil {
		request.ProjectContext = &domain.ProjectContext{Root: input.ProjectContext.Root, Paths: append([]string(nil), input.ProjectContext.Paths...)}
	}
	if input.KnowledgeFilter != nil {
		request.KnowledgeFilter = &domain.MetadataFilter{SchemaVersion: string(input.KnowledgeFilter.SchemaVersion), Where: metadataConditionFromInput(input.KnowledgeFilter.Where)}
	}
	if input.ExecutionConstraint != nil {
		request.ExecutionConstraint = string(*input.ExecutionConstraint)
	}
	if input.PlanOrigin != nil {
		request.PlanOrigin = &domain.PlanOrigin{RunID: input.PlanOrigin.RunId, Hash: input.PlanOrigin.Hash}
	}
	if input.WorkspaceId != nil {
		request.WorkspaceID = *input.WorkspaceId
	}
	if input.ConversationId != nil {
		request.ConversationID = *input.ConversationId
	}
	if input.AgentId != nil {
		request.AgentID = *input.AgentId
	}
	if input.Mode != nil {
		request.Mode = string(*input.Mode)
	}
	request.CompletionContract = completionContractFromInput(input.CompletionContract)
	return request
}

func metadataConditionFromInput(input apicontract.MetadataCondition) domain.MetadataCondition {
	condition := domain.MetadataCondition{Value: input.Value}
	if input.Field != nil {
		condition.Field = string(*input.Field)
	}
	if input.Op != nil {
		condition.Op = string(*input.Op)
	}
	if input.All != nil {
		condition.All = []domain.MetadataCondition{}
		for _, child := range *input.All {
			condition.All = append(condition.All, metadataConditionFromInput(child))
		}
	}
	if input.Any != nil {
		condition.Any = []domain.MetadataCondition{}
		for _, child := range *input.Any {
			condition.Any = append(condition.Any, metadataConditionFromInput(child))
		}
	}
	return condition
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
	if input.Output != nil {
		contract.Output = &domain.StructuredOutputConfig{Mode: string(input.Output.Mode), VerifierID: input.Output.VerifierId}
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

func routingRequirementsFromInput(input *apicontract.AgentRoutingRequirements) domain.AgentRoutingRequirements {
	if input == nil {
		return domain.AgentRoutingRequirements{}
	}
	requirements := domain.AgentRoutingRequirements{
		RequiredTools:         copyStringList(input.RequiredTools),
		ProhibitedTools:       copyStringList(input.ProhibitedTools),
		PreferredCapabilities: copyStringList(input.PreferredCapabilities),
	}
	if input.RequireMemory != nil {
		requirements.RequireMemory = *input.RequireMemory
	}
	if input.RequireRetrieval != nil {
		requirements.RequireRetrieval = *input.RequireRetrieval
	}
	return domain.NormalizeAgentRoutingRequirements(requirements)
}

func copyStringList(items *[]string) []string {
	if items == nil {
		return nil
	}
	return append([]string(nil), (*items)...)
}
