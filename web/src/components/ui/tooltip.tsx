"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

type TooltipContextValue = {
  open: boolean;
  setOpen: (open: boolean) => void;
};

const TooltipContext = React.createContext<TooltipContextValue | null>(null);

export function TooltipProvider({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}

export function Tooltip({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = React.useState(false);
  return <TooltipContext.Provider value={{ open, setOpen }}>{children}</TooltipContext.Provider>;
}

export function TooltipTrigger({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) {
  const context = React.useContext(TooltipContext);
  if (!context) {
    return null;
  }
  const triggerProps = {
    onMouseEnter: () => context.setOpen(true),
    onMouseLeave: () => context.setOpen(false),
    onFocus: () => context.setOpen(true),
    onBlur: () => context.setOpen(false),
  };
  if (asChild && React.isValidElement(children)) {
    return React.cloneElement(children as React.ReactElement, triggerProps);
  }
  return <span {...triggerProps}>{children}</span>;
}

export function TooltipContent({ className, children }: React.HTMLAttributes<HTMLDivElement>) {
  const context = React.useContext(TooltipContext);
  if (!context?.open) {
    return null;
  }
  return (
    <div
      className={cn(
        "absolute z-50 mt-2 max-w-64 rounded-md border border-border bg-foreground px-3 py-1.5 text-xs text-background shadow-lg",
        className,
      )}
    >
      {children}
    </div>
  );
}

