export type CustomerChatResponse = {
  answer: string;
  received_at?: string;
  answered_at?: string;
};

export type CustomerContextEstimateResponse = {
  mode: "customer" | string;
  context_usage: ContextUsage;
};

export type CustomerChatTraceResponse = Record<string, unknown>;

export type CustomerChatHistoryItem = {
  id?: string;
  role: "user" | "assistant";
  content: string;
  created_at?: string;
};

export type CustomerStreamEvent = {
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
  history?: CustomerChatHistoryItem[];
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

export type CustomerIntentsStatus = {
  path: string;
  loaded_at?: string;
  error?: string;
  warnings?: string[];
  rule_count: number;
};

export type CustomerIntentsResponse = {
  source: string;
  status: CustomerIntentsStatus;
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

export type AdminRuntimeSettings = {
  customer_query: {
    direct_min: number;
    review_min: number;
    candidate_top_k: number;
    max_evidence_chars: number;
    router_model_id?: string;
    specialist_model_id?: string;
    router_enable_thinking?: boolean;
    specialist_enable_thinking?: boolean;
  };
  support: {
    phone: string;
    wecom: string;
  };
  answer_log: {
    enabled: boolean;
    redact: boolean;
    retention_days: number;
  };
  knowledge: {
    max_text_file_kb: number;
  };
  sync: {
    remote: string;
    branch: string;
  };
};

export type AdminRuntimeEnvironment = {
  server_port: number;
  server_mode: string;
  wiki_root: string;
  wiki_name: string;
  qmd_index: string;
  workspace_dir: string;
  sqlite_path: string;
  web_dist_dir: string;
  web_enabled: boolean;
  customer_intents_path: string;
};

export type AdminRuntimeSettingsResponse = {
  settings: AdminRuntimeSettings;
  defaults: AdminRuntimeSettings;
  updated_at?: string;
  environment: AdminRuntimeEnvironment;
};

export type AdminDashboardResponse = {
  active_model?: LLMModel;
  models_total: number;
  review_pending: number;
  sync: {
    branch: string;
    remote: string;
    ahead: number;
    behind: number;
    changed_count: number;
    can_push: boolean;
    repo_ready: boolean;
    remote_ready: boolean;
    branch_ready: boolean;
    auth_configured: boolean;
    needs_setup: boolean;
    remote_url_redacted: string;
    configured_url_redacted: string;
    remote_matches_configured: boolean;
    setup_hint: string;
    error?: string;
  };
  qmd: {
    ok: boolean;
    index: string;
    root: string;
    message?: string;
    error?: string;
  };
  customer_chat_log: {
    enabled: boolean;
    redact: boolean;
    retention_days: number;
    path?: string;
  };
  recent_errors: Array<{
    scope: string;
    message: string;
  }>;
  generated_at: string;
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
  editable: boolean;
  text_kind: string;
  sha256: string;
  encoding: string;
  content?: string;
  download_url: string;
};

export type WikiSaveFileRequest = {
  path: string;
  content: string;
  expected_sha256: string;
};

export type WikiSaveFileResponse = WikiFileResponse;

export type WikiReplaceFileResponse = WikiFileResponse;

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
  can_commit: boolean;
  repo_ready: boolean;
  remote_ready: boolean;
  branch_ready: boolean;
  auth_configured: boolean;
  needs_setup: boolean;
  clean: boolean;
  remote_url_redacted: string;
  configured_url_redacted: string;
  remote_matches_configured: boolean;
  setup_hint: string;
  commits_to_push: SyncCommitInfo[];
  recent_commits: SyncCommitInfo[];
  files: SyncStatusFile[];
};

export type SyncDiagnosticResponse = {
  ok: boolean;
  action?: string;
  remote?: string;
  branch?: string;
  status?: SyncStatusResponse;
  stdout: string;
  stderr: string;
  exit_code: number;
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

export type CustomerConversationSummary = {
  id: string;
  session_id: string;
  user_id?: string;
  title: string;
  first_question: string;
  last_question: string;
  last_answer: string;
  last_answer_mode?: string;
  message_count: number;
  turn_count: number;
  started_at: string;
  updated_at: string;
  last_intent_type?: string;
};

export type CustomerConversationMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  created_at: string;
  trace_id?: string;
  message_id?: string;
  answer_mode?: string;
  process_summary?: string;
  details?: Record<string, unknown>;
};

export type CustomerConversationLogSummary = {
  enabled: boolean;
  redact: boolean;
  retention_days: number;
  path?: string;
};

export type CustomerConversationsResponse = {
  conversations: CustomerConversationSummary[];
  total: number;
  page: number;
  page_size: number;
  has_more: boolean;
  log: CustomerConversationLogSummary;
};

export type CustomerConversationDetailResponse = {
  conversation: CustomerConversationSummary;
  messages: CustomerConversationMessage[];
  log: CustomerConversationLogSummary;
};
