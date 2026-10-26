"use client";

import type { ReactNode } from "react";
import * as React from "react";

import { AppSidebar } from "@/app/(main)/_components/sidebar/app-sidebar";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { auth } from "@/lib/auth";
import { getClientCookie } from "@/lib/cookie.client";
import {
  SIDEBAR_COLLAPSIBLE_VALUES,
  SIDEBAR_VARIANT_VALUES,
  type SidebarCollapsible,
  type SidebarVariant,
} from "@/lib/preferences/layout";
import { PREFERENCE_DEFAULTS } from "@/lib/preferences/preferences-config";
import { FeatureModulesProvider } from "@/lib/feature-modules";
import { cn } from "@/lib/utils";

import { MainContent } from "./_components/main-content";

// Reads a layout-critical preference from the browser cookie (falls back to the
// default during static-export prerender where document is unavailable). Kept
// fully client-side so the app can be statically exported — no Server Actions /
// next/headers.
function readPref<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  if (typeof document === "undefined") return fallback;
  const value = getClientCookie(key);
  return value && (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

export default function Layout({ children }: Readonly<{ children: ReactNode }>) {
  // Client-side auth gate — replaces the Next proxy/middleware that static export
  // disables. No token → bounce to /login; render nothing until confirmed so no
  // protected UI (or its API calls) flashes for a logged-out visitor.
  const [authed, setAuthed] = React.useState(false);
  // 嵌入模式：被 findu 外壳用 iframe 挂在内容区时（?embed=1），只渲染页面内容，
  // 不渲染控制台自己的侧栏与顶栏。否则会出现「导航套导航」，且每次切页都会重建外壳。
  // 首次带参进入后记入 sessionStorage：iframe 内部的客户端路由（Link 会丢掉 query）
  // 与后续刷新都保持嵌入形态；直接在新标签打开控制台不受影响（新标签 sessionStorage 为空）。
  const [embed] = React.useState(() => {
    if (typeof window === "undefined") return false;
    const flag = new URLSearchParams(window.location.search).get("embed") === "1";
    if (flag) {
      try {
        sessionStorage.setItem("findu_embed", "1");
      } catch {
        /* 隐私模式禁用 storage 时降级为只认 query */
      }
    }
    try {
      return flag || sessionStorage.getItem("findu_embed") === "1";
    } catch {
      return flag;
    }
  });
  React.useEffect(() => {
    if (auth.getToken()) {
      setAuthed(true);
    } else {
      // 客户端守卫认为未登录时，必须同时清掉 cookie：否则 proxy.ts 仅凭
      // “cookie 存在”就把我们从 /login 又重定向回主界面，与本守卫来回弹跳
      // 形成无限重定向 → 白屏（cookie 与 localStorage 不一致时触发）。
      auth.clearToken();
      window.location.href = "/login";
    }
  }, []);

  const defaultOpen = typeof document === "undefined" ? true : getClientCookie("sidebar_state") !== "false";
  const variant = readPref<SidebarVariant>(
    "sidebar_variant",
    SIDEBAR_VARIANT_VALUES,
    PREFERENCE_DEFAULTS.sidebar_variant,
  );
  const collapsible = readPref<SidebarCollapsible>(
    "sidebar_collapsible",
    SIDEBAR_COLLAPSIBLE_VALUES,
    PREFERENCE_DEFAULTS.sidebar_collapsible,
  );

  if (!authed) return null;

  if (embed) {
    return (
      <div className="min-h-screen bg-background">
        <div className="min-h-screen p-4 md:p-6">{children}</div>
      </div>
    );
  }

  return (
    <FeatureModulesProvider>
      <SidebarProvider
        defaultOpen={defaultOpen}
        style={
          {
            "--sidebar-width": "calc(var(--spacing) * 56)",
          } as React.CSSProperties
        }
      >
        <AppSidebar variant={variant} collapsible={collapsible} />
        <SidebarInset
          className={cn(
            "[html[data-content-layout=centered]_&>*]:mx-auto",
            "[html[data-content-layout=centered]_&>*]:w-full",
            "[html[data-content-layout=centered]_&>*]:max-w-screen-2xl",
            "peer-data-[variant=inset]:border",
            "[--dashboard-header-height:--spacing(12)]",
            "min-w-0 overflow-x-hidden",
          )}
        >
          <MainContent>{children}</MainContent>
        </SidebarInset>
      </SidebarProvider>
    </FeatureModulesProvider>
  );
}
