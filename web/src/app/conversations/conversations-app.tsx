"use client";

import { ConversationsModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function ConversationsApp() {
  const moduleProps = useAdminModuleContext();

  return <ConversationsModule {...moduleProps} />;
}
