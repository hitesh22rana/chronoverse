function getCookie(name: string) {
    return document.cookie
        .split("; ")
        .find((c) => c.startsWith(name + "="))
        ?.split("=")[1] ?? ""
}

async function resolveCsrfToken(url: string): Promise<string> {
    const fromCookie = getCookie("csrf")
    if (fromCookie) {
        return fromCookie
    }
    try {
        const res = await fetch(new URL("/auth/csrf", url).href, {
            credentials: "include",
        })
        if (res.ok) {
            const data = (await res.json()) as { csrfToken?: string }
            return data.csrfToken ?? ""
        }
    } catch {
        // No token available; caller sends the request without it.
    }
    return ""
}

async function fetchWithCredentials(url: string, options: RequestInit = {}) {
    const headers = new Headers(options.headers)
    if (!headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json")
    }
    const method = (options.method ?? "GET").toUpperCase()
    if (!headers.has("X-CSRF-Token") && method !== "GET" && method !== "HEAD" && typeof document !== "undefined") {
        const csrf = await resolveCsrfToken(url)
        if (csrf) {
            headers.set("X-CSRF-Token", csrf)
        }
    }

    return fetch(url, {
        ...options,
        credentials: "include",
        headers,
    })
}

export async function fetchApi(
    url: string,
    errorMessage = "API request failed",
    options: RequestInit = {},
) {
    const response = await fetchWithCredentials(url, options)
    if (!response.ok) {
        throw new Error(errorMessage)
    }

    return response
}

export async function fetchApiJson<T>(
    url: string,
    errorMessage = "API request failed",
    options: RequestInit = {},
) {
    const response = await fetchApi(url, errorMessage, options)
    return response.json() as Promise<T>
}

export function createIdempotencyKey() {
    if (typeof globalThis.crypto?.randomUUID !== "function") {
        throw new Error("secure idempotency key generation is unavailable")
    }

    return globalThis.crypto.randomUUID()
}
