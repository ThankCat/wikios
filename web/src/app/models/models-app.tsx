"use client";

import { ModelsModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function ModelsApp() {
  const moduleProps = useAdminModuleContext();

  return <ModelsModule {...moduleProps} />;
}
