import { useQuery } from "@tanstack/react-query";

import apiClient from "@/lib/api-client";

export interface WorkflowAnalytics {
  workflowId: string;
  totalJobs: number;
  totalJoblogs: number;
}

const getWorkflowAnalytics = async (
  workflowId: string
): Promise<WorkflowAnalytics> => {
  const { data } = await apiClient.get(`/analytics/workflows/${workflowId}`);
  return data;
};

export const useWorkflowAnalytics = (workflowId: string) => {
  return useQuery<WorkflowAnalytics, Error>({
    queryKey: ["workflow-analytics", workflowId],
    queryFn: () => getWorkflowAnalytics(workflowId),
    enabled: !!workflowId,
  });
};
