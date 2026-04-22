import type {
  AdminChatRequest,
  AdminChatResponse,
  AdminStreamEvent,
  AdminUser,
  PublicChatHistoryItem,
  PublicAnswerResponse,
  PublicStreamEvent,
  UploadResponse,
} from "@/types/api";

async function request<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const response = await fetch(input, {
    credentials: "include",
    ...init,
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `Request failed: ${response.status}`);
  }
  return (await response.json()) as T;
}

export const api = {
  publicAnswer(question: string, history?: PublicChatHistoryItem[]) {
    return request<PublicAnswerResponse>("/api/v1/public/answer", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, history }),
    });
  },
  async publicAnswerStream(
    question: string,
    history: PublicChatHistoryItem[] | undefined,
    onEvent: (event: PublicStreamEvent) => void,
  ) {
    const response = await fetch("/api/v1/public/answer/stream", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, history }),
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || `Request failed: ${response.status}`);
    }
    if (!response.body) {
      throw new Error("stream body is unavailable");
    }
    await consumeSSE(response, onEvent);
  },
  login(username: string, password: string) {
    return request<{ user: AdminUser }>("/api/v1/admin/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
  },
  logout() {
    return request<{ ok: boolean }>("/api/v1/admin/auth/logout", {
      method: "POST",
    });
  },
  me() {
    return request<{ user: AdminUser }>("/api/v1/admin/auth/me");
  },
  adminChat(payload: AdminChatRequest) {
    return request<AdminChatResponse>("/api/v1/admin/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  },
  async adminChatStream(payload: AdminChatRequest, onEvent: (event: AdminStreamEvent) => void) {
    const response = await fetch("/api/v1/admin/chat/stream", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || `Request failed: ${response.status}`);
    }
    if (!response.body) {
      throw new Error("stream body is unavailable");
    }
    await consumeSSE(response, onEvent);
  },
  upload(file: File) {
    const body = new FormData();
    body.append("file", file);
    return request<UploadResponse>("/api/v1/admin/upload", {
      method: "POST",
      body,
    });
  },
};

async function consumeSSE(
  response: Response,
  onEvent: (event: { type: string; data: unknown }) => void,
) {
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
        if (event.type === "delta" || event.type === "llm_delta") {
          await delay(18);
        }
      }
    }
  }
  if (buffer.trim()) {
    const event = parseSSEBlock(buffer);
    if (event) {
      onEvent(event);
      if (event.type === "delta" || event.type === "llm_delta") {
        await delay(18);
      }
    }
  }
}

function delay(ms: number) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
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
