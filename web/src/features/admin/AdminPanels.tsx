import { useMemo, useState } from "react";
import type { TaskRecord } from "@/types/api";
import { api } from "@/lib/api";
import { stripEmpty } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";

type ViewKey = "query" | "ingest" | "lint" | "reflect" | "repair" | "sync";

type Props = {
  view: ViewKey;
  token: string;
  onTaskCreated: (task: TaskRecord) => void;
  onSelectTask: (task: TaskRecord) => void;
};

export function AdminPanels({ view, token, onTaskCreated, onSelectTask }: Props) {
  const [queryQuestion, setQueryQuestion] = useState("知识库系统规则是什么？");
  const [queryWriteOutput, setQueryWriteOutput] = useState(true);
  const [ingestPath, setIngestPath] = useState("raw/example.md");
  const [ingestInteractive, setIngestInteractive] = useState(false);
  const [reflectTopic, setReflectTopic] = useState("当前知识库中的模式与缺口");
  const [reflectWriteReport, setReflectWriteReport] = useState(true);
  const [reflectAutoFix, setReflectAutoFix] = useState(false);
  const [repairPath, setRepairPath] = useState("wiki/concepts/example.md");
  const [repairOps, setRepairOps] = useState(
    JSON.stringify(
      [
        {
          type: "append_section",
          section: "## Evolution Log",
          content: "- 2026-04-22：手工测试 low risk repair。",
        },
      ],
      null,
      2,
    ),
  );
  const [proposalId, setProposalId] = useState("");
  const [syncMessage, setSyncMessage] = useState("chore: sync wiki updates");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const title = useMemo(() => {
    switch (view) {
      case "query":
        return { title: "管理员查询", description: "触发深度查询，自动写入任务中心并轮询结果。" };
      case "ingest":
        return { title: "摄入来源", description: "将 raw 下的文件摄入到 wiki/source 并刷新索引。" };
      case "lint":
        return { title: "健康检查", description: "执行 lint 和 qmd 状态检查。" };
      case "reflect":
        return { title: "反思分析", description: "执行 Stage 0-3 反思与 gap report。" };
      case "repair":
        return { title: "修复执行", description: "执行 low-risk repair 或应用 proposal。" };
      case "sync":
        return { title: "同步 Wiki", description: "执行 git status / commit / push。" };
    }
  }, [view]);

  async function runTask(request: Promise<{ task_id: string }>) {
    setLoading(true);
    setError("");
    try {
      const accepted = await request;
      const task = await api.pollTask(token, accepted.task_id, (tick) => {
        onTaskCreated(tick);
      });
      onTaskCreated(task);
      onSelectTask(task);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "request failed");
    } finally {
      setLoading(false);
    }
  }

  function submitRepairLowRisk() {
    let ops: unknown;
    try {
      ops = JSON.parse(repairOps);
    } catch {
      setError("repair ops 必须是合法 JSON。");
      return;
    }
    void runTask(
      api.adminRepairLowRisk(token, {
        path: repairPath,
        ops,
      }),
    );
  }

  return (
    <Card className="flex h-full flex-col">
      <CardHeader>
        <CardTitle>{title.title}</CardTitle>
        <CardDescription>{title.description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {error && (
          <div className="rounded-2xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
            {error}
          </div>
        )}
        {view === "query" && (
          <>
            <Input value={queryQuestion} onChange={(event) => setQueryQuestion(event.target.value)} />
            <label className="flex items-center gap-3 text-sm text-muted-foreground">
              <input
                checked={queryWriteOutput}
                className="h-4 w-4 rounded border-border"
                onChange={(event) => setQueryWriteOutput(event.target.checked)}
                type="checkbox"
              />
              write_output
            </label>
            <Button disabled={loading} onClick={() => void runTask(api.adminQuery(token, { question: queryQuestion, write_output: queryWriteOutput }))}>
              执行管理员查询
            </Button>
          </>
        )}

        {view === "ingest" && (
          <>
            <Input value={ingestPath} onChange={(event) => setIngestPath(event.target.value)} />
            <label className="flex items-center gap-3 text-sm text-muted-foreground">
              <input
                checked={ingestInteractive}
                className="h-4 w-4 rounded border-border"
                onChange={(event) => setIngestInteractive(event.target.checked)}
                type="checkbox"
              />
              interactive
            </label>
            <Button
              disabled={loading}
              onClick={() =>
                void runTask(
                  api.adminIngest(token, {
                    input_type: "file",
                    path: ingestPath,
                    interactive: ingestInteractive,
                  }),
                )
              }
            >
              开始摄入
            </Button>
          </>
        )}

        {view === "lint" && (
          <Button
            disabled={loading}
            onClick={() => void runTask(api.adminLint(token, { write_report: true, auto_fix_low_risk: false }))}
          >
            执行健康检查
          </Button>
        )}

        {view === "reflect" && (
          <>
            <Input value={reflectTopic} onChange={(event) => setReflectTopic(event.target.value)} />
            <div className="flex flex-wrap gap-4 text-sm text-muted-foreground">
              <label className="flex items-center gap-3">
                <input
                  checked={reflectWriteReport}
                  className="h-4 w-4 rounded border-border"
                  onChange={(event) => setReflectWriteReport(event.target.checked)}
                  type="checkbox"
                />
                write_report
              </label>
              <label className="flex items-center gap-3">
                <input
                  checked={reflectAutoFix}
                  className="h-4 w-4 rounded border-border"
                  onChange={(event) => setReflectAutoFix(event.target.checked)}
                  type="checkbox"
                />
                auto_fix_low_risk
              </label>
            </div>
            <Button
              disabled={loading}
              onClick={() =>
                void runTask(
                  api.adminReflect(token, {
                    topic: reflectTopic,
                    write_report: reflectWriteReport,
                    auto_fix_low_risk: reflectAutoFix,
                  }),
                )
              }
            >
              执行反思分析
            </Button>
          </>
        )}

        {view === "repair" && (
          <div className="grid gap-6 lg:grid-cols-2">
            <div className="space-y-4 rounded-3xl border border-border p-4">
              <p className="font-medium">Low-risk repair</p>
              <Input value={repairPath} onChange={(event) => setRepairPath(event.target.value)} />
              <Textarea value={repairOps} onChange={(event) => setRepairOps(event.target.value)} />
              <Button disabled={loading} onClick={submitRepairLowRisk}>
                应用 low risk 修复
              </Button>
            </div>
            <div className="space-y-4 rounded-3xl border border-border p-4">
              <p className="font-medium">Apply proposal</p>
              <Input value={proposalId} onChange={(event) => setProposalId(event.target.value)} placeholder="proposal_xxx" />
              <Button
                disabled={loading || !proposalId.trim()}
                variant="secondary"
                onClick={() => void runTask(api.adminRepairProposal(token, stripEmpty({ proposal_id: proposalId.trim() })))}
              >
                应用 proposal
              </Button>
            </div>
          </div>
        )}

        {view === "sync" && (
          <>
            <Input value={syncMessage} onChange={(event) => setSyncMessage(event.target.value)} />
            <Button disabled={loading} onClick={() => void runTask(api.adminSync(token, { message: syncMessage }))}>
              执行同步
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  );
}
