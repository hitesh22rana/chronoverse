import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";
import { documentationMdxPlugin } from "./scripts/mdx-vitest-plugin";

export default defineConfig({
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)), "@docs": fileURLToPath(new URL("./content/docs", import.meta.url)) } },
  plugins: [documentationMdxPlugin],
  test: {
    environment: "jsdom",
    maxWorkers: 2,
    include: ["tests/**/*.test.{ts,tsx}"],
    coverage: {
      include: ["src/**/*.{ts,tsx}", "scripts/**/*.ts", "*.{ts,tsx,mjs}"],
      exclude: ["**/*.d.ts", "vitest.config.ts"],
      reporter: ["json", "lcov", "text-summary"],
      provider: "v8",
    },
  },
});
