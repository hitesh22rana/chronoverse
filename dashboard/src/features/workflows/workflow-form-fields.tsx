"use client"

import { useFormContext } from "react-hook-form"
import { Plus, Trash2 } from "lucide-react"
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"

const numberFields = {
    "heartbeatPayload.expectedStatusCode": {
        label: "Expected Status Code", min: 100, max: 599,
        description: "The HTTP status code expected from the endpoint (default: 200)",
    },
    interval: {
        label: "Interval (minutes)", min: 1, max: undefined,
        description: "How often to run this workflow.",
    },
    maxConsecutiveJobFailuresAllowed: {
        label: "Max consecutive failures allowed", min: 3, max: undefined,
        description: "Maximum number of consecutive failures before the workflow is auto-disabled (default: 3).",
    },
}

export function WorkflowNumberField({ name }: { name: keyof typeof numberFields }) {
    const { control } = useFormContext()
    const { label, min, max, description } = numberFields[name]
    return (
        <FormField control={control} name={name} render={({ field }) => (
            <FormItem>
                <FormLabel>{label}</FormLabel>
                <FormControl>
                    <Input type="number" min={min} max={max} {...field}
                        value={field.value === undefined ? "" : field.value}
                        onChange={(e) => field.onChange(e.target.value === "" ? "" : Number(e.target.value))}
                    />
                </FormControl>
                <FormDescription>{description}</FormDescription>
                <FormMessage />
            </FormItem>
        )} />
    )
}

const containerLists = {
    cmd: { title: "Command", label: "Command argument", add: "Add argument", placeholder: "sh -c 'echo hello'", description: "Optional command and arguments to run in the container" },
    env: { title: "Environment variables", label: "Environment variable", add: "Add variable", placeholder: "MY_ENV=VALUE", description: "Optional environment variables to set in the container" },
}

export function ContainerListField({ name, values, ids }: { name: "cmd" | "env", values: string[], ids: string[] }) {
    const form = useFormContext()
    const labels = containerLists[name]
    return (
        <div className="space-y-2">
            <div className="text-sm font-medium">
                {labels.title} (optional)
                <Button type="button" variant="outline" size="sm" className="ml-2" onClick={() => {
                    form.setValue(`containerPayload.${name}`, [...values, ""])
                    form.setValue(`containerPayload.${name}Ids`, [...ids, crypto.randomUUID()])
                }}>
                    <Plus className="mr-1 h-3 w-3" /> {labels.add}
                </Button>
            </div>
            <p className="text-muted-foreground text-sm">{labels.description}</p>
            {values.map((_, index) => (
                <div key={ids[index]} className="flex items-center gap-2 mt-2">
                    <FormField control={form.control} name={`containerPayload.${name}.${index}`} render={({ field }) => (
                        <FormItem className="flex-1">
                            <FormLabel className="sr-only">{labels.label} {index + 1}</FormLabel>
                            <FormControl><Input placeholder={labels.placeholder} {...field} value={field.value || ""} /></FormControl>
                            <FormMessage />
                        </FormItem>
                    )} />
                    <Button type="button" variant="ghost" size="sm" onClick={() => {
                        form.setValue(`containerPayload.${name}`, values.filter((_, i) => i !== index))
                        form.setValue(`containerPayload.${name}Ids`, ids.filter((_, i) => i !== index))
                    }}>
                        <Trash2 className="h-4 w-4" />
                        <span className="sr-only">Remove {labels.label.toLowerCase()} {index + 1}</span>
                    </Button>
                </div>
            ))}
        </div>
    )
}
