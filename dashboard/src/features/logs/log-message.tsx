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

    const highlightedSegments: { start: number; end: number }[] = []
    let currentIndex = 0
    let rawMessage = ""

    while (currentIndex < message.length) {
        const startIndex = message.indexOf(tags.startTag, currentIndex)
        if (startIndex === -1) break

        const matchStartIndex = startIndex + tags.startTag.length
        const endIndex = message.indexOf(tags.endTag, matchStartIndex)
        if (endIndex === -1) break

        rawMessage += message.slice(currentIndex, startIndex)
        highlightedSegments.push({ start: rawMessage.length, end: rawMessage.length + endIndex - matchStartIndex })
        rawMessage += message.slice(matchStartIndex, endIndex)
        currentIndex = endIndex + tags.endTag.length
    }

    rawMessage += message.slice(currentIndex)
    return { rawMessage, highlightedSegments }
}

type MessagePiece = { text: string; source: number | null }

const formatJson = (message: string, source: number): MessagePiece[] => {
    JSON.parse(message)
    const tokens = Array.from(message.matchAll(/"(?:[^"\\]|\\.)*"|[^\s{}\[\],:]+|[{}\[\],:]/g))
    const pieces: MessagePiece[] = []
    let depth = 0
    const newline = () => pieces.push({ text: `\n${"  ".repeat(depth)}`, source: null })

    for (const [index, token] of tokens.entries()) {
        const value = token[0]
        if ((value === "}" || value === "]") && tokens[index - 1]?.[0] !== "{" && tokens[index - 1]?.[0] !== "[") {
            depth--
            newline()
        }
        pieces.push({ text: value, source: source + token.index })
        if ((value === "{" || value === "[") && tokens[index + 1]?.[0] !== "}" && tokens[index + 1]?.[0] !== "]") {
            depth++
            newline()
        }
        if (value === ",") newline()
        if (value === ":") pieces.push({ text: " ", source: null })
    }
    return pieces
}

const renderHighlightedSegments = (pieces: MessagePiece[], segments: { start: number; end: number }[]): ReactNode => {
    const message = pieces.map((piece) => piece.text).join("")
    const rendered: ReactNode[] = []
    let currentIndex = 0
    let pieceIndex = 0
    let pieceOffset = 0

    for (const segment of segments) {
        let start = -1
        let end = -1
        let resumeIndex = -1
        let resumeOffset = 0

        // Resume at the piece that extends beyond this segment.
        while (pieceIndex < pieces.length) {
            const piece = pieces[pieceIndex]
            if (piece.source !== null) {
                if (piece.source >= segment.end) break
                const first = Math.max(segment.start, piece.source)
                const last = Math.min(segment.end, piece.source + piece.text.length)
                if (first < last) {
                    if (start === -1) start = pieceOffset + first - piece.source
                    end = pieceOffset + last - piece.source
                }
                if (piece.source + piece.text.length > segment.end) {
                    resumeIndex = pieceIndex
                    resumeOffset = pieceOffset
                }
            }
            pieceOffset += piece.text.length
            pieceIndex++
        }
        if (resumeIndex !== -1) {
            pieceIndex = resumeIndex
            pieceOffset = resumeOffset
        }

        if (start === -1) continue
        rendered.push(message.slice(currentIndex, start))
        rendered.push(<mark key={`${start}:${end}`} className={highlightClass}>{message.slice(start, end)}</mark>)
        currentIndex = end
    }
    rendered.push(message.slice(currentIndex))
    return rendered
}

export function parseLogMessage(message: string, highlightToken: string | undefined, parseJson: boolean) {
    if (!parseJson) return renderHighlightedText(message, highlightToken)

    const { rawMessage, highlightedSegments } = parseHighlightedMessage(message, highlightToken)
    let pieces: MessagePiece[]
    try {
        pieces = formatJson(rawMessage, 0)
    } catch {
        pieces = []
        let offset = 0
        for (const match of rawMessage.matchAll(jsonRegex)) {
            pieces.push({ text: rawMessage.slice(offset, match.index), source: offset })
            try {
                // Avoid spread argument limits for large JSON objects.
                for (const piece of formatJson(match[0], match.index)) pieces.push(piece)
            } catch {
                pieces.push({ text: match[0], source: match.index })
            }
            offset = match.index + match[0].length
        }
        pieces.push({ text: rawMessage.slice(offset), source: offset })
    }
    return renderHighlightedSegments(pieces, highlightedSegments)
}
