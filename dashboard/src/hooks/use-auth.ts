"use client"

import { useRouter } from "next/navigation"
import { useMutation } from "@tanstack/react-query"
import { toast } from "sonner"

import { fetchWithAuth } from "@/utils/api-client"

const API_URL = process.env.NEXT_PUBLIC_API_URL
const LOGIN_ENDPOINT = `${API_URL}/auth/login`
const SIGNUP_ENDPOINT = `${API_URL}/auth/register`
const LOGOUT_ENDPOINT = `${API_URL}/auth/logout`

type LoginCredentials = {
    email: string
    password: string
}

type SignupCredentials = {
    email: string
    password: string
}

// Unified authentication hook that handles login, signup, and logout operations
export function useAuth() {
    const router = useRouter()

    // Login mutation
    const loginMutation = useMutation({
        mutationFn: async (credentials: LoginCredentials) => {
            const response = await fetch(LOGIN_ENDPOINT, {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                },
                credentials: "include",
                body: JSON.stringify(credentials),
            })

            if (!response.ok) {
                throw new Error("failed to login")
            }
        },
        onSuccess: () => {
            router.push("/")
        },
        onError: (error: Error) => {
            toast.error(error.message)
        },
    })

    // Signup mutation
    const signupMutation = useMutation({
        mutationFn: async (credentials: SignupCredentials) => {
            const response = await fetch(SIGNUP_ENDPOINT, {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                },
                credentials: "include",
                body: JSON.stringify(credentials),
            })

            if (!response.ok) {
                throw new Error("failed to signup")
            }
        },
        onSuccess: () => {
            router.push("/")
        },
        onError: (error: Error) => {
            toast.error(error.message)
        },
    })

    // Logout mutation
    const logoutMutation = useMutation({
        mutationFn: async () => {
            const response = await fetchWithAuth(LOGOUT_ENDPOINT, {
                method: "POST"
            })

            if (!response.ok) {
                throw new Error("failed to logout")
            }
        },
        onSuccess: () => {
            router.refresh()
        },
        onError: (error: Error) => {
            toast.error(error.message)
        },
    })

    return {
        login: loginMutation.mutate,
        isLoginLoading: loginMutation.isPending,
        signup: signupMutation.mutate,
        isSignupLoading: signupMutation.isPending,
        logout: logoutMutation.mutate,
        isLogoutLoading: logoutMutation.isPending,
    }
}
