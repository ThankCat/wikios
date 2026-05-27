import { api } from "@/lib/api";

export const reviewAPI = {
  count: api.reviewCount,
  next: api.reviewNext,
  approve: api.reviewApprove,
  reject: api.reviewReject,
  delete: api.reviewDelete,
};

