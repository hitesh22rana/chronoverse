export type HeaderFormValue = {
    id?: string
    key: string
    value: string
}

export type WorkflowConfigurationValues = {
    heartbeatPayload?: {
        endpoint: string
        expectedStatusCode: number
        headers: HeaderFormValue[]
        timeout: string
    }
    containerPayload?: {
        image: string
        cmd: string[]
        cmdIds?: string[]
        env: string[]
        envIds?: string[]
        timeout: string
    }
}

export function serializeWorkflowPayload(kind: string, data: WorkflowConfigurationValues) {
    let payload: string = "{}"

    if (kind === "HEARTBEAT") {
        const { endpoint, expectedStatusCode, headers = [], timeout } = data.heartbeatPayload ?? {
            endpoint: "",
            expectedStatusCode: 200,
            headers: [],
            timeout: "",
        }
        const headersObject = headers.reduce((acc, header) => {
            if (header.key) {
                acc[header.key] = header.value
            }
            return acc
        }, {} as Record<string, string>)

        payload = JSON.stringify({
            endpoint,
            expected_status_code: expectedStatusCode,
            headers: headersObject,
            ...(timeout ? { timeout } : {})
        })
    } else if (kind === "CONTAINER") {
        const { image, cmd = [], env = [], timeout } = data.containerPayload ?? {
            image: "",
            cmd: [],
            env: [],
            timeout: "",
        }
        const envObject = env.reduce((acc, item) => {
            const [key, value] = item.split("=")
            if (key) {
                acc[key] = value || ""
            }
            return acc
        }, {} as Record<string, string>)

        payload = JSON.stringify({
            image,
            ...(cmd && cmd.length > 0 ? { cmd } : {}),
            ...(env && env.length > 0 ? { env: envObject } : {}),
            ...(timeout ? { timeout } : {})
        })
    }
    return payload
}
