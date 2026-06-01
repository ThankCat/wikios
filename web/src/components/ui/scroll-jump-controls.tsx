"use client";

import { ArrowDown, ArrowUp } from "lucide-react";

import { cn } from "@/lib/utils";

type Props = {
  show: boolean;
  onTop: () => void;
  onBottom: () => void;
  className?: string;
};

export function ScrollJumpControls({ show, onTop, onBottom, className }: Props) {
  if (!show) {
    return null;
  }
  return (
    <div
      className={cn(
        "pointer-events-none absolute z-20 flex flex-col overflow-hidden rounded-full border bg-background/85 p-0.5 text-muted-foreground shadow-sm backdrop-blur dark:bg-card/85",
        className,
      )}
    >
      <button
        type="button"
        title="回到顶部"
        aria-label="回到顶部"
        onClick={onTop}
        className="pointer-events-auto inline-flex size-6 items-center justify-center rounded-full transition hover:bg-muted hover:text-foreground"
      >
        <ArrowUp className="size-3.5" />
      </button>
      <div className="mx-auto my-0.5 h-px w-3 bg-border" />
      <button
        type="button"
        title="跳到最新"
        aria-label="跳到最新"
        onClick={onBottom}
        className="pointer-events-auto inline-flex size-6 items-center justify-center rounded-full transition hover:bg-muted hover:text-foreground"
      >
        <ArrowDown className="size-3.5" />
      </button>
    </div>
  );
}
