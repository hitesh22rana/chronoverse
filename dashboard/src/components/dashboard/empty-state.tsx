"use client"

import { useEffect, useState } from "react"
import { motion } from "framer-motion"
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
    const [mounted, setMounted] = useState(false)

    useEffect(() => {
        setMounted(true)
        return () => setMounted(false)
    }, [])

    return (
        <div
            className={cn(
                "flex flex-col items-center justify-center flex-1 h-full w-full",
                "rounded-lg border border-dashed",
                "bg-gradient-to-b from-muted/10 to-muted/20",
                className,
            )}
        >
            {mounted ? (
                <motion.div
                    className="flex flex-col items-center text-center max-w-md mx-auto p-8"
                    initial={{ opacity: 0, y: 20 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.5 }}
                >
                    <motion.div
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
                        <motion.div
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
                    </motion.div>

                    <motion.h2
                        className="text-2xl font-semibold tracking-tight mb-3"
                        initial={{ opacity: 0 }}
                        animate={{ opacity: 1 }}
                        transition={{ delay: 0.2, duration: 0.5 }}
                    >
                        {title}
                    </motion.h2>

                    <motion.p
                        className="text-muted-foreground leading-relaxed"
                        initial={{ opacity: 0 }}
                        animate={{ opacity: 1 }}
                        transition={{ delay: 0.3, duration: 0.5 }}
                    >
                        {description}
                    </motion.p>

                    <motion.div
                        className="absolute opacity-20"
                        style={{
                            borderRadius: "30% 70% 70% 30% / 30% 30% 70% 70%",
                            filter: "blur(60px)",
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
                </motion.div>
            ) : (
                <div className="flex flex-col items-center text-center max-w-md mx-auto p-8">
                    <div className="flex h-16 w-16 items-center justify-center rounded-full bg-primary/10 mb-6">
                        <Sparkles className="h-8 w-8 text-primary" />
                    </div>
                    <h2 className="text-2xl font-semibold tracking-tight mb-3">
                        {title}
                    </h2>
                    <p className="text-muted-foreground leading-relaxed">
                        {description}
                    </p>
                </div>
            )}
        </div>
    )
}