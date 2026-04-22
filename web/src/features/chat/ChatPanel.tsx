import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { MessageSquareText, Send } from "lucide-react";
import type { PublicAnswerResponse } from "@/types/api";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";

export type ChatEntry = {
  id: string;
  question: string;
  answer?: PublicAnswerResponse;
  error?: string;
};

type Props = {
  chats: ChatEntry[];
  onChatsChange: (items: ChatEntry[]) => void;
  onSelectAnswer: (answer: PublicAnswerResponse | null) => void;
};

export function ChatPanel({ chats, onChatsChange, onSelectAnswer }: Props) {
  const [question, setQuestion] = useState("");
  const [loading, setLoading] = useState(false);

  async function submitQuestion() {
    if (!question.trim()) {
      return;
    }
    const draft: ChatEntry = {
      id: crypto.randomUUID(),
      question: question.trim(),
    };
    onChatsChange([draft, ...chats]);
    setLoading(true);
    try {
      const answer = await api.publicAnswer({
        question: draft.question,
        user_id: "web-user",
        session_id: draft.id,
        context: { channel: "workbench" },
      });
      const next = [draft, ...chats].map((item) =>
        item.id === draft.id ? { ...item, answer } : item,
      );
      onChatsChange(next);
      onSelectAnswer(answer);
      setQuestion("");
    } catch (error) {
      const next = [draft, ...chats].map((item) =>
        item.id === draft.id ? { ...item, error: error instanceof Error ? error.message : "request failed" } : item,
      );
      onChatsChange(next);
    } finally {
      setLoading(false);
    }
  }

  return (
    <Card className="flex h-full flex-col">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <MessageSquareText className="h-5 w-5 text-primary" />
          用户对话测试
        </CardTitle>
        <CardDescription>直接调用 public answer，模拟真实对话端的测试体验。</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-4">
        <div className="flex gap-3">
          <Input
            placeholder="例如：知识库系统规则是什么？"
            value={question}
            onChange={(event) => setQuestion(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void submitQuestion();
              }
            }}
          />
          <Button onClick={() => void submitQuestion()} disabled={loading}>
            <Send className="mr-2 h-4 w-4" />
            提问
          </Button>
        </div>

        <ScrollArea className="flex-1 pr-3">
          <div className="space-y-4">
            {chats.length === 0 ? (
              <div className="rounded-3xl border border-dashed border-border p-8 text-sm text-muted-foreground">
                在这里输入问题，测试用户端对话返回和来源展示。
              </div>
            ) : (
              chats.map((chat) => (
                <div key={chat.id} className="space-y-3 rounded-3xl border border-border bg-white/70 p-4">
                  <div className="rounded-2xl bg-slate-900 px-4 py-3 text-sm text-slate-100">{chat.question}</div>
                  {chat.error ? (
                    <div className="rounded-2xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
                      {chat.error}
                    </div>
                  ) : chat.answer ? (
                    <button
                      className="w-full rounded-2xl border border-border px-4 py-3 text-left transition hover:border-primary/40"
                      onClick={() => onSelectAnswer(chat.answer ?? null)}
                      type="button"
                    >
                      <div className="prose prose-slate max-w-none text-sm">
                        <ReactMarkdown remarkPlugins={[remarkGfm]}>
                          {chat.answer.answer_markdown}
                        </ReactMarkdown>
                      </div>
                    </button>
                  ) : (
                    <div className="rounded-2xl border border-border px-4 py-3 text-sm text-muted-foreground">
                      正在等待回答...
                    </div>
                  )}
                </div>
              ))
            )}
          </div>
        </ScrollArea>
      </CardContent>
    </Card>
  );
}

