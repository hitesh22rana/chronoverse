export type Notification = {
    id: string
    kind: string
    payload: string
    read_at?: string
    created_at: string
    updated_at: string
}

export type NotificationPayload = {
    title: string
    message: string
    entity_id: string
    entity_type: string
    action_url: string
}

export type NotificationsResponse = {
    notifications: Notification[]
    cursor?: string
}
