import type { LucideIcon } from "lucide-react"
import {
    AlertTriangle,
    CheckCircle,
    CircleDashed,
    Clock,
    RefreshCw,
    XCircle,
} from "lucide-react"

export type StatusKey =
    | "PENDING"
    | "QUEUED"
    | "RUNNING"
    | "COMPLETED"
    | "FAILED"
    | "CANCELED"
    | "TERMINATED"
    | "UNKNOWN"

export type StatusContext = "job" | "workflow"

export type StatusMeta = {
    key: StatusKey
    icon: LucideIcon
    labels: {
        default: string
        workflow?: string
        job?: string
    }
    // Tailwind classes for text & background of a badge/pill
    badgeClass: string
    // Optional fancy shadow/border class used by some cards
    glowClass?: string
    // Optional icon animation class (e.g. spinning)
    iconClass?: string
    // Optional dot color used by some cards
    dotColor?: string
}

const COLORS = {
    gray: {
        badge: "text-gray-600 bg-gray-50 dark:bg-gray-950/30",
        glow: "shadow-none",
        dot: "#6b7280",
    },
    amber: {
        badge: "text-amber-600 bg-amber-50 dark:bg-amber-950/30",
        glow:
            "shadow-[0_0_15px_rgba(245,158,11,0.15)] dark:shadow-[0_0_20px_rgba(245,158,11,0.25)] border-amber-200/50 dark:border-amber-800/30",
        dot: "#f59e0b",
    },
    blue: {
        badge: "text-blue-600 bg-blue-50 dark:bg-blue-950/30",
        glow:
            "shadow-[0_0_15px_rgba(59,130,246,0.15)] dark:shadow-[0_0_20px_rgba(59,130,246,0.25)] border-blue-200/50 dark:border-blue-800/30",
        dot: "#3b82f6",
    },
    emerald: {
        badge: "text-emerald-600 bg-emerald-50 dark:bg-emerald-950/30",
        glow:
            "shadow-[0_0_15px_rgba(16,185,129,0.15)] dark:shadow-[0_0_20px_rgba(16,185,129,0.25)] border-emerald-200/50 dark:border-emerald-800/30",
        dot: "#10b981",
    },
    red: {
        badge: "text-red-600 bg-red-50 dark:bg-red-950/30",
        glow:
            "shadow-[0_0_15px_rgba(239,68,68,0.15)] dark:shadow-[0_0_20px_rgba(239,68,68,0.25)] border-red-200/50 dark:border-red-800/30",
        dot: "#ef4444",
    },
    orange: {
        badge: "text-orange-600 bg-orange-50 dark:bg-orange-950/30",
        glow:
            "shadow-[0_0_15px_rgba(249,115,22,0.15)] dark:shadow-[0_0_20px_rgba(249,115,22,0.25)] border-orange-200/50 dark:border-orange-800/30",
        dot: "#f97316",
    },
} as const

const STATUS_META: Record<StatusKey, StatusMeta> = {
    PENDING: {
        key: "PENDING",
        icon: CircleDashed,
        labels: { default: "Pending" },
        badgeClass: COLORS.gray.badge,
        glowClass: COLORS.gray.glow,
        dotColor: COLORS.gray.dot,
    },
    QUEUED: {
        key: "QUEUED",
        icon: Clock,
        labels: { default: "Queued" },
        badgeClass: COLORS.amber.badge,
        glowClass: COLORS.amber.glow,
        dotColor: COLORS.amber.dot,
    },
    RUNNING: {
        key: "RUNNING",
        icon: RefreshCw,
        labels: { default: "Running", workflow: "Building" },
        badgeClass: COLORS.blue.badge,
        glowClass: COLORS.blue.glow,
        iconClass: "animate-spin",
        dotColor: COLORS.blue.dot,
    },
    COMPLETED: {
        key: "COMPLETED",
        icon: CheckCircle,
        labels: { default: "Completed", workflow: "Active" },
        badgeClass: COLORS.emerald.badge,
        glowClass: COLORS.emerald.glow,
        dotColor: COLORS.emerald.dot,
    },
    FAILED: {
        key: "FAILED",
        icon: AlertTriangle,
        labels: { default: "Failed" },
        badgeClass: COLORS.red.badge,
        glowClass: COLORS.red.glow,
        dotColor: COLORS.red.dot,
    },
    CANCELED: {
        key: "CANCELED",
        icon: XCircle,
        labels: { default: "Canceled" },
        badgeClass: COLORS.orange.badge,
        glowClass: COLORS.orange.glow,
        dotColor: COLORS.orange.dot,
    },
    TERMINATED: {
        key: "TERMINATED",
        icon: XCircle,
        labels: { default: "Terminated" },
        badgeClass: COLORS.red.badge,
        glowClass: COLORS.red.glow,
        dotColor: COLORS.red.dot,
    },
    UNKNOWN: {
        key: "UNKNOWN",
        icon: CircleDashed,
        labels: { default: "Unknown" },
        badgeClass: COLORS.gray.badge,
        glowClass: COLORS.gray.glow,
        dotColor: COLORS.gray.dot,
    },
}

// Normalize incoming backend strings to canonical keys
const NORMALIZE: Record<string, StatusKey> = {
    PENDING: "PENDING",
    QUEUED: "QUEUED",
    RUNNING: "RUNNING",
    STARTED: "RUNNING",
    BUILDING: "RUNNING",
    COMPLETED: "COMPLETED",
    FAILED: "FAILED",
    CANCELED: "CANCELED",
    CANCELLED: "CANCELED",
    TERMINATED: "TERMINATED",
    ACTIVE: "COMPLETED",
}

export function normalizeStatus(input?: string | null): StatusKey {
    if (!input) return "UNKNOWN"
    const key = input.toUpperCase().trim()
    return NORMALIZE[key] ?? "UNKNOWN"
}

export function getStatusMeta(input?: string | null): StatusMeta {
    const key = normalizeStatus(input)
    return STATUS_META[key] ?? STATUS_META.UNKNOWN
}

export function getStatusLabel(input?: string | null, context?: StatusContext): string {
    const meta = getStatusMeta(input)
    if (context && meta.labels[context]) return meta.labels[context] as string
    return meta.labels.default
}

export function isTerminal(input?: string | null): boolean {
    const k = normalizeStatus(input)
    return k === "COMPLETED" ||
        k === "FAILED" ||
        k === "CANCELED" ||
        k === "TERMINATED"
}
