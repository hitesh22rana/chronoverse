"use client";

import { Bar, BarChart, CartesianGrid, XAxis } from "recharts";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { useUserAnalytics } from "@/hooks/use-user-analytics";

export function UserAnalytics() {
  const { data, isLoading, error } = useUserAnalytics();

  if (isLoading) {
    return <div>Loading...</div>;
  }

  if (error) {
    return <div>Error: {error.message}</div>;
  }

  const chartData = [
    {
      name: "Workflows",
      total: data?.totalWorkflows,
    },
    {
      name: "Jobs",
      total: data?.totalJobs,
    },
    {
      name: "Job Logs",
      total: data?.totalJoblogs,
    },
  ];

  return (
    <Card>
      <CardHeader>
        <CardTitle>User Analytics</CardTitle>
        <CardDescription>
          A breakdown of your activity on the platform.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ChartContainer
          config={{
            total: {
              label: "Total",
              color: "hsl(var(--chart-1))",
            },
          }}
        >
          <BarChart data={chartData}>
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="name"
              tickLine={false}
              tickMargin={10}
              axisLine={false}
            />
            <ChartTooltip
              cursor={false}
              content={<ChartTooltipContent indicator="dot" />}
            />
            <Bar dataKey="total" fill="var(--color-total)" radius={4} />
          </BarChart>
        </ChartContainer>
      </CardContent>
    </Card>
  );
}
