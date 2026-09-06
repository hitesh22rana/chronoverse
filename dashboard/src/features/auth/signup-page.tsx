"use client"

import Link from "next/link"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { AuthInput } from "./auth-input"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle
} from "@/components/ui/card"
import { Form } from "@/components/ui/form"

import { useAuth } from "@/features/auth/use-auth"
import { signupSchema, type SignupValues } from "@/features/auth/auth-schemas"
import { Fragment } from "react"

export default function SignupPage() {
  const { signup, isSignupLoading: isLoading } = useAuth()

  const form = useForm<SignupValues>({
    resolver: zodResolver(signupSchema),
    defaultValues: {
      email: "",
      password: "",
      confirmPassword: ""
    },
    mode: "onChange",
    reValidateMode: "onChange"
  })

  const onSubmit = (values: SignupValues) => {
    signup({ email: values.email, password: values.password })
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-12 sm:px-6 lg:px-8">
      <Card className="w-full max-w-md">
        <CardHeader className="space-y-1 text-center">
          <CardTitle className="text-3xl font-bold">Create an account</CardTitle>
          <CardDescription>Enter your information to create your account</CardDescription>
        </CardHeader>
        <CardContent>
          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <AuthInput name="email" label="Email" placeholder="Enter your email address" />

              <AuthInput name="password" label="Password" type="password" placeholder="Create a password" />

              <AuthInput name="confirmPassword" label="Confirm Password" type="password" placeholder="Confirm your password" />

              <Button type="submit" className="w-full" disabled={isLoading}>
                {isLoading ? (
                  <Fragment>
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    Creating account...
                  </Fragment>
                ) : (
                  "Create account"
                )}
              </Button>
            </form>
          </Form>
        </CardContent>
        <CardFooter className="flex justify-center">
          <div className="text-sm text-muted-foreground">
            Already have an account?{" "}
            <Link href="/login" className="font-medium text-primary hover:underline">
              Sign in
            </Link>
          </div>
        </CardFooter>
      </Card>
    </div>
  )
}
