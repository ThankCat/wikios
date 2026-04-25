"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { cn } from "@/lib/utils";

const markdownComponents = {
  a({ children, href }: React.ComponentPropsWithoutRef<"a">) {
    return (
      <a href={href} target="_blank" rel="noreferrer" className="break-words font-medium text-blue-700 underline underline-offset-2">
        {children}
      </a>
    );
  },
  h1({ children }: React.ComponentPropsWithoutRef<"h1">) {
    return <h1 className="mb-3 mt-1 text-xl font-semibold leading-8 text-slate-950">{children}</h1>;
  },
  h2({ children }: React.ComponentPropsWithoutRef<"h2">) {
    return <h2 className="mb-2 mt-5 border-b border-slate-200 pb-1 text-lg font-semibold leading-7 text-slate-950">{children}</h2>;
  },
  h3({ children }: React.ComponentPropsWithoutRef<"h3">) {
    return <h3 className="mb-2 mt-4 text-base font-semibold leading-6 text-slate-900">{children}</h3>;
  },
  p({ children }: React.ComponentPropsWithoutRef<"p">) {
    return <p className="my-2 leading-7 text-slate-800">{children}</p>;
  },
  ul({ children }: React.ComponentPropsWithoutRef<"ul">) {
    return <ul className="my-2 list-disc space-y-1 pl-5 text-slate-800">{children}</ul>;
  },
  ol({ children }: React.ComponentPropsWithoutRef<"ol">) {
    return <ol className="my-2 list-decimal space-y-1 pl-5 text-slate-800">{children}</ol>;
  },
  li({ children }: React.ComponentPropsWithoutRef<"li">) {
    return <li className="pl-1 leading-7 marker:text-slate-400">{children}</li>;
  },
  strong({ children }: React.ComponentPropsWithoutRef<"strong">) {
    return <strong className="font-semibold text-slate-950">{children}</strong>;
  },
  code({ children }: React.ComponentPropsWithoutRef<"code">) {
    return <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[0.92em] text-slate-900">{children}</code>;
  },
  pre({ children }: React.ComponentPropsWithoutRef<"pre">) {
    return <pre className="my-3 overflow-x-auto rounded-lg bg-slate-950 p-3 text-xs leading-6 text-slate-100">{children}</pre>;
  },
  blockquote({ children }: React.ComponentPropsWithoutRef<"blockquote">) {
    return <blockquote className="my-3 border-l-4 border-slate-300 pl-4 text-slate-600">{children}</blockquote>;
  },
  hr() {
    return <hr className="my-4 border-slate-200" />;
  },
  table({ children }: React.ComponentPropsWithoutRef<"table">) {
    return (
      <div className="my-3 w-full overflow-x-auto rounded-lg border border-slate-200">
        <table className="min-w-full border-collapse text-left text-sm">{children}</table>
      </div>
    );
  },
  thead({ children }: React.ComponentPropsWithoutRef<"thead">) {
    return <thead className="bg-slate-50 text-slate-700">{children}</thead>;
  },
  th({ children }: React.ComponentPropsWithoutRef<"th">) {
    return <th className="whitespace-nowrap border-b border-slate-200 px-3 py-2 font-semibold">{children}</th>;
  },
  td({ children }: React.ComponentPropsWithoutRef<"td">) {
    return <td className="border-t border-slate-100 px-3 py-2 align-top">{children}</td>;
  },
  tr({ children }: React.ComponentPropsWithoutRef<"tr">) {
    return <tr className="even:bg-slate-50/60">{children}</tr>;
  },
};

type Props = {
  children: string;
  className?: string;
};

export function MarkdownContent({ children, className }: Props) {
  return (
    <div className={cn("markdown-content", className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
        {children}
      </ReactMarkdown>
    </div>
  );
}
