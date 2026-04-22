package report

import (
	"fmt"
	"strings"
)

func Markdown(r Report) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`# %s Report

## Summary

%s

## Timeline

`, strings.Title(r.TaskType), r.Summary))
	for _, event := range r.Timeline {
		b.WriteString(fmt.Sprintf("- %s | %s | %s\n", event.Step, event.Status, event.Message))
	}
	if len(r.Findings) > 0 {
		b.WriteString("\n## Findings\n\n")
		for _, finding := range r.Findings {
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", finding.Level, finding.Title, finding.Detail))
		}
	}
	if len(r.Proposals) > 0 {
		b.WriteString("\n## Proposals\n\n")
		for _, proposal := range r.Proposals {
			b.WriteString(fmt.Sprintf("- %s (%s): %s\n", proposal.Title, proposal.RiskLevel, proposal.Summary))
		}
	}
	return b.String()
}
