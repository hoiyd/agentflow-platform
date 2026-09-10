import assert from "node:assert/strict";
import test from "node:test";

import { removePendingMessages } from "./pending-messages.ts";

test("failed unstarted streams remove only their optimistic messages", () => {
  const messages = [
    { id: "persisted", content: "Earlier answer" },
    { id: "pending-user", content: "Try this" },
    { id: "pending-assistant", content: "" }
  ];

  assert.deepEqual(removePendingMessages(messages, ["pending-user", "pending-assistant"]), [messages[0]]);
});
