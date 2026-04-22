import type { ComponentType } from "react";
import { MessageSquare, ShieldCheck, Sparkles, Wrench, Activity, Upload, RefreshCcw, ListTodo } from "lucide-react";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";

export type ViewKey =
  | "chat"
  | "query"
  | "ingest"
  | "lint"
  | "reflect"
  | "repair"
  | "sync"
  | "tasks";

const navItems: Array<{ key: ViewKey; label: string; admin?: boolean; icon: ComponentType<{ className?: string }> }> = [
  { key: "chat", label: "用户对话", icon: MessageSquare },
  { key: "query", label: "管理员查询", admin: true, icon: ShieldCheck },
  { key: "ingest", label: "摄入", admin: true, icon: Upload },
  { key: "lint", label: "健康检查", admin: true, icon: Activity },
  { key: "reflect", label: "反思分析", admin: true, icon: Sparkles },
  { key: "repair", label: "修复", admin: true, icon: Wrench },
  { key: "sync", label: "同步", admin: true, icon: RefreshCcw },
  { key: "tasks", label: "任务中心", admin: true, icon: ListTodo },
];

type RecentTask = {
  id: string;
  type: string;
  status: string;
};

type Props = {
  currentView: ViewKey;
  onSelect: (view: ViewKey, admin?: boolean) => void;
  recentTasks: RecentTask[];
  chatCount: number;
  hasAdmin: boolean;
};

export function WorkspaceSidebar({ currentView, onSelect, recentTasks, chatCount, hasAdmin }: Props) {
  return (
    <aside className="panel-glass flex h-full flex-col gap-5 p-4">
      <div>
        <p className="text-xs uppercase tracking-[0.24em] text-muted-foreground">WikiOS</p>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight">Workbench</h1>
        <p className="mt-2 text-sm text-muted-foreground">NextChat 风格的同域测试工作台。</p>
      </div>

      <div className="space-y-2">
        {navItems.map((item) => {
          const Icon = item.icon;
          const active = currentView === item.key;
          return (
            <button
              key={item.key}
              className={cn("sidebar-pill flex items-center gap-3", active && "sidebar-pill-active")}
              onClick={() => onSelect(item.key, item.admin)}
              type="button"
            >
              <Icon className="h-4 w-4" />
              <span className="flex-1">{item.label}</span>
              {item.key === "chat" && <Badge>{chatCount}</Badge>}
              {item.admin && !hasAdmin && <Badge variant="warning">锁定</Badge>}
            </button>
          );
        })}
      </div>

      <div className="rounded-3xl bg-slate-900 p-4 text-slate-100">
        <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Recent Tasks</p>
        <div className="mt-3 space-y-2">
          {recentTasks.length === 0 ? (
            <p className="text-sm text-slate-400">暂无任务历史</p>
          ) : (
            recentTasks.slice(0, 5).map((task) => (
              <div key={task.id} className="rounded-2xl border border-slate-700 px-3 py-2">
                <p className="text-xs font-medium uppercase tracking-wide text-slate-400">{task.type}</p>
                <p className="mt-1 truncate text-sm">{task.id}</p>
                <p className="mt-1 text-xs text-slate-400">{task.status}</p>
              </div>
            ))
          )}
        </div>
      </div>
    </aside>
  );
}
