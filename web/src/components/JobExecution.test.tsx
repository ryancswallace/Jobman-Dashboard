import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { Job } from "../lib/models";
import { JobExecution } from "./JobExecution";

const job: Job = {
  deploymentId: "source",
  namespaceId: "namespace",
  jobId: "job",
  phase: "running",
  revision: "1",
  createdAt: "2026-10-06T00:00:00Z",
};
afterEach(() => vi.unstubAllGlobals());

it("renders and copies exact executable and argument boundaries without interpreting content", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  const argv = [
    "/bin/sh",
    "-c",
    "printf '%s\\n' '<script>alert(1)</script>'\necho done",
    "",
    "two words",
    "a\tb",
    "$HOME",
    "☃",
  ];
  const { container } = render(
    <JobExecution
      job={{
        ...job,
        execution: {
          command: { executable: argv[0], args: argv.slice(1) },
          workingDirectory: "workspace:/a directory",
        },
      }}
    />,
  );
  const text = screen.getByLabelText(
    "Submitted command argument vector",
  ).textContent!;
  expect(JSON.parse(text)).toEqual(argv);
  expect(container.querySelector("script")).toBeNull();
  expect(screen.getByText("workspace:/a directory")).toBeVisible();
  expect(screen.getByText("Submitted working directory")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Copy command as JSON" }));
  await screen.findByRole("status");
  expect(JSON.parse(writeText.mock.calls[0][0])).toEqual(argv);
});

it("offers selectable exact text when clipboard permission is unavailable", async () => {
  vi.stubGlobal("navigator", {
    clipboard: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
  });
  render(
    <JobExecution
      job={{
        ...job,
        execution: {
          command: { executable: "true", args: [] },
          workingDirectory: "",
        },
      }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Copy command as JSON" }));
  await waitFor(() =>
    expect(screen.getByRole("status")).toHaveTextContent("Select and copy"),
  );
  expect(
    screen.getByLabelText("Submitted command argument vector"),
  ).toHaveAttribute("tabindex", "0");
  expect(screen.getByText("Not specified")).toBeVisible();
});

it("makes invisible Unicode controls inspectable without changing displayed or copied argument values", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  const argv = [
    "tool\u202e",
    "a\u2066b\u2069",
    "x\u007fy\u0085",
    "line\u2028paragraph\u2029",
    "tag\u{e0001}",
    "\t\n",
  ];
  render(
    <JobExecution
      job={{
        ...job,
        execution: {
          command: { executable: argv[0], args: argv.slice(1) },
          workingDirectory: "workspace:/",
        },
      }}
    />,
  );
  const displayed = screen.getByLabelText(
    "Submitted command argument vector",
  ).textContent!;
  expect(JSON.parse(displayed)).toEqual(argv);
  expect(displayed).not.toMatch(/[\p{Cf}\p{Zl}\p{Zp}\u007f-\u009f]/u);
  expect(displayed).toContain("\\u202e");
  expect(displayed).toContain("\\udb40\\udc01");
  fireEvent.click(screen.getByRole("button", { name: "Copy command as JSON" }));
  await screen.findByRole("status");
  expect(writeText).toHaveBeenCalledWith(JSON.stringify(argv, null, 2));
  expect(JSON.parse(writeText.mock.calls[0][0])).toEqual(argv);
});

it.each([
  ["missing", "Control has no submitted execution specification"],
  ["unsupported", "This Control deployment does not provide"],
  ["too_large", "No partial command is shown"],
  ["future_reason", "The submitted execution specification is unavailable"],
  [undefined, "The submitted execution specification is unavailable"],
])(
  "keeps unavailable specification (%s) distinct from an empty command",
  (reason, message) => {
    render(
      <JobExecution job={{ ...job, executionUnavailableReason: reason }} />,
    );
    expect(screen.getByText(message, { exact: false })).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Copy command as JSON" }),
    ).toBeNull();
    expect(
      screen.queryByLabelText("Submitted command argument vector"),
    ).toBeNull();
  },
);
