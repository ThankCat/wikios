import { AdminModulePage } from "@/features/admin-shell/admin-module-page";

import { PromptsApp } from "./prompts-app";

export default function PromptsPage() {
  return (
    <AdminModulePage activeModule="prompts">
      <PromptsApp />
    </AdminModulePage>
  );
}
