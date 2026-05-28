import { api } from "@/lib/api";

export const chatAPI = {
  adminChat: api.adminChat,
  adminChatStream: api.adminChatStream,
  estimateAdminContext: api.estimateAdminContext,
  customerChat: api.customerChat,
  estimateCustomerContext: api.estimateCustomerContext,
};
