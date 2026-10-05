import { fileURLToPath } from "node:url"
import { defineConfig } from "vitest/config"

export default defineConfig({
    resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
    test: {
        coverage: {
            // Name the application sources explicitly. Without `include`, Vitest
            // reports only the files a test happened to import, so the summary
            // described a subset of the dashboard instead of the dashboard.
            include: ["src/**/*.{ts,tsx}"],
            exclude: [
                // Test files are the subject of the measurement, not part of it.
                "src/**/*.{test,spec}.{ts,tsx}",
                "src/**/__tests__/**",
                // Type declarations carry no runtime behaviour.
                "**/*.d.ts",
            ],
        },
    },
})
