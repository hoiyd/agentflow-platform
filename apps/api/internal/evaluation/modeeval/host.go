package modeeval

import (
	"context"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/runtimehost"
)

type host struct{ *runtimehost.Host }

func newHost(ctx context.Context, data Dataset, opts Options) (*host, error) {
	environment, err := runtimehost.New(ctx, runtimehost.Spec{Material: data.Material, Agent: domain.Agent{
		Name: "Evidence report reader", Description: "Read release report evidence and return exact sourced facts",
		SystemPrompt: "Read the authorized material. Return the requested JSON report with exact facts and quotation. Never perform writes.",
		Tools:        []string{"file_read"}, Executor: domain.DefaultAgentExecutor,
		RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"evidence_report"}, TaskExamples: []string{data.Task}},
	}}, runtimehost.Options{DatabaseURL: opts.DatabaseURL, BaseURL: opts.BaseURL, Model: opts.Model, CredentialEnvironment: opts.CredentialEnvironment, MaxRequests: opts.MaxRequests, SampleCalls: opts.SampleCalls, SampleTokens: opts.SampleTokens, TimeoutMS: opts.TimeoutMS})
	if err != nil {
		return nil, err
	}
	return &host{environment}, nil
}
