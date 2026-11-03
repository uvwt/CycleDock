import { useQuery } from "@tanstack/react-query";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";

export function useAgentStatus() {
  return useQuery({
    queryKey: keys.agentStatus,
    queryFn: api.agentStatus,
    // 设置可能在另一个页面或由工作区所有者修改，恢复配置后自动解除发送限制。
    refetchInterval: 5000,
  });
}
