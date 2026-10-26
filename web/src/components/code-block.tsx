"use client";
import * as React from "react";

import { CheckIcon, CopyIcon, WrapTextIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn, copyText } from "@/lib/utils";

/** JsonLine 对单行做轻量 JSON 高亮（key/字符串/数字/布尔/null）。 */
function JsonLine({ line }: { line: string }) {
  const parts: React.ReactNode[] = [];
  const tokens = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g;
  let cursor = 0;
  for (const match of line.matchAll(tokens)) {
    const index = match.index ?? 0;
    if (index > cursor) parts.push(line.slice(cursor, index));
    let className = "text-chart-4";
    if (match[1]) className = match[2] ? "text-primary" : "text-chart-2";
    else if (match[3]) className = "text-chart-3";
    parts.push(
      <span key={`${index}-${match[0].length}`} className={className}>
        {match[0]}
      </span>,
    );
    cursor = index + match[0].length;
  }
  if (cursor < line.length) parts.push(line.slice(cursor));
  return parts;
}

/** CodeBlock 通用代码块：行号 + 复制 + 换行切换 + 基础 JSON 高亮，替代裸 <pre>。 */
export function CodeBlock({
  code,
  maxHeight = "max-h-[60vh]",
  className,
}: {
  code: string;
  maxHeight?: string;
  className?: string;
}) {
  const [wrapLines, setWrapLines] = React.useState(true);
  const [copied, setCopied] = React.useState(false);
  const value = code || "（空）";
  const lines = value.replaceAll("\r\n", "\n").split("\n");
  const isJson = /^\s*[{[]/.test(value.trimStart());

  React.useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);

  const copy = async () => {
    const ok = await copyText(value);
    if (ok) {
      setCopied(true);
      return;
    }
    toast.error("复制失败，请使用 Ctrl/Cmd+A 后复制");
  };

  return (
    <div className={cn("relative overflow-hidden rounded-md border bg-background shadow-xs", className)}>
      <div className="absolute top-2 right-2 flex items-center gap-0.5 rounded-md border bg-background/95 p-0.5 shadow-xs">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={wrapLines ? "关闭自动换行" : "开启自动换行"}
              aria-pressed={wrapLines}
              onClick={() => setWrapLines((current) => !current)}
            >
              <WrapTextIcon />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{wrapLines ? "关闭自动换行" : "开启自动换行"}</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={copied ? "已复制" : "复制代码"}
              onClick={() => void copy()}
            >
              {copied ? <CheckIcon /> : <CopyIcon />}
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{copied ? "已复制" : "复制代码"}</TooltipContent>
        </Tooltip>
      </div>
      {/* biome-ignore lint/a11y/useSemanticElements: 需保留行号与高亮标记，无法用 textarea 表达。 */}
      <div
        role="textbox"
        aria-label="代码"
        aria-multiline="true"
        aria-readonly="true"
        tabIndex={0}
        className={cn(
          "min-w-0 overflow-auto bg-background py-3 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
          maxHeight,
        )}
        onKeyDown={(event) => {
          if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== "a") return;
          event.preventDefault();
          const selection = window.getSelection();
          if (!selection) return;
          const range = document.createRange();
          range.selectNodeContents(event.currentTarget);
          selection.removeAllRanges();
          selection.addRange(range);
        }}
      >
        {lines.map((line, index) => (
          <div
            key={`${index}-${line}`}
            data-line={index + 1}
            className="grid min-w-0 grid-cols-[1.5rem_minmax(0,1fr)] leading-relaxed before:sticky before:left-0 before:self-stretch before:border-r before:bg-muted/20 before:px-1 before:text-right before:text-muted-foreground/60 before:content-[attr(data-line)]"
          >
            <code
              className={cn(
                "min-h-[1lh] min-w-0 pr-16 pl-1.5 [tab-size:4]",
                wrapLines ? "break-words whitespace-pre-wrap" : "whitespace-pre",
              )}
            >
              {isJson ? <JsonLine line={line} /> : line}
            </code>
          </div>
        ))}
      </div>
    </div>
  );
}