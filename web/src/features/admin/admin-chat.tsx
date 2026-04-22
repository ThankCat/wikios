"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Activity, LogOut, Paperclip, SendHorizontal, Sparkles, Trash2, Wrench } from "lucide-react";

import { ConversationSidebar, type ConversationItem } from "@/components/chat/conversation-sidebar";
import { MessageCard } from "@/components/chat/message-card";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { AdminChatRequest, AdminChatResponse, AdminStreamEvent } from "@/types/api";

type AdminMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  details?: unknown;
};

type AdminConversation = {
  id: string;
  title: string;
  messages: AdminMessage[];
  stream: boolean;
  lastMode: string;
};

const storageKey = "wikios.admin.chat";

export function AdminChat({ username }: { username: string }) {
  const [conversations, setConversations] = useState<AdminConversation[]>([]);
  const [activeId, setActiveId] = useState("");
  const [composer, setComposer] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const typingTimersRef = useRef<Record<string, number>>({});

  useEffect(() => {
    const raw = localStorage.getItem(storageKey);
    if (raw) {
      try {
        const parsed = JSON.parse(raw) as AdminConversation[];
        if (parsed.length > 0) {
          setConversations(parsed);
          setActiveId(parsed[0].id);
          return;
        }
      } catch {}
    }
    const initial = createConversation("管理员会话");
    setConversations([initial]);
    setActiveId(initial.id);
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

  const activeConversation = useMemo(
    () => conversations.find((item) => item.id === activeId) ?? conversations[0],
    [activeId, conversations],
  );

  useEffect(() => {
    viewportRef.current?.scrollTo({ top: viewportRef.current.scrollHeight, behavior: "smooth" });
  }, [activeConversation?.messages]);

  async function send(messageOverride?: string, overrides?: Partial<AdminChatRequest>) {
    const text = (messageOverride ?? composer).trim();
    if (!activeConversation || !text) {
      return;
    }
    const userMessage: AdminMessage = { id: crypto.randomUUID(), role: "user", content: text };
    appendMessage(activeConversation.id, userMessage);
    setComposer("");
    setError("");
    setBusy(true);
    const stream = overrides?.stream ?? activeConversation.stream;
    const request: AdminChatRequest = {
      message: text,
      stream,
      mode_hint: overrides?.mode_hint,
      context: {
        last_mode: activeConversation.lastMode,
        ...(overrides?.context ?? {}),
      },
      attachments: overrides?.attachments,
      history: conversationHistory(activeConversation.messages),
    };
    if (stream) {
      const assistantId = crypto.randomUUID();
      appendMessage(activeConversation.id, {
        id: assistantId,
        role: "assistant",
        content: "",
        details: { prompts: [], steps: [] },
      });
      try {
        await api.adminChatStream(request, (event) => handleStreamEvent(activeConversation.id, assistantId, event));
        renameConversation(activeConversation.id, text);
      } catch (reason) {
        setError(reason instanceof Error ? reason.message : "请求失败");
      } finally {
        setBusy(false);
      }
      return;
    }
    try {
      const response = await api.adminChat(request);
      appendMessage(activeConversation.id, {
        id: crypto.randomUUID(),
        role: "assistant",
        content: response.reply,
        details: {
          result: response.details,
          execution: response.execution,
          steps: response.execution?.steps ?? [],
        },
      });
      updateLastMode(activeConversation.id, response.mode);
      renameConversation(activeConversation.id, text);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "请求失败");
    } finally {
      setBusy(false);
    }
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
      patchMessage(conversationId, assistantId, {
        content: "",
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
      });
      mergeDetails(conversationId, assistantId, { error: data });
      setError(String(data.message ?? "执行失败"));
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
      mergeDetails(conversationId, assistantId, { execution: data.execution });
      setBusy(false);
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
    updates: { content?: string | ((prev: string) => string); details?: unknown },
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
        item.id === conversationId ? { ...item, lastMode: mode || item.lastMode } : item,
      ),
    );
  }

  function createNewConversation() {
    const next = createConversation("管理员会话");
    setConversations((current) => [next, ...current]);
    setActiveId(next.id);
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
    appendMessage(activeConversation.id, {
      id: crypto.randomUUID(),
      role: "user",
      content: `上传文件：${file.name}`,
    });
    try {
      const response = await api.upload(file);
      appendMessage(activeConversation.id, {
        id: crypto.randomUUID(),
        role: "assistant",
        content: response.reply,
        details: response.details,
      });
      updateLastMode(activeConversation.id, "ingest");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "上传失败");
    } finally {
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
      setBusy(false);
    }
  }

  const sidebarItems: ConversationItem[] = conversations.map((item) => ({ id: item.id, title: item.title }));

  return (
    <div className="chat-shell">
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
      <section className="panel-glass flex h-full min-h-0 flex-col overflow-hidden">
        <header className="border-b px-6 py-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h1 className="text-lg font-semibold">管理员对话工作台</h1>
              <p className="mt-1 text-sm text-muted-foreground">上传摄入、健康检查和修复都在同一会话里完成。</p>
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
            accept=".txt,.md,.markdown,.doc,.docx,.rtf,.png,.jpg,.jpeg,.webp"
            onChange={handleUpload}
          />
        </header>
        <ScrollArea ref={viewportRef} className="min-h-0 flex-1 px-6 py-5">
          <div className="mx-auto flex max-w-3xl flex-col gap-4">
            {activeConversation?.messages.map((message) => (
              <MessageCard key={message.id} role={message.role} content={message.content || "处理中..."} details={message.details} />
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
              <span>{error || "支持多轮上下文，也会在详情里显示执行命令和方法。"}</span>
            </div>
            <div className="rounded-[28px] border bg-white p-3 shadow-soft">
              <Textarea
                value={composer}
                onChange={(event) => setComposer(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.shiftKey) {
                    event.preventDefault();
                    void send();
                  }
                }}
                className="min-h-[88px] resize-none border-0 bg-transparent p-2 shadow-none focus-visible:ring-0"
                placeholder="请输入管理员指令或问题"
              />
              <div className="mt-3 flex items-center justify-end">
                <Button onClick={() => void send()} disabled={busy}>
                  <SendHorizontal className="mr-2 h-4 w-4" />
                  {busy ? "处理中" : "发送"}
                </Button>
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}

function createConversation(title: string): AdminConversation {
  return {
    id: crypto.randomUUID(),
    title,
    messages: [],
    stream: true,
    lastMode: "query",
  };
}

function conversationHistory(messages: AdminMessage[]) {
  return messages
    .filter((message) => message.content.trim() !== "")
    .slice(-8)
    .map((message) => ({ role: message.role, content: message.content }));
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
        content: truncateText(String(item.content ?? ""), 1200),
      };
    }),
  };
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
