export type AppConfig = {
  mountedWikiName: string;
  webEnabled: boolean;
};

export type PublicSource = {
  path: string;
  title: string;
  confidence: string;
};

export type PublicAnswerResponse = {
  answer: string;
  answer_type: string;
  answer_markdown: string;
  sources: PublicSource[];
  confidence: number;
  notes?: string;
  trace_id: string;
};

export type TaskAccepted = {
  task_id: string;
  status: string;
};

export type TaskStep = {
  name: string;
  tool?: string;
  status: string;
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  duration_ms?: number;
};

export type TaskRecord = {
  id: string;
  type: string;
  status: string;
  result?: Record<string, unknown>;
  error?: string;
  steps: TaskStep[];
  created_at: string;
  updated_at: string;
};

export type AdminResult = Record<string, unknown>;

