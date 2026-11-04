// @vitest-environment node
import { expect, it } from "vitest";
import config from "../next.config";
const postcssModule = "../postcss.config.mjs";
const postcss = (await import(postcssModule)).default as { plugins: Record<string, unknown> };

it("exports static pages with MDX enabled and the canonical GitHub Pages base path", () => {
  expect(config).toMatchObject({ output: "export", basePath: "/chronoverse", trailingSlash: true, images: { unoptimized: true } });
  expect(config.pageExtensions).toContain("mdx");
  expect(postcss.plugins).toHaveProperty("@tailwindcss/postcss");
});
