package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"wikios/internal/llm"
)

type DirectAdminRequest struct {
	Message     string
	ModeHint    string
	History     []ChatMessage
	Attachments []DirectAdminAttachment
	Context     map[string]any
}

type DirectAdminAttachment struct {
	Path string
	Kind string
	Name string
}

type DirectAdminService struct {
	baseService
}

type directAdminAction struct {
	Action      string   `json:"action"`
	Command     string   `json:"command"`
	Reason      string   `json:"reason"`
	Reply       string   `json:"reply"`
	Summary     string   `json:"summary"`
	Answer      string   `json:"answer"`
	Artifacts   []string `json:"artifacts"`
	OutputFile  string   `json:"output_file"`
	OutputFiles []string `json:"output_files"`
	Warnings    []string `json:"warnings"`
	ReportFile  string   `json:"report_file"`
}

func NewDirectAdminService(deps Deps) *DirectAdminService {
	return &DirectAdminService{baseService: newBaseService(deps)}
}

func (s *DirectAdminService) Run(ctx context.Context, execution *Execution, traceID string, req DirectAdminRequest) (map[string]any, error) {
	env := s.env("admin_direct", traceID, execution.ID, "")
	messages := s.InitialMessages(req)
	commands := make([]map[string]any, 0, 24)
	const maxIterations = 24
	for iteration := 0; iteration < maxIterations; iteration++ {
		text, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, messages, fmt.Sprintf("llm direct admin %d", iteration+1))
		if err != nil {
			return nil, err
		}
		action, ok := parseDirectAdminAction(text)
		if !ok {
			reply := strings.TrimSpace(text)
			if reply == "" {
				reply = "直连模式执行完成。"
			}
			return normalizeDirectResult(map[string]any{
				"reply":        reply,
				"answer":       reply,
				"summary":      directModeSummary(req.ModeHint, "管理员直连执行完成"),
				"commands":     commands,
				"raw_response": text,
			}), nil
		}
		switch strings.ToLower(strings.TrimSpace(action.Action)) {
		case "shell":
			command := strings.TrimSpace(action.Command)
			if command == "" {
				return nil, fmt.Errorf("danger direct mode returned empty shell command")
			}
			result, err := s.executeTool(ctx, execution, env, "exec.shell", map[string]any{"command": command}, "direct shell")
			commandRecord := map[string]any{
				"command": command,
				"reason":  strings.TrimSpace(action.Reason),
			}
			if result.Data != nil {
				for key, value := range result.Data {
					commandRecord[key] = value
				}
			}
			if err != nil {
				commandRecord["error"] = err.Error()
			}
			commands = append(commands, commandRecord)
			messages = append(messages,
				llm.Message{Role: "assistant", Content: text},
				llm.Message{Role: "user", Content: directShellResultPrompt(commandRecord)},
			)
			if iteration >= maxIterations-3 {
				return s.forceDirectFinal(ctx, execution, messages, commands, "已经达到命令执行上限，请不要再输出 shell，直接给出 final 总结。")
			}
			continue
		case "final":
			reply := strings.TrimSpace(action.Reply)
			if reply == "" {
				reply = strings.TrimSpace(action.Answer)
			}
			if reply == "" {
				reply = strings.TrimSpace(action.Summary)
			}
			if reply == "" {
				reply = "直连模式执行完成。"
			}
			return normalizeDirectResult(map[string]any{
				"reply":        reply,
				"answer":       firstNonEmpty(strings.TrimSpace(action.Answer), reply),
				"summary":      firstNonEmpty(strings.TrimSpace(action.Summary), directModeSummary(req.ModeHint, "管理员直连执行完成")),
				"artifacts":    action.Artifacts,
				"output_file":  strings.TrimSpace(action.OutputFile),
				"output_files": action.OutputFiles,
				"warnings":     action.Warnings,
				"report_file":  strings.TrimSpace(action.ReportFile),
				"commands":     commands,
				"raw_response": text,
			}), nil
		default:
			reply := strings.TrimSpace(action.Reply)
			if reply == "" {
				reply = strings.TrimSpace(text)
			}
			if reply == "" {
				reply = "直连模式执行完成。"
			}
			return normalizeDirectResult(map[string]any{
				"reply":        reply,
				"answer":       reply,
				"summary":      directModeSummary(req.ModeHint, "管理员直连执行完成"),
				"commands":     commands,
				"raw_response": text,
			}), nil
		}
	}
	return s.forceDirectFinal(ctx, execution, messages, commands, "命令执行轮次已耗尽，请立即返回 final，总结已执行命令和当前结果。")
}

