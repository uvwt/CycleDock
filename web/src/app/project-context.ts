import { createContext, useContext } from "react";
import type { Project } from "../shared/api/types";

export type ProjectContextValue = {
  projects: Project[];
  projectID: string;
  project?: Project;
  isPending: boolean;
  error: unknown;
  refetch: () => void;
  setProjectID: (projectID: string) => void;
};

export const ProjectContext = createContext<ProjectContextValue | null>(null);

export function useProject() {
  const context = useContext(ProjectContext);
  if (!context) {
    throw new Error("useProject must be used inside ProjectProvider");
  }
  return context;
}
