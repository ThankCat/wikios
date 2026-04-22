"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { SendHorizontal, Trash2 } from "lucide-react";

import { ConversationSidebar, type ConversationItem } from "@/components/chat/conversation-sidebar";
import { MessageCard } from "@/components/chat/message-card";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { PublicAnswerResponse, PublicStreamEvent } from "@/types/api";

type UserMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  details?: unknown;
};

type UserConversation = {
  id: string;
  title: string;
  messages: UserMessage[];
  stream: boolean;
};

const storageKey = "wikios.user.chat";

export function UserChat() {
  const [conversations, setConversations] = useState<UserConversation[]>([]);
  const [activeId, setActiveId] = useState("");
  const [composer, setComposer] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const viewportRef = useRef<HTMLDivElement | null>(null);

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

  async function sendMessage() {
    const question = composer.trim();
    if (!question || !activeConversation) {
      return;
    }
    setError("");
    setComposer("");
    setBusy(true);
    const userMessage: UserMessage = { id: crypto.randomUUID(), role: "user", content: question };
    appendMessage(activeConversation.id, userMessage);
    const history = conversationHistory(activeConversation.messages);
    if (activeConversation.stream) {
      const assistantId = crypto.randomUUID();
      appendMessage(activeConversation.id, { id: assistantId, role: "assistant", content: "" });
      try {
        await api.publicAnswerStream(question, history, (event) => handleStreamEvent(activeConversation.id, assistantId, event));
        renameConversation(activeConversation.id, question);
      } catch (reason) {
        setError(reason instanceof Error ? reason.message : "请求失败");
      } finally {
        setBusy(false);
      }
      return;
    }
    try {
      const response = await api.publicAnswer(question, history);
      applyPublicResponse(activeConversation.id, response);
      renameConversation(activeConversation.id, question);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "请求失败");
    } finally {
      setBusy(false);
    }
  }

  function handleStreamEvent(conversationId: string, messageId: string, event: PublicStreamEvent) {
    if (event.type === "delta") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, messageId, {
        content: (prev) => `${prev}${String(data.delta ?? "")}`,
      });
      return;
    }
    if (event.type === "result") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      patchMessage(conversationId, messageId, {
        content: String(data.answer ?? ""),
        details: data.details,
      });
      return;
    }
    if (event.type === "error") {
      const data = (event.data ?? {}) as Record<string, unknown>;
      setError(String(data.message ?? "请求失败"));
      patchMessage(conversationId, messageId, {
        content: "暂时无法处理这条请求，请稍后再试。",
        details: data,
      });
      setBusy(false);
      return;
    }
    if (event.type === "done") {
      setBusy(false);
    }
  }

  function applyPublicResponse(conversationId: string, response: PublicAnswerResponse) {
    appendMessage(conversationId, {
      id: crypto.randomUUID(),
      role: "assistant",
      content: response.answer,
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
    <div className="chat-shell">
      <ConversationSidebar
        title="用户对话"
        subtitle="面向客户的知识库问答测试页"
        variant="user"
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
              <h1 className="text-lg font-semibold">知识库客服对话</h1>
              <p className="mt-1 text-sm text-muted-foreground">仅展示可直接面向客户的回答内容。</p>
            </div>
            <Button variant="ghost" size="sm" onClick={() => activeConversation && deleteConversation(activeConversation.id)}>
              <Trash2 className="mr-2 h-4 w-4" />
              删除会话
            </Button>
          </div>
        </header>
        <ScrollArea ref={viewportRef} className="min-h-0 flex-1 px-6 py-5">
          <div className="mx-auto flex max-w-3xl flex-col gap-4">
            {activeConversation?.messages.map((message) => (
              <MessageCard
                key={message.id}
                role={message.role}
                content={message.content}
                details={message.role === "assistant" ? message.details : undefined}
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
              <span>{error || "按 Enter 发送，Shift + Enter 换行"}</span>
            </div>
            <div className="rounded-[28px] border bg-white p-3 shadow-soft">
              <Textarea
                value={composer}
                onChange={(event) => setComposer(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.shiftKey) {
                    event.preventDefault();
                    void sendMessage();
                  }
                }}
                className="min-h-[88px] resize-none border-0 bg-transparent p-2 shadow-none focus-visible:ring-0"
                placeholder="请输入客户问题"
              />
              <div className="mt-3 flex items-center justify-between">
                <span className="text-xs text-muted-foreground">会话支持多轮上下文。</span>
                <Button onClick={() => void sendMessage()} disabled={busy}>
                  <SendHorizontal className="mr-2 h-4 w-4" />
                  {busy ? "发送中" : "发送"}
                </Button>
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
    id: crypto.randomUUID(),
    title,
    messages: [],
    stream: true,
  };
}

function conversationHistory(messages: UserMessage[]) {
  return messages
    .filter((message) => message.content.trim() !== "")
    .slice(-8)
    .map((message) => ({ role: message.role, content: message.content }));
}
