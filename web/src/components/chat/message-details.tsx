"use client";

import { ReactNode, useMemo, useState } from "react";
import { Activity, AlertTriangle, Bot, Braces, BrainCircuit, Cog, Database, FileJson2, GitBranch, MessageSquareQuote, Route, Search } from "lucide-react";

import { ScrollJumpControls } from "@/components/ui/scroll-jump-controls";
import { TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useScrollFollow } from "@/lib/use-scroll-follow";
import { cn, formatJSON } from "@/lib/utils";

type Props = {
  details: unknown;
  leadingContent?: ReactNode;
  message?: {
    role: "user" | "assistant";
    content: string;
    createdAt?: string;
    statusText?: string;
    answer?: string;
  };
};

type DetailTab = "reasoning" | "model" | "prompt" | "tools" | "execution" | "result";
type CustomerTraceTab = "summary" | "request" | "router" | "observability" | "retrieval" | "specialist" | "final" | "review" | "error";

type TabConfig<T extends string = DetailTab> = {
  id: T;
  label: string;
  icon: typeof MessageSquareQuote;
};

const tabs: TabConfig[] = [
  { id: "reasoning", label: "推理", icon: BrainCircuit },
  { id: "model", label: "模型", icon: Bot },
  { id: "prompt", label: "Prompt", icon: MessageSquareQuote },
  { id: "tools", label: "步骤", icon: Cog },
  { id: "execution", label: "执行", icon: Braces },
  { id: "result", label: "结果", icon: FileJson2 },
];

const customerTraceTabs: TabConfig<CustomerTraceTab>[] = [
  { id: "summary", label: "摘要", icon: FileJson2 },
  { id: "request", label: "请求", icon: MessageSquareQuote },
  { id: "router", label: "路由", icon: Route },
  { id: "observability", label: "观测", icon: Activity },
  { id: "retrieval", label: "检索", icon: Search },
  { id: "specialist", label: "模型", icon: Bot },
  { id: "final", label: "最终", icon: FileJson2 },
  { id: "review", label: "审查", icon: BrainCircuit },
  { id: "error", label: "错误", icon: AlertTriangle },
];

