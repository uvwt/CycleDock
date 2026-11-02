import { createContext, useContext } from "react";

export const ModelSelection = createContext<{
  selectedRouteId: string | null;
  selectRouteId: (routeId: string | null) => void;
} | null>(null);

export function useModelSelection() {
  const selection = useContext(ModelSelection);
  if (!selection) throw new Error("ModelSelectionProvider is required");
  return selection;
}
