export const BASE_PATH = "/chronoverse";
export const SITE_URL = "https://hitesh22rana.github.io/chronoverse";
export const REPOSITORY_URL = "https://github.com/hitesh22rana/chronoverse";

export function withBasePath(path: string) {
  if (!path.startsWith("/")) return path;
  return `${BASE_PATH}${path}`;
}
