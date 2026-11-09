import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { thirdPartyInventory } from "./third-party.mjs";

export default defineConfig({
  plugins: [react(), thirdPartyInventory()],
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8088",
      "/auth": "http://127.0.0.1:8088",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
    restoreMocks: true,
  },
  build: { sourcemap: false, target: "es2022" },
});
