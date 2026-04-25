"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  ChevronDown,
  Database,
  Download,
  FileText,
  GitBranch,
  GitMerge,
  LogOut,
  PanelLeft,
  PanelLeftClose,
  Paperclip,
  Plus,
  RefreshCw,
  Save,
  SendHorizontal,
  Sparkles,
  Trash2,
  Wrench,
  X,
} from "lucide-react";

import { ChatDetailDrawer } from "@/components/chat/chat-detail-drawer";
import {
  ConversationSidebar,
  type ConversationItem,
} from "@/components/chat/conversation-sidebar";
import { MessageCard } from "@/components/chat/message-card";
import { Button } from "@/components/ui/button";
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
  PublicIntentsStatus,
  SyncCommitResponse,
  SyncStatusResponse,
  UploadStreamEvent,
} from "@/types/api";

type MessageStatus = "pending" | "streaming" | "done" | "error" | "cancelled";

type AdminMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
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

const storageKey = "wikios.admin.chat";
const sidebarStorageKey = "wikios.admin.sidebar.open";
const drawerWidthStorageKey = "wikios.admin.detail.width";

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
          details: item.details,
        });
        return acc;
      }, [])
    : [];
  return {
    id,
    title: firstNonEmpty(stringValue(conversation, "title"), "管理员会话"),
    messages,
    stream:
      typeof conversation.stream === "boolean" ? conversation.stream : true,
    lastMode: firstNonEmpty(stringValue(conversation, "lastMode"), "query"),
    sessionState: normalizeAdminSessionState(conversation.sessionState),
  };
}

