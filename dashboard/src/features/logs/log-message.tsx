import type { ReactNode } from "react"

import { jsonRegex } from "@/lib/utils"

const highlightClass = "rounded bg-orange-400/80 px-0.5 text-inherit dark:bg-orange-500/70"
const highlightTokenPattern = /^[a-f0-9]{32}$/
const highlightStartPrefix = "__CV_HL_START_"
const highlightEndPrefix = "__CV_HL_END_"
const highlightSuffix = "__"

const renderHighlightedText = (message: string, highlightToken?: string): ReactNode => {
    if (!highlightToken || !highlightTokenPattern.test(highlightToken)) {
        return message
    }

    const startTag = `${highlightStartPrefix}${highlightToken}${highlightSuffix}`
    const endTag = `${highlightEndPrefix}${highlightToken}${highlightSuffix}`
    let currentIndex = 0
    const rendered: ReactNode[] = []

    while (currentIndex < message.length) {
        const startIndex = message.indexOf(startTag, currentIndex)
        if (startIndex === -1) break

        const matchStartIndex = startIndex + startTag.length
        const endIndex = message.indexOf(endTag, matchStartIndex)
        if (endIndex === -1) break

        rendered.push(message.slice(currentIndex, startIndex))
        rendered.push(
            <mark key={`${startIndex}:${endIndex}`} className={highlightClass}>
                {message.slice(matchStartIndex, endIndex)}
            </mark>,
        )
        currentIndex = endIndex + endTag.length
    }

    rendered.push(message.slice(currentIndex))
    return rendered
}

const getHighlightTags = (highlightToken?: string) => {
    if (!highlightToken || !highlightTokenPattern.test(highlightToken)) return null

    return {
        startTag: `${highlightStartPrefix}${highlightToken}${highlightSuffix}`,
        endTag: `${highlightEndPrefix}${highlightToken}${highlightSuffix}`,
    }
}

const parseHighlightedMessage = (message: string, highlightToken?: string) => {
    const tags = getHighlightTags(highlightToken)
    if (!tags) return { rawMessage: message, highlightedSegments: [] }

    const highlightedSegments: string[] = []
    let currentIndex = 0
    let rawMessage = ""

    while (currentIndex < message.length) {
        const startIndex = message.indexOf(tags.startTag, currentIndex)
        if (startIndex === -1) break

        const matchStartIndex = startIndex + tags.startTag.length
        const endIndex = message.indexOf(tags.endTag, matchStartIndex)
        if (endIndex === -1) break

        rawMessage += message.slice(currentIndex, startIndex)
        rawMessage += message.slice(matchStartIndex, endIndex)
        highlightedSegments.push(message.slice(matchStartIndex, endIndex))
        currentIndex = endIndex + tags.endTag.length
    }

    rawMessage += message.slice(currentIndex)
    return { rawMessage, highlightedSegments }
}

const renderHighlightedSegments = (message: string, segments: string[]): ReactNode => {
    if (!segments.length) return message

    let currentIndex = 0
    const rendered: ReactNode[] = []

    for (const segment of segments) {
        if (!segment) continue

        const segmentIndex = message.indexOf(segment, currentIndex)
        if (segmentIndex === -1) continue

        rendered.push(message.slice(currentIndex, segmentIndex))
        rendered.push(
            <mark key={`${segmentIndex}:${segment.length}`} className={highlightClass}>
                {segment}
            </mark>,
        )
        currentIndex = segmentIndex + segment.length
    }

    rendered.push(message.slice(currentIndex))
    return rendered
}

export function parseLogMessage(message: string, highlightToken: string | undefined, parseJson: boolean) {
    if (!parseJson) return renderHighlightedText(message, highlightToken)

    const { rawMessage, highlightedSegments } = parseHighlightedMessage(message, highlightToken)

    try {
        return renderHighlightedSegments(JSON.stringify(JSON.parse(rawMessage), null, 2), highlightedSegments)
    } catch {
        const formattedMessage = rawMessage.replace(jsonRegex, (match) => {
            try {
                return JSON.stringify(JSON.parse(match), null, 2)
            } catch {
                return match
            }
        })

        return renderHighlightedSegments(formattedMessage, highlightedSegments)
    }
}
