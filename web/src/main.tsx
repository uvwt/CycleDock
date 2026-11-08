import React from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { queryClient } from "./app/query";
import { App } from "./app/App";
import { TooltipProvider } from "./shared/ui/tooltip";
import "./index.css";
import {
  initializeLanguage,
  initializePreferences,
} from "./shared/preferences";

initializeLanguage();
initializePreferences();

if (typeof window !== "undefined") {
  const syncViewport = () => {
    if (window.scrollY !== 0 || window.scrollX !== 0) {
      window.scrollTo(0, 0);
    }
    const vv = window.visualViewport;
    if (!vv) return;
    // 判断软键盘是否弹起：不仅对比 innerHeight，同时对比 screen.height（兼容 interactive-widget=resizes-content 下 innerHeight 同步缩小的情况）
    const isKeyboardOpen =
      window.innerHeight - vv.height > 80 ||
      (window.screen.height > 0 && window.screen.height - vv.height > 140);
    if (isKeyboardOpen) {
      document.documentElement.style.setProperty(
        "--app-offset-top",
        `${Math.max(0, Math.round(vv.offsetTop))}px`,
      );
      document.documentElement.style.setProperty(
        "--app-height",
        `${Math.round(vv.height)}px`,
      );
    } else {
      document.documentElement.style.removeProperty("--app-offset-top");
      document.documentElement.style.removeProperty("--app-height");
    }
  };
  syncViewport();
  window.addEventListener("scroll", syncViewport, { passive: true });
  window.addEventListener("resize", syncViewport, { passive: true });
  window.visualViewport?.addEventListener("resize", syncViewport, {
    passive: true,
  });
  window.visualViewport?.addEventListener("scroll", syncViewport, {
    passive: true,
  });
  window.addEventListener("focusin", () => {
    setTimeout(syncViewport, 50);
    setTimeout(syncViewport, 250);
  });
  window.addEventListener("focusout", () => {
    setTimeout(syncViewport, 50);
    setTimeout(syncViewport, 250);
  });
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <TooltipProvider delayDuration={150}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
        <Toaster richColors position="top-right" />
      </TooltipProvider>
    </QueryClientProvider>
  </React.StrictMode>,
);
