"use client";

import { useEffect } from "react";

export default function WikiPage() {
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const path = params.get("path") ?? "";
    const suffix = path ? `&path=${encodeURIComponent(path)}` : "";
    window.location.replace(`/knowledge?view=browse${suffix}`);
  }, []);

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50 px-4 text-sm text-slate-600">
      正在打开知识库...
    </main>
  );
}
