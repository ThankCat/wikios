"use client";

import { useMemo, useState } from "react";
import { ArrowDown, BrainCircuit, ChevronDown, TerminalSquare } from "lucide-react";

import { MarkdownContent } from "@/components/chat/markdown-content";
import { useScrollFollow } from "@/lib/use-scroll-follow";
import { cn } from "@/lib/utils";

type Props = {
  id: string;
  role: "user" | "assistant";
  content: string;
  details?: unknown;
  pending?: boolean;
  statusText?: string;
  selected?: boolean;
  onInspect?: (payload: { id: string; role: "user" | "assistant"; content: string; details?: unknown }) => void;
};

export function MessageCard({ id, role, content, details, pending, statusText, selected, onInspect }: Props) {
  const displayContent = role === "assistant" && content.trim() === "" && pending ? "正在处理..." : content;
  const detailObject = useMemo(() => asObject(details), [details]);
  const reasoning = typeof detailObject.reasoning === "string" ? detailObject.reasoning.trim() : "";
  const executionObject = asObject(detailObject.execution);
  const stepItems: unknown[] = Array.isArray(detailObject.steps)
    ? detailObject.steps
    : Array.isArray(executionObject.steps)
      ? executionObject.steps
      : [];
  return (
    <div className={cn("flex w-full", role === "user" ? "justify-end" : "justify-start")}>
      <div className={role === "user" ? "chat-bubble-user" : "chat-bubble-assistant"}>
        {role === "assistant" && (reasoning || stepItems.length > 0) ? (
          <div className="mb-3 space-y-2">
            {reasoning ? <InlineTracePanel title="思考过程" icon="reasoning" content={reasoning} pending={pending} /> : null}
            {stepItems.length > 0 ? <InlineTracePanel title="执行过程" icon="tools" steps={stepItems} pending={pending} /> : null}
          </div>
        ) : null}
        {role === "assistant" ? (
          <MarkdownContent className="prose prose-slate prose-sm max-w-none prose-table:my-0 prose-th:p-0 prose-td:p-0">
            {displayContent}
          </MarkdownContent>
        ) : (
          <div className="whitespace-pre-wrap">{displayContent}</div>
        )}
        {statusText ? (
          <div className={cn("mt-2 text-xs", role === "user" ? "text-white/70" : "text-slate-500")}>{statusText}</div>
        ) : null}
        {details ? (
          <div className="mt-3 flex justify-end">
            <button
              type="button"
              onClick={() => onInspect?.({ id, role, content, details })}
              className={cn(
                "rounded-full border px-3 py-1.5 text-[11px] font-medium transition",
                role === "user"
                  ? selected
                    ? "border-white/40 bg-white/15 text-white"
                    : "border-white/20 bg-white/5 text-white/85 hover:bg-white/10"
                  : selected
                    ? "border-slate-900 bg-slate-900 text-white"
                    : "border-slate-300 bg-slate-50 text-slate-600 hover:bg-slate-100",
              )}
            >
              {selected ? "正在查看详情" : "查看详情"}
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function InlineTracePanel({
  title,
  icon,
  content,
  steps,
  pending,
}: {
  title: string;
  icon: "reasoning" | "tools";
  content?: string;
  steps?: unknown[];
  pending?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const Icon = icon === "reasoning" ? BrainCircuit : TerminalSquare;
  const count = steps?.length ?? 0;
  const traceScroll = useScrollFollow<HTMLPreElement>([content, open]);
  return (
    <section className="rounded-2xl border border-slate-200 bg-white/80 text-left shadow-sm">
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        className="flex w-full items-center justify-between gap-3 px-3 py-2 text-xs font-medium text-slate-700"
        title={open ? `收起${title}` : `展开${title}`}
      >
        <span className="flex min-w-0 items-center gap-2">
          <Icon className={cn("h-3.5 w-3.5", pending ? "animate-pulse text-slate-500" : "text-slate-500")} />
          <span>{title}</span>
          {pending ? <span className="text-slate-400">生成中</span> : null}
          {count > 0 ? <span className="text-slate-400">{count} 步</span> : null}
        </span>
        <ChevronDown className={cn("h-4 w-4 shrink-0 text-slate-400 transition", open ? "rotate-180" : "")} />
      </button>
      {open ? (
        <div className="relative border-t border-slate-200 px-3 py-2">
          {content ? (
            <pre
              ref={traceScroll.viewportRef}
              className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-xl bg-slate-50 p-3 text-xs leading-5 text-slate-600"
            >
              {content}
            </pre>
          ) : null}
          {steps?.length ? (
            <div className="space-y-2">
              {steps.slice(-12).map((step, index) => {
                const item = asObject(step);
                return (
                  <div key={index} className="rounded-xl border border-slate-200 bg-slate-50 px-3 py-2">
                    <div className="truncate text-xs font-semibold text-slate-800">{String(item.name ?? `Step ${index + 1}`)}</div>
                    <div className="mt-1 flex flex-wrap gap-2 text-[11px] text-slate-500">
                      <span>{String(item.tool ?? "tool")}</span>
                      <span>{String(item.status ?? "RUNNING")}</span>
                      {item.duration_ms ? <span>{String(item.duration_ms)} ms</span> : null}
                    </div>
                  </div>
                );
              })}
            </div>
          ) : null}
          {content && traceScroll.showControls ? (
            <button
              type="button"
              onClick={() => traceScroll.scrollToBottom()}
              className="absolute bottom-4 right-5 inline-flex h-8 w-8 items-center justify-center rounded-full border border-slate-200 bg-white text-slate-600 shadow-soft transition hover:bg-slate-50 hover:text-slate-950"
              title={`跳到${title}最新位置`}
            >
              <ArrowDown className="h-4 w-4" />
              <span className="sr-only">跳到最新</span>
            </button>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function asObject(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}
