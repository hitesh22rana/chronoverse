async function fetchWithCredentials(url: string, options: RequestInit = {}) {
    const headers = new Headers(options.headers)
    if (!headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json")
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
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
        return crypto.randomUUID()
    }

    return `${Date.now()}-${Math.random().toString(36).slice(2)}`
}
