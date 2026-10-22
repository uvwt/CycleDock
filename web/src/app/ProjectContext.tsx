import { useEffect, useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../shared/api/client";
import type { Project } from "../shared/api/types";
import { keys } from "./query";
import { ProjectContext, type ProjectContextValue } from "./project-context";

const storageKey = "cycledock.active-project";
const emptyProjects: Project[] = [];

function projectFromLocation(pathname: string, search: string) {
  const routeMatch = pathname.match(
    /^\/(?:chat|board|cards|projects)\/([^/]+)/,
  );
  if (routeMatch?.[1]) return decodeURIComponent(routeMatch[1]);
  return new URLSearchParams(search).get("project") || "";
}

function storedProjectID() {
  try {
    return window.localStorage.getItem(storageKey) || "";
  } catch {
    return "";
  }
}

export function ProjectProvider({ children }: { children: ReactNode }) {
  const location = useLocation();
  const projectsQuery = useQuery({
    queryKey: keys.projects,
    queryFn: api.projects,
  });
  const projects = projectsQuery.data ?? emptyProjects;
  const routeProjectID = projectFromLocation(
    location.pathname,
    location.search,
  );
  const [preferredProjectID, setPreferredProjectID] = useState(storedProjectID);
  const persistedProjectID = storedProjectID();

  const routeProjectExists = projects.some(
    (project) => project.id === routeProjectID,
  );
  const persistedProjectExists = projects.some(
    (project) => project.id === persistedProjectID,
  );
  const preferredProjectExists = projects.some(
    (project) => project.id === preferredProjectID,
  );
  const projectID = routeProjectExists
    ? routeProjectID
    : persistedProjectExists
      ? persistedProjectID
      : preferredProjectExists
        ? preferredProjectID
        : projects[0]?.id || "";

  function selectProjectID(nextProjectID: string) {
    try {
      window.localStorage.setItem(storageKey, nextProjectID);
    } catch {
      // 本地存储不可用时仍保留当前 React 会话中的项目选择。
    }
    setPreferredProjectID(nextProjectID);
  }

  useEffect(() => {
    if (!projectID) return;
    try {
      window.localStorage.setItem(storageKey, projectID);
    } catch {
      // 本地存储不可用时仍允许当前会话正常使用项目切换。
    }
  }, [projectID]);

  const value: ProjectContextValue = {
    projects,
    projectID,
    project: projects.find((project) => project.id === projectID),
    isPending: projectsQuery.isPending,
    error: projectsQuery.error,
    refetch: () => {
      void projectsQuery.refetch();
    },
    setProjectID: selectProjectID,
  };

  return (
    <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>
  );
}
