export type User = {
    email: string
    notification_preference: "ALERTS" | "ALL" | "NONE"
    created_at: string
    updated_at: string
}

export type UpdateUserDetails = {
    notification_preference: string
}
