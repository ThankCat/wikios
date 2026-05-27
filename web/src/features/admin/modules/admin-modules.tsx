"use client";

import {
  Activity,
  BookOpen,
  Bot,
  CheckCircle2,
  ClipboardCheck,
  Code2,
  Database,
  Download,
  FileJson,
  FileText,
  GitBranch,
  History,
  Languages,
  Loader2,
  ListChecks,
  MessageCircle,
  MessageSquareText,
  PanelLeftClose,
  PanelRightClose,
  PlugZap,
  RefreshCw,
  Save,
  Search,
  SendHorizontal,
  Settings,
  ShieldCheck,
  Sparkles,
  Square,
  Wrench,
  Trash2,
  Upload,
  XCircle,
} from "lucide-react";
import dynamic from "next/dynamic";
import { useRouter, useSearchParams } from "next/navigation";
import * as React from "react";
import type { Extension } from "@codemirror/state";
import type { Plugin } from "prettier";
import { css } from "@codemirror/lang-css";
import { html } from "@codemirror/lang-html";
import { javascript } from "@codemirror/lang-javascript";
import { json as jsonLanguage } from "@codemirror/lang-json";
import { markdown as markdownLanguage } from "@codemirror/lang-markdown";
import { yaml } from "@codemirror/lang-yaml";
import { oneDark } from "@codemirror/theme-one-dark";

import { MarkdownContent } from "@/components/chat/markdown-content";
import { AdminChat } from "@/features/admin/admin-chat";
import { ChatDetailDrawer } from "@/components/chat/chat-detail-drawer";
import { MessageCard } from "@/components/chat/message-card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { FileTree, type FileTreeNode } from "@/components/ui/file-tree";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Select } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { toast } from "@/components/ui/sonner";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api, APIError, isAbortError } from "@/lib/api";
import { createId } from "@/lib/id";
import { useScrollFollow } from "@/lib/use-scroll-follow";
import { cn, formatJSON } from "@/lib/utils";
import type {
  AdminDashboardResponse,
  AdminRuntimeEnvironment,
  AdminRuntimeSettings,
  AdminUser,
  LLMModel,
  LLMModelsResponse,
  LLMModelTestResponse,
  PublicChatHistoryItem,
  PublicConversationDetailResponse,
  PublicConversationSummary,
  PublicIntentsStatus,
  PublicStreamEvent,
  ReviewItem,
  SyncCommitResponse,
  SyncStatusResponse,
  WikiFileResponse,
  WikiTreeItem,
} from "@/types/api";
import type { AdminModuleId } from "@/features/admin-shell/navigation";

const CodeMirror = dynamic(() => import("@uiw/react-codemirror"), { ssr: false });

export type BaseModuleProps = {
  user: AdminUser;
  dashboard: AdminDashboardResponse | null;
  onDashboardRefresh: () => void;
  setDetail: (title: string, node: React.ReactNode) => void;
  openModule: (module: AdminModuleId) => void;
};

