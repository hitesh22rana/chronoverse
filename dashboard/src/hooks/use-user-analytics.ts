import { useQuery } from "@tanstack/react-query";

import apiClient from "@/lib/api-client";

export interface UserAnalytics {
  totalWorkflows: number;
  totalJobs: number;
  totalJoblogs: number;
}

const getUserAnalytics = async (): Promise<UserAnalytics> => {
  const { data } = await apiClient.get("/analytics/user");
  return data;
};

export const useUserAnalytics = () => {
  return useQuery<UserAnalytics, Error>({
    queryKey: ["user-analytics"],
    queryFn: getUserAnalytics,
  });
};
