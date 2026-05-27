"use client";

import { ReactNode, useMemo, useState } from "react";
import { Bot, Braces, BrainCircuit, Cog, FileJson2, MessageSquareQuote } from "lucide-react";

import { ScrollJumpControls } from "@/components/ui/scroll-jump-controls";
import { useScrollFollow } from "@/lib/use-scroll-follow";
import { cn, formatJSON } from "@/lib/utils";

type Props = {
  details: unknown;
  leadingContent?: ReactNode;
  message?: {
    role: "user" | "assistant";
    content: string;
    createdAt?: string;
    statusText?: string;
    answer?: string;
  };
};

type DetailTab = "reasoning" | "model" | "prompt" | "tools" | "execution" | "result";

type TabConfig = {
  id: DetailTab;
  label: string;
  icon: typeof MessageSquareQuote;
};

const tabs: TabConfig[] = [
  { id: "reasoning", label: "推理", icon: BrainCircuit },
  { id: "model", label: "模型", icon: Bot },
  { id: "prompt", label: "Prompt", icon: MessageSquareQuote },
  { id: "tools", label: "步骤", icon: Cog },
  { id: "execution", label: "执行", icon: Braces },
  { id: "result", label: "结果", icon: FileJson2 },
];

function asObject(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

export function MessageDetails({ details, leadingContent, message }: Props) {
  const object = asObject(details);
  const prompts = Array.isArray(object.prompts) ? object.prompts : [];
  const execution = asObject(object.execution);
  const executionSteps = Array.isArray(execution.steps) ? execution.steps : [];
  const steps = Array.isArray(object.steps) ? object.steps : executionSteps;
  const result = object.result ?? object;
  const reasoning = typeof object.reasoning === "string" ? object.reasoning.trim() : "";
  const processSummary = typeof object.process_summary === "string" ? object.process_summary.trim() : "";
  const llmStream = typeof object.llm_stream === "string" ? object.llm_stream.trim() : "";
  const llmDone = asObject(object.llm_done);
  const modelRaw = typeof object.model_json_raw === "string" ? object.model_json_raw.trim() : "";
  const hasModelDetails = llmStream !== "" || modelRaw !== "" || Object.keys(llmDone).length > 0 || object.model_json_parsed != null;

  const availableTabs = useMemo(
    () =>
      tabs.filter((tab) => {
        switch (tab.id) {
          case "reasoning":
            return reasoning.trim() !== "" || processSummary.trim() !== "";
          case "model":
            return hasModelDetails;
          case "prompt":
            return prompts.length > 0;
          case "tools":
            return steps.length > 0;
          case "execution":
            return Object.keys(execution).length > 0;
          case "result":
            return true;
        }
      }),
    [execution, hasModelDetails, processSummary, prompts.length, reasoning, steps.length],
  );

  const [activeTab, setActiveTab] = useState<DetailTab>("result");
  const resolvedTab = availableTabs.some((tab) => tab.id === activeTab) ? activeTab : (availableTabs[0]?.id ?? "result");

  return (
    <div className="min-w-0 space-y-3 break-words text-left [overflow-wrap:anywhere]">
      <div className="sticky top-0 z-10 flex flex-wrap gap-2 border-b border-slate-200 bg-white/95 py-3 backdrop-blur dark:border-border dark:bg-card/95">
        {availableTabs.map((tab) => {
          const Icon = tab.icon;
          const active = resolvedTab === tab.id;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-[11px] font-semibold uppercase tracking-[0.14em] transition",
                active
                  ? "border-slate-900 bg-slate-900 text-white dark:border-white dark:bg-white dark:text-slate-950"
                  : "border-slate-300 bg-white text-slate-600 hover:bg-slate-50 dark:border-border dark:bg-background dark:text-muted-foreground dark:hover:bg-secondary dark:hover:text-foreground",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {leadingContent}

      {resolvedTab === "reasoning" ? <ReasoningPanel reasoning={reasoning} processSummary={processSummary} /> : null}
      {resolvedTab === "model" ? <ModelPanel llmStream={llmStream} llmDone={llmDone} modelRaw={modelRaw} modelParsed={object.model_json_parsed} /> : null}
      {resolvedTab === "prompt" ? <PromptPanel prompts={prompts} /> : null}
      {resolvedTab === "tools" ? <ToolsPanel steps={steps} /> : null}
      {resolvedTab === "execution" ? <ExecutionPanel execution={execution} /> : null}
      {resolvedTab === "result" ? <ResultPanel result={result} message={message} /> : null}
    </div>
  );
}

function ReasoningPanel({ reasoning, processSummary }: { reasoning: string; processSummary: string }) {
  return (
    <div className="min-w-0 space-y-3">
      {reasoning ? (
        <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">Model Reasoning Stream</div>
          <CodeBlock value={reasoning} />
        </section>
      ) : null}
      {processSummary ? (
        <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">Process Summary</div>
          <CodeBlock value={processSummary} />
        </section>
      ) : null}
    </div>
  );
}

function ModelPanel({
  llmStream,
  llmDone,
  modelRaw,
  modelParsed,
}: {
  llmStream: string;
  llmDone: Record<string, unknown>;
  modelRaw: string;
  modelParsed: unknown;
}) {
  return (
    <div className="min-w-0 space-y-3">
      {llmStream ? (
        <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">Answer Markdown Stream</div>
          <CodeBlock value={llmStream} />
        </section>
      ) : null}
      {Object.keys(llmDone).length > 0 ? (
        <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">LLM Done Event</div>
          <CodeBlock value={llmDone} />
        </section>
      ) : null}
      {modelParsed != null ? (
        <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">Parsed Model JSON</div>
          <CodeBlock value={modelParsed} />
        </section>
      ) : null}
      {modelRaw ? (
        <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">Raw Model JSON</div>
          <CodeBlock value={modelRaw} />
        </section>
      ) : null}
    </div>
  );
}

function PromptPanel({ prompts }: { prompts: unknown[] }) {
  return (
    <div className="min-w-0 space-y-3">
      {prompts.map((prompt, index) => {
        const data = asObject(prompt);
        const messages = Array.isArray(data.messages) ? data.messages : [];
        return (
          <details key={index} className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
            <summary className="mb-3 flex cursor-pointer flex-wrap items-start justify-between gap-3">
              <div className="min-w-0 flex-1">
                <div className="break-words text-xs font-semibold text-slate-900 [overflow-wrap:anywhere]">{String(data.name ?? `Prompt ${index + 1}`)}</div>
                <div className="mt-1 break-words text-[11px] text-slate-500 [overflow-wrap:anywhere]">
                  model: {String(data.model ?? "unknown")}
                  {data.created_at ? ` · ${formatDateTime(String(data.created_at))}` : ""}
                </div>
              </div>
              <div className="shrink-0 rounded-full bg-slate-100 px-2 py-1 text-[11px] text-slate-600 dark:bg-secondary dark:text-muted-foreground">{messages.length} messages</div>
            </summary>
            <div className="space-y-2">
              {messages.map((message, messageIndex) => {
                const item = asObject(message);
                return (
                  <div key={messageIndex} className="min-w-0 rounded-xl border border-slate-200 bg-slate-50 p-3 dark:border-border dark:bg-secondary/60">
                    <div className="mb-2 break-words text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">
                      {String(item.role ?? "message")}
                    </div>
                    <CodeBlock value={String(item.content ?? "")} />
                  </div>
                );
              })}
            </div>
          </details>
        );
      })}
    </div>
  );
}

function ToolsPanel({ steps }: { steps: unknown[] }) {
  return (
    <div className="min-w-0 space-y-3">
      {steps.map((step, index) => {
        const item = asObject(step);
        const status = String(item.status ?? "UNKNOWN");
        return (
          <details key={index} className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
            <summary className="mb-3 flex cursor-pointer flex-wrap items-start justify-between gap-2">
              <div className="min-w-0 flex-1">
                <div className="break-words text-sm font-semibold text-slate-900 [overflow-wrap:anywhere]">{String(item.name ?? `Step ${index + 1}`)}</div>
                <div className="mt-1 break-words text-[11px] text-slate-500 [overflow-wrap:anywhere]">
                  {String(item.tool ?? "tool.unknown")}
                  {item.started_at ? ` · ${formatDateTime(String(item.started_at))}` : ""}
                </div>
              </div>
              <div className="flex shrink-0 flex-wrap items-center gap-2">
                {item.duration_ms ? (
                  <span className="rounded-full bg-slate-100 px-2 py-1 text-[11px] text-slate-600 dark:bg-secondary dark:text-muted-foreground">{String(item.duration_ms)} ms</span>
                ) : null}
                <span
                  className={cn(
                    "rounded-full px-2 py-1 text-[11px] font-semibold",
                    status === "SUCCESS"
                      ? "bg-emerald-50 text-emerald-700"
                      : status === "FAILED"
                        ? "bg-rose-50 text-rose-700"
                        : "bg-slate-100 text-slate-700 dark:bg-secondary dark:text-muted-foreground",
                  )}
                >
                  {status}
                </span>
              </div>
            </summary>
            <div className="grid min-w-0 gap-3 md:grid-cols-2">
              <PanelBlock title="Input" value={item.input ?? {}} />
              <PanelBlock title="Output" value={item.output ?? {}} />
            </div>
          </details>
        );
      })}
    </div>
  );
}

function ExecutionPanel({ execution }: { execution: Record<string, unknown> }) {
  const entries = Object.entries(execution);
  return (
    <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
      <div className="mb-3 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">Execution Summary</div>
      <div className="grid min-w-0 gap-2 md:grid-cols-2">
        {entries.map(([key, value]) => (
          <div key={key} className="min-w-0 rounded-xl border border-slate-200 bg-slate-50 px-3 py-2 dark:border-border dark:bg-secondary/60">
            <div className="break-words text-[11px] uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">{key}</div>
            <div className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-slate-900 [overflow-wrap:anywhere]">{displayValue(value)}</div>
          </div>
        ))}
      </div>
    </section>
  );
}

function ResultPanel({ result, message }: { result: unknown; message?: Props["message"] }) {
  const object = asObject(result);
  const response = callerResponseJSON(object, message);
  const answer = typeof response.answer === "string" ? response.answer : message?.answer ?? undefined;

  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
        <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">响应摘要</div>
        <div className="space-y-2">
          {message?.createdAt ? <SummaryLine label="时间" value={formatDateTime(message.createdAt)} /> : null}
          {message?.statusText ? <SummaryLine label="状态" value={message.statusText} /> : null}
          {answer ? <SummaryLine label="答案" value={answer} multiline /> : null}
        </div>
      </section>
      {answer ? (
        <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">Response JSON</div>
          <CodeBlock value={response} />
        </section>
      ) : null}
      <section className="min-w-0 rounded-2xl border border-slate-200 bg-white p-3 dark:border-border dark:bg-card">
        <div className="mb-2 break-words text-xs font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">Raw JSON</div>
        <CodeBlock value={result} />
      </section>
    </div>
  );
}

function callerResponseJSON(object: Record<string, unknown>, message?: Props["message"]) {
  const source = Object.keys(asObject(object.response)).length > 0 ? asObject(object.response) : object;
  if ("answer" in source || "received_at" in source || "answered_at" in source) {
    return {
      answer: typeof source.answer === "string" ? source.answer : message?.answer ?? "",
      received_at: typeof source.received_at === "string" ? source.received_at : null,
      answered_at: typeof source.answered_at === "string" ? source.answered_at : null,
    };
  }
  return source;
}

function SummaryLine({
  label,
  value,
  multiline,
}: {
  label: string;
  value: string;
  multiline?: boolean;
}) {
  return (
    <div className="rounded-xl border border-slate-200 bg-slate-50 px-3 py-2 dark:border-border dark:bg-secondary/60">
      <div className="text-[11px] uppercase tracking-[0.14em] text-slate-500">{label}</div>
      <div className={cn("mt-1 text-sm leading-6 text-slate-900 dark:text-foreground", multiline && "whitespace-pre-wrap break-words [overflow-wrap:anywhere]")}>
        {value}
      </div>
    </div>
  );
}

function PanelBlock({ title, value }: { title: string; value: unknown }) {
  return (
    <div className="min-w-0 rounded-xl border border-slate-200 bg-slate-50 p-3 dark:border-border dark:bg-secondary/60">
      <div className="mb-2 break-words text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500 [overflow-wrap:anywhere]">{title}</div>
      <CodeBlock value={value} />
    </div>
  );
}

function CodeBlock({ value }: { value: unknown }) {
  const formatted = typeof value === "string" ? value : formatJSON(value);
  const codeScroll = useScrollFollow<HTMLPreElement>([formatted]);

  return (
    <div className="relative min-w-0">
      <pre
        ref={codeScroll.viewportRef}
        className="detail-code-scroll max-h-[28rem] max-w-full overflow-x-hidden overflow-y-auto whitespace-pre-wrap break-words rounded-xl bg-slate-950 px-4 py-3 text-xs leading-6 text-slate-100 shadow-inner [overflow-wrap:anywhere]"
      >
        <code className="block min-w-0 max-w-full whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{formatted}</code>
      </pre>
      <ScrollJumpControls
        show={codeScroll.showControls}
        onTop={() => codeScroll.scrollToTop()}
        onBottom={() => codeScroll.scrollToBottom()}
        className="bottom-3 right-3"
      />
    </div>
  );
}

function displayValue(value: unknown) {
  if (typeof value === "string") {
    return value;
  }
  return formatJSON(value);
}

function formatDateTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  const pad = (num: number) => String(num).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
