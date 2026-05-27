import { ChevronDown, ChevronRight, File, Folder, FolderOpen, HardDrive } from "lucide-react";
import * as React from "react";

import { cn } from "@/lib/utils";

export type FileTreeNode = {
  id: string;
  name: string;
  path: string;
  isDirectory: boolean;
  preview?: string;
  expanded?: boolean;
  loading?: boolean;
  children?: FileTreeNode[];
};

type FileTreeProps = {
  nodes: FileTreeNode[];
  rootLabel?: string;
  rootPath?: string;
  selectedPath?: string;
  activePath?: string;
  loadingRoot?: boolean;
  emptyText?: string;
  onSelectFile: (node: FileTreeNode) => void;
  onToggleDirectory: (node: FileTreeNode) => void;
  onSelectRoot?: () => void;
  className?: string;
};

export function FileTree({
  nodes,
  rootLabel = "Files",
  rootPath = "",
  selectedPath,
  activePath,
  loadingRoot,
  emptyText = "暂无文件",
  onSelectFile,
  onToggleDirectory,
  onSelectRoot,
  className,
}: FileTreeProps) {
  return (
    <div className={cn("rounded-lg border bg-background text-sm", className)}>
      <button
        type="button"
        className={cn(
          "flex h-9 w-full items-center gap-2 border-b px-3 text-left transition hover:bg-secondary/70",
          activePath === rootPath && !selectedPath && "bg-secondary text-foreground",
        )}
        onClick={onSelectRoot}
      >
        <HardDrive className="h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-medium">{rootLabel}</span>
        {loadingRoot ? <span className="text-[11px] text-muted-foreground">加载中</span> : null}
      </button>
      <div className="py-1">
        {nodes.length ? (
          nodes.map((node) => (
            <FileTreeRow
              key={node.id}
              node={node}
              depth={0}
              selectedPath={selectedPath}
              activePath={activePath}
              onSelectFile={onSelectFile}
              onToggleDirectory={onToggleDirectory}
            />
          ))
        ) : (
          <div className="px-3 py-8 text-center text-xs text-muted-foreground">{loadingRoot ? "正在读取..." : emptyText}</div>
        )}
      </div>
    </div>
  );
}

function FileTreeRow({
  node,
  depth,
  selectedPath,
  activePath,
  onSelectFile,
  onToggleDirectory,
}: {
  node: FileTreeNode;
  depth: number;
  selectedPath?: string;
  activePath?: string;
  onSelectFile: (node: FileTreeNode) => void;
  onToggleDirectory: (node: FileTreeNode) => void;
}) {
  const selected = selectedPath === node.path;
  const activeDirectory = node.isDirectory && activePath === node.path && !selectedPath;
  const Icon = node.isDirectory ? (node.expanded ? FolderOpen : Folder) : File;
  return (
    <div>
      <button
        type="button"
        className={cn(
          "group flex h-8 w-full items-center gap-1.5 pr-2 text-left transition hover:bg-secondary/70",
          selected && "bg-primary text-primary-foreground hover:bg-primary",
          activeDirectory && "bg-secondary text-foreground",
        )}
        style={{ paddingLeft: `${depth * 14 + 8}px` }}
        aria-expanded={node.isDirectory ? Boolean(node.expanded) : undefined}
        onClick={() => {
          if (node.isDirectory) {
            onToggleDirectory(node);
            return;
          }
          onSelectFile(node);
        }}
        title={node.path || node.name}
      >
        <span className="flex h-5 w-5 shrink-0 items-center justify-center">
          {node.isDirectory ? (
            node.expanded ? (
              <ChevronDown className="h-3.5 w-3.5 text-muted-foreground group-data-[selected=true]:text-primary-foreground" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 text-muted-foreground group-data-[selected=true]:text-primary-foreground" />
            )
          ) : null}
        </span>
        <Icon className={cn("h-4 w-4 shrink-0", selected ? "text-primary-foreground" : "text-muted-foreground")} />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{node.name}</span>
        {node.loading ? <span className="shrink-0 text-[11px] text-muted-foreground">...</span> : null}
        {!node.isDirectory && node.preview ? (
          <span className={cn("shrink-0 text-[10px]", selected ? "text-primary-foreground/80" : "text-muted-foreground")}>
            {node.preview}
          </span>
        ) : null}
      </button>
      {node.isDirectory && node.expanded && node.children?.length ? (
        <div>
          {node.children.map((child) => (
            <FileTreeRow
              key={child.id}
              node={child}
              depth={depth + 1}
              selectedPath={selectedPath}
              activePath={activePath}
              onSelectFile={onSelectFile}
              onToggleDirectory={onToggleDirectory}
            />
          ))}
        </div>
      ) : null}
      {node.isDirectory && node.expanded && node.loading ? (
        <div className="space-y-1 py-1" style={{ paddingLeft: `${(depth + 1) * 14 + 34}px` }}>
          <div className="h-3 w-32 animate-pulse rounded bg-muted" />
          <div className="h-3 w-24 animate-pulse rounded bg-muted" />
        </div>
      ) : null}
    </div>
  );
}
