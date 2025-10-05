"use server"

import { cookies } from "next/headers"

import { hashStringSHA256 } from "@/lib/utils"

export async function logout() {
    const cookieStore = await cookies()

    // Remove the session cookie
    cookieStore.delete("session")

    // Remove the CSRF cookie
    cookieStore.delete("csrf")
}

export async function getSessionHash() {
    const cookieStore = await cookies()

    return await hashStringSHA256(cookieStore.get("session")?.value)
}
