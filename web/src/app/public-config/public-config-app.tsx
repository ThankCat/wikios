"use client";

import { PublicConfigModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function PublicConfigApp() {
  const moduleProps = useAdminModuleContext();

  return <PublicConfigModule {...moduleProps} />;
}
