import type { Metadata } from "next";

import { DocShell } from "@/components/docs/doc-shell";
import Introduction from "../../../content/docs/introduction.mdx";
import { getDocHeadings, getDocNavigation } from "@/lib/docs";
import { SITE_URL } from "@/lib/site";

const title = "Chronoverse Documentation";
const description = "Product, engineering, API, deployment, and operations documentation for Chronoverse.";
const url = `${SITE_URL}/docs/`;

export const metadata: Metadata = {
  title,
  description,
  alternates: { canonical: url },
  openGraph: { title, description, type: "website", url },
  twitter: { card: "summary", title, description },
};

export default function DocsIndexPage() {
  const navigation = getDocNavigation("introduction");
  return <DocShell activeSlug="introduction" description="Product, engineering, API, deployment, and operations documentation." headings={getDocHeadings("introduction")} next={navigation.next} title="Chronoverse documentation"><Introduction /></DocShell>;
}
