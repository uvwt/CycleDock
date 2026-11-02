import { useState, type ReactNode } from "react";
import { ModelSelection } from "./model-selection";

const STORAGE_KEY = "cycledock:preferred_model_route_id";

export function ModelSelectionProvider({ children }: { children: ReactNode }) {
  const [selectedRouteId, setSelectedRouteId] = useState<string | null>(() => {
    try {
      return localStorage.getItem(STORAGE_KEY) || null;
    } catch {
      return null;
    }
  });

  const selectRouteId = (routeId: string | null) => {
    setSelectedRouteId(routeId);
    try {
      if (routeId) {
        localStorage.setItem(STORAGE_KEY, routeId);
      } else {
        localStorage.removeItem(STORAGE_KEY);
      }
    } catch {
      // 忽略 localStorage 存取限制
    }
  };

  return (
    <ModelSelection.Provider value={{ selectedRouteId, selectRouteId }}>
      {children}
    </ModelSelection.Provider>
  );
}
