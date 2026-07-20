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
