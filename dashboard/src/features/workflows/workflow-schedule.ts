export function formatWorkflowInterval(interval: number) {
    if (interval === 1440) return "daily"

    const isHourly = interval % 60 === 0 && interval >= 60
    const amount = isHourly ? interval / 60 : interval
    const unit = isHourly ? "hour" : "minute"

    return `every ${amount} ${unit}${amount !== 1 ? "s" : ""}`
}