func (s *DirectAdminService) InitialMessages(req DirectAdminRequest) []llm.Message {
	systemPrompt := directAdminSystemPrompt(s.deps.Config.MountedWiki.Root)
	if guide := s.directAdminWikiModeGuide(req.ModeHint); strings.TrimSpace(guide) != "" {
		systemPrompt += "\n\n" + guide
	}
	messages := []llm.Message{{Role: "system", Content: systemPrompt}}
	for _, item := range req.History {
		role := strings.TrimSpace(strings.ToLower(item.Role))
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		if role != "assistant" {
			role = "user"
		}
		messages = append(messages, llm.Message{Role: role, Content: content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: directAdminCurrentUserPrompt(req)})
	return messages
}

func (s *DirectAdminService) directAdminWikiModeGuide(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "query":
		return s.loadWikiAgentSections("当前 mode_hint=query 的 Wiki 规则",
			"## 系统概述",
			"## QUERY 操作规范",
			"## Source Integrity Rules",
		)
	case "ingest", "upload":
		return s.loadWikiAgentSections("当前 mode_hint=ingest 的 Wiki 规则",
			"## 系统概述",
			"## INGEST 操作规范",
			"## Wikilink 使用规范",
			"## Wiki 语言规范",
			"## Confidence 更新规则",
			"## Source Integrity Rules",
			"## 系统文件隔离规则",
			"## 报告命名规范",
		)
	case "lint":
		return s.loadWikiAgentSections("当前 mode_hint=lint 的 Wiki 规则",
			"## 系统概述",
			"## LINT 操作规范",
			"## Source Integrity Rules",
			"## 系统文件隔离规则",
			"## 报告命名规范",
		)
	case "reflect":
		return s.loadWikiAgentSections("当前 mode_hint=reflect 的 Wiki 规则",
			"## 系统概述",
			"## REFLECT 操作规范",
			"## Wikilink 使用规范",
			"## Wiki 语言规范",
			"## Confidence 更新规则",
			"## Source Integrity Rules",
			"## 系统文件隔离规则",
			"## 报告命名规范",
		)
	case "repair":
		return s.loadWikiAgentSections("当前 mode_hint=repair 的 Wiki 规则",
			"## 系统概述",
			"## REPAIR 操作规范",
			"## LINT 操作规范",
			"## Wikilink 使用规范",
			"## Wiki 语言规范",
			"## Source Integrity Rules",
			"## 系统文件隔离规则",
			"## 报告命名规范",
		)
	case "merge":
		return s.loadWikiAgentSections("当前 mode_hint=merge 的 Wiki 规则",
			"## 系统概述",
			"## MERGE 操作规范",
			"## Wikilink 使用规范",
			"## Wiki 语言规范",
			"## 系统文件隔离规则",
			"## 报告命名规范",
		)
	case "add-question":
		return s.loadWikiAgentSections("当前 mode_hint=add-question 的 Wiki 规则",
			"## 系统概述",
			"## ADD-QUESTION 操作规范",
			"## 系统文件隔离规则",
		)
	case "sync":
		return s.loadWikiAgentSections("当前 mode_hint=sync 的 Wiki 规则",
			"## 系统概述",
			"## SYNC 操作规范",
			"## LINT 操作规范",
			"## 系统文件隔离规则",
			"## 报告命名规范",
		)
	}
	return ""
}

