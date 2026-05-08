"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { PanelLeft, PanelLeftClose, SendHorizontal, Trash2 } from "lucide-react";

import { ConversationSidebar, type ConversationItem } from "@/components/chat/conversation-sidebar";
import { MessageCard } from "@/components/chat/message-card";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ScrollJumpControls } from "@/components/ui/scroll-jump-controls";
import { Textarea } from "@/components/ui/textarea";
import { api, isAbortError } from "@/lib/api";
import { createId } from "@/lib/id";
import { useScrollFollow } from "@/lib/use-scroll-follow";
import { cn } from "@/lib/utils";
import type { PublicAnswerResponse, PublicStreamEvent } from "@/types/api";

type MessageStatus = "pending" | "streaming" | "done" | "error" | "cancelled";

type UserMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  created_at?: string;
  status?: MessageStatus;
  details?: unknown;
};

type UserConversation = {
  id: string;
  title: string;
  messages: UserMessage[];
  stream: boolean;
};

const storageKey = "wikios.user.chat";
const storageVersionKey = "wikios.user.chat.version";
const sidebarStorageKey = "wikios.user.sidebar.open";
const storageVersion = "2";
const HISTORY_LIMIT = 8;

export function UserChat() {
  const [conversations, setConversations] = useState<UserConversation[]>([]);
  const [activeId, setActiveId] = useState("");
  const [composer, setComposer] = useState("");
  const [error, setError] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const requestControllersRef = useRef<Record<string, AbortController>>({});
  const [requestLabels, setRequestLabels] = useState<Record<string, string>>({});

  useEffect(() => {
    const raw = localStorage.getItem(storageKey);
    if (raw) {
      try {
        const resetStoredStream = localStorage.getItem(storageVersionKey) !== storageVersion;
        const parsed = normalizeUserConversations(JSON.parse(raw), resetStoredStream);
        if (parsed.length > 0) {
          setConversations(parsed);
          setActiveId(parsed[0].id);
          return;
        }
      } catch {}
    }
    const initial = createConversation("新会话");
    setConversations([initial]);
    setActiveId(initial.id);
  }, []);

  useEffect(() => {
    const raw = localStorage.getItem(sidebarStorageKey);
    if (raw === "0") {
      setSidebarOpen(false);
    }
  }, []);

  useEffect(() => {
    if (conversations.length === 0) {
      return;
    }
    const timer = window.setTimeout(() => {
      localStorage.setItem(storageKey, JSON.stringify(conversations));
      localStorage.setItem(storageVersionKey, storageVersion);
    }, 180);
    return () => window.clearTimeout(timer);
  }, [conversations]);

  useEffect(() => {
    localStorage.setItem(sidebarStorageKey, sidebarOpen ? "1" : "0");
  }, [sidebarOpen]);

  const activeConversation = useMemo(
    () => conversations.find((item) => item.id === activeId) ?? conversations[0],
    [activeId, conversations],
  );
  const busyLabel = activeConversation ? (requestLabels[activeConversation.id] ?? "") : "";
  const busy = busyLabel !== "";
  const chatScroll = useScrollFollow<HTMLDivElement>([activeId, activeConversation?.messages]);

  useEffect(() => {
    chatScroll.scrollToBottom("auto");
  }, [activeId, chatScroll.scrollToBottom]);

  useEffect(
    () => () => {
      Object.values(requestControllersRef.current).forEach((controller) => controller.abort());
      requestControllersRef.current = {};
    },
    [],
  );

  async function sendMessage() {
    const question = composer.trim();
    if (!question || !activeConversation || busy) {
      return;
    }
    setError("");
    setComposer("");
    const questionCreatedAt = new Date().toISOString();
    const conversationId = activeConversation.id;
    const userMessage: UserMessage = { id: createId(), role: "user", content: question, created_at: questionCreatedAt };
    appendMessage(conversationId, userMessage);
    const history = conversationHistory(activeConversation.messages);
    const controller = new AbortController();
    startConversationRequest(conversationId, controller, "正在生成回答...");
    if (activeConversation.stream) {
      const assistantId = createId();
      appendMessage(conversationId, {
        id: assistantId,
        role: "assistant",
        content: "",
        created_at: new Date().toISOString(),
        status: "streaming",
        details: pendingPublicDetails(),
      });
      try {
        await api.publicAnswerStream(
          question,
          history,
          {
            session_id: conversationId,
            question_message_id: userMessage.id,
            answer_message_id: assistantId,
            question_created_at: questionCreatedAt,
          },
          (event) => handleStreamEvent(conversationId, assistantId, event),
          controller.signal,
        );
        renameConversation(conversationId, question);
      } catch (reason) {
        if (isAbortError(reason)) {
          patchMessage(conversationId, assistantId, {
            content: (prev) => prev || "已停止生成。",
            status: "cancelled",
          });
        } else {
          setError(reason instanceof Error ? reason.message : "请求失败");
          patchMessage(conversationId, assistantId, {
            content: "暂时无法处理这条请求，请稍后再试。",
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
      details: pendingPublicDetails(),
    });
    try {
      const response = await api.publicAnswer(
        question,
        history,
        {
          session_id: conversationId,
          question_message_id: userMessage.id,
          answer_message_id: assistantId,
          question_created_at: questionCreatedAt,
        },
        controller.signal,
      );
      applyPublicResponse(conversationId, assistantId, response);
      renameConversation(conversationId, question);
    } catch (reason) {
      if (isAbortError(reason)) {
        patchMessage(conversationId, assistantId, {
          content: "已取消本次请求。",
          status: "cancelled",
        });
      } else {
        setError(reason instanceof Error ? reason.message : "请求失败");
        patchMessage(conversationId, assistantId, {
          content: "暂时无法处理这条请求，请稍后再试。",
          status: "error",
        });
      }
    } finally {
      finishConversationRequest(conversationId, controller);
    }
  }

  function stopActiveRequest() {
    if (!activeConversation) {
      return;
    }
    const controller = requestControllersRef.current[activeConversation.id];
    controller?.abort();
    finishConversationRequest(activeConversation.id, controller);
  }

  function startConversationRequest(conversationId: string, controller: AbortController, label: string) {
    requestControllersRef.current[conversationId] = controller;
    setRequestLabels((current) => ({ ...current, [conversationId]: label }));
  }

  function finishConversationRequest(conversationId: string, controller?: AbortController) {
    if (controller && requestControllersRef.current[conversationId] !== controller) {
      return;
    }
    delete requestControllersRef.current[conversationId];
    setRequestLabels((current) => {
      if (!current[conversationId]) {
        return current;
      }
      const next = { ...current };
      delete next[conversationId];
      return next;
    });
  }

  function handleStreamEvent(conversationId: string, messageId: string, event: PublicStreamEvent) {
    if (event.type === "delta") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, messageId, {
        content: (prev) => `${prev}${String(data.delta ?? "")}`,
        status: "streaming",
      });
      return;
    }
    if (event.type === "llm_reasoning_delta") {
      const data = asRecord(event.data);
      appendDetailText(conversationId, messageId, "reasoning", String(data.delta ?? ""), 12000);
      appendEventDetail(
        conversationId,
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
    if (event.type === "step_start" || event.type === "step_finish") {
      appendEventDetail(conversationId, messageId, "steps", summarizeStepEvent(event.data), 40);
      return;
    }
    if (event.type === "result") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, messageId, {
        content: String(data.answer ?? ""),
        created_at: String(data.answered_at ?? ""),
        status: "done",
        details: (prev: unknown) => ({
          ...asRecord(prev),
          ...asRecord(data.details),
        }),
      });
      return;
    }
    if (event.type === "error") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      setError(String(data.message ?? "请求失败"));
      patchMessage(conversationId, messageId, {
        content: "暂时无法处理这条请求，请稍后再试。",
        status: "error",
      });
      return;
    }
    if (event.type === "done") {
      patchMessage(conversationId, messageId, {
        status: "done",
      });
    }
  }

  function applyPublicResponse(conversationId: string, messageId: string, response: PublicAnswerResponse) {
    patchMessage(conversationId, messageId, {
      content: response.answer,
      created_at: response.answered_at,
      status: "done",
      details: response.details,
    });
  }

  function appendMessage(conversationId: string, message: UserMessage) {
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
              typeof updates.content === "function" ? updates.content(message.content) : updates.content ?? message.content;
            return {
              ...message,
              content: nextContent,
              created_at: updates.created_at?.trim() ? updates.created_at : message.created_at,
              status: updates.status ?? message.status,
              details: "details" in updates ? resolveDetailUpdate(updates.details, message.details) : message.details,
            };
          }),
        };
      }),
    );
  }

  function appendDetailText(conversationId: string, messageId: string, key: string, text: string, maxLength: number) {
    if (text === "") {
      return;
    }
    patchMessage(conversationId, messageId, {
      details: (prev: unknown) => {
        const object = asRecord(prev);
        const current = typeof object[key] === "string" ? String(object[key]) : "";
        return { ...object, [key]: truncateText(`${current}${text}`, maxLength) };
      },
    });
  }

  function appendEventDetail(conversationId: string, messageId: string, key: string, value: unknown, limit: number) {
    patchMessage(conversationId, messageId, {
      details: (prev: unknown) => {
        const object = asRecord(prev);
        const current = Array.isArray(object[key]) ? object[key] : [];
        return { ...object, [key]: [...current, value].slice(-limit) };
      },
    });
  }

  function createNewConversation() {
    const next = createConversation("新会话");
    setConversations((current) => [next, ...current]);
    setActiveId(next.id);
    setError("");
  }

  function deleteConversation(id: string) {
    requestControllersRef.current[id]?.abort();
    finishConversationRequest(id, requestControllersRef.current[id]);
    setConversations((current) => {
      const remaining = current.filter((item) => item.id !== id);
      if (remaining.length === 0) {
        const fallback = createConversation("新会话");
        setActiveId(fallback.id);
        return [fallback];
      }
      if (activeId === id) {
        setActiveId(remaining[0].id);
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
    <div className={cn("chat-shell", !sidebarOpen && "chat-shell-collapsed")}>
      {sidebarOpen ? (
        <ConversationSidebar
          title="用户对话"
          subtitle="面向客户的客服问答页"
          variant="user"
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
                <h1 className="text-lg font-semibold">四叶天客服对话</h1>
                <p className="mt-1 text-sm text-muted-foreground">仅展示可直接面向客户的回答内容。</p>
                </div>
              </div>
              <Button variant="ghost" size="sm" onClick={() => activeConversation && deleteConversation(activeConversation.id)}>
                <Trash2 className="mr-2 h-4 w-4" />
                删除会话
              </Button>
            </div>
          </header>
          <div className="relative min-h-0 flex-1">
            <ScrollArea viewportRef={chatScroll.viewportRef} className="h-full px-6 py-5">
              <div className="mx-auto flex max-w-3xl flex-col gap-4 pb-8">
                {activeConversation?.messages.map((message) => (
                  <MessageCard
                    key={message.id}
                    id={message.id}
                    role={message.role}
                    content={message.content}
                    createdAt={message.created_at}
                    pending={message.status === "pending" || message.status === "streaming"}
                    statusText={messageStatusText(message)}
                    details={message.details}
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
              <div className="rounded-[28px] border bg-white p-3 shadow-soft">
                <div className="mb-2 flex items-center justify-between gap-3 px-1 text-xs text-muted-foreground">
                  <div className="flex rounded-full border bg-slate-50 p-0.5" title="选择本次 public 回复方式">
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
                            item.id === activeConversation?.id ? { ...item, stream: true } : item,
                          ),
                        );
                      }}
                      title="开启流式返回，边生成边展示回答"
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
                            item.id === activeConversation?.id ? { ...item, stream: false } : item,
                          ),
                        );
                      }}
                      title="关闭流式返回，等待完整结果后一次展示"
                    >
                      非流式
                    </button>
                  </div>
                  <span className="text-right">{error || busyLabel || "按 Enter 发送，Shift + Enter 换行"}</span>
                </div>
                <Textarea
                  value={composer}
                  onChange={(event) => setComposer(event.target.value)}
                  onKeyDown={(event) => {
                    if (busy) {
                      return;
                    }
                    if (event.key === "Enter" && !event.shiftKey) {
                      event.preventDefault();
                      void sendMessage();
                    }
                  }}
                  className="min-h-[88px] resize-none border-0 bg-transparent p-2 shadow-none focus-visible:ring-0"
                  placeholder="请输入客户问题"
                />
                <div className="mt-3 flex items-center justify-between">
                  <span className="text-xs text-muted-foreground">{busy ? "回答生成中，可随时停止。" : "会话支持多轮上下文。"}</span>
                  <div className="flex items-center gap-2">
                    {busy ? (
                      <Button type="button" variant="outline" onClick={stopActiveRequest}>
                        停止
                      </Button>
                    ) : null}
                    <Button onClick={() => void sendMessage()} disabled={busy}>
                      <SendHorizontal className="mr-2 h-4 w-4" />
                      {busy ? "发送中" : "发送"}
                    </Button>
                  </div>
                </div>
              </div>
            </div>
          </div>
      </section>
    </div>
  );
}

