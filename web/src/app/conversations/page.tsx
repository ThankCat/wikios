import { AdminModulePage } from "@/features/admin-shell/admin-module-page";

import { ConversationsApp } from "./conversations-app";

export default function ConversationsPage() {
  return (
    <AdminModulePage activeModule="conversations">
      <ConversationsApp />
    </AdminModulePage>
  );
}