func directAdminSystemPrompt(wikiRoot string) string {
	return strings.TrimSpace(fmt.Sprintf(`
你处于 WikiOS 管理员全权限直连模式。

当前挂载知识库根目录：
%s

要求：
1. 除 public query 外，管理员能力全部默认走直连模式；不要走服务层封装方法，不要假设存在 query/ingest/lint/repair/sync 等内置动作。
2. 你唯一可用的执行能力是 exec.shell，它会在挂载知识库根目录下执行任意 shell 命令。
3. 你可以根据 mode_hint 和上下文，自行决定检索、读取、写入、修复、反思、同步、qmd、git、脚本等执行流程。
4. 管理员只关心任务是否成功、做了什么、产物在哪，不需要解释实现细节。
5. 如果 mode_hint=query，你服务的是管理员，不是客户；允许输出内部文件、命令、来源和限制信息。
6. 如果上下文已经给出上传后的 stored_path、source_format、FAQ 分段计划或当前 segment 预览，优先基于这些预处理结果继续执行，不要重新实现上传预处理。
7. 如果正在处理 FAQ segment，只处理当前 segment，不要把整份 FAQ 全文重新塞回上下文。
8. 如果当前 mode_hint 注入了 mounted wiki 的 AGENT.md 规则，AGENT.md 是 Wiki 治理规则的最高优先级来源；除 server 安全与权限边界外，任何 ingest/lint/repair/reflect/merge/query/wikilink/目录/报告规则冲突时都以 AGENT.md 为准。
9. 每一轮只返回一个 JSON 对象，不要输出 Markdown，不要输出代码块。

返回格式二选一：
{"action":"shell","command":"<shell command>","reason":"<why>"}
{"action":"final","reply":"<admin visible reply>","summary":"<short summary>","answer":"<optional answer>","artifacts":["<path>"],"output_file":"<path>","output_files":["<path>"],"report_file":"<path>","warnings":["<warning>"]}

当 shell 执行完成后，你会收到命令结果，再决定下一步。
如果任务已经完成，就返回 final。`, wikiRoot))
}

