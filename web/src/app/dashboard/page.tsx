import { AdminModulePage } from "@/features/admin-shell/admin-module-page";

import { DashboardApp } from "./dashboard-app";

export default function DashboardPage() {
  return (
    <AdminModulePage activeModule="dashboard">
      <DashboardApp />
    </AdminModulePage>
  );
}
