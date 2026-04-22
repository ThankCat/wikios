import type {
  AdminChatRequest,
  AdminResult,
  AdminStreamEvent,
  AppConfig,
  PublicAnswerResponse,
  TaskAccepted,
  TaskRecord,
} from "@/types/api";

async function request<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const response = await fetch(input, init);
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `Request failed: ${response.status}`);
  }
  return (await response.json()) as T;
}

function adminHeaders(token: string) {
  return {
    "Content-Type": "application/json",
    Authorization: `Bearer ${token}`,
  };
}

export const api = {
  getAppConfig() {
    return request<AppConfig>("/app-config.json");
  },
  publicAnswer(payload: Record<string, unknown>) {
    return request<PublicAnswerResponse>("/api/v1/public/answer", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  },
  adminQuery(token: string, payload: Record<string, unknown>) {
    return request<TaskAccepted>("/api/v1/admin/query", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(payload),
    });
  },
  adminIngest(token: string, payload: Record<string, unknown>) {
    return request<TaskAccepted>("/api/v1/admin/ingest", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(payload),
    });
  },
  adminLint(token: string, payload: Record<string, unknown>) {
    return request<TaskAccepted>("/api/v1/admin/lint", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(payload),
    });
  },
  adminReflect(token: string, payload: Record<string, unknown>) {
    return request<TaskAccepted>("/api/v1/admin/reflect", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(payload),
    });
  },
  adminRepairLowRisk(token: string, payload: Record<string, unknown>) {
    return request<TaskAccepted>("/api/v1/admin/repair/apply-low-risk", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(payload),
    });
  },
  adminRepairProposal(token: string, payload: Record<string, unknown>) {
    return request<TaskAccepted>("/api/v1/admin/repair/apply-proposal", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(payload),
    });
  },
  adminSync(token: string, payload: Record<string, unknown>) {
    return request<TaskAccepted>("/api/v1/admin/sync", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(payload),
    });
  },
  task(token: string, taskId: string) {
    return request<TaskRecord>(`/api/v1/admin/tasks/${taskId}`, {
      headers: adminHeaders(token),
    });
  },
  async adminChatStream(
    token: string,
    payload: AdminChatRequest,
    onEvent: (event: AdminStreamEvent) => void,
  ) {
    const response = await fetch("/api/v1/admin/chat/stream", {
      method: "POST",
      headers: adminHeaders(token),
      body: JSON.stringify(payload),
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || `Request failed: ${response.status}`);
    }
    if (!response.body) {
      throw new Error("stream body is unavailable");
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split("\n\n");
      buffer = parts.pop() ?? "";
      for (const part of parts) {
        const event = parseSSEBlock(part);
        if (event) {
          onEvent(event);
        }
      }
    }
    if (buffer.trim()) {
      const event = parseSSEBlock(buffer);
      if (event) {
        onEvent(event);
      }
    }
  },
  async pollTask(token: string, taskId: string, onTick?: (task: TaskRecord) => void) {
    const terminal = new Set(["SUCCESS", "FAILED"]);
    for (;;) {
      const task = await api.task(token, taskId);
      onTick?.(task);
      if (terminal.has(task.status)) {
        return task;
      }
      await new Promise((resolve) => setTimeout(resolve, 1500));
    }
  },
};

export function taskResult(task: TaskRecord) {
  return (task.result ?? {}) as AdminResult;
}

function parseSSEBlock(block: string): AdminStreamEvent | null {
  const lines = block.split("\n");
  let eventType = "message";
  const dataLines: string[] = [];
  for (const line of lines) {
    if (line.startsWith("event:")) {
      eventType = line.slice("event:".length).trim();
      continue;
    }
    if (line.startsWith("data:")) {
      dataLines.push(line.slice("data:".length).trim());
    }
  }
  if (dataLines.length === 0) {
    return null;
  }
  const payload = dataLines.join("\n");
  try {
    return { type: eventType, data: JSON.parse(payload) };
  } catch {
    return { type: eventType, data: payload };
  }
}
