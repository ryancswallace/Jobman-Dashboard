import type { PresentationTheme } from "../../../contracts/typescript/dashboard.generated";

function luminance(value: string): number {
  const channels = [1, 3, 5].map((index) => {
    const channel = parseInt(value.slice(index, index + 2), 16) / 255;
    return channel <= 0.04045
      ? channel / 12.92
      : ((channel + 0.055) / 1.055) ** 2.4;
  });
  return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
}
function contrast(a: string, b: string): number {
  const x = luminance(a),
    y = luminance(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}
export function validateTheme(theme: PresentationTheme | undefined): void {
  if (theme === undefined) return;
  for (const mode of ["light", "dark"] as const) {
    const pair = theme?.[mode];
    if (
      !pair ||
      !/^#[0-9a-fA-F]{6}$/.test(pair.accent) ||
      !/^#[0-9a-fA-F]{6}$/.test(pair.foreground) ||
      contrast(pair.accent, pair.foreground) < 4.5
    )
      throw new Error("Invalid brand color pair");
    const surfaces =
      mode === "light"
        ? ["#ffffff", "#f7f8fc", "#fafafe", "#efebfb"]
        : ["#000000", "#14141d", "#1c1c28", "#222230", "#312645"];
    if (surfaces.some((surface) => contrast(pair.accent, surface) < 4.5))
      throw new Error("Brand accent must remain readable");
  }
}
export function applyTheme(theme: PresentationTheme | undefined): () => void {
  validateTheme(theme);
  const root = document.documentElement;
  const names = [
    "--brand-light-accent",
    "--brand-light-foreground",
    "--brand-dark-accent",
    "--brand-dark-foreground",
  ];
  if (theme) {
    root.dataset.brandTheme = "custom";
    [
      theme.light.accent,
      theme.light.foreground,
      theme.dark.accent,
      theme.dark.foreground,
    ].forEach((value, index) => root.style.setProperty(names[index], value));
  }
  return () => {
    delete root.dataset.brandTheme;
    names.forEach((name) => root.style.removeProperty(name));
  };
}
