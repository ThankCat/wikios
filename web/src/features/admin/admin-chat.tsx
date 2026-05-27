"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  Bot,
  CheckCircle2,
  ClipboardCheck,
  Database,
  Download,
  FileText,
  GitBranch,
  GitMerge,
  PanelLeft,
  PanelLeftClose,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Save,
  SendHorizontal,
  Sparkles,
  Square,
  Trash2,
  Upload,
  Wrench,
  X,
  XCircle,
} from "lucide-react";

import { ChatDetailDrawer } from "@/components/chat/chat-detail-drawer";
import {
  ConversationSidebar,
  type ConversationItem,
} from "@/components/chat/conversation-sidebar";
import { MessageCard } from "@/components/chat/message-card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ScrollJumpControls } from "@/components/ui/scroll-jump-controls";
import { Textarea } from "@/components/ui/textarea";
import { api, APIError, isAbortError } from "@/lib/api";
import { createId } from "@/lib/id";
import { useScrollFollow } from "@/lib/use-scroll-follow";
import { cn } from "@/lib/utils";
import type {
  AdminChatRequest,
  AdminChatResponse,
  AdminStreamEvent,
  ContextUsage,
  LLMModel,
  PublicIntentsStatus,
  ReviewItem,
  ReviewTarget,
  SyncCommitResponse,
  SyncStatusResponse,
} from "@/types/api";

type MessageStatus = "pending" | "streaming" | "done" | "error" | "cancelled";

type AdminMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  created_at?: string;
  status?: MessageStatus;
  details?: unknown;
};

type AdminSessionState = {
  uploadedPaths: string[];
  lastReply: string;
  lastSummary: string;
  lastMode: string;
  lastReportFile: string;
  lastOutputFiles: string[];
  lastCommands: string[];
  lastArtifacts: string[];
};

type AdminConversation = {
  id: string;
  title: string;
  messages: AdminMessage[];
  stream: boolean;
  lastMode: string;
  sessionState: AdminSessionState;
};

type AdminChatRuntime = {
  storageKey: string;
  initialized: boolean;
  conversations: AdminConversation[];
  activeId: string;
  requestLabels: Record<string, string>;
  controllers: Record<string, AbortController>;
  locks: Record<string, boolean>;
  listeners: Set<() => void>;
  persistTimer?: number;
};

type LLMModelFormState = {
  id: string;
  display_name: string;
  provider: string;
  base_url: string;
  model_name: string;
  api_key: string;
  timeout_sec: string;
  admin_timeout_sec: string;
};

type AdminChatProps = {
  username: string;
  embedded?: boolean;
  title?: string;
  subtitle?: string;
  sidebarTitle?: string;
  sidebarSubtitle?: string;
  storageKey?: string;
  sidebarStorageKey?: string;
  showAdminShortcuts?: boolean;
  showKnowledgeTasks?: boolean;
  onKnowledgeChanged?: () => void;
};

const defaultStorageKey = "wikios.admin.chat";
const defaultSidebarStorageKey = "wikios.admin.sidebar.open";
const adminChatRuntimes = new Map<string, AdminChatRuntime>();

type KnowledgeTaskAction = {
  id: string;
  label: string;
  title: string;
  mode: string;
  message: string;
  icon: typeof Activity;
};

const knowledgeTaskActions: KnowledgeTaskAction[] = [
  {
    id: "lint",
    label: "健康检查",
    title: "按 Wiki 的 LINT 规则执行健康检查",
    mode: "lint",
    message: "执行一次健康检查，并在回答里说明发现的问题、执行过的动作和建议的下一步。",
    icon: Activity,
  },
  {
    id: "reflect",
    label: "综合分析",
    title: "让 LLM 按 Wiki 的 REFLECT 规则做综合分析",
    mode: "reflect",
    message: "请做一次综合反思分析，聚焦知识库结构、缺口、重复内容和可维护性。",
    icon: Sparkles,
  },
  {
    id: "repair",
    label: "修复问题",
    title: "让 LLM 按 Wiki 的 REPAIR 规则修复低风险问题",
    mode: "repair",
    message: "请尝试自动修复当前知识库中的低风险问题，并列出你实际修改或建议修改的内容。",
    icon: Wrench,
  },
  {
    id: "merge",
    label: "合并冲突",
    title: "让 LLM 按 Wiki 的 MERGE 规则提出合并方案，不自动合并",
    mode: "merge",
    message: "请根据 MERGE 操作规范检查当前知识库中的可合并、重复或冲突内容，只给出合并方案，不要自动执行合并。",
    icon: GitMerge,
  },
];

function getAdminChatRuntime(storageKey: string): AdminChatRuntime {
  const existing = adminChatRuntimes.get(storageKey);
  if (existing) {
    return existing;
  }
  const runtime: AdminChatRuntime = {
    storageKey,
    initialized: false,
    conversations: [],
    activeId: "",
    requestLabels: {},
    controllers: {},
    locks: {},
    listeners: new Set(),
  };
  adminChatRuntimes.set(storageKey, runtime);
  return runtime;
}

function notifyAdminChatRuntime(runtime: AdminChatRuntime) {
  for (const listener of runtime.listeners) {
    listener();
  }
}

function scheduleAdminChatPersist(runtime: AdminChatRuntime) {
  if (typeof window === "undefined" || runtime.conversations.length === 0) {
    return;
  }
  if (runtime.persistTimer) {
    window.clearTimeout(runtime.persistTimer);
  }
  runtime.persistTimer = window.setTimeout(() => {
    localStorage.setItem(runtime.storageKey, JSON.stringify(runtime.conversations));
    runtime.persistTimer = undefined;
  }, 80);
}

function emptyAdminSessionState(): AdminSessionState {
  return {
    uploadedPaths: [],
    lastReply: "",
    lastSummary: "",
    lastMode: "query",
    lastReportFile: "",
    lastOutputFiles: [],
    lastCommands: [],
    lastArtifacts: [],
  };
}

function emptyLLMModelForm(): LLMModelFormState {
  return {
    id: "",
    display_name: "",
    provider: "openai-compatible",
    base_url: "",
    model_name: "",
    api_key: "",
    timeout_sec: "90",
    admin_timeout_sec: "300",
  };
}

function normalizeAdminSessionState(value: unknown): AdminSessionState {
  const state = asRecord(value);
  const fallback = emptyAdminSessionState();
  return {
    uploadedPaths: stringArrayValue(state, "uploadedPaths"),
    lastReply: stringValue(state, "lastReply"),
    lastSummary: stringValue(state, "lastSummary"),
    lastMode: firstNonEmpty(stringValue(state, "lastMode"), fallback.lastMode),
    lastReportFile: stringValue(state, "lastReportFile"),
    lastOutputFiles: stringArrayValue(state, "lastOutputFiles"),
    lastCommands: stringArrayValue(state, "lastCommands"),
    lastArtifacts: stringArrayValue(state, "lastArtifacts"),
  };
}

function normalizeAdminConversation(value: unknown): AdminConversation | null {
  const conversation = asRecord(value);
  const id = stringValue(conversation, "id").trim();
  if (id === "") {
    return null;
  }
  const messages: AdminMessage[] = Array.isArray(conversation.messages)
    ? conversation.messages.reduce<AdminMessage[]>((acc, message) => {
        const item = asRecord(message);
        const messageId = stringValue(item, "id").trim();
        if (messageId === "") {
          return acc;
        }
        const role: AdminMessage["role"] =
          stringValue(item, "role") === "assistant" ? "assistant" : "user";
        acc.push({
          id: messageId,
          role,
          content: stringValue(item, "content"),
          created_at: firstNonEmpty(stringValue(item, "created_at"), new Date().toISOString()),
          details: item.details,
        });
        return acc;
      }, [])
    : [];
  return {
    id,
    title: firstNonEmpty(stringValue(conversation, "title"), "知识库会话"),
    messages,
    stream:
      typeof conversation.stream === "boolean" ? conversation.stream : true,
    lastMode: firstNonEmpty(stringValue(conversation, "lastMode"), "query"),
    sessionState: normalizeAdminSessionState(conversation.sessionState),
  };
}

