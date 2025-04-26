"use client"

import {
    useEffect,
    useMemo,
    useRef,
    useState,
} from "react";
import Link from "next/link";
import {
    formatDistanceToNow,
    isToday,
    isYesterday,
    format,
} from "date-fns";
import {
    Bell,
    Check,
    XCircle,
    AlertTriangle,
    LayoutList,
    SquareArrowOutUpRight,
} from "lucide-react";
import {
    VariableSizeList as List,
    ListChildComponentProps,
} from "react-window";
import AutoSizer from "react-virtualized-auto-sizer";

import {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
} from "@/components/ui/sheet";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";

import {
    useNotifications,
    type Notification,
    type NotificationPayload,
} from "@/hooks/use-notifications";

import { cn } from "@/lib/utils";

type Severity = "ERROR" | "ALERT" | "INFO" | "SUCCESS";

const KIND_TO_SEVERITY: Record<string, Severity> = {
    WEB_ERROR: "ERROR",
    WEB_ALERT: "ALERT",
    WEB_WARN: "ALERT",
    WEB_INFO: "INFO",
    WEB_SUCCESS: "SUCCESS",
};

const SEVERITY_META: Record<Severity, { color: string }> = {
    ERROR: {
        color:
            "border-l-4 border-red-500 bg-red-500/5 dark:bg-red-900/20 text-red-100",
    },
    ALERT: {
        color:
            "border-l-4 border-orange-500 bg-orange-500/5 dark:bg-orange-900/20 text-orange-100",
    },
    INFO: {
        color:
            "border-l-4 border-blue-500 bg-blue-500/5 dark:bg-blue-900/20 text-blue-100",
    },
    SUCCESS: {
        color:
            "border-l-4 border-green-500 bg-green-500/5 dark:bg-green-900/20 text-green-100",
    },
};

function dateHeading(iso: string) {
    const d = new Date(iso);
    if (isToday(d)) return "Today";
    if (isYesterday(d)) return "Yesterday";
    return format(d, "MMM d, yyyy");
}

interface NotificationsDrawerProps {
    open: boolean;
    onClose: () => void;
}

