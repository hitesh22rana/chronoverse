"use client"

import * as React from "react"
import {
  Label,
  Pie,
  PieChart as RechartsPieChart,
  Sector,
  Tooltip as RechartsTooltip,
} from "recharts"
import {
  Cell,
  PieProps as RechartsPieProps,
  PieSectorDataItem,
  TooltipProps as RechartsTooltipProps,
} from "recharts"

import { cn } from "@/lib/utils"

// Helper to type chart config
type ChartConfig = {
  [k in string]: {
    label?: React.ReactNode
    icon?: React.ComponentType
  } & (
    | { color?: string; theme?: never }
    | { color?: never; theme: Record<string, string> }
  )
}

type ChartContextProps = {
  config: ChartConfig
}

const ChartContext = React.createContext<ChartContextProps | null>(null)

function useChart() {
  const context = React.useContext(ChartContext)

  if (!context) {
    throw new Error("useChart must be used within a <ChartContainer />")
  }

  return context
}

const ChartContainer = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement> & {
    config: ChartConfig
    children: React.ReactNode
  }
>(({ id, className, children, config, ...props }, ref) => {
  const uniqueId = React.useId()
  const chartId = `chart-${id || uniqueId.replace(/:/g, "")}`

  return (
    <ChartContext.Provider value={{ config }}>
      <div
        data-chart={chartId}
        ref={ref}
        className={cn(
          "flex aspect-video justify-center text-xs [&_.recharts-cartesian-axis-tick_text]:fill-muted-foreground [&_.recharts-cartesian-grid_line[stroke-dasharray]]:stroke-border/50 [&_.recharts-curve.recharts-tooltip-cursor]:stroke-border [&_.recharts-dot[stroke='#fff']]:stroke-transparent [&_.recharts-layer:focus-visible]:outline-none [&_.recharts-polar-axis-tick_text]:fill-muted-foreground [&_.recharts-radial-bar-background-sector]:fill-muted [&_.recharts-rectangle.recharts-tooltip-cursor]:fill-muted [&_.recharts-reference-line_line]:stroke-border [&_.recharts-sector[path_]:focus-visible]:outline-none [&_.recharts-sector:focus-visible]:outline-none [&_.recharts-surface]:outline-none",
          className
        )}
        {...props}
      >
        {children}
      </div>
    </ChartContext.Provider>
  )
})
ChartContainer.displayName = "Chart"

const ChartTooltip = RechartsTooltip

const ChartTooltipContent = React.forwardRef<
  HTMLDivElement,
  React.ComponentProps<"div"> &
    Pick<RechartsTooltipProps<any, any>, "label" | "payload"> & {
      indicator?: "dot" | "line" | "dashed"
      hideLabel?: boolean
      hideIndicator?: boolean
      labelFormatter?: (label: any, payload: any) => React.ReactNode
      nameKey?: string
      labelKey?: string
    }
>(
  (
    {
      className,
      label,
      payload,
      indicator = "dot",
      hideLabel = false,
      hideIndicator = false,
      labelFormatter,
      nameKey = "name",
      labelKey,
    },
    ref
  ) => {
    const { config } = useChart()

    const formattedLabel = React.useMemo(() => {
      if (hideLabel || !payload?.length) {
        return null
      }

      if (labelFormatter) {
        return labelFormatter(label, payload)
      }

      if (labelKey && payload[0].payload[labelKey]) {
        return payload[0].payload[labelKey] as React.ReactNode
      }

      return label
    }, [label, payload, hideLabel, labelFormatter, labelKey])

    if (!payload?.length) {
      return null
    }

    return (
      <div
        ref={ref}
        className={cn(
          "grid min-w-[8rem] items-start gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl",
          className
        )}
      >
        {!hideLabel && formattedLabel ? (
          <div className="font-medium">{formattedLabel}</div>
        ) : null}
        <div className="grid gap-1.5">
          {payload.map((item, i) => {
            const key = `${item.name}-${item.color}`
            const itemConfig = config[item.name as keyof typeof config]
            const
              indicatorColor = itemConfig?.color || item.color

            return (
              <div
                key={key}
                className="flex w-full items-center justify-between"
              >
                <div className="flex items-center gap-1.5">
                  {!hideIndicator && (
                    <span
                      className="h-2.5 w-2.5 shrink-0 rounded-[2px]"
                      style={{
                        backgroundColor: indicatorColor,
                      }}
                    />
                  )}
                  {itemConfig?.label || item.name}
                </div>
                <div className="font-medium text-right">{item.value}</div>
              </div>
            )
          })}
        </div>
      </div>
    )
  }
)
ChartTooltipContent.displayName = "ChartTooltip"

// Pie Chart
const PieChart = RechartsPieChart

const Pie = React.forwardRef<
  React.ElementRef<typeof RechartsPieChart>,
  React.ComponentProps<typeof RechartsPieChart>
>(({ className, ...props }, ref) => {
  const { config } = useChart()

  return (
    <RechartsPieChart
      ref={ref}
      className={cn(
        "[&_.recharts-pie-label_text]:fill-primary-foreground",
        className
      )}
      {...props}
    />
  )
})
Pie.displayName = "Pie"

const ActiveShape = (props: PieSectorDataItem) => {
  const { cx, cy, innerRadius, outerRadius, startAngle, endAngle, fill } =
    props
  return (
    <g>
      <Sector
        cx={cx}
        cy={cy}
        innerRadius={innerRadius}
        outerRadius={outerRadius! + 4}
        startAngle={startAngle}
        endAngle={endAngle}
        fill={fill}
      />
    </g>
  )
}

export {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  PieChart,
  Pie,
  Cell,
  Label,
  ActiveShape,
}
