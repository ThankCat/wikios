"use client";

import { useEffect, useState } from "react";

import { AdminChat } from "@/features/admin/admin-chat";
import { api } from "@/lib/api";
import type { AdminUser } from "@/types/api";

export default function AdminPage() {
  const [user, setUser] = useState<AdminUser | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    void api
      .me()
      .then((response) => {
        setUser(response.user);
      })
      .catch(() => {
        window.location.href = "/admin/login";
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  if (loading) {
    return <main className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">加载中…</main>;
  }
  if (!user) {
    return null;
  }
  return <AdminChat username={user.username} />;
}
