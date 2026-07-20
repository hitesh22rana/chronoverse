const decimalFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 })
const integerFormatter = new Intl.NumberFormat()

export function divide(numerator = 0, denominator = 0) {
    return denominator > 0 ? numerator / denominator : 0
}

export function formatDecimal(value: number) {
    return decimalFormatter.format(value)
}

export function formatInteger(value: number) {
    return integerFormatter.format(value)
}

export function formatRetainedLogsPerJob(totalLogs: number, totalJobs: number) {
    if (!Number.isFinite(totalLogs) || !Number.isFinite(totalJobs) || totalLogs <= 0 || totalJobs <= 0) {
        return null
    }

    const logsPerJob = totalLogs / totalJobs
    if (logsPerJob < 1) {
        return "<1 log per job"
    }

    const roundedLogs = Math.round(logsPerJob)
    const unit = roundedLogs === 1 ? "log" : "logs"
    return `~${formatInteger(roundedLogs)} ${unit} per job`
}

export function formatWorkflowKind(kind: string) {
    return kind
        .toLocaleLowerCase()
        .split("_")
        .map((word) => word.charAt(0).toLocaleUpperCase() + word.slice(1))
        .join(" ")
}

export function truncateWorkflowLabel(value: string) {
    return value.length > 18 ? `${value.slice(0, 17)}…` : value
}
