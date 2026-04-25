import type {
  AdminChatRequest,
  AdminChatResponse,
  AdminStreamEvent,
  AdminUser,
  ContextEstimateResponse,
  PublicChatHistoryItem,
  PublicAnswerResponse,
  PublicIntentsResponse,
  PublicStreamEvent,
  SyncCommitResponse,
  SyncGenerateMessageResponse,
  SyncPushResponse,
  SyncStatusResponse,
  UploadResponse,
  UploadStreamEvent,
  WikiFileResponse,
  WikiTreeResponse,
} from "@/types/api";

export class APIError extends Error {
  status: number;
  payload: unknown;

  constructor(message: string, status: number, payload: unknown) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.payload = payload;
  }
}

async function request<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const response = await fetch(normalizeInput(input), {
    credentials: "include",
    ...init,
  });
  if (!response.ok) {
    const text = await response.text();
    let payload: unknown = text;
    let message = text || `Request failed: ${response.status}`;
    try {
      payload = JSON.parse(text);
      const object = payload && typeof payload === "object" ? (payload as Record<string, unknown>) : null;
      const errorObject =
        object && object.error && typeof object.error === "object" ? (object.error as Record<string, unknown>) : null;
      const payloadMessage = typeof errorObject?.message === "string" ? errorObject.message : "";
      if (payloadMessage.trim() !== "") {
        message = payloadMessage;
      }
    } catch {
      // Keep plain text fallback.
    }
    throw new APIError(message, response.status, payload);
  }
  return (await response.json()) as T;
}

export const api = {
  publicAnswer(question: string, history?: PublicChatHistoryItem[], signal?: AbortSignal) {
    return request<PublicAnswerResponse>(apiURL("/api/v1/public/answer"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, history }),
      signal,
    });
  },
  async publicAnswerStream(
    question: string,
    history: PublicChatHistoryItem[] | undefined,
    onEvent: (event: PublicStreamEvent) => void,
    signal?: AbortSignal,
  ) {
    const response = await fetch(apiURL("/api/v1/public/answer/stream"), {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, history }),
      signal,
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
    return request<{ user: AdminUser }>(apiURL("/api/v1/admin/auth/login"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
  },
  logout() {
    return request<{ ok: boolean }>(apiURL("/api/v1/admin/auth/logout"), {
      method: "POST",
    });
  },
  me() {
    return request<{ user: AdminUser }>(apiURL("/api/v1/admin/auth/me"));
  },
  getPublicIntents() {
    return request<PublicIntentsResponse>(apiURL("/api/v1/admin/public-intents"));
  },
  updatePublicIntents(source: string, signal?: AbortSignal) {
    return request<PublicIntentsResponse>(apiURL("/api/v1/admin/public-intents"), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source }),
      signal,
    });
  },
  adminChat(payload: AdminChatRequest, signal?: AbortSignal) {
    return request<AdminChatResponse>(apiURL("/api/v1/admin/chat"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal,
    });
  },
  estimateAdminContext(payload: AdminChatRequest, signal?: AbortSignal) {
    return request<ContextEstimateResponse>(apiURL("/api/v1/admin/context/estimate"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal,
    });
  },
  wikiTree(path = "", signal?: AbortSignal) {
    return request<WikiTreeResponse>(apiURL(`/api/v1/admin/wiki/tree?path=${encodeURIComponent(path)}`), { signal });
  },
  wikiFile(path: string, signal?: AbortSignal) {
    return request<WikiFileResponse>(apiURL(`/api/v1/admin/wiki/file?path=${encodeURIComponent(path)}`), { signal });
  },
  wikiDownloadURL(path: string) {
    return apiURL(`/api/v1/admin/wiki/download?path=${encodeURIComponent(path)}`);
  },
  syncStatus(signal?: AbortSignal) {
    return request<SyncStatusResponse>(apiURL("/api/v1/admin/sync/status"), { signal });
  },
  syncCommit(paths: string[], message: string, signal?: AbortSignal) {
    return request<SyncCommitResponse>(apiURL("/api/v1/admin/sync/commit"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ paths, message }),
      signal,
    });
  },
  syncGenerateMessage(paths: string[], signal?: AbortSignal) {
    return request<SyncGenerateMessageResponse>(apiURL("/api/v1/admin/sync/generate-message"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ paths }),
      signal,
    });
  },
  syncPush(remote: string, branch: string, signal?: AbortSignal) {
    return request<SyncPushResponse>(apiURL("/api/v1/admin/sync/push"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ remote, branch }),
      signal,
    });
  },
  async adminChatStream(payload: AdminChatRequest, onEvent: (event: AdminStreamEvent) => void, signal?: AbortSignal) {
    const response = await fetch(apiURL("/api/v1/admin/chat/stream"), {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal,
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
  upload(file: File, signal?: AbortSignal) {
    const body = new FormData();
    body.append("file", file);
    return request<UploadResponse>(apiURL("/api/v1/admin/upload"), {
      method: "POST",
      body,
      signal,
    });
  },
  async uploadStream(file: File, onEvent: (event: UploadStreamEvent) => void, signal?: AbortSignal) {
    const body = new FormData();
    body.append("file", file);
    const response = await fetch(apiURL("/api/v1/admin/upload/stream"), {
      method: "POST",
      credentials: "include",
      body,
      signal,
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
};

export function isAbortError(error: unknown) {
  return error instanceof DOMException ? error.name === "AbortError" : error instanceof Error && error.name === "AbortError";
}

function apiURL(path: string) {
  const base = resolveAPIBaseURL();
  if (!base) {
    return path;
  }
  return `${base}${path}`;
}

function normalizeInput(input: RequestInfo) {
  if (typeof input === "string") {
    return input;
  }
  return input;
}

function resolveAPIBaseURL() {
  const envBase = process.env.NEXT_PUBLIC_API_BASE_URL?.trim();
  if (envBase) {
    return envBase.replace(/\/$/, "");
  }
  if (typeof window === "undefined") {
    return "";
  }
  const { protocol, hostname } = window.location;
  return `${protocol}//${hostname}:8080`;
}

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
