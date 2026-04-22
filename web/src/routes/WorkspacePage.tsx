import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { LogOut, LockKeyhole } from "lucide-react";
import type { PublicAnswerResponse, TaskRecord } from "@/types/api";
import { clearAdminToken, loadAdminToken } from "@/lib/storage";
import { usePersistentState } from "@/hooks/use-persistent-state";
import { WorkspaceSidebar, type ViewKey } from "@/components/layout/WorkspaceSidebar";
import { DetailPanel } from "@/components/layout/DetailPanel";
import { ChatPanel, type ChatEntry } from "@/features/chat/ChatPanel";
import { AdminPanels } from "@/features/admin/AdminPanels";
import { TaskCenter } from "@/features/tasks/TaskCenter";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

type Props = {
  mountedWikiName: string;
};

type WorkbenchState = {
  currentView: ViewKey;
  chats: ChatEntry[];
  tasks: TaskRecord[];
};

const fallbackState: WorkbenchState = {
  currentView: "chat",
  chats: [],
  tasks: [],
};

export function WorkspacePage({ mountedWikiName }: Props) {
  const [adminToken, setAdminToken] = useState(loadAdminToken());
  const [selectedChat, setSelectedChat] = useState<PublicAnswerResponse | null>(null);
  const [selectedTask, setSelectedTask] = useState<TaskRecord | null>(null);
  const [state, setState] = usePersistentState<WorkbenchState>("wikios.workbench", fallbackState);
  const navigate = useNavigate();

  function updateTasks(task: TaskRecord) {
    setState((current) => {
      const existing = current.tasks.filter((item) => item.id !== task.id);
      return {
        ...current,
        tasks: [task, ...existing].slice(0, 30),
      };
    });
  }

  const hasAdmin = adminToken.trim().length > 0;
  const title = useMemo(() => {
    switch (state.currentView) {
      case "chat":
        return "用户对话端";
      case "query":
        return "管理员查询";
      case "ingest":
        return "摄入";
      case "lint":
        return "健康检查";
      case "reflect":
        return "反思分析";
      case "repair":
        return "修复";
      case "sync":
        return "同步";
      case "tasks":
        return "任务中心";
    }
  }, [state.currentView]);

  return (
    <div className="min-h-screen p-4 lg:p-6">
      <div className="mx-auto grid max-w-[1800px] gap-4 lg:grid-cols-[280px_minmax(0,1fr)_360px]">
        <div className="h-[calc(100vh-2rem)] lg:h-[calc(100vh-3rem)]">
          <WorkspaceSidebar
            chatCount={state.chats.length}
            currentView={state.currentView}
            hasAdmin={hasAdmin}
            onSelect={(view, admin) => {
              if (admin && !hasAdmin) {
                navigate("/login");
                return;
              }
              setState((current) => ({ ...current, currentView: view }));
            }}
            recentTasks={state.tasks}
          />
        </div>

        <main className="panel-glass flex h-[calc(100vh-2rem)] flex-col overflow-hidden lg:h-[calc(100vh-3rem)]">
          <header className="flex flex-wrap items-center justify-between gap-4 border-b border-border px-5 py-4">
            <div>
              <p className="text-xs uppercase tracking-[0.24em] text-muted-foreground">Current View</p>
              <h2 className="mt-1 text-2xl font-semibold tracking-tight">{title}</h2>
            </div>
            <div className="flex items-center gap-3">
              <Badge variant="success">{mountedWikiName}</Badge>
              {hasAdmin ? (
                <>
                  <Badge>Admin unlocked</Badge>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      clearAdminToken();
                      setAdminToken("");
                    }}
                  >
                    <LogOut className="mr-2 h-4 w-4" />
                    退出
                  </Button>
                </>
              ) : (
                <Button size="sm" variant="outline" onClick={() => navigate("/login")}>
                  <LockKeyhole className="mr-2 h-4 w-4" />
                  管理员登录
                </Button>
              )}
            </div>
          </header>

          <div className="flex-1 overflow-hidden p-5">
            {state.currentView === "chat" && (
              <ChatPanel
                chats={state.chats}
                onChatsChange={(chats) => setState((current) => ({ ...current, chats }))}
                onSelectAnswer={setSelectedChat}
              />
            )}

            {state.currentView === "tasks" && hasAdmin && (
              <TaskCenter token={adminToken} tasks={state.tasks} onTaskUpdate={updateTasks} onSelectTask={setSelectedTask} />
            )}

            {state.currentView !== "chat" && state.currentView !== "tasks" && hasAdmin && (
              <AdminPanels
                token={adminToken}
                view={state.currentView as Exclude<ViewKey, "chat" | "tasks">}
                onTaskCreated={(task) => {
                  updateTasks(task);
                  setSelectedTask(task);
                }}
                onSelectTask={setSelectedTask}
              />
            )}

            {state.currentView !== "chat" && state.currentView !== "tasks" && !hasAdmin && (
              <div className="flex h-full items-center justify-center rounded-3xl border border-dashed border-border bg-white/50 p-8 text-center text-muted-foreground">
                管理员功能已锁定，请先登录后再执行后台操作。
              </div>
            )}
          </div>
        </main>

        <div className="h-[calc(100vh-2rem)] lg:h-[calc(100vh-3rem)]">
          <DetailPanel mountedWikiName={mountedWikiName} selectedChat={selectedChat} selectedTask={selectedTask} />
        </div>
      </div>
    </div>
  );
}
