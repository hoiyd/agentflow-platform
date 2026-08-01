import { Bot, Repeat2, Users } from "lucide-react";

import type { ChatMode } from "../../lib/api";

type ModeChooserProps = {
  chatMode: ChatMode;
  disabled: boolean;
  setChatMode: (mode: ChatMode) => void;
};

export function ModeChooser({ chatMode, disabled, setChatMode }: ModeChooserProps) {
  return (
    <section className="mode-chooser" aria-label="Chat mode">
      <button
        className={chatMode === "single" ? "active" : ""}
        disabled={disabled}
        onClick={() => setChatMode("single")}
        type="button"
      >
        <Bot size={16} />
        <span>Direct</span>
        <strong>Single agent</strong>
      </button>
      <button
        className={chatMode === "multi_agent" ? "active" : ""}
        disabled={disabled}
        onClick={() => setChatMode("multi_agent")}
        type="button"
      >
        <Users size={16} />
        <span>Coordinate</span>
        <strong>Multi-agent</strong>
      </button>
      <button
        className={chatMode === "autonomous" ? "active" : ""}
        disabled={disabled}
        onClick={() => setChatMode("autonomous")}
        type="button"
      >
        <Repeat2 size={16} />
        <span>Autonomous</span>
        <strong>Bounded loop</strong>
      </button>
    </section>
  );
}
