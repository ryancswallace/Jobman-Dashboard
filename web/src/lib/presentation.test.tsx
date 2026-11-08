import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  decodePresentation,
  defaultPresentation,
  DeploymentNotices,
  PresentationProvider,
  usePresentation,
} from "./presentation";
import { nativeInboxLink } from "./privateLinks";

afterEach(() => {
  vi.unstubAllGlobals();
  document.title = "Jobman Dashboard";
});
function Name() {
  return <h1>{usePresentation().productName}</h1>;
}
describe("deployment presentation", () => {
  it("keeps a neutral default and rejects unsafe content and schemes", () => {
    expect(decodePresentation(defaultPresentation).productName).toBe(
      "Jobman Dashboard",
    );
    for (const patch of [
      { schemaVersion: 2 },
      { logoPath: "https://elsewhere.invalid/logo.svg" },
      { logoPath: "/../logo.svg" },
      { contentLinkScheme: "javascript" },
      {
        notices: [
          { id: "same", text: "a", severity: "info" },
          { id: "same", text: "b", severity: "info" },
        ],
      },
      {
        helpLinks: [
          { label: "Help", url: "https://user:password@example.invalid" },
        ],
      },
    ]) {
      expect(() =>
        decodePresentation({ ...defaultPresentation, ...patch }),
      ).toThrow();
    }
    expect(nativeInboxLink("item", "org.example.jobs")).toBe(
      "org.example.jobs://inbox/item",
    );
    expect(() => nativeInboxLink("item", "javascript")).toThrow();
  });
  it("loads only the public projection and renders notice text without markup", async () => {
    const fetcher = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          ...defaultPresentation,
          productName: "Example Jobs",
          notices: [
            { id: "one", text: "<script>example</script>", severity: "info" },
          ],
        }),
        { headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetcher);
    render(
      <PresentationProvider>
        <Name />
        <DeploymentNotices />
      </PresentationProvider>,
    );
    expect(
      await screen.findByRole("heading", { name: "Example Jobs" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("<script>example</script>").querySelector("script"),
    ).toBeNull();
    expect(fetcher).toHaveBeenCalledWith(
      "/presentation",
      expect.objectContaining({
        credentials: "omit",
        cache: "no-store",
        redirect: "error",
      }),
    );
    await waitFor(() => expect(document.title).toBe("Example Jobs"));
  });
  it("keeps neutral branding on unavailable presentation", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    render(
      <PresentationProvider>
        <Name />
      </PresentationProvider>,
    );
    expect(
      await screen.findByRole("heading", { name: "Jobman Dashboard" }),
    ).toBeInTheDocument();
  });
});
