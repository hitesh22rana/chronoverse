"use client"

import { useRouter } from "next/navigation"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { createIdempotencyKey, fetchApi } from "@/lib/api/client"
import { apiEndpoints } from "@/lib/api/endpoints"

type LoginCredentials = {
    email: string
    password: string
}

type SignupCredentials = {
    email: string
    password: string
}

type SignupCommand = SignupCredentials & { idempotencyKey: string }

export function useAuth() {
    const router = useRouter()
    const queryClient = useQueryClient()

    // Login mutation
    const loginMutation = useMutation({
        mutationFn: async (credentials: LoginCredentials) => {
            await fetchApi(apiEndpoints.auth.login, "failed to login", {
                method: "POST",
				body: JSON.stringify({ email: credentials.email, password: credentials.password }),
            })
        },
        onSuccess: () => {
            queryClient.clear()
            router.push("/")
        },
        onError: (error: Error) => {
            toast.error(error.message)
        },
    })

    // Signup mutation
    const signupMutation = useMutation({
        mutationFn: async (command: SignupCommand) => {
            await fetchApi(apiEndpoints.auth.signup, "failed to signup", {
                method: "POST",
				headers: { "Idempotency-Key": command.idempotencyKey },
                body: JSON.stringify({ email: command.email, password: command.password }),
            })
        },
        onSuccess: () => {
            queryClient.clear()
            router.push("/")
        },
        onError: (error: Error) => {
            toast.error(error.message)
        },
    })

    // Logout mutation
    const logoutMutation = useMutation({
        mutationFn: async () => {
            await fetchApi(apiEndpoints.auth.logout, "failed to logout", {
                method: "POST"
            })
        },
        onSuccess: () => {
            queryClient.clear()
            router.replace("/login")
            router.refresh()
        },
        onError: (error: Error) => {
            toast.error(error.message)
            router.replace("/login")
            router.refresh()
        },
    })

    return {
        login: loginMutation.mutate,
        isLoginLoading: loginMutation.isPending,
		signup: (credentials: SignupCredentials) => signupMutation.mutate({
			...credentials,
			idempotencyKey: createIdempotencyKey(),
		}),
        isSignupLoading: signupMutation.isPending,
        logout: logoutMutation.mutate,
        isLogoutLoading: logoutMutation.isPending,
    }
}
