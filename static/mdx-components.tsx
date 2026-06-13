import type { MDXComponents } from "mdx/types";
import type { AnchorHTMLAttributes } from "react";

import {
  ArchitectureMap,
  Callout,
  CardGrid,
  DocCard,
  Endpoint,
  SourceLink,
  Step,
  Steps,
} from "@/components/docs/mdx-blocks";
import { CodeBlock } from "@/components/docs/code-block";
import { withBasePath } from "@/lib/site";

function MdxLink({ href = "", ...props }: AnchorHTMLAttributes<HTMLAnchorElement>) {
  if (!href.startsWith("/")) return <a href={href} {...props} />;

  const [path, hash] = href.split("#", 2);
  const trailingPath = path.startsWith("/docs/") && !path.endsWith("/") ? `${path}/` : path;
  const resolvedHref = `${withBasePath(trailingPath)}${hash ? `#${hash}` : ""}`;

  return <a href={resolvedHref} {...props} />;
}

const components: MDXComponents = {
  a: MdxLink,
  h1: ({ children }) => <h1 className="mdx-page-title">{children}</h1>,
  pre: CodeBlock,
  ArchitectureMap,
  Callout,
  CardGrid,
  DocCard,
  Endpoint,
  SourceLink,
  Step,
  Steps,
};

export function useMDXComponents(): MDXComponents {
  return components;
}
