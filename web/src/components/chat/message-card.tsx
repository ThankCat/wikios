"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { MessageDetails } from "@/components/chat/message-details";
import { cn } from "@/lib/utils";

type Props = {
  role: "user" | "assistant";
  content: string;
  details?: unknown;
};

export function MessageCard({ role, content, details }: Props) {
  return (
    <div className={cn("flex w-full", role === "user" ? "justify-end" : "justify-start")}>
      <div className={role === "user" ? "chat-bubble-user" : "chat-bubble-assistant"}>
        {role === "assistant" ? (
          <div className="prose prose-sm max-w-none prose-p:my-2 prose-ul:my-2 prose-headings:mb-2 prose-headings:mt-4">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
          </div>
        ) : (
          <div className="whitespace-pre-wrap">{content}</div>
        )}
        {details ? (
          <details
            className={cn(
              "mt-3 rounded-2xl border px-3 py-2 text-xs",
              role === "user" ? "border-white/20 bg-white/5" : "border-slate-200 bg-slate-50/80",
            )}
          >
            <summary className="cursor-pointer select-none opacity-80">查看详情</summary>
            <MessageDetails details={details} />
          </details>
        ) : null}
      </div>
    </div>
  );
}
