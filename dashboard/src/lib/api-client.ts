// Utility for making authenticated API requests that include the necessary headers and credentials
export async function fetchWithAuth(url: string, options: RequestInit = {}) {
    // Ensure credentials are included to send cookies
    const defaultOptions: RequestInit = {
        headers: {
            "Content-Type": "application/json",
            ...options.headers,
        },
        credentials: "include",
        ...options,
    }

    return fetch(url, defaultOptions)
}
