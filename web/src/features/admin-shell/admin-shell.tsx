"use client";

import { ChevronLeft, ChevronRight, Menu, Moon, RefreshCw, Sun } from "lucide-react";
import Link from "next/link";
import * as React from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Toaster } from "@/components/ui/sonner";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import type { AdminDashboardResponse } from "@/types/api";

import { adminNavItems, type AdminModuleId, systemStatusIcon } from "./navigation";

type Props = {
  activeModule: AdminModuleId;
  dashboard?: AdminDashboardResponse | null;
  dashboardLoading?: boolean;
  dashboardError?: string;
  detailTitle?: string;
  detail?: React.ReactNode;
  onRefreshDashboard: () => void;
  children: React.ReactNode;
};

export function AdminShell({
  activeModule,
  dashboard,
  dashboardLoading,
  dashboardError,
  detailTitle,
  detail,
  onRefreshDashboard,
  children,
}: Props) {
  const [navOpen, setNavOpen] = React.useState(true);
  const [detailOpen, setDetailOpen] = React.useState(true);
  const [darkMode, setDarkMode] = React.useState(false);
  const [knowledgeDirty, setKnowledgeDirty] = React.useState(false);
  const active = adminNavItems.find((item) => item.id === activeModule) ?? adminNavItems[0];
  const StatusIcon = systemStatusIcon;

  React.useEffect(() => {
    const root = document.documentElement;
    const sync = () => setDarkMode(root.classList.contains("dark"));
    sync();
    const syncTimer = window.setTimeout(sync, 0);
    const observer = new MutationObserver(sync);
    observer.observe(root, { attributes: true, attributeFilter: ["class"] });
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onMediaChange = (event: MediaQueryListEvent) => {
      if (localStorage.getItem("wikios.theme")) {
        return;
      }
      root.classList.toggle("dark", event.matches);
      root.style.colorScheme = event.matches ? "dark" : "light";
      setDarkMode(event.matches);
    };
    media.addEventListener("change", onMediaChange);
    return () => {
      window.clearTimeout(syncTimer);
      observer.disconnect();
      media.removeEventListener("change", onMediaChange);
    };
  }, []);

  React.useEffect(() => {
    const handleDirty = (event: Event) => {
      const detail = (event as CustomEvent<{ dirty?: boolean }>).detail;
      setKnowledgeDirty(Boolean(detail?.dirty));
    };
    window.addEventListener("wikios:knowledge-dirty", handleDirty);
    return () => window.removeEventListener("wikios:knowledge-dirty", handleDirty);
  }, []);

  function toggleTheme() {
    const next = !darkMode;
    const root = document.documentElement;
    root.classList.toggle("dark", next);
    root.style.colorScheme = next ? "dark" : "light";
    localStorage.setItem("wikios.theme", next ? "dark" : "light");
    setDarkMode(next);
  }

  function confirmNavigation(event: React.MouseEvent<HTMLAnchorElement>) {
    if (!knowledgeDirty) {
      return;
    }
    if (!window.confirm("知识库文件有未保存内容，确认离开当前页面？")) {
      event.preventDefault();
    }
  }

  return (
    <TooltipProvider>
      <main className="admin-shell-root flex h-screen min-h-screen overflow-hidden bg-background text-foreground">
        <Toaster />
        <aside
          className={cn(
            "hidden border-r border-border bg-white/90 transition-[width] duration-200 dark:bg-card/90 lg:flex lg:flex-col",
            navOpen ? "w-64" : "w-20",
          )}
        >
          <div className={cn("flex h-16 items-center border-b px-4", navOpen ? "justify-start" : "justify-center")}>
            <div className="truncate text-sm font-semibold tracking-normal">WIKIOS</div>
          </div>
          <ScrollArea className="flex-1 px-3 py-3">
            <nav className="space-y-1">
              {adminNavItems.map((item) => {
                const Icon = item.icon;
                const selected = item.id === activeModule;
                const navLink = (
                  <Link
                    key={item.id}
                    href={item.path}
                    aria-current={selected ? "page" : undefined}
                    onClick={confirmNavigation}
	                    className={cn(
	                      "flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm transition",
	                      selected
	                        ? "bg-slate-950 text-white shadow-sm dark:bg-white dark:text-slate-950"
	                        : "text-slate-600 hover:bg-slate-100 hover:text-slate-950 dark:text-muted-foreground dark:hover:bg-secondary dark:hover:text-foreground",
	                      !navOpen && "justify-center px-0",
	                    )}
                  >
                    <Icon className="h-4 w-4 shrink-0" />
                    {navOpen ? <span className="truncate">{item.label}</span> : null}
                  </Link>
                );
                if (navOpen) {
                  return navLink;
                }
                return (
                  <Tooltip key={item.id}>
                    <TooltipTrigger asChild>{navLink}</TooltipTrigger>
                    <TooltipContent>{item.label}</TooltipContent>
                  </Tooltip>
                );
              })}
            </nav>
          </ScrollArea>
          <div className="border-t p-3">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className={cn("w-full", !navOpen && "px-0")}
              onClick={() => setNavOpen((value) => !value)}
            >
              {navOpen ? <ChevronLeft className="mr-2 h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
              {navOpen ? "收起导航" : null}
            </Button>
          </div>
        </aside>

        <section className="flex min-w-0 flex-1 flex-col">
          <header className="flex min-h-16 items-center justify-between gap-3 border-b bg-white/90 px-4 backdrop-blur dark:bg-card/90">
            <div className="flex min-w-0 items-center gap-3">
              <Button className="lg:hidden" variant="outline" size="sm" onClick={() => setNavOpen((value) => !value)}>
                <Menu className="h-4 w-4" />
              </Button>
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <h1 className="truncate text-base font-semibold">{active.label}</h1>
                  <Badge className="rounded-md">
                    {dashboard?.active_model?.display_name ?? "未启用模型"}
                  </Badge>
                </div>
                <p className="truncate text-xs text-muted-foreground">{active.description}</p>
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <div className="hidden items-center gap-2 rounded-lg border bg-white px-3 py-2 text-xs text-muted-foreground dark:bg-background md:flex">
                <StatusIcon className={cn("h-3.5 w-3.5", dashboardError ? "text-red-500" : "text-emerald-600")} />
                <span>{dashboardLoading ? "刷新状态中" : dashboardError ? "状态异常" : "系统在线"}</span>
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={toggleTheme}
                title={darkMode ? "切换到亮色模式" : "切换到暗色模式"}
              >
                {darkMode ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
              </Button>
              <Button variant="outline" size="sm" onClick={onRefreshDashboard} title="刷新系统状态">
                <RefreshCw className={cn("h-4 w-4", dashboardLoading && "animate-spin")} />
              </Button>
            </div>
          </header>

          <div className="border-b bg-white px-3 py-2 dark:bg-card lg:hidden">
            <ScrollArea className="w-full">
              <div className="flex min-w-max gap-2 pb-1">
                {adminNavItems.map((item) => {
                  const Icon = item.icon;
                  const selected = item.id === activeModule;
                  return (
                    <Link
                      key={item.id}
                      href={item.path}
                      aria-current={selected ? "page" : undefined}
                      onClick={confirmNavigation}
	                      className={cn(
	                        "inline-flex h-9 items-center gap-2 rounded-lg border px-3 text-xs font-medium",
	                        selected
	                          ? "border-slate-950 bg-slate-950 text-white dark:border-white dark:bg-white dark:text-slate-950"
	                          : "border-border bg-white text-slate-600 dark:bg-background dark:text-muted-foreground",
	                      )}
                    >
                      <Icon className="h-3.5 w-3.5" />
                      {item.label}
                    </Link>
                  );
                })}
              </div>
            </ScrollArea>
          </div>

          <div
            className={cn(
              "grid min-h-0 flex-1 grid-cols-1",
              detail && detailOpen ? "xl:grid-cols-[minmax(0,1fr)_360px]" : "",
            )}
          >
            <div className="min-h-0 overflow-hidden">{children}</div>
            {detail ? (
	              <aside className={cn("hidden min-h-0 border-l bg-white dark:bg-card xl:flex xl:flex-col", !detailOpen && "xl:hidden")}>
                <div className="flex h-14 items-center justify-between border-b px-4">
                  <div className="truncate text-sm font-semibold">{detailTitle ?? "详情"}</div>
                  <Button variant="ghost" size="sm" onClick={() => setDetailOpen(false)}>
                    <ChevronRight className="h-4 w-4" />
                  </Button>
                </div>
                <ScrollArea className="flex-1 p-4">{detail}</ScrollArea>
              </aside>
            ) : null}
          </div>
        </section>
      </main>
    </TooltipProvider>
  );
}
