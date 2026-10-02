package httpapi

import (
	"errors"
	"net/http"

	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

func modelAdmissionFailureStatus(w http.ResponseWriter, status int, err error) int {
	var admission *requestcontrol.OwnerAdmissionError
	if !errors.As(err, &admission) {
		return status
	}
	switch admission.Code {
	case "model_owner_required", "model_owner_unavailable":
		return http.StatusForbidden
	case "owner_model_queue_full":
		w.Header().Set("Retry-After", overloadRetryAfterSeconds)
		return http.StatusTooManyRequests
	default:
		w.Header().Set("Retry-After", overloadRetryAfterSeconds)
		return http.StatusServiceUnavailable
	}
}
