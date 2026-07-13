package tools

type ErrorCode string

const (
	ErrorToolNotFound      ErrorCode = "tool_not_found"
	ErrorInvalidArgs       ErrorCode = "invalid_arguments"
	ErrorExecutionFailed   ErrorCode = "execution_failed"
	ErrorExecutionTimeout  ErrorCode = "execution_timeout"
	ErrorExecutionCanceled ErrorCode = "execution_canceled"
	ErrorResultEncoding    ErrorCode = "result_encoding_failed"
)

type ExecutionError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Cause   error     `json:"-"`
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
