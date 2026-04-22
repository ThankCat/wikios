package runtime

import "fmt"

type Validator struct{}

func NewValidator() *Validator {
	return &Validator{}
}

func (v *Validator) Validate(call ToolCall, tool Tool) error {
	if call.Name == "" {
		return fmt.Errorf("tool name is required")
	}
	if call.Args == nil {
		call.Args = map[string]any{}
	}
	return tool.Validate(call.Args)
}
