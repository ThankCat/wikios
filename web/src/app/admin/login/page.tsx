"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";

export default function AdminLoginPage() {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("admin123");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function submit() {
    setError("");
    setLoading(true);
    try {
      await api.login(username, password);
      window.location.href = "/admin";
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "登录失败");
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>管理员登录</CardTitle>
          <CardDescription>默认账号：admin，默认密码：admin123</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="space-y-4">
            <Input value={username} onChange={(event) => setUsername(event.target.value)} placeholder="账号名" />
            <Input
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              placeholder="密码"
            />
            {error ? <p className="text-sm text-destructive">{error}</p> : null}
            <Button className="w-full" onClick={() => void submit()} disabled={loading}>
              {loading ? "登录中" : "进入后台"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </main>
  );
}
