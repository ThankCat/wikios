package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
				"command":      command,
				"reason":       strings.TrimSpace(action.Reason),
				"tool_success": result.Success,
			}
			if result.Data != nil {
				for key, value := range result.Data {
					commandRecord[key] = value
				}
			}
			if result.Error != nil {
				commandRecord["error_code"] = result.Error.Code
				commandRecord["error_message"] = result.Error.Message
				commandRecord["error"] = result.Error.Message
			}
			if err != nil {
				commandRecord["error"] = err.Error()
				if _, ok := commandRecord["error_message"]; !ok {
					commandRecord["error_message"] = err.Error()
				}
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
			result := map[string]any{
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
			}
			s.maybeRefreshDirectQMD(ctx, execution, traceID, req.ModeHint, result)
			return normalizeDirectResult(result), nil
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
		if strings.TrimSpace(item.CreatedAt) != "" {
			content = "[" + strings.TrimSpace(item.CreatedAt) + "] " + content
		}
		messages = append(messages, llm.Message{Role: role, Content: content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: directAdminCurrentUserPrompt(req)})
	return messages
}

func (s *DirectAdminService) directAdminWikiModeGuide(mode string) string {
	agentPath := filepath.Join(s.deps.Config.MountedWiki.Root, "AGENT.md")
	raw, err := os.ReadFile(agentPath)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return ""
	}
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "query"
	}
	return "【mounted wiki AGENT.md 全文（Wiki 治理规则最高优先级；当前 mode_hint=" + mode + "）】\n" + strings.TrimSpace(string(raw))
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
6. 如果上下文已经给出上传后的 stored_path、source_format 或 document_preview，必须按 AGENT.md 的 INGEST 流程从 raw/ 原始文档读取和处理；server 不做结构化问答、表格、JSON 或分段预处理。
7. AGENT.md 全文是 Wiki 治理规则的最高优先级来源；除 server 安全与权限边界外，任何 ingest/query/lint/repair/reflect/merge/wikilink/目录/报告规则冲突时都以 AGENT.md 为准。
8. raw/ 只读；正式知识只能写入 AGENT.md 规定的 wiki/sources、knowledge、policies、procedures、comparisons、concepts、entities、synthesis、intents 等目录；报告写入根目录 outputs/，并包含 graph-excluded: true。
9. 查询、检查、修复和合并优先使用 AGENT.md 规定的 qmd 命令、scripts/lint.py 和正式知识目录。低风险机械修复可直接做；高风险价格、政策、安全边界、slug 重命名、页面合并或删除必须先向管理员确认。
10. 每一轮只返回一个 JSON 对象，不要输出 Markdown，不要输出代码块。

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
		"shell_result:\ncommand: %s\ncwd: %s\nshell: %s\ntool_success: %v\nexit_code: %v\nerror_code: %s\nerror_message: %s\nerror: %s\nstdout:\n%s\nstderr:\n%s",
		directStringValue(result, "command"),
		directStringValue(result, "cwd"),
		directStringValue(result, "shell"),
		result["tool_success"],
		result["exit_code"],
		directStringValue(result, "error_code"),
		directStringValue(result, "error_message"),
		directStringValue(result, "error"),
		trimShellOutput(directStringValue(result, "stdout")),
		trimShellOutput(directStringValue(result, "stderr")),
	)
}

func (s *DirectAdminService) maybeRefreshDirectQMD(ctx context.Context, execution *Execution, traceID string, mode string, result map[string]any) {
	if execution == nil || result == nil || !directModeShouldRefreshQMD(mode) {
		return
	}
	env := s.env("admin_direct", traceID+"-qmd", execution.ID, "")
	refresh := map[string]any{}
	if collectionResult, err := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{
		"subcommand": "collection_add",
		"path":       "wiki/",
		"name":       "wiki",
	}, "qmd collection add"); err != nil {
		refresh["collection_add_error"] = err.Error()
	} else {
		refresh["collection_add"] = collectionResult.Data
	}
	updateResult, err := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{"subcommand": "update"}, "qmd update")
	if err != nil {
		refresh["update_error"] = err.Error()
		result["qmd_updated"] = false
		appendDirectWarning(result, "qmd 索引刷新仍失败："+err.Error())
		result["qmd_refresh"] = refresh
		return
	}
	refresh["update"] = updateResult.Data
	result["qmd_updated"] = true
	result["qmd_refresh"] = refresh
	reply := strings.TrimSpace(directStringValue(result, "reply"))
	if reply != "" {
		note := "补充：服务端已完成 qmd 索引刷新。"
		lower := strings.ToLower(reply)
		if strings.Contains(lower, "qmd") && (strings.Contains(reply, "失败") || strings.Contains(lower, "failed") || strings.Contains(lower, "node_module_version")) {
			note = "补充：服务端已自动修复并刷新 qmd 索引，上方 qmd 失败提示已过期。"
		}
		result["reply"] = reply + "\n\n" + note
	}
}

func directModeShouldRefreshQMD(mode string) bool {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "ingest", "lint", "repair", "reflect", "merge", "add-question", "upload":
		return true
	default:
		return false
	}
}

func appendDirectWarning(result map[string]any, warning string) {
	warning = strings.TrimSpace(warning)
	if warning == "" {
		return
	}
	switch typed := result["warnings"].(type) {
	case []string:
		result["warnings"] = append(typed, warning)
	case []any:
		result["warnings"] = append(typed, warning)
	default:
		result["warnings"] = []string{warning}
	}
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
	appendLine("document_preview", directStringValue(context, "document_preview"))
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
