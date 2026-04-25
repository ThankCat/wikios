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
    <div className={cn("pointer-events-none absolute z-20 flex flex-col gap-2", className)}>
      <button
        type="button"
        title="回到顶部"
        aria-label="回到顶部"
        onClick={onTop}
        className="pointer-events-auto inline-flex h-9 w-9 items-center justify-center rounded-full border border-slate-200 bg-white/95 text-slate-700 shadow-lg transition hover:bg-slate-50"
      >
        <ArrowUp className="h-4 w-4" />
      </button>
      <button
        type="button"
        title="跳到最新"
        aria-label="跳到最新"
        onClick={onBottom}
        className="pointer-events-auto inline-flex h-9 w-9 items-center justify-center rounded-full border border-slate-900 bg-slate-900 text-white shadow-lg transition hover:bg-slate-800"
      >
        <ArrowDown className="h-4 w-4" />
      </button>
    </div>
  );
}
