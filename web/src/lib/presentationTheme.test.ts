import { describe, expect, it } from "vitest";
import { applyTheme, validateTheme } from "./presentationTheme";

const theme = {
  light: { accent: "#00675f", foreground: "#ffffff" },
  dark: { accent: "#78d9cb", foreground: "#14141d" },
};
describe("bounded brand theme", () => {
  it("validates contrast on reading surfaces and pairs without allowing CSS", () => {
    expect(() => validateTheme(undefined)).not.toThrow();
    expect(() => validateTheme(theme)).not.toThrow();
    for (const pair of [
      { accent: "red", foreground: "#ffffff" },
      { accent: "#fff", foreground: "#000000" },
      { accent: "#ffffff", foreground: "#000000" },
      { accent: "#00675f;url(x)", foreground: "#ffffff" },
      { accent: "#00675f", foreground: "#00675f" },
    ]) {
      expect(() => validateTheme({ ...theme, light: pair })).toThrow();
    }
    expect(() =>
      validateTheme({
        ...theme,
        dark: { accent: "#14141d", foreground: "#ffffff" },
      }),
    ).toThrow();
  });
  it("applies light and dark pairs and restores the default on cleanup", () => {
    const cleanup = applyTheme(theme);
    expect(document.documentElement.dataset.brandTheme).toBe("custom");
    expect(
      document.documentElement.style.getPropertyValue("--brand-dark-accent"),
    ).toBe("#78d9cb");
    cleanup();
    expect(document.documentElement.dataset.brandTheme).toBeUndefined();
    expect(
      document.documentElement.style.getPropertyValue("--brand-dark-accent"),
    ).toBe("");
  });
});
