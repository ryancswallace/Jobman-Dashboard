import { describe, it, expect } from "vitest";
import { appendLog, safeLogText } from "./logs";
const empty = { text: "", evicted: false, gap: false };
describe("bounded log presentation", () => {
  it("removes terminal escape capabilities while keeping text untrusted/plain", () => {
    expect(
      safeLogText(
        "\x1b[31mred\x1b[0m\x1b]8;;https://host\x07link\x1b]8;;\x07\x00",
      ),
    ).toBe("red[terminal control omitted]link[terminal control omitted]�");
    expect(safeLogText("<script>alert(1)</script>")).toBe(
      "<script>alert(1)</script>",
    );
  });
  it("preserves original offsets when rendering UTF-8 and evicting memory", () => {
    const initial = appendLog(
      empty,
      {
        text: "😀".repeat(10),
        startOffset: "100",
        endOffset: "140",
        runId: "run1",
      },
      9,
    );
    expect(new TextEncoder().encode(initial.text).length).toBeLessThanOrEqual(
      9,
    );
    expect(initial.text).not.toContain("�");
    expect(initial.endOffset).toBe("140");
    expect(initial.evicted).toBe(true);
  });
  it("does not duplicate a replayed byte range", () => {
    const chunk = {
      text: "abcd",
      startOffset: "0",
      endOffset: "4",
      runId: "r",
    };
    const current = appendLog(empty, chunk);
    expect(appendLog(current, chunk)).toBe(current);
  });
  it("refuses a gap rather than silently appending out-of-order data", () => {
    const current = appendLog(empty, {
      text: "abc",
      startOffset: "0",
      endOffset: "3",
    });
    const next = appendLog(current, {
      text: "xyz",
      startOffset: "5",
      endOffset: "8",
    });
    expect(next.text).toBe("abc");
    expect(next.gap).toBe(true);
  });
  it("never mixes two executions and visibly records reset", () => {
    const current = appendLog(empty, {
      text: "old",
      startOffset: "0",
      endOffset: "3",
      runId: "r1",
    });
    const next = appendLog(current, {
      text: "new",
      startOffset: "0",
      endOffset: "3",
      runId: "r2",
    });
    expect(next.text).toBe("new");
    expect(next.gap).toBe(true);
  });
  it("caps a pathological long line", () => {
    expect(safeLogText("a".repeat(100000)).length).toBeLessThan(16500);
  });
});
