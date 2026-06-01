import type {
  AdminChatRequest,
  AdminChatResponse,
  AdminDashboardResponse,
  AdminRuntimeSettings,
  AdminRuntimeSettingsResponse,
  AdminStreamEvent,
  ContextEstimateResponse,
  LLMModelResponse,
  LLMModelTestResponse,
  LLMModelsResponse,
  CustomerChatHistoryItem,
  CustomerChatResponse,
  CustomerChatTraceResponse,
  CustomerConversationDeleteResponse,
  CustomerConversationDetailResponse,
  CustomerConversationsResponse,
  CustomerContextEstimateResponse,
  CustomerIntentsResponse,
  ReviewActionResponse,
  ReviewCountResponse,
  ReviewNextResponse,
  CustomerStreamEvent,
  SyncCommitResponse,
  SyncDiagnosticResponse,
  SyncGenerateMessageResponse,
  SyncPushResponse,
  SyncStatusResponse,
  UploadResponse,
  UploadStreamEvent,
  WikiFileResponse,
  WikiReplaceFileResponse,
  WikiSaveFileRequest,
  WikiSaveFileResponse,
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

export type CustomerChatMeta = {
  session_id?: string;
  message_id?: string;
  answer_message_id?: string;
  message_created_at?: string;
  user_id?: string;
  context?: Record<string, unknown>;
};

async function request<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const normalizedInput = normalizeInput(input);
  const response = await fetch(normalizedInput, {
    credentials: "include",
    ...init,
    headers: init?.headers,
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
  customerChat(message: string, history?: CustomerChatHistoryItem[], meta?: CustomerChatMeta, signal?: AbortSignal) {
    return request<CustomerChatResponse>(apiURL("/api/v1/customer/chat"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message, history, ...meta, entrypoint: "external", stream: false }),
      signal,
    });
  },
  estimateCustomerContext(message: string, history?: CustomerChatHistoryItem[], signal?: AbortSignal) {
    return request<CustomerContextEstimateResponse>(apiURL("/api/v1/customer/context/estimate"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message, history, entrypoint: "external" }),
      signal,
    });
  },
  async customerChatAuditStream(
    message: string,
    history: CustomerChatHistoryItem[] | undefined,
    meta: CustomerChatMeta | undefined,
    onEvent: (event: CustomerStreamEvent) => void,
    signal?: AbortSignal,
  ): Promise<string> {
    const response = await fetch(apiURL("/api/v1/customer/chat"), {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message, history, ...meta, entrypoint: "internal", simulation: true, stream: true }),
      signal,
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || `Request failed: ${response.status}`);
    }
    if (!response.body) {
      throw new Error("stream body is unavailable");
    }
    const traceID = response.headers.get("X-Trace-ID") ?? "";
    await consumeSSE(response, onEvent);
    return traceID;
  },
  customerChatTrace(traceID: string, signal?: AbortSignal) {
    return request<CustomerChatTraceResponse>(apiURL(`/api/v1/admin/customer-chat/traces/${encodeURIComponent(traceID)}`), { signal });
  },
  adminDashboard(signal?: AbortSignal) {
    return request<AdminDashboardResponse>(apiURL("/api/v1/admin/dashboard"), { signal });
  },
  getCustomerIntents() {
    return request<CustomerIntentsResponse>(apiURL("/api/v1/admin/customer-intents"));
  },
  updateCustomerIntents(source: string, signal?: AbortSignal) {
    return request<CustomerIntentsResponse>(apiURL("/api/v1/admin/customer-intents"), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source }),
      signal,
    });
  },
  getRuntimeSettings(signal?: AbortSignal) {
    return request<AdminRuntimeSettingsResponse>(apiURL("/api/v1/admin/runtime-settings"), { signal });
  },
  updateRuntimeSettings(settings: AdminRuntimeSettings, signal?: AbortSignal) {
    return request<AdminRuntimeSettingsResponse>(apiURL("/api/v1/admin/runtime-settings"), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
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
    return request<AdminChatResponse>(apiURL("/api/v1/admin/knowledge/assistant/chat"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...payload, stream: false }),
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
  async wikiFile(path: string, signal?: AbortSignal) {
    const response = await request<WikiFileResponse>(apiURL(`/api/v1/admin/wiki/file?path=${encodeURIComponent(path)}`), { signal });
    return normalizeWikiFileResponse(response);
  },
  wikiSaveFile(payload: WikiSaveFileRequest, signal?: AbortSignal) {
    return request<WikiSaveFileResponse>(apiURL("/api/v1/admin/wiki/file"), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal,
    });
  },
  wikiReplaceFile(path: string, file: File, signal?: AbortSignal) {
    const form = new FormData();
    form.set("path", path);
    form.set("file", file);
    return request<WikiReplaceFileResponse>(apiURL("/api/v1/admin/wiki/file/replace"), {
      method: "POST",
      body: form,
      signal,
    });
  },
  wikiDownloadURL(path: string) {
    return apiURL(`/api/v1/admin/wiki/download?path=${encodeURIComponent(path)}`);
  },
  customerConversations(
    params?: { q?: string; page?: number; page_size?: number; from?: string; to?: string; entrypoint?: string; simulation?: boolean | string },
    signal?: AbortSignal,
  ) {
    const search = new URLSearchParams();
    if (params?.q) search.set("q", params.q);
    if (params?.page) search.set("page", String(params.page));
    if (params?.page_size) search.set("page_size", String(params.page_size));
    if (params?.from) search.set("from", params.from);
    if (params?.to) search.set("to", params.to);
    if (params?.entrypoint) search.set("entrypoint", params.entrypoint);
    if (params?.simulation !== undefined && params.simulation !== "") search.set("simulation", String(params.simulation));
    const suffix = search.toString() ? `?${search.toString()}` : "";
    return request<CustomerConversationsResponse>(apiURL(`/api/v1/admin/customer-conversations${suffix}`), { signal });
  },
  customerConversationDetail(id: string, signal?: AbortSignal) {
    return request<CustomerConversationDetailResponse>(apiURL(`/api/v1/admin/customer-conversations/${encodeURIComponent(id)}`), { signal });
  },
  deleteCustomerConversation(id: string, signal?: AbortSignal) {
    return request<CustomerConversationDeleteResponse>(apiURL(`/api/v1/admin/customer-conversations/${encodeURIComponent(id)}`), {
      method: "DELETE",
      signal,
    });
  },
  syncStatus(signal?: AbortSignal) {
    return request<SyncStatusResponse>(apiURL("/api/v1/admin/sync/status"), { signal });
  },
  syncTest(signal?: AbortSignal) {
    return request<SyncDiagnosticResponse>(apiURL("/api/v1/admin/sync/test"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({}),
      signal,
    });
  },
  syncSetup(signal?: AbortSignal) {
    return request<SyncDiagnosticResponse>(apiURL("/api/v1/admin/sync/setup"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({}),
      signal,
    });
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
    const url = apiURL("/api/v1/admin/knowledge/assistant/chat");
    const response = await fetch(url, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...payload, stream: true }),
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
  return "";
}

function normalizeWikiFileResponse(response: WikiFileResponse): WikiFileResponse {
  const textKind = response.text_kind || inferWikiTextKind(response.path);
  const editable = typeof response.editable === "boolean" ? response.editable : Boolean(textKind && typeof response.content === "string");
  return {
    ...response,
    editable,
    text_kind: textKind,
    sha256: response.sha256 ?? "",
    encoding: response.encoding ?? (editable ? "utf-8" : ""),
  };
}

function inferWikiTextKind(path: string) {
  const ext = path.toLowerCase().slice(path.lastIndexOf("."));
  switch (ext) {
    case ".md":
    case ".markdown":
    case ".qmd":
      return "markdown";
    case ".yaml":
    case ".yml":
      return "yaml";
    case ".json":
      return "json";
    case ".txt":
    case ".log":
      return "text";
    case ".csv":
      return "csv";
    case ".tsv":
      return "tsv";
    case ".toml":
      return "toml";
    case ".ini":
      return "ini";
    case ".html":
      return "html";
    case ".css":
      return "css";
    case ".js":
      return "javascript";
    case ".ts":
      return "typescript";
    default:
      return "";
  }
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