function createConversation(title: string): UserConversation {
  return {
    id: createId(),
    title,
    messages: [],
    stream: false,
  };
}

function pendingPublicDetails() {
  return {
    reasoning:
      "1. 正在检查问题是否属于可回答范围。\n2. 正在检索正式知识库证据。\n3. 将根据证据可信度生成用户可见回答。",
    steps: [],
  };
}

function normalizeUserConversations(value: unknown, resetStoredStream = false): UserConversation[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.reduce<UserConversation[]>((acc, conversation) => {
    if (!conversation || typeof conversation !== "object") {
      return acc;
    }
    const item = conversation as Partial<UserConversation>;
    const id = typeof item.id === "string" && item.id.trim() !== "" ? item.id : createId();
    const migrationTime = new Date().toISOString();
    const messages = Array.isArray(item.messages)
      ? item.messages.reduce<UserMessage[]>((messageAcc, message) => {
          if (!message || typeof message !== "object") {
            return messageAcc;
          }
          const raw = message as Partial<UserMessage>;
          const role = raw.role === "assistant" ? "assistant" : "user";
          messageAcc.push({
            id: typeof raw.id === "string" && raw.id.trim() !== "" ? raw.id : createId(),
            role,
            content: typeof raw.content === "string" ? raw.content : "",
            created_at: typeof raw.created_at === "string" && raw.created_at.trim() !== "" ? raw.created_at : migrationTime,
            status: raw.status,
            details: raw.details,
          });
          return messageAcc;
        }, [])
      : [];
    acc.push({
      id,
      title: typeof item.title === "string" && item.title.trim() !== "" ? item.title : "新会话",
      messages,
      stream: resetStoredStream ? false : typeof item.stream === "boolean" ? item.stream : false,
    });
    return acc;
  }, []);
}

