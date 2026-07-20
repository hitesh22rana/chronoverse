export type JobLog = {
    event_id?: string
    highlightToken?: string
    timestamp: string
    message: string
    sequence_num: number
    stream: "stdout" | "stderr"
}

export type JobLogsResponse = {
    id: string
    workflow_id: string
    logs: JobLog[]
    cursor?: string
    highlight_token?: string
}

export type DownloadLogsFormat = "txt" | "json" | "jsonl"
