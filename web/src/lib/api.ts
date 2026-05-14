import type {
  AdminChatRequest,
  AdminChatResponse,
  AdminLoginResponse,
  AdminStreamEvent,
  AdminUser,
  ContextEstimateResponse,
  LLMModelResponse,
  LLMModelTestResponse,
  LLMModelsResponse,
  PublicChatHistoryItem,
  PublicAnswerResponse,
  PublicContextEstimateResponse,
  PublicIntentsResponse,
  ReviewActionResponse,
  ReviewCountResponse,
  ReviewNextResponse,
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

export type PublicAnswerMeta = {
  session_id?: string;
  question_message_id?: string;
  answer_message_id?: string;
  question_created_at?: string;
  stream?: boolean;
};

let memoryAdminSessionToken = "";
let memoryAdminSessionExpiresAt = "";

async function request<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const normalizedInput = normalizeInput(input);
  const response = await fetch(normalizedInput, {
    credentials: "include",
    ...init,
    headers: withAdminAuthHeaders(normalizedInput, init?.headers),
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
  publicAnswer(question: string, history?: PublicChatHistoryItem[], meta?: PublicAnswerMeta, signal?: AbortSignal) {
    return request<PublicAnswerResponse>(apiURL("/api/v1/public/answer"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, history, ...meta, stream: false }),
      signal,
    });
  },
  estimatePublicContext(question: string, history?: PublicChatHistoryItem[], signal?: AbortSignal) {
    return request<PublicContextEstimateResponse>(apiURL("/api/v1/public/context/estimate"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, history }),
      signal,
    });
  },
  async publicAnswerStream(
    question: string,
    history: PublicChatHistoryItem[] | undefined,
    meta: PublicAnswerMeta | undefined,
    onEvent: (event: PublicStreamEvent) => void,
    signal?: AbortSignal,
  ) {
    const response = await fetch(apiURL("/api/v1/public/answer"), {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, history, ...meta, stream: true }),
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
  async login(username: string, password: string) {
    const response = await request<AdminLoginResponse>(apiURL("/api/v1/admin/auth/login"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    storeAdminSessionToken(response.token, response.expires_at);
    return response;
  },
  async logout() {
    try {
      return await request<{ ok: boolean }>(apiURL("/api/v1/admin/auth/logout"), {
        method: "POST",
      });
    } finally {
      clearAdminSessionToken();
    }
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
  listLLMModels(signal?: AbortSignal) {
    return request<LLMModelsResponse>(apiURL("/api/v1/admin/models"), { signal });
  },
  getLLMModel(id: string, signal?: AbortSignal) {
    return request<LLMModelResponse>(apiURL(`/api/v1/admin/models/${encodeURIComponent(id)}`), { signal });
  },
  createLLMModel(
    payload: {
      display_name: string;
      provider: string;
      base_url: string;
      model_name: string;
      api_key: string;
      timeout_sec: number;
      admin_timeout_sec: number;
    },
    signal?: AbortSignal,
  ) {
    return request<LLMModelResponse>(apiURL("/api/v1/admin/models"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal,
    });
  },
  updateLLMModel(
    id: string,
    payload: {
      display_name: string;
      provider: string;
      base_url: string;
      model_name: string;
      api_key: string;
      timeout_sec: number;
      admin_timeout_sec: number;
    },
    signal?: AbortSignal,
  ) {
    return request<LLMModelResponse>(apiURL(`/api/v1/admin/models/${encodeURIComponent(id)}`), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal,
    });
  },
  deleteLLMModel(id: string, signal?: AbortSignal) {
    return request<{ ok: boolean }>(apiURL(`/api/v1/admin/models/${encodeURIComponent(id)}`), {
      method: "DELETE",
      signal,
    });
  },
  activateLLMModel(id: string, signal?: AbortSignal) {
    return request<LLMModelResponse>(apiURL(`/api/v1/admin/models/${encodeURIComponent(id)}/activate`), {
      method: "POST",
      signal,
    });
  },
  testLLMModel(id: string, signal?: AbortSignal) {
    return request<LLMModelTestResponse>(apiURL(`/api/v1/admin/models/${encodeURIComponent(id)}/test`), {
      method: "POST",
      signal,
    });
  },
  reviewCount(signal?: AbortSignal) {
    return request<ReviewCountResponse>(apiURL("/api/v1/admin/reviews/count"), { signal });
  },
  reviewNext(cursor?: string, signal?: AbortSignal) {
    const suffix = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
    return request<ReviewNextResponse>(apiURL(`/api/v1/admin/reviews/next${suffix}`), { signal });
  },
  reviewApprove(id: string, payload: { question?: string; answer: string; target_path: string }, signal?: AbortSignal) {
    return request<ReviewActionResponse>(apiURL(`/api/v1/admin/reviews/${encodeURIComponent(id)}/approve`), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal,
    });
  },
  reviewReject(id: string, payload: { reason: string }, signal?: AbortSignal) {
    return request<ReviewActionResponse>(apiURL(`/api/v1/admin/reviews/${encodeURIComponent(id)}/reject`), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal,
    });
  },
  reviewDelete(id: string, signal?: AbortSignal) {
    return request<ReviewActionResponse>(apiURL(`/api/v1/admin/reviews/${encodeURIComponent(id)}/delete`), {
      method: "POST",
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
    const url = apiURL("/api/v1/admin/chat/stream");
    const response = await fetch(url, {
      method: "POST",
      credentials: "include",
      headers: withAdminAuthHeaders(url, { "Content-Type": "application/json" }),
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
    const url = apiURL("/api/v1/admin/upload/stream");
    const response = await fetch(url, {
      method: "POST",
      credentials: "include",
      headers: withAdminAuthHeaders(url),
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

function withAdminAuthHeaders(input: RequestInfo, initHeaders?: HeadersInit) {
  const headers = new Headers(initHeaders);
  if (!isAdminAPIRequest(input) || headers.has("Authorization")) {
    return headers;
  }
  const token = currentAdminSessionToken();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  return headers;
}

function isAdminAPIRequest(input: RequestInfo) {
  const url = typeof input === "string" ? input : input.url;
  if (url.startsWith("/api/v1/admin/")) {
    return true;
  }
  try {
    const base = typeof window === "undefined" ? "http://localhost" : window.location.origin;
    return new URL(url, base).pathname.startsWith("/api/v1/admin/");
  } catch {
    return false;
  }
}

function storeAdminSessionToken(token?: string, expiresAt?: string) {
	const cleanToken = token?.trim();
	if (!cleanToken) {
		clearAdminSessionToken();
		return;
	}
	memoryAdminSessionToken = cleanToken;
	memoryAdminSessionExpiresAt = expiresAt ?? "";
}

function clearAdminSessionToken() {
	memoryAdminSessionToken = "";
	memoryAdminSessionExpiresAt = "";
}

function currentAdminSessionToken() {
	return currentMemoryAdminSessionToken();
}

function currentMemoryAdminSessionToken() {
  if (!memoryAdminSessionToken) {
    return "";
  }
  const expiresAt = memoryAdminSessionExpiresAt ? Date.parse(memoryAdminSessionExpiresAt) : 0;
  if (expiresAt > 0 && expiresAt <= Date.now() + 5_000) {
    clearAdminSessionToken();
    return "";
  }
  return memoryAdminSessionToken;
}

function resolveAPIBaseURL() {
  const envBase = process.env.NEXT_PUBLIC_API_BASE_URL?.trim();
  if (envBase) {
    return envBase.replace(/\/$/, "");
  }
  if (typeof window === "undefined") {
    return "";
  }
  return window.location.origin;
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
