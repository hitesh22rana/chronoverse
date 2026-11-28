const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function isWorkflowRouteId(id: string) {
    return uuid.test(id)
}

export function workflowIdFromPath(path: string) {
    const segment = /^\/workflows\/([^/]+)(?:\/jobs\/[^/]+)?\/?$/.exec(path)?.[1]
    if (!segment) return
    try {
        const id = decodeURIComponent(segment)
        if (isWorkflowRouteId(id)) return id.toLowerCase()
    } catch {
        // Malformed encodings do not identify a workflow route.
    }
}
