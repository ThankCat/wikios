package runtime

import "context"

type Runtime struct {
	Registry  *Registry
	Policy    *PolicyEngine
	Validator *Validator
	Auditor   *AuditLogger
}

func NewRuntime(registry *Registry, policy *PolicyEngine, validator *Validator, auditor *AuditLogger) *Runtime {
	return &Runtime{
		Registry:  registry,
		Policy:    policy,
		Validator: validator,
		Auditor:   auditor,
	}
}

func (r *Runtime) Execute(ctx context.Context, env *ExecEnv, call ToolCall) (ToolResult, error) {
	tool, ok := r.Registry.Get(call.Name)
	if !ok {
		return ToolResult{
			Success: false,
			Error: &ToolError{
				Code:    "TOOL_NOT_FOUND",
				Message: "tool not found",
			},
		}, nil
	}

	if err := r.Validator.Validate(call, tool); err != nil {
		return ToolResult{
			Success:   false,
			RiskLevel: tool.RiskLevel(),
			Error: &ToolError{
				Code:    "INVALID_ARGS",
				Message: err.Error(),
			},
		}, nil
	}

	if err := r.Policy.Allow(env, tool, call.Args); err != nil {
		return ToolResult{
			Success:   false,
			RiskLevel: tool.RiskLevel(),
			Error: &ToolError{
				Code:    "POLICY_DENIED",
				Message: err.Error(),
			},
		}, nil
	}

	result, err := tool.Execute(ctx, env, call.Args)
	if result.RiskLevel == "" {
		result.RiskLevel = tool.RiskLevel()
	}
	r.Auditor.Log(ctx, env, call, result, err)
	return result, err
}
