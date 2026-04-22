"use client";

import { Plus, Shield, Trash2, UserRound } from "lucide-react";

import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";

export type ConversationItem = {
  id: string;
  title: string;
};

type Props = {
  title: string;
  subtitle: string;
  variant: "user" | "admin";
  items: ConversationItem[];
  activeId: string;
  onSelect: (id: string) => void;
  onCreate: () => void;
  onDelete: (id: string) => void;
};

export function ConversationSidebar({ title, subtitle, variant, items, activeId, onSelect, onCreate, onDelete }: Props) {
  return (
    <aside className="panel-glass flex h-full min-h-0 flex-col overflow-hidden p-4">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-semibold">
            {variant === "admin" ? <Shield className="h-4 w-4" /> : <UserRound className="h-4 w-4" />}
            {title}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">{subtitle}</p>
        </div>
        <Button variant="outline" size="sm" onClick={onCreate}>
          <Plus className="h-4 w-4" />
        </Button>
      </div>
      <ScrollArea className="min-h-0 flex-1 pr-1">
        <div className="space-y-2">
          {items.map((item) => (
            <div
              key={item.id}
              className={cn(
                "flex items-center gap-2 rounded-2xl px-3 py-3 text-left text-sm transition",
                item.id === activeId ? "bg-foreground text-white" : "bg-white/60 text-foreground hover:bg-secondary",
              )}
            >
              <button type="button" onClick={() => onSelect(item.id)} className="min-w-0 flex-1 text-left">
                <div className="truncate font-medium">{item.title}</div>
              </button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className={cn("h-8 w-8 shrink-0 px-0", item.id === activeId ? "hover:bg-white/10" : "hover:bg-white")}
                onClick={() => onDelete(item.id)}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
        </div>
      </ScrollArea>
    </aside>
  );
}