export function DashboardModule({ dashboard, onDashboardRefresh, openModule }: BaseModuleProps) {
  const activeModel = dashboard?.active_model;
  const hasErrors = Boolean(dashboard?.recent_errors?.length);

  return (
    <ModuleFrame
      title="系统总览"
      description="集中查看模型、知识库、审查队列和日志策略，作为后台的第一屏。"
      action={
        <Button variant="outline" size="sm" onClick={onDashboardRefresh}>
          <RefreshCw className="mr-2 h-4 w-4" />
          刷新
        </Button>
      }
    >
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          icon={Bot}
          label="当前模型"
          value={activeModel?.display_name ?? "未启用"}
          detail={activeModel ? activeModel.model_name : "LLM 请求会进入安全兜底"}
          tone={activeModel ? "success" : "warning"}
          onClick={() => openModule("models")}
        />
        <MetricCard
          icon={ClipboardCheck}
          label="待审问题"
          value={String(dashboard?.review_pending ?? 0)}
          detail="低置信回答审查队列"
          tone={(dashboard?.review_pending ?? 0) > 0 ? "warning" : "success"}
          onClick={() => openModule("review")}
        />
        <MetricCard
          icon={Database}
          label="知识库变更"
          value={String(dashboard?.sync.changed_count ?? 0)}
          detail={dashboard?.sync.branch ? `分支 ${dashboard.sync.branch}` : "Git 状态待刷新"}
          tone={(dashboard?.sync.changed_count ?? 0) > 0 ? "warning" : "neutral"}
          onClick={() => openModule("knowledge")}
        />
        <MetricCard
          icon={History}
          label="Public 日志"
          value={dashboard?.public_answer_log.enabled ? "开启" : "关闭"}
          detail={dashboard?.public_answer_log.redact ? "已脱敏" : "未脱敏"}
          tone={dashboard?.public_answer_log.enabled && dashboard.public_answer_log.redact ? "success" : "warning"}
          onClick={() => openModule("logs")}
        />
      </div>

      <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
        <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldCheck className="h-4 w-4" />
              运行状态
            </CardTitle>
            <CardDescription>后台第一版聚合接口，后续日志和 prompt 模块会继续接入。</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="grid gap-3 md:grid-cols-2">
              <StatusLine label="qmd 索引" value={dashboard?.qmd.index ?? "-"} ok={dashboard?.qmd.ok} />
              <StatusLine label="Wiki 根目录" value={dashboard?.qmd.root ?? "-"} ok={!dashboard?.qmd.error} />
              <StatusLine label="Git Remote" value={dashboard?.sync.remote ?? "-"} ok={!dashboard?.sync.error && dashboard?.sync.remote_ready} />
              <StatusLine label="Git URL" value={dashboard?.sync.remote_url_redacted || "-"} ok={dashboard?.sync.remote_ready} />
              <StatusLine label="凭据" value={dashboard?.sync.auth_configured ? "已配置" : "未配置"} ok={dashboard?.sync.auth_configured} />
              <StatusLine
                label="Push 状态"
                value={dashboard?.sync.can_push ? `可推送 ${dashboard.sync.ahead} 个提交` : "暂无待推送"}
                ok={!dashboard?.sync.can_push}
              />
            </div>
            {dashboard?.qmd.error ? (
              <Alert className="mt-4 rounded-lg" variant="destructive">
                <AlertTitle>qmd 状态异常</AlertTitle>
                <AlertDescription>{dashboard.qmd.error}</AlertDescription>
              </Alert>
            ) : null}
            {dashboard?.sync.error ? (
              <Alert className="mt-4 rounded-lg" variant="destructive">
                <AlertTitle>Git 状态异常</AlertTitle>
                <AlertDescription>{dashboard.sync.error}</AlertDescription>
              </Alert>
            ) : null}
            {!dashboard?.sync.error && dashboard?.sync.needs_setup ? (
              <Alert className="mt-4 rounded-lg border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-950/30 dark:text-amber-100">
                <AlertTitle>同步配置待完善</AlertTitle>
                <AlertDescription>{dashboard.sync.setup_hint || "请在知识库同步页执行检测或修复。"}</AlertDescription>
              </Alert>
            ) : null}
          </CardContent>
        </Card>

        <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
          <CardHeader>
            <CardTitle className="text-base">近期风险</CardTitle>
            <CardDescription>Dashboard 聚合到的最近错误摘要。</CardDescription>
          </CardHeader>
          <CardContent>
            {hasErrors ? (
              <div className="space-y-2">
                {dashboard?.recent_errors.map((item) => (
                  <div key={`${item.scope}-${item.message}`} className="rounded-lg border border-red-100 bg-red-50 p-3 text-sm dark:border-red-500/40 dark:bg-red-950/30">
                    <div className="font-medium text-red-700 dark:text-red-200">{item.scope}</div>
                    <div className="mt-1 text-red-600 dark:text-red-100">{item.message}</div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="rounded-lg border border-emerald-100 bg-emerald-50 p-4 text-sm text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-950/30 dark:text-emerald-100">
                暂未发现聚合错误。
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </ModuleFrame>
  );
}

export function ConversationsModule(_props: BaseModuleProps) {
  const [items, setItems] = React.useState<PublicConversationSummary[]>([]);
  const [activeId, setActiveId] = React.useState("");
  const [detail, setDetail] = React.useState<PublicConversationDetailResponse | null>(null);
  const [query, setQuery] = React.useState("");
  const [loading, setLoading] = React.useState(false);
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const [total, setTotal] = React.useState(0);
  const [logEnabled, setLogEnabled] = React.useState(true);
  const [activeTab, setActiveTab] = React.useState<"records" | "chat-test">("records");
  const requestSeqRef = React.useRef(0);
  const activeIdRef = React.useRef("");
  const queryRef = React.useRef("");

  React.useEffect(() => {
    activeIdRef.current = activeId;
  }, [activeId]);

  React.useEffect(() => {
    queryRef.current = query;
  }, [query]);

  const load = React.useCallback(async (nextQuery?: string) => {
    const requestSeq = ++requestSeqRef.current;
    const resolvedQuery = nextQuery ?? queryRef.current;
    const currentActiveId = activeIdRef.current;
    setLoading(true);
    setError("");
    try {
      const response = await api.publicConversations({ q: resolvedQuery.trim(), page_size: 50 });
      if (requestSeq !== requestSeqRef.current) {
        return;
      }
      const conversations = Array.isArray(response.conversations) ? response.conversations : [];
      setItems(conversations);
      setTotal(Number.isFinite(response.total) ? response.total : conversations.length);
      setLogEnabled(response.log?.enabled ?? true);
      const nextActive = conversations.find((item) => item.id === currentActiveId)?.id ?? conversations[0]?.id ?? "";
      setActiveId(nextActive);
    } catch (err) {
      if (requestSeq !== requestSeqRef.current) {
        return;
      }
      setError(errorMessage(err));
    } finally {
      if (requestSeq === requestSeqRef.current) {
        setLoading(false);
      }
    }
  }, []);

  React.useEffect(() => {
    void load("");
    return () => {
      requestSeqRef.current += 1;
    };
  }, [load]);

  const detailMessages = Array.isArray(detail?.messages) ? detail.messages : [];

  React.useEffect(() => {
    if (!activeId) {
      setDetail(null);
      return;
    }
    let cancelled = false;
    setDetailLoading(true);
    setDetail(null);
    api.publicConversationDetail(activeId)
      .then((response) => {
        if (!cancelled) {
          setDetail(response);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setDetail(null);
          setError(errorMessage(err));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setDetailLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [activeId]);

  return (
    <ModuleFrame
      title="用户会话"
      description="按 public session 聚合终端用户问答记录，用于运营查看和问题追踪。"
      action={
        <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
          <RefreshCw className={cn("mr-2 h-4 w-4", loading && "animate-spin")} />
          刷新
        </Button>
      }
    >
      <Tabs className="space-y-4">
        <TabsList className="grid w-full max-w-md grid-cols-2">
          <TabsTrigger active={activeTab === "records"} onClick={() => setActiveTab("records")}>
            会话记录
          </TabsTrigger>
          <TabsTrigger active={activeTab === "chat-test"} onClick={() => setActiveTab("chat-test")}>
            聊天接口测试
          </TabsTrigger>
        </TabsList>

        {activeTab === "records" ? (
        <TabsContent className="mt-0 space-y-4">
          {error ? (
            <Alert variant="destructive" className="rounded-lg">
              <AlertTitle>用户会话读取失败</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}
          {!logEnabled ? (
            <Alert className="rounded-lg border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-950/30 dark:text-amber-100">
              <AlertTitle>Public 日志未开启</AlertTitle>
              <AlertDescription>当前不会产生新的用户会话记录，请在配置中开启 public answer log。</AlertDescription>
            </Alert>
          ) : null}
          <div className="grid min-h-[680px] gap-4 xl:grid-cols-[360px_minmax(0,1fr)]">
            <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
              <CardHeader className="space-y-3">
                <div>
                  <CardTitle className="flex items-center gap-2 text-base">
                    <MessageSquareText className="h-4 w-4" />
                    会话列表
                  </CardTitle>
                  <CardDescription>共 {total} 个会话，按最近更新时间排序。</CardDescription>
                </div>
                <div className="flex gap-2">
                  <Input
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        void load(query);
                      }
                    }}
                    placeholder="搜索问题、回答、session"
                  />
                  <Button variant="outline" onClick={() => void load(query)}>
                    <Search className="h-4 w-4" />
                  </Button>
                </div>
              </CardHeader>
              <CardContent>
                <div className="max-h-[560px] space-y-2 overflow-y-auto pr-1">
                  {items.map((item) => (
                    <button
                      key={item.id}
                      type="button"
                      className={cn(
                        "w-full rounded-lg border p-3 text-left transition hover:border-slate-300 hover:bg-slate-50 dark:hover:border-border dark:hover:bg-secondary/50",
                        activeId === item.id && "border-slate-950 bg-slate-50 dark:border-primary dark:bg-secondary",
                      )}
                      onClick={() => setActiveId(item.id)}
                    >
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0 truncate text-sm font-medium">{item.title || item.session_id}</div>
                        <Badge>{item.turn_count} 轮</Badge>
                      </div>
                      <div className="mt-2 line-clamp-2 text-xs leading-5 text-muted-foreground">{item.last_question || "无问题文本"}</div>
                      <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                        <span className="min-w-0 truncate">{item.session_id}</span>
                        <span className="shrink-0">{formatDate(item.updated_at)}</span>
                      </div>
                    </button>
                  ))}
                  {!items.length && !loading ? (
                    <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
                      {query.trim() ? "没有匹配的用户会话。" : "还没有 public 用户会话记录。"}
                    </div>
                  ) : null}
                </div>
              </CardContent>
            </Card>

            <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
              <CardHeader>
                <CardTitle className="text-base">会话详情</CardTitle>
                <CardDescription>
                  {detail?.conversation ? `${detail.conversation.session_id} · ${detail.conversation.turn_count} 轮` : "选择左侧会话查看消息流水。"}
                </CardDescription>
              </CardHeader>
              <CardContent>
                {detailLoading ? <div className="py-12 text-center text-sm text-muted-foreground">正在读取会话...</div> : null}
                {!detailLoading && detail ? (
                  <div className="space-y-4">
                    <div className="grid gap-2 md:grid-cols-3">
                      <StatusLine label="Session" value={detail.conversation.session_id} ok />
                      <StatusLine label="User" value={detail.conversation.user_id || "-"} ok={Boolean(detail.conversation.user_id)} />
                      <StatusLine label="更新" value={formatDate(detail.conversation.updated_at)} ok />
                    </div>
                    <div className="max-h-[520px] space-y-3 overflow-y-auto pr-1">
                      {detailMessages.map((message, index) => (
                        <div key={`${message.id || message.message_id || "message"}-${message.role}-${index}`} className="space-y-2">
                          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                            <div className="flex items-center gap-2">
                              <Badge variant={message.role === "user" ? "default" : "success"}>{message.role === "user" ? "用户" : "助手"}</Badge>
                              {message.answer_mode ? <Badge variant="warning">{message.answer_mode}</Badge> : null}
                            </div>
                            <span>{formatDate(message.created_at)}</span>
                          </div>
                          <MessageCard
                            id={message.id}
                            role={message.role}
                            content={message.content}
                            createdAt={message.created_at}
                            details={message.role === "assistant" ? message.details : undefined}
                            detailMode="after"
                          />
                          {message.trace_id ? <div className="px-1 font-mono text-[11px] text-muted-foreground">trace: {message.trace_id}</div> : null}
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
                {!detailLoading && !detail ? <div className="py-12 text-center text-sm text-muted-foreground">暂无会话详情。</div> : null}
              </CardContent>
            </Card>
          </div>
        </TabsContent>
        ) : null}

        {activeTab === "chat-test" ? (
        <TabsContent className="mt-0">
          <ChatInterfaceTestPanel />
        </TabsContent>
        ) : null}
      </Tabs>
    </ModuleFrame>
  );
}

type SimulationMessageStatus = "pending" | "streaming" | "done" | "error" | "cancelled";

type ConversationSimulationMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  created_at?: string;
  status?: SimulationMessageStatus;
  details?: unknown;
};

type ConversationSimulationSession = {
  draft: string;
  messages: ConversationSimulationMessage[];
};

type ConversationSimulationStore = Record<string, ConversationSimulationSession>;

const conversationSimulationStorageKey = "wikios.admin.public-chat-interface-test.v1";
const chatInterfaceTestSessionId = "external-chat-interface";

function ChatInterfaceTestPanel() {
  const [store, setStore] = React.useState<ConversationSimulationStore>({});
  const [hydrated, setHydrated] = React.useState(false);
  const [error, setError] = React.useState("");
  const [busySessionId, setBusySessionId] = React.useState("");
  const [selectedDetailId, setSelectedDetailId] = React.useState("");
  const activeRequestRef = React.useRef<{ sessionId: string; controller: AbortController } | null>(null);
  const sessionId = chatInterfaceTestSessionId;
  const scroll = useScrollFollow<HTMLDivElement>([sessionId, store[sessionId]?.messages]);
  const activeSession = store[sessionId] ?? emptySimulationSession();
  const busy = Boolean(sessionId && busySessionId === sessionId);
  const canSend = Boolean(activeSession.draft.trim() && !busySessionId);
  const selectedDetail = React.useMemo(
    () => activeSession.messages.find((message) => message.id === selectedDetailId && message.details) ?? null,
    [activeSession.messages, selectedDetailId],
  );

  React.useEffect(() => {
    try {
      setStore(normalizeConversationSimulationStore(JSON.parse(localStorage.getItem(conversationSimulationStorageKey) || "{}")));
    } catch {
      setStore({});
    } finally {
      setHydrated(true);
    }
  }, []);

  React.useEffect(() => {
    if (!hydrated) {
      return;
    }
    const timer = window.setTimeout(() => {
      localStorage.setItem(conversationSimulationStorageKey, JSON.stringify(store));
    }, 120);
    return () => window.clearTimeout(timer);
  }, [hydrated, store]);

  React.useEffect(() => {
    scroll.scrollToBottom("auto");
  }, [sessionId, scroll.scrollToBottom]);

  React.useEffect(
    () => () => {
      activeRequestRef.current?.controller.abort();
      activeRequestRef.current = null;
    },
    [],
  );

  function patchSession(id: string, updater: (current: ConversationSimulationSession) => ConversationSimulationSession) {
    if (!id) {
      return;
    }
    setStore((current) => ({ ...current, [id]: updater(current[id] ?? emptySimulationSession()) }));
  }

  function setDraft(value: string) {
    patchSession(sessionId, (current) => ({ ...current, draft: value }));
  }

  function appendMessage(id: string, message: ConversationSimulationMessage) {
    patchSession(id, (current) => ({ ...current, messages: [...current.messages, message] }));
  }

  function patchMessage(
    id: string,
    messageId: string,
    updates: {
      content?: string | ((prev: string) => string);
      created_at?: string;
      status?: SimulationMessageStatus;
      details?: unknown | ((prev: unknown) => unknown);
    },
  ) {
    patchSession(id, (current) => ({
      ...current,
      messages: current.messages.map((message) => {
        if (message.id !== messageId) {
          return message;
        }
        return {
          ...message,
          content: typeof updates.content === "function" ? updates.content(message.content) : updates.content ?? message.content,
          created_at: updates.created_at?.trim() ? updates.created_at : message.created_at,
          status: updates.status ?? message.status,
          details: "details" in updates ? resolveSimulationDetailUpdate(updates.details, message.details) : message.details,
        };
      }),
    }));
  }

  function appendEventDetail(id: string, messageId: string, key: string, value: unknown, limit: number) {
    patchMessage(id, messageId, {
      details: (previous: unknown) => {
        const object = simulationRecord(previous);
        const current = Array.isArray(object[key]) ? object[key] : [];
        return { ...object, [key]: [...current, value].slice(-limit) };
      },
    });
  }

  function appendDetailText(id: string, messageId: string, key: string, value: string, limit: number) {
    if (!value) {
      return;
    }
    patchMessage(id, messageId, {
      details: (previous: unknown) => {
        const object = simulationRecord(previous);
        const next = `${String(object[key] ?? "")}${value}`;
        return { ...object, [key]: next.length > limit ? next.slice(-limit) : next };
      },
    });
  }

  function handleStreamEvent(id: string, messageId: string, event: PublicStreamEvent) {
    if (event.type === "meta") {
      const data = simulationRecord(event.data);
      patchMessage(id, messageId, {
        details: (previous: unknown) => ({
          ...simulationRecord(previous),
          execution: {
            ...simulationRecord(simulationRecord(previous).execution),
            kind: "public-answer-audit",
            status: "RUNNING",
            started_at: data.received_at,
          },
        }),
      });
      return;
    }
    if (event.type === "prompt") {
      appendEventDetail(id, messageId, "prompts", summarizeSimulationPromptEvent(event.data), 8);
      return;
    }
    if (event.type === "delta") {
      const data = simulationRecord(event.data);
      patchMessage(id, messageId, {
        content: (previous) => `${previous}${String(data.delta ?? "")}`,
        status: "streaming",
      });
      scroll.scrollToBottom("smooth");
      return;
    }
    if (event.type === "step_start" || event.type === "step_finish") {
      appendEventDetail(id, messageId, "steps", summarizeSimulationStepEvent(event.data), 40);
      return;
    }
    if (event.type === "llm_delta") {
      const data = simulationRecord(event.data);
      appendDetailText(id, messageId, "llm_stream", String(data.delta ?? ""), 20000);
      patchMessage(id, messageId, {
        details: (previous: unknown) => ({
          ...simulationRecord(previous),
          llm_stream_preview: truncateSimulationText(String(data.delta ?? ""), 500),
        }),
      });
      return;
    }
    if (event.type === "llm_reasoning_delta") {
      const data = simulationRecord(event.data);
      appendDetailText(id, messageId, "reasoning", String(data.delta ?? ""), 12000);
      appendEventDetail(
        id,
        messageId,
        "reasoning_events",
        {
          name: data.name,
          delta: data.delta,
          created_at: data.created_at,
        },
        80,
      );
      return;
    }
    if (event.type === "llm_done") {
      const data = simulationRecord(event.data);
      const reasoning = String(data.reasoning ?? "");
      patchMessage(id, messageId, {
        details: (previous: unknown) => ({
          ...simulationRecord(previous),
          llm_done: data,
          ...(reasoning.trim() ? { reasoning } : {}),
        }),
      });
      return;
    }
    if (event.type === "result") {
      const data = simulationRecord(event.data);
      patchMessage(id, messageId, {
        content: String(data.answer ?? ""),
        created_at: String(data.answered_at ?? ""),
        status: "done",
        details: (previous: unknown) => ({
          ...mergeSimulationDetails(previous, data.details),
          response: data,
        }),
      });
      scroll.scrollToBottom("smooth");
      return;
    }
    if (event.type === "error") {
      const data = simulationRecord(event.data);
      const message = String(data.message ?? "接口测试失败");
      setError(message);
      patchMessage(id, messageId, {
        content: message,
        status: "error",
        details: (previous: unknown) => ({ ...simulationRecord(previous), error: data }),
      });
      return;
    }
    if (event.type === "done") {
      patchMessage(id, messageId, { status: "done" });
    }
  }

  async function sendSimulation() {
    const id = sessionId;
    const question = activeSession.draft.trim();
    if (!id || !question || busySessionId) {
      return;
    }
    setError("");
    patchSession(id, (current) => ({ ...current, draft: "" }));
    const questionCreatedAt = new Date().toISOString();
    const userMessage: ConversationSimulationMessage = {
      id: createId(),
      role: "user",
      content: question,
      created_at: questionCreatedAt,
    };
    const assistantId = createId();
    const history = simulationMessagesToPublicHistory(activeSession.messages);
    appendMessage(id, userMessage);
    appendMessage(id, {
      id: assistantId,
      role: "assistant",
      content: "",
      created_at: new Date().toISOString(),
      status: "streaming",
    });
    window.requestAnimationFrame(() => scroll.scrollToBottom("auto"));

    const controller = new AbortController();
    activeRequestRef.current = { sessionId: id, controller };
    setBusySessionId(id);
    try {
      await api.publicAnswerAuditStream(
        question,
        history,
        {
          session_id: `test-${id}`,
          question_message_id: userMessage.id,
          answer_message_id: assistantId,
          question_created_at: questionCreatedAt,
        },
        (event) => handleStreamEvent(id, assistantId, event),
        controller.signal,
      );
    } catch (reason) {
      if (isAbortError(reason)) {
        patchMessage(id, assistantId, {
          content: (previous) => previous || "已停止生成。",
          status: "cancelled",
        });
      } else {
        const message = reason instanceof Error ? reason.message : "接口测试失败";
        setError(message);
        patchMessage(id, assistantId, { content: message, status: "error" });
      }
    } finally {
      if (activeRequestRef.current?.controller === controller) {
        activeRequestRef.current = null;
      }
      setBusySessionId((current) => (current === id ? "" : current));
    }
  }

  function stopSimulation() {
    if (!busy || activeRequestRef.current?.sessionId !== sessionId) {
      return;
    }
    activeRequestRef.current.controller.abort();
  }

  function clearSimulation() {
    if (busy) {
      return;
    }
    patchSession(sessionId, () => emptySimulationSession());
    setSelectedDetailId("");
    setError("");
  }

  return (
    <Card className="relative overflow-hidden rounded-lg border bg-white shadow-sm dark:bg-card xl:col-span-2 2xl:col-span-1">
      <CardHeader className="space-y-3 pb-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2 text-base">
              <Bot className="h-4 w-4" />
              外部聊天接口审查
            </CardTitle>
            <CardDescription className="mt-1">用于审查 Public 对外聊天接口，使用独立本地上下文。</CardDescription>
          </div>
          <Button type="button" variant="ghost" size="sm" onClick={clearSimulation} disabled={!sessionId || busy || activeSession.messages.length === 0}>
            清空上下文
          </Button>
        </div>
        <div className="rounded-lg bg-slate-50 px-3 py-2 text-xs leading-5 text-muted-foreground dark:bg-secondary/50">
          审查消息只保存在本机浏览器，不写入用户会话日志，也不会改变会话记录。
        </div>
      </CardHeader>
      <CardContent className="flex h-[640px] min-h-0 flex-col gap-3">
        {error ? (
          <Alert variant="destructive" className="rounded-lg">
            <AlertTitle>接口测试失败</AlertTitle>
            <AlertDescription className="break-words">{error}</AlertDescription>
          </Alert>
        ) : null}
        <div className="relative min-h-0 flex-1 rounded-lg bg-slate-50 dark:bg-background/60">
          <ScrollArea viewportRef={scroll.viewportRef} className="h-full px-3 py-4">
            <div className="mx-auto flex max-w-2xl flex-col gap-3 pb-2">
              {!activeSession.messages.length ? (
                <div className="rounded-lg border border-dashed bg-white p-5 text-center text-sm leading-6 text-muted-foreground dark:bg-card">
                  输入客户问题，审查外部聊天接口返回；这里的上下文独立于真实用户会话。
                </div>
              ) : null}
              {activeSession.messages.map((message) => (
                <MessageCard
                  key={message.id}
                  id={message.id}
                  role={message.role}
                  content={message.content}
                  createdAt={message.created_at}
                  pending={message.status === "pending" || message.status === "streaming"}
                  statusText={simulationMessageStatusText(message)}
                  details={message.details}
                  selected={selectedDetailId === message.id}
                  onInspect={message.details ? ({ id }) => setSelectedDetailId(id) : undefined}
                />
              ))}
            </div>
          </ScrollArea>
        </div>
        <div className="rounded-2xl border bg-white p-2 shadow-sm dark:bg-background dark:shadow-none">
          <Textarea
            value={activeSession.draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                if (busy) {
                  stopSimulation();
                  return;
                }
                void sendSimulation();
              }
            }}
            disabled={busySessionId !== "" && !busy}
            className="min-h-[68px] resize-none border-0 bg-transparent p-2 text-sm shadow-none focus-visible:ring-0"
            placeholder="输入客户问题，Enter 发送"
          />
          <div className="mt-2 flex items-center justify-between gap-3 px-1">
            <span className="min-w-0 truncate text-xs text-muted-foreground">
              {busy ? "正在调用接口，可随时停止。" : "使用 Public answer 引擎，审查结果不入库。"}
            </span>
            <Button
              type="button"
              onClick={() => {
                if (busy) {
                  stopSimulation();
                  return;
                }
                void sendSimulation();
              }}
              disabled={!busy && !canSend}
              className="h-9 w-9 shrink-0 rounded-full px-0"
              aria-label={busy ? "停止生成" : "发送测试消息"}
              title={busy ? "停止生成" : "发送测试消息"}
            >
              {busy ? <Square className="h-3.5 w-3.5 fill-current" /> : <SendHorizontal className="h-3.5 w-3.5" />}
            </Button>
          </div>
        </div>
        <ChatDetailDrawer
          title="接口测试详情"
          open={Boolean(selectedDetail)}
          selected={
            selectedDetail
              ? {
                  role: selectedDetail.role,
                  content: selectedDetail.content,
                  createdAt: selectedDetail.created_at,
                  details: selectedDetail.details,
                  statusText: selectedDetail.role === "assistant" ? simulationMessageStatusText(selectedDetail) : "",
                }
              : null
          }
          onClear={() => setSelectedDetailId("")}
        />
      </CardContent>
    </Card>
  );
}

function emptySimulationSession(): ConversationSimulationSession {
  return { draft: "", messages: [] };
}

function normalizeConversationSimulationStore(value: unknown): ConversationSimulationStore {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  const result: ConversationSimulationStore = {};
  for (const [sessionId, rawSession] of Object.entries(value as Record<string, unknown>)) {
    if (!sessionId.trim() || !rawSession || typeof rawSession !== "object" || Array.isArray(rawSession)) {
      continue;
    }
    const session = rawSession as Partial<ConversationSimulationSession>;
    result[sessionId] = {
      draft: typeof session.draft === "string" ? session.draft : "",
      messages: normalizeConversationSimulationMessages(session.messages),
    };
  }
  return result;
}

function normalizeConversationSimulationMessages(value: unknown): ConversationSimulationMessage[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.reduce<ConversationSimulationMessage[]>((acc, message) => {
    if (!message || typeof message !== "object" || Array.isArray(message)) {
      return acc;
    }
    const raw = message as Partial<ConversationSimulationMessage>;
    if (raw.role !== "user" && raw.role !== "assistant") {
      return acc;
    }
    acc.push({
      id: typeof raw.id === "string" && raw.id.trim() ? raw.id : createId(),
      role: raw.role,
      content:
        typeof raw.content === "string" && raw.content.trim()
          ? raw.content
          : raw.role === "assistant" && (raw.status === "pending" || raw.status === "streaming")
            ? "已停止生成。"
            : typeof raw.content === "string"
              ? raw.content
              : "",
      created_at: typeof raw.created_at === "string" ? raw.created_at : "",
      status: raw.status === "pending" || raw.status === "streaming" ? "cancelled" : raw.status,
      details: raw.details,
    });
    return acc;
  }, []);
}

function simulationMessagesToPublicHistory(messages: ConversationSimulationMessage[]): PublicChatHistoryItem[] {
  return messages
    .filter((message) => {
      if (!message.content.trim()) {
        return false;
      }
      if (message.role === "assistant" && message.status && message.status !== "done") {
        return false;
      }
      return message.status !== "error" && message.status !== "cancelled";
    })
    .map((message) => ({
      id: message.id,
      role: message.role,
      content: message.content,
      created_at: message.created_at,
    }));
}

function mergeSimulationDetails(left: unknown, right: unknown) {
  const merged = {
    ...simulationRecord(left),
    ...simulationRecord(right),
  };
  return Object.keys(merged).length > 0 ? merged : undefined;
}

function resolveSimulationDetailUpdate(update: unknown, previous: unknown) {
  if (typeof update === "function") {
    return (update as (prev: unknown) => unknown)(previous);
  }
  return update;
}

function simulationRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

function summarizeSimulationPromptEvent(value: unknown) {
  const data = simulationRecord(value);
  const messages = Array.isArray(data.messages) ? data.messages : [];
  return {
    name: data.name,
    model: data.model,
    created_at: data.created_at,
    prompt_chars: data.prompt_chars,
    prompt_estimated_tokens: data.prompt_estimated_tokens,
    timeout_sec: data.timeout_sec,
    messages: messages.map((message) => {
      const item = simulationRecord(message);
      return {
        role: item.role,
        content: truncateSimulationText(String(item.content ?? ""), 1600),
      };
    }),
  };
}

function summarizeSimulationStepEvent(value: unknown) {
  const data = simulationRecord(value);
  return {
    name: data.name,
    tool: data.tool,
    status: data.status,
    input: sanitizeSimulationPayload(data.input),
    output: sanitizeSimulationPayload(data.output),
    duration_ms: data.duration_ms,
    started_at: data.started_at,
    ended_at: data.ended_at,
  };
}

function sanitizeSimulationPayload(value: unknown): unknown {
  if (value == null) {
    return value;
  }
  if (typeof value === "string") {
    return truncateSimulationText(value, 1000);
  }
  if (Array.isArray(value)) {
    return value.slice(0, 16).map((item) => sanitizeSimulationPayload(item));
  }
  if (typeof value === "object") {
    return Object.fromEntries(
      Object.entries(simulationRecord(value))
        .slice(0, 20)
        .map(([key, item]) => [key, sanitizeSimulationPayload(item)]),
    );
  }
  return value;
}

function truncateSimulationText(value: string, limit: number) {
  const text = value.trim();
  if (text.length <= limit) {
    return text;
  }
  return `${text.slice(0, limit)}\n\n[truncated]`;
}

function simulationMessageStatusText(message: ConversationSimulationMessage) {
  if (message.role !== "assistant") {
    return "";
  }
  switch (message.status) {
    case "pending":
      return "正在处理请求...";
    case "streaming":
      return "正在生成回答...";
    case "cancelled":
      return "本次测试已停止。";
    case "error":
      return "本次测试失败。";
    default:
      return "";
  }
}

type ModelFormState = {
  id?: string;
  display_name: string;
  provider: string;
  base_url: string;
  model_name: string;
  api_key: string;
  timeout_sec: number;
  admin_timeout_sec: number;
};

const emptyModelForm: ModelFormState = {
  display_name: "",
  provider: "",
  base_url: "",
  model_name: "",
  api_key: "",
  timeout_sec: 90,
  admin_timeout_sec: 300,
};

function ModelSettingsPanel({ onDashboardRefresh, setDetail }: Pick<BaseModuleProps, "onDashboardRefresh" | "setDetail">) {
  const [models, setModels] = React.useState<LLMModel[]>([]);
  const [form, setForm] = React.useState<ModelFormState>(emptyModelForm);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState("");
  const [testingId, setTestingId] = React.useState("");
  const [testResults, setTestResults] = React.useState<Record<string, LLMModelTestResponse>>({});

  const loadModels = React.useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await api.listLLMModels();
      setModels(response.models);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void loadModels();
  }, [loadModels]);

  async function saveModel() {
    setSaving(true);
    setError("");
    try {
      const payload = {
        display_name: form.display_name,
        provider: form.provider,
        base_url: form.base_url,
        model_name: form.model_name,
        api_key: form.api_key,
        timeout_sec: Number(form.timeout_sec) || 90,
        admin_timeout_sec: Number(form.admin_timeout_sec) || 300,
      };
      if (form.id) {
        await api.updateLLMModel(form.id, payload);
      } else {
        await api.createLLMModel(payload);
      }
      setForm(emptyModelForm);
      await loadModels();
      onDashboardRefresh();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function activateModel(id: string) {
    setError("");
    try {
      await api.activateLLMModel(id);
      await loadModels();
      onDashboardRefresh();
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  async function deleteModel(id: string) {
    if (!window.confirm("确认删除这个模型配置？删除当前模型后会进入无启用模型状态。")) {
      return;
    }
    setError("");
    try {
      await api.deleteLLMModel(id);
      await loadModels();
      onDashboardRefresh();
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  async function testModel(model: LLMModel) {
    setTestingId(model.id);
    setError("");
    try {
      const result = await api.testLLMModel(model.id);
      setTestResults((items) => ({ ...items, [model.id]: result }));
      setDetail("连接测试", <ModelTestDetail model={model} result={result} />);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setTestingId("");
    }
  }

  return (
    <>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-base font-semibold">模型配置</h3>
          <p className="mt-1 text-sm text-muted-foreground">管理 OpenAI-compatible 模型，支持启用、删除和连接测试。</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void loadModels()} disabled={loading}>
          <RefreshCw className={cn("mr-2 h-4 w-4", loading && "animate-spin")} />
          刷新
        </Button>
      </div>
      {error ? (
        <Alert variant="destructive" className="rounded-lg">
          <AlertTitle>模型操作失败</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      <div className="grid gap-3 xl:grid-cols-[380px_minmax(0,1fr)]">
        <Card className="rounded-lg border bg-white shadow-sm">
          <CardHeader>
            <CardTitle className="text-base">{form.id ? "编辑模型" : "新增模型"}</CardTitle>
            <CardDescription>API Key 只提交给后端，编辑时留空会保留原密钥。</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              <Input placeholder="显示名称，如 Qianwen" value={form.display_name} onChange={(event) => setFormField("display_name", event.target.value, setForm)} />
              <Input placeholder="服务商，如 BaiLian / DeepSeek" value={form.provider} onChange={(event) => setFormField("provider", event.target.value, setForm)} />
              <Input placeholder="端点域名，如 https://api.openai.com" value={form.base_url} onChange={(event) => setFormField("base_url", event.target.value, setForm)} />
              <Input placeholder="模型名，如 gpt-4.1-mini" value={form.model_name} onChange={(event) => setFormField("model_name", event.target.value, setForm)} />
              <Input
                placeholder={form.id ? "API Key 留空保持不变" : "API Key"}
                type="password"
                value={form.api_key}
                onChange={(event) => setFormField("api_key", event.target.value, setForm)}
              />
              <div className="grid grid-cols-2 gap-3">
                <Input
                  type="number"
                  min={1}
                  value={form.timeout_sec}
                  onChange={(event) => setFormField("timeout_sec", Number(event.target.value), setForm)}
                />
                <Input
                  type="number"
                  min={1}
                  value={form.admin_timeout_sec}
                  onChange={(event) => setFormField("admin_timeout_sec", Number(event.target.value), setForm)}
                />
              </div>
              <div className="flex gap-2">
                <Button onClick={() => void saveModel()} disabled={saving}>
                  {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
                  保存
                </Button>
                <Button variant="outline" onClick={() => setForm(emptyModelForm)}>
                  清空
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card className="rounded-lg border bg-white shadow-sm">
          <CardHeader>
            <CardTitle className="text-base">已添加模型</CardTitle>
            <CardDescription>备用模型调用成功后，后端会自动把成功模型持久设为当前 active。</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>模型</TableHead>
                    <TableHead>端点</TableHead>
                    <TableHead className="w-20 whitespace-nowrap">状态</TableHead>
                    <TableHead className="w-24 whitespace-nowrap">测试</TableHead>
                    <TableHead className="w-[220px] whitespace-nowrap text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {models.map((model) => {
                    const result = testResults[model.id];
                    return (
                      <TableRow key={model.id}>
                        <TableCell>
                          <div className="font-medium">{model.display_name}</div>
                          <div className="text-xs text-muted-foreground">{model.model_name}</div>
                          <div className="mt-1 text-xs text-muted-foreground">密钥：{model.api_key_mask || "未配置"}</div>
                        </TableCell>
                        <TableCell>
                          <div className="max-w-[260px] truncate text-xs">{model.base_url}</div>
                          <div className="mt-1 text-xs text-muted-foreground">{model.provider || "未标注服务商"}</div>
                        </TableCell>
                        <TableCell className="whitespace-nowrap">
                          {model.is_active ? <Badge variant="success">当前</Badge> : <Badge>备用</Badge>}
                        </TableCell>
                        <TableCell className="whitespace-nowrap">
                          {result ? (
                            <span className={cn("inline-flex items-center gap-1 text-xs", result.ok ? "text-emerald-700" : "text-red-700")}>
                              {result.ok ? <CheckCircle2 className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
                              {result.latency_ms}ms
                            </span>
                          ) : (
                            <span className="text-xs text-muted-foreground">未测试</span>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-nowrap">
                          <div className="flex min-w-[210px] justify-end gap-2">
                            <Button className="w-10 shrink-0 px-0" variant="outline" size="sm" onClick={() => void testModel(model)} disabled={testingId === model.id}>
                              {testingId === model.id ? <Loader2 className="h-4 w-4 animate-spin" /> : <PlugZap className="h-4 w-4" />}
                            </Button>
                            <Button
                              className="w-14 shrink-0"
                              variant="outline"
                              size="sm"
                              onClick={() =>
                                setForm({
                                  id: model.id,
                                  display_name: model.display_name,
                                  provider: model.provider,
                                  base_url: model.base_url,
                                  model_name: model.model_name,
                                  api_key: "",
                                  timeout_sec: model.timeout_sec,
                                  admin_timeout_sec: model.admin_timeout_sec,
                                })
                              }
                            >
                              编辑
                            </Button>
                            <Button className="w-14 shrink-0" variant={model.is_active ? "secondary" : "default"} size="sm" onClick={() => void activateModel(model.id)}>
                              启用
                            </Button>
                            <Button className="w-9 shrink-0 px-0" variant="ghost" size="sm" onClick={() => void deleteModel(model.id)}>
                              <Trash2 className="h-4 w-4" />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                  {!models.length && !loading ? (
                    <TableRow>
                      <TableCell colSpan={5} className="py-8 text-center text-sm text-muted-foreground">
                        还没有模型配置。
                      </TableCell>
                    </TableRow>
                  ) : null}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
      </div>
    </>
  );
}

export function ModelsModule({ onDashboardRefresh, setDetail }: BaseModuleProps) {
  return (
    <ModuleFrame title="模型管理" description="管理 OpenAI-compatible 模型，支持启用、删除和连接测试。">
      <ModelSettingsPanel onDashboardRefresh={onDashboardRefresh} setDetail={setDetail} />
    </ModuleFrame>
  );
}

type KnowledgeView = "browse" | "assistant" | "sync";
type WikiEditorMode = "split" | "edit" | "preview";
type SyncOperationFeedback = {
  kind: "success" | "error";
  title: string;
  message: string;
  code?: string;
  stdout?: string;
  stderr?: string;
  exitCode?: number;
};

const knowledgeStateKey = "wikios.knowledge.workspace";
const knowledgeSyncStateKey = "wikios.knowledge.sync";
const knowledgeViews: Array<{ id: KnowledgeView; label: string; icon: typeof Activity }> = [
  { id: "browse", label: "浏览", icon: BookOpen },
  { id: "assistant", label: "助手", icon: MessageSquareText },
  { id: "sync", label: "同步", icon: GitBranch },
];

type KnowledgeModuleProps = BaseModuleProps & {
  initialPath?: string | null;
  initialView?: string | null;
};

export function KnowledgeModule({ dashboard, user, onDashboardRefresh, initialPath, initialView }: KnowledgeModuleProps) {
  const router = useRouter();
  const hasURLPath = initialPath !== undefined && initialPath !== null;
  const urlView = normalizeKnowledgeView(initialView ?? "");
  const urlPath = hasURLPath ? (initialPath ?? "") : "";
  const [storageRestored, setStorageRestored] = React.useState(false);
  const [view, setView] = React.useState<KnowledgeView>(() => urlView ?? "browse");
  const [treeNodes, setTreeNodes] = React.useState<FileTreeNode[]>([]);
  const [path, setPath] = React.useState(urlPath ?? "");
  const [pathInput, setPathInput] = React.useState(urlPath ?? "");
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const [selected, setSelected] = React.useState<WikiFileResponse | null>(null);
  const [draft, setDraft] = React.useState("");
  const [dirty, setDirty] = React.useState(false);
  const [editorMode, setEditorMode] = React.useState<WikiEditorMode>("split");
  const [savingFile, setSavingFile] = React.useState(false);
  const [formattingFile, setFormattingFile] = React.useState(false);
  const [replacingFile, setReplacingFile] = React.useState(false);
  const [syncStatus, setSyncStatus] = React.useState<SyncStatusResponse | null>(null);
  const [selectedSyncPaths, setSelectedSyncPaths] = React.useState<string[]>([]);
  const [syncMessage, setSyncMessage] = React.useState("");
  const [syncMessageRule, setSyncMessageRule] = React.useState("");
  const [syncResult, setSyncResult] = React.useState<SyncCommitResponse | null>(null);
  const [syncBusy, setSyncBusy] = React.useState(false);
  const [syncMessageBusy, setSyncMessageBusy] = React.useState(false);
  const [syncFeedback, setSyncFeedback] = React.useState<SyncOperationFeedback | null>(null);

  React.useEffect(() => {
    const initialize = async () => {
      const storedKnowledgeState = readJSON<Record<string, unknown>>(knowledgeStateKey, {});
      const storedSyncState = readJSON<Record<string, unknown>>(knowledgeSyncStateKey, {});
      const storedView = normalizeKnowledgeView(typeof storedKnowledgeState.view === "string" ? storedKnowledgeState.view : "");
      const storedPath = stringValue(storedKnowledgeState, "path");
      const storedSelectedPath = hasURLPath ? "" : stringValue(storedKnowledgeState, "selectedPath");
      const storedDraftPath = hasURLPath ? "" : stringValue(storedKnowledgeState, "selectedDraftPath");
      const storedDraft = stringValue(storedKnowledgeState, "draft");
      const storedEditorMode = normalizeWikiEditorMode(stringValue(storedKnowledgeState, "editorMode"));
      const nextPath = hasURLPath ? urlPath : storedPath;
      setView(urlView ?? storedView ?? "browse");
      if (storedEditorMode) {
        setEditorMode(storedEditorMode);
      }
      setPath(nextPath);
      setPathInput(nextPath);
      setSelectedSyncPaths(stringArrayValue(storedSyncState, "selectedSyncPaths"));
      setSyncMessage(stringValue(storedSyncState, "syncMessage"));
      setSyncMessageRule(stringValue(storedSyncState, "syncMessageRule"));
      setSyncResult(syncCommitResultValue(storedSyncState, "syncResult"));
      setSyncFeedback(syncFeedbackValue(storedSyncState, "syncFeedback"));
      if (hasURLPath) {
        await openKnowledgePath(urlPath);
      } else {
        await loadTree(storedPath);
        if (storedSelectedPath) {
          await openWikiFile(storedSelectedPath, { draft: storedSelectedPath === storedDraftPath ? storedDraft : "" });
        }
      }
      setStorageRestored(true);
    };
    void initialize();
  }, []);

  React.useEffect(() => {
    if (!storageRestored) {
      return;
    }
    writeJSON(knowledgeStateKey, {
      view,
      path,
      selectedPath: selected?.path ?? "",
      selectedDraftPath: dirty ? selected?.path ?? "" : "",
      draft: dirty ? draft : "",
      editorMode,
    });
  }, [dirty, draft, editorMode, path, selected?.path, storageRestored, view]);

  React.useEffect(() => {
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!dirty) {
        return;
      }
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", handleBeforeUnload);
    return () => window.removeEventListener("beforeunload", handleBeforeUnload);
  }, [dirty]);

  React.useEffect(() => {
    window.dispatchEvent(new CustomEvent("wikios:knowledge-dirty", { detail: { dirty } }));
    return () => {
      window.dispatchEvent(new CustomEvent("wikios:knowledge-dirty", { detail: { dirty: false } }));
    };
  }, [dirty]);

  React.useEffect(() => {
    if (!storageRestored) {
      return;
    }
    writeJSON(knowledgeSyncStateKey, { selectedSyncPaths, syncMessage, syncMessageRule, syncResult, syncFeedback });
  }, [selectedSyncPaths, storageRestored, syncFeedback, syncMessage, syncMessageRule, syncResult]);

  React.useEffect(() => {
    if (!storageRestored) {
      return;
    }
    const params = new URLSearchParams();
    params.set("view", view);
    if (view === "browse" && (selected?.path || path)) {
      params.set("path", selected?.path ?? path);
    }
    router.replace(`/knowledge?${params.toString()}`, { scroll: false });
  }, [path, router, selected?.path, storageRestored, view]);

  async function loadTree(nextPath = path) {
    if (!confirmDiscardWikiDraft()) {
      return;
    }
    setLoading(true);
    setError("");
    try {
      const response = await api.wikiTree(nextPath);
      setPath(response.path);
      setPathInput(response.path);
      if (!response.path) {
        setTreeNodes(wikiItemsToTreeNodes(response.items));
      } else {
        setTreeNodes((current) => upsertDirectoryChildren(current, response.path, wikiItemsToTreeNodes(response.items), true));
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  async function openKnowledgePath(nextPath: string) {
    if (!confirmDiscardWikiDraft()) {
      return;
    }
    setLoading(true);
    setError("");
    try {
      await expandTreePath(nextPath);
      setSelected(null);
      setLoading(false);
      return;
    } catch {}
    try {
      const response = await api.wikiFile(nextPath);
      const parent = parentWikiPath(response.path);
      setSelected(response);
      resetWikiDraft(response);
      setPath(parent);
      setPathInput(parent);
      try {
        await expandTreePath(parent);
      } catch {}
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  async function openWikiFile(filePath: string, options?: { draft?: string }) {
    setLoading(true);
    setError("");
    try {
      const response = await api.wikiFile(filePath);
      setSelected(response);
      resetWikiDraft(response, options?.draft);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  async function expandTreePath(targetPath: string) {
    const segments = targetPath.split("/").filter(Boolean);
    let currentPath = "";
    const root = await api.wikiTree("");
    setTreeNodes(wikiItemsToTreeNodes(root.items));
    setPath(root.path);
    setPathInput(root.path);
    for (const segment of segments) {
      currentPath = currentPath ? `${currentPath}/${segment}` : segment;
      const response = await api.wikiTree(currentPath);
      setTreeNodes((current) => upsertDirectoryChildren(current, response.path, wikiItemsToTreeNodes(response.items), true));
      setPath(response.path);
      setPathInput(response.path);
    }
  }

  async function openWikiNode(node: FileTreeNode) {
    if (!confirmDiscardWikiDraft()) {
      return;
    }
    if (node.isDirectory) {
      setSelected(null);
      resetWikiDraft(null);
      await toggleTreeDirectory(node);
      return;
    }
    await openWikiFile(node.path);
  }

  async function toggleTreeDirectory(node: FileTreeNode) {
    if (!node.isDirectory) {
      return;
    }
    if (node.expanded && node.children) {
      setTreeNodes((current) => setDirectoryExpanded(current, node.path, false));
      setPath(node.path);
      setPathInput(node.path);
      return;
    }
    setPath(node.path);
    setPathInput(node.path);
    setTreeNodes((current) => setDirectoryLoading(current, node.path, true));
    setLoading(true);
    setError("");
    try {
      const response = await api.wikiTree(node.path);
      setPath(response.path);
      setPathInput(response.path);
      setTreeNodes((current) => upsertDirectoryChildren(current, response.path, wikiItemsToTreeNodes(response.items), true));
    } catch (err) {
      setTreeNodes((current) => setDirectoryLoading(current, node.path, false));
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  function switchView(nextView: KnowledgeView) {
    if (nextView !== view && !confirmDiscardWikiDraft()) {
      return;
    }
    setView(nextView);
    if (nextView === "sync" && !syncStatus && !syncBusy) {
      void refreshSyncStatus(true);
    }
  }

  function upPath() {
    if (!confirmDiscardWikiDraft()) {
      return;
    }
    const parent = path.split("/").filter(Boolean).slice(0, -1).join("/");
    setSelected(null);
    resetWikiDraft(null);
    if (parent) {
      void expandTreePath(parent);
      return;
    }
    void loadTree("");
  }

  async function refreshSyncStatus(resetSelection = false) {
    setSyncBusy(true);
    setSyncFeedback(null);
    try {
      const response = await api.syncStatus();
      setSyncStatus(response);
      setSelectedSyncPaths((current) => {
        const valid = current.filter((item) => response.files.some((file) => file.path === item));
        if (resetSelection || valid.length === 0) {
          return response.files.filter((file) => file.default_on).map((file) => file.path);
        }
        return valid;
      });
      setSyncMessage((current) => current.trim() || defaultSyncMessage(response));
    } catch (err) {
      setSyncFeedback(syncErrorFeedback(err, "读取同步状态失败"));
    } finally {
      setSyncBusy(false);
    }
  }

  function toggleSyncPath(filePath: string) {
    setSelectedSyncPaths((current) =>
      current.includes(filePath) ? current.filter((item) => item !== filePath) : [...current, filePath],
    );
  }

  async function generateSyncMessage() {
    if (selectedSyncPaths.length === 0) {
      setSyncFeedback(syncTextFeedback("error", "无法生成提交信息", "请先选择要提交的文件。"));
      return;
    }
    setSyncMessageBusy(true);
    setSyncFeedback(null);
    try {
      const response = await api.syncGenerateMessage(selectedSyncPaths);
      setSyncMessage(response.message);
      setSyncMessageRule(response.rule);
    } catch (err) {
      setSyncFeedback(syncErrorFeedback(err, "生成提交信息失败"));
    } finally {
      setSyncMessageBusy(false);
    }
  }

  async function commitSyncFiles() {
    if (selectedSyncPaths.length === 0 || syncMessage.trim() === "") {
      setSyncFeedback(syncTextFeedback("error", "无法提交", "请选择文件并填写提交信息。"));
      return;
    }
    setSyncBusy(true);
    setSyncFeedback(null);
    try {
      const response = await api.syncCommit(selectedSyncPaths, syncMessage.trim());
      await refreshSyncStatus(false);
      setSyncResult(response);
      setSyncFeedback({
        kind: "success",
        title: "提交完成",
        message: response.hash ? `最近提交：${response.hash}` : "Git commit 已完成。",
        stdout: response.stdout,
        stderr: response.stderr,
        exitCode: response.exit_code,
      });
      onDashboardRefresh();
    } catch (err) {
      setSyncFeedback(syncErrorFeedback(err, "提交失败"));
    } finally {
      setSyncBusy(false);
    }
  }

  async function pushSyncCommit() {
    if (!syncStatus) {
      setSyncFeedback(syncTextFeedback("error", "无法推送", "请先刷新同步状态。"));
      return;
    }
    if (!syncStatus.can_push) {
      const message =
        syncStatus.push_count <= 0 && syncStatus.ahead <= 0
          ? "当前没有待推送提交。"
          : syncStatus.setup_hint || "同步配置尚未就绪，请先检测连接或修复同步配置。";
      setSyncFeedback(syncTextFeedback(syncStatus.push_count <= 0 ? "success" : "error", syncStatus.push_count <= 0 ? "已推送" : "无法推送", message));
      return;
    }
    const remote = syncStatus.remote.trim();
    const branch = (syncStatus.branch || "main").trim();
    if (!remote || !branch) {
      setSyncFeedback(syncTextFeedback("error", "无法推送", "缺少 Git remote 或 branch，请先在设置中配置知识库同步默认值。"));
      return;
    }
    if (!window.confirm(`确认推送到 ${remote}/${branch}？`)) {
      return;
    }
    setSyncBusy(true);
    setSyncFeedback(null);
    try {
      const response = await api.syncPush(remote, branch);
      await refreshSyncStatus(false);
      setSyncFeedback({
        kind: "success",
        title: "推送完成",
        message: `已推送到 ${response.remote}/${response.branch}。`,
        stdout: response.stdout,
        stderr: response.stderr,
        exitCode: response.exit_code,
      });
      onDashboardRefresh();
    } catch (err) {
      setSyncFeedback(syncErrorFeedback(err, "推送失败"));
    } finally {
      setSyncBusy(false);
    }
  }

  async function testSyncConnection() {
    setSyncBusy(true);
    setSyncFeedback(null);
    try {
      const response = await api.syncTest();
      if (response.status) {
        setSyncStatus(response.status);
      }
      setSyncFeedback({
        kind: "success",
        title: "连接正常",
        message: response.branch ? `已确认远端分支 ${response.branch} 可访问。` : "已确认远端可访问。",
        stdout: response.stdout,
        stderr: response.stderr,
        exitCode: response.exit_code,
      });
    } catch (err) {
      setSyncFeedback(syncErrorFeedback(err, "检测连接失败"));
    } finally {
      setSyncBusy(false);
    }
  }

  async function setupSyncRepository() {
    if (!window.confirm("确认修复知识库同步配置？该操作只会设置 remote、branch/upstream 或在空目录 clone，不会 reset 或丢弃本地改动。")) {
      return;
    }
    setSyncBusy(true);
    setSyncFeedback(null);
    try {
      const response = await api.syncSetup();
      if (response.status) {
        setSyncStatus(response.status);
        setSelectedSyncPaths((current) => current.filter((item) => response.status?.files.some((file) => file.path === item)));
      } else {
        await refreshSyncStatus(false);
      }
      setSyncFeedback({
        kind: "success",
        title: "同步配置已修复",
        message: response.action === "clone" ? "已 clone 知识库仓库。" : "已更新 remote、branch 与 upstream。",
        stdout: response.stdout,
        stderr: response.stderr,
        exitCode: response.exit_code,
      });
      onDashboardRefresh();
    } catch (err) {
      setSyncFeedback(syncErrorFeedback(err, "修复同步配置失败"));
    } finally {
      setSyncBusy(false);
    }
  }

  function confirmDiscardWikiDraft() {
    if (!dirty) {
      return true;
    }
    return window.confirm("当前文件有未保存内容，继续操作会丢失这些修改。");
  }

  function resetWikiDraft(file: WikiFileResponse | null, restoredDraft = "") {
    const content = file?.content ?? "";
    if (restoredDraft && file?.editable) {
      setDraft(restoredDraft);
      setDirty(restoredDraft !== content);
      if (file.preview !== "markdown") {
        setEditorMode("edit");
      }
      return;
    }
    setDraft(content);
    setDirty(false);
    if (file?.preview === "markdown") {
      setEditorMode("split");
    } else if (file?.editable) {
      setEditorMode("edit");
    }
  }

  async function saveWikiDraft() {
    if (!selected || !selected.editable || !dirty) {
      return;
    }
    setSavingFile(true);
    setError("");
    try {
      const response = await api.wikiSaveFile({
        path: selected.path,
        content: draft,
        expected_sha256: selected.sha256,
      });
      setSelected(response);
      resetWikiDraft(response);
      void refreshTreeAfterFileChange(path);
      setPathInput(path);
      onDashboardRefresh();
      toast.success("文件已保存", response.path);
    } catch (err) {
      if (err instanceof APIError && err.status === 409) {
        setError("文件已被其他任务或会话修改，请重新加载后再编辑。");
      } else {
        setError(errorMessage(err));
      }
    } finally {
      setSavingFile(false);
    }
  }

  async function formatWikiDraft() {
    if (!selected?.editable || !canFormatWikiFile(selected)) {
      return;
    }
    setFormattingFile(true);
    setError("");
    try {
      const formatted = await formatWikiContent(draft, selected.text_kind);
      setDraft(formatted);
      setDirty(formatted !== (selected.content ?? ""));
      toast.success("格式化完成", selected.name);
    } catch (err) {
      setError(errorMessage(err));
      toast.error("格式化失败", errorMessage(err));
    } finally {
      setFormattingFile(false);
    }
  }

  async function replaceWikiFile(file: File) {
    if (!selected || !file) {
      return;
    }
    setReplacingFile(true);
    setError("");
    try {
      const response = await api.wikiReplaceFile(selected.path, file);
      setSelected(response);
      resetWikiDraft(response);
      void refreshTreeAfterFileChange(path);
      setPathInput(path);
      onDashboardRefresh();
      toast.success("文件已替换", response.path);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setReplacingFile(false);
    }
  }

  async function refreshTreeAfterFileChange(nextPath: string) {
    try {
      const response = await api.wikiTree(nextPath);
      setPath(response.path);
      setPathInput(response.path);
      if (!response.path) {
        setTreeNodes(wikiItemsToTreeNodes(response.items));
      } else {
        setTreeNodes((current) => upsertDirectoryChildren(current, response.path, wikiItemsToTreeNodes(response.items), true));
      }
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  return (
    <ModuleFrame
      title="知识库"
      description="在一个工作台内完成 Wiki 浏览、知识库助手、运维任务和 Git 同步。"
      action={
        <Button variant="outline" size="sm" onClick={() => void onDashboardRefresh()}>
          <RefreshCw className="mr-2 h-4 w-4" />
          刷新状态
        </Button>
      }
    >
      <div className="grid gap-2 md:grid-cols-4">
        <StatusLine label="qmd" value={dashboard?.qmd.ok ? "可用" : "异常"} ok={dashboard?.qmd.ok} />
        <StatusLine label="Git 变更" value={`${dashboard?.sync.changed_count ?? 0} 个文件`} ok={(dashboard?.sync.changed_count ?? 0) === 0} />
        <StatusLine label="分支" value={dashboard?.sync.branch || "-"} ok={!dashboard?.sync.error} />
        <StatusLine label="助手任务" value="对话留痕" ok />
      </div>

      {error ? (
        <Alert variant="destructive" className="rounded-lg">
          <AlertTitle>知识库读取失败</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      <Tabs className="min-h-[680px]">
        <TabsList className="w-full justify-start overflow-x-auto rounded-lg bg-white p-1 dark:bg-card">
          {knowledgeViews.map((item) => {
            const Icon = item.icon;
            return (
              <TabsTrigger
                key={item.id}
                active={view === item.id}
                onClick={() => switchView(item.id)}
                className="inline-flex h-8 items-center gap-1.5 whitespace-nowrap px-2.5 text-xs"
              >
                <Icon className="h-3.5 w-3.5 shrink-0" />
                {item.label}
              </TabsTrigger>
            );
          })}
        </TabsList>

        {view === "browse" ? (
          <TabsContent>
            <div className="grid gap-3 xl:grid-cols-[360px_minmax(0,1fr)]">
              <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
                <CardHeader className="space-y-3">
                  <div>
                    <CardTitle className="text-base">Wiki 浏览</CardTitle>
                    <CardDescription>{path ? `当前路径：${path}` : "当前路径：根目录"}</CardDescription>
                  </div>
                  <div className="flex gap-2">
                    <Input
                      placeholder="输入路径跳转"
                      value={pathInput}
                      onChange={(event) => setPathInput(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") {
                          setSelected(null);
                          void loadTree(pathInput);
                        }
                      }}
                    />
                    <Button variant="outline" onClick={() => void loadTree(pathInput)}>
                      <Search className="h-4 w-4" />
                    </Button>
                  </div>
                  <div className="flex gap-2">
                    {path ? (
                      <Button variant="outline" size="sm" onClick={upPath}>
                        返回上级
                      </Button>
                    ) : null}
                    <Button variant="outline" size="sm" onClick={() => void loadTree(path)} disabled={loading}>
                      <RefreshCw className={cn("mr-2 h-4 w-4", loading && "animate-spin")} />
                      刷新
                    </Button>
                  </div>
                </CardHeader>
                <CardContent>
                  <ScrollArea className="h-[520px] rounded-lg">
                    <FileTree
                      nodes={treeNodes}
                      rootLabel="Wiki"
                      rootPath=""
                      selectedPath={selected?.path}
                      activePath={path}
                      loadingRoot={loading}
                      emptyText="暂无 Wiki 文件"
                      onSelectRoot={() => {
                        setSelected(null);
                        void loadTree("");
                      }}
                      onSelectFile={(node) => void openWikiNode(node)}
                      onToggleDirectory={(node) => void openWikiNode(node)}
                    />
                  </ScrollArea>
                </CardContent>
              </Card>
              <WikiFileWorkspace
                file={selected}
                draft={draft}
                dirty={dirty}
                loading={loading}
                saving={savingFile}
                formatting={formattingFile}
                replacing={replacingFile}
                mode={editorMode}
                onDraftChange={(value) => {
                  setDraft(value);
                  setDirty(value !== (selected?.content ?? ""));
                }}
                onModeChange={setEditorMode}
                onSave={() => void saveWikiDraft()}
                onFormat={() => void formatWikiDraft()}
                onReplace={(file) => void replaceWikiFile(file)}
              />
            </div>
          </TabsContent>
        ) : null}

        {view === "assistant" ? (
          <TabsContent className="h-[680px] overflow-hidden rounded-lg border bg-white dark:bg-card">
            <AdminChat
              username={user.username}
              embedded
              title="知识库助手"
              sidebarTitle="知识库会话"
              sidebarSubtitle="本地保存"
              storageKey="wikios.knowledge.assistant.chat"
              sidebarStorageKey="wikios.knowledge.assistant.sidebar.open"
              showAdminShortcuts={false}
              showKnowledgeTasks
              onKnowledgeChanged={onDashboardRefresh}
            />
          </TabsContent>
        ) : null}

        {view === "sync" ? (
          <TabsContent>
            <KnowledgeSyncPanel
              status={syncStatus}
              selectedPaths={selectedSyncPaths}
              message={syncMessage}
              messageRule={syncMessageRule}
              result={syncResult}
              busy={syncBusy}
              messageBusy={syncMessageBusy}
              feedback={syncFeedback}
              onRefresh={() => void refreshSyncStatus(false)}
              onTogglePath={toggleSyncPath}
              onMessageChange={setSyncMessage}
              onGenerateMessage={() => void generateSyncMessage()}
              onCommit={() => void commitSyncFiles()}
              onPush={() => void pushSyncCommit()}
              onTest={() => void testSyncConnection()}
              onSetup={() => void setupSyncRepository()}
            />
          </TabsContent>
        ) : null}
      </Tabs>
    </ModuleFrame>
  );
}

function WikiFileWorkspace({
  file,
  draft,
  dirty,
  loading,
  saving,
  formatting,
  replacing,
  mode,
  onDraftChange,
  onModeChange,
  onSave,
  onFormat,
  onReplace,
}: {
  file: WikiFileResponse | null;
  draft: string;
  dirty: boolean;
  loading: boolean;
  saving: boolean;
  formatting: boolean;
  replacing: boolean;
  mode: WikiEditorMode;
  onDraftChange: (value: string) => void;
  onModeChange: (mode: WikiEditorMode) => void;
  onSave: () => void;
  onFormat: () => void;
  onReplace: (file: File) => void;
}) {
  const fileInputRef = React.useRef<HTMLInputElement | null>(null);
  const isMarkdown = file?.preview === "markdown";
  const canFormat = Boolean(file && canFormatWikiFile(file));
  const editorVisible = Boolean(file?.editable && (!isMarkdown || mode !== "preview"));
  const previewVisible = Boolean(isMarkdown && mode !== "edit");
  const editorExtensions = React.useMemo(() => (file ? wikiEditorExtensions(file.text_kind) : []), [file]);
  const editorTheme = useCodeMirrorTheme();

  React.useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const command = event.metaKey || event.ctrlKey;
      if (!command || !file?.editable) {
        return;
      }
      const key = event.key.toLowerCase();
      if (key === "s") {
        event.preventDefault();
        onSave();
      }
      if (key === "f" && event.shiftKey && canFormat) {
        event.preventDefault();
        onFormat();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [canFormat, file?.editable, onFormat, onSave]);

  return (
    <Card className="min-h-[620px] rounded-lg border bg-white shadow-sm dark:bg-card">
      <CardHeader className="space-y-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <CardTitle className="text-base">文件工作台</CardTitle>
            <CardDescription className="break-all">{file?.path ?? "选择一个 Wiki 文件后在这里查看、编辑或替换。"}</CardDescription>
          </div>
          {file ? (
            <div className="flex flex-wrap items-center gap-2">
              {dirty ? <Badge variant="warning">未保存</Badge> : file.editable ? <Badge variant="success">已同步</Badge> : <Badge>非文本</Badge>}
              <a
                href={api.wikiDownloadURL(file.path)}
                target="_blank"
                rel="noreferrer"
                className="inline-flex h-9 items-center gap-2 rounded-xl border px-3 text-sm hover:bg-slate-50 dark:hover:bg-secondary"
                title="下载这个文件"
              >
                <Download className="h-4 w-4" />
                下载
              </a>
            </div>
          ) : null}
        </div>
        {file ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-slate-50 px-3 py-2 text-xs text-muted-foreground dark:bg-background">
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <span className="font-mono text-foreground">{file.name}</span>
              <span>{file.text_kind || file.preview}</span>
              <span>{file.size.toLocaleString()} bytes</span>
              <span>{formatDate(file.modified_at)}</span>
            </div>
            {file.editable ? (
              <div className="flex flex-wrap items-center gap-2">
                {isMarkdown ? (
                  <div className="flex rounded-lg border bg-white p-0.5 dark:bg-card">
                    <Button variant={mode === "edit" ? "secondary" : "ghost"} size="sm" className="h-7 rounded-md px-2 text-xs" onClick={() => onModeChange("edit")}>
                      <PanelLeftClose className="mr-1 h-3.5 w-3.5" />
                      编辑
                    </Button>
                    <Button variant={mode === "split" ? "secondary" : "ghost"} size="sm" className="h-7 rounded-md px-2 text-xs" onClick={() => onModeChange("split")}>
                      <Code2 className="mr-1 h-3.5 w-3.5" />
                      双栏
                    </Button>
                    <Button variant={mode === "preview" ? "secondary" : "ghost"} size="sm" className="h-7 rounded-md px-2 text-xs" onClick={() => onModeChange("preview")}>
                      <PanelRightClose className="mr-1 h-3.5 w-3.5" />
                      预览
                    </Button>
                  </div>
                ) : null}
                <Button variant="outline" size="sm" onClick={onFormat} disabled={!canFormat || formatting}>
                  {formatting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Languages className="mr-2 h-4 w-4" />}
                  格式化
                </Button>
                <Button size="sm" onClick={onSave} disabled={!dirty || saving}>
                  {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
                  保存
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}
      </CardHeader>
      <CardContent>
        {loading && !file ? <div className="py-12 text-center text-sm text-muted-foreground">正在读取...</div> : null}
        {!loading && !file ? <EmptyPanel text="请选择左侧文件。" /> : null}
        {file?.editable ? (
          <div
            className={cn(
              "grid min-h-[540px] overflow-hidden rounded-lg border bg-background",
              editorVisible && previewVisible ? "xl:grid-cols-2" : "grid-cols-1",
            )}
          >
            {editorVisible ? (
              <div className="min-w-0 border-b xl:border-b-0 xl:border-r">
                <div className="flex h-9 items-center gap-2 border-b bg-slate-50 px-3 text-xs font-medium text-muted-foreground dark:bg-secondary/40">
                  <FileText className="h-3.5 w-3.5" />
                  源码
                </div>
                <CodeMirror
                  value={draft}
                  height="500px"
                  basicSetup={{
                    lineNumbers: true,
                    foldGutter: true,
                    highlightActiveLine: true,
                    autocompletion: true,
                  }}
                  extensions={editorExtensions}
                  theme={editorTheme}
                  onChange={onDraftChange}
                  className="wiki-code-editor text-sm"
                />
              </div>
            ) : null}
            {previewVisible ? (
              <div className="min-w-0">
                <div className="flex h-9 items-center gap-2 border-b bg-slate-50 px-3 text-xs font-medium text-muted-foreground dark:bg-secondary/40">
                  <BookOpen className="h-3.5 w-3.5" />
                  Markdown 预览
                </div>
                <div className="h-[500px] overflow-auto p-4">
                  <MarkdownContent className="prose prose-slate max-w-none dark:prose-invert">{draft}</MarkdownContent>
                </div>
              </div>
            ) : null}
          </div>
        ) : null}
        {file && !file.editable ? (
          <div className="flex min-h-[520px] flex-col justify-center rounded-lg border border-dashed bg-slate-50 p-8 text-center dark:bg-background">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-white text-muted-foreground shadow-sm dark:bg-secondary">
              <Upload className="h-5 w-5" />
            </div>
            <div className="mt-4 text-sm font-medium text-foreground">该文件不是文本格式</div>
            <div className="mx-auto mt-2 max-w-md text-sm leading-6 text-muted-foreground">
              可以下载后在本地编辑，再上传同名内容覆盖当前知识库文件。
            </div>
            <div className="mt-5 flex justify-center">
              <input
                ref={fileInputRef}
                type="file"
                className="hidden"
                onChange={(event) => {
                  const replacement = event.target.files?.[0];
                  if (replacement) {
                    onReplace(replacement);
                  }
                  event.currentTarget.value = "";
                }}
              />
              <Button variant="outline" onClick={() => fileInputRef.current?.click()} disabled={replacing}>
                {replacing ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Upload className="mr-2 h-4 w-4" />}
                上传替换
              </Button>
            </div>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function KnowledgeSyncPanel({
  status,
  selectedPaths,
  message,
  messageRule,
  result,
  busy,
  messageBusy,
  feedback,
  onRefresh,
  onTogglePath,
  onMessageChange,
  onGenerateMessage,
  onCommit,
  onPush,
  onTest,
  onSetup,
}: {
  status: SyncStatusResponse | null;
  selectedPaths: string[];
  message: string;
  messageRule: string;
  result: SyncCommitResponse | null;
  busy: boolean;
  messageBusy: boolean;
  feedback: SyncOperationFeedback | null;
  onRefresh: () => void;
  onTogglePath: (path: string) => void;
  onMessageChange: (value: string) => void;
  onGenerateMessage: () => void;
  onCommit: () => void;
  onPush: () => void;
  onTest: () => void;
  onSetup: () => void;
}) {
  const pushDone = syncAlreadyPushed(status, feedback);
  const pushDisabled = busy || !status?.can_push;
  const pushLabel = pushDone ? "已推送" : `推送${(status?.push_count ?? 0) > 0 ? `（${status?.push_count}）` : ""}`;
  return (
    <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <CardTitle className="flex items-center gap-2 text-base">
                <GitBranch className="h-4 w-4" />
                Wiki 同步
              </CardTitle>
              <CardDescription>HTTPS Token 走环境变量，server 使用非交互 Git 执行检测、修复、提交和推送。</CardDescription>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" onClick={onRefresh} disabled={busy}>
                <RefreshCw className={cn("mr-2 h-4 w-4", busy && "animate-spin")} />
                刷新
              </Button>
              <Button variant="outline" size="sm" onClick={onTest} disabled={busy}>
                <PlugZap className="mr-2 h-4 w-4" />
                检测连接
              </Button>
              <Button variant="outline" size="sm" onClick={onSetup} disabled={busy}>
                <Wrench className="mr-2 h-4 w-4" />
                修复配置
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <div className="mb-3 grid gap-2 md:grid-cols-3">
              <StatusLine label="仓库" value={status?.repo_ready ? "已初始化" : "未初始化"} ok={status?.repo_ready} />
              <StatusLine label="Remote" value={status?.remote || "-"} ok={status?.remote_ready} />
              <StatusLine label="凭据" value={status?.auth_configured ? "已配置" : "未配置"} ok={status?.auth_configured} />
              <StatusLine label="分支" value={status?.branch || "-"} ok={status?.branch_ready} />
              <StatusLine label="待提交" value={`${status?.changed_count ?? status?.files.length ?? 0} 个文件`} ok={(status?.changed_count ?? 0) === 0} />
              <StatusLine label="待推送" value={`${status?.push_count ?? 0} 个提交`} ok={(status?.push_count ?? 0) === 0} />
            </div>
          {status?.configured_url_redacted && (!status.remote_url_redacted || status.remote_matches_configured) ? (
            <div className="mb-3 rounded-lg border bg-slate-50 px-3 py-2 text-xs text-muted-foreground dark:bg-background">
              Git URL：<span className="break-all font-mono">{status.configured_url_redacted}</span>
            </div>
          ) : null}
          {!status?.configured_url_redacted && status?.remote_url_redacted ? (
            <div className="mb-3 rounded-lg border bg-slate-50 px-3 py-2 text-xs text-muted-foreground dark:bg-background">
              Git URL：<span className="break-all font-mono">{status.remote_url_redacted}</span>
            </div>
          ) : null}
          {status?.configured_url_redacted && status.remote_url_redacted && !status.remote_matches_configured ? (
            <Alert className="mb-3 rounded-lg border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-950/30 dark:text-amber-100">
              <AlertTitle>仓库 remote 与环境变量不一致</AlertTitle>
              <AlertDescription>
                <div>配置 URL：<span className="break-all font-mono">{status.configured_url_redacted}</span></div>
                <div className="mt-1">仓库 remote：<span className="break-all font-mono">{status.remote_url_redacted}</span></div>
                <div className="mt-2">点击“修复配置”会把仓库 remote 更新成 `.env` 里的 Git URL。</div>
              </AlertDescription>
            </Alert>
          ) : null}
          {status?.needs_setup ? (
            <Alert className="mb-3 rounded-lg border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-950/30 dark:text-amber-100">
              <AlertTitle>同步配置需要处理</AlertTitle>
              <AlertDescription>{status.setup_hint || "请先检测连接或修复同步配置。"}</AlertDescription>
            </Alert>
          ) : null}
          <div className="rounded-lg border">
            {(status?.files.length ?? 0) === 0 ? (
              <div className="px-4 py-10 text-center text-sm text-muted-foreground">{busy ? "正在读取变更..." : "当前没有需要同步的文件。"}</div>
            ) : (
              <div className="max-h-[420px] overflow-y-auto">
                {status?.files.map((file) => (
                  <label key={file.path} className="flex items-center gap-3 border-b px-4 py-3 text-sm last:border-b-0">
                    <input
                      type="checkbox"
                      checked={selectedPaths.includes(file.path)}
                      onChange={() => onTogglePath(file.path)}
                      title="选择是否把这个文件加入本次提交"
                    />
                    <span className="w-14 shrink-0 rounded-full bg-slate-100 px-2 py-1 text-center text-[11px] text-slate-600 dark:bg-secondary dark:text-muted-foreground">{file.status || "?"}</span>
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">{file.path}</span>
                    {file.deleted ? <span className="text-xs text-rose-600 dark:text-rose-300">已删除</span> : <span className="text-xs text-emerald-700 dark:text-emerald-300">可提交</span>}
                  </label>
                ))}
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="text-base">提交</CardTitle>
          <CardDescription>已选择 {selectedPaths.length} 个文件。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="space-y-3">
            <div>
              <div className="mb-2 flex items-center justify-between gap-2">
                <label className="text-xs font-semibold text-slate-600 dark:text-muted-foreground">提交信息</label>
                <Button type="button" variant="outline" size="sm" disabled={busy || messageBusy || selectedPaths.length === 0} onClick={onGenerateMessage}>
                  <Sparkles className="mr-2 h-4 w-4" />
                  {messageBusy ? "生成中" : "LLM 生成"}
                </Button>
              </div>
              <Input value={message} onChange={(event) => onMessageChange(event.target.value)} placeholder="例如：更新 Wiki 内容" />
              <p className="mt-2 text-[11px] leading-5 text-muted-foreground">
                {messageRule || "规则：中文一行，说明本次 Wiki 资料变更，不提 LLM/AI/server/prompt。"}
              </p>
            </div>
            {result ? <div className="rounded-lg border bg-emerald-50 p-3 text-xs text-emerald-800 dark:border-emerald-500/40 dark:bg-emerald-950/30 dark:text-emerald-100">最近提交：{result.hash}</div> : null}
            {(status?.commits_to_push.length ?? 0) > 0 ? (
              <div className="rounded-lg border border-emerald-200 bg-emerald-50/60 p-3 dark:border-emerald-500/40 dark:bg-emerald-950/30">
                <div className="text-xs font-semibold text-emerald-800 dark:text-emerald-100">待推送提交</div>
                <div className="mt-2 space-y-1">
                  {status?.commits_to_push.map((commit) => (
                    <div key={commit.hash} className="text-xs text-emerald-900 dark:text-emerald-100">
                      <span className="font-mono">{commit.hash}</span> {commit.subject}
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
            {feedback ? <SyncFeedbackAlert feedback={feedback} /> : null}
            <div className="flex flex-wrap gap-2">
              <Button disabled={busy || !status?.can_commit || selectedPaths.length === 0 || message.trim() === ""} onClick={onCommit}>
                提交
              </Button>
              <Button variant="outline" disabled={pushDisabled} onClick={onPush}>
                {pushLabel}
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function SyncFeedbackAlert({ feedback }: { feedback: SyncOperationFeedback }) {
  const stdout = feedback.stdout?.trim() ?? "";
  const stderr = feedback.stderr?.trim() ?? "";
  const hasDetails = Boolean(stdout || stderr || typeof feedback.exitCode === "number" || feedback.code);
  return (
    <Alert
      variant={feedback.kind === "error" ? "destructive" : "default"}
      className={cn(
        "rounded-lg",
        feedback.kind === "success" && "border-emerald-200 bg-emerald-50 text-emerald-900 dark:border-emerald-500/40 dark:bg-emerald-950/30 dark:text-emerald-100",
      )}
    >
      <AlertTitle>{feedback.title}</AlertTitle>
      <div className={cn("text-sm leading-6", feedback.kind === "error" ? "text-red-800 dark:text-red-100" : "text-emerald-800 dark:text-emerald-100")}>
        {feedback.message}
      </div>
      {hasDetails ? (
        <div className="mt-3 space-y-2 rounded-md border border-current/10 bg-background/70 p-3 text-xs">
          {feedback.code ? (
            <div className="flex flex-wrap gap-2">
              <span className="font-semibold">错误码</span>
              <span className="break-all font-mono">{feedback.code}</span>
            </div>
          ) : null}
          {typeof feedback.exitCode === "number" ? (
            <div className="flex flex-wrap gap-2">
              <span className="font-semibold">退出码</span>
              <span className="font-mono">{feedback.exitCode}</span>
            </div>
          ) : null}
          {stderr ? (
            <div>
              <div className="mb-1 font-semibold">stderr</div>
              <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded bg-black/5 p-2 font-mono text-[11px] leading-5">
                {stderr}
              </pre>
            </div>
          ) : null}
          {stdout ? (
            <div>
              <div className="mb-1 font-semibold">stdout</div>
              <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded bg-black/5 p-2 font-mono text-[11px] leading-5">
                {stdout}
              </pre>
            </div>
          ) : null}
        </div>
      ) : null}
    </Alert>
  );
}

function syncAlreadyPushed(status: SyncStatusResponse | null, feedback: SyncOperationFeedback | null) {
  return (
    status !== null &&
    feedback?.kind === "success" &&
    feedback.title === "推送完成" &&
    (status.push_count ?? 0) <= 0 &&
    (status.ahead ?? 0) <= 0 &&
    (status.commits_to_push?.length ?? 0) === 0
  );
}

function EmptyPanel({ text }: { text: string }) {
  return <div className="rounded-lg border border-dashed p-10 text-center text-sm text-muted-foreground">{text}</div>;
}

export function ReviewModule({ setDetail, onDashboardRefresh }: BaseModuleProps) {
  const [item, setItem] = React.useState<ReviewItem | null>(null);
  const [pending, setPending] = React.useState(0);
  const [answer, setAnswer] = React.useState("");
  const [targetPath, setTargetPath] = React.useState("");
  const [reason, setReason] = React.useState("");
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");

  const loadNext = React.useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await api.reviewNext();
      setItem(response.item ?? null);
      setPending(response.pending_count);
      setAnswer(response.item?.draft_answer ?? "");
      setTargetPath(response.item?.suggested_target_path ?? "");
      if (response.item) {
        setDetail("审查详情", <ReviewDetail item={response.item} />);
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [setDetail]);

  React.useEffect(() => {
    void loadNext();
  }, [loadNext]);

  async function approve() {
    if (!item) return;
    setLoading(true);
    try {
      await api.reviewApprove(item.id, { question: item.question, answer, target_path: targetPath });
      await loadNext();
      onDashboardRefresh();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  async function reject() {
    if (!item) return;
    setLoading(true);
    try {
      await api.reviewReject(item.id, { reason: reason || "管理员驳回" });
      await loadNext();
      onDashboardRefresh();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  async function deleteItem() {
    if (!item) return;
    const confirmed = window.confirm("确定从待审队列删除这条问题吗？不会写入知识库或禁答列表。");
    if (!confirmed) {
      return;
    }
    setLoading(true);
    setError("");
    try {
      await api.reviewDelete(item.id);
      await loadNext();
      onDashboardRefresh();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  return (
    <ModuleFrame
      title="问题审查"
      description="处理 public 低置信自答和需要人工确认的知识沉淀。"
      action={
        <Button variant="outline" size="sm" onClick={() => void loadNext()} disabled={loading}>
          <RefreshCw className={cn("mr-2 h-4 w-4", loading && "animate-spin")} />
          下一条
        </Button>
      }
    >
      {error ? (
        <Alert variant="destructive" className="rounded-lg">
          <AlertTitle>审查队列异常</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="text-base">待审队列</CardTitle>
          <CardDescription>当前剩余 {pending} 条。</CardDescription>
        </CardHeader>
        <CardContent>
          {item ? (
            <div className="space-y-4">
              <div className="rounded-lg border bg-slate-50 p-3 dark:bg-secondary/50">
                <div className="mb-1 text-xs text-muted-foreground">问题</div>
                <div className="text-sm">{item.question}</div>
              </div>
              <Textarea className="min-h-36" value={answer} onChange={(event) => setAnswer(event.target.value)} />
              <Input value={targetPath} onChange={(event) => setTargetPath(event.target.value)} placeholder="目标知识页路径" />
              <Input value={reason} onChange={(event) => setReason(event.target.value)} placeholder="驳回原因（可选）" />
              <div className="flex flex-wrap gap-2">
                <Button onClick={() => void approve()} disabled={loading}>
                  <CheckCircle2 className="mr-2 h-4 w-4" />
                  通过
                </Button>
                <Button variant="outline" onClick={() => void reject()} disabled={loading}>
                  <XCircle className="mr-2 h-4 w-4" />
                  驳回
                </Button>
                <Button variant="destructive" onClick={() => void deleteItem()} disabled={loading}>
                  <Trash2 className="mr-2 h-4 w-4" />
                  删除
                </Button>
              </div>
            </div>
          ) : (
            <div className="rounded-lg border border-emerald-100 bg-emerald-50 p-6 text-center text-sm text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-950/30 dark:text-emerald-100">
              暂无待审内容。
            </div>
          )}
        </CardContent>
      </Card>
    </ModuleFrame>
  );
}

function IntentSettingsPanel({ setDetail }: Pick<BaseModuleProps, "setDetail">) {
  const [source, setSource] = React.useState("");
  const [status, setStatus] = React.useState<PublicIntentsStatus | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState("");

  const load = React.useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await api.getPublicIntents();
      setSource(response.source);
      setStatus(response.status);
      setDetail("前置话术状态", <pre className="whitespace-pre-wrap text-xs">{formatJSON(response.status)}</pre>);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [setDetail]);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    setSaving(true);
    setError("");
    try {
      const response = await api.updatePublicIntents(source);
      setStatus(response.status);
      setDetail("前置话术状态", <pre className="whitespace-pre-wrap text-xs">{formatJSON(response.status)}</pre>);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-base font-semibold">意图话术</h3>
          <p className="mt-1 text-sm text-muted-foreground">编辑 public intents、兜底话术池和模型不可用话术。</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
          <RefreshCw className={cn("mr-2 h-4 w-4", loading && "animate-spin")} />
          重载
        </Button>
      </div>
      {error ? (
        <Alert variant="destructive" className="rounded-lg">
          <AlertTitle>配置读取失败</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="text-base">configs/public_intents.yaml</CardTitle>
          <CardDescription>
            当前规则数 {status?.rule_count ?? 0}，状态文件：{status?.path ?? "-"}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Textarea className="min-h-[520px] font-mono text-xs" value={source} onChange={(event) => setSource(event.target.value)} />
          <div className="flex gap-2">
            <Button onClick={() => void save()} disabled={saving}>
              {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
              保存配置
            </Button>
            {status?.warnings?.length ? <Badge variant="warning">{status.warnings.length} 个警告</Badge> : null}
          </div>
        </CardContent>
      </Card>
    </>
  );
}

export function PublicConfigModule({ setDetail }: BaseModuleProps) {
  return (
    <ModuleFrame title="Public 配置" description="管理 public intents、兜底话术池、日志开关和脱敏策略。">
      <IntentSettingsPanel setDetail={setDetail} />
    </ModuleFrame>
  );
}

export function PromptsModule(_props: BaseModuleProps) {
  return (
    <ReadonlyModule
      icon={FileJson}
      title="提示词"
      description="第一版先预留 public routed/admin/json 修复/同步提交 prompt 的查看与测试入口。"
      items={["public_router_system.md", "public_specialist_*.md", "admin_sync_commit_message.md", "json 修复 prompt"]}
    />
  );
}

export function LogsModule({ dashboard }: BaseModuleProps) {
  return (
    <ReadonlyModule
      icon={History}
      title="日志"
      description="查看 trace、用户会话日志和模型切换记录；日志策略已归入设置页。"
      items={[
        `用户会话日志读取：${dashboard?.public_answer_log.enabled ? "可用" : "未写入"}`,
        "trace_id 检索预留",
        "模型自动切换记录预留",
      ]}
    />
  );
}

const defaultRuntimeSettings: AdminRuntimeSettings = {
  public_query: {
    direct_min: 0.7,
    review_min: 0.25,
    candidate_top_k: 6,
    max_evidence_chars: 2400,
    router_model_id: "",
    specialist_model_id: "",
    router_enable_thinking: false,
    specialist_enable_thinking: true,
  },
  support: {
    phone: "400-1080-106",
    wecom: "企业微信",
  },
  answer_log: {
    enabled: true,
    redact: true,
    retention_days: 14,
  },
  knowledge: {
    max_text_file_kb: 500,
  },
  sync: {
    remote: "origin",
    branch: "main",
  },
};

const defaultRuntimeEnvironment: AdminRuntimeEnvironment = {
  server_port: 0,
  server_mode: "",
  wiki_root: "",
  wiki_name: "",
  qmd_index: "",
  workspace_dir: "",
  sqlite_path: "",
  web_dist_dir: "",
  web_enabled: true,
  public_intents_path: "",
};

type SettingsTab = "models" | "public-query" | "intents" | "logs" | "knowledge" | "environment";

const settingsTabs: Array<{ id: SettingsTab; label: string; icon: typeof Settings }> = [
  { id: "models", label: "模型", icon: Bot },
  { id: "public-query", label: "公开问答", icon: MessageCircle },
  { id: "intents", label: "意图话术", icon: ListChecks },
  { id: "logs", label: "日志隐私", icon: History },
  { id: "knowledge", label: "知识库同步", icon: Database },
  { id: "environment", label: "环境", icon: Settings },
];

export function SettingsModule({ dashboard, onDashboardRefresh, setDetail }: BaseModuleProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [activeTab, setActiveTab] = React.useState<SettingsTab>(() => normalizeSettingsTab(searchParams.get("tab")));
  const [form, setForm] = React.useState<AdminRuntimeSettings>(defaultRuntimeSettings);
  const [defaults, setDefaults] = React.useState<AdminRuntimeSettings>(defaultRuntimeSettings);
  const [environment, setEnvironment] = React.useState<AdminRuntimeEnvironment>(defaultRuntimeEnvironment);
  const [updatedAt, setUpdatedAt] = React.useState("");
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [saved, setSaved] = React.useState(false);
  const [error, setError] = React.useState("");
  const [fieldErrors, setFieldErrors] = React.useState<Record<string, string>>({});
  const [runtimeModels, setRuntimeModels] = React.useState<LLMModel[]>([]);

  const loadSettings = React.useCallback(async () => {
    setLoading(true);
    setError("");
    setFieldErrors({});
    try {
      const [response, modelsResponse] = await Promise.all([
        api.getRuntimeSettings(),
        api.listLLMModels().catch((): LLMModelsResponse => ({ models: [] })),
      ]);
      setForm(response.settings);
      setDefaults(response.defaults);
      setEnvironment(response.environment);
      setUpdatedAt(response.updated_at ?? "");
      setRuntimeModels(modelsResponse.models);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void loadSettings();
  }, [loadSettings]);

  React.useEffect(() => {
    setActiveTab(normalizeSettingsTab(searchParams.get("tab")));
  }, [searchParams]);

  function switchTab(nextTab: SettingsTab) {
    setActiveTab(nextTab);
    router.replace(`/settings?tab=${nextTab}`, { scroll: false });
  }

  async function saveSettings() {
    setSaving(true);
    setSaved(false);
    setError("");
    setFieldErrors({});
    try {
      const response = await api.updateRuntimeSettings(form);
      setForm(response.settings);
      setDefaults(response.defaults);
      setEnvironment(response.environment);
      setUpdatedAt(response.updated_at ?? "");
      setSaved(true);
      onDashboardRefresh();
    } catch (err) {
      setFieldErrors(apiFieldErrors(err));
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  function patchSettings(patch: (current: AdminRuntimeSettings) => AdminRuntimeSettings) {
    setForm((current) => patch(current));
    setSaved(false);
  }

  function updateNumber(path: RuntimeNumberPath, value: number) {
    const normalized = Number.isFinite(value) ? value : 0;
    patchSettings((current) => setRuntimeNumber(current, path, normalized));
  }

  function updateString(path: RuntimeStringPath, value: string) {
    patchSettings((current) => setRuntimeString(current, path, value));
  }

  function updateBool(path: RuntimeBoolPath, value: boolean) {
    patchSettings((current) => setRuntimeBool(current, path, value));
  }

  const publicQueryModelOptions = React.useMemo(
    () => [
      { value: "", label: "使用当前模型" },
      ...runtimeModels.map((model) => ({
        value: model.id,
        label: `${model.display_name}${model.is_active ? "（当前）" : ""} · ${model.model_name}`,
      })),
    ],
    [runtimeModels],
  );

  return (
    <ModuleFrame
      title="系统设置"
      description="集中管理运行中可修改的模型、公开问答、日志和知识库同步配置。"
      action={
        <Button variant="outline" size="sm" onClick={() => void loadSettings()} disabled={loading}>
          <RefreshCw className={cn("mr-2 h-4 w-4", loading && "animate-spin")} />
          刷新
        </Button>
      }
    >
      {error ? (
        <Alert variant="destructive" className="rounded-lg">
          <AlertTitle>设置保存失败</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      {saved ? (
        <Alert className="rounded-lg border-emerald-200 bg-emerald-50 text-emerald-800">
          <AlertTitle>已保存</AlertTitle>
          <AlertDescription>运行时配置已保存，相关服务读取新配置即时生效。</AlertDescription>
        </Alert>
      ) : null}

      <Tabs>
        <TabsList className="flex w-full flex-wrap justify-start gap-1 rounded-lg bg-white p-1 dark:bg-card">
          {settingsTabs.map((item) => {
            const Icon = item.icon;
            return (
              <TabsTrigger
                key={item.id}
                active={activeTab === item.id}
                onClick={() => switchTab(item.id)}
                className="inline-flex h-9 items-center gap-2 whitespace-nowrap"
              >
                <Icon className="h-4 w-4" />
                {item.label}
              </TabsTrigger>
            );
          })}
        </TabsList>

        {activeTab === "models" ? (
          <TabsContent>
            <ModelSettingsPanel onDashboardRefresh={onDashboardRefresh} setDetail={setDetail} />
          </TabsContent>
        ) : null}

        {activeTab === "public-query" ? (
          <TabsContent>
            <div className="grid gap-3">
              <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
                <CardHeader>
                  <CardTitle className="flex items-center gap-2 text-base">
                    <MessageCircle className="h-4 w-4" />
                    公开问答策略
                  </CardTitle>
                  <CardDescription>控制 public 问答的检索规模、证据长度、人工审查阈值和联系方式。</CardDescription>
                </CardHeader>
                <CardContent>
                  <div className="grid gap-4">
                    <div className="grid gap-4 lg:grid-cols-2">
                      <RoleModelSetting
                        title="Router"
                        description="理解问题、改写指代并分配专家；建议关闭思考模式降低延迟。"
                        modelValue={form.public_query.router_model_id ?? ""}
                        modelError={fieldErrors["public_query.router_model_id"]}
                        modelOptions={publicQueryModelOptions}
                        thinkingChecked={Boolean(form.public_query.router_enable_thinking)}
                        thinkingDescription="开启后可能更稳，但通常会让路由变慢。"
                        onModelChange={(value) => updateString("public_query.router_model_id", value)}
                        onThinkingChange={(checked) => updateBool("public_query.router_enable_thinking", checked)}
                      />
                      <RoleModelSetting
                        title="Specialist"
                        description="读取证据并生成最终回复；可保持当前模型默认行为。"
                        modelValue={form.public_query.specialist_model_id ?? ""}
                        modelError={fieldErrors["public_query.specialist_model_id"]}
                        modelOptions={publicQueryModelOptions}
                        thinkingChecked={Boolean(form.public_query.specialist_enable_thinking)}
                        thinkingDescription="开启后可能更稳但更慢；关闭则强制 no-think。"
                        onModelChange={(value) => updateString("public_query.specialist_model_id", value)}
                        onThinkingChange={(checked) => updateBool("public_query.specialist_enable_thinking", checked)}
                      />
                    </div>
                    <div className="grid gap-4 md:grid-cols-2">
                      <RuntimeNumberInput
                        label="直答置信度"
                        description={`默认 ${defaults.public_query.direct_min}，达到后不进入审查队列。`}
                        value={form.public_query.direct_min}
                        step={0.01}
                        min={0}
                        max={1}
                        fieldError={fieldErrors["public_query.direct_min"]}
                        onChange={(value) => updateNumber("public_query.direct_min", value)}
                      />
                      <RuntimeNumberInput
                        label="审查最低置信度"
                        description={`默认 ${defaults.public_query.review_min}，低于该值通常不沉淀审查。`}
                        value={form.public_query.review_min}
                        step={0.01}
                        min={0}
                        max={1}
                        fieldError={fieldErrors["public_query.review_min"]}
                        onChange={(value) => updateNumber("public_query.review_min", value)}
                      />
                      <RuntimeNumberInput
                        label="检索候选数"
                        description={`默认 ${defaults.public_query.candidate_top_k}，影响 qmd 与兜底检索数量。`}
                        value={form.public_query.candidate_top_k}
                        min={1}
                        max={20}
                        fieldError={fieldErrors["public_query.candidate_top_k"]}
                        onChange={(value) => updateNumber("public_query.candidate_top_k", Math.round(value))}
                      />
                      <RuntimeNumberInput
                        label="证据字符上限"
                        description={`默认 ${defaults.public_query.max_evidence_chars}，控制每页进入 prompt 的证据长度。`}
                        value={form.public_query.max_evidence_chars}
                        min={200}
                        max={20000}
                        fieldError={fieldErrors["public_query.max_evidence_chars"]}
                        onChange={(value) => updateNumber("public_query.max_evidence_chars", Math.round(value))}
                      />
                    </div>
                    <div className="grid gap-4 md:grid-cols-2">
                      <RuntimeTextInput
                        label="客服电话"
                        description="会进入 public answer 的客户可见联系方式。"
                        value={form.support.phone}
                        fieldError={fieldErrors["support.phone"]}
                        onChange={(value) => updateString("support.phone", value)}
                      />
                      <RuntimeTextInput
                        label="企业微信"
                        description="用于无法自助解决时的客户引导。"
                        value={form.support.wecom}
                        fieldError={fieldErrors["support.wecom"]}
                        onChange={(value) => updateString("support.wecom", value)}
                      />
                    </div>
                  </div>
                  <div className="mt-5 flex flex-wrap gap-2">
                    <Button onClick={() => void saveSettings()} disabled={saving}>
                      {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
                      保存设置
                    </Button>
                    <Button
                      variant="outline"
                      onClick={() => {
                        setForm(defaults);
                        setSaved(false);
                        setFieldErrors({});
                      }}
                    >
                      恢复默认值
                    </Button>
                  </div>
                </CardContent>
              </Card>
            </div>
          </TabsContent>
        ) : null}

        {activeTab === "intents" ? (
          <TabsContent>
            <IntentSettingsPanel setDetail={setDetail} />
          </TabsContent>
        ) : null}

        {activeTab === "logs" ? (
          <TabsContent>
            <RuntimeLogSettingsPanel
              form={form}
              defaults={defaults}
              fieldErrors={fieldErrors}
              saving={saving}
              onPatch={patchSettings}
              onNumberChange={updateNumber}
              onSave={saveSettings}
            />
          </TabsContent>
        ) : null}

        {activeTab === "knowledge" ? (
          <TabsContent>
            <RuntimeKnowledgeSettingsPanel
              dashboard={dashboard}
              form={form}
              defaults={defaults}
              fieldErrors={fieldErrors}
              saving={saving}
              onNumberChange={updateNumber}
              onStringChange={updateString}
              onSave={saveSettings}
            />
          </TabsContent>
        ) : null}

        {activeTab === "environment" ? (
          <TabsContent>
            <RuntimeEnvironmentPanel dashboard={dashboard} environment={environment} updatedAt={updatedAt} />
          </TabsContent>
        ) : null}
      </Tabs>
    </ModuleFrame>
  );
}

function ModuleFrame({
  title,
  description,
  action,
  children,
}: {
  title: string;
  description: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <ScrollArea className="h-full">
      <div className="mx-auto flex w-full max-w-7xl flex-col gap-4 p-4 lg:p-6">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="text-xl font-semibold tracking-tight">{title}</h2>
            <p className="mt-1 text-sm text-muted-foreground">{description}</p>
          </div>
          {action}
        </div>
        {children}
      </div>
    </ScrollArea>
  );
}

function MetricCard({
  icon: Icon,
  label,
  value,
  detail,
  tone,
  onClick,
}: {
  icon: typeof Bot;
  label: string;
  value: string;
  detail: string;
  tone: "success" | "warning" | "neutral";
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="rounded-lg border bg-white p-4 text-left shadow-sm transition hover:border-slate-300 hover:shadow-md dark:bg-card dark:hover:border-border"
      onClick={onClick}
    >
      <div className="flex items-center justify-between gap-3">
        <div className="text-sm text-muted-foreground">{label}</div>
        <div
          className={cn(
            "flex h-8 w-8 items-center justify-center rounded-lg",
            tone === "success" && "bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-200",
            tone === "warning" && "bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-200",
            tone === "neutral" && "bg-slate-100 text-slate-700 dark:bg-secondary dark:text-secondary-foreground",
          )}
        >
          <Icon className="h-4 w-4" />
        </div>
      </div>
      <div className="mt-3 truncate text-2xl font-semibold">{value}</div>
      <div className="mt-1 truncate text-xs text-muted-foreground">{detail}</div>
    </button>
  );
}

function StatusLine({ label, value, ok }: { label: string; value: string; ok?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-lg border bg-white px-3 py-2 text-sm dark:bg-background">
      <span className="text-muted-foreground">{label}</span>
      <span className="flex min-w-0 items-center gap-2 font-medium">
        {ok === undefined ? null : ok ? <CheckCircle2 className="h-3.5 w-3.5 text-emerald-600" /> : <XCircle className="h-3.5 w-3.5 text-amber-600" />}
        <span className="truncate">{value}</span>
      </span>
    </div>
  );
}

function RuntimeLogSettingsPanel({
  form,
  defaults,
  fieldErrors,
  saving,
  onPatch,
  onNumberChange,
  onSave,
}: {
  form: AdminRuntimeSettings;
  defaults: AdminRuntimeSettings;
  fieldErrors: Record<string, string>;
  saving: boolean;
  onPatch: (patch: (current: AdminRuntimeSettings) => AdminRuntimeSettings) => void;
  onNumberChange: (path: RuntimeNumberPath, value: number) => void;
  onSave: () => void;
}) {
  return (
    <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <History className="h-4 w-4" />
            Public 日志隐私
          </CardTitle>
          <CardDescription>控制用户问答日志是否写入、是否脱敏，以及自动保留周期。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 md:grid-cols-2">
            <ToggleSetting
              label="写入 public answer log"
              description="关闭后后台用户会话页无法读取新的用户问答记录。"
              checked={form.answer_log.enabled}
              onChange={(checked) =>
                onPatch((current) => ({
                  ...current,
                  answer_log: { ...current.answer_log, enabled: checked },
                }))
              }
            />
            <ToggleSetting
              label="日志脱敏"
              description="保存日志前移除或替换敏感字段，建议保持开启。"
              checked={form.answer_log.redact}
              onChange={(checked) =>
                onPatch((current) => ({
                  ...current,
                  answer_log: { ...current.answer_log, redact: checked },
                }))
              }
            />
            <RuntimeNumberInput
              label="保留天数"
              description={`默认 ${defaults.answer_log.retention_days} 天，范围 1-365。`}
              value={form.answer_log.retention_days}
              min={1}
              max={365}
              fieldError={fieldErrors["answer_log.retention_days"]}
              onChange={(value) => onNumberChange("answer_log.retention_days", Math.round(value))}
            />
          </div>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void onSave()} disabled={saving}>
              {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
              保存日志配置
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="text-base">当前摘要</CardTitle>
          <CardDescription>这些值会影响 Dashboard 和用户会话读取。</CardDescription>
        </CardHeader>
        <CardContent>
          <StatusLine label="日志写入" value={form.answer_log.enabled ? "开启" : "关闭"} ok={form.answer_log.enabled} />
          <StatusLine label="脱敏" value={form.answer_log.redact ? "开启" : "关闭"} ok={form.answer_log.redact} />
          <StatusLine label="保留" value={`${form.answer_log.retention_days} 天`} ok />
        </CardContent>
      </Card>
    </div>
  );
}

function RuntimeKnowledgeSettingsPanel({
  dashboard,
  form,
  defaults,
  fieldErrors,
  saving,
  onNumberChange,
  onStringChange,
  onSave,
}: {
  dashboard: AdminDashboardResponse | null;
  form: AdminRuntimeSettings;
  defaults: AdminRuntimeSettings;
  fieldErrors: Record<string, string>;
  saving: boolean;
  onNumberChange: (path: RuntimeNumberPath, value: number) => void;
  onStringChange: (path: RuntimeStringPath, value: string) => void;
  onSave: () => void;
}) {
  return (
    <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Database className="h-4 w-4" />
            知识库运行配置
          </CardTitle>
          <CardDescription>控制上传摄入限制和 Git 同步默认目标，Wiki 根路径与 qmd index 只读展示。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-4 md:grid-cols-2">
            <RuntimeNumberInput
              label="文本上传上限 KB"
              description={`默认 ${defaults.knowledge.max_text_file_kb}KB，影响 txt/md/rtf/docx 等摄入。`}
              value={form.knowledge.max_text_file_kb}
              min={1}
              fieldError={fieldErrors["knowledge.max_text_file_kb"]}
              onChange={(value) => onNumberChange("knowledge.max_text_file_kb", Math.round(value))}
            />
            <RuntimeTextInput
              label="Git remote"
              description={`默认 ${defaults.sync.remote}，同步推送时作为默认远端。`}
              value={form.sync.remote}
              fieldError={fieldErrors["sync.remote"]}
              onChange={(value) => onStringChange("sync.remote", value)}
            />
            <RuntimeTextInput
              label="Git branch"
              description={`默认 ${defaults.sync.branch}，同步推送时作为默认分支。`}
              value={form.sync.branch}
              fieldError={fieldErrors["sync.branch"]}
              onChange={(value) => onStringChange("sync.branch", value)}
            />
          </div>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void onSave()} disabled={saving}>
              {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
              保存知识库配置
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="text-base">只读状态</CardTitle>
          <CardDescription>路径级配置需要重启服务或修改部署配置。</CardDescription>
        </CardHeader>
        <CardContent>
          <StatusLine label="qmd index" value={dashboard?.qmd.index ?? "-"} ok={dashboard?.qmd.ok} />
          <StatusLine label="Wiki root" value={dashboard?.qmd.root ?? "-"} ok={!dashboard?.qmd.error} />
          <StatusLine label="仓库状态" value={dashboard?.sync.repo_ready ? "已初始化" : "未初始化"} ok={dashboard?.sync.repo_ready} />
          <StatusLine label="当前分支" value={dashboard?.sync.branch ?? "-"} ok={!dashboard?.sync.error} />
          <StatusLine label="Git remote" value={dashboard?.sync.remote ?? form.sync.remote} ok={dashboard?.sync.remote_ready} />
          <StatusLine label="配置 URL" value={dashboard?.sync.configured_url_redacted || "由 WIKIOS_WIKI_GIT_URL 提供"} ok={Boolean(dashboard?.sync.configured_url_redacted)} />
          <StatusLine label="仓库 remote" value={dashboard?.sync.remote_url_redacted || "-"} ok={dashboard?.sync.remote_matches_configured} />
          <StatusLine label="凭据" value={dashboard?.sync.auth_configured ? "已配置" : "未配置"} ok={dashboard?.sync.auth_configured} />
          {!dashboard?.sync.remote_matches_configured && dashboard?.sync.configured_url_redacted ? (
            <Alert className="mt-3 rounded-lg border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-950/30 dark:text-amber-100">
              <AlertTitle>仓库 remote 还不是环境里的 URL</AlertTitle>
              <AlertDescription>点击知识库同步页的“修复配置”会把仓库 remote 更新成 `.env` 里的 Git URL。</AlertDescription>
            </Alert>
          ) : null}
          {dashboard?.sync.needs_setup ? (
            <Alert className="mt-3 rounded-lg border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-950/30 dark:text-amber-100">
              <AlertTitle>同步配置待处理</AlertTitle>
              <AlertDescription>{dashboard.sync.setup_hint || "请到知识库同步页检测或修复。"}</AlertDescription>
            </Alert>
          ) : null}
        </CardContent>
      </Card>
    </div>
  );
}

function RuntimeEnvironmentPanel({
  dashboard,
  environment,
  updatedAt,
}: {
  dashboard: AdminDashboardResponse | null;
  environment: AdminRuntimeEnvironment;
  updatedAt: string;
}) {
  return (
    <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Settings className="h-4 w-4" />
            环境配置
          </CardTitle>
          <CardDescription>这些配置来自启动配置或部署环境，不在运行中修改。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 md:grid-cols-2">
            <ReadonlySetting label="服务端口" value={environment.server_port ? String(environment.server_port) : "-"} />
            <ReadonlySetting label="运行模式" value={environment.server_mode || "-"} />
            <ReadonlySetting label="Wiki 名称" value={environment.wiki_name || "-"} />
            <ReadonlySetting label="Wiki root" value={environment.wiki_root || "-"} />
            <ReadonlySetting label="qmd index" value={environment.qmd_index || "-"} />
            <ReadonlySetting label="Workspace" value={environment.workspace_dir || "-"} />
            <ReadonlySetting label="SQLite" value={environment.sqlite_path || "-"} />
            <ReadonlySetting label="Web dist" value={environment.web_dist_dir || "-"} />
            <ReadonlySetting label="Web 静态服务" value={environment.web_enabled ? "开启" : "关闭"} />
            <ReadonlySetting label="Intents 文件" value={environment.public_intents_path || "-"} />
          </div>
        </CardContent>
      </Card>

      <Card className="rounded-lg border bg-white shadow-sm dark:bg-card">
        <CardHeader>
          <CardTitle className="text-base">运行摘要</CardTitle>
          <CardDescription>方便确认当前服务读到的状态。</CardDescription>
        </CardHeader>
        <CardContent>
          <StatusLine label="模型配置数" value={String(dashboard?.models_total ?? 0)} ok />
          <StatusLine label="当前模型" value={dashboard?.active_model?.display_name ?? "未启用"} ok={Boolean(dashboard?.active_model)} />
          <StatusLine label="qmd" value={dashboard?.qmd.ok ? "可用" : "异常"} ok={dashboard?.qmd.ok} />
          <Separator />
          <div className="rounded-lg border bg-slate-50 p-3 text-sm dark:bg-background">
            <div className="text-xs text-muted-foreground">运行时配置最近保存</div>
            <div className="mt-1 break-words font-medium">{updatedAt ? formatDate(updatedAt) : "尚未覆盖默认值"}</div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function RuntimeNumberInput({
  label,
  description,
  value,
  min,
  max,
  step = 1,
  fieldError,
  onChange,
}: {
  label: string;
  description: string;
  value: number;
  min?: number;
  max?: number;
  step?: number;
  fieldError?: string;
  onChange: (value: number) => void;
}) {
  return (
    <label className="block space-y-2 rounded-lg border bg-slate-50 p-3 dark:bg-background">
      <span className="block text-sm font-medium">{label}</span>
      <span className="block text-xs leading-5 text-muted-foreground">{description}</span>
      <Input
        className="h-9 rounded-lg"
        type="number"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(event) => onChange(Number(event.target.value))}
      />
      {fieldError ? <span className="block text-xs text-red-600">{fieldError}</span> : null}
    </label>
  );
}

function RuntimeTextInput({
  label,
  description,
  value,
  fieldError,
  onChange,
}: {
  label: string;
  description: string;
  value: string;
  fieldError?: string;
  onChange: (value: string) => void;
}) {
  return (
    <label className="block space-y-2 rounded-lg border bg-slate-50 p-3 dark:bg-background">
      <span className="block text-sm font-medium">{label}</span>
      <span className="block text-xs leading-5 text-muted-foreground">{description}</span>
      <Input className="h-9 rounded-lg" value={value} onChange={(event) => onChange(event.target.value)} />
      {fieldError ? <span className="block text-xs text-red-600">{fieldError}</span> : null}
    </label>
  );
}

function RuntimeSelectInput({
  label,
  description,
  value,
  fieldError,
  options,
  onChange,
}: {
  label: string;
  description: string;
  value: string;
  fieldError?: string;
  options: Array<{ value: string; label: string }>;
  onChange: (value: string) => void;
}) {
  return (
    <label className="block space-y-2 rounded-lg border bg-white p-3 text-sm dark:bg-background">
      <div className="font-medium">{label}</div>
      <div className="text-xs leading-5 text-muted-foreground">{description}</div>
      <Select value={value} onChange={(event) => onChange(event.target.value)}>
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </Select>
      {fieldError ? <div className="text-xs text-destructive">{fieldError}</div> : null}
    </label>
  );
}

function RoleModelSetting({
  title,
  description,
  modelValue,
  modelError,
  modelOptions,
  thinkingChecked,
  thinkingDescription,
  onModelChange,
  onThinkingChange,
}: {
  title: string;
  description: string;
  modelValue: string;
  modelError?: string;
  modelOptions: Array<{ value: string; label: string }>;
  thinkingChecked: boolean;
  thinkingDescription: string;
  onModelChange: (value: string) => void;
  onThinkingChange: (checked: boolean) => void;
}) {
  return (
    <div className="rounded-lg border bg-slate-50 p-3 dark:bg-background">
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="text-sm font-medium">{title}</div>
          <div className="mt-1 text-xs leading-5 text-muted-foreground">{description}</div>
        </div>
        <div className="flex shrink-0 items-center gap-2 pt-0.5 text-xs text-muted-foreground">
          思考
          <Switch checked={thinkingChecked} onClick={() => onThinkingChange(!thinkingChecked)} />
        </div>
      </div>
      <div className="mt-3">
        <Select value={modelValue} onChange={(event) => onModelChange(event.target.value)}>
          {modelOptions.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </Select>
        {modelError ? <div className="mt-2 text-xs text-destructive">{modelError}</div> : null}
      </div>
      <div className="mt-2 text-xs leading-5 text-muted-foreground">{thinkingDescription}</div>
    </div>
  );
}

function ToggleSetting({
  label,
  description,
  checked,
  onChange,
}: {
  label: string;
  description: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4 rounded-lg border bg-slate-50 p-3 dark:bg-background">
      <div>
        <div className="text-sm font-medium">{label}</div>
        <div className="mt-1 text-xs leading-5 text-muted-foreground">{description}</div>
      </div>
      <Switch checked={checked} onClick={() => onChange(!checked)} />
    </div>
  );
}

function ReadonlySetting({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border bg-slate-50 p-3 text-sm dark:bg-background">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 break-all font-medium">{value}</div>
    </div>
  );
}

function ReadonlyModule({
  icon: Icon,
  title,
  description,
  items,
}: {
  icon: typeof FileJson;
  title: string;
  description: string;
  items: string[];
}) {
  return (
    <ModuleFrame title={title} description={description}>
      <Card className="rounded-lg border bg-white shadow-sm">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Icon className="h-4 w-4" />
            模块规划
          </CardTitle>
          <CardDescription>后端接口会按模块逐步补齐，当前版本保留兼容入口。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-2 md:grid-cols-2">
            {items.map((item) => (
              <div key={item} className="rounded-lg border bg-slate-50 p-3 text-sm">
                {item}
              </div>
            ))}
          </div>
        </CardContent>
      </Card>
    </ModuleFrame>
  );
}

function ModelTestDetail({ model, result }: { model: LLMModel; result: LLMModelTestResponse }) {
  return (
    <div className="space-y-3 text-sm">
      <StatusLine label="模型" value={model.display_name} ok={result.ok} />
      <StatusLine label="耗时" value={`${result.latency_ms}ms`} ok={result.ok} />
      <div className="rounded-lg border bg-slate-50 p-3">
        <div className="mb-1 text-xs text-muted-foreground">结果</div>
        <div>{result.message}</div>
      </div>
      <div className="text-xs text-muted-foreground">测试时间：{formatDate(result.tested_at)}</div>
    </div>
  );
}

function ReviewDetail({ item }: { item: ReviewItem }) {
  return (
    <div className="space-y-3 text-sm">
      <div className="rounded-lg border bg-slate-50 p-3">
        <div className="mb-1 text-xs text-muted-foreground">ID</div>
        <div className="break-all">{item.id}</div>
      </div>
      <div className="rounded-lg border bg-slate-50 p-3">
        <div className="mb-1 text-xs text-muted-foreground">来源</div>
        <pre className="whitespace-pre-wrap text-xs">{formatJSON(item.retrieved_pages ?? item.matched_pages)}</pre>
      </div>
      <div className="rounded-lg border bg-slate-50 p-3">
        <div className="mb-1 text-xs text-muted-foreground">对话片段</div>
        <pre className="whitespace-pre-wrap text-xs">{formatJSON(item.conversation_excerpt ?? [])}</pre>
      </div>
    </div>
  );
}

function setFormField<K extends keyof ModelFormState>(
  key: K,
  value: ModelFormState[K],
  setForm: React.Dispatch<React.SetStateAction<ModelFormState>>,
) {
  setForm((current) => ({ ...current, [key]: value }));
}

function errorMessage(error: unknown) {
  if (error instanceof APIError) {
    return error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "操作失败";
}

function syncTextFeedback(kind: SyncOperationFeedback["kind"], title: string, message: string): SyncOperationFeedback {
  return { kind, title, message };
}

function syncErrorFeedback(error: unknown, fallbackTitle: string): SyncOperationFeedback {
  const base: SyncOperationFeedback = {
    kind: "error",
    title: fallbackTitle,
    message: errorMessage(error),
  };
  if (!(error instanceof APIError) || !error.payload || typeof error.payload !== "object") {
    return base;
  }
  const payload = error.payload as Record<string, unknown>;
  const errorObject = recordValue(payload.error);
  const message = stringFromUnknown(errorObject?.message) || base.message;
  return {
    ...base,
    message,
    code: stringFromUnknown(errorObject?.code),
    stdout: stringFromUnknown(errorObject?.stdout) || stringFromUnknown(payload.stdout),
    stderr: stringFromUnknown(errorObject?.stderr) || stringFromUnknown(payload.stderr),
    exitCode: numberFromUnknown(errorObject?.exit_code) ?? numberFromUnknown(payload.exit_code),
  };
}

function syncFeedbackValue(record: Record<string, unknown>, key: string): SyncOperationFeedback | null {
  const value = recordValue(record[key]);
  if (!value) {
    return null;
  }
  const kind = value.kind === "success" ? "success" : value.kind === "error" ? "error" : null;
  const title = stringFromUnknown(value.title);
  const message = stringFromUnknown(value.message);
  if (!kind || !title || !message) {
    return null;
  }
  return {
    kind,
    title,
    message,
    code: stringFromUnknown(value.code) || undefined,
    stdout: stringFromUnknown(value.stdout) || undefined,
    stderr: stringFromUnknown(value.stderr) || undefined,
    exitCode: numberFromUnknown(value.exitCode),
  };
}

function syncCommitResultValue(record: Record<string, unknown>, key: string): SyncCommitResponse | null {
  const value = recordValue(record[key]);
  if (!value) {
    return null;
  }
  const ok = value.ok === true;
  const hash = stringFromUnknown(value.hash);
  if (!ok || !hash) {
    return null;
  }
  return {
    ok,
    hash,
    stdout: stringFromUnknown(value.stdout),
    stderr: stringFromUnknown(value.stderr),
    exit_code: numberFromUnknown(value.exit_code) ?? 0,
  };
}

function recordValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

function stringFromUnknown(value: unknown) {
  return typeof value === "string" ? value : "";
}

function numberFromUnknown(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

type RuntimeNumberPath =
  | "public_query.direct_min"
  | "public_query.review_min"
  | "public_query.candidate_top_k"
  | "public_query.max_evidence_chars"
  | "answer_log.retention_days"
  | "knowledge.max_text_file_kb";

type RuntimeStringPath =
  | "public_query.router_model_id"
  | "public_query.specialist_model_id"
  | "support.phone"
  | "support.wecom"
  | "sync.remote"
  | "sync.branch";

type RuntimeBoolPath = "public_query.router_enable_thinking" | "public_query.specialist_enable_thinking";

function setRuntimeNumber(settings: AdminRuntimeSettings, path: RuntimeNumberPath, value: number): AdminRuntimeSettings {
  switch (path) {
    case "public_query.direct_min":
      return { ...settings, public_query: { ...settings.public_query, direct_min: value } };
    case "public_query.review_min":
      return { ...settings, public_query: { ...settings.public_query, review_min: value } };
    case "public_query.candidate_top_k":
      return { ...settings, public_query: { ...settings.public_query, candidate_top_k: value } };
    case "public_query.max_evidence_chars":
      return { ...settings, public_query: { ...settings.public_query, max_evidence_chars: value } };
    case "answer_log.retention_days":
      return { ...settings, answer_log: { ...settings.answer_log, retention_days: value } };
    case "knowledge.max_text_file_kb":
      return { ...settings, knowledge: { ...settings.knowledge, max_text_file_kb: value } };
  }
}

function setRuntimeString(settings: AdminRuntimeSettings, path: RuntimeStringPath, value: string): AdminRuntimeSettings {
  switch (path) {
    case "public_query.router_model_id":
      return { ...settings, public_query: { ...settings.public_query, router_model_id: value } };
    case "public_query.specialist_model_id":
      return { ...settings, public_query: { ...settings.public_query, specialist_model_id: value } };
    case "support.phone":
      return { ...settings, support: { ...settings.support, phone: value } };
    case "support.wecom":
      return { ...settings, support: { ...settings.support, wecom: value } };
    case "sync.remote":
      return { ...settings, sync: { ...settings.sync, remote: value } };
    case "sync.branch":
      return { ...settings, sync: { ...settings.sync, branch: value } };
  }
}

function setRuntimeBool(settings: AdminRuntimeSettings, path: RuntimeBoolPath, value: boolean): AdminRuntimeSettings {
  switch (path) {
    case "public_query.router_enable_thinking":
      return { ...settings, public_query: { ...settings.public_query, router_enable_thinking: value } };
    case "public_query.specialist_enable_thinking":
      return { ...settings, public_query: { ...settings.public_query, specialist_enable_thinking: value } };
  }
}

function apiFieldErrors(error: unknown): Record<string, string> {
  if (!(error instanceof APIError) || !error.payload || typeof error.payload !== "object") {
    return {};
  }
  const payload = error.payload as Record<string, unknown>;
  const errorObject = payload.error && typeof payload.error === "object" ? (payload.error as Record<string, unknown>) : null;
  const fields = errorObject?.fields && typeof errorObject.fields === "object" ? (errorObject.fields as Record<string, unknown>) : null;
  if (!fields) {
    return {};
  }
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(fields)) {
    if (typeof value === "string") {
      out[key] = value;
    }
  }
  return out;
}

function normalizeSettingsTab(value: string | null): SettingsTab {
  if (
    value === "models" ||
    value === "public-query" ||
    value === "intents" ||
    value === "logs" ||
    value === "knowledge" ||
    value === "environment"
  ) {
    return value;
  }
  return "models";
}

function normalizeKnowledgeView(value: string | null): KnowledgeView | null {
  if (value === "tasks") {
    return "assistant";
  }
  return value === "browse" || value === "assistant" || value === "sync" ? value : null;
}

function normalizeWikiEditorMode(value: string | null): WikiEditorMode | null {
  return value === "split" || value === "edit" || value === "preview" ? value : null;
}

function useCodeMirrorTheme() {
  const [dark, setDark] = React.useState(false);
  React.useEffect(() => {
    const root = document.documentElement;
    const sync = () => setDark(root.classList.contains("dark"));
    sync();
    const observer = new MutationObserver(sync);
    observer.observe(root, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, []);
  return dark ? oneDark : "light";
}

function wikiEditorExtensions(kind: string): Extension[] {
  switch (kind) {
    case "markdown":
      return [markdownLanguage()];
    case "yaml":
      return [yaml()];
    case "json":
      return [jsonLanguage()];
    case "html":
      return [html()];
    case "css":
      return [css()];
    case "javascript":
      return [javascript({ jsx: true })];
    case "typescript":
      return [javascript({ typescript: true, jsx: true })];
    default:
      return [];
  }
}

function canFormatWikiFile(file: WikiFileResponse) {
  return ["markdown", "yaml", "json", "html", "css", "javascript", "typescript"].includes(file.text_kind);
}

async function formatWikiContent(content: string, kind: string) {
  const prettier = await import("prettier/standalone");
  let parser = "";
  let plugins: Plugin[] = [];
  switch (kind) {
    case "markdown": {
      parser = "markdown";
      const plugin = await import("prettier/plugins/markdown");
      plugins = [plugin.default ?? plugin];
      break;
    }
    case "yaml": {
      parser = "yaml";
      const plugin = await import("prettier/plugins/yaml");
      plugins = [plugin.default ?? plugin];
      break;
    }
    case "json": {
      parser = "json";
      const babel = await import("prettier/plugins/babel");
      const estree = await import("prettier/plugins/estree");
      plugins = [babel.default ?? babel, estree.default ?? estree];
      break;
    }
    case "html": {
      parser = "html";
      const plugin = await import("prettier/plugins/html");
      plugins = [plugin.default ?? plugin];
      break;
    }
    case "css": {
      parser = "css";
      const plugin = await import("prettier/plugins/postcss");
      plugins = [plugin.default ?? plugin];
      break;
    }
    case "javascript": {
      parser = "babel";
      const babel = await import("prettier/plugins/babel");
      const estree = await import("prettier/plugins/estree");
      plugins = [babel.default ?? babel, estree.default ?? estree];
      break;
    }
    case "typescript": {
      parser = "typescript";
      const typescript = await import("prettier/plugins/typescript");
      const estree = await import("prettier/plugins/estree");
      plugins = [typescript.default ?? typescript, estree.default ?? estree];
      break;
    }
    default:
      throw new Error("当前文件类型暂不支持格式化");
  }
  return prettier.format(content, { parser, plugins, printWidth: 100, proseWrap: "preserve" });
}

function readJSON<T>(key: string, fallback: T): T {
  if (typeof window === "undefined") {
    return fallback;
  }
  try {
    const raw = window.localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

function writeJSON(key: string, value: unknown) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {}
}

function stringValue(record: Record<string, unknown>, key: string) {
  const value = record[key];
  return typeof value === "string" ? value : "";
}

function stringArrayValue(record: Record<string, unknown>, key: string) {
  const value = record[key];
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string" && item.trim() !== "") : [];
}

function defaultSyncMessage(status: SyncStatusResponse) {
  const count = status.files.length;
  return count > 0 ? `更新 Wiki 内容（${count} 个文件）` : "同步 Wiki 内容";
}

function wikiItemsToTreeNodes(items: WikiTreeItem[]): FileTreeNode[] {
  return items
    .slice()
    .sort((a, b) => {
      if (a.is_dir !== b.is_dir) {
        return a.is_dir ? -1 : 1;
      }
      return a.name.localeCompare(b.name, "zh-Hans-CN");
    })
    .map((item) => ({
      id: item.path || item.name,
      name: item.name,
      path: item.path,
      isDirectory: item.is_dir,
      preview: item.preview,
      expanded: false,
      loading: false,
      children: item.is_dir ? undefined : [],
    }));
}

function upsertDirectoryChildren(nodes: FileTreeNode[], path: string, children: FileTreeNode[], expanded: boolean): FileTreeNode[] {
  if (!path) {
    return children;
  }
  return nodes.map((node) => {
    if (node.path === path && node.isDirectory) {
      return { ...node, children, expanded, loading: false };
    }
    if (node.children?.length) {
      return { ...node, children: upsertDirectoryChildren(node.children, path, children, expanded) };
    }
    return node;
  });
}

function setDirectoryExpanded(nodes: FileTreeNode[], path: string, expanded: boolean): FileTreeNode[] {
  return nodes.map((node) => {
    if (node.path === path && node.isDirectory) {
      return { ...node, expanded };
    }
    if (node.children?.length) {
      return { ...node, children: setDirectoryExpanded(node.children, path, expanded) };
    }
    return node;
  });
}

function setDirectoryLoading(nodes: FileTreeNode[], path: string, loading: boolean): FileTreeNode[] {
  return nodes.map((node) => {
    if (node.path === path && node.isDirectory) {
      return { ...node, expanded: true, loading };
    }
    if (node.children?.length) {
      return { ...node, children: setDirectoryLoading(node.children, path, loading) };
    }
    return node;
  });
}

function parentWikiPath(value: string) {
  return value.split("/").filter(Boolean).slice(0, -1).join("/");
}

function formatDate(value: string) {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) {
    return value;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(timestamp);
}
