import {
  Activity,
  ClipboardCheck,
  Database,
  FileJson,
  History,
  LayoutDashboard,
  MessagesSquare,
  Settings,
} from "lucide-react";

export type AdminModuleId =
  | "dashboard"
  | "conversations"
  | "models"
  | "knowledge"
  | "review"
  | "prompts"
  | "logs"
  | "settings";

export type AdminNavItem = {
  id: AdminModuleId;
  label: string;
  description: string;
  path: string;
  icon: typeof LayoutDashboard;
};

export const adminModulePaths: Record<AdminModuleId, string> = {
  dashboard: "/dashboard",
  conversations: "/conversations",
  models: "/models",
  knowledge: "/knowledge",
  review: "/review",
  prompts: "/prompts",
  logs: "/logs",
  settings: "/settings",
};

export const adminNavItems: AdminNavItem[] = [
  { id: "dashboard", label: "总览", description: "系统状态与近期风险", path: adminModulePaths.dashboard, icon: LayoutDashboard },
  { id: "conversations", label: "用户会话", description: "客户问答记录", path: adminModulePaths.conversations, icon: MessagesSquare },
  { id: "knowledge", label: "知识库", description: "浏览、助手、运维与同步", path: adminModulePaths.knowledge, icon: Database },
  { id: "review", label: "审查", description: "低置信回答审查队列", path: adminModulePaths.review, icon: ClipboardCheck },
  { id: "prompts", label: "提示词", description: "Prompt 查看、测试与版本预留", path: adminModulePaths.prompts, icon: FileJson },
  { id: "logs", label: "日志", description: "trace、模型调用和切换记录", path: adminModulePaths.logs, icon: History },
  { id: "settings", label: "设置", description: "模型、问答、日志、知识库运行配置", path: adminModulePaths.settings, icon: Settings },
];

export function adminModulePath(module: AdminModuleId) {
  return adminModulePaths[module];
}

export const systemStatusIcon = Activity;
