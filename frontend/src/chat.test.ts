import { describe, expect, it } from "vitest";
import { chatMessages } from "./chat";
import type { Conversation, Snapshot } from "./types";

const history: Conversation = {
  id: "thread",
  title: "Chat",
  updated_at: 1,
  messages: [
    { role: "user", content: "first question", status: "complete" },
    { role: "assistant", content: "first answer", status: "complete" },
  ],
};
const stream: NonNullable<Snapshot["stream"]> = {
  conversation_id: "thread",
  message_index: 2,
  text: "second question",
  content: "second answer",
  state: "running",
};

describe("chat snapshot and stream ordering", () => {
  it("keeps the preceding turn when a snapshot lags the second reply", () => {
    const result = chatMessages(history, stream, history.id);
    expect(result.map((m) => m.content)).toEqual([
      "first question",
      "first answer",
      "second question",
      "second answer",
    ]);
    expect(history.messages).toHaveLength(2);
  });
  it("keeps the last streamed reply visible while the terminal snapshot catches up", () => {
    for (const [state, status] of [
      ["completed", "complete"],
      ["cancelled", "cancelled"],
      ["disconnected", "interrupted"],
    ]) {
      const result = chatMessages(history, { ...stream, state }, history.id);
      expect(result[3]).toMatchObject({ content: "second answer", status });
    }
  });
  it("never duplicates a persisted terminal answer", () => {
    const saved = {
      ...history,
      messages: chatMessages(
        history,
        { ...stream, state: "completed" },
        history.id,
      ),
    };
    expect(chatMessages(saved, stream, history.id)).toEqual(saved.messages);
  });
  it("does not roll back a snapshot which is ahead of the token reader", () => {
    const saved = {
      ...history,
      messages: chatMessages(history, stream, history.id),
    };
    expect(
      chatMessages(saved, { ...stream, content: "second" }, history.id)[3]
        ?.content,
    ).toBe("second answer");
  });
  it("does not mix conversations or overwrite a newer turn", () => {
    expect(chatMessages(history, stream, "other")).toEqual(history.messages);
    const saved = {
      ...history,
      messages: [...history.messages, ...history.messages, ...history.messages],
    };
    expect(chatMessages(saved, stream, history.id)).toEqual(saved.messages);
  });
});
