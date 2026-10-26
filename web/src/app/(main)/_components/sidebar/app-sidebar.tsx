"use client";

import Link from "next/link";

import { useShallow } from "zustand/react/shallow";

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import { APP_CONFIG } from "@/config/app-config";
import { useCurrentUser } from "@/hooks/use-current-user";
import { type FeatureModuleKey, useFeatureModules } from "@/lib/feature-modules";
import { type NavGroup, type NavMainItem, sidebarItems } from "@/navigation/sidebar/sidebar-items";
import { usePreferencesStore } from "@/stores/preferences/preferences-provider";

import { NavMain } from "./nav-main";
import { NavUser } from "./nav-user";
import { SidebarSupportCard } from "./sidebar-support-card";

// 导航项所属的功能模块：模块关闭时隐藏对应导航项。
// 关键：任一可开关模块默认启用（未配置时启用），仅在明确关闭时隐藏。
const NAV_MODULE_MAP: Record<string, FeatureModuleKey> = {
  "info-collect": "info_collect",
  assets: "info_collect",
  miniprogram: "mobile_reverse",
  android: "mobile_reverse",
  "vuln-kb": "vulnerability",
  webshell: "webshell",
};

function filterItems(groups: readonly NavGroup[], isEnabled: (k: FeatureModuleKey) => boolean): NavGroup[] {
  return groups
    .map((group) => {
      const items = group.items
        .map((item): NavMainItem | null => {
          const moduleKey = NAV_MODULE_MAP[item.id];
          if (moduleKey && !isEnabled(moduleKey)) return null;
          if (!item.subItems) return item;
          const subItems = item.subItems.filter((sub) => !NAV_MODULE_MAP[sub.id] || isEnabled(NAV_MODULE_MAP[sub.id]));
          if (subItems.length === 0) return null;
          return { ...item, subItems } as NavMainItem;
        })
        .filter((item): item is NavMainItem => item !== null);
      return { ...group, items };
    })
    .filter((group) => group.items.length > 0);
}

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  const currentUser = useCurrentUser();
  const { isEnabled } = useFeatureModules();
  const { sidebarVariant, sidebarCollapsible, isSynced } = usePreferencesStore(
    useShallow((s) => ({
      sidebarVariant: s.sidebarVariant,
      sidebarCollapsible: s.sidebarCollapsible,
      isSynced: s.isSynced,
    })),
  );

  const variant = isSynced ? sidebarVariant : props.variant;
  const collapsible = isSynced ? sidebarCollapsible : props.collapsible;
  const visibleItems = filterItems(sidebarItems, isEnabled);

  return (
    <Sidebar {...props} variant={variant} collapsible={collapsible}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton asChild>
              <Link prefetch={false} href="/">
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src="/logo.png" alt="FindUAI" width={40} height={40} className="shrink-0" />
                <span className="font-semibold text-base">{APP_CONFIG.name}</span>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <NavMain items={visibleItems} />
        {/* <NavDocuments items={data.documents} /> */}
        {/* <NavSecondary items={data.navSecondary} className="mt-auto" /> */}
      </SidebarContent>
      <SidebarFooter>
        <NavUser user={currentUser} />
      </SidebarFooter>
    </Sidebar>
  );
}
