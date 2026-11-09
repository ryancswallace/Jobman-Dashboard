import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { isAbsolute, relative, resolve } from "node:path";

// These virtual modules contain upstream code even though they have no file in
// node_modules. An unfamiliar virtual module must be reviewed before shipping.
const helpers = {
    "\0rolldown/runtime.js": "node_modules/rolldown",
    "\0vite/modulepreload-polyfill.js": "node_modules/vite",
    "\0vite/preload-helper.js": "node_modules/vite",
};
const sha256 = (data) => createHash("sha256").update(data).digest("hex");

export function moduleObservation(id, code, root) {
    if (id.startsWith("\0")) {
        const packagePath = helpers[id];
        if (!packagePath)
            throw new Error(`Unattributed bundled virtual module: ${id}`);
        return {
            kind: "injected-helper",
            module: id.slice(1),
            packagePath,
            renderedSHA256: sha256(code),
        };
    }
    const path = relative(root, id.split("?")[0]).replaceAll("\\", "/");
    if (path.startsWith("node_modules/")) {
        return {
            kind: "bundled-module",
            module: path,
            sourceSHA256: sha256(readFileSync(id.split("?")[0])),
        };
    }
    // The only allowed source outside web is the generated first-party contract.
    if (
        isAbsolute(path) ||
        (path.startsWith("../") && !path.startsWith("../contracts/typescript/"))
    ) {
        throw new Error(`Bundled module is outside the source tree: ${id}`);
    }
    return undefined;
}

export function thirdPartyInventory() {
    let root;
    const observations = new Map();
    function collect(bundle) {
        for (const output of Object.values(bundle)) {
            if (output.type === "asset") {
                for (const name of output.originalFileNames ?? []) {
                    const observation = moduleObservation(
                        resolve(root, name),
                        "",
                        root,
                    );
                    if (observation)
                        observations.set(
                            JSON.stringify(observation),
                            observation,
                        );
                }
                continue;
            }
            for (const [id, module] of Object.entries(output.modules)) {
                // CSS is extracted into assets, so its JS rendered length is zero.
                if (
                    !module.renderedLength &&
                    !id.split("?")[0].endsWith(".css")
                )
                    continue;
                const observation = moduleObservation(
                    id,
                    module.code ?? "",
                    root,
                );
                if (observation)
                    observations.set(JSON.stringify(observation), observation);
            }
        }
    }
    return {
        name: "jobman-third-party-inventory",
        apply: "build",
        config() {
            return {
                worker: {
                    plugins: () => [
                        {
                            name: "jobman-worker-third-party-inventory",
                            generateBundle(_options, bundle) {
                                collect(bundle);
                            },
                        },
                    ],
                },
            };
        },
        configResolved(config) {
            root = resolve(config.root);
        },
        buildStart() {
            observations.clear();
        },
        generateBundle(_options, bundle) {
            collect(bundle);
            this.emitFile({
                type: "asset",
                fileName: "third-party-web.json",
                source:
                    JSON.stringify(
                        {
                            schemaVersion: 1,
                            lockSHA256: sha256(
                                readFileSync(
                                    resolve(root, "package-lock.json"),
                                ),
                            ),
                            observations: [...observations.values()].sort(
                                (a, b) =>
                                    a.module < b.module
                                        ? -1
                                        : a.module > b.module
                                          ? 1
                                          : 0,
                            ),
                        },
                        null,
                        2,
                    ) + "\n",
            });
        },
    };
}
