"use client";

import { DashboardModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function DashboardApp() {
  const moduleProps = useAdminModuleContext();

  return <DashboardModule {...moduleProps} />;
}
