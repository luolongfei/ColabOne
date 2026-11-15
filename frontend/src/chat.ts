import type { Conversation, Message, Snapshot } from "./types";

// Snapshot polling and token streaming are independent. Address the current turn
// explicitly so a delayed snapshot cannot turn the previous answer into this one.
export function chatMessages(
  conversation: Conversation | undefined,
  stream: Snapshot["stream"],
  selected: string,
): Message[] {
  const messages = conversation?.messages.map((m) => ({ ...m })) || [];
  if (!stream || stream.conversation_id !== selected) return messages;
  const index =
    stream.message_index ??
    (messages.at(-1)?.status === "streaming"
      ? messages.length - 2
      : messages.length);
  if (
    !Number.isInteger(index) ||
    index < 0 ||
    messages.length < index ||
    messages.length > index + 2
  )
    return messages;
  const saved = messages[index + 1];
  // A persisted terminal answer is authoritative, even if the token reader lags.
  if (saved && saved.status !== "streaming") return messages;
  messages[index] = { role: "user", content: stream.text, status: "complete" };
  messages[index + 1] = {
    ...saved,
    role: "assistant",
    content:
      (saved?.content.length || 0) > stream.content.length
        ? saved!.content
        : stream.content,
    status:
      stream.state === "running"
        ? "streaming"
        : stream.state === "completed"
          ? "complete"
          : stream.state === "cancelled"
            ? "cancelled"
            : "interrupted",
  };
  return messages;
}
