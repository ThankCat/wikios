import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { PublicAnswerResponse, TaskRecord } from "@/types/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { formatJSON } from "@/lib/utils";

type Props = {
  mountedWikiName: string;
  selectedChat?: PublicAnswerResponse | null;
  selectedTask?: TaskRecord | null;
};

export function DetailPanel({ mountedWikiName, selectedChat, selectedTask }: Props) {
  return (
    <div className="flex h-full flex-col gap-4">
      <Card className="min-h-[160px]">
        <CardHeader>
          <CardTitle>环境摘要</CardTitle>
          <CardDescription>当前工作台连接到的后端信息。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">Mounted Wiki</span>
              <Badge variant="success">{mountedWikiName}</Badge>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">Frontend Mode</span>
              <Badge>same-origin</Badge>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card className="flex-1">
        <CardHeader>
          <CardTitle>详情面板</CardTitle>
          <CardDescription>聊天来源、报告、任务结果和结构化输出。</CardDescription>
        </CardHeader>
        <CardContent className="h-[calc(100%-88px)]">
          <ScrollArea className="h-full pr-3">
            {selectedTask ? (
              <div className="space-y-5 text-sm">
                <div className="space-y-2">
                  <p className="text-xs uppercase tracking-[0.2em] text-muted-foreground">Task</p>
                  <p className="font-mono text-xs">{selectedTask.id}</p>
                  <Badge variant={selectedTask.status === "SUCCESS" ? "success" : selectedTask.status === "FAILED" ? "danger" : "warning"}>
                    {selectedTask.status}
                  </Badge>
                </div>
                <div>
                  <p className="mb-2 text-xs uppercase tracking-[0.2em] text-muted-foreground">Result</p>
                  <pre className="overflow-auto rounded-2xl bg-slate-950 p-4 text-xs text-slate-100">
                    {formatJSON(selectedTask.result ?? {})}
                  </pre>
                </div>
                {typeof selectedTask.error === "string" && selectedTask.error.trim() !== "" && (
                  <div className="rounded-2xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
                    {selectedTask.error}
                  </div>
                )}
                {selectedTask.steps.length > 0 && (
                  <div className="space-y-3">
                    <p className="text-xs uppercase tracking-[0.2em] text-muted-foreground">Steps</p>
                    {selectedTask.steps.map((step, index) => (
                      <div key={`${step.name}-${index}`} className="rounded-2xl border border-border p-3">
                        <div className="flex items-center justify-between gap-3">
                          <p className="font-medium">{step.name}</p>
                          <Badge variant={step.status === "SUCCESS" ? "success" : "danger"}>{step.status}</Badge>
                        </div>
                        {step.tool && <p className="mt-1 font-mono text-xs text-muted-foreground">{step.tool}</p>}
                        {typeof step.output?.error === "string" && step.output.error.trim() !== "" && (
                          <p className="mt-2 text-sm text-rose-600">{step.output.error}</p>
                        )}
                      </div>
                    ))}
                  </div>
                )}
                {typeof selectedTask.result?.report === "string" && (
                  <div className="prose prose-slate max-w-none text-sm">
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>
                      {selectedTask.result.report as string}
                    </ReactMarkdown>
                  </div>
                )}
              </div>
            ) : selectedChat ? (
              <div className="space-y-5 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge>{selectedChat.answer_type || "text"}</Badge>
                  <Badge variant="success">{selectedChat.confidence.toFixed(2)}</Badge>
                </div>
                <div className="prose prose-slate max-w-none text-sm">
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>
                    {selectedChat.answer_markdown}
                  </ReactMarkdown>
                </div>
                <div className="space-y-2">
                  <p className="text-xs uppercase tracking-[0.2em] text-muted-foreground">Sources</p>
                  {selectedChat.sources.map((source) => (
                    <div key={source.path} className="rounded-2xl border border-border p-3">
                      <p className="font-medium">{source.title}</p>
                      <p className="mt-1 break-all font-mono text-xs text-muted-foreground">{source.path}</p>
                      <p className="mt-2 text-xs text-muted-foreground">confidence: {source.confidence}</p>
                    </div>
                  ))}
                </div>
              </div>
            ) : (
              <div className="rounded-3xl border border-dashed border-border p-6 text-sm text-muted-foreground">
                选择一条聊天结果或任务记录后，这里会显示来源、报告、输出路径和结构化结果。
              </div>
            )}
          </ScrollArea>
        </CardContent>
      </Card>
    </div>
  );
}
