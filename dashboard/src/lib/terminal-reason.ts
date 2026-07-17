export function getTerminalReason(status?: string, message?: string): string | undefined {
    if (status !== "FAILED" && status !== "CANCELED") return undefined
    return message?.trim() || "Terminal reason unavailable"
}
