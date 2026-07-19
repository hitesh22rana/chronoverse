import type { Metadata } from "next";

import { DocShell } from "@/components/docs/doc-shell";
import Introduction from "../../../content/docs/introduction.mdx";
import { getDocHeadings, getDocNavigation } from "@/lib/docs";
import { SOCIAL_IMAGE_ALT, SOCIAL_IMAGE_PATH, sitePageUrl, withBasePath } from "@/lib/site";

const title = "Chronoverse Documentation";
const description = "Product, engineering, API, deployment, and operations documentation for Chronoverse.";
const url = sitePageUrl("/docs");

export const metadata: Metadata = {
  title,
  description,
  alternates: { canonical: url },
  openGraph: { title, description, type: "website", url, images: [{ url: withBasePath(SOCIAL_IMAGE_PATH), width: 1200, height: 630, alt: SOCIAL_IMAGE_ALT }] },
  twitter: { card: "summary_large_image", title, description, images: [{ url: withBasePath(SOCIAL_IMAGE_PATH), alt: SOCIAL_IMAGE_ALT }] },
};

export default function DocsIndexPage() {
  const navigation = getDocNavigation("introduction");
  return <DocShell activeSlug="introduction" canonicalPath="/docs" description="Product, engineering, API, deployment, and operations documentation." headings={getDocHeadings("introduction")} next={navigation.next} title="Chronoverse documentation"><Introduction /></DocShell>;
}
