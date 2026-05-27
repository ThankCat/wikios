import { redirect } from "next/navigation";

export default function ModelsPage() {
  redirect("/settings?tab=models");
}
