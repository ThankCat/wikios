import { AdminModulePage } from "@/features/admin-shell/admin-module-page";

import { KnowledgeApp } from "./knowledge-app";

export default function KnowledgePage() {
  return (
    <AdminModulePage activeModule="knowledge">
      <KnowledgeApp />
    </AdminModulePage>
  );
}
