"use client";

import * as React from "react";

import { api } from "@/lib/api";
import type { FeatureModulesConfig } from "@/lib/types";

export type FeatureModuleKey = "info_collect" | "mobile_reverse" | "vulnerability" | "webshell";

interface FeatureModulesState {
  /** 模块开关原始配置；null 表示尚未加载。 */
  modules: FeatureModulesConfig | null;
  /** 该模块当前是否启用（未配置时默认启用）。 */
  isEnabled: (key: FeatureModuleKey) => boolean;
  /** 重新从后端拉取模块开关。 */
  refresh: () => Promise<void>;
  /** 就地更新内存中的模块开关（供模块管理 UI 在保存成功后刷新导航）。 */
  apply: (next: FeatureModulesConfig) => void;
  /** 后端读取失败时置为 false。 */
  loadFailed: boolean;
}

const FeatureModulesContext = React.createContext<FeatureModulesState | null>(null);

export function FeatureModulesProvider({ children }: { children: React.ReactNode }) {
  const [modules, setModules] = React.useState<FeatureModulesConfig | null>(null);
  const [loadFailed, setLoadFailed] = React.useState(false);

  const refresh = React.useCallback(async () => {
    try {
      const cfg = await api.config();
      setModules(cfg.feature_modules ?? {});
      setLoadFailed(false);
    } catch {
      setLoadFailed(true);
    }
  }, []);

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  const isEnabled = React.useCallback((key: FeatureModuleKey) => modules?.[key] !== false, [modules]);

  const apply = React.useCallback((next: FeatureModulesConfig) => {
    setModules(next);
  }, []);

  const value = React.useMemo<FeatureModulesState>(
    () => ({ modules, isEnabled, refresh, apply, loadFailed }),
    [modules, isEnabled, refresh, apply, loadFailed],
  );

  return <FeatureModulesContext.Provider value={value}>{children}</FeatureModulesContext.Provider>;
}

export function useFeatureModules(): FeatureModulesState {
  const ctx = React.useContext(FeatureModulesContext);
  if (!ctx) {
    throw new Error("useFeatureModules must be used within FeatureModulesProvider");
  }
  return ctx;
}
