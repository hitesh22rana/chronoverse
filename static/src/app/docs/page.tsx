import type { Metadata } from "next";

import { DocShell } from "@/components/docs/doc-shell";
import Introduction from "../../../content/docs/introduction.mdx";
import { getDocHeadings, getDocNavigation } from "@/lib/docs";

export const metadata: Metadata = { title: "Chronoverse Documentation", description: "Product, engineering, API, deployment, and operations documentation for Chronoverse." };

export default function DocsIndexPage() {
  const navigation = getDocNavigation("introduction");
  return <DocShell activeSlug="introduction" description="Product, engineering, API, deployment, and operations documentation." headings={getDocHeadings("introduction")} next={navigation.next} title="Chronoverse documentation"><Introduction /></DocShell>;
}
