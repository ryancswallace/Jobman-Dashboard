const MAX_BUFFER_BYTES = 2 * 1024 * 1024;
const MAX_LINE = 16384;
/** Render plain text only. OSC/CSI/control sequences never become active links or HTML. */
export function safeLogText(text: string): string {
  return text
    .replace(
      /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?/g,
      "[terminal control omitted]",
    )
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g, "�")
    .split("\n")
    .map((line) =>
      line.length > MAX_LINE
        ? `${line.slice(0, MAX_LINE)} [long line truncated]`
        : line,
    )
    .join("\n");
}
export interface LogBuffer {
  text: string;
  startOffset?: string;
  endOffset?: string;
  runId?: string;
  evicted: boolean;
  gap: boolean;
}
export function appendLog(
  previous: LogBuffer,
  chunk: {
    text: string;
    startOffset: string;
    endOffset: string;
    runId?: string;
  },
  limit = MAX_BUFFER_BYTES,
): LogBuffer {
  if (previous.runId && chunk.runId && previous.runId !== chunk.runId)
    return {
      text: safeLogText(chunk.text),
      startOffset: chunk.startOffset,
      endOffset: chunk.endOffset,
      runId: chunk.runId,
      evicted: false,
      gap: true,
    };
  if (
    previous.endOffset &&
    BigInt(chunk.endOffset) <= BigInt(previous.endOffset) &&
    !(
      chunk.startOffset === previous.endOffset &&
      chunk.endOffset === previous.endOffset &&
      chunk.text
    )
  )
    return previous;
  if (previous.endOffset && chunk.startOffset !== previous.endOffset)
    return { ...previous, gap: true };
  const encoded = new TextEncoder().encode(
    previous.text + safeLogText(chunk.text),
  );
  const evicted = encoded.length > limit;
  let offset = Math.max(0, encoded.length - limit);
  // Avoid beginning a rendered buffer in the middle of a UTF-8 code point.
  while (offset < encoded.length && (encoded[offset] & 0xc0) === 0x80) offset++;
  return {
    text: new TextDecoder().decode(encoded.subarray(offset)),
    startOffset: previous.startOffset ?? chunk.startOffset,
    endOffset: chunk.endOffset,
    runId: chunk.runId ?? previous.runId,
    evicted: previous.evicted || evicted,
    gap: previous.gap,
  };
}
