// Utility for making authenticated API requests that include the necessary headers and credentials
export async function fetchWithAuth(url: string, options: RequestInit = {}) {
    const headers = new Headers(options.headers)
    if (!headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json")
    }

    // Ensure credentials are included to send cookies
    const fetchOptions: RequestInit = {
        ...options,
        credentials: "include",
        headers,
    }

    return fetch(url, fetchOptions)
}

export function createIdempotencyKey() {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
        return crypto.randomUUID()
    }

    return `${Date.now()}-${Math.random().toString(36).slice(2)}`
}
