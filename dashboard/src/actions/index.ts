"use server"

import { cookies } from "next/headers"

// This is a server action
export async function logout() {
    const cookieStore = await cookies()

    // Remove the session cookie
    cookieStore.delete("session")

    // Remove the CSRF cookie
    cookieStore.delete("csrf")
}