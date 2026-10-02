export const BASE_PATH = "/chronoverse";
export const SITE_URL = "https://hitesh22rana.github.io/chronoverse";
export const REPOSITORY_URL = "https://github.com/hitesh22rana/chronoverse";
export const SOCIAL_IMAGE_PATH = "/assets/chronoverse-social.jpg";
export const SOCIAL_IMAGE_ALT = "Chronoverse astronaut with an hourglass visor";
export const SOCIAL_IMAGE_TYPE = "image/jpeg";

// First commit in this repository.
export const SITE_PUBLISHED = "2025-01-26";

export const AUTHOR_NAME = "Hitesh Rana";
export const AUTHOR_URL = "https://github.com/hitesh22rana";
export const AUTHOR_IMAGE = "https://github.com/hitesh22rana.png";
export const CONTACT_EMAIL = "hitesh22rana@gmail.com";

export const AUTHOR_PROFILES = [
  { name: "GitHub", url: "https://github.com/hitesh22rana" },
  { name: "LinkedIn", url: "https://www.linkedin.com/in/hitesh22rana" },
  { name: "X", url: "https://x.com/hitesh22rana" },
] as const;

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

export const SOCIAL_IMAGE_URL = siteAssetUrl(SOCIAL_IMAGE_PATH);
