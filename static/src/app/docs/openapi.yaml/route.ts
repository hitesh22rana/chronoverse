import fs from "node:fs";

import { getOpenApiPath } from "@/lib/openapi";

export const dynamic = "force-static";

const openApiYaml = fs.readFileSync(getOpenApiPath(), "utf8");

export function GET() {
  return new Response(openApiYaml, { headers: { "Content-Type": "application/yaml; charset=utf-8" } });
}
