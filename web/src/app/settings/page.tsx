import { AdminModulePage } from "@/features/admin-shell/admin-module-page";

import { SettingsApp } from "./settings-app";

export default function SettingsPage() {
  return (
    <AdminModulePage activeModule="settings">
      <SettingsApp />
    </AdminModulePage>
  );
}
