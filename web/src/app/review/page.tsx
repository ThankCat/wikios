import { AdminModulePage } from "@/features/admin-shell/admin-module-page";

import { ReviewApp } from "./review-app";

export default function ReviewPage() {
  return (
    <AdminModulePage activeModule="review">
      <ReviewApp />
    </AdminModulePage>
  );
}
