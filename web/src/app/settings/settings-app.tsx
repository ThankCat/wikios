"use client";

import { Suspense } from "react";

import { SettingsModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function SettingsApp() {
  return (
    <Suspense fallback={null}>
      <SettingsAppContent />
    </Suspense>
  );
}

function SettingsAppContent() {
  const moduleProps = useAdminModuleContext();

  return <SettingsModule {...moduleProps} />;
}
