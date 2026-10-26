---
name: waf-bypass-techniques
description: >-
  WAF 绕过:编码变形,内联注释混淆,换注入点(header/cookie/JSON),分块传输,HPP 参数污染,Content-Type 变换,等价函数替换。Use when injection payloads are blocked by a WAF, or when crafting WAF-evasion variants for SQLi/XSS/command injection.
  触发：WAF绕过,编码变形,内联注释,换注入点,分块传输,HPP,Content-Type变换
metadata:
  roles: [渗透测试, Web与API安全测试]
  stages: [web, api, exploit]
  tags: [渗透测试, penetration-testing, WAF, 红队]
---

## WAF 绕过

```
=== WAF bypass ===
先识别: wafw00f / 响应头与拦截页指纹 → 按产品选打法(CDN WAF/硬件 WAF/主机软件)
编码变形: 双重URL编码 %2527 | unicode \\u0027 | hex 0x27/unhex | char(39) | HTML实体 &#39; | 超长UTF-8 %c0%a7
注释混淆: 内联注释 /*!50000select*/ | 关键词分段 sel/**/ect | 换行插空 %0a %0d | 空格替代 /**/+/()%09
换注入点: X-Forwarded-For/Referer/Cookie | JSON key 与嵌套层 | XML/multipart 字段 | 参数名大小写与同义(a[]=)
分块传输: Transfer-Encoding: chunked 拆包 → WAF 不重组 chunk 即漏检(后端须支持)
HPP: 同参数重复 id=1&id=2UNION.. → WAF 取首/尾,后端取异的分歧
Content-Type: form ↔ json ↔ multipart 互转,后端框架兼容解析而 WAF 只解一种
脏数据: 超长参数名/上千个参数/巨型 header 撑过 WAF 检测上限
等价替换: substring→mid/substr | ascii→ord | concat→|| | =→like/<> | and→&&
纪律: 每次变形记录响应差异(拦截页/200/报错)定位过滤规则;对授权目标使用,不做全网变形喷射
```

## 本平台实战案例（真实目标验证）

### 案例1：CDN 502 绕过（URL 模式过滤）

```
🚨CDN 502绕过(URL模式过滤): CDN对admin路径返回502时,HTTP编码/走私/方法变换全无效→换协议:
  ①WebSocket升级: CDN通常只过滤HTTP响应,WS路径可能不在过滤规则中。/api/v1/a/ws/返回200而/api/v1/a/doLogin返回502=WS路径不受过滤
  ②找源站IP直连: DNS历史/证书透明/SNI扫描/同子网扫描/邮件头Return-Path/Shodan指纹匹配/CDN回源配置泄露/唯一错误页面指纹→绕过CDN直连后端
  ③非标端口: 扫CDN节点全端口,某些端口可能代理不同后端或过滤规则不同(如8085暴露actuator)
  ④路径穿越: /api/v1/v/..;/a/doLogin让Tomcat路由到admin但CDN可能不识别(注意:CDN仍可能过滤响应)
  ⑤泛解析识别: 所有子域名解析到CDN=通配符DNS,不是真实记录。用114.114.114.114/8.8.8.8对比结果
```
要点：区分「URL 模式过滤」（时序固定、所有方法/编码/端口/节点都 502）vs「后端挂了」vs「响应内容过滤」——判错方向会白耗大量时间。

### 案例2：网宿（WSCN）CDN JS 挑战识别与绕过

```
🚨WSCN/网宿CDN JS挑战识别与绕过: 响应含`_jsc_ch_conf`+`ws_sec_page.js`=网宿JS挑战(非WAF拦截)
  特征: cv:".域名",mt:"jhsq987",chType/chTs/chID/chHash字段 | 403页6000+字节含"Embed Iframe"
  绕过: CDN只对特定path白名单(如/c/)不做挑战→用Spring Boot ..;/ traversal(`/c/..;/target_path`)穿透到后端其他路由
  识别后端框架: JSON错误`{"timestamp":...,"status":...,"error":...,"path":...}`=Spring Boot | 405=路由存在但方法错
```
要点：JS 挑战 ≠ WAF 拦截（前者是 CDN 的机器人识别）。识别特征在响应体（`_jsc_ch_conf`），绕过靠「白名单路径 + 穿越」。