export function AdminChat({ username }: { username: string }) {
  const [conversations, setConversations] = useState<AdminConversation[]>([]);
  const [activeId, setActiveId] = useState("");
  const [composer, setComposer] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [selectedDetailId, setSelectedDetailId] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [drawerWidth, setDrawerWidth] = useState(460);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const typingTimersRef = useRef<Record<string, number>>({});
  const activeRequestRef = useRef<AbortController | null>(null);
  const [busyLabel, setBusyLabel] = useState("");
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
  const [syncResult, setSyncResult] = useState<SyncCommitResponse | null>(null);
  const [syncError, setSyncError] = useState("");
  const [toolsOpen, setToolsOpen] = useState(true);

  useEffect(() => {
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
          setConversations(normalized);
          setActiveId(normalized[0].id);
          return;
        }
      } catch {}
    }
    const initial = createConversation("管理员会话");
    setConversations([initial]);
    setActiveId(initial.id);
  }, []);

  useEffect(() => {
    const raw = localStorage.getItem(sidebarStorageKey);
    if (raw === "0") {
      setSidebarOpen(false);
    }
    const savedWidth = Number(
      localStorage.getItem(drawerWidthStorageKey) ?? "",
    );
    if (Number.isFinite(savedWidth) && savedWidth >= 320 && savedWidth <= 960) {
      setDrawerWidth(savedWidth);
    }
  }, []);

  useEffect(() => {
    if (conversations.length === 0) {
      return;
    }
    const timer = window.setTimeout(() => {
      localStorage.setItem(storageKey, JSON.stringify(conversations));
    }, 180);
    return () => window.clearTimeout(timer);
  }, [conversations]);

  useEffect(() => {
    localStorage.setItem(sidebarStorageKey, sidebarOpen ? "1" : "0");
  }, [sidebarOpen]);

  useEffect(() => {
    localStorage.setItem(drawerWidthStorageKey, String(drawerWidth));
  }, [drawerWidth]);

  const activeConversation = useMemo(
    () =>
      conversations.find((item) => item.id === activeId) ?? conversations[0],
    [activeId, conversations],
  );
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
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      const request = buildAdminRequest(activeConversation, composer, {});
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
  }, [activeConversation, contextEstimateKey]);

  useEffect(
    () => () => {
      activeRequestRef.current?.abort();
      Object.values(typingTimersRef.current).forEach((timer) =>
        window.clearTimeout(timer),
      );
      typingTimersRef.current = {};
    },
    [],
  );

  function startDrawerResize() {
    const handleMove = (event: MouseEvent) => {
      const nextWidth = Math.min(
        960,
        Math.max(320, window.innerWidth - event.clientX),
      );
      setDrawerWidth(nextWidth);
    };
    const handleUp = () => {
      window.removeEventListener("mousemove", handleMove);
      window.removeEventListener("mouseup", handleUp);
    };
    window.addEventListener("mousemove", handleMove);
    window.addEventListener("mouseup", handleUp);
  }

  async function send(
    messageOverride?: string,
    overrides?: Partial<AdminChatRequest>,
  ) {
    const text = (messageOverride ?? composer).trim();
    if (!activeConversation || !text || busy) {
      return;
    }
    const stream = overrides?.stream ?? activeConversation.stream;
    const request = buildAdminRequest(activeConversation, text, {
      ...overrides,
      stream,
    });
    const estimate = await api.estimateAdminContext(request).catch(() => null);
    if (estimate?.context_usage.blocked) {
      setContextUsage(estimate.context_usage);
      setError("当前对话已接近上下文上限，请创建新的对话继续。");
      return;
    }
    if (estimate?.context_usage) {
      setContextUsage(estimate.context_usage);
    }
    const userMessage: AdminMessage = {
      id: createId(),
      role: "user",
      content: text,
    };
    appendMessage(activeConversation.id, userMessage);
    setComposer("");
    setError("");
    setBusy(true);
    setBusyLabel(stream ? "正在执行管理员会话..." : "正在处理管理员请求...");
    const controller = new AbortController();
    activeRequestRef.current = controller;
    if (stream) {
      const assistantId = createId();
      appendMessage(activeConversation.id, {
        id: assistantId,
        role: "assistant",
        content: "",
        status: "streaming",
        details: { prompts: [], steps: [] },
      });
      try {
        await api.adminChatStream(
          request,
          (event) =>
            handleStreamEvent(activeConversation.id, assistantId, event),
          controller.signal,
        );
        renameConversation(activeConversation.id, text);
      } catch (reason) {
        if (isAbortError(reason)) {
          patchMessage(activeConversation.id, assistantId, {
            content: (prev) => prev || "已停止当前会话。",
            status: "cancelled",
          });
        } else {
          setError(reason instanceof Error ? reason.message : "请求失败");
          patchMessage(activeConversation.id, assistantId, {
            content: "执行失败，请稍后重试。",
            status: "error",
          });
        }
      } finally {
        activeRequestRef.current = null;
        setBusy(false);
        setBusyLabel("");
      }
      return;
    }
    const assistantId = createId();
    appendMessage(activeConversation.id, {
      id: assistantId,
      role: "assistant",
      content: "",
      status: "pending",
      details: { steps: [] },
    });
    try {
      const response = await api.adminChat(request, controller.signal);
      applySessionStatePatch(
        activeConversation.id,
        response.mode,
        response.reply,
        response.details,
        response.execution,
      );
      patchMessage(activeConversation.id, assistantId, {
        content: response.reply,
        status: "done",
        details: {
          result: response.details,
          execution: response.execution,
          steps: response.execution?.steps ?? [],
        },
      });
      updateLastMode(activeConversation.id, response.mode);
      renameConversation(activeConversation.id, text);
    } catch (reason) {
      if (isAbortError(reason)) {
        patchMessage(activeConversation.id, assistantId, {
          content: "已取消本次管理员请求。",
          status: "cancelled",
        });
      } else {
        setError(reason instanceof Error ? reason.message : "请求失败");
        patchMessage(activeConversation.id, assistantId, {
          content: "执行失败，请稍后重试。",
          status: "error",
        });
      }
    } finally {
      activeRequestRef.current = null;
      setBusy(false);
      setBusyLabel("");
    }
  }

  function stopActiveRequest() {
    activeRequestRef.current?.abort();
    activeRequestRef.current = null;
    setBusy(false);
    setBusyLabel("");
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

  async function pushSyncCommit() {
    if (!syncStatus) {
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
      setBusy(false);
      setBusyLabel("");
      activeRequestRef.current = null;
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
    if (event.type === "llm_done") {
      mergeDetails(conversationId, assistantId, { llm_done: event.data });
      return;
    }
    if (event.type === "done") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, assistantId, {
        status: "done",
      });
      mergeDetails(conversationId, assistantId, { execution: data.execution });
      activeRequestRef.current = null;
      setBusy(false);
      setBusyLabel("");
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
    const next = createConversation("管理员会话");
    setConversations((current) => [next, ...current]);
    setActiveId(next.id);
    setSelectedDetailId("");
    setError("");
  }

  function deleteConversation(id: string) {
    setConversations((current) => {
      const remaining = current.filter((item) => item.id !== id);
      if (remaining.length === 0) {
        const fallback = createConversation("管理员会话");
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

  async function handleUpload(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file || !activeConversation) {
      return;
    }
    setBusy(true);
    setError("");
    setBusyLabel("正在上传并摄入文件...");
    appendMessage(activeConversation.id, {
      id: createId(),
      role: "user",
      content: `上传文件：${file.name}`,
    });
    const assistantId = createId();
    appendMessage(activeConversation.id, {
      id: assistantId,
      role: "assistant",
      content: "",
      status: "pending",
      details: {
        phase: "upload",
        file_name: file.name,
      },
    });
    const controller = new AbortController();
    activeRequestRef.current = controller;
    try {
      await api.uploadStream(
        file,
        (streamEvent) =>
          handleUploadStreamEvent(
            activeConversation.id,
            assistantId,
            streamEvent,
          ),
        controller.signal,
      );
    } catch (reason) {
      if (isAbortError(reason)) {
        patchMessage(activeConversation.id, assistantId, {
          content: "已取消上传和摄入。",
          status: "cancelled",
        });
      } else {
        const message = reason instanceof Error ? reason.message : "上传失败";
        setError(message);
        const errorDetails =
          reason instanceof APIError &&
          reason.payload &&
          typeof reason.payload === "object"
            ? ((reason.payload as { details?: unknown }).details ?? {
                error: message,
                kind: "upload_validation",
              })
            : {
                error: message,
                kind: "upload_validation",
              };
        patchMessage(activeConversation.id, assistantId, {
          content: message,
          status: "error",
          details: errorDetails,
        });
      }
    } finally {
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
      activeRequestRef.current = null;
      setBusy(false);
      setBusyLabel("");
    }
  }

  function handleUploadStreamEvent(
    conversationId: string,
    assistantId: string,
    event: UploadStreamEvent,
  ) {
    if (event.type === "meta") {
      const data = asRecord(event.data);
      mergeDetails(conversationId, assistantId, {
        execution: {
          id: data.execution_id,
          kind: data.mode ?? "ingest",
          status: "RUNNING",
          started_at: data.started_at,
        },
        file_name: data.file_name,
        media_kind: data.media_kind,
        stored_path: data.stored_path,
        source_format: data.source_format,
      });
      patchMessage(conversationId, assistantId, {
        content: "正在拆分文件...",
        status: "streaming",
      });
      setBusyLabel("正在拆分文件...");
      return;
    }
    if (event.type === "ingest_plan") {
      const data = asRecord(event.data);
      const total = numberValue(data, "segments_total");
      mergeDetails(conversationId, assistantId, {
        ingest_plan: data,
        segments_total: total,
      });
      patchMessage(conversationId, assistantId, {
        content:
          total > 1
            ? `已完成文件拆分，共 ${total} 段，准备开始逐段分析。`
            : "文件已准备完成，开始分析。",
        status: "streaming",
      });
      setBusyLabel(
        total > 1
          ? `已拆分为 ${total} 段，准备开始逐段分析...`
          : "开始分析内容...",
      );
      return;
    }
    if (event.type === "segment_start") {
      const data = asRecord(event.data);
      appendEventDetail(
        conversationId,
        assistantId,
        "segment_timeline",
        sanitizePayload(data),
        60,
      );
      mergeDetails(conversationId, assistantId, {
        current_segment: data,
      });
      const index = numberValue(data, "index");
      const total = numberValue(data, "total");
      const title = String(data.title ?? "");
      patchMessage(conversationId, assistantId, {
        content:
          total > 0
            ? `正在分析第 ${index}/${total} 段：${title}`
            : `正在分析分段：${title}`,
        status: "streaming",
      });
      setBusyLabel(
        total > 0 ? `正在分析第 ${index}/${total} 段...` : "正在分析分段...",
      );
      return;
    }
    if (event.type === "prompt") {
      appendEventDetail(
        conversationId,
        assistantId,
        "prompts",
        summarizePromptEvent(event.data),
        16,
      );
      return;
    }
    if (event.type === "step_start" || event.type === "step_finish") {
      appendEventDetail(
        conversationId,
        assistantId,
        "steps",
        summarizeStepEvent(event.data),
        80,
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
    if (event.type === "llm_done") {
      mergeDetails(conversationId, assistantId, { llm_done: event.data });
      return;
    }
    if (event.type === "segment_result") {
      const data = asRecord(event.data);
      appendEventDetail(
        conversationId,
        assistantId,
        "segment_results",
        sanitizePayload(data),
        80,
      );
      const index = numberValue(data, "index");
      const total = numberValue(data, "total");
      const title = String(data.title ?? "");
      patchMessage(conversationId, assistantId, {
        content:
          total > 0
            ? `已完成第 ${index}/${total} 段落库：${title}`
            : `已完成分段落库：${title}`,
        status: "streaming",
      });
      setBusyLabel(
        total > 0
          ? `已完成第 ${index}/${total} 段，继续处理后续分段...`
          : "继续处理后续分段...",
      );
      return;
    }
    if (event.type === "segment_error") {
      const data = asRecord(event.data);
      appendEventDetail(
        conversationId,
        assistantId,
        "failed_segments",
        sanitizePayload(data),
        80,
      );
      const index = numberValue(data, "index");
      const total = numberValue(data, "total");
      patchMessage(conversationId, assistantId, {
        content:
          total > 0
            ? `第 ${index}/${total} 段处理失败，继续执行后续分段。`
            : "有分段处理失败，继续执行后续分段。",
        status: "streaming",
      });
      setBusyLabel(
        total > 0
          ? `第 ${index}/${total} 段失败，继续处理后续分段...`
          : "有分段失败，继续处理后续分段...",
      );
      return;
    }
    if (event.type === "result") {
      const data = asRecord(event.data);
      const reply = String(data.reply ?? "");
      const details = asRecord(data.details);
      const execution = asRecord(data.execution);
      applySessionStatePatch(
        conversationId,
        "ingest",
        reply,
        details,
        execution as { steps?: Array<{ tool?: string }> },
      );
      patchMessage(conversationId, assistantId, {
        content: reply,
        status: execution.status === "FAILED" ? "error" : "done",
      });
      mergeDetails(conversationId, assistantId, {
        result: details,
        execution,
        steps: Array.isArray(execution.steps) ? execution.steps : [],
      });
      updateLastMode(conversationId, "ingest");
      return;
    }
    if (event.type === "error") {
      const data = asRecord(event.data);
      const message = String(data.message ?? "上传摄入失败");
      patchMessage(conversationId, assistantId, {
        content: `执行失败：${message}`,
        status: "error",
      });
      mergeDetails(conversationId, assistantId, { error: data });
      setError(message);
      return;
    }
    if (event.type === "done") {
      const data = asRecord(event.data);
      mergeDetails(conversationId, assistantId, { execution: data.execution });
      activeRequestRef.current = null;
      setBusy(false);
      setBusyLabel("");
    }
  }

  const sidebarItems: ConversationItem[] = conversations.map((item) => ({
    id: item.id,
    title: item.title,
  }));

  return (
    <div className={cn("chat-shell", !sidebarOpen && "chat-shell-collapsed")}>
      {sidebarOpen ? (
        <ConversationSidebar
          title="管理员后台"
          subtitle={`已登录：${username}`}
          variant="admin"
          items={sidebarItems}
          activeId={activeConversation?.id ?? ""}
          onSelect={setActiveId}
          onCreate={createNewConversation}
          onDelete={deleteConversation}
        />
      ) : null}
      <section className="panel-glass relative flex h-full min-h-0 flex-col overflow-hidden">
        <header className="border-b px-6 py-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex items-start gap-3">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setSidebarOpen((value) => !value)}
                title="显示或隐藏左侧会话列表"
              >
                {sidebarOpen ? (
                  <PanelLeftClose className="mr-2 h-4 w-4" />
                ) : (
                  <PanelLeft className="mr-2 h-4 w-4" />
                )}
                {sidebarOpen ? "隐藏会话" : "显示会话"}
              </Button>
              <div>
                <h1 className="text-lg font-semibold">管理员对话工作台</h1>
                <p className="mt-1 text-sm text-muted-foreground">
                  上传摄入、健康检查和修复都在同一会话里完成。
                </p>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
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
                onClick={() => window.open("/admin/wiki", "_blank")}
                title="打开 Wiki 资料库浏览器"
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
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  activeConversation &&
                  deleteConversation(activeConversation.id)
                }
                title="删除当前本地会话记录"
              >
                <Trash2 className="mr-2 h-4 w-4" />
                删除会话
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={async () => {
                  await api.logout();
                  window.location.href = "/admin/login";
                }}
                title="退出管理员登录"
              >
                <LogOut className="mr-2 h-4 w-4" />
                退出
              </Button>
            </div>
          </div>
          <input
            ref={fileInputRef}
            type="file"
            className="hidden"
            onChange={handleUpload}
          />
        </header>
        <div className="relative min-h-0 flex-1">
          <ScrollArea
            viewportRef={chatScroll.viewportRef}
            className="h-full px-6 py-5"
          >
            <div className="mx-auto flex max-w-3xl flex-col gap-4 pb-8">
              {activeConversation?.messages.map((message) => (
                <MessageCard
                  key={message.id}
                  id={message.id}
                  role={message.role}
                  content={message.content || "处理中..."}
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
        <div className="border-t px-6 py-5">
          <div className="mx-auto max-w-3xl">
            <div className="mb-2 text-right text-xs text-muted-foreground">
              {error ||
                busyLabel ||
                "支持多轮上下文，也会在详情里显示执行命令和方法。"}
            </div>
            <ContextUsageBar
              usage={contextUsage}
              loading={contextLoading}
              onNewConversation={createNewConversation}
            />
            <div className="rounded-[28px] border bg-white p-3 shadow-soft">
              {toolsOpen ? (
                <div className="mb-2 flex flex-wrap items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 rounded-full px-3"
                    onClick={() => fileInputRef.current?.click()}
                    disabled={busy}
                    title="选择文件并交给 server 摄入到 Wiki"
                  >
                    <Paperclip className="mr-2 h-4 w-4" />
                    上传并摄入
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 rounded-full px-3"
                    onClick={() =>
                      void send("执行一次健康检查", { mode_hint: "lint" })
                    }
                    disabled={busy}
                    title="按 Wiki 的 LINT 规则执行健康检查"
                  >
                    <Activity className="mr-2 h-4 w-4" />
                    健康检查
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 rounded-full px-3"
                    onClick={() =>
                      void send("请做一次综合反思分析", {
                        mode_hint: "reflect",
                      })
                    }
                    disabled={busy}
                    title="让 LLM 按 Wiki 的 REFLECT 规则做综合分析"
                  >
                    <Sparkles className="mr-2 h-4 w-4" />
                    综合分析
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 rounded-full px-3"
                    onClick={() =>
                      void send("请尝试自动修复当前上下文中的低风险问题", {
                        mode_hint: "repair",
                      })
                    }
                    disabled={busy}
                    title="让 LLM 按 Wiki 的 REPAIR 规则修复低风险问题"
                  >
                    <Wrench className="mr-2 h-4 w-4" />
                    修复问题
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 rounded-full px-3"
                    onClick={() =>
                      void send(
                        "请根据 MERGE 操作规范检查当前上下文中的可合并或去重项，只给出合并方案，不要自动执行合并。",
                        {
                          mode_hint: "merge",
                        },
                      )
                    }
                    disabled={busy}
                    title="让 LLM 按 Wiki 的 MERGE 规则提出合并方案，不自动合并"
                  >
                    <GitMerge className="mr-2 h-4 w-4" />
                    合并冲突
                  </Button>
                </div>
              ) : null}
              <Textarea
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
                className="min-h-[88px] resize-none border-0 bg-transparent p-2 shadow-none focus-visible:ring-0"
                placeholder="请输入管理员指令或问题"
              />
              <div className="mt-3 flex flex-wrap items-center justify-between gap-3">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-8 w-8 rounded-full"
                    onClick={() => setToolsOpen((value) => !value)}
                    title={toolsOpen ? "隐藏输入框工具栏" : "显示输入框工具栏"}
                  >
                    {toolsOpen ? (
                      <ChevronDown className="h-4 w-4" />
                    ) : (
                      <Plus className="h-4 w-4" />
                    )}
                  </Button>
                  <span className="rounded-full border border-orange-200 bg-orange-50 px-3 py-1 text-xs font-medium text-orange-700">
                    完全访问权限
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  <div
                    className="flex rounded-full border bg-slate-50 p-0.5"
                    title="选择本次管理员回复方式"
                  >
                    <button
                      type="button"
                      className={cn(
                        "rounded-full px-3 py-1 text-xs transition",
                        activeConversation?.stream
                          ? "bg-white text-slate-950 shadow-sm"
                          : "text-muted-foreground hover:text-slate-950",
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
                          ? "bg-white text-slate-950 shadow-sm"
                          : "text-muted-foreground hover:text-slate-950",
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
                  {busy ? (
                    <Button
                      type="button"
                      variant="outline"
                      onClick={stopActiveRequest}
                      title="停止当前正在执行的管理员请求"
                    >
                      停止
                    </Button>
                  ) : null}
                  <Button
                    className="h-10 w-10 rounded-full p-0"
                    onClick={() => void send()}
                    disabled={busy || Boolean(contextUsage?.blocked)}
                    title={
                      contextUsage?.blocked
                        ? "当前对话已达到上下文上限，请新建会话"
                        : "发送管理员指令"
                    }
                  >
                    <SendHorizontal className="h-4 w-4" />
                    <span className="sr-only">{busy ? "处理中" : "发送"}</span>
                  </Button>
                </div>
              </div>
            </div>
          </div>
        </div>
        <ChatDetailDrawer
          title="执行详情"
          open={Boolean(selectedDetail)}
          width={drawerWidth}
          selected={
            selectedDetail
              ? {
                  role: selectedDetail.role,
                  content: selectedDetail.content,
                  details: selectedDetail.details,
                }
              : null
          }
          onClear={() => setSelectedDetailId("")}
          onResizeStart={startDrawerResize}
        />
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
                                  `/admin/wiki?path=${encodeURIComponent(file.path)}`,
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
                  <label className="text-xs font-semibold text-slate-600">
                    提交信息
                  </label>
                  <input
                    value={syncMessage}
                    onChange={(event) => setSyncMessage(event.target.value)}
                    className="h-10 w-full rounded-md border border-input bg-white px-3 text-sm"
                    placeholder="例如：更新 Wiki 内容"
                    title="本次 Git commit 的提交信息"
                  />
                </div>
                <div className="mt-3 text-xs text-muted-foreground">
                  已选择 {selectedSyncPaths.length} 个文件。
                  {syncResult ? `最近提交：${syncResult.hash}` : ""}
                </div>
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
                  disabled={syncBusy}
                  onClick={() => void pushSyncCommit()}
                  title="把当前分支推送到配置的远端"
                >
                  推送
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
    .map((message) => ({ role: message.role, content: message.content }));
}

function ContextUsageBar({
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
      <div className="mb-3 rounded-xl border border-slate-200 bg-white px-3 py-2 text-xs text-muted-foreground">
        {loading ? "正在计算上下文..." : "上下文用量暂不可用"}
      </div>
    );
  }
  const percent =
    usage.max_tokens > 0
      ? Math.min(100, Math.round((usage.used_tokens / usage.max_tokens) * 100))
      : 0;
  return (
    <div className="mb-3 rounded-xl border border-slate-200 bg-white px-3 py-2">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>
          上下文：已用 {usage.used_tokens.toLocaleString()} /{" "}
          {usage.max_tokens.toLocaleString()}，剩余{" "}
          {usage.remaining_tokens.toLocaleString()}
          {usage.estimated ? "（估算）" : ""}
        </span>
        {usage.blocked ? (
          <button
            type="button"
            className="font-semibold text-destructive hover:underline"
            onClick={onNewConversation}
            title="创建一个新对话继续"
          >
            创建新对话
          </button>
        ) : null}
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-slate-100">
        <div
          className={cn(
            "h-full rounded-full",
            usage.blocked ? "bg-destructive" : "bg-slate-900",
          )}
          style={{ width: `${percent}%` }}
        />
      </div>
      {usage.blocked ? (
        <div className="mt-2 text-xs text-destructive">
          当前对话已接近上下文上限，请创建新的对话继续。
        </div>
      ) : null}
    </div>
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

function numberValue(record: Record<string, unknown>, key: string) {
  const value = record[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
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
