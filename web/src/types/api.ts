export type PublicPriceInfo = {
  expected_price: string;
  product_type: "static" | "dynamic" | "box";
  product_bandwidth: number;
  intended_purchase_quantity: number;
  box_usage_time: number;
  box_usage_quantity_min: number;
  box_usage_quantity_max: number;
};

export type PublicUserIntent =
  | {
      type: "price_adjustment";
      price_info: PublicPriceInfo;
    }
  | {
      type: "switch_ip";
    };

export type PublicAnswerResponse = {
  answer: string;
  received_at?: string;
  answered_at?: string;
  user_intent: PublicUserIntent | null;
  details?: Record<string, unknown>;
};

export type PublicContextEstimateResponse = {
  mode: "public" | string;
  context_usage: ContextUsage;
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

export type AdminLoginResponse = {
  user: AdminUser;
  token?: string;
  expires_at?: string;
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

export type LLMModel = {
  id: string;
  display_name: string;
  provider: string;
  base_url: string;
  model_name: string;
  has_api_key: boolean;
  api_key_mask: string;
  is_active: boolean;
  timeout_sec: number;
  admin_timeout_sec: number;
  created_at: string;
  updated_at: string;
};

export type LLMModelsResponse = {
  models: LLMModel[];
};

export type LLMModelResponse = {
  model: LLMModel;
};

export type LLMModelTestResponse = {
  ok: boolean;
  message: string;
  latency_ms: number;
  tested_at: string;
};

export type ReviewItem = {
  id: string;
  path: string;
  question: string;
  original_question?: string;
  draft_answer: string;
  suggested_target_path: string;
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
  preview: "markdown" | "download";
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
  preview: "markdown" | "download";
  content?: string;
  download_url: string;
};

export type SyncStatusFile = {
  path: string;
  old_path?: string;
  status: string;
  index: string;
  worktree: string;
  preview: "markdown" | "download";
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
