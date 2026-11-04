import { afterEach, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";
import type { AnchorHTMLAttributes, ComponentType } from "react";
import { documentationMdxPlugin } from "../scripts/mdx-vitest-plugin";
import { useMDXComponents } from "../mdx-components";

afterEach(cleanup);
it("compiles actual MDX while leaving unrelated source files to their own loaders", async () => {
  expect(await documentationMdxPlugin.transform("const x = 1", "module.ts")).toBeUndefined();
  const result = await documentationMdxPlugin.transform("# Documentation\n\nA contract.", "guide.mdx");
  expect(result?.code).toContain("Documentation");
  expect(result?.code).toContain("useMDXComponents");
  expect(result?.map).toBeNull();
  await expect(documentationMdxPlugin.transform("{unfinished", "invalid.mdx")).rejects.toThrow();
});
it("rewrites internal MDX links with canonical base paths and preserved anchors", () => {
  const components = useMDXComponents();
  const Link = components.a as ComponentType<AnchorHTMLAttributes<HTMLAnchorElement>>;
  const { container, rerender } = render(<Link href="/docs/concepts/jobs#retention">Jobs</Link>);
  expect(container.querySelector("a")?.getAttribute("href")).toBe("/chronoverse/docs/concepts/jobs/#retention");
  for (const [href, expected] of [["/docs/", "/chronoverse/docs/"], ["/assets/logo.png", "/chronoverse/assets/logo.png"], ["https://example.org", "https://example.org"], [undefined, ""]] as const) {
    rerender(<Link href={href}>Contract</Link>);
    expect(container.querySelector("a")?.getAttribute("href")).toBe(expected);
  }
  const Heading = components.h1 as ComponentType;
  rerender(<Heading />);
  expect(container.innerHTML).toBe("");
});
