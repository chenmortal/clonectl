import * as React from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { describeCron, nextCronRuns } from "@/lib/cron";
import { cn } from "@/lib/utils";

const DEFAULT_PRESETS = [
  { expr: "*/30 * * * *", label: "每 30 分钟" },
  { expr: "0 * * * *", label: "每小时" },
  { expr: "0 3 * * *", label: "每天 03:00" },
  { expr: "0 2 * * 1", label: "每周一 02:00" },
  { expr: "0 0 1 * *", label: "每月 1 日" },
];

const pad2 = (n: number) => String(n).padStart(2, "0");

function fmtNext(ts: number): string {
  const d = new Date(ts);
  return `${pad2(d.getUTCMonth() + 1)}-${pad2(d.getUTCDate())} ${pad2(d.getUTCHours())}:${pad2(d.getUTCMinutes())}`;
}

function localUtcLabel(): string {
  const off = -new Date().getTimezoneOffset();
  const sign = off >= 0 ? "+" : "-";
  const h = Math.floor(Math.abs(off) / 60);
  const m = Math.abs(off) % 60;
  return `UTC${sign}${h}${m ? `:${pad2(m)}` : ""}`;
}

/**
 * Cron(5 字段)输入框:预设按钮 + 实时直白释义 + 未来 3 次执行预览。
 * 语义与后端调度器一致(robfig/cron 标准 5 字段,按 UTC 执行)。
 */
export function CronField({
  value,
  onChange,
  presets = DEFAULT_PRESETS,
  optional = false,
  emptyHint,
  label = "Cron 计划",
}: {
  value: string;
  onChange: (v: string) => void;
  presets?: { expr: string; label: string }[];
  /** 允许留空(检查任务:留空 = 仅手动触发)。 */
  optional?: boolean;
  emptyHint?: string;
  label?: string;
}) {
  const trimmed = value.trim();
  const desc = React.useMemo(() => (trimmed ? describeCron(trimmed) : null), [trimmed]);
  const next = React.useMemo(() => (trimmed ? nextCronRuns(trimmed, 3) : null), [trimmed]);
  const invalid = desc && !desc.ok;

  return (
    <div className="space-y-2">
      <Label>{label}</Label>
      <Input
        className={cn("font-mono", invalid && "border-destructive focus-visible:ring-destructive")}
        placeholder={optional ? "0 4 * * *" : "0 3 * * *"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <div className="flex flex-wrap gap-1">
        {presets.map((p) => (
          <Button
            key={p.expr}
            type="button"
            variant="secondary"
            size="sm"
            className="h-6 px-2 text-[11px]"
            title={p.expr}
            onClick={() => onChange(p.expr)}
          >
            {p.label}
          </Button>
        ))}
      </div>
      <div className="space-y-0.5 text-xs">
        {trimmed === "" ? (
          emptyHint && <p className="text-muted-foreground">{emptyHint}</p>
        ) : desc && desc.ok ? (
          <p className="font-medium text-emerald-600 dark:text-emerald-400">{desc.text}(UTC)</p>
        ) : (
          <p className="text-destructive">{desc && !desc.ok ? desc.error : ""}</p>
        )}
        {next && next.length > 0 && (
          <p className="text-muted-foreground">
            接下来 3 次:{next.map((t) => fmtNext(t)).join("、")}
            <span className="ml-1 opacity-75">(UTC,本机 {localUtcLabel()})</span>
          </p>
        )}
      </div>
    </div>
  );
}