function conversationHistory(messages: UserMessage[]) {
  return messages
    .filter((message) => message.content.trim() !== "")
    .slice(-HISTORY_LIMIT)
    .map((message) => ({
      id: message.id,
      role: message.role,
      content: message.content,
      created_at: message.created_at,
    }));
}

function lastMessageTime(messages: UserMessage[]) {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const createdAt = messages[index]?.created_at;
    if (createdAt) {
      return createdAt;
    }
  }
  return "";
}

function messageStatusText(message: UserMessage) {
  if (message.role !== "assistant") {
    return "";
  }
  switch (message.status) {
    case "pending":
      return "正在处理请求...";
    case "streaming":
      return "正在生成回答...";
    case "cancelled":
      return "本次会话已停止。";
    case "error":
      return "本次请求失败。";
    default:
      return "";
  }
}

function resolveDetailUpdate(update: unknown, previous: unknown) {
  if (typeof update === "function") {
    return (update as (prev: unknown) => unknown)(previous);
  }
  return update;
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

function truncateText(value: string, maxLength: number) {
  if (maxLength <= 0) {
    return value;
  }
  const runes = Array.from(value);
  if (runes.length <= maxLength) {
    return value;
  }
  return runes.slice(runes.length - maxLength).join("");
}

function summarizeStepEvent(value: unknown) {
  const data = asRecord(value);
  return {
    name: data.name,
    tool: data.tool,
    status: data.status,
    output: data.output,
    duration_ms: data.duration_ms,
    started_at: data.started_at,
    ended_at: data.ended_at,
  };
}
