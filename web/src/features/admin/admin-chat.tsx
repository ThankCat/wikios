"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  LogOut,
  PanelLeft,
  PanelLeftClose,
  Paperclip,
  SendHorizontal,
  Sparkles,
  Trash2,
  Wrench,
} from "lucide-react";

import { ChatDetailDrawer } from "@/components/chat/chat-detail-drawer";
import { ConversationSidebar, type ConversationItem } from "@/components/chat/conversation-sidebar";
import { MessageCard } from "@/components/chat/message-card";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Textarea } from "@/components/ui/textarea";
import { api, APIError, isAbortError } from "@/lib/api";
import { createId } from "@/lib/id";
import { cn } from "@/lib/utils";
import type { AdminChatRequest, AdminChatResponse, AdminStreamEvent, UploadStreamEvent } from "@/types/api";

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
const HISTORY_LIMIT = 8;

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
        const role: AdminMessage["role"] = stringValue(item, "role") === "assistant" ? "assistant" : "user";
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
    stream: typeof conversation.stream === "boolean" ? conversation.stream : true,
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
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const typingTimersRef = useRef<Record<string, number>>({});
  const activeRequestRef = useRef<AbortController | null>(null);
  const [busyLabel, setBusyLabel] = useState("");

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
    const savedWidth = Number(localStorage.getItem(drawerWidthStorageKey) ?? "");
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
    () => conversations.find((item) => item.id === activeId) ?? conversations[0],
    [activeId, conversations],
  );
  const selectedDetail = useMemo(
    () => activeConversation?.messages.find((message) => message.id === selectedDetailId && message.details) ?? null,
    [activeConversation, selectedDetailId],
  );

  useEffect(() => {
    viewportRef.current?.scrollTo({ top: viewportRef.current.scrollHeight, behavior: "smooth" });
  }, [activeConversation?.messages]);

  useEffect(
    () => () => {
      activeRequestRef.current?.abort();
      Object.values(typingTimersRef.current).forEach((timer) => window.clearTimeout(timer));
      typingTimersRef.current = {};
    },
    [],
  );

  function startDrawerResize() {
    const handleMove = (event: MouseEvent) => {
      const nextWidth = Math.min(960, Math.max(320, window.innerWidth - event.clientX));
      setDrawerWidth(nextWidth);
    };
    const handleUp = () => {
      window.removeEventListener("mousemove", handleMove);
      window.removeEventListener("mouseup", handleUp);
    };
    window.addEventListener("mousemove", handleMove);
    window.addEventListener("mouseup", handleUp);
  }

  async function send(messageOverride?: string, overrides?: Partial<AdminChatRequest>) {
    const text = (messageOverride ?? composer).trim();
    if (!activeConversation || !text || busy) {
      return;
    }
    const userMessage: AdminMessage = { id: createId(), role: "user", content: text };
    appendMessage(activeConversation.id, userMessage);
    setComposer("");
    setError("");
    setBusy(true);
    const stream = overrides?.stream ?? activeConversation.stream;
    setBusyLabel(stream ? "正在执行管理员会话..." : "正在处理管理员请求...");
    const request: AdminChatRequest = {
      message: text,
      stream,
      mode_hint: overrides?.mode_hint,
      context: {
        last_mode: activeConversation.lastMode,
        session_state: normalizeAdminSessionState(activeConversation.sessionState),
        ...(overrides?.context ?? {}),
      },
      attachments: overrides?.attachments,
      history: conversationHistory(activeConversation.messages),
    };
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
        await api.adminChatStream(request, (event) => handleStreamEvent(activeConversation.id, assistantId, event), controller.signal);
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
      applySessionStatePatch(activeConversation.id, response.mode, response.reply, response.details, response.execution);
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

  function handleStreamEvent(conversationId: string, assistantId: string, event: AdminStreamEvent) {
    if (event.type === "meta") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      updateLastMode(conversationId, String(data.mode ?? "query"));
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
      appendEventDetail(conversationId, assistantId, "prompts", summarizePromptEvent(event.data), 8);
      return;
    }
    if (event.type === "result") {
      const data = event.data as AdminChatResponse;
      applySessionStatePatch(conversationId, data.mode, data.reply, data.details, data.execution);
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
      appendEventDetail(conversationId, assistantId, "steps", summarizeStepEvent(event.data), 40);
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

  async function animateAssistantReply(conversationId: string, messageId: string, text: string) {
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
    updates: { content?: string | ((prev: string) => string); status?: MessageStatus; details?: unknown },
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
              typeof updates.content === "function" ? updates.content(message.content) : updates.content ?? message.content;
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

  function mergeDetails(conversationId: string, messageId: string, patch: Record<string, unknown>) {
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

  function appendEventDetail(conversationId: string, messageId: string, key: string, value: unknown, maxItems = 24) {
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
        item.id === conversationId ? { ...item, messages: [...item.messages, message] } : item,
      ),
    );
  }

  function renameConversation(conversationId: string, title: string) {
    setConversations((current) =>
      current.map((item) =>
        item.id === conversationId ? { ...item, title: title.slice(0, 24) } : item,
      ),
    );
  }

  function updateLastMode(conversationId: string, mode: string) {
    setConversations((current) =>
      current.map((item) =>
        item.id === conversationId
          ? (() => {
              const sessionState = normalizeAdminSessionState(item.sessionState);
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
        const nextState = nextAdminSessionState(item.sessionState, mode, reply, details, execution);
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
      await api.uploadStream(file, (streamEvent) => handleUploadStreamEvent(activeConversation.id, assistantId, streamEvent), controller.signal);
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
          reason instanceof APIError && reason.payload && typeof reason.payload === "object"
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

  function handleUploadStreamEvent(conversationId: string, assistantId: string, event: UploadStreamEvent) {
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
        content: total > 1 ? `已完成文件拆分，共 ${total} 段，准备开始逐段分析。` : "文件已准备完成，开始分析。",
        status: "streaming",
      });
      setBusyLabel(total > 1 ? `已拆分为 ${total} 段，准备开始逐段分析...` : "开始分析内容...");
      return;
    }
    if (event.type === "segment_start") {
      const data = asRecord(event.data);
      appendEventDetail(conversationId, assistantId, "segment_timeline", sanitizePayload(data), 60);
      mergeDetails(conversationId, assistantId, {
        current_segment: data,
      });
      const index = numberValue(data, "index");
      const total = numberValue(data, "total");
      const title = String(data.title ?? "");
      patchMessage(conversationId, assistantId, {
        content: total > 0 ? `正在分析第 ${index}/${total} 段：${title}` : `正在分析分段：${title}`,
        status: "streaming",
      });
      setBusyLabel(total > 0 ? `正在分析第 ${index}/${total} 段...` : "正在分析分段...");
      return;
    }
    if (event.type === "prompt") {
      appendEventDetail(conversationId, assistantId, "prompts", summarizePromptEvent(event.data), 16);
      return;
    }
    if (event.type === "step_start" || event.type === "step_finish") {
      appendEventDetail(conversationId, assistantId, "steps", summarizeStepEvent(event.data), 80);
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
      appendEventDetail(conversationId, assistantId, "segment_results", sanitizePayload(data), 80);
      const index = numberValue(data, "index");
      const total = numberValue(data, "total");
      const title = String(data.title ?? "");
      patchMessage(conversationId, assistantId, {
        content: total > 0 ? `已完成第 ${index}/${total} 段落库：${title}` : `已完成分段落库：${title}`,
        status: "streaming",
      });
      setBusyLabel(total > 0 ? `已完成第 ${index}/${total} 段，继续处理后续分段...` : "继续处理后续分段...");
      return;
    }
    if (event.type === "segment_error") {
      const data = asRecord(event.data);
      appendEventDetail(conversationId, assistantId, "failed_segments", sanitizePayload(data), 80);
      const index = numberValue(data, "index");
      const total = numberValue(data, "total");
      patchMessage(conversationId, assistantId, {
        content:
          total > 0
            ? `第 ${index}/${total} 段处理失败，继续执行后续分段。`
            : "有分段处理失败，继续执行后续分段。",
        status: "streaming",
      });
      setBusyLabel(total > 0 ? `第 ${index}/${total} 段失败，继续处理后续分段...` : "有分段失败，继续处理后续分段...");
      return;
    }
    if (event.type === "result") {
      const data = asRecord(event.data);
      const reply = String(data.reply ?? "");
      const details = asRecord(data.details);
      const execution = asRecord(data.execution);
      applySessionStatePatch(conversationId, "ingest", reply, details, execution as { steps?: Array<{ tool?: string }> });
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

  const sidebarItems: ConversationItem[] = conversations.map((item) => ({ id: item.id, title: item.title }));

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
                <Button type="button" variant="ghost" size="sm" onClick={() => setSidebarOpen((value) => !value)}>
                  {sidebarOpen ? <PanelLeftClose className="mr-2 h-4 w-4" /> : <PanelLeft className="mr-2 h-4 w-4" />}
                  {sidebarOpen ? "隐藏会话" : "显示会话"}
                </Button>
                <div>
                  <h1 className="text-lg font-semibold">管理员对话工作台</h1>
                  <p className="mt-1 text-sm text-muted-foreground">上传摄入、健康检查和修复都在同一会话里完成。</p>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="outline" size="sm" onClick={() => fileInputRef.current?.click()} disabled={busy}>
                  <Paperclip className="mr-2 h-4 w-4" />
                  上传并摄入
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void send("执行一次健康检查", { mode_hint: "lint" })}
                  disabled={busy}
                >
                  <Activity className="mr-2 h-4 w-4" />
                  健康检查
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void send("请做一次综合反思分析", { mode_hint: "reflect" })}
                  disabled={busy}
                >
                  <Sparkles className="mr-2 h-4 w-4" />
                  Reflect
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void send("请尝试自动修复当前上下文中的低风险问题", { mode_hint: "repair" })}
                  disabled={busy}
                >
                  <Wrench className="mr-2 h-4 w-4" />
                  Repair
                </Button>
                <Button variant="ghost" size="sm" disabled title="Merge 暂未开放快捷入口，请通过对话显式提出">
                  Merge
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => activeConversation && deleteConversation(activeConversation.id)}
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
              accept=".txt,.md,.markdown,.json,.doc,.docx,.rtf,.png,.jpg,.jpeg,.webp"
              onChange={handleUpload}
            />
          </header>
          <ScrollArea viewportRef={viewportRef} className="min-h-0 flex-1 px-6 py-5">
            <div className="mx-auto flex max-w-3xl flex-col gap-4 pb-8">
              {activeConversation?.messages.map((message) => (
                <MessageCard
                  key={message.id}
                  id={message.id}
                  role={message.role}
                  content={message.content || "处理中..."}
                  pending={message.status === "pending" || message.status === "streaming"}
                  statusText={messageStatusText(message)}
                  details={message.details}
                  selected={selectedDetailId === message.id}
                  onInspect={({ id }) => setSelectedDetailId(id)}
                />
              ))}
            </div>
          </ScrollArea>
          <div className="border-t px-6 py-5">
            <div className="mx-auto max-w-3xl">
              <div className="mb-2 flex items-center justify-between text-xs text-muted-foreground">
                <label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={Boolean(activeConversation?.stream)}
                    onChange={(event) => {
                      setConversations((current) =>
                        current.map((item) =>
                          item.id === activeConversation?.id ? { ...item, stream: event.target.checked } : item,
                        ),
                      );
                    }}
                  />
                  流式返回
                </label>
                <span>{error || busyLabel || "支持多轮上下文，也会在详情里显示执行命令和方法。"}</span>
              </div>
              <div className="rounded-[28px] border bg-white p-3 shadow-soft">
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
                <div className="mt-3 flex items-center justify-end">
                  <div className="flex items-center gap-2">
                    {busy ? (
                      <Button type="button" variant="outline" onClick={stopActiveRequest}>
                        停止
                      </Button>
                    ) : null}
                    <Button onClick={() => void send()} disabled={busy}>
                      <SendHorizontal className="mr-2 h-4 w-4" />
                      {busy ? "处理中" : "发送"}
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
            selectedDetail ? { role: selectedDetail.role, content: selectedDetail.content, details: selectedDetail.details } : null
          }
          onClear={() => setSelectedDetailId("")}
          onResizeStart={startDrawerResize}
        />
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

function conversationHistory(messages: AdminMessage[]) {
  return messages
    .filter((message) => message.content.trim() !== "")
    .slice(-HISTORY_LIMIT)
    .map((message) => ({ role: message.role, content: message.content }));
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
    lastReportFile: firstNonEmpty(stringValue(details, "report_file"), state.lastReportFile),
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
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string" && item.trim() !== "") : [];
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
      values.filter((value): value is string => typeof value === "string" && value.trim() !== ""),
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
        content: summarizePromptMessage(String(item.role ?? ""), String(item.content ?? "")),
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
  const stateMatch = prefix.match(/(?:^|\n\n)会话状态：\n([\s\S]*?)(?=\n\n(?:会话上下文：|最近对话：|当前附件：)|$)/);
  if (stateMatch) {
    const count = stateMatch[1]
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean).length;
    if (count > 0) {
      lines.push(`[会话状态已折叠：${count} 行]`);
    }
  }
  const historyMatch = prefix.match(/(?:^|\n\n)(?:会话上下文|最近对话)：\n([\s\S]*?)(?=\n\n当前附件：|$)/);
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
