"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

type ToastItem = {
  id: string;
  title: string;
  description?: string;
  tone?: "default" | "success" | "error";
};

const listeners = new Set<(item: ToastItem) => void>();

export const toast = {
  message(title: string, description?: string) {
    emitToast({ title, description });
  },
  success(title: string, description?: string) {
    emitToast({ title, description, tone: "success" });
  },
  error(title: string, description?: string) {
    emitToast({ title, description, tone: "error" });
  },
};

export function Toaster() {
  const [items, setItems] = React.useState<ToastItem[]>([]);

  React.useEffect(() => {
    function listener(item: ToastItem) {
      setItems((current) => [...current.slice(-3), item]);
      window.setTimeout(() => {
        setItems((current) => current.filter((candidate) => candidate.id !== item.id));
      }, 3000);
    }
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }, []);

  if (!items.length) {
    return null;
  }
  return (
    <div className="fixed right-4 top-4 z-[80] flex w-[min(360px,calc(100vw-2rem))] flex-col gap-2">
      {items.map((item) => (
        <div
          key={item.id}
	          className={cn(
	            "rounded-lg border bg-white p-3 text-sm shadow-lg dark:bg-card dark:text-card-foreground dark:shadow-none",
	            item.tone === "success" && "border-emerald-200 bg-emerald-50 text-emerald-900 dark:border-emerald-700/60 dark:bg-emerald-950/40 dark:text-emerald-200",
	            item.tone === "error" && "border-red-200 bg-red-50 text-red-900 dark:border-red-700/60 dark:bg-red-950/40 dark:text-red-200",
	          )}
        >
          <div className="font-medium">{item.title}</div>
          {item.description ? <div className="mt-1 text-xs opacity-80">{item.description}</div> : null}
        </div>
      ))}
    </div>
  );
}

function emitToast(item: Omit<ToastItem, "id">) {
  const payload = { ...item, id: `${Date.now()}-${Math.random().toString(16).slice(2)}` };
  for (const listener of listeners) {
    listener(payload);
  }
}
