import type { CustomerChatHistoryItem } from "@/types/api";

const CUSTOMER_HISTORY_SYNTHETIC_ID = "customer-history-context";
const CUSTOMER_HISTORY_MAX_ITEMS = 9;
const CUSTOMER_HISTORY_RECENT_ITEMS = 6;
const CUSTOMER_HISTORY_EXCERPT_CHARS = 160;
const CUSTOMER_HISTORY_SUMMARY_CHARS = 1400;
const CUSTOMER_HISTORY_HEAD_EXCERPTS = 4;
const CUSTOMER_HISTORY_TAIL_EXCERPTS = 10;

export function compactCustomerHistoryForRouter(
  history?: CustomerChatHistoryItem[],
): CustomerChatHistoryItem[] | undefined {
  const normalized = normalizeCustomerHistory(history);
  if (normalized.length <= CUSTOMER_HISTORY_MAX_ITEMS) {
    return normalized.length > 0 ? normalized : undefined;
  }

  const recent = normalized.slice(-CUSTOMER_HISTORY_RECENT_ITEMS);
  const older = normalized.slice(0, -CUSTOMER_HISTORY_RECENT_ITEMS);
  const context = buildEarlierConversationContext(older);

  if (!context) {
    return recent;
  }

  return [
    {
      id: CUSTOMER_HISTORY_SYNTHETIC_ID,
      role: "assistant",
      content: context,
      created_at: older[older.length - 1]?.created_at,
    },
    ...recent,
  ];
}

function normalizeCustomerHistory(history?: CustomerChatHistoryItem[]) {
  if (!Array.isArray(history)) {
    return [];
  }
  return history.reduce<CustomerChatHistoryItem[]>((items, item) => {
    if (!item || (item.role !== "user" && item.role !== "assistant")) {
      return items;
    }
    const content = normalizeWhitespace(item.content);
    if (!content || item.id === CUSTOMER_HISTORY_SYNTHETIC_ID) {
      return items;
    }
    items.push({
      id: item.id,
      role: item.role,
      content,
      created_at: item.created_at,
    });
    return items;
  }, []);
}

function buildEarlierConversationContext(history: CustomerChatHistoryItem[]) {
  const excerpts = selectEarlierContextExcerpts(history);
  const lines: string[] = [
    "较早对话摘录（由前端为避免长对话丢上下文而压缩；只保留原话片段，不新增事实）：",
  ];
  for (const item of excerpts) {
    const excerpt = truncateText(item.content, CUSTOMER_HISTORY_EXCERPT_CHARS);
    if (!excerpt) {
      continue;
    }
    lines.push(`${item.role === "user" ? "客户" : "客服"}：${excerpt}`);
  }
  return truncateText(lines.join("\n"), CUSTOMER_HISTORY_SUMMARY_CHARS);
}

function selectEarlierContextExcerpts(history: CustomerChatHistoryItem[]) {
  if (history.length <= CUSTOMER_HISTORY_HEAD_EXCERPTS + CUSTOMER_HISTORY_TAIL_EXCERPTS) {
    return history;
  }
  return [
    ...history.slice(0, CUSTOMER_HISTORY_HEAD_EXCERPTS),
    ...history.slice(-CUSTOMER_HISTORY_TAIL_EXCERPTS),
  ];
}

function normalizeWhitespace(value: string) {
  return String(value ?? "").replace(/\s+/g, " ").trim();
}

function truncateText(value: string, maxChars: number) {
  const text = normalizeWhitespace(value);
  if (text.length <= maxChars) {
    return text;
  }
  return `${text.slice(0, Math.max(0, maxChars - 1)).trimEnd()}…`;
}
