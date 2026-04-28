export type PublicAnswerResponse = {
  answer: string;
  received_at?: string;
  answered_at?: string;
};

export type PublicChatHistoryItem = {
  id?: string;
  role: "user" | "assistant";
  content: string;
  created_at?: string;
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
  context_usage?: ContextUsage;
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
  started_at?: string;
  ended_at?: string;
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

export type PublicIntentsStatus = {
  path: string;
  loaded_at?: string;
  error?: string;
  warnings?: string[];
  rule_count: number;
};

export type PublicIntentsResponse = {
  source: string;
  status: PublicIntentsStatus;
};

export type LLMBalanceInfo = {
  currency: "CNY" | "USD" | string;
  total_balance: string;
  granted_balance: string;
  topped_up_balance: string;
};

export type LLMBalanceResponse = {
  is_available: boolean;
  balance_infos: LLMBalanceInfo[];
  checked_at: string;
};

export type ReviewItem = {
  id: string;
  path: string;
  question: string;
  original_question?: string;
  draft_answer: string;
  suggested_faq_path: string;
  confidence: number;
  boundary_reason: string;
  matched_pages: string[];
  created_at: string;
  session_id?: string;
  question_message_id?: string;
  answer_message_id?: string;
  question_created_at?: string;
  answer_created_at?: string;
  answer_mode?: string;
  evidence_confidence?: number;
  retrieved_pages?: string[];
  conversation_excerpt?: string[];
};

export type ReviewTarget = {
  path: string;
  title: string;
};

export type ReviewCountResponse = {
  pending_count: number;
};

export type ReviewNextResponse = {
  item?: ReviewItem;
  pending_count: number;
  remaining_count: number;
  target_paths: ReviewTarget[];
};

export type ReviewActionResponse = {
  ok: boolean;
  item: ReviewItem;
  pending_count: number;
};

export type ContextUsage = {
  used_tokens: number;
  remaining_tokens: number;
  max_tokens: number;
  reserve_tokens: number;
  blocked: boolean;
  estimated: boolean;
  counter: string;
  tokenizer?: string;
  error?: string;
};

export type ContextEstimateResponse = {
  mode: string;
  context_usage: ContextUsage;
};

export type WikiTreeItem = {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  modified_at: string;
  preview: "markdown" | "json" | "image" | "download";
};

export type WikiTreeResponse = {
  path: string;
  items: WikiTreeItem[];
};

export type WikiFileResponse = {
  path: string;
  name: string;
  size: number;
  modified_at: string;
  preview: "markdown" | "json" | "image" | "download";
  content?: string;
  data_url?: string;
  mime_type?: string;
  download_url: string;
};

export type SyncStatusFile = {
  path: string;
  old_path?: string;
  status: string;
  index: string;
  worktree: string;
  preview: "markdown" | "json" | "image" | "download";
  default_on: boolean;
  deleted: boolean;
};

export type SyncCommitInfo = {
  hash: string;
  date: string;
  author: string;
  subject: string;
};

export type SyncStatusResponse = {
  branch: string;
  remote: string;
  ahead: number;
  behind: number;
  changed_count: number;
  push_count: number;
  can_push: boolean;
  commits_to_push: SyncCommitInfo[];
  recent_commits: SyncCommitInfo[];
  files: SyncStatusFile[];
};

export type SyncGenerateMessageResponse = {
  message: string;
  rule: string;
  paths: string[];
};

export type SyncCommitResponse = {
  ok: boolean;
  hash: string;
  stdout: string;
  stderr: string;
  exit_code: number;
};

export type SyncPushResponse = {
  ok: boolean;
  remote: string;
  branch: string;
  stdout: string;
  stderr: string;
  exit_code: number;
};