function asObject(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

function specialistPromptMessages(input: Record<string, unknown>) {
  const messages = input.messages;
  if (!Array.isArray(messages)) {
    return [] as Array<{ role: string; content: string }>;
  }
  return messages
    .map((item) => asObject(item))
    .map((item) => ({
      role: textValue(item.role),
      content: textValue(item.content),
    }))
    .filter((item) => item.role !== "" && item.content !== "");
}

function specialistInputSummary(input: Record<string, unknown>) {
  if (!Array.isArray(input.messages)) {
    return input;
  }
  const summary = { ...input };
  delete summary.messages;
  return summary;
}

export function MessageDetails({ details, leadingContent, message }: Props) {
  const object = asObject(details);
  const customerTrace = customerTraceFromDetails(object);

  if (customerTrace) {
    return <CustomerChatTraceDetails trace={customerTrace} leadingContent={leadingContent} />;
  }

  return <LegacyMessageDetails object={object} leadingContent={leadingContent} message={message} />;
}

function LegacyMessageDetails({ object, leadingContent, message }: { object: Record<string, unknown>; leadingContent?: ReactNode; message?: Props["message"] }) {
  const prompts = Array.isArray(object.prompts) ? object.prompts : [];
  const execution = asObject(object.execution);
  const executionSteps = Array.isArray(execution.steps) ? execution.steps : [];
  const steps = Array.isArray(object.steps) ? object.steps : executionSteps;
  const result = object.result ?? object;
  const reasoning = typeof object.reasoning === "string" ? object.reasoning.trim() : "";
  const processSummary = typeof object.process_summary === "string" ? object.process_summary.trim() : "";
  const llmStream = typeof object.llm_stream === "string" ? object.llm_stream.trim() : "";
  const llmDone = asObject(object.llm_done);
  const modelRaw = typeof object.model_json_raw === "string" ? object.model_json_raw.trim() : "";
  const hasModelDetails = llmStream !== "" || modelRaw !== "" || Object.keys(llmDone).length > 0 || object.model_json_parsed != null;

  const availableTabs = useMemo(
    () =>
      tabs.filter((tab) => {
        switch (tab.id) {
          case "reasoning":
            return reasoning.trim() !== "" || processSummary.trim() !== "";
          case "model":
            return hasModelDetails;
          case "prompt":
            return prompts.length > 0;
          case "tools":
            return steps.length > 0;
          case "execution":
            return Object.keys(execution).length > 0;
          case "result":
            return true;
        }
      }),
    [execution, hasModelDetails, processSummary, prompts.length, reasoning, steps.length],
  );

  const [activeTab, setActiveTab] = useState<DetailTab>("result");
  const resolvedTab = availableTabs.some((tab) => tab.id === activeTab) ? activeTab : (availableTabs[0]?.id ?? "result");

  return (
    <div className="min-w-0 space-y-3 break-words text-left [overflow-wrap:anywhere]">
      <div className="sticky top-0 z-10 border-b border-border bg-background/95 py-3 backdrop-blur dark:bg-card/95">
        <TabsList className="flex flex-wrap gap-1">
        {availableTabs.map((tab) => {
          const Icon = tab.icon;
          const active = resolvedTab === tab.id;
          return (
            <TabsTrigger
              key={tab.id}
              active={active}
              onClick={() => setActiveTab(tab.id)}
              className="h-8 gap-2 text-xs"
            >
              <Icon className="h-3.5 w-3.5" />
              {tab.label}
            </TabsTrigger>
          );
        })}
        </TabsList>
      </div>

      {leadingContent}

      {resolvedTab === "reasoning" ? <ReasoningPanel reasoning={reasoning} processSummary={processSummary} /> : null}
      {resolvedTab === "model" ? <ModelPanel llmStream={llmStream} llmDone={llmDone} modelRaw={modelRaw} modelParsed={object.model_json_parsed} /> : null}
      {resolvedTab === "prompt" ? <PromptPanel prompts={prompts} /> : null}
      {resolvedTab === "tools" ? <ToolsPanel steps={steps} /> : null}
      {resolvedTab === "execution" ? <ExecutionPanel execution={execution} /> : null}
      {resolvedTab === "result" ? <ResultPanel result={result} message={message} /> : null}
    </div>
  );
}

function CustomerChatTraceDetails({ trace, leadingContent }: { trace: Record<string, unknown>; leadingContent?: ReactNode }) {
  const [activeTab, setActiveTab] = useState<CustomerTraceTab>("summary");
  const resolvedTab = customerTraceTabs.some((tab) => tab.id === activeTab) ? activeTab : "summary";

  return (
    <div className="min-w-0 space-y-3 break-words text-left [overflow-wrap:anywhere]">
      <div className="sticky top-0 z-10 border-b border-border bg-background/95 py-3 backdrop-blur dark:bg-card/95">
        <TabsList className="flex flex-wrap gap-1">
        {customerTraceTabs.map((tab) => {
          const Icon = tab.icon;
          const active = resolvedTab === tab.id;
          return (
            <TabsTrigger
              key={tab.id}
              active={active}
              onClick={() => setActiveTab(tab.id)}
              className="h-8 gap-2 text-xs"
            >
              <Icon className="h-3.5 w-3.5" />
              {tab.label}
            </TabsTrigger>
          );
        })}
        </TabsList>
      </div>

      {leadingContent}

      {resolvedTab === "summary" ? <CustomerTraceSummaryPanel trace={trace} /> : null}
      {resolvedTab === "request" ? <CustomerTraceRequestPanel request={asObject(trace.request)} /> : null}
      {resolvedTab === "router" ? <CustomerTraceRouterPanel router={asObject(trace.router)} /> : null}
      {resolvedTab === "observability" ? <CustomerTraceObservabilityPanel observability={asObject(trace.observability)} router={asObject(trace.router)} retrieval={asObject(trace.retrieval)} /> : null}
      {resolvedTab === "retrieval" ? <CustomerTraceRetrievalPanel retrieval={asObject(trace.retrieval)} /> : null}
      {resolvedTab === "specialist" ? <CustomerTraceSpecialistPanel specialist={asObject(trace.specialist)} /> : null}
      {resolvedTab === "final" ? <CustomerTraceFinalPanel final={asObject(trace.final)} /> : null}
      {resolvedTab === "review" ? <CustomerTraceReviewPanel review={asObject(trace.review)} /> : null}
      {resolvedTab === "error" ? <CustomerTraceErrorPanel error={trace.error} /> : null}
    </div>
  );
}

function CustomerTraceSummaryPanel({ trace }: { trace: Record<string, unknown> }) {
  const time = asObject(trace.time);
  const runtime = asObject(trace.runtime);
  const knownKeys = new Set(["schema_version", "record_type", "trace_id", "session_id", "time", "runtime", "request", "router", "observability", "retrieval", "specialist", "final", "error", "review"]);
  const extra = Object.fromEntries(Object.entries(trace).filter(([key]) => !knownKeys.has(key)));
  const errorObject = trace.error == null ? null : asObject(trace.error);
  const final = asObject(trace.final);
  const observability = asObject(trace.observability);
  const decision = asObject(observability.decision);
  const qualitySignals = Array.isArray(observability.quality_signals) ? observability.quality_signals : [];

  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-3 flex items-center gap-2 text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">
          <GitBranch className="h-3.5 w-3.5" />
          链路摘要
        </div>
        <div className="grid min-w-0 gap-2 md:grid-cols-2">
          <SummaryLine label="Trace ID" value={textValue(trace.trace_id) || "-"} />
          <SummaryLine label="会话" value={textValue(trace.session_id) || "-"} />
          <SummaryLine label="入口" value={textValue(runtime.entrypoint) || "-"} />
          <SummaryLine label="渠道" value={textValue(runtime.client_channel) || "web"} />
          <SummaryLine label="测试请求" value={simulationText(runtime.simulation)} />
          <SummaryLine label="模式" value={textValue(runtime.customer_chat_mode) || "-"} />
          <SummaryLine label="回答模式" value={textValue(final.answer_mode) || "-"} />
          <SummaryLine label="用户意图" value={userIntentLabel(textValue(asObject(final.user_intent).type))} />
          <SummaryLine label="问题阶段" value={stageLabel(textValue(decision.question_stage))} />
          <SummaryLine label="回答策略" value={strategyLabel(textValue(decision.answer_strategy))} />
          <SummaryLine label="质量信号" value={qualitySignals.length ? `${qualitySignals.length} 条` : "无"} />
          <SummaryLine label="总耗时" value={durationText(time.total_duration_ms)} />
          <SummaryLine label="错误状态" value={errorObject ? textValue(errorObject.stage) || "有错误" : "无错误"} />
          <SummaryLine label="接收时间" value={dateText(time.received_at)} />
          <SummaryLine label="完成时间" value={dateText(time.answered_at)} />
          <SummaryLine label="记录时间" value={dateText(time.logged_at)} />
          <SummaryLine label="代码版本" value={textValue(runtime.git_commit) || "未记录"} />
          <SummaryLine label="Schema" value={textValue(trace.schema_version) || "-"} />
          <SummaryLine label="记录类型" value={textValue(trace.record_type) || "-"} />
        </div>
      </section>
      <PanelBlock title="运行信息" value={runtime} />
      <PanelBlock title="时间" value={time} />
      {Object.keys(extra).length > 0 ? <PanelBlock title="其他顶级字段" value={extra} /> : null}
    </div>
  );
}

