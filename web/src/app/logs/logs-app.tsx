"use client";

import { LogsModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function LogsApp() {
  const moduleProps = useAdminModuleContext();

  return <LogsModule {...moduleProps} />;
}
