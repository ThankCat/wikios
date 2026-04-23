export type PublicSource = {
  path: string;
  title: string;
  confidence: string;
};

export type PublicAnswerDetails = {
  answer_type: string;
  answer_markdown: string;
  sources: PublicSource[];
  confidence: number;
  notes?: string;
  trace_id: string;
};

export type PublicAnswerResponse = {
  answer: string;
  details?: PublicAnswerDetails;
};

export type PublicChatHistoryItem = {
  role: "user" | "assistant";
  content: string;
};

export type PublicStreamEvent = {
  type: string;
  data: unknown;
};

export type AdminAttachment = {
  path: string;
  kind: string;
  name?: string;
};

export type AdminChatRequest = {
  message: string;
  stream: boolean;
  mode_hint?: string;
  context?: Record<string, unknown>;
  attachments?: AdminAttachment[];
  history?: PublicChatHistoryItem[];
};

export type AdminChatResponse = {
  mode: string;
  reply: string;
  details: Record<string, unknown>;
  execution: {
    id: string;
    kind: string;
    status: string;
    steps: AdminExecutionStep[];
    error?: string;
    started_at: string;
    ended_at?: string;
  };
};

export type AdminExecutionStep = {
  name: string;
  tool?: string;
  status: string;
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  duration_ms?: number;
};

export type AdminStreamEvent = {
  type: string;
  data: unknown;
};

export type UploadExecution = {
  id: string;
  kind: string;
  status: string;
  steps: AdminExecutionStep[];
  error?: string;
  started_at: string;
  ended_at?: string;
};

export type AdminUser = {
  id: string;
  username: string;
};

export type UploadResponse = {
  reply: string;
  details: Record<string, unknown>;
  execution?: UploadExecution;
};

export type UploadStreamEvent = {
  type: string;
  data: unknown;
};
