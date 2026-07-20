const integerFormatter = new Intl.NumberFormat()

export function divide(numerator = 0, denominator = 0) {
    return denominator > 0 ? numerator / denominator : 0
}

export function formatInteger(value = 0) {
    return integerFormatter.format(Number.isFinite(value) ? value : 0)
}

export function formatJobsPerWorkflow(totalJobs: number, totalWorkflows: number) {
    return formatApproximateRate(totalJobs, totalWorkflows, "job", "jobs", "workflow")
}

export function formatLogsPerJob(totalLogs: number, totalJobs: number) {
    return formatApproximateRate(totalLogs, totalJobs, "log", "logs", "job")
}

function formatApproximateRate(
    total: number,
    divisor: number,
    singularItem: string,
    pluralItem: string,
    unit: string,
) {
    if (!Number.isFinite(total) || !Number.isFinite(divisor) || total <= 0 || divisor <= 0) {
        return null
    }

    const rate = total / divisor
    if (rate < 1) {
        return `<1 ${singularItem} per ${unit}`
    }

    const roundedRate = Math.round(rate)
    const item = roundedRate === 1 ? singularItem : pluralItem
    return `~${formatInteger(roundedRate)} ${item} per ${unit}`
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
