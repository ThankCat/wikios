import { redirect } from "next/navigation";

export default function PublicConfigPage() {
  redirect("/settings?tab=intents");
}