export function NotificationsDrawer({ open, onClose }: NotificationsDrawerProps) {
    const {
        notifications,
        isLoading,
        markAsRead,
        fetchNextPage,
        isFetchingNextPage,
        hasNextPage,
    } = useNotifications();

    const listRef = useRef<List>(null);
    const [tab, setTab] = useState<"all" | "alerts" | "errors">("all");
    const filtered = useMemo(() => {
        if (tab === "all") return notifications;
        if (tab === "alerts")
            return notifications.filter((n) => {
                const sev = KIND_TO_SEVERITY[n.kind];
                return sev === "ALERT";
            });
        return notifications.filter((n) => KIND_TO_SEVERITY[n.kind] === "ERROR");
    }, [notifications, tab]);

    const flat = useMemo(() => {
        const result: { type: "heading" | "item"; heading?: string; n?: Notification }[] = [];
        const groups = new Map<string, Notification[]>();
        filtered.forEach((n) => {
            const key = dateHeading(n.created_at);
            const arr = groups.get(key) ?? [];
            arr.push(n);
            groups.set(key, arr);
        });
        groups.forEach((arr, heading) => {
            result.push({ type: "heading", heading });
            arr.forEach((n) => result.push({ type: "item", n }));
        });
        return result;
    }, [filtered]);

    useEffect(() => {
        // reset from index 0 so every row is re-measured
        listRef.current?.resetAfterIndex(0, true);
    }, [flat]);

    const itemKey = (index: number) =>
        flat[index].type === "heading"
            ? `heading-${flat[index].heading}`
            : (flat[index].n as Notification).id;

    // bulk selection
    const [selected, setSelected] = useState<Set<string>>(new Set());
    const toggleSelect = (id: string) => {
        setSelected((prev) => {
            const next = new Set(prev);
            // eslint-disable-next-line @typescript-eslint/no-unused-expressions
            next.has(id) ? next.delete(id) : next.add(id);
            return next;
        });
    };
    const clearSel = () => setSelected(new Set());
    const bulkRead = () => {
        if (selected.size === 0) return;
        markAsRead(Array.from(selected));
        clearSel();
    };

    const Row = ({ index, style }: ListChildComponentProps) => {
        const d = flat[index];

        if (index === flat.length - 1 && hasNextPage && !isFetchingNextPage) fetchNextPage();

        if (d.type === "heading") {
            return (
                <div
                    style={style}
                    key={index}
                    className="bg-background/90 text-sm backdrop-blur font-semibold border-b px-4 py-2"
                >
                    {d.heading}
                </div>
            )
        }

        const n = d.n!;
        const payload: NotificationPayload = JSON.parse(n.payload);
        const sev = KIND_TO_SEVERITY[n.kind] ?? "INFO";
        const meta = SEVERITY_META[sev];

        return (
            <div
                style={style}
                className={cn(
                    "relative px-2 py-4 flex gap-3 group hover:bg-muted/30 transition-colors",
                    meta.color,
                    !n.read_at && "ring-1 ring-white/5"
                )}
                onClick={() => {
                    window.open(payload.action_url, "_blank");
                    if (!n.read_at) markAsRead([n.id]);
                }}
                key={index}
            >
                <div
                    onClick={(e) => {
                        e.stopPropagation();
                        toggleSelect(n.id);
                    }}
                >
                    <Checkbox checked={selected.has(n.id)} />
                </div>
                <div className="flex-1 min-w-0 flex flex-col gap-2">
                    <div className="flex items-center justify-between gap-2">
                        <p className="font-medium text-sm truncate">{payload.title}</p>
                        <span className="text-xs text-muted-foreground shrink-0">
                            {formatDistanceToNow(new Date(n.created_at), { addSuffix: true })}
                        </span>
                    </div>
                    <p className="text-xs font-medium text-muted-foreground line-clamp-2">
                        {payload.message}
                    </p>

                    <Link href={payload.action_url} className="absolute top-1 right-1 opacity-0 group-hover:opacity-100 transition-opacity duration-200">
                        <SquareArrowOutUpRight className="h-4 w-4" />
                    </Link>
                </div>
            </div>
        );
    };

    return (
        <Sheet open={open} onOpenChange={onClose}>
            <SheetContent className="w-full sm:max-w-md p-0 gap-0 h-full flex flex-col">
                {/* header */}
                <SheetHeader className="px-6 py-4 border-b flex-shrink-0">
                    <div className="flex items-center gap-2">
                        <Bell className="h-5 w-5" />
                        <SheetTitle>Notifications</SheetTitle>
                    </div>
                </SheetHeader>

                {/* tabs */}
                <Tabs value={tab} onValueChange={(v) => setTab(v as never)} className="p-4 w-full">
                    <TabsList className="flex flex-row w-full">
                        <TabsTrigger value="all" className="gap-1 cursor-pointer">
                            <LayoutList className="h-4 w-4" /> All
                        </TabsTrigger>
                        <TabsTrigger value="alerts" className="gap-1 cursor-pointer">
                            <AlertTriangle className="h-4 w-4" /> Alerts
                        </TabsTrigger>
                        <TabsTrigger value="errors" className="gap-1 cursor-pointer">
                            <XCircle className="h-4 w-4" /> Errors
                        </TabsTrigger>
                    </TabsList>
                </Tabs>

                {/* bulk bar */}
                <div className="px-4 py-2 border-b flex items-center justify-between gap-3 bg-background/95">
                    <p className="text-sm font-medium">{selected.size || 0} selected</p>
                    <Button size="sm" variant="ghost" className="gap-1 cursor-pointer" onClick={bulkRead} disabled={!selected.size}>
                        <Check className="h-4 w-4" /> Mark read
                    </Button>
                </div>

                {/* list / loading / empty */}
                <div className="flex-1">
                    {isLoading && filtered.length === 0 ? (
                        [...Array(10)].map((_, i) => (
                            <div key={i} className="px-6 py-4 space-y-3">
                                <Skeleton className="h-4 w-3/4" />
                                <Skeleton className="h-3 w-full" />
                                <Skeleton className="h-3 w-1/2" />
                            </div>
                        ))
                    ) : filtered.length === 0 ? (
                        <div className="flex flex-col items-center justify-center p-8 text-center gap-2 text-muted-foreground">
                            <Bell className="h-8 w-8" />
                            <p className="font-medium">You&apos;re all caught up!</p>
                            <p className="text-sm">We&apos;ll notify you when there&apos;s new activity.</p>
                        </div>
                    ) : (
                        <AutoSizer disableWidth>
                            {({ height }) => (
                                <List
                                    ref={listRef}
                                    height={height}
                                    width="100%"
                                    itemCount={flat.length}
                                    itemKey={itemKey}
                                    itemSize={(idx) =>
                                        flat[idx].type === "heading" ? 36 : 90
                                    }
                                    overscanCount={5}
                                >
                                    {Row}
                                </List>
                            )}
                        </AutoSizer>
                    )}
                </div>
            </SheetContent>
        </Sheet>
    );
}
