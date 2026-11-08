import { useQuery } from "@tanstack/react-query";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";

export function useAgentStatus() {
  return useQuery({
    queryKey: keys.agentStatus,
    queryFn: api.agentStatus,
    // 未配置模型时轮询等待配置完成以自动解除发送限制；已配置后停止无意义的 5 秒高频轮询。
    refetchInterval: (query) =>
      query.state.data?.model_configured ? false : 5000,
  });
}
