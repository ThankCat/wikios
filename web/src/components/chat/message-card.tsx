"use client";

import { MarkdownContent } from "@/components/chat/markdown-content";
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
  return (
    <div className={cn("flex w-full", role === "user" ? "justify-end" : "justify-start")}>
      <div className={role === "user" ? "chat-bubble-user" : "chat-bubble-assistant"}>
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
