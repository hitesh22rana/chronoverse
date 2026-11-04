import { compile } from "@mdx-js/mdx";

export const documentationMdxPlugin = {
  name: "documentation-mdx",
  enforce: "pre" as const,
  async transform(source: string, id: string) {
    if (!id.endsWith(".mdx")) return;
    return { code: String(await compile(source, { providerImportSource: "@mdx-js/react" })), map: null };
  },
};
