"use client";

import { ReviewModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function ReviewApp() {
  const moduleProps = useAdminModuleContext();

  return <ReviewModule {...moduleProps} />;
}
