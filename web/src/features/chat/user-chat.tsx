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
  status?: MessageStatus;
};

type UserConversation = {
  id: string;
  title: string;
  messages: UserMessage[];
  stream: boolean;
};

const storageKey = "wikios.user.chat";
const sidebarStorageKey = "wikios.user.sidebar.open";
const HISTORY_LIMIT = 8;

export function UserChat() {
  const [conversations, setConversations] = useState<UserConversation[]>([]);
  const [activeId, setActiveId] = useState("");
  const [composer, setComposer] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const activeRequestRef = useRef<AbortController | null>(null);
  const activeAssistantIdRef = useRef("");
  const [busyLabel, setBusyLabel] = useState("");

  useEffect(() => {
    const raw = localStorage.getItem(storageKey);
    if (raw) {
      try {
        const parsed = JSON.parse(raw) as UserConversation[];
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
  const chatScroll = useScrollFollow<HTMLDivElement>([activeId, activeConversation?.messages]);

  useEffect(() => {
    chatScroll.scrollToBottom("auto");
  }, [activeId, chatScroll.scrollToBottom]);

  async function sendMessage() {
    const question = composer.trim();
    if (!question || !activeConversation || busy) {
      return;
    }
    setError("");
    setComposer("");
    setBusy(true);
    setBusyLabel("正在生成回答...");
    const userMessage: UserMessage = { id: createId(), role: "user", content: question };
    appendMessage(activeConversation.id, userMessage);
    const history = conversationHistory(activeConversation.messages);
    const controller = new AbortController();
    activeRequestRef.current = controller;
    if (activeConversation.stream) {
      const assistantId = createId();
      activeAssistantIdRef.current = assistantId;
      appendMessage(activeConversation.id, { id: assistantId, role: "assistant", content: "", status: "streaming" });
      try {
        await api.publicAnswerStream(
          question,
          history,
          (event) => handleStreamEvent(activeConversation.id, assistantId, event),
          controller.signal,
        );
        renameConversation(activeConversation.id, question);
      } catch (reason) {
        if (isAbortError(reason)) {
          patchMessage(activeConversation.id, assistantId, {
            content: (prev) => prev || "已停止生成。",
            status: "cancelled",
          });
        } else {
          setError(reason instanceof Error ? reason.message : "请求失败");
          patchMessage(activeConversation.id, assistantId, {
            content: "暂时无法处理这条请求，请稍后再试。",
            status: "error",
          });
        }
      } finally {
        activeRequestRef.current = null;
        activeAssistantIdRef.current = "";
        setBusy(false);
        setBusyLabel("");
      }
      return;
    }
    const assistantId = createId();
    activeAssistantIdRef.current = assistantId;
    appendMessage(activeConversation.id, { id: assistantId, role: "assistant", content: "", status: "pending" });
    try {
      const response = await api.publicAnswer(question, history, controller.signal);
      applyPublicResponse(activeConversation.id, assistantId, response);
      renameConversation(activeConversation.id, question);
    } catch (reason) {
      if (isAbortError(reason)) {
        patchMessage(activeConversation.id, assistantId, {
          content: "已取消本次请求。",
          status: "cancelled",
        });
      } else {
        setError(reason instanceof Error ? reason.message : "请求失败");
        patchMessage(activeConversation.id, assistantId, {
          content: "暂时无法处理这条请求，请稍后再试。",
          status: "error",
        });
      }
    } finally {
      activeRequestRef.current = null;
      activeAssistantIdRef.current = "";
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

  function handleStreamEvent(conversationId: string, messageId: string, event: PublicStreamEvent) {
    if (event.type === "delta") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, messageId, {
        content: (prev) => `${prev}${String(data.delta ?? "")}`,
        status: "streaming",
      });
      return;
    }
    if (event.type === "result") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, messageId, {
        content: String(data.answer ?? ""),
        status: "done",
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
      setBusy(false);
      setBusyLabel("");
      activeRequestRef.current = null;
      activeAssistantIdRef.current = "";
      return;
    }
    if (event.type === "done") {
      patchMessage(conversationId, messageId, {
        status: "done",
      });
      setBusy(false);
      setBusyLabel("");
      activeRequestRef.current = null;
      activeAssistantIdRef.current = "";
    }
  }

  function applyPublicResponse(conversationId: string, messageId: string, response: PublicAnswerResponse) {
    patchMessage(conversationId, messageId, {
      content: response.answer,
      status: "done",
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
    updates: { content?: string | ((prev: string) => string); status?: MessageStatus },
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
            };
          }),
        };
      }),
    );
  }

  function createNewConversation() {
    const next = createConversation("新会话");
    setConversations((current) => [next, ...current]);
    setActiveId(next.id);
    setError("");
  }

  function deleteConversation(id: string) {
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
                    pending={message.status === "pending" || message.status === "streaming"}
                    statusText={messageStatusText(message)}
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
                <span>{error || busyLabel || "按 Enter 发送，Shift + Enter 换行"}</span>
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
    stream: true,
  };
}

function conversationHistory(messages: UserMessage[]) {
  return messages
    .filter((message) => message.content.trim() !== "")
    .slice(-HISTORY_LIMIT)
    .map((message) => ({ role: message.role, content: message.content }));
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
