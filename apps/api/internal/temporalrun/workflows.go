package temporalrun

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func AgentRunWorkflow(ctx workflow.Context, input AgentRunWorkflowInput) (AgentRunWorkflowResult, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    5 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	var result AgentRunWorkflowResult
	if err := workflow.ExecuteActivity(ctx, ExecuteAutonomousRunActivityName, input).Get(ctx, &result); err != nil {
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, err
	}
	return result, nil
}
