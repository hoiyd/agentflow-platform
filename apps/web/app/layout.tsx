import type { Metadata } from "next";
import "./styles/base.css";
import "./styles/home.css";
import "./styles/shell.css";
import "./styles/chat.css";
import "./styles/tools.css";
import "./styles/knowledge.css";
import "./styles/composer-agent.css";
import "./styles/collaboration.css";
import "./styles/replay.css";
import "./styles/responsive.css";

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
