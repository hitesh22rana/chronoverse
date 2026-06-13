import fs from "node:fs";

import { getOpenApiPath } from "@/lib/openapi";

export const dynamic = "force-static";

export function GET() {
  return new Response(fs.readFileSync(getOpenApiPath(), "utf8"), { headers: { "Content-Type": "application/yaml; charset=utf-8" } });
}
