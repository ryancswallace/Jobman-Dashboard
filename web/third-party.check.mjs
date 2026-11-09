import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { build } from "vite";
import { moduleObservation, thirdPartyInventory } from "./third-party.mjs";

test("unknown injected code fails closed; known helpers retain rendered hashes", () => {
    assert.throws(
        () => moduleObservation("\0new-helper", "code", "/source/web"),
        /Unattributed/,
    );
    const first = moduleObservation(
        "\0rolldown/runtime.js",
        "one",
        "/source/web",
    );
    const second = moduleObservation(
        "\0rolldown/runtime.js",
        "two",
        "/source/web",
    );
    assert.equal(first.packagePath, "node_modules/rolldown");
    assert.notEqual(first.renderedSHA256, second.renderedSHA256);
    assert.throws(
        () => moduleObservation("/outside/code.js", "code", "/source/web"),
        /outside/,
    );
});

test("production build includes a dependency used only inside a worker", async () => {
    const root = mkdtempSync(join(tmpdir(), "dashboard-notices-"));
    try {
        mkdirSync(join(root, "node_modules/worker-only"), { recursive: true });
        writeFileSync(join(root, "package-lock.json"), "{}\n");
        writeFileSync(
            join(root, "index.html"),
            '<script type="module" src="/main.js"></script>',
        );
        writeFileSync(
            join(root, "main.js"),
            'new Worker(new URL("./worker.js", import.meta.url), {type:"module"});',
        );
        writeFileSync(
            join(root, "worker.js"),
            'import {message} from "worker-only"; postMessage(message);',
        );
        writeFileSync(
            join(root, "node_modules/worker-only/package.json"),
            '{"name":"worker-only","version":"1.0.0","type":"module","main":"index.js"}',
        );
        writeFileSync(
            join(root, "node_modules/worker-only/index.js"),
            'export const message="worker dependency";',
        );
        const result = await build({
            root,
            configFile: false,
            logLevel: "silent",
            plugins: [thirdPartyInventory()],
            build: { write: false },
        });
        const graph = JSON.parse(
            result.output.find(
                (item) => item.fileName === "third-party-web.json",
            ).source,
        );
        assert.ok(
            graph.observations.some(
                (item) => item.module === "node_modules/worker-only/index.js",
            ),
        );
        assert.ok(
            graph.observations.some((item) => item.kind === "injected-helper"),
        );
    } finally {
        rmSync(root, { recursive: true, force: true });
    }
});
