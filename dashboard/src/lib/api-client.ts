// Utility for making authenticated API requests that include the necessary headers and credentials
export async function fetchWithAuth(url: string, options: RequestInit = {}) {
    // Ensure credentials are included to send cookies
    const fetchOptions: RequestInit = {
        ...options,
        credentials: "include",
        headers: {
            ...options.headers,
            "Content-Type": "application/json",
        },
    }

    return fetch(url, fetchOptions)
}