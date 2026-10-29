import { createContext, useContext } from "react";

export const ContextSidebarVisibility = createContext(true);

export function useContextSidebarVisible() {
  return useContext(ContextSidebarVisibility);
}
