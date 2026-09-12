function getCookie(name: string) {
    return document.cookie
        .split("; ")
        .find((c) => c.startsWith(name + "="))
        ?.split("=")[1] ?? ""
}

async function fetchWithCredentials(url: string, options: RequestInit = {}) {
    const headers = new Headers(options.headers)
    if (!headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json")
    }
    const csrf = typeof document !== "undefined" ? getCookie("csrf") : ""
    if (csrf && !headers.has("X-CSRF-Token")) {
        headers.set("X-CSRF-Token", csrf)
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
