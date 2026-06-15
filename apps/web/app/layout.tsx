import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "AgentFlow Platform",
  description: "AI agent workflow platform with memory, tools, RAG, and replay"
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
