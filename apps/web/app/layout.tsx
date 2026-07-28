import type { Metadata } from "next";
import "@fontsource-variable/manrope/wght.css";
import "@fontsource-variable/ibm-plex-sans/wght.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "@fontsource/ibm-plex-mono/600.css";
import "./styles/base.css";
import "./styles/shell.css";
import "./styles/chat.css";
import "./styles/tools.css";
import "./styles/knowledge.css";
import "./styles/composer-agent.css";
import "./styles/collaboration/panel.css";
import "./styles/collaboration/autonomous.css";
import "./styles/collaboration/dag.css";
import "./styles/collaboration/plan-steps.css";
import "./styles/replay.css";
import "./styles/run-usage.css";
import "./styles/responsive.css";
import "./styles/workbench/foundation.css";
import "./styles/workbench/home.css";
import "./styles/workbench/home-runtime.css";
import "./styles/workbench/shell.css";
import "./styles/workbench/chat.css";
import "./styles/workbench/composer.css";
import "./styles/workbench/verification.css";
import "./styles/workbench/tools-knowledge.css";
import "./styles/workbench/collaboration.css";
import "./styles/workbench/replay-overlays.css";
import "./styles/workbench/responsive.css";

export const metadata: Metadata = {
  title: "AgentFlow Platform",
  description:
    "Go-native AI agent workflow platform with multi-agent orchestration, hybrid RAG, tools, verification, budgets, traces, and replay"
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
