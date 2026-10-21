import React from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { queryClient } from "./app-v2/query";
import { AppV2 } from "./app-v2/AppV2";
import UiShowcase from "./routes/showcase/UiShowcase";
import "./index.css";
import "./styles/tokens.css";
import "./styles/application.css";
import {
  initializeLanguage,
  initializePreferences,
} from "./shared/preferences";

initializeLanguage();
initializePreferences();

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        {import.meta.env.DEV && window.location.pathname === "/__ui" ? (
          <UiShowcase />
        ) : (
          <AppV2 />
        )}
      </BrowserRouter>
      <Toaster richColors position="bottom-right" />
    </QueryClientProvider>
  </React.StrictMode>,
);
