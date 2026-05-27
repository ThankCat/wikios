import { AdminModulePage } from "@/features/admin-shell/admin-module-page";

import { LogsApp } from "./logs-app";

export default function LogsPage() {
  return (
    <AdminModulePage activeModule="logs">
      <LogsApp />
    </AdminModulePage>
  );
}
