package report

type Report struct {
	TaskID        string     `json:"task_id"`
	TaskType      string     `json:"task_type"`
	Title         string     `json:"title"`
	Summary       string     `json:"summary"`
	Inputs        []Field    `json:"inputs,omitempty"`
	Outputs       []Field    `json:"outputs,omitempty"`
	Timeline      []Event    `json:"timeline"`
	Findings      []Finding  `json:"findings"`
	Artifacts     []Artifact `json:"artifacts,omitempty"`
	LowRiskFixes  []string   `json:"low_risk_fixes,omitempty"`
	Proposals     []Proposal `json:"proposals,omitempty"`
	NextActions   []string   `json:"next_actions,omitempty"`
	OutputFiles   []string   `json:"output_files,omitempty"`
	TriggeredSync bool       `json:"triggered_sync"`
}

type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Event struct {
	Step       string `json:"step"`
	Tool       string `json:"tool,omitempty"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Message    string `json:"message,omitempty"`
}

type Finding struct {
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type Proposal struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	RiskLevel   string   `json:"risk_level"`
	TargetFiles []string `json:"target_files"`
	Summary     string   `json:"summary"`
}

type Artifact struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Path  string `json:"path"`
}
