"use client";

import { useSearchParams } from "next/navigation";
import { Suspense } from "react";

import { KnowledgeModule } from "@/features/admin/modules/admin-modules";
import { useAdminModuleContext } from "@/features/admin-shell/admin-module-page";

export function KnowledgeApp() {
  return (
    <Suspense fallback={null}>
      <KnowledgeAppContent />
    </Suspense>
  );
}

function KnowledgeAppContent() {
  const moduleProps = useAdminModuleContext();
  const searchParams = useSearchParams();
  const initialPath = searchParams.get("path");
  const initialView = searchParams.get("view");

  return <KnowledgeModule {...moduleProps} initialPath={initialPath} initialView={initialView} />;
}