function CustomerTraceRequestPanel({ request }: { request: Record<string, unknown> }) {
  const context = Array.isArray(request.conversation_context) ? request.conversation_context : [];
  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-2 text-xs font-medium text-muted-foreground">客户输入</div>
        <div className="space-y-2">
          <SummaryLine label="当前消息" value={textValue(request.message) || "-"} multiline />
          <SummaryLine label="历史摘要" value={textValue(request.history_summary) || "-"} multiline />
          <SummaryLine label="历史轮数" value={textValue(request.history_turns) || "0"} />
          {request.history_message_count != null ? <SummaryLine label="历史消息数" value={textValue(request.history_message_count) || "0"} /> : null}
        </div>
      </section>
      {context.length > 0 ? (
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-3 text-xs font-medium text-muted-foreground">对话上下文</div>
          <div className="space-y-2">
            {context.map((item, index) => {
              const row = asObject(item);
              return (
                <div key={index} className="rounded-md border border-border bg-muted/40 p-3 dark:border-border dark:bg-secondary/60">
                  <div className="mb-2 text-[11px] font-semibold text-muted-foreground">第 {index + 1} 轮</div>
                  <SummaryLine label="问题" value={textValue(row.question) || "-"} multiline />
                  <div className="mt-2">
                    <SummaryLine label="回答" value={textValue(row.answer) || "-"} multiline />
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      ) : null}
      <PanelBlock title="请求 JSON" value={request} />
    </div>
  );
}

function CustomerTraceRouterPanel({ router }: { router: Record<string, unknown> }) {
  const model = asObject(router.model);
  const output = asObject(router.output);
  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-3 text-xs font-medium text-muted-foreground">路由判断</div>
        <div className="grid gap-2 md:grid-cols-2">
          <SummaryLine label="模型" value={modelLabel(model)} />
          <SummaryLine label="耗时" value={durationText(router.duration_ms)} />
          <SummaryLine label="目标角色" value={textValue(output.specialist) || "-"} />
          <SummaryLine label="问题阶段" value={stageLabel(textValue(output.question_stage))} />
          <SummaryLine label="客户目标" value={textValue(output.user_goal) || "-"} multiline />
          <SummaryLine label="回答策略" value={strategyLabel(textValue(output.answer_strategy))} />
          <SummaryLine label="边界" value={boundaryLabel(textValue(output.risk_boundary))} />
          <SummaryLine label="已有产品" value={booleanText(output.has_product)} />
          <SummaryLine label="需问产品" value={booleanText(output.needs_product_clarification)} />
          <SummaryLine label="追问槽位" value={textValue(output.clarification_target) || "-"} />
          <SummaryLine label="意图" value={textValue(output.intent) || "-"} />
          <SummaryLine label="路由置信度" value={textValue(output.routing_confidence) || "-"} />
          <SummaryLine label="是否检索" value={booleanText(output.needs_retrieval)} />
          <SummaryLine label="改写问题" value={textValue(output.rewritten_question) || "-"} multiline />
          <SummaryLine label="路由原因" value={textValue(output.routing_reason) || "-"} multiline />
          <SummaryLine label="交接备注" value={textValue(output.handoff_notes) || "-"} multiline />
        </div>
      </section>
      <ThinkingBlock title="路由思考" thinking={asObject(router.thinking)} />
      <div className="grid gap-3 md:grid-cols-2">
        <PanelBlock title="槽位 Slots" value={asObject(output.slots)} />
        <PanelBlock title="歧义 Ambiguity" value={asObject(output.ambiguity)} />
        <PanelBlock title="缺失信息 Missing Info" value={output.missing_info ?? []} />
        <PanelBlock title="风险标记 Risk Flags" value={output.risk_flags ?? []} />
      </div>
      <PanelBlock title="检索 Query" value={output.retrieval_queries ?? []} />
      <PanelBlock title="路由输出 JSON" value={output} />
      {router.raw_output ? <PanelBlock title="路由原始输出" value={router.raw_output} /> : null}
    </div>
  );
}

function CustomerTraceObservabilityPanel({
  observability,
  router,
  retrieval,
}: {
  observability: Record<string, unknown>;
  router: Record<string, unknown>;
  retrieval: Record<string, unknown>;
}) {
  const routerOutput = asObject(router.output);
  const decision = Object.keys(asObject(observability.decision)).length > 0 ? asObject(observability.decision) : routerOutput;
  const clarification = asObject(observability.clarification);
  const hardStop = asObject(observability.hard_stop);
  const diagnostics = Object.keys(asObject(observability.retrieval_diagnostics)).length > 0 ? asObject(observability.retrieval_diagnostics) : retrieval;
  const signals = Array.isArray(observability.quality_signals) ? observability.quality_signals : [];

  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-3 flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Activity className="h-3.5 w-3.5" />
          决策观测
        </div>
        <div className="grid gap-2 md:grid-cols-2">
          <SummaryLine label="问题阶段" value={stageLabel(textValue(decision.question_stage))} />
          <SummaryLine label="客户目标" value={textValue(decision.user_goal) || "-"} multiline />
          <SummaryLine label="目标角色" value={textValue(decision.specialist) || "-"} />
          <SummaryLine label="回答策略" value={strategyLabel(textValue(decision.answer_strategy))} />
          <SummaryLine label="边界" value={boundaryLabel(textValue(decision.risk_boundary))} />
          <SummaryLine label="路由置信度" value={textValue(decision.routing_confidence) || "-"} />
          <SummaryLine label="需要检索" value={booleanText(decision.needs_retrieval)} />
          <SummaryLine label="Query 数" value={textValue(decision.retrieval_query_count) || "-"} />
        </div>
      </section>
      <div className="grid gap-3 md:grid-cols-2">
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-3 text-xs font-medium text-muted-foreground">澄清判断</div>
          <div className="space-y-2">
            <SummaryLine label="需要澄清" value={booleanText(clarification.needed ?? decision.needs_product_clarification)} />
            <SummaryLine label="目标槽位" value={textValue(clarification.target ?? decision.clarification_target) || "-"} />
            <SummaryLine label="原因" value={textValue(clarification.reason) || "-"} multiline />
          </div>
        </section>
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-3 text-xs font-medium text-muted-foreground">硬停判断</div>
          <div className="space-y-2">
            <SummaryLine label="类型" value={textValue(hardStop.type) || "none"} />
            <SummaryLine label="边界" value={boundaryLabel(textValue(hardStop.risk_boundary ?? decision.risk_boundary))} />
            <SummaryLine label="备注" value={textValue(hardStop.handoff_notes) || "-"} multiline />
          </div>
        </section>
      </div>
      <QualitySignalList items={signals} />
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-3 text-xs font-medium text-muted-foreground">检索诊断</div>
        <div className="grid gap-2 md:grid-cols-2">
          <SummaryLine label="请求检索" value={booleanText(diagnostics.requested ?? routerOutput.needs_retrieval)} />
          <SummaryLine label="实际执行" value={booleanText(diagnostics.executed)} />
          <SummaryLine label="候选数" value={textValue(diagnostics.candidate_count) || textValue(diagnostics.source_count) || "0"} />
          <SummaryLine label="证据数" value={textValue(diagnostics.source_count) || "0"} />
          <SummaryLine label="TopK" value={textValue(diagnostics.candidate_top_k) || "-"} />
          <SummaryLine label="耗时" value={durationText(diagnostics.duration_ms ?? retrieval.duration_ms)} />
        </div>
      </section>
      <PanelBlock title="观测 JSON" value={observability} />
    </div>
  );
}

function QualitySignalList({ items }: { items: unknown[] }) {
  if (items.length === 0) {
    return (
      <section className="min-w-0 rounded-lg border border-border bg-muted/40 p-4 text-sm text-foreground">
        没有记录质量预警。
      </section>
    );
  }
  return (
    <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
      <div className="mb-3 text-xs font-medium text-muted-foreground">质量信号</div>
      <div className="space-y-2">
        {items.map((item, index) => {
          const signal = asObject(item);
          const severity = textValue(signal.severity);
          return (
            <div
              key={index}
              className={cn(
                "rounded-md border p-3",
                severity === "critical"
                  ? "border-destructive/30 bg-destructive/10 text-destructive"
                  : severity === "warning"
                    ? "border-amber-500/30 bg-amber-500/10 text-amber-900 dark:text-amber-200"
                    : "border-border bg-muted/40 text-foreground dark:border-border dark:bg-secondary/60",
              )}
            >
              <div className="text-xs font-semibold">{textValue(signal.code) || `signal_${index + 1}`}</div>
              <div className="mt-1 text-sm leading-6">{textValue(signal.message) || "-"}</div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

function CustomerTraceRetrievalPanel({ retrieval }: { retrieval: Record<string, unknown> }) {
  const evidencePreview = Array.isArray(retrieval.evidence_preview) ? retrieval.evidence_preview : [];
  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-3 flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Database className="h-3.5 w-3.5" />
          检索概览
        </div>
        <div className="grid gap-2 md:grid-cols-2">
          <SummaryLine label="目标角色" value={textValue(retrieval.target_specialist) || "-"} />
          <SummaryLine label="范围" value={textValue(retrieval.scope) || "-"} />
          <SummaryLine label="耗时" value={durationText(retrieval.duration_ms)} />
          <SummaryLine label="执行方" value={textValue(retrieval.executed_by) || "-"} />
          <SummaryLine label="QMD 缓存" value={`hit ${textValue(retrieval.qmd_cache_hits) || "0"} / miss ${textValue(retrieval.qmd_cache_misses) || "0"}`} />
          <SummaryLine label="页面缓存" value={`hit ${textValue(retrieval.page_cache_hits) || "0"} / miss ${textValue(retrieval.page_cache_misses) || "0"}`} />
          <SummaryLine label="跳过 Query 数" value={textValue(retrieval.skipped_query_count) || "0"} />
          <SummaryLine label="证据源数量" value={String(Array.isArray(retrieval.sources) ? retrieval.sources.length : 0)} />
        </div>
      </section>
      <div className="grid gap-3 md:grid-cols-2">
        <PanelBlock title="尝试 Query" value={retrieval.attempted_queries ?? []} />
        <PanelBlock title="执行 Query" value={retrieval.executed_queries ?? []} />
        <PanelBlock title="候选结果 Candidates" value={retrieval.candidates ?? []} />
        <PanelBlock title="来源 Sources" value={retrieval.sources ?? []} />
        <PanelBlock title="Query 耗时" value={retrieval.query_timings ?? []} />
        <PanelBlock title="页面耗时" value={retrieval.page_timings ?? []} />
      </div>
      {evidencePreview.length > 0 ? <EvidencePreviewList items={evidencePreview} /> : null}
      <PanelBlock title="检索 JSON" value={retrieval} />
    </div>
  );
}

function CustomerTraceSpecialistPanel({ specialist }: { specialist: Record<string, unknown> }) {
  const model = asObject(specialist.model);
  const output = asObject(specialist.output);
  const input = asObject(specialist.input);
  const promptMessages = specialistPromptMessages(input);
  const inputSummary = specialistInputSummary(input);
  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-3 text-xs font-medium text-muted-foreground">模型回答</div>
        <div className="grid gap-2 md:grid-cols-2">
          <SummaryLine label="目标角色" value={textValue(specialist.name) || "-"} />
          <SummaryLine label="模型" value={modelLabel(model)} />
          <SummaryLine label="耗时" value={durationText(specialist.duration_ms)} />
          <SummaryLine label="回答模式" value={textValue(output.answer_mode) || "-"} />
          <SummaryLine label="置信度" value={textValue(output.confidence) || "-"} />
          <SummaryLine label="证据置信度" value={textValue(output.evidence_confidence) || "-"} />
          <SummaryLine label="需要审查" value={booleanText(output.review_required)} />
          <SummaryLine label="备注" value={textValue(output.notes) || "-"} multiline />
        </div>
      </section>
      <ThinkingBlock title="模型思考" thinking={asObject(specialist.thinking)} />
      <SummaryLine label="客户可见答案" value={textValue(output.answer) || "-"} multiline />
      {promptMessages.length > 0 ? (
        <div className="space-y-3">
          {promptMessages.map((message) => (
            <PanelBlock
              key={message.role}
              title={message.role === "system" ? "System Prompt（完整）" : "User Prompt（完整）"}
              value={message.content}
            />
          ))}
        </div>
      ) : null}
      <div className="grid gap-3 md:grid-cols-2">
        <PanelBlock title={promptMessages.length > 0 ? "输入摘要" : "输入 Input"} value={inputSummary} />
        <PanelBlock title="来源 Sources" value={output.sources ?? []} />
      </div>
      <PanelBlock title="模型输出 JSON" value={output} />
      {specialist.raw_output ? <PanelBlock title="模型原始输出" value={specialist.raw_output} /> : null}
    </div>
  );
}

function CustomerTraceFinalPanel({ final }: { final: Record<string, unknown> }) {
  const userIntent = asObject(final.user_intent);
  const intentType = textValue(userIntent.type);
  const intentExtra = asObject(userIntent.extra);
  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-3 text-xs font-medium text-muted-foreground">最终响应</div>
        <div className="grid gap-2 md:grid-cols-2">
          <SummaryLine label="回答模式" value={textValue(final.answer_mode) || "-"} />
          <SummaryLine label="来源数" value={textValue(final.source_count) || "0"} />
          <SummaryLine label="需要审查" value={booleanText(final.review_required)} />
          <SummaryLine label="用户意图" value={userIntentLabel(intentType)} />
          {intentType === "discount" ? (
            <>
              <SummaryLine label="意图·产品类型" value={productTypeLabel(textValue(intentExtra.product_type))} />
              <SummaryLine label="意图·数量" value={textValue(intentExtra.quantity) || "-"} />
            </>
          ) : null}
        </div>
        <div className="mt-3">
          <SummaryLine label="客户可见答案" value={textValue(final.answer) || "-"} multiline />
        </div>
      </section>
      <PanelBlock title="最终 JSON" value={final} />
    </div>
  );
}

function CustomerTraceReviewPanel({ review }: { review: Record<string, unknown> }) {
  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-3 text-xs font-medium text-muted-foreground">人工审查</div>
        <div className="grid gap-2 md:grid-cols-2">
          <SummaryLine label="状态" value={textValue(review.status) || "unreviewed"} />
          <SummaryLine label="是否好回答" value={review.is_good_answer == null ? "未评审" : booleanText(review.is_good_answer)} />
          <SummaryLine label="错误类型" value={textValue(review.error_type) || "-"} />
          <SummaryLine label="审核人" value={textValue(review.reviewed_by) || "-"} />
          <SummaryLine label="审核时间" value={dateText(review.reviewed_at)} />
          <SummaryLine label="备注" value={textValue(review.note) || "-"} multiline />
          <SummaryLine label="正确答案" value={textValue(review.correct_answer) || "-"} multiline />
        </div>
      </section>
      <PanelBlock title="审查 JSON" value={review} />
    </div>
  );
}

function CustomerTraceErrorPanel({ error }: { error: unknown }) {
  if (error == null) {
    return (
      <section className="min-w-0 rounded-lg border border-border bg-muted/40 p-4 text-sm text-foreground">
        本轮链路没有记录错误。
      </section>
    );
  }
  const object = asObject(error);
  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-destructive">
        <div className="mb-2 text-xs font-medium">错误摘要</div>
        <div className="space-y-2">
          <SummaryLine label="阶段" value={textValue(object.stage) || "-"} />
          <SummaryLine label="消息" value={textValue(object.message) || "-"} multiline />
        </div>
      </section>
      <PanelBlock title="错误 JSON" value={error} />
    </div>
  );
}

function ThinkingBlock({ title, thinking }: { title: string; thinking: Record<string, unknown> }) {
  const enabled = thinking.enabled === true;
  const saved = thinking.saved === true;
  const content = textValue(thinking.content);
  const reason = textValue(thinking.unavailable_reason);
  const omitted = thinking.omitted === true;
  const emptyMessage = omitted
    ? "thinking 内容未持久化，只保留是否产生和字符数。"
    : reason || "没有保存 thinking 内容。";
  return (
    <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
      <div className="mb-3 text-xs font-medium text-muted-foreground">{title}</div>
      <div className="mb-3 grid gap-2 md:grid-cols-3">
        <SummaryLine label="启用" value={enabled ? "true" : "false"} />
        <SummaryLine label="已保存" value={saved ? "true" : "false"} />
        <SummaryLine label="字符数" value={textValue(thinking.chars) || "0"} />
      </div>
      {content ? <CodeBlock value={content} /> : <div className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">{emptyMessage}</div>}
    </section>
  );
}

function EvidencePreviewList({ items }: { items: unknown[] }) {
  return (
    <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
      <div className="mb-3 text-xs font-medium text-muted-foreground">证据预览</div>
      <div className="space-y-2">
        {items.map((item, index) => {
          const data = asObject(item);
          return (
            <details key={index} className="rounded-md border border-border bg-muted/40 p-3 dark:border-border dark:bg-secondary/60">
              <summary className="cursor-pointer">
                <div className="inline-flex max-w-full flex-col gap-1 align-top">
                  <span className="break-words text-sm font-semibold text-foreground dark:text-foreground [overflow-wrap:anywhere]">
                    {textValue(data.title) || textValue(data.path) || `Evidence ${index + 1}`}
                  </span>
                  <span className="break-words font-mono text-[11px] text-muted-foreground [overflow-wrap:anywhere]">
                    {textValue(data.path)}
                    {data.confidence ? ` · ${textValue(data.confidence)}` : ""}
                    {data.body_chars ? ` · ${textValue(data.body_chars)} chars` : ""}
                  </span>
                </div>
              </summary>
              <div className="mt-3">
                <CodeBlock value={textValue(data.preview) || data} />
              </div>
            </details>
          );
        })}
      </div>
    </section>
  );
}

function ReasoningPanel({ reasoning, processSummary }: { reasoning: string; processSummary: string }) {
  return (
    <div className="min-w-0 space-y-3">
      {reasoning ? (
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">Model Reasoning Stream</div>
          <CodeBlock value={reasoning} />
        </section>
      ) : null}
      {processSummary ? (
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">Process Summary</div>
          <CodeBlock value={processSummary} />
        </section>
      ) : null}
    </div>
  );
}

function ModelPanel({
  llmStream,
  llmDone,
  modelRaw,
  modelParsed,
}: {
  llmStream: string;
  llmDone: Record<string, unknown>;
  modelRaw: string;
  modelParsed: unknown;
}) {
  return (
    <div className="min-w-0 space-y-3">
      {llmStream ? (
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">Answer Markdown Stream</div>
          <CodeBlock value={llmStream} />
        </section>
      ) : null}
      {Object.keys(llmDone).length > 0 ? (
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">LLM Done Event</div>
          <CodeBlock value={llmDone} />
        </section>
      ) : null}
      {modelParsed != null ? (
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">Parsed Model JSON</div>
          <CodeBlock value={modelParsed} />
        </section>
      ) : null}
      {modelRaw ? (
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">Raw Model JSON</div>
          <CodeBlock value={modelRaw} />
        </section>
      ) : null}
    </div>
  );
}

function PromptPanel({ prompts }: { prompts: unknown[] }) {
  return (
    <div className="min-w-0 space-y-3">
      {prompts.map((prompt, index) => {
        const data = asObject(prompt);
        const messages = Array.isArray(data.messages) ? data.messages : [];
        return (
          <details key={index} className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
            <summary className="mb-3 flex cursor-pointer flex-wrap items-start justify-between gap-3">
              <div className="min-w-0 flex-1">
                <div className="break-words text-xs font-semibold text-foreground [overflow-wrap:anywhere]">{String(data.name ?? `Prompt ${index + 1}`)}</div>
                <div className="mt-1 break-words text-[11px] text-muted-foreground [overflow-wrap:anywhere]">
                  model: {String(data.model ?? "unknown")}
                  {data.created_at ? ` · ${formatDateTime(String(data.created_at))}` : ""}
                </div>
              </div>
              <div className="shrink-0 rounded-md bg-muted px-2 py-1 text-[11px] text-muted-foreground dark:bg-secondary dark:text-muted-foreground">{messages.length} messages</div>
            </summary>
            <div className="space-y-2">
              {messages.map((message, messageIndex) => {
                const item = asObject(message);
                return (
                  <div key={messageIndex} className="min-w-0 rounded-md border border-border bg-muted/40 p-3 dark:border-border dark:bg-secondary/60">
                    <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">
                      {String(item.role ?? "message")}
                    </div>
                    <CodeBlock value={String(item.content ?? "")} />
                  </div>
                );
              })}
            </div>
          </details>
        );
      })}
    </div>
  );
}

function ToolsPanel({ steps }: { steps: unknown[] }) {
  return (
    <div className="min-w-0 space-y-3">
      {steps.map((step, index) => {
        const item = asObject(step);
        const status = String(item.status ?? "UNKNOWN");
        return (
          <details key={index} className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
            <summary className="mb-3 flex cursor-pointer flex-wrap items-start justify-between gap-2">
              <div className="min-w-0 flex-1">
                <div className="break-words text-sm font-semibold text-foreground [overflow-wrap:anywhere]">{String(item.name ?? `Step ${index + 1}`)}</div>
                <div className="mt-1 break-words text-[11px] text-muted-foreground [overflow-wrap:anywhere]">
                  {String(item.tool ?? "tool.unknown")}
                  {item.started_at ? ` · ${formatDateTime(String(item.started_at))}` : ""}
                </div>
              </div>
              <div className="flex shrink-0 flex-wrap items-center gap-2">
                {item.duration_ms ? (
                  <span className="rounded-md bg-muted px-2 py-1 text-[11px] text-muted-foreground dark:bg-secondary dark:text-muted-foreground">{String(item.duration_ms)} ms</span>
                ) : null}
                <span
                  className={cn(
                    "rounded-md px-2 py-1 text-[11px] font-semibold",
                    status === "SUCCESS"
                      ? "bg-muted/40 text-muted-foreground"
                      : status === "FAILED"
                        ? "bg-destructive/10 text-destructive"
                        : "bg-muted text-foreground dark:bg-secondary dark:text-muted-foreground",
                  )}
                >
                  {status}
                </span>
              </div>
            </summary>
            <div className="grid min-w-0 gap-3 md:grid-cols-2">
              <PanelBlock title="Input" value={item.input ?? {}} />
              <PanelBlock title="Output" value={item.output ?? {}} />
            </div>
          </details>
        );
      })}
    </div>
  );
}

function ExecutionPanel({ execution }: { execution: Record<string, unknown> }) {
  const entries = Object.entries(execution);
  return (
    <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
      <div className="mb-3 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">Execution Summary</div>
      <div className="grid min-w-0 gap-2 md:grid-cols-2">
        {entries.map(([key, value]) => (
          <div key={key} className="min-w-0 rounded-md border border-border bg-muted/40 px-3 py-2 dark:border-border dark:bg-secondary/60">
            <div className="break-words text-xs text-muted-foreground [overflow-wrap:anywhere]">{key}</div>
            <div className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-foreground [overflow-wrap:anywhere]">{displayValue(value)}</div>
          </div>
        ))}
      </div>
    </section>
  );
}

function ResultPanel({ result, message }: { result: unknown; message?: Props["message"] }) {
  const object = asObject(result);
  const response = callerResponseJSON(object, message);
  const answer = typeof response.answer === "string" ? response.answer : message?.answer ?? undefined;

  return (
    <div className="min-w-0 space-y-3">
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">响应摘要</div>
        <div className="space-y-2">
          {message?.createdAt ? <SummaryLine label="时间" value={formatDateTime(message.createdAt)} /> : null}
          {message?.statusText ? <SummaryLine label="状态" value={message.statusText} /> : null}
          {answer ? <SummaryLine label="答案" value={answer} multiline /> : null}
        </div>
      </section>
      {answer ? (
        <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
          <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">Response JSON</div>
          <CodeBlock value={response} />
        </section>
      ) : null}
      <section className="min-w-0 rounded-lg border border-border bg-card p-3 dark:border-border dark:bg-card">
        <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">Raw JSON</div>
        <CodeBlock value={result} />
      </section>
    </div>
  );
}

function callerResponseJSON(object: Record<string, unknown>, message?: Props["message"]) {
  const source = Object.keys(asObject(object.response)).length > 0 ? asObject(object.response) : object;
  if ("answer" in source || "received_at" in source || "answered_at" in source) {
    return {
      answer: typeof source.answer === "string" ? source.answer : message?.answer ?? "",
      answer_mode: typeof source.answer_mode === "string" ? source.answer_mode : null,
      review_required: typeof source.review_required === "boolean" ? source.review_required : null,
      source_count: typeof source.source_count === "number" ? source.source_count : null,
      user_intent: "user_intent" in source ? source.user_intent ?? null : null,
      received_at: typeof source.received_at === "string" ? source.received_at : null,
      answered_at: typeof source.answered_at === "string" ? source.answered_at : null,
    };
  }
  return source;
}

function userIntentLabel(type: string) {
  switch (type) {
    case "wecom":
      return "企业微信 (wecom)";
    case "refund":
      return "退款 (refund)";
    case "switch_ip":
      return "切换IP (switch_ip)";
    case "discount":
      return "申请优惠 (discount)";
    default:
      return "无";
  }
}

function productTypeLabel(product: string) {
  switch (product) {
    case "static_ip":
      return "静态IP (static_ip)";
    case "dynamic_ip":
      return "动态IP (dynamic_ip)";
    case "overseas_ip":
      return "海外IP (overseas_ip)";
    case "residential_ip":
      return "住宅IP (residential_ip)";
    case "datacenter_ip":
      return "数据中心IP (datacenter_ip)";
    case "unlimited_ip":
      return "无限IP (unlimited_ip)";
    case "mobile_proxy":
      return "手机代理 (mobile_proxy)";
    case "":
      return "-";
    default:
      return product;
  }
}

function stageLabel(stage: string) {
  switch (stage) {
    case "goal_consulting":
      return "目标咨询 (goal_consulting)";
    case "product_selection":
      return "产品选型 (product_selection)";
    case "operation_howto":
      return "操作方法 (operation_howto)";
    case "troubleshooting":
      return "故障排查 (troubleshooting)";
    case "pricing":
      return "价格报价 (pricing)";
    case "purchase":
      return "购买开通 (purchase)";
    case "after_sales":
      return "售后财务 (after_sales)";
    case "safety_boundary":
      return "安全边界 (safety_boundary)";
    case "reception":
      return "接待 (reception)";
    case "":
      return "-";
    default:
      return stage;
  }
}

function strategyLabel(strategy: string) {
  switch (strategy) {
    case "answer_with_evidence":
      return "查资料直接答";
    case "recommend_with_boundary":
      return "推荐并说明边界";
    case "ask_clarification":
      return "先追问";
    case "troubleshoot_steps":
      return "排查步骤";
    case "quote_or_price":
      return "报价";
    case "purchase_guidance":
      return "购买指引";
    case "refuse_with_boundary":
      return "边界拒答";
    case "smalltalk":
      return "接待寒暄";
    case "":
      return "-";
    default:
      return strategy;
  }
}

function boundaryLabel(boundary: string) {
  switch (boundary) {
    case "none":
      return "无";
    case "platform_result_not_guaranteed":
      return "平台结果不承诺";
    case "safety_refusal":
      return "安全拒答";
    case "overseas_access_boundary":
      return "海外访问边界";
    case "internal_security_boundary":
      return "内部安全边界";
    case "pricing_review":
      return "价格需按资料";
    case "after_sales_review":
      return "售后需谨慎";
    case "":
      return "-";
    default:
      return boundary;
  }
}

function SummaryLine({
  label,
  value,
  multiline,
}: {
  label: string;
  value: string;
  multiline?: boolean;
}) {
  return (
    <div className="rounded-md border border-border bg-muted/40 px-3 py-2 dark:border-border dark:bg-secondary/60">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={cn("mt-1 text-sm leading-6 text-foreground dark:text-foreground", multiline && "whitespace-pre-wrap break-words [overflow-wrap:anywhere]")}>
        {value}
      </div>
    </div>
  );
}

function PanelBlock({ title, value }: { title: string; value: unknown }) {
  return (
    <div className="min-w-0 rounded-md border border-border bg-muted/40 p-3 dark:border-border dark:bg-secondary/60">
      <div className="mb-2 break-words text-xs font-medium text-muted-foreground [overflow-wrap:anywhere]">{title}</div>
      <CodeBlock value={value} />
    </div>
  );
}

function CodeBlock({ value }: { value: unknown }) {
  const formatted = formatReadableValue(value);
  const codeScroll = useScrollFollow<HTMLPreElement>([formatted]);

  return (
    <div className="relative min-w-0">
      <pre
        ref={codeScroll.viewportRef}
        className="detail-code-scroll max-h-[28rem] max-w-full overflow-x-hidden overflow-y-auto whitespace-pre-wrap break-words rounded-md bg-zinc-950 px-4 py-3 text-xs leading-6 text-zinc-100 shadow-inner [overflow-wrap:anywhere]"
      >
        <code className="block min-w-0 max-w-full whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{formatted}</code>
      </pre>
      <ScrollJumpControls
        show={codeScroll.showControls}
        onTop={() => codeScroll.scrollToTop()}
        onBottom={() => codeScroll.scrollToBottom()}
        className="bottom-3 right-3"
      />
    </div>
  );
}

function displayValue(value: unknown) {
  return formatReadableValue(value);
}

function formatReadableValue(value: unknown) {
  if (typeof value !== "string") {
    return formatJSON(value);
  }
  const trimmed = value.trim();
  if ((trimmed.startsWith("{") && trimmed.endsWith("}")) || (trimmed.startsWith("[") && trimmed.endsWith("]"))) {
    try {
      return formatJSON(JSON.parse(trimmed));
    } catch {
      return value;
    }
  }
  return value;
}

function customerTraceFromDetails(object: Record<string, unknown>) {
  const nested = asObject(object.audit_trace);
  if (isCustomerChatTrace(nested)) {
    return nested;
  }
  if (isCustomerChatTrace(object)) {
    return object;
  }
  return null;
}

function isCustomerChatTrace(object: Record<string, unknown>) {
  return object.schema_version === "customer_chat_audit.v1" || object.record_type === "customer_chat_trace";
}

function textValue(value: unknown) {
  if (value == null) {
    return "";
  }
  if (typeof value === "string") {
    return value.trim();
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return formatReadableValue(value);
}

function booleanText(value: unknown) {
  if (value === true) {
    return "true";
  }
  if (value === false) {
    return "false";
  }
  return "-";
}

function simulationText(value: unknown) {
  if (value === true) {
    return "simulation";
  }
  if (value === false) {
    return "正式";
  }
  return "-";
}

function durationText(value: unknown) {
  const ms = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(ms) || ms <= 0) {
    return "-";
  }
  if (ms >= 1000) {
    const seconds = ms / 1000;
    return `${seconds.toFixed(seconds >= 10 ? 1 : 2)}s (${Math.round(ms)}ms)`;
  }
  return `${Math.round(ms)}ms`;
}

function dateText(value: unknown) {
  const text = textValue(value);
  return text ? formatDateTime(text) : "-";
}

function modelLabel(model: Record<string, unknown>) {
  const id = textValue(model.id) || "-";
  const name = textValue(model.name);
  const thinking = model.thinking_enabled === true ? "thinking:on" : "thinking:off";
  return name ? `${name} (${id}, ${thinking})` : `${id} (${thinking})`;
}

function formatDateTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  const pad = (num: number) => String(num).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
