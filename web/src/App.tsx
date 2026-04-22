import { useEffect, useState } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { LoginPage } from "@/routes/LoginPage";
import { WorkspacePage } from "@/routes/WorkspacePage";
import { api } from "@/lib/api";
import type { AppConfig } from "@/types/api";

export function App() {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    void api
      .getAppConfig()
      .then(setConfig)
      .catch((reason) => {
        setError(reason instanceof Error ? reason.message : "failed to load app config");
      });
  }, []);

  if (error) {
    return (
      <div className="flex min-h-screen items-center justify-center p-6">
        <div className="panel-glass max-w-xl rounded-3xl p-6">
          <h1 className="text-xl font-semibold">Failed to boot workbench</h1>
          <p className="mt-3 text-sm text-muted-foreground">{error}</p>
        </div>
      </div>
    );
  }

  if (!config) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="rounded-3xl border border-border bg-white/70 px-6 py-4 text-sm text-muted-foreground">
          正在初始化 WikiOS Workbench...
        </div>
      </div>
    );
  }

  return (
    <Routes>
      <Route path="/" element={<WorkspacePage mountedWikiName={config.mountedWikiName} />} />
      <Route path="/login" element={<LoginPage mountedWikiName={config.mountedWikiName} />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
