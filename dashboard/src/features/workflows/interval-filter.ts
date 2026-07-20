export const minimumWorkflowIntervalMinutes = 1
export const maximumWorkflowIntervalMinutes = 10_080

export function normalizeIntervalFilter(value?: string | null) {
    if (!value || !/^\d+$/.test(value)) {
        return ""
    }

    const interval = Number(value)
    if (
        !Number.isSafeInteger(interval) ||
        interval < minimumWorkflowIntervalMinutes ||
        interval > maximumWorkflowIntervalMinutes
    ) {
        return ""
    }

    return String(interval)
}

export function isIntervalRangeInvalid(intervalMin: string, intervalMax: string) {
    return Boolean(intervalMin && intervalMax && Number(intervalMax) < Number(intervalMin))
}

export function isDisallowedIntervalKey(key: string) {
    return ["-", "+", ".", "e", "E"].includes(key)
}
