package report

import (
	"fmt"
	"strings"
)

func Markdown(r Report) string {
	var b strings.Builder
	title := r.Title
	if title == "" {
		title = strings.Title(r.TaskType) + " Report"
	}
	b.WriteString(fmt.Sprintf(`# %s

## Summary

%s

## Inputs

`, title, nonEmpty(r.Summary)))
	writeFields(&b, r.Inputs)
	b.WriteString("\n## Outputs\n\n")
	writeFields(&b, r.Outputs)
	b.WriteString("\n## Findings\n\n")
	if len(r.Findings) == 0 {
		b.WriteString("- 暂无\n")
	} else {
		for _, finding := range r.Findings {
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", finding.Level, finding.Title, finding.Detail))
		}
	}
	b.WriteString("\n## Artifacts\n\n")
	if len(r.Artifacts) == 0 {
		b.WriteString("- 暂无\n")
	} else {
		for _, artifact := range r.Artifacts {
			b.WriteString(fmt.Sprintf("- %s | %s | %s\n", artifact.Kind, artifact.Label, artifact.Path))
		}
	}
	b.WriteString("\n## Timeline\n\n")
	if len(r.Timeline) == 0 {
		b.WriteString("- 暂无\n")
	}
	for _, event := range r.Timeline {
		b.WriteString(fmt.Sprintf("- %s | %s | %dms | %s\n", event.Step, event.Status, event.DurationMs, nonEmpty(event.Message)))
	}
	if len(r.Proposals) > 0 {
		b.WriteString("\n## Proposals\n\n")
		for _, proposal := range r.Proposals {
			b.WriteString(fmt.Sprintf("- %s (%s): %s\n", proposal.Title, proposal.RiskLevel, proposal.Summary))
		}
	}
	b.WriteString("\n## Next Actions\n\n")
	if len(r.NextActions) == 0 {
		b.WriteString("- 暂无\n")
	} else {
		for _, action := range r.NextActions {
			b.WriteString("- " + action + "\n")
		}
	}
	return b.String()
}

func writeFields(b *strings.Builder, fields []Field) {
	if len(fields) == 0 {
		b.WriteString("- 暂无\n")
		return
	}
	for _, field := range fields {
		b.WriteString(fmt.Sprintf("- %s: %s\n", field.Label, nonEmpty(field.Value)))
	}
}

func nonEmpty(text string) string {
	if strings.TrimSpace(text) == "" {
		return "暂无"
	}
	return text
}
