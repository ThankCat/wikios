import type { TaskRecord } from "@/types/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { useState } from "react";
import { api } from "@/lib/api";

type Props = {
  token: string;
  tasks: TaskRecord[];
  onTaskUpdate: (task: TaskRecord) => void;
  onSelectTask: (task: TaskRecord) => void;
};

export function TaskCenter({ token, tasks, onTaskUpdate, onSelectTask }: Props) {
  const [manualTaskId, setManualTaskId] = useState("");

  async function fetchTask() {
    if (!manualTaskId.trim()) {
      return;
    }
    const task = await api.task(token, manualTaskId.trim());
    onTaskUpdate(task);
    onSelectTask(task);
  }

  return (
    <Card className="flex h-full flex-col">
      <CardHeader>
        <CardTitle>任务中心</CardTitle>
        <CardDescription>统一查看所有异步管理员任务并继续轮询状态。</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-4">
        <div className="flex gap-3">
          <Input placeholder="输入 task id" value={manualTaskId} onChange={(event) => setManualTaskId(event.target.value)} />
          <Button variant="secondary" onClick={() => void fetchTask()}>
            查询任务
          </Button>
        </div>
        <ScrollArea className="flex-1 pr-3">
          <div className="space-y-3">
            {tasks.length === 0 ? (
              <div className="rounded-3xl border border-dashed border-border p-8 text-sm text-muted-foreground">
                暂无任务，执行任意管理员操作后会自动进入任务中心。
              </div>
            ) : (
              tasks.map((task) => (
                <button
                  className="w-full rounded-3xl border border-border bg-white/70 p-4 text-left transition hover:border-primary/40"
                  key={task.id}
                  onClick={() => onSelectTask(task)}
                  type="button"
                >
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="text-xs uppercase tracking-[0.2em] text-muted-foreground">{task.type}</p>
                      <p className="mt-2 font-mono text-xs">{task.id}</p>
                    </div>
                    <Badge
                      variant={
                        task.status === "SUCCESS"
                          ? "success"
                          : task.status === "FAILED"
                            ? "danger"
                            : "warning"
                      }
                    >
                      {task.status}
                    </Badge>
                  </div>
                  {typeof task.result?.summary === "string" && (
                    <p className="mt-3 text-sm text-muted-foreground">{task.result.summary}</p>
                  )}
                  {typeof task.error === "string" && task.error.trim() !== "" && (
                    <p className="mt-3 text-sm text-rose-600">{task.error}</p>
                  )}
                </button>
              ))
            )}
          </div>
        </ScrollArea>
      </CardContent>
    </Card>
  );
}
