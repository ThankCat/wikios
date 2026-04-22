import type { Metadata } from "next";
import "@/app/globals.css";

export const metadata: Metadata = {
  title: "WikiOS",
  description: "WikiOS chat workbench",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
