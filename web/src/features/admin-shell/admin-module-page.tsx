"use client";

import { useRouter } from "next/navigation";
import * as React from "react";

import type { BaseModuleProps } from "@/features/admin/modules/admin-modules";
import { api } from "@/lib/api";
import type { AdminDashboardResponse, AdminUser } from "@/types/api";

import { AdminShell } from "./admin-shell";
import { adminModulePath, type AdminModuleId } from "./navigation";

const localAdminUser: AdminUser = {
  id: "local_admin",
  username: "admin",
};

const AdminModuleContext = React.createContext<BaseModuleProps | null>(null);

export function useAdminModuleContext() {
  const context = React.useContext(AdminModuleContext);
  if (!context) {
    throw new Error("useAdminModuleContext must be used inside AdminModulePage");
  }
  return context;
}

export function AdminModulePage({
  activeModule,
  children,
}: {
  activeModule: AdminModuleId;
  children: React.ReactNode;
}) {
  const router = useRouter();
  const [dashboard, setDashboard] = React.useState<AdminDashboardResponse | null>(null);
  const [dashboardLoading, setDashboardLoading] = React.useState(true);
  const [dashboardError, setDashboardError] = React.useState("");
  const [detailTitle, setDetailTitle] = React.useState("详情");
  const [detail, setDetailNode] = React.useState<React.ReactNode>(null);

  const loadDashboard = React.useCallback(async () => {
    setDashboardLoading(true);
    setDashboardError("");
    try {
      const response = await api.adminDashboard();
      setDashboard(response);
    } catch (error) {
      setDashboardError(error instanceof Error ? error.message : "状态刷新失败");
    } finally {
      setDashboardLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void loadDashboard();
  }, [loadDashboard]);

  const setDetail = React.useCallback((title: string, node: React.ReactNode) => {
    setDetailTitle(title);
    setDetailNode(node);
  }, []);

  const openModule = React.useCallback(
    (module: AdminModuleId) => {
      setDetailNode(null);
      if (module === "models") {
        router.push("/settings?tab=models");
        return;
      }
      router.push(adminModulePath(module));
    },
    [router],
  );

  const context = React.useMemo<BaseModuleProps>(
    () => ({
      user: localAdminUser,
      dashboard,
      onDashboardRefresh: loadDashboard,
      setDetail,
      openModule,
    }),
    [dashboard, loadDashboard, openModule, setDetail],
  );

  return (
    <AdminModuleContext.Provider value={context}>
      <AdminShell
        activeModule={activeModule}
        dashboard={dashboard}
        dashboardLoading={dashboardLoading}
        dashboardError={dashboardError}
        detailTitle={detailTitle}
        detail={detail}
        onRefreshDashboard={loadDashboard}
      >
        {children}
      </AdminShell>
    </AdminModuleContext.Provider>
  );
}
