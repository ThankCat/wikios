"use client";

import { PromptsModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function PromptsApp() {
  const moduleProps = useAdminModuleContext();

  return <PromptsModule {...moduleProps} />;
}
