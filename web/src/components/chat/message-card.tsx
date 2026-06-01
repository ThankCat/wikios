"use client";

import { useMemo, useState } from "react";
import { ArrowDown, BrainCircuit, ChevronDown } from "lucide-react";

import { MarkdownContent } from "@/components/chat/markdown-content";
import { useScrollFollow } from "@/lib/use-scroll-follow";
import { cn } from "@/lib/utils";

type Props = {
  id: string;
  role: "user" | "assistant";
  content: string;
  createdAt?: string;
  details?: unknown;
  detailMode?: "inline" | "after";
  detailInitiallyOpen?: boolean;
  pending?: boolean;
  statusText?: string;
  selected?: boolean;
  onInspect?: (payload: { id: string; role: "user" | "assistant"; content: string; details?: unknown }) => void;
};

export function MessageCard({
  id,
  role,
  content,
  createdAt,
  details,
  detailMode = "inline",
  detailInitiallyOpen,
  pending,
  statusText,
  selected,
  onInspect,
}: Props) {
  const showTypingIndicator = role === "assistant" && content.trim() === "" && pending;
  const displayContent = showTypingIndicator ? "" : content;
  const detailObject = useMemo(() => asObject(details), [details]);
  const modelReasoning = typeof detailObject.reasoning === "string" ? detailObject.reasoning.trim() : "";
  const hasReasoningDetails = role === "assistant" && modelReasoning;
  const durationText = role === "assistant" ? responseDurationText(detailObject) : "";
  return (
    <div className={cn("flex w-full", role === "user" ? "justify-end" : "justify-start")}>
      <div className={cn("flex w-full min-w-0 flex-col", role === "user" ? "items-end" : "items-start")}>
        {createdAt ? (
          <div className="mb-1 px-1 text-[10px] leading-4 text-muted-foreground">
            {formatMessageTime(createdAt)}
            {durationText ? <span> · 耗时 {durationText}</span> : null}
          </div>
        ) : null}
        <div className={role === "user" ? "chat-bubble-user" : "chat-bubble-assistant"}>
          {hasReasoningDetails && detailMode === "inline" ? (
            <div className="mb-3">
              <InlineTracePanel title="模型思考" content={modelReasoning} pending={pending} initiallyOpen={false} />
            </div>
          ) : null}
          {role === "assistant" ? (
            showTypingIndicator ? (
              <TypingDots />
            ) : (
              <MarkdownContent className="prose prose-slate prose-sm max-w-none dark:prose-invert prose-table:my-0 prose-th:p-0 prose-td:p-0">
                {displayContent}
              </MarkdownContent>
            )
          ) : (
            <div className="whitespace-pre-wrap">{displayContent}</div>
          )}
          {statusText ? (
            <div className={cn("mt-2 text-xs", role === "user" ? "text-primary-foreground/70" : "text-muted-foreground")}>{statusText}</div>
          ) : null}
          {details && onInspect ? (
            <div className="mt-3 flex justify-end">
              <button
                type="button"
                onClick={() => onInspect?.({ id, role, content, details })}
                className={cn(
                  "inline-flex h-8 items-center justify-center rounded-md border px-2.5 text-[11px] font-medium transition",
                  role === "user"
                    ? selected
                      ? "border-primary-foreground/40 bg-primary-foreground/15 text-primary-foreground"
                      : "border-primary-foreground/20 bg-primary-foreground/10 text-primary-foreground/85 hover:bg-primary-foreground/10"
                    : selected
                      ? "border-primary bg-primary text-primary-foreground dark:border-border dark:bg-card dark:text-foreground"
                      : "border-border bg-muted/40 text-muted-foreground hover:bg-muted dark:border-border dark:bg-secondary dark:text-muted-foreground dark:hover:bg-secondary/80 dark:hover:text-foreground",
                )}
              >
                {selected ? "正在查看详情" : "查看详情"}
              </button>
            </div>
          ) : null}
        </div>
        {hasReasoningDetails && detailMode === "after" ? (
          <div className="mt-2 w-full">
            <InlineTracePanel title="模型思考" content={modelReasoning} pending={pending} initiallyOpen={detailInitiallyOpen} />
          </div>
        ) : null}
      </div>
    </div>
  );
}

function TypingDots() {
  return (
    <div className="flex h-6 items-center gap-1.5 px-1" aria-label="正在生成回答">
      <span className="h-2 w-2 animate-bounce rounded-full bg-muted-foreground/60 [animation-delay:-0.24s]" />
      <span className="h-2 w-2 animate-bounce rounded-full bg-muted-foreground/60 [animation-delay:-0.12s]" />
      <span className="h-2 w-2 animate-bounce rounded-full bg-muted-foreground/60" />
    </div>
  );
}

function formatMessageTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  const pad = (item: number) => String(item).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function responseDurationText(details: Record<string, unknown>) {
  const source = asObject(details.response);
  const receivedAt = typeof source.received_at === "string" ? source.received_at : typeof details.received_at === "string" ? details.received_at : "";
  const answeredAt = typeof source.answered_at === "string" ? source.answered_at : typeof details.answered_at === "string" ? details.answered_at : "";
  return formatDurationBetween(receivedAt, answeredAt);
}

function formatDurationBetween(startValue: string, endValue: string) {
  if (!startValue || !endValue) {
    return "";
  }
  const start = new Date(startValue).getTime();
  const end = new Date(endValue).getTime();
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
    return "";
  }
  const totalSeconds = Math.round((end - start) / 1000);
  if (totalSeconds <= 0) {
    return "";
  }
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

function InlineTracePanel({
  title,
  content,
  pending,
  initiallyOpen,
}: {
  title: string;
  content?: string;
  pending?: boolean;
  initiallyOpen?: boolean;
}) {
  const [open, setOpen] = useState(() => (initiallyOpen === undefined ? Boolean(pending || content) : initiallyOpen));
  const traceScroll = useScrollFollow<HTMLPreElement>([content, open]);
  return (
    <section className="rounded-lg border border-border bg-card/80 text-left shadow-sm dark:border-border dark:bg-card/80 dark:shadow-none">
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        className="flex w-full items-center justify-between gap-3 px-3 py-2 text-xs font-medium text-foreground dark:text-foreground"
        title={open ? `收起${title}` : `展开${title}`}
      >
        <span className="flex min-w-0 items-center gap-2">
          <BrainCircuit className={cn("h-3.5 w-3.5", pending ? "animate-pulse text-muted-foreground" : "text-muted-foreground")} />
          <span>{title}</span>
          {pending ? <span className="text-muted-foreground">生成中</span> : null}
        </span>
        <ChevronDown className={cn("h-4 w-4 shrink-0 text-muted-foreground transition", open ? "rotate-180" : "")} />
      </button>
      {open ? (
        <div className="relative border-t border-border px-3 py-2 dark:border-border">
          {content ? (
            <pre
              ref={traceScroll.viewportRef}
              className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-3 text-xs leading-5 text-muted-foreground dark:bg-secondary/60 dark:text-foreground/85"
            >
              {content}
            </pre>
          ) : null}
          {content && traceScroll.showControls ? (
            <button
              type="button"
              onClick={() => traceScroll.scrollToBottom()}
              className="absolute bottom-4 right-5 inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-card text-muted-foreground shadow-sm transition hover:bg-muted/40 hover:text-foreground dark:border-border dark:bg-card dark:text-muted-foreground dark:shadow-none dark:hover:bg-secondary dark:hover:text-foreground"
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
