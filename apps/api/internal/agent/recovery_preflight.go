package agent

// PreExecutionRejection marks recovery validation before resumed execution is
// accepted. Errors after that boundary must not be wrapped.
type PreExecutionRejection struct {
	Err error
}

func (e *PreExecutionRejection) Error() string { return e.Err.Error() }
func (e *PreExecutionRejection) Unwrap() error { return e.Err }

func rejectRecovery(err error) error { return &PreExecutionRejection{Err: err} }
