package temporalrun

import (
	"context"
	"fmt"
	"strings"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

type Runner struct {
	client    client.Client
	taskQueue string
}

func NewRunner(temporalClient client.Client, taskQueue string) *Runner {
	return &Runner{client: temporalClient, taskQueue: strings.TrimSpace(taskQueue)}
}

func (r *Runner) StartAgentRun(ctx context.Context, input AgentRunWorkflowInput) (workflowID string, workflowRunID string, err error) {
	if r == nil || r.client == nil {
		return "", "", fmt.Errorf("temporal runner is not configured")
	}
	workflowID = "agentflow-run-" + strings.TrimSpace(input.RunID)
	options := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: r.taskQueue,
	}
	execution, err := r.client.ExecuteWorkflow(ctx, options, AgentRunWorkflow, input)
	if err != nil {
		return "", "", err
	}
	return execution.GetID(), execution.GetRunID(), nil
}

func (r *Runner) CancelRun(ctx context.Context, workflowID string, workflowRunID string) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("temporal runner is not configured")
	}
	if strings.TrimSpace(workflowID) == "" {
		return fmt.Errorf("workflow id is required")
	}
	return r.client.CancelWorkflow(ctx, strings.TrimSpace(workflowID), strings.TrimSpace(workflowRunID))
}

func (r *Runner) DescribeRunStatus(ctx context.Context, workflowID string, workflowRunID string) (string, error) {
	if r == nil || r.client == nil || strings.TrimSpace(workflowID) == "" {
		return "", nil
	}
	resp, err := r.client.DescribeWorkflowExecution(ctx, strings.TrimSpace(workflowID), strings.TrimSpace(workflowRunID))
	if err != nil {
		return "", err
	}
	return temporalStatus(resp.WorkflowExecutionInfo.Status), nil
}

func temporalStatus(status enums.WorkflowExecutionStatus) string {
	switch status {
	case enums.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return WorkflowStatusRunning
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return WorkflowStatusCompleted
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED:
		return WorkflowStatusFailed
	case enums.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return WorkflowStatusCanceled
	case enums.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return "terminated"
	case enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return "timed_out"
	default:
		return strings.ToLower(strings.TrimPrefix(status.String(), "WORKFLOW_EXECUTION_STATUS_"))
	}
}
