"use client";

import { ChevronRight, Menu, Moon, RefreshCw, Sun } from "lucide-react";
import Link from "next/link";
import * as React from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { Toaster } from "@/components/ui/sonner";
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

export function AdminShell(props: Props) {
  return (
    <SidebarProvider defaultOpen>
      <AdminShellContent {...props} />
    </SidebarProvider>
  );
}

function AdminShellContent({
  activeModule,
  dashboard,
  dashboardLoading,
  dashboardError,
  detailTitle,
  detail,
  onRefreshDashboard,
  children,
}: Props) {
  const [detailOpen, setDetailOpen] = React.useState(true);
  const [darkMode, setDarkMode] = React.useState(false);
  const [knowledgeDirty, setKnowledgeDirty] = React.useState(false);
  const active = adminNavItems.find((item) => item.id === activeModule) ?? adminNavItems[0];
  const StatusIcon = systemStatusIcon;
  const { open: sidebarOpen } = useSidebar();

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
    <>
      <Toaster />
      <Sidebar className="admin-shell-sidebar">
        <SidebarHeader className={cn(sidebarOpen ? "justify-start" : "justify-center")}>
          <Link href="/dashboard" className="flex min-w-0 items-center gap-2 font-semibold" onClick={confirmNavigation}>
            <div className="flex size-7 items-center justify-center rounded-md bg-sidebar-primary text-xs text-sidebar-primary-foreground">W</div>
            {sidebarOpen ? <span className="truncate text-sm">WIKIOS</span> : null}
          </Link>
        </SidebarHeader>
        <SidebarContent>
          <SidebarGroup>
            <SidebarGroupLabel>管理后台</SidebarGroupLabel>
            <SidebarMenu>
              {adminNavItems.map((item) => {
                const Icon = item.icon;
                const selected = item.id === activeModule;
                return (
                  <SidebarMenuItem key={item.id}>
                    <SidebarMenuButton asChild isActive={selected} tooltip={item.label}>
                      <Link href={item.path} aria-current={selected ? "page" : undefined} onClick={confirmNavigation}>
                        <Icon />
                        {sidebarOpen ? <span className="truncate">{item.label}</span> : null}
                      </Link>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                );
              })}
            </SidebarMenu>
          </SidebarGroup>
        </SidebarContent>
        <SidebarFooter>
          <SidebarTrigger className={cn("w-full", !sidebarOpen && "px-0")} title={sidebarOpen ? "收起导航" : "展开导航"}>
            <Menu />
            {sidebarOpen ? <span>收起导航</span> : null}
          </SidebarTrigger>
        </SidebarFooter>
      </Sidebar>

      <SidebarInset className="h-screen overflow-hidden">
        <header className="flex min-h-14 items-center justify-between gap-3 border-b bg-background px-4">
          <div className="flex min-w-0 items-center gap-3">
            <SidebarTrigger className="md:hidden" title="打开导航">
              <Menu />
            </SidebarTrigger>
            <div className="min-w-0">
              <h1 className="truncate text-sm font-semibold">{active.label}</h1>
              <p className="truncate text-xs text-muted-foreground">{active.description}</p>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Badge variant="outline">{dashboard?.active_model?.display_name ?? "未启用模型"}</Badge>
            <Badge variant="outline" className="gap-1.5 font-normal text-muted-foreground">
              <StatusIcon className={cn("size-3.5", dashboardError ? "text-destructive" : "text-muted-foreground", dashboardLoading && "animate-pulse")} />
              {dashboardLoading ? "刷新状态中" : dashboardError ? "状态异常" : "系统在线"}
            </Badge>
            <Button variant="outline" size="sm" onClick={toggleTheme} title={darkMode ? "切换到亮色模式" : "切换到暗色模式"}>
              {darkMode ? <Sun /> : <Moon />}
            </Button>
            <Button variant="outline" size="sm" onClick={onRefreshDashboard} title="刷新系统状态">
              <RefreshCw className={cn(dashboardLoading && "animate-spin")} />
            </Button>
          </div>
        </header>

        <div className="border-b bg-background px-3 py-2 md:hidden">
          <ScrollArea className="w-full">
            <div className="flex min-w-max gap-2 pb-1">
              {adminNavItems.map((item) => {
                const Icon = item.icon;
                const selected = item.id === activeModule;
                return (
                  <Button key={item.id} asChild variant={selected ? "default" : "outline"} size="sm">
                    <Link href={item.path} aria-current={selected ? "page" : undefined} onClick={confirmNavigation}>
                      <Icon />
                      {item.label}
                    </Link>
                  </Button>
                );
              })}
            </div>
          </ScrollArea>
        </div>

        <div className={cn("grid min-h-0 flex-1 grid-cols-1", detail && detailOpen ? "xl:grid-cols-[minmax(0,1fr)_360px]" : "")}>
          <div className="min-h-0 overflow-hidden">{children}</div>
          {detail ? (
            <aside className={cn("hidden min-h-0 border-l bg-background xl:flex xl:flex-col", !detailOpen && "xl:hidden")}>
              <div className="flex h-14 items-center justify-between border-b px-4">
                <div className="truncate text-sm font-semibold">{detailTitle ?? "详情"}</div>
                <Button variant="ghost" size="sm" onClick={() => setDetailOpen(false)}>
                  <ChevronRight />
                </Button>
              </div>
              <ScrollArea className="flex-1 p-4">{detail}</ScrollArea>
            </aside>
          ) : null}
        </div>
      </SidebarInset>
    </>
  );
}
