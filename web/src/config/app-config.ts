import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "FindUAI",
  // 登录用户名：与后端 server/auth.go 的 loginUsername 保持一致
  loginUsername: "FindUAI",
  version: packageJson.version,
  copyright: `© ${currentYear}, FindUAI.`,
  meta: {
    title: "FindUAI — 自主渗透测试控制台",
    description: "LLM 驱动的自主渗透测试系统控制台",
  },
};