export function AdminChat({
  username,
  embedded = false,
  title = "知识库助手",
  subtitle = "围绕知识库进行检索、分析、修复和沉淀。",
  sidebarTitle = "知识库会话",
  sidebarSubtitle,
  storageKey = defaultStorageKey,
  sidebarStorageKey = defaultSidebarStorageKey,
  showAdminShortcuts = true,
  showKnowledgeTasks = false,
  onKnowledgeChanged,
}: AdminChatProps) {
  const runtime = getAdminChatRuntime(storageKey);
  const [conversations, setConversationsSnapshot] = useState<AdminConversation[]>(() => runtime.conversations);
  const [activeId, setActiveIdSnapshot] = useState(() => runtime.activeId);
  const [composer, setComposer] = useState("");
  const [error, setError] = useState("");
  const [selectedDetailId, setSelectedDetailId] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const composerRef = useRef<HTMLTextAreaElement | null>(null);
  const uploadInputRef = useRef<HTMLInputElement | null>(null);
  const typingTimersRef = useRef<Record<string, number>>({});
  const [requestLabels, setRequestLabelsSnapshot] = useState<Record<string, string>>(() => runtime.requestLabels);
  const [intentEditorOpen, setIntentEditorOpen] = useState(false);
  const [intentSource, setIntentSource] = useState("");
  const [intentStatus, setIntentStatus] = useState<PublicIntentsStatus | null>(
    null,
  );
  const [intentLoading, setIntentLoading] = useState(false);
  const [intentSaving, setIntentSaving] = useState(false);
  const [intentMessage, setIntentMessage] = useState("");
  const [contextUsage, setContextUsage] = useState<ContextUsage | null>(null);
  const [contextLoading, setContextLoading] = useState(false);
  const [syncOpen, setSyncOpen] = useState(false);
  const [syncStatus, setSyncStatus] = useState<SyncStatusResponse | null>(null);
  const [selectedSyncPaths, setSelectedSyncPaths] = useState<string[]>([]);
  const [syncMessage, setSyncMessage] = useState("");
  const [syncBusy, setSyncBusy] = useState(false);
  const [syncMessageBusy, setSyncMessageBusy] = useState(false);
  const [syncMessageRule, setSyncMessageRule] = useState("");
  const [syncResult, setSyncResult] = useState<SyncCommitResponse | null>(null);
  const [syncError, setSyncError] = useState("");
  const [reviewCount, setReviewCount] = useState(0);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [reviewLoading, setReviewLoading] = useState(false);
  const [reviewBusy, setReviewBusy] = useState(false);
  const [reviewItem, setReviewItem] = useState<ReviewItem | null>(null);
  const [reviewTargets, setReviewTargets] = useState<ReviewTarget[]>([]);
  const [reviewQuestion, setReviewQuestion] = useState("");
  const [reviewAnswer, setReviewAnswer] = useState("");
  const [reviewTargetPath, setReviewTargetPath] = useState("");
  const [reviewRejectReason, setReviewRejectReason] = useState("");
  const [reviewMessage, setReviewMessage] = useState("");
  const [modelOpen, setModelOpen] = useState(false);
  const [models, setModels] = useState<LLMModel[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelBusy, setModelBusy] = useState(false);
  const [modelTestingId, setModelTestingId] = useState<string | null>(null);
  const [modelMessage, setModelMessage] = useState("");
  const [modelForm, setModelForm] = useState<LLMModelFormState>(() => emptyLLMModelForm());

  function syncRuntimeSnapshot() {
    setConversationsSnapshot(runtime.conversations);
    setActiveIdSnapshot(runtime.activeId);
    setRequestLabelsSnapshot({ ...runtime.requestLabels });
  }

  function setConversations(
    updater: AdminConversation[] | ((current: AdminConversation[]) => AdminConversation[]),
  ) {
    runtime.conversations = typeof updater === "function" ? updater(runtime.conversations) : updater;
    scheduleAdminChatPersist(runtime);
    notifyAdminChatRuntime(runtime);
  }

  function setActiveId(updater: string | ((current: string) => string)) {
    runtime.activeId = typeof updater === "function" ? updater(runtime.activeId) : updater;
    notifyAdminChatRuntime(runtime);
  }

  function setRequestLabels(
    updater: Record<string, string> | ((current: Record<string, string>) => Record<string, string>),
  ) {
    runtime.requestLabels = typeof updater === "function" ? updater(runtime.requestLabels) : updater;
    notifyAdminChatRuntime(runtime);
  }

  useEffect(() => {
    runtime.listeners.add(syncRuntimeSnapshot);
    syncRuntimeSnapshot();
    return () => {
      runtime.listeners.delete(syncRuntimeSnapshot);
    };
  }, [runtime]);

  useEffect(() => {
    if (runtime.initialized) {
      syncRuntimeSnapshot();
      return;
    }
    const raw = localStorage.getItem(storageKey);
    if (raw) {
      try {
        const parsed = JSON.parse(raw) as unknown;
        const normalized = Array.isArray(parsed)
          ? parsed
              .map((item) => normalizeAdminConversation(item))
              .filter((item): item is AdminConversation => item !== null)
          : [];
        if (normalized.length > 0) {
          runtime.conversations = normalized;
          runtime.activeId = normalized[0].id;
          runtime.initialized = true;
          notifyAdminChatRuntime(runtime);
          return;
        }
      } catch {}
    }
    const initial = createConversation("知识库会话");
    runtime.conversations = [initial];
    runtime.activeId = initial.id;
    runtime.initialized = true;
    scheduleAdminChatPersist(runtime);
    notifyAdminChatRuntime(runtime);
  }, [runtime, storageKey]);

  useEffect(() => {
    const raw = localStorage.getItem(sidebarStorageKey);
    if (raw === "0") {
      setSidebarOpen(false);
    }
  }, [sidebarStorageKey]);

  useEffect(() => {
    if (conversations.length === 0) {
      return;
    }
    const timer = window.setTimeout(() => {
      localStorage.setItem(storageKey, JSON.stringify(conversations));
    }, 180);
    return () => window.clearTimeout(timer);
  }, [conversations, storageKey]);

  useEffect(() => {
    const element = composerRef.current;
    if (!element) {
      return;
    }
    const maxHeight = embedded ? 136 : 180;
    element.style.height = "0px";
    const nextHeight = Math.min(maxHeight, Math.max(embedded ? 38 : 48, element.scrollHeight));
    element.style.height = `${nextHeight}px`;
    element.style.overflowY = element.scrollHeight > maxHeight ? "auto" : "hidden";
  }, [composer, embedded]);

  useEffect(() => {
    localStorage.setItem(sidebarStorageKey, sidebarOpen ? "1" : "0");
  }, [sidebarOpen, sidebarStorageKey]);

  const activeConversation = useMemo(
    () =>
      conversations.find((item) => item.id === activeId) ?? conversations[0],
    [activeId, conversations],
  );
  const busyLabel = activeConversation ? (requestLabels[activeConversation.id] ?? "") : "";
  const busy = busyLabel !== "";
  const activeModel = useMemo(
    () => models.find((model) => model.is_active) ?? null,
    [models],
  );
  const forceStream = showKnowledgeTasks;
  const selectedDetail = useMemo(
    () =>
      activeConversation?.messages.find(
        (message) => message.id === selectedDetailId && message.details,
      ) ?? null,
    [activeConversation, selectedDetailId],
  );
  const chatScroll = useScrollFollow<HTMLDivElement>([
    activeId,
    activeConversation?.messages,
  ]);
  const contextEstimateKey = useMemo(
    () =>
      activeConversation
        ? [
            activeConversation.id,
            activeConversation.messages
              .map(
                (message) =>
                  `${message.role}:${message.status ?? ""}:${message.content.length}`,
              )
              .join("|"),
            activeConversation.lastMode,
            composer,
          ].join("::")
        : "",
    [activeConversation, composer],
  );

  useEffect(() => {
    chatScroll.scrollToBottom("auto");
  }, [activeId, chatScroll.scrollToBottom]);

  useEffect(() => {
    if (!activeConversation) {
      setContextUsage(null);
      return;
    }
    const message = composer.trim();
    const hasHistory = activeConversation.messages.some(
      (item) => item.content.trim() !== "",
    );
    if (!hasHistory && message === "") {
      setContextUsage(null);
      setContextLoading(false);
      return;
    }
    if (busy) {
      setContextLoading(false);
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      const request = buildAdminRequest(activeConversation, message, {});
      setContextLoading(true);
      void api
        .estimateAdminContext(request, controller.signal)
        .then((response) => setContextUsage(response.context_usage))
        .catch(() => {
          if (!controller.signal.aborted) {
            setContextUsage(null);
          }
        })
        .finally(() => {
          if (!controller.signal.aborted) {
            setContextLoading(false);
          }
        });
    }, 350);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [activeConversation, busy, composer, contextEstimateKey]);

  useEffect(() => {
    void refreshReviewCount();
    const timer = window.setInterval(() => {
      void refreshReviewCount();
    }, 30000);
    return () => window.clearInterval(timer);
  }, []);

  function startConversationRequest(conversationId: string, controller: AbortController, label: string) {
    runtime.controllers[conversationId] = controller;
    setRequestLabels((current) => ({ ...current, [conversationId]: label }));
  }

  function finishConversationRequest(conversationId: string, controller?: AbortController) {
    if (controller && runtime.controllers[conversationId] !== controller) {
      return;
    }
    delete runtime.controllers[conversationId];
    setRequestLabels((current) => {
      if (!current[conversationId]) {
        return current;
      }
      const next = { ...current };
      delete next[conversationId];
      return next;
    });
  }

  async function send(
    messageOverride?: string,
    overrides?: Partial<AdminChatRequest>,
  ) {
    const text = (messageOverride ?? composer).trim();
    if (!activeConversation || !text || busy) {
      return;
    }
    if (contextUsage?.blocked) {
      setError("当前对话已接近上下文上限，请创建新的对话继续。");
      return;
    }
    const conversationId = activeConversation.id;
    if (runtime.locks[conversationId]) {
      return;
    }
    runtime.locks[conversationId] = true;
    try {
      const stream = forceStream ? true : (overrides?.stream ?? activeConversation.stream);
      const request = buildAdminRequest(activeConversation, text, {
        ...overrides,
        stream,
      });
      const userMessage: AdminMessage = {
        id: createId(),
        role: "user",
        content: text,
        created_at: new Date().toISOString(),
      };
      appendMessage(conversationId, userMessage);
      setComposer("");
      setError("");
      const controller = new AbortController();
      startConversationRequest(conversationId, controller, stream ? "正在执行知识库会话..." : "正在处理知识库请求...");
      if (stream) {
        const assistantId = createId();
        appendMessage(conversationId, {
          id: assistantId,
          role: "assistant",
          content: "",
          created_at: new Date().toISOString(),
          status: "streaming",
          details: { prompts: [], steps: [] },
        });
        try {
          await api.adminChatStream(
            request,
            (event) =>
              handleStreamEvent(conversationId, assistantId, event),
            controller.signal,
          );
          renameConversation(conversationId, text);
        } catch (reason) {
          if (isAbortError(reason)) {
            patchMessage(conversationId, assistantId, {
              content: (prev) => prev || "已停止当前会话。",
              status: "cancelled",
            });
          } else {
            setError(reason instanceof Error ? reason.message : "请求失败");
            patchMessage(conversationId, assistantId, {
              content: "执行失败，请稍后重试。",
              status: "error",
            });
          }
        } finally {
          finishConversationRequest(conversationId, controller);
        }
        return;
      }
      const assistantId = createId();
      appendMessage(conversationId, {
        id: assistantId,
        role: "assistant",
        content: "",
        created_at: new Date().toISOString(),
        status: "pending",
        details: { steps: [] },
      });
      try {
        const response = await api.adminChat(request, controller.signal);
        applySessionStatePatch(
          conversationId,
          response.mode,
          response.reply,
          response.details,
          response.execution,
        );
        patchMessage(conversationId, assistantId, {
          content: response.reply,
          status: "done",
          details: {
            result: response.details,
            execution: response.execution,
            steps: response.execution?.steps ?? [],
          },
        });
        updateLastMode(conversationId, response.mode);
        renameConversation(conversationId, text);
      } catch (reason) {
        if (isAbortError(reason)) {
          patchMessage(conversationId, assistantId, {
            content: "已取消本次管理员请求。",
            status: "cancelled",
          });
        } else {
          setError(reason instanceof Error ? reason.message : "请求失败");
          patchMessage(conversationId, assistantId, {
            content: "执行失败，请稍后重试。",
            status: "error",
          });
        }
      } finally {
        finishConversationRequest(conversationId, controller);
      }
    } finally {
      delete runtime.locks[conversationId];
    }
  }

  async function runKnowledgeTask(action: KnowledgeTaskAction) {
    if (busy) {
      return;
    }
    await send(action.message, {
      mode_hint: action.mode,
      stream: true,
      context: {
        task_kind: action.id,
        task_label: action.label,
        source: "knowledge_assistant",
      },
    });
  }

  async function uploadKnowledgeFile(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file || !activeConversation || busy) {
      return;
    }
    const conversationId = activeConversation.id;
    if (runtime.locks[conversationId]) {
      return;
    }
    runtime.locks[conversationId] = true;
    const userText = `上传并摄入：${file.name}`;
    const userMessage: AdminMessage = {
      id: createId(),
      role: "user",
      content: userText,
      created_at: new Date().toISOString(),
    };
    const assistantId = createId();
    appendMessage(conversationId, userMessage);
    appendMessage(conversationId, {
      id: assistantId,
      role: "assistant",
      content: "",
      created_at: new Date().toISOString(),
      status: "streaming",
      details: {
        prompts: [],
        steps: [
          {
            name: "准备上传文件",
            tool: "upload",
            status: "SUCCESS",
            input: {
              file_name: file.name,
              size: file.size,
              type: file.type || "unknown",
            },
          },
        ],
      },
    });
    setComposer("");
    setError("");
    const controller = new AbortController();
    startConversationRequest(conversationId, controller, "正在上传并摄入知识库...");
    try {
      await api.uploadStream(
        file,
        (event) => handleUploadStreamEvent(conversationId, assistantId, event),
        controller.signal,
      );
      renameConversation(conversationId, userText);
      onKnowledgeChanged?.();
    } catch (reason) {
      if (isAbortError(reason)) {
        patchMessage(conversationId, assistantId, {
          content: (prev) => prev || "已停止上传摄入任务。",
          status: "cancelled",
        });
      } else {
        const message = reason instanceof Error ? reason.message : "上传摄入失败";
        setError(message);
        patchMessage(conversationId, assistantId, {
          content: `上传摄入失败：${message}`,
          status: "error",
        });
        mergeDetails(conversationId, assistantId, { error: { message } });
      }
    } finally {
      finishConversationRequest(conversationId, controller);
      delete runtime.locks[conversationId];
      if (uploadInputRef.current) {
        uploadInputRef.current.value = "";
      }
    }
  }

  function stopActiveRequest() {
    if (!activeConversation) {
      return;
    }
    const controller = runtime.controllers[activeConversation.id];
    controller?.abort();
    finishConversationRequest(activeConversation.id, controller);
  }

  async function refreshReviewCount() {
    try {
      const response = await api.reviewCount();
      setReviewCount(response.pending_count);
    } catch {
      setReviewCount(0);
    }
  }

  async function openModelModal() {
    setModelOpen(true);
    setModelMessage("");
    setModelTestingId(null);
    setModelForm(emptyLLMModelForm());
    await refreshModels();
  }

  async function refreshModels() {
    setModelsLoading(true);
    try {
      const response = await api.listLLMModels();
      setModels(response.models);
    } catch (reason) {
      setModelMessage(reason instanceof Error ? reason.message : "模型列表读取失败");
    } finally {
      setModelsLoading(false);
    }
  }

  function editModel(model: LLMModel) {
    setModelMessage("");
    setModelForm({
      id: model.id,
      display_name: model.display_name,
      provider: model.provider || "openai-compatible",
      base_url: model.base_url,
      model_name: model.model_name,
      api_key: "",
      timeout_sec: String(model.timeout_sec || 90),
      admin_timeout_sec: String(model.admin_timeout_sec || 300),
    });
  }

  async function saveModel() {
    setModelBusy(true);
    setModelMessage("");
    const payload = {
      display_name: modelForm.display_name.trim(),
      provider: firstNonEmpty(modelForm.provider.trim(), "openai-compatible"),
      base_url: modelForm.base_url.trim(),
      model_name: modelForm.model_name.trim(),
      api_key: modelForm.api_key.trim(),
      timeout_sec: positiveNumber(modelForm.timeout_sec, 90),
      admin_timeout_sec: positiveNumber(modelForm.admin_timeout_sec, 300),
    };
    try {
      if (modelForm.id) {
        await api.updateLLMModel(modelForm.id, payload);
        setModelMessage("模型已更新");
      } else {
        await api.createLLMModel(payload);
        setModelMessage("模型已新增");
      }
      setModelForm(emptyLLMModelForm());
      await refreshModels();
    } catch (reason) {
      setModelMessage(reason instanceof Error ? reason.message : "模型保存失败");
    } finally {
      setModelBusy(false);
    }
  }

  async function activateModel(id: string) {
    setModelBusy(true);
    setModelMessage("");
    try {
      await api.activateLLMModel(id);
      setModelMessage("当前模型已切换");
      await refreshModels();
    } catch (reason) {
      setModelMessage(reason instanceof Error ? reason.message : "模型切换失败");
    } finally {
      setModelBusy(false);
    }
  }

  async function testModel(id: string) {
    setModelTestingId(id);
    setModelMessage("");
    try {
      const response = await api.testLLMModel(id);
      const latency = Number.isFinite(response.latency_ms) ? `（${response.latency_ms}ms）` : "";
      setModelMessage(response.ok ? `连接成功${latency}` : `连接失败：${response.message}`);
    } catch (reason) {
      setModelMessage(reason instanceof Error ? reason.message : "模型连接测试失败");
    } finally {
      setModelTestingId(null);
    }
  }

  async function deleteModel(id: string) {
    if (!window.confirm("确认删除这个模型吗？如果它是当前模型，删除后需要重新启用一个模型。")) {
      return;
    }
    setModelBusy(true);
    setModelMessage("");
    try {
      await api.deleteLLMModel(id);
      setModelMessage("模型已删除");
      if (modelForm.id === id) {
        setModelForm(emptyLLMModelForm());
      }
      await refreshModels();
    } catch (reason) {
      setModelMessage(reason instanceof Error ? reason.message : "模型删除失败");
    } finally {
      setModelBusy(false);
    }
  }

  async function openReviewModal() {
    setReviewOpen(true);
    await loadReviewItem();
  }

  async function loadReviewItem(cursor?: string) {
    setReviewLoading(true);
    setReviewMessage("");
    try {
      const response = await api.reviewNext(cursor);
      setReviewCount(response.pending_count);
      setReviewTargets(response.target_paths);
      if (response.item) {
        setReviewItem(response.item);
        setReviewQuestion(response.item.question);
        setReviewAnswer(response.item.draft_answer);
        setReviewTargetPath(
          response.item.suggested_target_path ||
            response.target_paths[0]?.path ||
            "",
        );
        setReviewRejectReason("");
      } else {
        setReviewItem(null);
        setReviewQuestion("");
        setReviewAnswer("");
        setReviewTargetPath(response.target_paths[0]?.path || "");
        setReviewRejectReason("");
        setReviewMessage("暂无待审查问题。");
      }
    } catch (reason) {
      setReviewMessage(reason instanceof Error ? reason.message : "读取审查队列失败");
    } finally {
      setReviewLoading(false);
    }
  }

  async function approveReviewItem() {
    if (!reviewItem) {
      return;
    }
    if (reviewAnswer.trim() === "" || reviewTargetPath.trim() === "") {
      setReviewMessage("请填写回答并选择目标知识页。");
      return;
    }
    setReviewBusy(true);
    setReviewMessage("");
    try {
      const response = await api.reviewApprove(reviewItem.id, {
        question: reviewQuestion.trim(),
        answer: reviewAnswer.trim(),
        target_path: reviewTargetPath.trim(),
      });
      setReviewCount(response.pending_count);
      await loadReviewItem();
      setReviewMessage("已记录到知识库。后续可以在同步窗口提交并推送变更。");
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : "通过失败";
      setReviewMessage(
        message.includes("qmd update") && !message.includes("知识页已回滚")
          ? `通过失败：知识页已回滚，qmd update 失败，请修复 qmd 后重试。${message}`
          : message,
      );
    } finally {
      setReviewBusy(false);
    }
  }

  async function rejectReviewItem() {
    if (!reviewItem) {
      return;
    }
    setReviewBusy(true);
    setReviewMessage("");
    try {
      const response = await api.reviewReject(reviewItem.id, {
        reason: reviewRejectReason.trim() || "管理员驳回",
      });
      setReviewCount(response.pending_count);
      await loadReviewItem();
      setReviewMessage("已记录到禁答列表。相似问题后续将不会回复。");
    } catch (reason) {
      setReviewMessage(reason instanceof Error ? reason.message : "驳回失败");
    } finally {
      setReviewBusy(false);
    }
  }

  async function deleteReviewItem() {
    if (!reviewItem) {
      return;
    }
    const confirmed = window.confirm("确认从待审队列删除这条问题吗？删除后不会写入知识库或禁答列表。");
    if (!confirmed) {
      return;
    }
    setReviewBusy(true);
    setReviewMessage("");
    try {
      const response = await api.reviewDelete(reviewItem.id);
      setReviewCount(response.pending_count);
      await loadReviewItem();
      setReviewMessage("已从待审队列删除。不会写入知识库或禁答列表。");
    } catch (reason) {
      setReviewMessage(reason instanceof Error ? reason.message : "删除失败");
    } finally {
      setReviewBusy(false);
    }
  }

  async function openIntentEditor() {
    setIntentEditorOpen(true);
    if (intentSource.trim() !== "") {
      return;
    }
    setIntentLoading(true);
    setIntentMessage("");
    try {
      const response = await api.getPublicIntents();
      setIntentSource(response.source);
      setIntentStatus(response.status);
    } catch (reason) {
      setIntentMessage(
        reason instanceof Error ? reason.message : "读取前置话术失败",
      );
    } finally {
      setIntentLoading(false);
    }
  }

  function closeIntentEditor() {
    setIntentEditorOpen(false);
  }

  async function reloadIntentSource() {
    setIntentLoading(true);
    setIntentMessage("");
    try {
      const response = await api.getPublicIntents();
      setIntentSource(response.source);
      setIntentStatus(response.status);
      setIntentMessage("已重新读取当前配置。");
    } catch (reason) {
      setIntentMessage(
        reason instanceof Error ? reason.message : "读取前置话术失败",
      );
    } finally {
      setIntentLoading(false);
    }
  }

  async function saveIntentSource() {
    setIntentSaving(true);
    setIntentMessage("");
    try {
      const response = await api.updatePublicIntents(intentSource);
      setIntentSource(response.source);
      setIntentStatus(response.status);
      const warningText = response.status.warnings?.length
        ? `，警告：${response.status.warnings.join("；")}`
        : "";
      setIntentMessage(`保存成功，已替换内存缓存${warningText}`);
    } catch (reason) {
      if (reason instanceof APIError) {
        const payload = asRecord(reason.payload);
        const errorObject = asRecord(payload.error);
        setIntentMessage(String(errorObject.message ?? reason.message));
      } else {
        setIntentMessage(
          reason instanceof Error ? reason.message : "保存前置话术失败",
        );
      }
    } finally {
      setIntentSaving(false);
    }
  }

  async function openSyncModal() {
    setSyncOpen(true);
    setSyncBusy(true);
    setSyncError("");
    setSyncResult(null);
    try {
      const response = await api.syncStatus();
      setSyncStatus(response);
      setSelectedSyncPaths(
        response.files
          .filter((file) => file.default_on)
          .map((file) => file.path),
      );
      setSyncMessage(defaultSyncMessage(response));
      setSyncMessageRule("");
    } catch (reason) {
      setSyncError(
        reason instanceof Error ? reason.message : "读取同步状态失败",
      );
    } finally {
      setSyncBusy(false);
    }
  }

  async function refreshSyncStatus() {
    setSyncBusy(true);
    setSyncError("");
    try {
      const response = await api.syncStatus();
      setSyncStatus(response);
      setSelectedSyncPaths((current) =>
        current.filter((path) =>
          response.files.some((file) => file.path === path),
        ),
      );
      if (syncMessage.trim() === "") {
        setSyncMessage(defaultSyncMessage(response));
      }
    } catch (reason) {
      setSyncError(
        reason instanceof Error ? reason.message : "刷新同步状态失败",
      );
    } finally {
      setSyncBusy(false);
    }
  }

  async function commitSyncFiles() {
    if (selectedSyncPaths.length === 0 || syncMessage.trim() === "") {
      setSyncError("请选择文件并填写提交信息。");
      return;
    }
    setSyncBusy(true);
    setSyncError("");
    try {
      const response = await api.syncCommit(
        selectedSyncPaths,
        syncMessage.trim(),
      );
      setSyncResult(response);
      await refreshSyncStatus();
    } catch (reason) {
      setSyncError(reason instanceof Error ? reason.message : "提交失败");
    } finally {
      setSyncBusy(false);
    }
  }

  async function generateSyncMessage() {
    if (selectedSyncPaths.length === 0) {
      setSyncError("请先选择要提交的文件。");
      return;
    }
    setSyncMessageBusy(true);
    setSyncError("");
    try {
      const response = await api.syncGenerateMessage(selectedSyncPaths);
      setSyncMessage(response.message);
      setSyncMessageRule(response.rule);
    } catch (reason) {
      setSyncError(reason instanceof Error ? reason.message : "生成提交信息失败");
    } finally {
      setSyncMessageBusy(false);
    }
  }

  async function pushSyncCommit() {
    if (!syncStatus) {
      return;
    }
    if (!syncStatus.can_push || syncStatus.push_count <= 0) {
      setSyncError("当前没有未推送的提交，请先选择文件并提交。");
      return;
    }
    if (
      !window.confirm(
        `确认推送到 ${syncStatus.remote}/${syncStatus.branch || "main"}？`,
      )
    ) {
      return;
    }
    setSyncBusy(true);
    setSyncError("");
    try {
      await api.syncPush(syncStatus.remote, syncStatus.branch || "main");
      await refreshSyncStatus();
      setSyncError("推送完成。");
    } catch (reason) {
      setSyncError(reason instanceof Error ? reason.message : "推送失败");
    } finally {
      setSyncBusy(false);
    }
  }

  function toggleSyncPath(path: string) {
    setSelectedSyncPaths((current) =>
      current.includes(path)
        ? current.filter((item) => item !== path)
        : [...current, path],
    );
  }

  function handleStreamEvent(
    conversationId: string,
    assistantId: string,
    event: AdminStreamEvent,
  ) {
    if (event.type === "meta") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      updateLastMode(conversationId, String(data.mode ?? "query"));
      const usage = asRecord(data.context_usage);
      if (Object.keys(usage).length > 0) {
        setContextUsage(usage as ContextUsage);
      }
      mergeDetails(conversationId, assistantId, {
        execution: {
          id: data.execution_id,
          kind: data.mode,
          status: "RUNNING",
          started_at: data.started_at,
        },
      });
      return;
    }
    if (event.type === "prompt") {
      appendEventDetail(
        conversationId,
        assistantId,
        "prompts",
        summarizePromptEvent(event.data),
        8,
      );
      return;
    }
    if (event.type === "result") {
      const data = event.data as AdminChatResponse;
      if (data.context_usage) {
        setContextUsage(data.context_usage);
      }
      applySessionStatePatch(
        conversationId,
        data.mode,
        data.reply,
        data.details,
        data.execution,
      );
      patchMessage(conversationId, assistantId, {
        content: "",
        status: "done",
      });
      mergeDetails(conversationId, assistantId, {
        response: data,
        result: data.details,
        execution: data.execution,
        steps: data.execution?.steps ?? [],
      });
      void animateAssistantReply(conversationId, assistantId, data.reply);
      return;
    }
    if (event.type === "error") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, assistantId, {
        content: `执行失败：${String(data.message ?? "未知错误")}`,
        status: "error",
      });
      mergeDetails(conversationId, assistantId, { error: data });
      setError(String(data.message ?? "执行失败"));
      return;
    }
    if (event.type === "step_start" || event.type === "step_finish") {
      appendEventDetail(
        conversationId,
        assistantId,
        "steps",
        summarizeStepEvent(event.data),
        40,
      );
      return;
    }
    if (event.type === "llm_delta") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      mergeDetails(conversationId, assistantId, {
        llm_stream_preview: truncateText(String(data.delta ?? ""), 400),
      });
      return;
    }
    if (event.type === "llm_reasoning_delta") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      appendDetailText(conversationId, assistantId, "reasoning", String(data.delta ?? ""), 12000);
      appendEventDetail(
        conversationId,
        assistantId,
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
      const data = asRecord(event.data);
      const reasoning = String(data.reasoning ?? "");
      mergeDetails(conversationId, assistantId, {
        llm_done: event.data,
        ...(reasoning.trim() !== "" ? { reasoning } : {}),
      });
      return;
    }
    if (event.type === "done") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, assistantId, {
        status: "done",
      });
      mergeDetails(conversationId, assistantId, { execution: data.execution });
      onKnowledgeChanged?.();
      return;
    }
  }

  function handleUploadStreamEvent(
    conversationId: string,
    assistantId: string,
    event: AdminStreamEvent,
  ) {
    if (event.type === "meta") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      updateLastMode(conversationId, String(data.mode ?? "ingest"));
      mergeDetails(conversationId, assistantId, {
        execution: {
          id: data.execution_id,
          kind: data.mode ?? "ingest",
          status: "RUNNING",
          started_at: data.started_at,
        },
        upload: {
          file_name: data.file_name,
          stored_path: data.stored_path,
          source_format: data.source_format,
          media_kind: data.media_kind,
        },
      });
      return;
    }
    if (event.type === "prompt") {
      appendEventDetail(
        conversationId,
        assistantId,
        "prompts",
        summarizePromptEvent(event.data),
        8,
      );
      return;
    }
    if (event.type === "result") {
      const data = asRecord(event.data);
      const reply = firstNonEmpty(stringValue(data, "reply"), "上传并摄入完成。");
      const details = asRecord(data.details);
      const execution = asRecord(data.execution);
      applySessionStatePatch(
        conversationId,
        "ingest",
        reply,
        details,
        { steps: executionSteps(execution) },
      );
      patchMessage(conversationId, assistantId, {
        content: "",
        status: "done",
      });
      mergeDetails(conversationId, assistantId, {
        response: data,
        result: details,
        execution,
        steps: executionSteps(execution),
      });
      void animateAssistantReply(conversationId, assistantId, reply);
      return;
    }
    if (event.type === "error") {
      const data = asRecord(event.data);
      const message = String(data.message ?? "上传摄入失败");
      patchMessage(conversationId, assistantId, {
        content: `上传摄入失败：${message}`,
        status: "error",
      });
      mergeDetails(conversationId, assistantId, { error: data });
      setError(message);
      return;
    }
    if (event.type === "step_start" || event.type === "step_finish") {
      appendEventDetail(
        conversationId,
        assistantId,
        "steps",
        summarizeStepEvent(event.data),
        40,
      );
      return;
    }
    if (event.type === "llm_delta") {
      const data = asRecord(event.data);
      mergeDetails(conversationId, assistantId, {
        llm_stream_preview: truncateText(String(data.delta ?? ""), 400),
      });
      return;
    }
    if (event.type === "llm_reasoning_delta") {
      const data = asRecord(event.data);
      appendDetailText(conversationId, assistantId, "reasoning", String(data.delta ?? ""), 12000);
      appendEventDetail(
        conversationId,
        assistantId,
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
      const data = asRecord(event.data);
      const reasoning = String(data.reasoning ?? "");
      mergeDetails(conversationId, assistantId, {
        llm_done: event.data,
        ...(reasoning.trim() !== "" ? { reasoning } : {}),
      });
      return;
    }
    if (event.type === "done") {
      const data = asRecord(event.data);
      patchMessage(conversationId, assistantId, {
        status: "done",
      });
      mergeDetails(conversationId, assistantId, { execution: data.execution });
      onKnowledgeChanged?.();
    }
  }

  async function animateAssistantReply(
    conversationId: string,
    messageId: string,
    text: string,
  ) {
    const key = `${conversationId}:${messageId}`;
    const existing = typingTimersRef.current[key];
    if (existing) {
      window.clearTimeout(existing);
      delete typingTimersRef.current[key];
    }
    const chunks = chunkTextForTyping(text, 12);
    let index = 0;
    const tick = () => {
      const next = chunks[index] ?? "";
      patchMessage(conversationId, messageId, {
        content: (prev) => `${prev}${next}`,
      });
      index += 1;
      if (index >= chunks.length) {
        delete typingTimersRef.current[key];
        return;
      }
      typingTimersRef.current[key] = window.setTimeout(tick, 20);
    };
    tick();
  }

  function patchMessage(
    conversationId: string,
    messageId: string,
    updates: {
      content?: string | ((prev: string) => string);
      created_at?: string;
      status?: MessageStatus;
      details?: unknown;
    },
  ) {
    setConversations((current) =>
      current.map((conversation) => {
        if (conversation.id !== conversationId) {
          return conversation;
        }
        return {
          ...conversation,
          messages: conversation.messages.map((message) => {
            if (message.id !== messageId) {
              return message;
            }
            const nextContent =
              typeof updates.content === "function"
                ? updates.content(message.content)
                : (updates.content ?? message.content);
            return {
              ...message,
              content: nextContent,
              created_at: updates.created_at?.trim() ? updates.created_at : message.created_at,
              status: updates.status ?? message.status,
              details: updates.details ?? message.details,
            };
          }),
        };
      }),
    );
  }

  function mergeDetails(
    conversationId: string,
    messageId: string,
    patch: Record<string, unknown>,
  ) {
    setConversations((current) =>
      current.map((conversation) => {
        if (conversation.id !== conversationId) {
          return conversation;
        }
        return {
          ...conversation,
          messages: conversation.messages.map((message) => {
            if (message.id !== messageId) {
              return message;
            }
            return {
              ...message,
              details: {
                ...asRecord(message.details),
                ...patch,
              },
            };
          }),
        };
      }),
    );
  }

  function appendEventDetail(
    conversationId: string,
    messageId: string,
    key: string,
    value: unknown,
    maxItems = 24,
  ) {
    setConversations((current) =>
      current.map((conversation) => {
        if (conversation.id !== conversationId) {
          return conversation;
        }
        return {
          ...conversation,
          messages: conversation.messages.map((message) => {
            if (message.id !== messageId) {
              return message;
            }
            const details = asRecord(message.details);
            const values = Array.isArray(details[key]) ? details[key] : [];
            return {
              ...message,
              details: {
                ...details,
                [key]: [...values, value].slice(-maxItems),
              },
            };
          }),
        };
      }),
    );
  }

  function appendDetailText(
    conversationId: string,
    messageId: string,
    key: string,
    value: string,
    maxChars = 12000,
  ) {
    if (value === "") {
      return;
    }
    setConversations((current) =>
      current.map((conversation) => {
        if (conversation.id !== conversationId) {
          return conversation;
        }
        return {
          ...conversation,
          messages: conversation.messages.map((message) => {
            if (message.id !== messageId) {
              return message;
            }
            const details = asRecord(message.details);
            const next = `${String(details[key] ?? "")}${value}`;
            return {
              ...message,
              details: {
                ...details,
                [key]: next.length > maxChars ? next.slice(-maxChars) : next,
              },
            };
          }),
        };
      }),
    );
  }

  function appendMessage(conversationId: string, message: AdminMessage) {
    setConversations((current) =>
      current.map((item) =>
        item.id === conversationId
          ? { ...item, messages: [...item.messages, message] }
          : item,
      ),
    );
  }

  function renameConversation(conversationId: string, title: string) {
    setConversations((current) =>
      current.map((item) =>
        item.id === conversationId
          ? { ...item, title: title.slice(0, 24) }
          : item,
      ),
    );
  }

  function updateLastMode(conversationId: string, mode: string) {
    setConversations((current) =>
      current.map((item) =>
        item.id === conversationId
          ? (() => {
              const sessionState = normalizeAdminSessionState(
                item.sessionState,
              );
              return {
                ...item,
                lastMode: mode || item.lastMode,
                sessionState: {
                  ...sessionState,
                  lastMode: mode || sessionState.lastMode,
                },
              };
            })()
          : item,
      ),
    );
  }

  function applySessionStatePatch(
    conversationId: string,
    mode: string,
    reply: string,
    details: Record<string, unknown>,
    execution?: { steps?: Array<{ tool?: string }> },
  ) {
    setConversations((current) =>
      current.map((item) => {
        if (item.id !== conversationId) {
          return item;
        }
        const nextState = nextAdminSessionState(
          item.sessionState,
          mode,
          reply,
          details,
          execution,
        );
        return {
          ...item,
          lastMode: mode || item.lastMode,
          sessionState: nextState,
        };
      }),
    );
  }

  function createNewConversation() {
    const next = createConversation("知识库会话");
    setConversations((current) => [next, ...current]);
    setActiveId(next.id);
    setSelectedDetailId("");
    setError("");
  }

  function deleteConversation(id: string) {
    const controller = runtime.controllers[id];
    controller?.abort();
    finishConversationRequest(id, controller);
    setConversations((current) => {
      const remaining = current.filter((item) => item.id !== id);
      if (remaining.length === 0) {
        const fallback = createConversation("知识库会话");
        setActiveId(fallback.id);
        return [fallback];
      }
      if (activeId === id) {
        setActiveId(remaining[0].id);
      }
      if (selectedDetailId && id === activeId) {
        setSelectedDetailId("");
      }
      return remaining;
    });
  }

  const sidebarItems: ConversationItem[] = conversations.map((item) => ({
    id: item.id,
    title: item.title,
    updatedAt: lastMessageTime(item.messages),
  }));

  return (
	    <div
	      className={cn(
	        embedded ? "grid h-full min-h-0 grid-cols-1 gap-2 overflow-hidden p-2" : "chat-shell",
	        embedded && sidebarOpen && "lg:grid-cols-[232px_minmax(0,1fr)]",
	        embedded && !sidebarOpen && "lg:grid-cols-1",
	        !embedded && !sidebarOpen && "chat-shell-collapsed",
	      )}
	    >
      {sidebarOpen ? (
        <ConversationSidebar
          title={sidebarTitle}
          subtitle={sidebarSubtitle ?? `已登录：${username}`}
          variant="admin"
          items={sidebarItems}
          activeId={activeConversation?.id ?? ""}
          onSelect={setActiveId}
          onCreate={createNewConversation}
          onDelete={deleteConversation}
        />
      ) : null}
      <section className={cn("relative flex h-full min-h-0 flex-col overflow-hidden", embedded ? "rounded-2xl border bg-white shadow-sm dark:bg-card dark:shadow-none" : "panel-glass")}>
        <header className={cn("relative z-10", embedded ? "px-4 pb-0 pt-3" : "border-b px-6 py-5")}>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex min-w-0 items-center gap-2">
              {embedded ? (
                <button
                  type="button"
                  onClick={() => setSidebarOpen((value) => !value)}
                  title="显示或隐藏左侧会话列表"
                  aria-label={sidebarOpen ? "隐藏会话列表" : "显示会话列表"}
	                  className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-slate-500 transition hover:bg-slate-100 hover:text-slate-950 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-300 dark:text-muted-foreground dark:hover:bg-secondary dark:hover:text-foreground dark:focus-visible:ring-ring"
                >
                  {sidebarOpen ? <PanelLeftClose className="h-4 w-4" /> : <PanelLeft className="h-4 w-4" />}
                </button>
              ) : (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setSidebarOpen((value) => !value)}
                  title="显示或隐藏左侧会话列表"
                >
                  {sidebarOpen ? <PanelLeftClose className="mr-2 h-4 w-4" /> : <PanelLeft className="mr-2 h-4 w-4" />}
                  {sidebarOpen ? "隐藏会话" : "显示会话"}
                </Button>
              )}
              <div className="min-w-0">
                <h1 className={cn("truncate font-semibold", embedded ? "text-base" : "text-lg")}>{title}</h1>
                {!showKnowledgeTasks && subtitle ? (
                  <p className={cn("mt-0.5 truncate text-muted-foreground", embedded ? "text-xs" : "text-sm")}>
                    {subtitle}
                  </p>
                ) : null}
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {showAdminShortcuts ? (
                <>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void openModelModal()}
                    disabled={modelsLoading}
                    title="管理并切换当前 LLM 模型"
                  >
                    <Bot className="mr-2 h-4 w-4" />
                    模型
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void openIntentEditor()}
                    disabled={intentLoading}
                    title="编辑 server 端前置话术 YAML"
                  >
                    <FileText className="mr-2 h-4 w-4" />
                    前置话术
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void openReviewModal()}
                    disabled={reviewLoading}
                    title="逐条审查 LLM 低置信自答内容"
                    className="relative"
                  >
                    <ClipboardCheck className="mr-2 h-4 w-4" />
                    问题审查
                    {reviewCount > 0 ? (
                      <span className="absolute -right-2 -top-2 min-w-5 rounded-full bg-red-600 px-1.5 py-0.5 text-[10px] font-semibold leading-none text-white">
                        {reviewCount > 99 ? "99+" : reviewCount}
                      </span>
                    ) : null}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => window.open("/knowledge?view=browse", "_blank")}
                    title="打开知识库浏览"
                  >
                    <Database className="mr-2 h-4 w-4" />
                    资料库
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void openSyncModal()}
                    disabled={syncBusy}
                    title="查看 Wiki Git 变更，选择文件提交并推送"
                  >
                    <GitBranch className="mr-2 h-4 w-4" />
                    同步
                  </Button>
                </>
              ) : null}
              {showKnowledgeTasks ? (
                <KnowledgeTaskCommandBar
                  actions={knowledgeTaskActions}
                  busy={busy}
                  onRun={(action) => void runKnowledgeTask(action)}
                  onUpload={() => uploadInputRef.current?.click()}
                />
              ) : null}
              {!showKnowledgeTasks ? (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    activeConversation &&
                    deleteConversation(activeConversation.id)
                  }
                  title="删除当前本地会话记录"
                  className={cn(embedded && "h-8 px-2 text-xs")}
                >
                  <Trash2 className="mr-2 h-4 w-4" />
                  删除会话
                </Button>
              ) : null}
            </div>
          </div>
        </header>
        <div className="relative min-h-0 flex-1">
          <ScrollArea
            viewportRef={chatScroll.viewportRef}
            className={cn("h-full", embedded ? "px-4 py-3" : "px-6 py-5")}
          >
            <div className={cn("mx-auto flex flex-col gap-4 pb-8", embedded ? "max-w-2xl" : "max-w-3xl")}>
              {activeConversation?.messages.map((message) => (
                <MessageCard
                  key={message.id}
                  id={message.id}
                  role={message.role}
                  content={message.content || "处理中..."}
                  createdAt={message.created_at}
                  pending={
                    message.status === "pending" ||
                    message.status === "streaming"
                  }
                  statusText={messageStatusText(message)}
                  details={message.details}
                  selected={selectedDetailId === message.id}
                  onInspect={({ id }) => setSelectedDetailId(id)}
                />
              ))}
            </div>
          </ScrollArea>
          <ScrollJumpControls
            show={chatScroll.showControls}
            onTop={() => chatScroll.scrollToTop()}
            onBottom={() => chatScroll.scrollToBottom()}
            className="bottom-4 right-6"
          />
        </div>
        <div className={cn("bg-white/85 backdrop-blur dark:bg-card/85", embedded ? "px-4 py-3" : "border-t px-4 py-3")}>
          <div className={cn("mx-auto", embedded ? "max-w-3xl" : "max-w-4xl")}>
            {showKnowledgeTasks ? (
              <input
                ref={uploadInputRef}
                type="file"
                className="hidden"
                onChange={(event) => void uploadKnowledgeFile(event)}
              />
            ) : null}
            <div className={cn("mb-2 flex items-center justify-between gap-3 px-1 text-xs text-muted-foreground", embedded && "text-[11px]")}>
              <span className="truncate">
                {error || busyLabel || "支持多轮上下文，执行过程可在消息内展开。"}
              </span>
              <CompactContextUsage
                usage={contextUsage}
                loading={contextLoading}
                onNewConversation={createNewConversation}
              />
            </div>
            <div className={cn("rounded-2xl border bg-white px-3 py-2 dark:bg-background", embedded ? "shadow-sm dark:shadow-none" : "shadow-soft dark:shadow-none")}>
              <Textarea
                ref={composerRef}
                value={composer}
                onChange={(event) => setComposer(event.target.value)}
                onKeyDown={(event) => {
                  if (busy) {
                    return;
                  }
                  if (event.key === "Enter" && !event.shiftKey) {
                    event.preventDefault();
                    void send();
                  }
                }}
                rows={1}
                className={cn("resize-none overflow-hidden border-0 bg-transparent px-2 py-1.5 shadow-none focus-visible:ring-0", embedded ? "min-h-[38px]" : "min-h-[48px]")}
                placeholder="输入知识库问题或运维要求"
              />
              <div className="mt-1 flex flex-wrap items-center justify-end gap-2">
                <div className="flex items-center gap-2">
                  {!forceStream ? (
                    <div
	                      className="flex rounded-full border bg-slate-50 p-0.5 dark:bg-secondary"
                      title="选择本次管理员回复方式"
                    >
                      <button
                        type="button"
                        className={cn(
                          "rounded-full px-3 py-1 text-xs transition",
                          activeConversation?.stream
	                            ? "bg-white text-slate-950 shadow-sm dark:bg-background dark:text-foreground dark:shadow-none"
	                            : "text-muted-foreground hover:text-slate-950 dark:hover:text-foreground",
                        )}
                        onClick={() => {
                          setConversations((current) =>
                            current.map((item) =>
                              item.id === activeConversation?.id
                                ? { ...item, stream: true }
                                : item,
                            ),
                          );
                        }}
                        title="开启流式返回，边执行边显示过程"
                      >
                        流式
                      </button>
                      <button
                        type="button"
                        className={cn(
                          "rounded-full px-3 py-1 text-xs transition",
                          !activeConversation?.stream
	                            ? "bg-white text-slate-950 shadow-sm dark:bg-background dark:text-foreground dark:shadow-none"
	                            : "text-muted-foreground hover:text-slate-950 dark:hover:text-foreground",
                        )}
                        onClick={() => {
                          setConversations((current) =>
                            current.map((item) =>
                              item.id === activeConversation?.id
                                ? { ...item, stream: false }
                                : item,
                            ),
                          );
                        }}
                        title="关闭流式返回，等待完整结果后一次展示"
                      >
                        非流式
                      </button>
                    </div>
                  ) : null}
                  <Button
                    type="button"
                    className={cn(
                      "rounded-full p-0",
                      embedded ? "h-8 w-8" : "h-10 w-10",
                      busy && "bg-secondary text-secondary-foreground hover:bg-secondary/80",
                    )}
                    variant={busy ? "secondary" : "default"}
                    onClick={() => {
                      if (busy) {
                        stopActiveRequest();
                        return;
                      }
                      void send();
                    }}
                    disabled={!busy && Boolean(contextUsage?.blocked)}
                    title={
                      busy
                        ? "停止当前正在执行的管理员请求"
                        : contextUsage?.blocked
                        ? "当前对话已达到上下文上限，请新建会话"
                        : "发送管理员指令"
                    }
                  >
                    {busy ? (
                      <Square className={cn("fill-current", embedded ? "h-3 w-3" : "h-3.5 w-3.5")} />
                    ) : (
                      <SendHorizontal className={cn(embedded ? "h-3.5 w-3.5" : "h-4 w-4")} />
                    )}
                    <span className="sr-only">{busy ? "停止" : "发送"}</span>
                  </Button>
                </div>
              </div>
            </div>
          </div>
        </div>
        <ChatDetailDrawer
          title="执行详情"
          open={Boolean(selectedDetail)}
          selected={
            selectedDetail
              ? {
                  role: selectedDetail.role,
                  content: selectedDetail.content,
                  createdAt: selectedDetail.created_at,
                  details: selectedDetail.details,
                  statusText: messageStatusText(selectedDetail),
                }
              : null
          }
          onClear={() => setSelectedDetailId("")}
        />
        {modelOpen ? (
          <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/35 p-4"
            role="dialog"
            aria-modal="true"
            aria-labelledby="model-title"
            onMouseDown={(event) => {
              if (event.target === event.currentTarget) {
                setModelOpen(false);
              }
            }}
          >
            <div className="flex max-h-[88vh] w-full max-w-6xl flex-col overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl">
              <header className="flex items-start justify-between gap-4 border-b px-5 py-4">
                <div className="min-w-0">
                  <h2 id="model-title" className="text-sm font-semibold">
                    模型管理
                  </h2>
                  <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                    <span>当前：{activeModel?.display_name || "未启用模型"}</span>
                    {activeModel ? (
                      <span className="font-mono text-slate-500">
                        {activeModel.model_name}
                      </span>
                    ) : null}
                  </div>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setModelOpen(false)}
                  title="关闭模型弹窗"
                >
                  <X className="mr-2 h-4 w-4" />
                  关闭
                </Button>
              </header>
              <div className="grid min-h-0 flex-1 gap-4 overflow-y-auto px-5 py-4 lg:grid-cols-[minmax(0,1.1fr)_minmax(320px,0.9fr)]">
                <section className="min-h-0">
                  <div className="mb-3 flex items-center justify-between gap-2">
                    <div className="text-xs font-semibold text-slate-600">
                      已添加模型
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => void refreshModels()}
                      disabled={modelsLoading || modelBusy || modelTestingId !== null}
                      title="重新读取模型列表"
                    >
                      <RefreshCw className={cn("mr-2 h-4 w-4", modelsLoading && "animate-spin")} />
                      刷新
                    </Button>
                  </div>
                  <div className="space-y-3">
                    {modelsLoading ? (
                      <div className="rounded-xl border border-slate-200 px-4 py-8 text-center text-sm text-muted-foreground">
                        正在读取模型...
                      </div>
                    ) : models.length === 0 ? (
                      <div className="rounded-xl border border-slate-200 px-4 py-8 text-center text-sm text-muted-foreground">
                        还没有模型。
                      </div>
                    ) : (
                      models.map((model) => (
                        <div
                          key={model.id}
                          className={cn(
                            "rounded-xl border p-4",
                            model.is_active
                              ? "border-emerald-200 bg-emerald-50/50"
                              : "border-slate-200 bg-white",
                          )}
                        >
                          <div className="flex flex-wrap items-start justify-between gap-3">
                            <div className="min-w-0">
                              <div className="flex flex-wrap items-center gap-2">
                                <div className="truncate text-sm font-semibold text-slate-900">
                                  {model.display_name}
                                </div>
                                {model.is_active ? (
                                  <Badge variant="success">当前</Badge>
                                ) : null}
                              </div>
                              <div className="mt-1 truncate font-mono text-xs text-slate-600">
                                {model.model_name}
                              </div>
                            </div>
                            <div className="flex flex-wrap items-center gap-2">
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => editModel(model)}
                                disabled={modelBusy || modelTestingId !== null}
                                title="编辑这个模型"
                              >
                                <Pencil className="mr-2 h-4 w-4" />
                                编辑
                              </Button>
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => void testModel(model.id)}
                                disabled={modelBusy || modelTestingId !== null}
                                title="测试这个模型是否能正常连接"
                              >
                                <Activity
                                  className={cn(
                                    "mr-2 h-4 w-4",
                                    modelTestingId === model.id && "animate-pulse text-emerald-600",
                                  )}
                                />
                                {modelTestingId === model.id ? "测试中" : "测试"}
                              </Button>
                              <Button
                                type="button"
                                size="sm"
                                onClick={() => void activateModel(model.id)}
                                disabled={modelBusy || modelTestingId !== null || model.is_active}
                                title="切换为当前模型"
                              >
                                <Power className="mr-2 h-4 w-4" />
                                启用
                              </Button>
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => void deleteModel(model.id)}
                                disabled={modelBusy || modelTestingId !== null}
                                title="删除这个模型"
                              >
                                <Trash2 className="mr-2 h-4 w-4" />
                                删除
                              </Button>
                            </div>
                          </div>
                          <div className="mt-3 grid gap-2 text-xs text-slate-600 sm:grid-cols-2">
                            <div className="truncate">端点：{model.base_url}</div>
                            <div className="truncate">服务商：{model.provider || "-"}</div>
                            <div>请求超时：{model.timeout_sec}s</div>
                            <div>管理超时：{model.admin_timeout_sec}s</div>
                            <div className="truncate">密钥：{model.api_key_mask || "未设置"}</div>
                            <div>更新：{formatDateTime(model.updated_at)}</div>
                          </div>
                        </div>
                      ))
                    )}
                  </div>
                </section>
                <section className="rounded-xl border border-slate-200 bg-slate-50/70 p-4">
                  <div className="mb-3 flex items-center justify-between gap-2">
                    <div>
                      <div className="text-xs font-semibold text-slate-600">
                        {modelForm.id ? "编辑模型" : "新增模型"}
                      </div>
                      {modelForm.id ? (
                        <div className="mt-1 text-[11px] text-muted-foreground">
                          API Key 留空会保留原密钥。
                        </div>
                      ) : null}
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        setModelForm(emptyLLMModelForm());
                        setModelMessage("");
                      }}
                      disabled={modelBusy || modelTestingId !== null}
                      title="清空表单"
                    >
                      <Plus className="mr-2 h-4 w-4" />
                      新增
                    </Button>
                  </div>
                  <div className="space-y-3">
                    <label className="grid gap-1.5 text-xs font-semibold text-slate-600">
                      显示名称
                      <Input
                        value={modelForm.display_name}
                        onChange={(event) =>
                          setModelForm((current) => ({
                            ...current,
                            display_name: event.target.value,
                          }))
                        }
                        className="h-10 rounded-md"
                        placeholder="生产客服模型"
                      />
                    </label>
                    <label className="grid gap-1.5 text-xs font-semibold text-slate-600">
                      服务商
                      <Input
                        value={modelForm.provider}
                        onChange={(event) =>
                          setModelForm((current) => ({
                            ...current,
                            provider: event.target.value,
                          }))
                        }
                        className="h-10 rounded-md"
                        placeholder="openai-compatible"
                      />
                    </label>
                    <label className="grid gap-1.5 text-xs font-semibold text-slate-600">
                      端点域名
                      <Input
                        value={modelForm.base_url}
                        onChange={(event) =>
                          setModelForm((current) => ({
                            ...current,
                            base_url: event.target.value,
                          }))
                        }
                        className="h-10 rounded-md font-mono"
                        placeholder="https://api.example.com/v1"
                      />
                    </label>
                    <label className="grid gap-1.5 text-xs font-semibold text-slate-600">
                      模型名
                      <Input
                        value={modelForm.model_name}
                        onChange={(event) =>
                          setModelForm((current) => ({
                            ...current,
                            model_name: event.target.value,
                          }))
                        }
                        className="h-10 rounded-md font-mono"
                        placeholder="gpt-4.1-mini"
                      />
                    </label>
                    <label className="grid gap-1.5 text-xs font-semibold text-slate-600">
                      API Key
                      <Input
                        type="password"
                        value={modelForm.api_key}
                        onChange={(event) =>
                          setModelForm((current) => ({
                            ...current,
                            api_key: event.target.value,
                          }))
                        }
                        className="h-10 rounded-md font-mono"
                        placeholder={modelForm.id ? "留空保留原密钥" : "sk-..."}
                      />
                    </label>
                    <div className="grid gap-3 sm:grid-cols-2">
                      <label className="grid gap-1.5 text-xs font-semibold text-slate-600">
                        请求超时秒数
                        <Input
                          type="number"
                          min={1}
                          value={modelForm.timeout_sec}
                          onChange={(event) =>
                            setModelForm((current) => ({
                              ...current,
                              timeout_sec: event.target.value,
                            }))
                          }
                          className="h-10 rounded-md"
                        />
                      </label>
                      <label className="grid gap-1.5 text-xs font-semibold text-slate-600">
                        管理超时秒数
                        <Input
                          type="number"
                          min={1}
                          value={modelForm.admin_timeout_sec}
                          onChange={(event) =>
                            setModelForm((current) => ({
                              ...current,
                              admin_timeout_sec: event.target.value,
                            }))
                          }
                          className="h-10 rounded-md"
                        />
                      </label>
                    </div>
                  </div>
                </section>
              </div>
              <footer className="flex flex-wrap items-center justify-between gap-2 border-t bg-slate-50 px-5 py-4">
                <span
                  className={cn(
                    "text-xs",
                    modelMessage.includes("已") || modelMessage.includes("成功")
                      ? "text-emerald-700"
                      : modelMessage
                        ? "text-destructive"
                        : "text-muted-foreground",
                  )}
                >
                  {modelMessage || " "}
                </span>
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setModelOpen(false)}
                    disabled={modelBusy || modelTestingId !== null}
                  >
                    关闭
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    onClick={() => void saveModel()}
                    disabled={modelBusy || modelTestingId !== null}
                    title="保存模型配置"
                  >
                    <Save className="mr-2 h-4 w-4" />
                    {modelBusy ? "保存中" : "保存"}
                  </Button>
                </div>
              </footer>
            </div>
          </div>
        ) : null}
        {reviewOpen ? (
          <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/35 p-4"
            role="dialog"
            aria-modal="true"
            aria-labelledby="review-title"
            onMouseDown={(event) => {
              if (event.target === event.currentTarget) {
                setReviewOpen(false);
              }
            }}
          >
            <div className="flex max-h-[88vh] w-full max-w-4xl flex-col overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl">
              <header className="flex items-start justify-between gap-4 border-b px-5 py-4">
                <div>
                  <h2 id="review-title" className="text-sm font-semibold">
                    问题审查
                  </h2>
                  <p className="mt-1 text-xs text-muted-foreground">
                    待审 {reviewCount} 条；每次只处理当前这一条。
                  </p>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setReviewOpen(false)}
                  title="关闭审查弹窗"
                >
                  <X className="mr-2 h-4 w-4" />
                  关闭
                </Button>
              </header>
              <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
                {reviewLoading ? (
                  <div className="py-12 text-center text-sm text-muted-foreground">
                    正在读取待审查内容...
                  </div>
                ) : reviewItem ? (
                  <div className="space-y-4">
                    <div className="grid gap-2">
                      <label className="text-xs font-semibold text-slate-600">
                        问题
                      </label>
                      <Textarea
                        value={reviewQuestion}
                        onChange={(event) => setReviewQuestion(event.target.value)}
                        className="min-h-[76px] resize-none bg-white text-sm"
                        title="管理员可以修正问题表述后再通过"
                      />
                    </div>
                    <div className="grid gap-2">
                      <label className="text-xs font-semibold text-slate-600">
                        回答草稿
                      </label>
                      <Textarea
                        value={reviewAnswer}
                        onChange={(event) => setReviewAnswer(event.target.value)}
                        className="min-h-[180px] resize-y bg-white text-sm leading-relaxed"
                        title="管理员可以修改回答后再通过"
                      />
                    </div>
                    <div className="grid gap-2">
                      <label className="text-xs font-semibold text-slate-600">
                        目标知识页
                      </label>
                      <select
                        value={reviewTargetPath}
                        onChange={(event) => setReviewTargetPath(event.target.value)}
                        className="h-10 rounded-md border border-input bg-white px-3 text-sm"
                        title="通过后会沉淀到这个知识页或意图页"
                      >
                        {reviewTargets.map((target) => (
                          <option key={target.path} value={target.path}>
                            {target.title} · {target.path}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className="grid gap-2">
                      <label className="text-xs font-semibold text-slate-600">
                        驳回原因
                      </label>
                      <input
                        value={reviewRejectReason}
                        onChange={(event) => setReviewRejectReason(event.target.value)}
                        className="h-10 rounded-md border border-input bg-white px-3 text-sm"
                        placeholder="例如：回答不准确或不适合自动回复"
                        title="驳回后会写入禁答记录"
                      />
                    </div>
                    <div className="rounded-xl border border-slate-200 bg-slate-50 px-3 py-2 text-xs leading-5 text-slate-600">
                      <div>置信度：{reviewItem.confidence || 0}</div>
                      <div>证据置信度：{reviewItem.evidence_confidence ?? 0}</div>
                      <div>回答模式：{reviewItem.answer_mode || "-"}</div>
                      {reviewItem.original_question ? <div>原始提问：{reviewItem.original_question}</div> : null}
                      <div>提问时间：{formatDateTime(reviewItem.question_created_at)}</div>
                      <div>回答时间：{formatDateTime(reviewItem.answer_created_at)}</div>
                      <div>创建时间：{formatDateTime(reviewItem.created_at)}</div>
                      <div>原因：{reviewItem.boundary_reason || "低置信自答，等待人工确认。"}</div>
                      <div className="truncate">文件：{reviewItem.path}</div>
                      {reviewItem.matched_pages?.length ? (
                        <div className="mt-1">
                          相关路径：{reviewItem.matched_pages.slice(0, 4).join("、")}
                        </div>
                      ) : null}
                      {reviewItem.conversation_excerpt?.length ? (
                        <div className="mt-1 line-clamp-3">
                          对话片段：{reviewItem.conversation_excerpt.slice(-3).join(" / ")}
                        </div>
                      ) : null}
                    </div>
                  </div>
                ) : (
                  <div className="py-12 text-center text-sm text-muted-foreground">
                    当前没有待审查问题。
                  </div>
                )}
                {reviewMessage ? (
                  <div
                    className={cn(
                      "mt-3 text-xs",
                      reviewMessage.includes("已")
                        ? "text-emerald-700"
                        : "text-destructive",
                    )}
                  >
                    {reviewMessage}
                  </div>
                ) : null}
              </div>
              <footer className="flex flex-wrap items-center justify-between gap-2 border-t bg-slate-50 px-5 py-4">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={reviewLoading || reviewBusy || !reviewItem || reviewCount <= 1}
                  onClick={() => reviewItem && void loadReviewItem(reviewItem.id)}
                  title="跳到下一条待审查内容，不处理当前条"
                >
                  下一条
                </Button>
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setReviewOpen(false)}
                    title="关闭审查弹窗"
                  >
                    关闭
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={reviewLoading || reviewBusy || !reviewItem}
                    onClick={() => void deleteReviewItem()}
                    title="仅从待审队列删除，不写入知识库，也不进入禁答列表"
                  >
                    <Trash2 className="mr-2 h-4 w-4" />
                    删除
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={reviewLoading || reviewBusy || !reviewItem}
                    onClick={() => void rejectReviewItem()}
                    title="记录到禁答列表，后续不能回复这个问题"
                  >
                    <XCircle className="mr-2 h-4 w-4" />
                    驳回
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    disabled={reviewLoading || reviewBusy || !reviewItem}
                    onClick={() => void approveReviewItem()}
                    title="记录到知识库中，后续可直接回复"
                  >
                    <CheckCircle2 className="mr-2 h-4 w-4" />
                    通过
                  </Button>
                </div>
              </footer>
            </div>
          </div>
        ) : null}
        {syncOpen ? (
          <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/35 p-4"
            role="dialog"
            aria-modal="true"
            aria-labelledby="sync-title"
            onMouseDown={(event) => {
              if (event.target === event.currentTarget) {
                setSyncOpen(false);
              }
            }}
          >
            <div className="flex max-h-[88vh] w-full max-w-5xl flex-col overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl">
              <header className="flex items-start justify-between gap-4 border-b px-5 py-4">
                <div>
                  <h2 id="sync-title" className="text-sm font-semibold">
                    同步 Wiki
                  </h2>
                  <p className="mt-1 text-xs text-muted-foreground">
                    先选择文件并提交，再确认推送。同步由 server 执行，不经过
                    LLM。
                  </p>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setSyncOpen(false)}
                  title="关闭同步弹窗"
                >
                  <X className="mr-2 h-4 w-4" />
                  关闭
                </Button>
              </header>
              <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
                <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>
                    分支：{syncStatus?.branch || "-"}；远端：
                    {syncStatus?.remote || "-"}；ahead {syncStatus?.ahead ?? 0}{" "}
                    / behind {syncStatus?.behind ?? 0}
                  </span>
                  <span className="rounded-full bg-slate-100 px-2 py-1 text-slate-700">
                    待提交 {syncStatus?.changed_count ?? syncStatus?.files.length ?? 0} 个文件
                  </span>
                  <span
                    className={cn(
                      "rounded-full px-2 py-1",
                      (syncStatus?.push_count ?? 0) > 0
                        ? "bg-emerald-50 text-emerald-700"
                        : "bg-slate-100 text-slate-600",
                    )}
                  >
                    待推送 {syncStatus?.push_count ?? 0} 个提交
                  </span>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void refreshSyncStatus()}
                    disabled={syncBusy}
                    title="重新读取 Git 变更"
                  >
                    <RefreshCw className="mr-2 h-4 w-4" />
                    刷新
                  </Button>
                </div>
                {(syncStatus?.commits_to_push.length ?? 0) > 0 ? (
                  <div className="mb-3 rounded-xl border border-emerald-200 bg-emerald-50/60 p-3">
                    <div className="text-xs font-semibold text-emerald-800">
                      待推送提交
                    </div>
                    <div className="mt-2 space-y-1">
                      {syncStatus?.commits_to_push.map((commit) => (
                        <div
                          key={commit.hash}
                          className="flex flex-wrap items-center gap-2 text-xs text-emerald-900"
                        >
                          <span className="font-mono">{commit.hash}</span>
                          <span>{commit.subject}</span>
                          <span className="text-emerald-700">
                            {commit.date} · {commit.author}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
                <div className="rounded-xl border border-slate-200">
                  {(syncStatus?.files.length ?? 0) === 0 ? (
                    <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                      {syncBusy
                        ? "正在读取变更..."
                        : "当前没有需要同步的文件。"}
                    </div>
                  ) : (
                    <div className="max-h-[36vh] overflow-y-auto">
                      {syncStatus?.files.map((file) => (
                        <label
                          key={file.path}
                          className="flex items-center gap-3 border-b px-4 py-3 text-sm last:border-b-0"
                        >
                          <input
                            type="checkbox"
                            checked={selectedSyncPaths.includes(file.path)}
                            onChange={() => toggleSyncPath(file.path)}
                            title="选择是否把这个文件加入本次提交"
                          />
                          <span className="w-14 shrink-0 rounded-full bg-slate-100 px-2 py-1 text-center text-[11px] text-slate-600">
                            {file.status || "?"}
                          </span>
                          <button
                            type="button"
                            className="min-w-0 flex-1 truncate text-left font-mono text-xs text-slate-900 hover:underline disabled:text-slate-400 disabled:no-underline"
                            disabled={file.deleted}
                            title={
                              file.deleted
                                ? "已删除文件不能预览"
                                : "在新标签打开资料库查看这个文件"
                            }
                            onClick={(event) => {
                              event.preventDefault();
                              if (!file.deleted) {
                                window.open(
                                  `/knowledge?view=browse&path=${encodeURIComponent(file.path)}`,
                                  "_blank",
                                );
                              }
                            }}
                          >
                            {file.path}
                          </button>
                          {file.deleted ? (
                            <span className="text-xs text-rose-600">
                              已删除
                            </span>
                          ) : file.preview === "download" ? (
                            <a
                              href={api.wikiDownloadURL(file.path)}
                              target="_blank"
                              rel="noreferrer"
                              className="inline-flex items-center gap-1 text-xs text-slate-600 hover:text-slate-900"
                              title="下载后查看该格式文件"
                              onClick={(event) => event.stopPropagation()}
                            >
                              <Download className="h-3.5 w-3.5" />
                              下载
                            </a>
                          ) : (
                            <span className="text-xs text-emerald-700">
                              可查看
                            </span>
                          )}
                        </label>
                      ))}
                    </div>
                  )}
                </div>
                <div className="mt-4 space-y-2">
                  <div className="flex items-center justify-between gap-2">
                    <label className="text-xs font-semibold text-slate-600">
                      提交信息
                    </label>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={syncBusy || syncMessageBusy || selectedSyncPaths.length === 0}
                      onClick={() => void generateSyncMessage()}
                      title="根据已选择文件让 LLM 生成一条符合规则的提交信息"
                    >
                      <Sparkles className="mr-2 h-4 w-4" />
                      {syncMessageBusy ? "生成中" : "LLM 生成"}
                    </Button>
                  </div>
                  <input
                    value={syncMessage}
                    onChange={(event) => setSyncMessage(event.target.value)}
                    className="h-10 w-full rounded-md border border-input bg-white px-3 text-sm"
                    placeholder="例如：更新 Wiki 内容"
                    title="本次 Git commit 的提交信息"
                  />
                  <p className="text-[11px] leading-5 text-muted-foreground">
                    {syncMessageRule ||
                      "规则：中文一行，说明本次 Wiki 资料变更，不提 LLM/AI/server/prompt。"}
                  </p>
                </div>
                <div className="mt-3 text-xs text-muted-foreground">
                  已选择 {selectedSyncPaths.length} 个文件。
                  {syncResult ? `最近提交：${syncResult.hash}` : ""}
                </div>
                {(syncStatus?.recent_commits.length ?? 0) > 0 ? (
                  <div className="mt-4 rounded-xl border border-slate-200 p-3">
                    <div className="text-xs font-semibold text-slate-600">
                      最近提交记录
                    </div>
                    <div className="mt-2 max-h-32 space-y-1 overflow-y-auto">
                      {syncStatus?.recent_commits.map((commit) => (
                        <div
                          key={commit.hash}
                          className="flex flex-wrap items-center gap-2 text-xs text-slate-600"
                        >
                          <span className="font-mono text-slate-900">
                            {commit.hash}
                          </span>
                          <span className="text-slate-900">
                            {commit.subject}
                          </span>
                          <span>
                            {commit.date} · {commit.author}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
                {syncError ? (
                  <div
                    className={cn(
                      "mt-2 text-xs",
                      syncError.includes("完成")
                        ? "text-emerald-700"
                        : "text-destructive",
                    )}
                  >
                    {syncError}
                  </div>
                ) : null}
              </div>
              <footer className="flex flex-wrap items-center justify-end gap-2 border-t bg-slate-50 px-5 py-4">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setSyncOpen(false)}
                  title="关闭同步弹窗"
                >
                  关闭
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={
                    syncBusy ||
                    selectedSyncPaths.length === 0 ||
                    syncMessage.trim() === ""
                  }
                  onClick={() => void commitSyncFiles()}
                  title="提交当前勾选的 Wiki 变更"
                >
                  提交
                </Button>
                <Button
                  type="button"
                  size="sm"
                  disabled={syncBusy || !syncStatus?.can_push}
                  onClick={() => void pushSyncCommit()}
                  title={
                    syncStatus?.can_push
                      ? "把当前分支未推送的提交推送到配置远端"
                      : "没有未推送提交，需先提交后才能推送"
                  }
                >
                  推送{(syncStatus?.push_count ?? 0) > 0 ? `（${syncStatus?.push_count}）` : ""}
                </Button>
              </footer>
            </div>
          </div>
        ) : null}
        {intentEditorOpen ? (
          <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/35 p-4"
            role="dialog"
            aria-modal="true"
            aria-labelledby="public-intents-title"
            onMouseDown={(event) => {
              if (event.target === event.currentTarget) {
                closeIntentEditor();
              }
            }}
          >
            <div className="flex max-h-[88vh] w-full max-w-5xl flex-col overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl">
              <header className="flex items-start justify-between gap-4 border-b px-5 py-4">
                <div>
                  <h2
                    id="public-intents-title"
                    className="text-sm font-semibold"
                  >
                    前置话术策略
                  </h2>
                  <p className="mt-1 text-xs text-muted-foreground">
                    直接编辑 server 端
                    YAML。保存成功后会立即校验、写入文件并替换内存缓存。
                  </p>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={closeIntentEditor}
                  aria-label="关闭前置话术弹窗"
                >
                  <X className="mr-2 h-4 w-4" />
                  关闭
                </Button>
              </header>
              <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
                <Textarea
                  value={intentSource}
                  onChange={(event) => setIntentSource(event.target.value)}
                  className="min-h-[52vh] resize-none bg-white font-mono text-xs leading-relaxed"
                  spellCheck={false}
                  placeholder={intentLoading ? "正在读取配置..." : "version: 1"}
                />
                <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>
                    {intentStatus
                      ? `文件：${intentStatus.path}；规则数：${intentStatus.rule_count}${intentStatus.loaded_at ? `；加载：${intentStatus.loaded_at}` : ""}`
                      : "尚未读取配置"}
                  </span>
                  <span
                    className={cn(
                      intentMessage.includes("成功") ||
                        intentMessage.includes("重新读取")
                        ? "text-emerald-700"
                        : intentMessage
                          ? "text-destructive"
                          : "",
                    )}
                  >
                    {intentMessage || intentStatus?.error || ""}
                  </span>
                </div>
              </div>
              <footer className="flex flex-wrap items-center justify-end gap-2 border-t bg-slate-50 px-5 py-4">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void reloadIntentSource()}
                  disabled={intentLoading || intentSaving}
                >
                  重新读取
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={closeIntentEditor}
                >
                  关闭
                </Button>
                <Button
                  type="button"
                  size="sm"
                  onClick={() => void saveIntentSource()}
                  disabled={intentLoading || intentSaving}
                >
                  <Save className="mr-2 h-4 w-4" />
                  {intentSaving ? "保存中" : "保存并生效"}
                </Button>
              </footer>
            </div>
          </div>
        ) : null}
      </section>
    </div>
  );
}

function createConversation(title: string): AdminConversation {
  return {
    id: createId(),
    title,
    messages: [],
    stream: true,
    lastMode: "query",
    sessionState: emptyAdminSessionState(),
  };
}

function buildAdminRequest(
  conversation: AdminConversation,
  message: string,
  overrides?: Partial<AdminChatRequest>,
): AdminChatRequest {
  return {
    message,
    stream: overrides?.stream ?? conversation.stream,
    mode_hint: overrides?.mode_hint,
    context: {
      last_mode: conversation.lastMode,
      session_state: normalizeAdminSessionState(conversation.sessionState),
      ...(overrides?.context ?? {}),
    },
    attachments: overrides?.attachments,
    history: conversationHistory(conversation.messages),
  };
}

function conversationHistory(messages: AdminMessage[]) {
  return messages
    .filter((message) => message.content.trim() !== "")
    .map((message) => ({
      id: message.id,
      role: message.role,
      content: message.content,
      created_at: message.created_at,
    }));
}

function lastMessageTime(messages: AdminMessage[]) {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const createdAt = messages[index]?.created_at;
    if (createdAt) {
      return createdAt;
    }
  }
  return "";
}

function formatDateTime(value?: string) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

function positiveNumber(value: string, fallback: number) {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function CompactContextUsage({
  usage,
  loading,
  onNewConversation,
}: {
  usage: ContextUsage | null;
  loading: boolean;
  onNewConversation: () => void;
}) {
  if (!usage) {
    return (
      <span className="shrink-0 text-[11px] text-muted-foreground">
        {loading ? "上下文计算中..." : "上下文暂不可用"}
      </span>
    );
  }
  const percent =
    usage.max_tokens > 0
      ? Math.min(100, Math.round((usage.used_tokens / usage.max_tokens) * 100))
      : 0;
  return (
    <span className="flex min-w-[220px] max-w-[320px] shrink-0 items-center gap-2">
      <span className={cn("text-[11px]", usage.blocked ? "text-destructive" : "text-muted-foreground")}>
        上下文 {usage.used_tokens.toLocaleString()} / {usage.max_tokens.toLocaleString()}
      </span>
      <span className="h-1.5 min-w-20 flex-1 overflow-hidden rounded-full bg-slate-100">
        <span
          className={cn("block h-full rounded-full", usage.blocked ? "bg-destructive" : "bg-slate-900")}
          style={{ width: `${percent}%` }}
        />
      </span>
      {usage.blocked ? (
        <button
          type="button"
          className="shrink-0 text-[11px] font-semibold text-destructive hover:underline"
          onClick={onNewConversation}
          title="创建一个新对话继续"
        >
          新对话
        </button>
      ) : null}
    </span>
  );
}

function KnowledgeTaskCommandBar({
  actions,
  busy,
  onRun,
  onUpload,
}: {
  actions: KnowledgeTaskAction[];
  busy: boolean;
  onRun: (action: KnowledgeTaskAction) => void;
  onUpload: () => void;
}) {
  return (
    <div className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1">
      <KnowledgeTaskButton
        icon={Upload}
        label="上传并摄入"
        title="选择文件并交给知识库助手按 INGEST 流程摄入"
        disabled={busy}
        onClick={onUpload}
      />
      {actions.map((action) => (
        <KnowledgeTaskButton
          key={action.id}
          icon={action.icon}
          label={action.label}
          title={action.title}
          disabled={busy}
          onClick={() => onRun(action)}
        />
      ))}
    </div>
  );
}

function KnowledgeTaskButton({
  icon: Icon,
  label,
  title,
  disabled,
  onClick,
}: {
  icon: typeof Activity;
  label: string;
  title: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="inline-flex h-7 items-center gap-1.5 rounded-md px-1 text-xs font-medium text-slate-600 transition hover:bg-slate-100 hover:text-slate-950 disabled:cursor-not-allowed disabled:opacity-50 dark:text-muted-foreground dark:hover:bg-secondary dark:hover:text-foreground"
      title={title}
      disabled={disabled}
      onClick={onClick}
    >
      <Icon className="h-3.5 w-3.5" />
      {label}
    </button>
  );
}

function defaultSyncMessage(status: SyncStatusResponse) {
  const count = status.files.length;
  return count > 0 ? `更新 Wiki 内容（${count} 个文件）` : "同步 Wiki 内容";
}

function nextAdminSessionState(
  current: AdminSessionState | undefined,
  mode: string,
  reply: string,
  details: Record<string, unknown>,
  execution?: { steps?: Array<{ tool?: string }> },
): AdminSessionState {
  const state = normalizeAdminSessionState(current);
  const uploadedPaths = uniqueStrings([
    ...state.uploadedPaths,
    stringValue(details, "stored_path"),
    ...stringArrayValue(details, "output_files"),
  ]);
  const outputFiles = uniqueStrings([
    ...state.lastOutputFiles,
    stringValue(details, "output_file"),
    ...stringArrayValue(details, "output_files"),
  ]);
  const commands = uniqueStrings([
    ...state.lastCommands,
    ...commandValues(details["commands"]),
  ]).slice(-12);
  const artifacts = uniqueStrings([
    ...state.lastArtifacts,
    stringValue(details, "report_file"),
    ...outputFiles,
  ]).slice(-12);
  const summary = firstNonEmpty(
    stringValue(details, "summary"),
    stringValue(details, "answer"),
    reply,
    state.lastSummary,
  );
  return {
    uploadedPaths: uploadedPaths.slice(-12),
    lastReply: firstNonEmpty(reply, state.lastReply),
    lastSummary: summary,
    lastMode: firstNonEmpty(mode, state.lastMode),
    lastReportFile: firstNonEmpty(
      stringValue(details, "report_file"),
      state.lastReportFile,
    ),
    lastOutputFiles: outputFiles.slice(-12),
    lastCommands: commands,
    lastArtifacts: artifacts,
  };
}

function stringValue(record: Record<string, unknown>, key: string) {
  const value = record[key];
  return typeof value === "string" ? value : "";
}

function stringArrayValue(record: Record<string, unknown>, key: string) {
  const value = record[key];
  return Array.isArray(value)
    ? value.filter(
        (item): item is string =>
          typeof item === "string" && item.trim() !== "",
      )
    : [];
}

function commandValues(value: unknown) {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item) => {
      if (!item || typeof item !== "object" || Array.isArray(item)) {
        return "";
      }
      const record = item as Record<string, unknown>;
      return typeof record.command === "string" ? record.command : "";
    })
    .filter((item) => item.trim() !== "");
}

function uniqueStrings(values: Array<string | null | undefined>) {
  return Array.from(
    new Set(
      values.filter(
        (value): value is string =>
          typeof value === "string" && value.trim() !== "",
      ),
    ),
  );
}

function executionSteps(value: Record<string, unknown>): Array<{ tool?: string }> {
  const steps = value.steps;
  return Array.isArray(steps) ? (steps as Array<{ tool?: string }>) : [];
}

function firstNonEmpty(...values: Array<string | null | undefined>) {
  for (const value of values) {
    if (typeof value === "string" && value.trim() !== "") {
      return value;
    }
  }
  return "";
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

function summarizePromptEvent(value: unknown) {
  const event = asRecord(value);
  const messages = Array.isArray(event.messages) ? event.messages : [];
  return {
    name: event.name,
    model: event.model,
    messages: messages.map((message) => {
      const item = asRecord(message);
      return {
        role: item.role,
        content: summarizePromptMessage(
          String(item.role ?? ""),
          String(item.content ?? ""),
        ),
      };
    }),
  };
}

function summarizePromptMessage(role: string, content: string) {
  const text = content.trim();
  if (role !== "user" || text === "") {
    return truncateText(text, 1200);
  }
  const requestMatch = text.match(/\n\n(?:当前请求|管理员请求)：([\s\S]*)$/);
  if (!requestMatch) {
    return truncateText(text, 1200);
  }
  const request = requestMatch[1]?.trim() ?? "";
  const prefix = text.slice(0, requestMatch.index).trim();
  const lines = [request ? `当前请求：${request}` : "当前请求："];
  const stateMatch = prefix.match(
    /(?:^|\n\n)会话状态：\n([\s\S]*?)(?=\n\n(?:会话上下文：|最近对话：|当前附件：)|$)/,
  );
  if (stateMatch) {
    const count = stateMatch[1]
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean).length;
    if (count > 0) {
      lines.push(`[会话状态已折叠：${count} 行]`);
    }
  }
  const historyMatch = prefix.match(
    /(?:^|\n\n)(?:会话上下文|最近对话)：\n([\s\S]*?)(?=\n\n当前附件：|$)/,
  );
  if (historyMatch) {
    const count = historyMatch[1]
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean).length;
    if (count > 0) {
      lines.push(`[历史上下文已折叠：${count} 条]`);
    }
  }
  const attachmentMatch = prefix.match(/(?:^|\n\n)当前附件：\n([\s\S]*)$/);
  if (attachmentMatch) {
    const count = attachmentMatch[1]
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean).length;
    if (count > 0) {
      lines.push(`[附件上下文已折叠：${count} 项]`);
    }
  }
  return truncateText(lines.join("\n\n"), 1200);
}

function summarizeStepEvent(value: unknown) {
  const event = asRecord(value);
  return {
    name: event.name,
    tool: event.tool,
    status: event.status,
    duration_ms: event.duration_ms,
    input: sanitizePayload(event.input),
    output: sanitizePayload(event.output),
  };
}

function sanitizePayload(value: unknown): unknown {
  if (value == null) {
    return value;
  }
  if (typeof value === "string") {
    return truncateText(value, 800);
  }
  if (Array.isArray(value)) {
    return value.slice(0, 12).map((item) => sanitizePayload(item));
  }
  if (typeof value === "object") {
    const object = asRecord(value);
    return Object.fromEntries(
      Object.entries(object)
        .slice(0, 16)
        .map(([key, item]) => [key, sanitizePayload(item)]),
    );
  }
  return value;
}

function truncateText(value: string, maxLength: number) {
  const text = value.trim();
  if (text.length <= maxLength) {
    return text;
  }
  return `${text.slice(0, maxLength)}\n\n[truncated]`;
}

function chunkTextForTyping(value: string, size: number) {
  const runes = Array.from(value);
  if (runes.length === 0) {
    return [];
  }
  const chunks: string[] = [];
  for (let start = 0; start < runes.length; start += size) {
    chunks.push(runes.slice(start, start + size).join(""));
  }
  return chunks;
}

function messageStatusText(message: AdminMessage) {
  if (message.role !== "assistant") {
    return "";
  }
  switch (message.status) {
    case "pending":
      return "正在处理请求...";
    case "streaming":
      return "正在执行会话...";
    case "cancelled":
      return "本次会话已停止。";
    case "error":
      return "本次请求失败。";
    default:
      return "";
  }
}
