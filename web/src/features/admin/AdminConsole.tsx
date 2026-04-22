import { useMemo, useState } from "react";
import type { AdminChatRequest, AdminMode, AdminStreamEvent, TaskRecord } from "@/types/api";
import { api } from "@/lib/api";
import { formatJSON } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";

type Props = {
  mode: AdminMode;
  token: string;
  onTaskUpdate: (task: TaskRecord) => void;
  onSelectTask: (task: TaskRecord) => void;
};

type PromptMessage = {
  role: string;
  content: string;
};

type PromptTrace = {
  name: string;
  model: string;
  messages: PromptMessage[];
};

type StepTrace = {
  name: string;
  tool?: string;
  status?: string;
  detail?: string;
};

type AdminRun = {
  id: string;
  mode: AdminMode;
  userMessage: string;
  status: "streaming" | "success" | "failed";
  assistantText: string;
  prompts: PromptTrace[];
  steps: StepTrace[];
  task?: TaskRecord | null;
  result?: Record<string, unknown> | null;
  error?: string;
};

type ThreadMap = Record<AdminMode, AdminRun[]>;

const emptyThreads: ThreadMap = {
  query: [],
  ingest: [],
  lint: [],
  reflect: [],
  repair: [],
  sync: [],
};

export function AdminConsole({ mode, token, onTaskUpdate, onSelectTask }: Props) {
  const [threads, setThreads] = useState<ThreadMap>(emptyThreads);
  const [composer, setComposer] = useState<Record<AdminMode, string>>({
    query: "静态IP适用什么场景？",
    ingest: "请摄入新增来源",
    lint: "执行一次健康检查",
    reflect: "请分析当前知识库中的模式与缺口",
    repair: "请执行修复",
    sync: "请同步当前 wiki 改动",
  });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const [queryWriteOutput, setQueryWriteOutput] = useState(true);
  const [ingestPath, setIngestPath] = useState("raw/articles/customer1-5.md");
  const [ingestInteractive, setIngestInteractive] = useState(false);
  const [reflectWriteReport, setReflectWriteReport] = useState(true);
  const [reflectAutoFix, setReflectAutoFix] = useState(false);
  const [lintWriteReport, setLintWriteReport] = useState(true);
  const [lintAutoFix, setLintAutoFix] = useState(false);
  const [repairAction, setRepairAction] = useState<"low_risk" | "proposal">("low_risk");
  const [repairPath, setRepairPath] = useState("wiki/concepts/example.md");
  const [repairOps, setRepairOps] = useState(
    JSON.stringify(
      [
        {
          type: "append_section",
          section: "## Evolution Log",
          content: "- 2026-04-22：手工测试 low risk repair。",
        },
      ],
      null,
      2,
    ),
  );
  const [proposalId, setProposalId] = useState("");
  const [syncMessage, setSyncMessage] = useState("chore: sync wiki updates");

  const runs = threads[mode];

  const title = useMemo(() => {
    switch (mode) {
      case "query":
        return { title: "管理员查询对话", description: "以对话方式发起查询，并实时查看 prompt、流式 LLM 输出和任务结果。" };
      case "ingest":
        return { title: "摄入对话", description: "通过对话驱动来源摄入，同时查看摄入规则 prompt 和结构化提取过程。" };
      case "lint":
        return { title: "健康检查对话", description: "用对话入口触发 lint 和 qmd 检查。" };
      case "reflect":
        return { title: "反思分析对话", description: "查看 reflect 的提示词、流式输出和最终 gap 分析。" };
      case "repair":
        return { title: "修复对话", description: "通过对话方式触发 low-risk repair 或 proposal 应用。" };
      case "sync":
        return { title: "同步对话", description: "用对话入口执行 git status / commit / push。" };
    }
  }, [mode]);

  async function submit() {
    setError("");
    const message = composer[mode].trim();
    const request = buildRequest(mode, message, {
      queryWriteOutput,
      ingestPath,
      ingestInteractive,
      reflectWriteReport,
      reflectAutoFix,
      lintWriteReport,
      lintAutoFix,
      repairAction,
      repairPath,
      repairOps,
      proposalId,
      syncMessage,
    });
    if (!request) {
      setError("当前管理员消息配置无效，请检查 repair JSON 或必要参数。");
      return;
    }
    const runId = crypto.randomUUID();
    const userMessage = request.message || message || friendlyFallback(mode, request.options);
    const nextRun: AdminRun = {
      id: runId,
      mode,
      userMessage,
      status: "streaming",
      assistantText: "",
      prompts: [],
      steps: [],
      task: null,
      result: null,
    };
    setThreads((current) => ({
      ...current,
      [mode]: [...current[mode], nextRun],
    }));
    setLoading(true);
    try {
      await api.adminChatStream(token, request, (event) => {
        handleStreamEvent(mode, runId, event);
      });
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : "stream request failed";
      setError(message);
      updateRun(mode, runId, (run) => ({
        ...run,
        status: "failed",
        error: message,
      }));
    } finally {
      setLoading(false);
    }
  }

  function handleStreamEvent(currentMode: AdminMode, runId: string, event: AdminStreamEvent) {
    switch (event.type) {
      case "prompt": {
        const data = event.data as { name?: string; model?: string; messages?: PromptMessage[] };
        updateRun(currentMode, runId, (run) => ({
          ...run,
          prompts: [
            ...run.prompts,
            {
              name: data.name ?? "prompt",
              model: data.model ?? "",
              messages: Array.isArray(data.messages) ? data.messages : [],
            },
          ],
        }));
        break;
      }
      case "llm_delta": {
        const data = event.data as { delta?: string };
        updateRun(currentMode, runId, (run) => ({
          ...run,
          assistantText: run.assistantText + (data.delta ?? ""),
        }));
        break;
      }
      case "step_start": {
        const data = event.data as { name?: string; tool?: string };
        updateRun(currentMode, runId, (run) => ({
          ...run,
          steps: [...run.steps, { name: data.name ?? "step", tool: data.tool, status: "RUNNING" }],
        }));
        break;
      }
      case "step_finish": {
        const data = event.data as { name?: string; tool?: string; status?: string; output?: Record<string, unknown> };
        updateRun(currentMode, runId, (run) => ({
          ...run,
          steps: run.steps
            .filter((step) => !(step.name === data.name && step.status === "RUNNING"))
            .concat({
              name: data.name ?? "step",
              tool: data.tool,
              status: data.status,
              detail: typeof data.output?.error === "string" ? data.output.error : "",
            }),
        }));
        break;
      }
      case "task": {
        const task = event.data as TaskRecord;
        updateRun(currentMode, runId, (run) => ({
          ...run,
          task,
          status:
            task.status === "SUCCESS" ? "success" : task.status === "FAILED" ? "failed" : run.status,
          error: task.error ?? run.error,
        }));
        onTaskUpdate(task);
        onSelectTask(task);
        break;
      }
      case "result": {
        const result = (event.data ?? {}) as Record<string, unknown>;
        updateRun(currentMode, runId, (run) => ({
          ...run,
          result,
          assistantText: run.assistantText || deriveAssistantText(result),
        }));
        break;
      }
      case "error": {
        const data = event.data as { message?: string };
        updateRun(currentMode, runId, (run) => ({
          ...run,
          status: "failed",
          error: data.message ?? "request failed",
        }));
        break;
      }
      case "done": {
        updateRun(currentMode, runId, (run) => ({
          ...run,
          status: run.error ? "failed" : run.status === "streaming" ? "success" : run.status,
        }));
        break;
      }
    }
  }

  function updateRun(currentMode: AdminMode, runId: string, mutate: (run: AdminRun) => AdminRun) {
    setThreads((current) => ({
      ...current,
      [currentMode]: current[currentMode].map((run) => (run.id === runId ? mutate(run) : run)),
    }));
  }

  return (
    <Card className="flex h-full flex-col">
      <CardHeader>
        <CardTitle>{title.title}</CardTitle>
        <CardDescription>{title.description}</CardDescription>
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col gap-4">
        {renderControls(mode, {
          queryWriteOutput,
          setQueryWriteOutput,
          ingestPath,
          setIngestPath,
          ingestInteractive,
          setIngestInteractive,
          reflectWriteReport,
          setReflectWriteReport,
          reflectAutoFix,
          setReflectAutoFix,
          lintWriteReport,
          setLintWriteReport,
          lintAutoFix,
          setLintAutoFix,
          repairAction,
          setRepairAction,
          repairPath,
          setRepairPath,
          repairOps,
          setRepairOps,
          proposalId,
          setProposalId,
          syncMessage,
          setSyncMessage,
        })}
        {error && (
          <div className="rounded-2xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
            {error}
          </div>
        )}
        <ScrollArea className="min-h-0 flex-1 pr-3">
          <div className="space-y-4">
            {runs.length === 0 ? (
              <div className="rounded-3xl border border-dashed border-border p-8 text-sm text-muted-foreground">
                发送一条管理员消息后，这里会显示对话、prompt、流式 LLM 输出、步骤事件和任务结果。
              </div>
            ) : (
              runs.map((run) => (
                <div key={run.id} className="space-y-3 rounded-3xl border border-border bg-white/70 p-4">
                  <div className="rounded-2xl bg-slate-900 px-4 py-3 text-sm text-slate-100">{run.userMessage}</div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant={run.status === "success" ? "success" : run.status === "failed" ? "danger" : "warning"}>
                      {run.status}
                    </Badge>
                    {run.task?.id && <Badge>{run.task.id}</Badge>}
                  </div>
                  {run.prompts.map((prompt, index) => (
                    <details key={`${run.id}-prompt-${index}`} className="rounded-2xl border border-border bg-slate-50 p-3">
                      <summary className="cursor-pointer text-sm font-medium">
                        查看提示词: {prompt.name} {prompt.model ? `(${prompt.model})` : ""}
                      </summary>
                      <div className="mt-3 space-y-3">
                        {prompt.messages.map((message, messageIndex) => (
                          <div key={`${run.id}-prompt-${index}-${messageIndex}`}>
                            <p className="mb-1 text-xs uppercase tracking-[0.2em] text-muted-foreground">{message.role}</p>
                            <pre className="overflow-auto rounded-2xl bg-slate-950 p-3 text-xs text-slate-100">
                              {message.content}
                            </pre>
                          </div>
                        ))}
                      </div>
                    </details>
                  ))}
                  {run.assistantText && (
                    <pre className="overflow-auto rounded-2xl border border-border bg-slate-950 p-4 text-xs text-slate-100">
                      {run.assistantText}
                    </pre>
                  )}
                  {run.steps.length > 0 && (
                    <div className="space-y-2">
                      <p className="text-xs uppercase tracking-[0.2em] text-muted-foreground">Steps</p>
                      {run.steps.map((step, index) => (
                        <div key={`${run.id}-step-${index}`} className="rounded-2xl border border-border p-3">
                          <div className="flex items-center justify-between gap-3">
                            <div>
                              <p className="text-sm font-medium">{step.name}</p>
                              {step.tool && <p className="mt-1 font-mono text-xs text-muted-foreground">{step.tool}</p>}
                            </div>
                            <Badge variant={step.status === "SUCCESS" ? "success" : step.status === "FAILED" ? "danger" : "warning"}>
                              {step.status ?? "RUNNING"}
                            </Badge>
                          </div>
                          {step.detail && <p className="mt-2 text-sm text-rose-600">{step.detail}</p>}
                        </div>
                      ))}
                    </div>
                  )}
                  {run.result && (
                    <pre className="overflow-auto rounded-2xl bg-slate-100 p-3 text-xs text-slate-700">
                      {formatJSON(run.result)}
                    </pre>
                  )}
                  {run.error && (
                    <div className="rounded-2xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
                      {run.error}
                    </div>
                  )}
                </div>
              ))
            )}
          </div>
        </ScrollArea>
        <div className="space-y-3 border-t border-border pt-4">
          <Textarea
            value={composer[mode]}
            onChange={(event) =>
              setComposer((current) => ({
                ...current,
                [mode]: event.target.value,
              }))
            }
            placeholder={placeholderForMode(mode)}
          />
          <div className="flex justify-end">
            <Button disabled={loading} onClick={() => void submit()}>
              发送管理员消息
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function deriveAssistantText(result: Record<string, unknown>) {
  if (typeof result.answer === "string" && result.answer.trim() !== "") {
    return result.answer;
  }
  if (typeof result.summary === "string" && result.summary.trim() !== "") {
    return result.summary;
  }
  return formatJSON(result);
}

function friendlyFallback(mode: AdminMode, options?: Record<string, unknown>) {
  switch (mode) {
    case "ingest":
      return String(options?.path ?? "执行摄入");
    case "sync":
      return String(options?.message ?? "执行同步");
    case "repair":
      return String(options?.proposal_id ?? options?.path ?? "执行修复");
    default:
      return "执行管理员操作";
  }
}

function placeholderForMode(mode: AdminMode) {
  switch (mode) {
    case "query":
      return "例如：静态IP适用什么场景？";
    case "ingest":
      return "例如：请摄入这个来源，并严格遵守当前 wiki 的 AGENT 规则";
    case "lint":
      return "例如：请检查当前 wiki 是否健康";
    case "reflect":
      return "例如：请分析当前知识库中的内容空白和矛盾";
    case "repair":
      return "例如：请应用这个修复并说明理由";
    case "sync":
      return "例如：请同步当前 wiki 改动";
  }
}

function buildRequest(
  mode: AdminMode,
  message: string,
  options: {
    queryWriteOutput: boolean;
    ingestPath: string;
    ingestInteractive: boolean;
    reflectWriteReport: boolean;
    reflectAutoFix: boolean;
    lintWriteReport: boolean;
    lintAutoFix: boolean;
    repairAction: "low_risk" | "proposal";
    repairPath: string;
    repairOps: string;
    proposalId: string;
    syncMessage: string;
  },
): AdminChatRequest | null {
  switch (mode) {
    case "query":
      return {
        mode,
        message,
        options: { write_output: options.queryWriteOutput },
      };
    case "ingest":
      return {
        mode,
        message,
        options: {
          input_type: "file",
          path: options.ingestPath,
          interactive: options.ingestInteractive,
        },
      };
    case "lint":
      return {
        mode,
        message,
        options: {
          write_report: options.lintWriteReport,
          auto_fix_low_risk: options.lintAutoFix,
        },
      };
    case "reflect":
      return {
        mode,
        message,
        options: {
          write_report: options.reflectWriteReport,
          auto_fix_low_risk: options.reflectAutoFix,
        },
      };
    case "repair": {
      if (options.repairAction === "proposal") {
        return {
          mode,
          message,
          options: {
            action: "proposal",
            proposal_id: options.proposalId,
          },
        };
      }
      let ops: unknown;
      try {
        ops = JSON.parse(options.repairOps);
      } catch {
        return null;
      }
      return {
        mode,
        message,
        options: {
          action: "low_risk",
          path: options.repairPath,
          ops,
        },
      };
    }
    case "sync":
      return {
        mode,
        message,
        options: { message: options.syncMessage },
      };
  }
}

function renderControls(
  mode: AdminMode,
  state: {
    queryWriteOutput: boolean;
    setQueryWriteOutput: (value: boolean) => void;
    ingestPath: string;
    setIngestPath: (value: string) => void;
    ingestInteractive: boolean;
    setIngestInteractive: (value: boolean) => void;
    reflectWriteReport: boolean;
    setReflectWriteReport: (value: boolean) => void;
    reflectAutoFix: boolean;
    setReflectAutoFix: (value: boolean) => void;
    lintWriteReport: boolean;
    setLintWriteReport: (value: boolean) => void;
    lintAutoFix: boolean;
    setLintAutoFix: (value: boolean) => void;
    repairAction: "low_risk" | "proposal";
    setRepairAction: (value: "low_risk" | "proposal") => void;
    repairPath: string;
    setRepairPath: (value: string) => void;
    repairOps: string;
    setRepairOps: (value: string) => void;
    proposalId: string;
    setProposalId: (value: string) => void;
    syncMessage: string;
    setSyncMessage: (value: string) => void;
  },
) {
  if (mode === "query") {
    return (
      <label className="flex items-center gap-3 text-sm text-muted-foreground">
        <input
          checked={state.queryWriteOutput}
          className="h-4 w-4 rounded border-border"
          onChange={(event) => state.setQueryWriteOutput(event.target.checked)}
          type="checkbox"
        />
        write_output
      </label>
    );
  }
  if (mode === "ingest") {
    return (
      <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_160px]">
        <Input value={state.ingestPath} onChange={(event) => state.setIngestPath(event.target.value)} placeholder="raw/articles/..." />
        <label className="flex items-center gap-3 text-sm text-muted-foreground">
          <input
            checked={state.ingestInteractive}
            className="h-4 w-4 rounded border-border"
            onChange={(event) => state.setIngestInteractive(event.target.checked)}
            type="checkbox"
          />
          interactive
        </label>
      </div>
    );
  }
  if (mode === "lint") {
    return (
      <div className="flex flex-wrap gap-4 text-sm text-muted-foreground">
        <label className="flex items-center gap-3">
          <input
            checked={state.lintWriteReport}
            className="h-4 w-4 rounded border-border"
            onChange={(event) => state.setLintWriteReport(event.target.checked)}
            type="checkbox"
          />
          write_report
        </label>
        <label className="flex items-center gap-3">
          <input
            checked={state.lintAutoFix}
            className="h-4 w-4 rounded border-border"
            onChange={(event) => state.setLintAutoFix(event.target.checked)}
            type="checkbox"
          />
          auto_fix_low_risk
        </label>
      </div>
    );
  }
  if (mode === "reflect") {
    return (
      <div className="flex flex-wrap gap-4 text-sm text-muted-foreground">
        <label className="flex items-center gap-3">
          <input
            checked={state.reflectWriteReport}
            className="h-4 w-4 rounded border-border"
            onChange={(event) => state.setReflectWriteReport(event.target.checked)}
            type="checkbox"
          />
          write_report
        </label>
        <label className="flex items-center gap-3">
          <input
            checked={state.reflectAutoFix}
            className="h-4 w-4 rounded border-border"
            onChange={(event) => state.setReflectAutoFix(event.target.checked)}
            type="checkbox"
          />
          auto_fix_low_risk
        </label>
      </div>
    );
  }
  if (mode === "repair") {
    return (
      <div className="space-y-3">
        <div className="flex flex-wrap gap-3">
          <Button
            size="sm"
            type="button"
            variant={state.repairAction === "low_risk" ? "default" : "outline"}
            onClick={() => state.setRepairAction("low_risk")}
          >
            low risk
          </Button>
          <Button
            size="sm"
            type="button"
            variant={state.repairAction === "proposal" ? "default" : "outline"}
            onClick={() => state.setRepairAction("proposal")}
          >
            proposal
          </Button>
        </div>
        {state.repairAction === "low_risk" ? (
          <div className="space-y-3">
            <Input value={state.repairPath} onChange={(event) => state.setRepairPath(event.target.value)} placeholder="wiki/concepts/..." />
            <Textarea value={state.repairOps} onChange={(event) => state.setRepairOps(event.target.value)} />
          </div>
        ) : (
          <Input value={state.proposalId} onChange={(event) => state.setProposalId(event.target.value)} placeholder="proposal_xxx" />
        )}
      </div>
    );
  }
  return <Input value={state.syncMessage} onChange={(event) => state.setSyncMessage(event.target.value)} placeholder="commit message" />;
}
