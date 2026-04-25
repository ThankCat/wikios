"use client";

import { useEffect, useMemo, useState } from "react";
import { Download, File, Folder, RefreshCw } from "lucide-react";

import { MarkdownContent } from "@/components/chat/markdown-content";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { cn, formatJSON } from "@/lib/utils";
import type { WikiFileResponse, WikiTreeItem } from "@/types/api";

export default function AdminWikiPage() {
  const [path, setPath] = useState("");
  const [items, setItems] = useState<WikiTreeItem[]>([]);
  const [file, setFile] = useState<WikiFileResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const initialPath = params.get("path") ?? "";
    void openPath(initialPath);
  }, []);

  const breadcrumbs = useMemo(() => {
    const parts = path.split("/").filter(Boolean);
    const crumbs = [{ label: "资料库", path: "" }];
    let current = "";
    for (const part of parts) {
      current = current ? `${current}/${part}` : part;
      crumbs.push({ label: part, path: current });
    }
    return crumbs;
  }, [path]);

  async function openPath(nextPath: string) {
    setLoading(true);
    setError("");
    setPath(nextPath);
    window.history.replaceState(null, "", `/admin/wiki${nextPath ? `?path=${encodeURIComponent(nextPath)}` : ""}`);
    try {
      const tree = await api.wikiTree(nextPath);
      setItems(tree.items);
      setFile(null);
    } catch {
      try {
        const response = await api.wikiFile(nextPath);
        setFile(response);
        setItems([]);
      } catch (reason) {
        setError(reason instanceof Error ? reason.message : "读取资料库失败");
      }
    } finally {
      setLoading(false);
    }
  }

  const parentPath = path.split("/").slice(0, -1).join("/");

  return (
    <main className="flex min-h-screen flex-col bg-slate-50">
      <header className="border-b bg-white px-6 py-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-lg font-semibold">Wiki 资料库</h1>
            <div className="mt-1 flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
              {breadcrumbs.map((crumb, index) => (
                <button key={crumb.path || "root"} type="button" className="hover:text-slate-900" onClick={() => void openPath(crumb.path)} title={`打开 ${crumb.label}`}>
                  {index > 0 ? " / " : ""}
                  {crumb.label}
                </button>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-2">
            {path ? (
              <Button type="button" variant="outline" size="sm" onClick={() => void openPath(parentPath)} title="返回上一级目录">
                返回上级
              </Button>
            ) : null}
            <Button type="button" variant="outline" size="sm" onClick={() => void openPath(path)} title="重新读取当前路径">
              <RefreshCw className="mr-2 h-4 w-4" />
              刷新
            </Button>
          </div>
        </div>
      </header>
      <section className="grid min-h-0 flex-1 gap-4 p-6 lg:grid-cols-[360px_1fr]">
        <aside className="min-h-0 overflow-hidden rounded-xl border bg-white">
          <div className="border-b px-4 py-3 text-sm font-semibold">文件</div>
          <div className="max-h-[calc(100vh-150px)] overflow-y-auto">
            {loading ? <div className="px-4 py-6 text-sm text-muted-foreground">读取中...</div> : null}
            {error ? <div className="px-4 py-6 text-sm text-destructive">{error}</div> : null}
            {!loading && !error && items.length === 0 && !file ? (
              <div className="px-4 py-6 text-sm text-muted-foreground">目录为空。</div>
            ) : null}
            {items.map((item) => (
              <button
                key={item.path}
                type="button"
                className={cn("flex w-full items-center gap-3 border-b px-4 py-3 text-left text-sm hover:bg-slate-50", item.path === path && "bg-slate-50")}
                onClick={() => void openPath(item.path)}
                title={item.is_dir ? "打开目录" : item.preview === "download" ? "查看下载信息" : "在线查看文件"}
              >
                {item.is_dir ? <Folder className="h-4 w-4 text-slate-500" /> : <File className="h-4 w-4 text-slate-500" />}
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{item.name}</span>
                <span className="text-[11px] text-muted-foreground">{item.is_dir ? "目录" : item.preview}</span>
              </button>
            ))}
          </div>
        </aside>
        <section className="min-h-0 overflow-hidden rounded-xl border bg-white">
          {file ? <FilePreview file={file} /> : <div className="p-6 text-sm text-muted-foreground">请选择一个 Markdown、JSON、图片或其他文件。</div>}
        </section>
      </section>
    </main>
  );
}

function FilePreview({ file }: { file: WikiFileResponse }) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
        <div className="min-w-0">
          <div className="truncate font-mono text-sm font-semibold">{file.path}</div>
          <div className="mt-1 text-xs text-muted-foreground">
            {file.preview} · {file.size.toLocaleString()} bytes
          </div>
        </div>
        <a href={api.wikiDownloadURL(file.path)} target="_blank" rel="noreferrer" className="inline-flex h-9 items-center gap-2 rounded-md border px-3 text-sm" title="下载这个文件">
          <Download className="h-4 w-4" />
          下载
        </a>
      </header>
      <div className="min-h-0 flex-1 overflow-auto p-5">
        {file.preview === "markdown" ? (
          <MarkdownContent className="prose prose-slate max-w-none prose-table:my-0 prose-th:p-0 prose-td:p-0">
            {file.content ?? ""}
          </MarkdownContent>
        ) : null}
        {file.preview === "json" ? <pre className="rounded-xl bg-slate-950 p-4 text-xs leading-6 text-slate-100">{formatJSON(parseJSON(file.content ?? ""))}</pre> : null}
        {file.preview === "image" ? <img src={file.data_url} alt={file.name} className="max-h-[72vh] max-w-full rounded-lg border object-contain" /> : null}
        {file.preview === "download" ? <div className="text-sm text-muted-foreground">该格式暂不支持在线查看，请下载后打开。</div> : null}
      </div>
    </div>
  );
}

function parseJSON(value: string) {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}
