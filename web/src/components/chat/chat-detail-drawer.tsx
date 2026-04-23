"use client";

import { useEffect } from "react";
import { GripVertical, X } from "lucide-react";

import { MessageDetails } from "@/components/chat/message-details";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type Props = {
  title: string;
  open: boolean;
  width: number;
  selected: {
    role: "user" | "assistant";
    content: string;
    details?: unknown;
  } | null;
  onClear: () => void;
  onResizeStart: () => void;
};

export function ChatDetailDrawer({ title, open, width, selected, onClear, onResizeStart }: Props) {
  useEffect(() => {
    if (!open) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClear();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [open, onClear]);

  return (
    <>
      <div
        className={cn(
          "absolute inset-0 z-20 bg-slate-950/15 transition-opacity",
          open ? "pointer-events-auto opacity-100" : "pointer-events-none opacity-0",
        )}
        onClick={onClear}
      />
      <aside
        className={cn(
          "absolute inset-y-0 right-0 z-30 flex min-h-0 max-w-[calc(100%-40px)] flex-col overflow-hidden rounded-l-3xl border-l border-slate-200 bg-white shadow-2xl transition-transform duration-200",
          open ? "translate-x-0" : "translate-x-full",
        )}
        style={{ width }}
      >
        <button
          type="button"
          aria-label="调整详情抽屉宽度"
          onMouseDown={onResizeStart}
          className="absolute left-0 top-0 flex h-full w-4 -translate-x-1/2 cursor-col-resize items-center justify-center"
        >
          <span className="flex h-16 w-3 items-center justify-center rounded-full border border-slate-200 bg-white text-slate-400 shadow-sm">
            <GripVertical className="h-4 w-4" />
          </span>
        </button>
        <header className="border-b px-5 py-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold">{title}</h2>
              <p className="mt-1 text-xs text-muted-foreground">
                详情以抽屉形式显示，不占用聊天主区宽度。
              </p>
            </div>
            <Button type="button" variant="ghost" size="sm" onClick={onClear}>
              <X className="mr-2 h-4 w-4" />
              关闭
            </Button>
          </div>
        </header>
        <div className="flex min-h-0 flex-1 flex-col">
          {selected ? (
            <>
              <div className="shrink-0 border-b bg-slate-50/65 p-5">
                <section className="rounded-2xl border border-slate-200 bg-white p-4">
                  <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500">
                    当前消息
                  </div>
                  <div className="mb-3 flex items-center gap-2 text-xs text-slate-500">
                    <span className="rounded-full bg-slate-100 px-2 py-1">
                      {selected.role === "assistant" ? "Assistant" : "User"}
                    </span>
                  </div>
                  <div className="detail-scroll max-h-44 overflow-y-auto whitespace-pre-wrap pr-2 text-sm leading-6 text-slate-900">
                    {selected.content}
                  </div>
                </section>
              </div>
              <div className="detail-scroll min-h-0 flex-1 overflow-y-auto px-5 py-5">
                <MessageDetails details={selected.details ?? {}} />
              </div>
            </>
          ) : (
            <div className="detail-scroll min-h-0 flex-1 overflow-y-auto p-5">
              <div className="rounded-2xl border border-dashed border-slate-300 bg-white/70 p-6 text-sm leading-6 text-slate-500">
                选择一条带详情的消息后，这里会显示结构化结果、执行步骤、Prompt 和 JSON。
              </div>
            </div>
          )}
        </div>
      </aside>
    </>
  );
}
