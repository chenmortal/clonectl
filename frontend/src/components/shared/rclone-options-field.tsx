import { ChevronDown, ChevronRight } from "lucide-react";
import * as React from "react";

import { InfoTip } from "@/components/shared/info-tip";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { RCLONE_OPTION_DEFS, RCLONE_OPTION_GROUPS, type RcloneOptionDef } from "@/lib/rclone-options";

const CATALOG_KEYS = new Set(RCLONE_OPTION_DEFS.map((d) => d.key));

/**
 * rclone 参数编辑器。value 是 rclone_options 的 JSON 文本(与任务存储格式一致,
 * 键为 rclone 规范名);结构化控件与“其他参数”JSON 直接改写这份文本。
 */
export function RcloneOptionsField({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const parsed = React.useMemo(() => {
    try {
      const obj = JSON.parse(value || "{}");
      if (obj && typeof obj === "object" && !Array.isArray(obj)) {
        return { ok: true as const, obj: obj as Record<string, unknown> };
      }
      return { ok: false as const, obj: {} as Record<string, unknown> };
    } catch {
      return { ok: false as const, obj: {} as Record<string, unknown> };
    }
  }, [value]);

  const extraKeys = parsed.ok
    ? Object.keys(parsed.obj).filter((k) => !CATALOG_KEYS.has(k))
    : [];
  const [showExtra, setShowExtra] = React.useState(extraKeys.length > 0);
  React.useEffect(() => {
    // 只在出现自定义键时自动展开,收起交给用户。
    if (extraKeys.length > 0) setShowExtra(true);
  }, [extraKeys.length]);

  const write = (mutate: (obj: Record<string, unknown>) => void) => {
    const obj: Record<string, unknown> = parsed.ok ? { ...parsed.obj } : {};
    mutate(obj);
    onChange(JSON.stringify(obj, null, 0));
  };

  const setOption = (def: RcloneOptionDef, raw: string | boolean) => {
    write((obj) => {
      if (raw === false || raw === "") {
        delete obj[def.key];
        return;
      }
      if (def.type === "bool") {
        obj[def.key] = true;
        return;
      }
      if (def.type === "int") {
        const n = Number(raw);
        if (Number.isFinite(n)) {
          obj[def.key] = n;
        } else {
          delete obj[def.key];
        }
        return;
      }
      obj[def.key] = raw;
    });
  };

  const extraText = parsed.ok
    ? JSON.stringify(Object.fromEntries(extraKeys.map((k) => [k, parsed.obj[k]])), null, 2)
    : value;

  const onExtraChange = (text: string) => {
    if (parsed.ok) {
      // 只替换非目录键,结构化控件里的键保持不变。
      try {
        const next = JSON.parse(text || "{}") as Record<string, unknown>;
        const merged: Record<string, unknown> = {};
        for (const [k, v] of Object.entries(parsed.obj)) {
          if (CATALOG_KEYS.has(k)) merged[k] = v;
        }
        for (const [k, v] of Object.entries(next)) merged[k] = v;
        onChange(JSON.stringify(merged, null, 0));
        return;
      } catch {
        // 落入下方原样回写:输入中间态允许非法 JSON,提交时统一校验
      }
    }
    onChange(text);
  };

  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        留空即使用 rclone 默认值;悬停 ⓘ 查看参数含义与调高/调低的影响。
        {parsed.ok ? null : (
          <span className="ml-1 text-destructive">当前 JSON 不合法,修正前参数不会生效。</span>
        )}
      </p>
      {RCLONE_OPTION_GROUPS.map((group) => {
        const defs = RCLONE_OPTION_DEFS.filter((d) => d.group === group);
        if (defs.length === 0) return null;
        return (
          <div key={group} className="space-y-2">
            <div className="text-xs font-medium text-muted-foreground">{group}</div>
            <div className="grid grid-cols-1 gap-x-8 gap-y-2.5 lg:grid-cols-2">
              {defs.map((def) => (
                <OptionRow
                  key={def.key}
                  def={def}
                  present={parsed.ok && def.key in parsed.obj}
                  value={parsed.ok ? parsed.obj[def.key] : undefined}
                  onChange={(raw) => setOption(def, raw)}
                />
              ))}
            </div>
          </div>
        );
      })}

      <div className="rounded-lg border">
        <button
          type="button"
          className="flex w-full items-center gap-1.5 px-3 py-2 text-xs font-medium text-muted-foreground hover:text-foreground"
          onClick={() => setShowExtra((s) => !s)}
        >
          {showExtra ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          其他参数(JSON,高级)
          {extraKeys.length > 0 && (
            <span className="font-normal opacity-75">· {extraKeys.length} 个自定义键</span>
          )}
        </button>
        {showExtra && (
          <div className="space-y-1 border-t px-3 py-2">
            <Textarea
              className="min-h-16 font-mono text-xs"
              rows={2}
              spellCheck={false}
              placeholder='{"bwlimit": "10M"}'
              value={extraText}
              onChange={(e) => onExtraChange(e.target.value)}
            />
            <p className="text-[11px] text-muted-foreground">
              上表未收录的 rclone 参数写在这里,与任务一起存储并原样传给 rclone。
            </p>
          </div>
        )}
      </div>
    </div>
  );
}

function OptionRow({
  def,
  present,
  value,
  onChange,
}: {
  def: RcloneOptionDef;
  present: boolean;
  value: unknown;
  onChange: (raw: string | boolean) => void;
}) {
  const label = (
    <span className="text-xs text-muted-foreground">默认 {def.default}</span>
  );
  return (
    <div
      className={cn(
        "flex items-center gap-2 rounded-md border px-2.5 py-1.5",
        present && "border-primary/40 bg-primary/5",
      )}
    >
      {def.type === "bool" ? (
        <Switch
          checked={value === true}
          onCheckedChange={(v) => onChange(v)}
          aria-label={def.key}
        />
      ) : (
        <Input
          className="h-7 w-24 font-mono text-xs"
          type={def.type === "int" ? "number" : "text"}
          inputMode={def.type === "int" ? "numeric" : undefined}
          placeholder={def.placeholder ?? def.default}
          value={present && typeof value === "string" || typeof value === "number" ? String(value) : ""}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      <div className="flex min-w-0 flex-1 items-center gap-1">
        <Label className="min-w-0 flex-1 cursor-default truncate text-xs font-normal">
          {def.label}
        </Label>
        <InfoTip title={def.label}>
          {`含义:${def.help}`}
          {`\n默认值:${def.default}`}
          {`\n${def.type === "bool" ? "开与关" : "高与低"}:${def.effect}`}
        </InfoTip>
      </div>
      {def.type !== "bool" && label}
    </div>
  );
}
