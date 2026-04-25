"use client";

import { ReactNode, useMemo, useState } from "react";
import { Braces, BrainCircuit, Cog, FileJson2, MessageSquareQuote } from "lucide-react";

import { ScrollJumpControls } from "@/components/ui/scroll-jump-controls";
import { useScrollFollow } from "@/lib/use-scroll-follow";
import { cn, formatJSON } from "@/lib/utils";

type Props = {
  details: unknown;
  leadingContent?: ReactNode;
};

type DetailTab = "reasoning" | "prompt" | "tools" | "execution" | "result";

type TabConfig = {
  id: DetailTab;
  label: string;
  icon: typeof MessageSquareQuote;
};

const tabs: TabConfig[] = [
  { id: "reasoning", label: "Reasoning", icon: BrainCircuit },
  { id: "prompt", label: "Prompt", icon: MessageSquareQuote },
  { id: "tools", label: "Tools", icon: Cog },
  { id: "execution", label: "Execution", icon: Braces },
  { id: "result", label: "Result", icon: FileJson2 },
];

function asObject(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

export function MessageDetails({ details, leadingContent }: Props) {
  const object = asObject(details);
  const prompts = Array.isArray(object.prompts) ? object.prompts : [];
  const execution = asObject(object.execution);
  const executionSteps = Array.isArray(execution.steps) ? execution.steps : [];
  const steps = Array.isArray(object.steps) ? object.steps : executionSteps;
  const result = object.result ?? object;
  const reasoning = typeof object.reasoning === "string" ? object.reasoning : "";

  const availableTabs = useMemo(
    () =>
      tabs.filter((tab) => {
        switch (tab.id) {
          case "reasoning":
            return reasoning.trim() !== "";
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
    [execution, prompts.length, reasoning, steps.length],
  );

  const [activeTab, setActiveTab] = useState<DetailTab>(availableTabs[0]?.id ?? "result");
  const resolvedTab = availableTabs.some((tab) => tab.id === activeTab) ? activeTab : (availableTabs[0]?.id ?? "result");

  return (
    <div className="space-y-3 text-left">
      <div className="sticky top-0 z-10 flex flex-wrap gap-2 border-b border-slate-200 bg-white/95 py-3 backdrop-blur">
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
                  ? "border-slate-900 bg-slate-900 text-white"
                  : "border-slate-300 bg-white text-slate-600 hover:bg-slate-50",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {leadingContent}

      {resolvedTab === "reasoning" ? <ReasoningPanel reasoning={reasoning} /> : null}
      {resolvedTab === "prompt" ? <PromptPanel prompts={prompts} /> : null}
      {resolvedTab === "tools" ? <ToolsPanel steps={steps} /> : null}
      {resolvedTab === "execution" ? <ExecutionPanel execution={execution} /> : null}
      {resolvedTab === "result" ? <ResultPanel result={result} /> : null}
    </div>
  );
}

function ReasoningPanel({ reasoning }: { reasoning: string }) {
  return (
    <section className="rounded-2xl border border-slate-200 bg-white p-3">
      <div className="mb-2 text-xs font-semibold uppercase tracking-[0.14em] text-slate-500">Reasoning Stream</div>
      <CodeBlock value={reasoning} />
    </section>
  );
}

function PromptPanel({ prompts }: { prompts: unknown[] }) {
  return (
    <div className="space-y-3">
      {prompts.map((prompt, index) => {
        const data = asObject(prompt);
        const messages = Array.isArray(data.messages) ? data.messages : [];
        return (
          <details key={index} className="rounded-2xl border border-slate-200 bg-white p-3">
            <summary className="mb-3 flex cursor-pointer items-center justify-between gap-3">
              <div>
                <div className="text-xs font-semibold text-slate-900">{String(data.name ?? `Prompt ${index + 1}`)}</div>
                <div className="mt-1 text-[11px] text-slate-500">
                  model: {String(data.model ?? "unknown")}
                  {data.created_at ? ` · ${formatDateTime(String(data.created_at))}` : ""}
                </div>
              </div>
              <div className="rounded-full bg-slate-100 px-2 py-1 text-[11px] text-slate-600">{messages.length} messages</div>
            </summary>
            <div className="space-y-2">
              {messages.map((message, messageIndex) => {
                const item = asObject(message);
                return (
                  <div key={messageIndex} className="rounded-xl border border-slate-200 bg-slate-50 p-3">
                    <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500">
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
    <div className="space-y-3">
      {steps.map((step, index) => {
        const item = asObject(step);
        const status = String(item.status ?? "UNKNOWN");
        return (
          <details key={index} className="rounded-2xl border border-slate-200 bg-white p-3">
            <summary className="mb-3 flex cursor-pointer flex-wrap items-center justify-between gap-2">
              <div className="min-w-0">
                <div className="truncate text-sm font-semibold text-slate-900">{String(item.name ?? `Step ${index + 1}`)}</div>
                <div className="mt-1 text-[11px] text-slate-500">
                  {String(item.tool ?? "tool.unknown")}
                  {item.started_at ? ` · ${formatDateTime(String(item.started_at))}` : ""}
                </div>
              </div>
              <div className="flex items-center gap-2">
                {item.duration_ms ? (
                  <span className="rounded-full bg-slate-100 px-2 py-1 text-[11px] text-slate-600">{String(item.duration_ms)} ms</span>
                ) : null}
                <span
                  className={cn(
                    "rounded-full px-2 py-1 text-[11px] font-semibold",
                    status === "SUCCESS"
                      ? "bg-emerald-50 text-emerald-700"
                      : status === "FAILED"
                        ? "bg-rose-50 text-rose-700"
                        : "bg-slate-100 text-slate-700",
                  )}
                >
                  {status}
                </span>
              </div>
            </summary>
            <div className="grid gap-3 md:grid-cols-2">
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
    <section className="rounded-2xl border border-slate-200 bg-white p-3">
      <div className="mb-3 text-xs font-semibold uppercase tracking-[0.14em] text-slate-500">Execution Summary</div>
      <div className="grid gap-2 md:grid-cols-2">
        {entries.map(([key, value]) => (
          <div key={key} className="rounded-xl border border-slate-200 bg-slate-50 px-3 py-2">
            <div className="text-[11px] uppercase tracking-[0.14em] text-slate-500">{key}</div>
            <div className="mt-1 break-all font-mono text-xs text-slate-900">{displayValue(value)}</div>
          </div>
        ))}
      </div>
    </section>
  );
}

function ResultPanel({ result }: { result: unknown }) {
  const object = asObject(result);
  const answer = typeof object.answer === "string" ? object.answer : undefined;

  return (
    <div className="space-y-3">
      {answer ? (
        <section className="rounded-2xl border border-slate-200 bg-white p-3">
          <div className="mb-2 text-xs font-semibold uppercase tracking-[0.14em] text-slate-500">Rendered Answer</div>
          <div className="rounded-xl border border-slate-200 bg-slate-50 px-3 py-2 text-sm leading-6 text-slate-900 whitespace-pre-wrap">
            {answer}
          </div>
        </section>
      ) : null}
      <section className="rounded-2xl border border-slate-200 bg-white p-3">
        <div className="mb-2 text-xs font-semibold uppercase tracking-[0.14em] text-slate-500">Raw JSON</div>
        <CodeBlock value={result} />
      </section>
    </div>
  );
}

function PanelBlock({ title, value }: { title: string; value: unknown }) {
  return (
    <div className="rounded-xl border border-slate-200 bg-slate-50 p-3">
      <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500">{title}</div>
      <CodeBlock value={value} />
    </div>
  );
}

function CodeBlock({ value }: { value: unknown }) {
  const formatted = formatJSON(value);
  const codeScroll = useScrollFollow<HTMLPreElement>([formatted]);

  return (
    <div className="relative">
      <pre
        ref={codeScroll.viewportRef}
        className="detail-code-scroll max-h-[28rem] overflow-auto rounded-xl bg-slate-950 px-4 py-3 text-xs leading-6 text-slate-100 shadow-inner"
      >
        <code>{formatted}</code>
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
