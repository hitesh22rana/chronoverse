export const BASE_PATH = "/chronoverse";
export const SITE_URL = "https://hitesh22rana.github.io/chronoverse";
export const REPOSITORY_URL = "https://github.com/hitesh22rana/chronoverse";
export const SOCIAL_IMAGE_PATH = "/assets/chronoverse-social.webp";
export const SOCIAL_IMAGE_ALT = "Chronoverse astronaut with an hourglass visor";

export function withBasePath(path: string) {
  if (!path.startsWith("/")) return path;
  return `${BASE_PATH}${path}`;
}

export function sitePageUrl(path = "/") {
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  return `${SITE_URL}${normalizedPath.endsWith("/") ? normalizedPath : `${normalizedPath}/`}`;
}

export function siteAssetUrl(path: string) {
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  return `${SITE_URL}${normalizedPath}`;
}
