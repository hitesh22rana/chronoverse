"use client"

import { useFormContext } from "react-hook-form"
import { Input } from "@/components/ui/input"
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form"

export function AuthInput({ name, label, type, placeholder }: {
    name: "email" | "password" | "confirmPassword"
    label: string
    type?: "password"
    placeholder: string
}) {
    const { control } = useFormContext()
    return (
        <FormField control={control} name={name} render={({ field }) => (
            <FormItem>
                <FormLabel>{label}</FormLabel>
                <FormControl><Input type={type} placeholder={placeholder} {...field} /></FormControl>
                <FormMessage />
            </FormItem>
        )} />
    )
}
