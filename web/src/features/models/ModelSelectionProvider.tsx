import { useState, type ReactNode } from "react";
import { ModelSelection } from "./model-selection";

// 内存态草稿选择，不再使用跨会话的全局 localStorage 避免状态串扰与跨卡片污染
export function ModelSelectionProvider({ children }: { children: ReactNode }) {
  const [selectedRouteId, selectRouteId] = useState<string | null>(null);

  return (
    <ModelSelection.Provider value={{ selectedRouteId, selectRouteId }}>
      {children}
    </ModelSelection.Provider>
  );
}
