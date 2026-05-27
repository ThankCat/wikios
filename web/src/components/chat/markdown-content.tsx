"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { cn } from "@/lib/utils";

type MarkdownCodeProps = React.ComponentPropsWithoutRef<"code"> & {
  node?: unknown;
};

const markdownComponents = {
  a({ children, href }: React.ComponentPropsWithoutRef<"a">) {
    return (
      <a href={href} target="_blank" rel="noreferrer" className="break-words font-medium text-blue-700 underline underline-offset-2 dark:text-sky-300">
        {children}
      </a>
    );
  },
  h1({ children }: React.ComponentPropsWithoutRef<"h1">) {
    return <h1 className="mb-3 mt-1 text-xl font-semibold leading-8 text-slate-950 dark:text-foreground">{children}</h1>;
  },
  h2({ children }: React.ComponentPropsWithoutRef<"h2">) {
    return <h2 className="mb-2 mt-5 border-b border-slate-200 pb-1 text-lg font-semibold leading-7 text-slate-950 dark:border-border dark:text-foreground">{children}</h2>;
  },
  h3({ children }: React.ComponentPropsWithoutRef<"h3">) {
    return <h3 className="mb-2 mt-4 text-base font-semibold leading-6 text-slate-900 dark:text-foreground">{children}</h3>;
  },
  p({ children }: React.ComponentPropsWithoutRef<"p">) {
    return <p className="my-2 leading-7 text-slate-800 dark:text-foreground/90">{children}</p>;
  },
  ul({ children }: React.ComponentPropsWithoutRef<"ul">) {
    return <ul className="my-2 list-disc space-y-1 pl-5 text-slate-800 dark:text-foreground/90">{children}</ul>;
  },
  ol({ children }: React.ComponentPropsWithoutRef<"ol">) {
    return <ol className="my-2 list-decimal space-y-1 pl-5 text-slate-800 dark:text-foreground/90">{children}</ol>;
  },
  li({ children }: React.ComponentPropsWithoutRef<"li">) {
    return <li className="pl-1 leading-7 marker:text-slate-400 dark:marker:text-muted-foreground">{children}</li>;
  },
  strong({ children }: React.ComponentPropsWithoutRef<"strong">) {
    return <strong className="font-semibold text-slate-950 dark:text-foreground">{children}</strong>;
  },
  code({ children, className }: MarkdownCodeProps) {
    const isBlock = typeof children === "string" && children.includes("\n");
    if (isBlock) {
      return <code className={cn("block min-w-max whitespace-pre font-mono text-xs leading-6 text-slate-100", className)}>{children}</code>;
    }
    return <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[0.92em] text-slate-900 dark:bg-secondary dark:text-foreground">{children}</code>;
  },
  pre({ children }: React.ComponentPropsWithoutRef<"pre">) {
    return <pre className="my-3 overflow-x-auto rounded-lg bg-slate-950 p-3 text-xs leading-6 text-slate-100">{children}</pre>;
  },
  blockquote({ children }: React.ComponentPropsWithoutRef<"blockquote">) {
    return <blockquote className="my-3 border-l-4 border-slate-300 pl-4 text-slate-600 dark:border-border dark:text-muted-foreground">{children}</blockquote>;
  },
  hr() {
    return <hr className="my-4 border-slate-200 dark:border-border" />;
  },
  table({ children }: React.ComponentPropsWithoutRef<"table">) {
    return (
      <div className="my-3 w-full overflow-x-auto rounded-lg border border-slate-200 dark:border-border">
        <table className="min-w-full border-collapse text-left text-sm">{children}</table>
      </div>
    );
  },
  thead({ children }: React.ComponentPropsWithoutRef<"thead">) {
    return <thead className="bg-slate-50 text-slate-700 dark:bg-secondary dark:text-foreground">{children}</thead>;
  },
  th({ children }: React.ComponentPropsWithoutRef<"th">) {
    return <th className="whitespace-nowrap border-b border-slate-200 px-3 py-2 font-semibold dark:border-border">{children}</th>;
  },
  td({ children }: React.ComponentPropsWithoutRef<"td">) {
    return <td className="border-t border-slate-100 px-3 py-2 align-top dark:border-border">{children}</td>;
  },
  tr({ children }: React.ComponentPropsWithoutRef<"tr">) {
    return <tr className="even:bg-slate-50/60 dark:even:bg-secondary/40">{children}</tr>;
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
        {normalizeBareLinks(children)}
      </ReactMarkdown>
    </div>
  );
}

function normalizeBareLinks(markdown: string) {
  let inFence = false;
  let fenceChar = "";
  let fenceLength = 0;

  return markdown.replace(/([^\r\n]*)(\r\n|\n|\r|$)/g, (match, line: string, lineEnding: string) => {
    if (match === "") {
      return match;
    }

    const fenceMatch = line.match(/^ {0,3}(`{3,}|~{3,})/);
    if (!inFence && fenceMatch) {
      fenceChar = fenceMatch[1][0];
      fenceLength = fenceMatch[1].length;
      inFence = true;
      return match;
    }

    if (inFence) {
      if (isClosingFence(line, fenceChar, fenceLength)) {
        inFence = false;
      }
      return match;
    }

    return `${normalizeBareLinksOutsideCodeSpans(line)}${lineEnding}`;
  });
}

function isClosingFence(line: string, marker: string, length: number) {
  if (!marker || length <= 0) {
    return false;
  }
  const trimmed = line.trimStart();
  if (line.length - trimmed.length > 3 || !trimmed.startsWith(marker.repeat(length))) {
    return false;
  }

  let index = 0;
  while (trimmed[index] === marker) {
    index += 1;
  }
  return index >= length && trimmed.slice(index).trim() === "";
}

function normalizeBareLinksOutsideCodeSpans(line: string) {
  let result = "";
  let index = 0;

  while (index < line.length) {
    if (line[index] === "`") {
      const tickRun = line.slice(index).match(/^`+/)?.[0] ?? "`";
      const closingIndex = line.indexOf(tickRun, index + tickRun.length);
      if (closingIndex !== -1) {
        const end = closingIndex + tickRun.length;
        result += line.slice(index, end);
        index = end;
        continue;
      }
      result += normalizeBareLinksInText(line.slice(index));
      break;
    }

    const nextCodeSpan = line.indexOf("`", index);
    const end = nextCodeSpan === -1 ? line.length : nextCodeSpan;
    result += normalizeBareLinksInText(line.slice(index, end));
    index = end;
  }

  return result;
}

function normalizeBareLinksInText(text: string) {
  return text.replace(/(https?:\/\/[A-Za-z0-9][^\s<>"'\u4e00-\u9fff，。！？；、（）【】《》]+)(?=[\u4e00-\u9fff，。！？；、）】》])/g, "$1 ");
}
