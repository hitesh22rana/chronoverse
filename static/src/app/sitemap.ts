import type { MetadataRoute } from "next";

import { docPages } from "../../docs.config";
import { getOpenApiOperations } from "@/lib/openapi";
import { SITE_URL } from "@/lib/site";

export const dynamic = "force-static";

export default function sitemap(): MetadataRoute.Sitemap {
  const routes = ["", "/docs", ...docPages.map((page) => `/docs/${page.slug}`), ...getOpenApiOperations().map((operation) => `/docs/api/reference/${operation.operationId}`)];
  return routes.map((route) => ({ url: `${SITE_URL}${route}`, lastModified: new Date(), changeFrequency: route.startsWith("/docs/api/reference") ? "monthly" : "weekly", priority: route === "" ? 1 : route === "/docs" ? 0.9 : 0.7 }));
}