func directAdminCurrentUserPrompt(req DirectAdminRequest) string {
	var b strings.Builder
	if mode := strings.TrimSpace(req.ModeHint); mode != "" {
		b.WriteString("模式提示：\n")
		b.WriteString(mode)
		b.WriteString("\n\n")
	}
	if state := directSessionStateSummary(req.Context); state != "" {
		b.WriteString("会话状态：\n")
		b.WriteString(state)
		b.WriteString("\n\n")
	}
	if contextSummary := directContextSummary(req.Context); contextSummary != "" {
		b.WriteString("请求上下文：\n")
		b.WriteString(contextSummary)
		b.WriteString("\n\n")
	}
	if len(req.Attachments) > 0 {
		b.WriteString("当前附件：\n")
		for _, item := range req.Attachments {
			b.WriteString("- ")
			b.WriteString(firstNonEmpty(strings.TrimSpace(item.Name), strings.TrimSpace(item.Path)))
			if item.Kind != "" {
				b.WriteString(" (")
				b.WriteString(item.Kind)
				b.WriteString(")")
			}
			if item.Path != "" {
				b.WriteString(" -> ")
				b.WriteString(item.Path)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("管理员请求：\n")
	b.WriteString(strings.TrimSpace(req.Message))
	return strings.TrimSpace(b.String())
}

func directShellResultPrompt(result map[string]any) string {
	return fmt.Sprintf(
		"shell_result:\ncommand: %s\ncwd: %s\nexit_code: %v\nstdout:\n%s\nstderr:\n%s",
		directStringValue(result, "command"),
		directStringValue(result, "cwd"),
		result["exit_code"],
		trimShellOutput(directStringValue(result, "stdout")),
		trimShellOutput(directStringValue(result, "stderr")),
	)
}

func trimShellOutput(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 4000 {
		return text[:4000] + "\n...[truncated]"
	}
	return text
}

func directStringValue(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	raw, ok := data[key]
	if !ok {
		return ""
	}
	value, _ := raw.(string)
	return value
}

func (s *DirectAdminService) forceDirectFinal(
	ctx context.Context,
	execution *Execution,
	messages []llm.Message,
	commands []map[string]any,
	instruction string,
) (map[string]any, error) {
	forceMessages := append(append([]llm.Message{}, messages...),
		llm.Message{Role: "user", Content: instruction},
	)
	text, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, forceMessages, "llm direct admin finalizer")
	if err != nil {
		return normalizeDirectResult(map[string]any{
			"reply":        "管理员直连模式已停止继续执行命令，请查看详情中的命令记录。",
			"summary":      directModeSummary("", "管理员直连模式达到执行上限"),
			"commands":     commands,
			"raw_response": "",
		}), nil
	}
	if action, ok := parseDirectAdminAction(text); ok {
		reply := strings.TrimSpace(action.Reply)
		if reply == "" {
			reply = strings.TrimSpace(action.Answer)
		}
		if reply == "" {
			reply = strings.TrimSpace(action.Summary)
		}
		if reply == "" {
			reply = "管理员直连模式已停止继续执行命令，请查看详情中的命令记录。"
		}
		return normalizeDirectResult(map[string]any{
			"reply":        reply,
			"answer":       firstNonEmpty(strings.TrimSpace(action.Answer), reply),
			"summary":      firstNonEmpty(strings.TrimSpace(action.Summary), directModeSummary("", "管理员直连模式达到执行上限")),
			"artifacts":    action.Artifacts,
			"output_file":  strings.TrimSpace(action.OutputFile),
			"output_files": action.OutputFiles,
			"warnings":     action.Warnings,
			"report_file":  strings.TrimSpace(action.ReportFile),
			"commands":     commands,
			"raw_response": text,
		}), nil
	}
	reply := strings.TrimSpace(text)
	if reply == "" {
		reply = "管理员直连模式已停止继续执行命令，请查看详情中的命令记录。"
	}
	return normalizeDirectResult(map[string]any{
		"reply":        reply,
		"answer":       reply,
		"summary":      directModeSummary("", "管理员直连模式达到执行上限"),
		"commands":     commands,
		"raw_response": text,
	}), nil
}

var directFieldPatterns = map[string]*regexp.Regexp{
	"action":  regexp.MustCompile(`(?s)"action"\s*:\s*"([^"]*)"`),
	"command": regexp.MustCompile(`(?s)"command"\s*:\s*"([^"]*)"`),
	"reason":  regexp.MustCompile(`(?s)"reason"\s*:\s*"([^"]*)"`),
	"reply":   regexp.MustCompile(`(?s)"reply"\s*:\s*"(.*?)"\s*,\s*"summary"`),
	"answer":  regexp.MustCompile(`(?s)"answer"\s*:\s*"(.*?)"(?:\s*,|\s*})`),
	"summary": regexp.MustCompile(`(?s)"summary"\s*:\s*"(.*?)"\s*}`),
}

func parseDirectAdminAction(text string) (directAdminAction, bool) {
	action := directAdminAction{}
	if err := llm.DecodeJSONObject(text, &action); err == nil {
		return action, true
	}
	extracted := directAdminAction{
		Action:  extractDirectField(text, "action"),
		Command: extractDirectField(text, "command"),
		Reason:  extractDirectField(text, "reason"),
		Reply:   extractDirectField(text, "reply"),
		Answer:  extractDirectField(text, "answer"),
		Summary: extractDirectField(text, "summary"),
	}
	if strings.TrimSpace(extracted.Action) == "" && strings.TrimSpace(extracted.Reply) == "" && strings.TrimSpace(extracted.Answer) == "" && strings.TrimSpace(extracted.Command) == "" {
		return directAdminAction{}, false
	}
	return extracted, true
}

func extractDirectField(text string, key string) string {
	pattern, ok := directFieldPatterns[key]
	if !ok {
		return ""
	}
	match := pattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	value := match[1]
	value = strings.ReplaceAll(value, `\"`, `"`)
	value = strings.ReplaceAll(value, `\\n`, "\n")
	value = strings.ReplaceAll(value, `\\t`, "\t")
	value = strings.ReplaceAll(value, `\\`, `\`)
	return strings.TrimSpace(value)
}

func directSessionStateSummary(context map[string]any) string {
	if context == nil {
		return ""
	}
	raw, ok := context["session_state"]
	if !ok {
		return ""
	}
	state, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	lines := []string{}
	appendLine := func(label string, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			lines = append(lines, label+": "+value)
		}
	}
	appendLine("last_mode", directStringValue(state, "lastMode"))
	if summary := strings.TrimSpace(directStringValue(state, "lastSummary")); summary != "" {
		appendLine("last_summary", truncateDirectContextValue(summary, 300))
	} else if reply := strings.TrimSpace(directStringValue(state, "lastReply")); reply != "" {
		appendLine("last_reply", truncateDirectContextValue(reply, 500))
	}
	appendLine("last_report_file", directStringValue(state, "lastReportFile"))
	if values := directStringSlice(state, "uploadedPaths"); len(values) > 0 {
		lines = append(lines, "uploaded_paths: "+strings.Join(values, ", "))
	}
	if values := directStringSlice(state, "lastOutputFiles"); len(values) > 0 {
		lines = append(lines, "last_output_files: "+strings.Join(values, ", "))
	}
	if values := directStringSlice(state, "lastCommands"); len(values) > 0 {
		lines = append(lines, "last_commands: "+strings.Join(values, " | "))
	}
	if values := directStringSlice(state, "lastArtifacts"); len(values) > 0 {
		lines = append(lines, "last_artifacts: "+strings.Join(values, ", "))
	}
	return strings.Join(lines, "\n")
}

func directContextSummary(context map[string]any) string {
	if context == nil {
		return ""
	}
	lines := []string{}
	appendLine := func(label string, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			lines = append(lines, label+": "+truncateDirectContextValue(value, 800))
		}
	}
	appendLine("question", directStringValue(context, "question"))
	appendLine("topic", directStringValue(context, "topic"))
	appendLine("path", directStringValue(context, "path"))
	appendLine("stored_path", directStringValue(context, "stored_path"))
	appendLine("source_format", directStringValue(context, "source_format"))
	appendLine("file_name", directStringValue(context, "file_name"))
	appendLine("ingest_plan", directStringValue(context, "ingest_plan"))
	appendLine("segment_title", directStringValue(context, "segment_title"))
	appendLine("segment_slug", directStringValue(context, "segment_slug"))
	appendLine("segment_category", directStringValue(context, "segment_category"))
	appendLine("segment_preview", directStringValue(context, "segment_preview"))
	if raw, ok := context["segment_index"]; ok {
		lines = append(lines, fmt.Sprintf("segment_index: %v", raw))
	}
	if raw, ok := context["segment_total"]; ok {
		lines = append(lines, fmt.Sprintf("segment_total: %v", raw))
	}
	if raw, ok := context["faq_entry_count"]; ok {
		lines = append(lines, fmt.Sprintf("faq_entry_count: %v", raw))
	}
	return strings.Join(lines, "\n")
}

func directModeSummary(mode string, fallback string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "query":
		return "管理员查询完成"
	case "ingest":
		return "管理员摄入完成"
	case "lint":
		return "管理员检查完成"
	case "reflect":
		return "管理员反思分析完成"
	case "repair":
		return "管理员修复完成"
	case "sync":
		return "管理员同步完成"
	case "upload":
		return "管理员上传处理完成"
	default:
		return fallback
	}
}

func normalizeDirectResult(result map[string]any) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	reply := strings.TrimSpace(directStringValue(result, "reply"))
	answer := strings.TrimSpace(directStringValue(result, "answer"))
	if reply == "" && answer != "" {
		result["reply"] = answer
	}
	if answer == "" && reply != "" {
		result["answer"] = reply
	}
	if _, ok := result["artifacts"]; !ok {
		result["artifacts"] = []string{}
	}
	if _, ok := result["output_files"]; !ok {
		result["output_files"] = []string{}
	}
	if _, ok := result["warnings"]; !ok {
		result["warnings"] = []string{}
	}
	return result
}

func truncateDirectContextValue(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func directStringSlice(data map[string]any, key string) []string {
	if data == nil {
		return nil
	}
	raw, ok := data[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			out = append(out, strings.TrimSpace(value))
		}
		return out
	default:
		return nil
	}
}
