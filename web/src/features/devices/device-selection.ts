import { createContext, useContext } from "react";
import type { DeviceTarget } from "../../shared/api/types";

export const DeviceSelection = createContext<{
  target: DeviceTarget | null;
  selectTarget: (target: DeviceTarget | null) => void;
} | null>(null);

export function useDeviceSelection() {
  const selection = useContext(DeviceSelection);
  if (!selection) throw new Error("DeviceSelectionProvider is required");
  return selection;
}
