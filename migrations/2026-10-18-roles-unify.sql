
BEGIN;

-- 1) 角色分层：engine=引擎编排件，role=用户可选角色
ALTER TABLE public.agents ADD COLUMN IF NOT EXISTS kind   text NOT NULL DEFAULT 'engine';
ALTER TABLE public.agents ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'findu';
ALTER TABLE public.agents ADD COLUMN IF NOT EXISTS icon   text;
UPDATE public.agents SET kind='role' WHERE role='assistant';

-- 2) 迁入 findu 角色（B 方案：渗透测试并入既有 pentest，不新建行）
INSERT INTO public.agents (key,name,description,role,builtin,enabled,kind,source,icon)
VALUES
 ('findu_default','默认','默认角色，不额外携带用户提示词，使用默认MCP','assistant',false,true,'role','findu','\U0001F535'),
 ('findu_audit','审计取证','安全审计与取证（云 / 容器 / 合规基线 / 数字取证），只评估不利用','assistant',false,true,'role','findu','\U0001F50E')
ON CONFLICT (key) DO NOTHING;

-- 3) prompt 落库（version=1），并指为当前版本
INSERT INTO public.agent_prompts (agent_id,version,template_text,note,updated_by)
SELECT a.id,1,v.txt,'migrated from findu configs/roles/'||v.f,'findu-migration'
FROM (VALUES
 ('findu_default','默认.yaml','开工前先看 skill 工具描述里的 available_skills 列表，本轮任务命中其中任一技能时，必须先调用 skill 加载该技能的完整流程再动手。'),
 ('findu_audit','审计取证.yaml','你是一个专业的云安全审计、容器安全与数字取证专家。以只读方式评估——云环境（AWS/Azure/GCP）的配置缺陷、权限与合规基线；Docker 与 Kubernetes 的配置、镜像漏洞与运行时风险；文件、磁盘镜像、内存转储的取证分析，以及隐写、元数据、数据恢复等取证件提取。只做评估与取证，不做漏洞利用、提权、横向移动或权限维持（那属于「渗透测试」角色的范围）。产出以证据为中心，逐项写明依据、影响与风险等级。 开工前先看 skill 工具描述里的 available_skills 列表，本轮任务命中其中任一技能时，必须先调用 skill 加载该技能的完整流程再动手。')
) AS v(k,f,txt)
JOIN public.agents a ON a.key=v.k
WHERE NOT EXISTS (SELECT 1 FROM public.agent_prompts p WHERE p.agent_id=a.id AND p.version=1);

UPDATE public.agents a SET current_prompt_id=p.id
FROM public.agent_prompts p
WHERE p.agent_id=a.id AND p.version=1 AND a.current_prompt_id IS NULL AND a.key IN ('findu_default','findu_audit');

-- 4) B 方案：findu 的渗透领域提示词作为 pentest 的 prompt v2（v1 保留可回滚）
INSERT INTO public.agent_prompts (agent_id,version,template_text,note,updated_by)
SELECT a.id,2,
       '你是一个专业的网络安全渗透测试专家，覆盖从侦察到利用的完整攻击链——资产与子域名收集、端口与服务识别、Web/API/框架漏洞检测、目录与文件枚举、综合漏洞扫描、二进制逆向与密码分析、CTF 解题，以及拿到权限后的提权、横向移动与权限维持。按「先侦察后打击、先验证再上报」的顺序推进；上报漏洞前必须有可复现证据（请求/响应、回显或工具原始输出），拿不到证据的结论标注为待验证，不得凭推测定性。' || E'\n\n' || COALESCE(p1.template_text,''),
       'merge findu roles/渗透测试.yaml (B)', 'findu-migration'
FROM public.agents a
LEFT JOIN public.agent_prompts p1 ON p1.id=a.current_prompt_id
WHERE a.key='pentest'
  AND NOT EXISTS (SELECT 1 FROM public.agent_prompts p WHERE p.agent_id=a.id AND p.version=2);

UPDATE public.agents a SET current_prompt_id=p.id, source='findu'
FROM public.agent_prompts p
WHERE p.agent_id=a.id AND p.version=2 AND a.key='pentest';

-- 5) 工具绑定：新角色继承 auto 的内置工具集（findu 白名单语义，不绑就没有工具）
UPDATE public.tools SET agents = agents || '["findu_default"]'::jsonb
WHERE agents ? 'auto' AND NOT agents ? 'findu_default';
UPDATE public.tools SET agents = agents || '["findu_audit"]'::jsonb
WHERE agents ? 'auto' AND NOT agents ? 'findu_audit';

COMMIT;
