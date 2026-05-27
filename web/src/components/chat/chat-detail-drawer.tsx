"use client";

import { useEffect } from "react";
import { X } from "lucide-react";

import { MessageDetails } from "@/components/chat/message-details";
import { Button } from "@/components/ui/button";
import { ScrollJumpControls } from "@/components/ui/scroll-jump-controls";
import { useScrollFollow } from "@/lib/use-scroll-follow";

type Props = {
  title: string;
  open: boolean;
  selected: {
    role: "user" | "assistant";
    content: string;
    createdAt?: string;
    details?: unknown;
    statusText?: string;
  } | null;
  onClear: () => void;
};

export function ChatDetailDrawer({ title, open, selected, onClear }: Props) {
  const messageScroll = useScrollFollow<HTMLDivElement>([open, selected?.content]);
  const detailScroll = useScrollFollow<HTMLDivElement>([open, selected?.details]);

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

  if (!open || !selected) {
    return null;
  }

  return (
    <>
      <div className="absolute inset-0 z-20 bg-slate-950/15 dark:bg-black/45" onClick={onClear} />
      <aside
        className="absolute inset-y-0 right-0 z-30 flex min-h-0 w-[min(980px,92vw)] max-w-[calc(100%-40px)] translate-x-0 flex-col overflow-visible break-words rounded-l-3xl border-l border-slate-200 bg-white shadow-2xl transition-transform duration-200 [overflow-wrap:anywhere] dark:border-border dark:bg-card dark:shadow-none"
      >
        <header className="border-b px-5 py-4">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <h2 className="break-words text-sm font-semibold [overflow-wrap:anywhere]">{title}</h2>
            </div>
            <Button type="button" variant="ghost" size="sm" onClick={onClear}>
              <X className="mr-2 h-4 w-4" />
              关闭
            </Button>
          </div>
        </header>
        <div className="flex min-h-0 flex-1 flex-col">
          {selected ? (
            <div className="relative min-h-0 flex-1">
              <div ref={detailScroll.viewportRef} className="detail-scroll h-full overflow-y-auto px-5 pb-5">
                <MessageDetails
                  details={selected.details ?? {}}
                  message={{
                    role: selected.role,
                    content: selected.content,
                    createdAt: selected.createdAt,
                    statusText: selected.statusText,
                    answer: selected.role === "assistant" ? selected.content : undefined,
                  }}
                  leadingContent={
                    <section className="min-w-0 rounded-xl bg-slate-50 px-3 py-2 dark:bg-secondary/50">
                      <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-slate-500">
                        <span className="break-words rounded-full bg-slate-100 px-2 py-1 [overflow-wrap:anywhere] dark:bg-secondary">
                          {selected.role === "assistant" ? "Assistant" : "User"}
                        </span>
                      </div>
                      <div className="relative">
                        <div
                          ref={messageScroll.viewportRef}
                          className="detail-scroll max-h-28 overflow-x-hidden overflow-y-auto whitespace-pre-wrap break-words pr-2 text-xs leading-5 text-slate-600 [overflow-wrap:anywhere] dark:text-muted-foreground"
                        >
                          {selected.content}
                        </div>
                        <ScrollJumpControls
                          show={messageScroll.showControls}
                          onTop={() => messageScroll.scrollToTop()}
                          onBottom={() => messageScroll.scrollToBottom()}
                          className="bottom-2 right-2"
                        />
                      </div>
                    </section>
                  }
                />
              </div>
              <ScrollJumpControls
                show={detailScroll.showControls}
                onTop={() => detailScroll.scrollToTop()}
                onBottom={() => detailScroll.scrollToBottom()}
                className="bottom-4 right-5"
              />
            </div>
          ) : (
            <div className="detail-scroll min-h-0 flex-1 overflow-y-auto p-5">
              <div className="break-words rounded-2xl border border-dashed border-slate-300 bg-white/70 p-6 text-sm leading-6 text-slate-500 [overflow-wrap:anywhere]">
                选择一条带详情的消息后，这里会显示结构化结果、执行步骤、Prompt 和 JSON。
              </div>
            </div>
          )}
        </div>
      </aside>
    </>
  );
}
