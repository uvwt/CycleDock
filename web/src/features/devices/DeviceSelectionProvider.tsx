import { useState, type ReactNode } from "react";
import type { DeviceTarget } from "../../shared/api/types";
import { DeviceSelection } from "./device-selection";

// 选择在当前 Workspace 的页面间共享；切换 Workspace 或刷新页面后默认不指定。
export function DeviceSelectionProvider({ children }: { children: ReactNode }) {
  const [target, selectTarget] = useState<DeviceTarget | null>(null);
  return (
    <DeviceSelection.Provider value={{ target, selectTarget }}>
      {children}
    </DeviceSelection.Provider>
  );
}
