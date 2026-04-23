package service

type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

type ExecutionError struct {
	Message string
	Details map[string]any
}

func (e ExecutionError) Error() string {
	return e.Message
}
