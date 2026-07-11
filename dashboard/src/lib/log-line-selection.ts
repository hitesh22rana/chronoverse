export type LogLineSelection = {
    start: number
    end: number
}

const logLineFragmentPattern = /^#L([1-9]\d*)(?:-L([1-9]\d*))?$/

const isValidLineNumber = (value: number) => {
    return Number.isSafeInteger(value) && value > 0
}

export const normalizeLogLineSelection = (
    start: number,
    end: number = start
): LogLineSelection | null => {
    if (!isValidLineNumber(start) || !isValidLineNumber(end)) {
        return null
    }

    return {
        start: Math.min(start, end),
        end: Math.max(start, end),
    }
}

export const parseLogLineSelection = (fragment: string): LogLineSelection | null => {
    const match = logLineFragmentPattern.exec(fragment)
    if (!match) {
        return null
    }

    const start = Number(match[1])
    const end = match[2] ? Number(match[2]) : start
    return normalizeLogLineSelection(start, end)
}

export const formatLogLineSelection = (selection: LogLineSelection): string => {
    const normalized = normalizeLogLineSelection(selection.start, selection.end)
    if (!normalized) {
        return ""
    }

    if (normalized.start === normalized.end) {
        return `#L${normalized.start}`
    }

    return `#L${normalized.start}-L${normalized.end}`
}

export const isLogLineSelected = (lineNumber: number, selection: LogLineSelection | null) => {
    return Boolean(
        selection &&
        lineNumber >= selection.start &&
        lineNumber <= selection.end
    )
}

export const shouldFetchMoreLogsForSelection = (
    selection: LogLineSelection | null,
    loadedLogCount: number,
    hasNextPage: boolean
) => {
    return Boolean(selection && hasNextPage && loadedLogCount < selection.end)
}

export const getSelectedLogText = (
    messages: readonly string[],
    selection: LogLineSelection
) => {
    return messages.slice(selection.start - 1, selection.end).join("\n")
}

export const buildLogViewerUrl = (
    pathname: string,
    query: string,
    fragment: string
) => {
    return `${pathname}${query ? `?${query}` : ""}${fragment}`
}

export const getUnavailableSelectionMessage = (selection: LogLineSelection) => {
    if (selection.start === selection.end) {
        return `Log line ${selection.end} is unavailable`
    }

    return "Some selected log lines are unavailable"
}

export const getNextLogLineSelection = (
    lineNumber: number,
    anchor: number | null,
    extendSelection: boolean
) => {
    if (!extendSelection || anchor === null) {
        return normalizeLogLineSelection(lineNumber)
    }

    return normalizeLogLineSelection(anchor, lineNumber)
}

export const shouldIgnoreLogRowSelection = (
    hasTextSelection: boolean,
    isLineOptionsInteraction: boolean
) => {
    return hasTextSelection || isLineOptionsInteraction
}
