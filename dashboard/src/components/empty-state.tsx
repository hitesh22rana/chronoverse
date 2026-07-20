"use client"

import { domAnimation, LazyMotion, m, MotionConfig } from "motion/react"
import { Sparkles } from "lucide-react"
import { cn } from "@/lib/utils"

export function EmptyState({
    title,
    description,
    className
}: {
    title: string
    description: string
    className?: string
}) {
    return (
        <div
            className={cn(
                "flex flex-1 flex-col items-center justify-center h-full w-full",
                "rounded-lg border border-dashed",
                "bg-linear-to-b from-muted/10 to-muted/20",
                className,
            )}
        >
            <MotionConfig reducedMotion="user">
                <LazyMotion features={domAnimation}>
                <m.div
                    className="flex flex-col items-center text-center max-w-md mx-auto p-8"
                    initial={{ opacity: 0, y: 20 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.5 }}
                >
                    <m.div
                        className="relative flex h-16 w-16 items-center justify-center rounded-full bg-primary/10 mb-6"
                        animate={{
                            boxShadow: [
                                "0 0 0 0 rgba(147, 51, 234, 0)",
                                "0 0 0 10px rgba(147, 51, 234, 0.1)",
                                "0 0 0 0 rgba(147, 51, 234, 0)"
                            ]
                        }}
                        transition={{
                            repeat: Infinity,
                            duration: 3,
                            ease: "easeInOut"
                        }}
                    >
                        <m.div
                            className="absolute inset-0 rounded-full bg-primary/5"
                            animate={{
                                scale: [1, 1.1, 1],
                            }}
                            transition={{
                                repeat: Infinity,
                                duration: 4,
                                ease: "easeInOut"
                            }}
                        />
                        <Sparkles className="h-8 w-8 text-primary" />
                    </m.div>

                    <m.h2
                        className="text-2xl font-semibold tracking-tight mb-3"
                        initial={{ opacity: 0 }}
                        animate={{ opacity: 1 }}
                        transition={{ delay: 0.2, duration: 0.5 }}
                    >
                        {title}
                    </m.h2>

                    <m.p
                        className="text-muted-foreground leading-relaxed"
                        initial={{ opacity: 0 }}
                        animate={{ opacity: 1 }}
                        transition={{ delay: 0.3, duration: 0.5 }}
                    >
                        {description}
                    </m.p>

                    <m.div
                        className="absolute opacity-20"
                        style={{
                            borderRadius: "30% 70% 70% 30% / 30% 30% 70% 70%",
                            filter: "blur(8px)",
                            zIndex: -1,
                            background: "radial-gradient(circle, rgba(147,51,234,0.2) 0%, rgba(79,70,229,0.1) 50%, transparent 70%)",
                        }}
                        animate={{
                            borderRadius: [
                                "30% 70% 70% 30% / 30% 30% 70% 70%",
                                "70% 30% 30% 70% / 70% 70% 30% 30%",
                                "30% 70% 70% 30% / 30% 30% 70% 70%"
                            ]
                        }}
                        transition={{
                            repeat: Infinity,
                            duration: 8,
                            ease: "easeInOut"
                        }}
                    />
                </m.div>
                </LazyMotion>
            </MotionConfig>
        </div>
    )
}
