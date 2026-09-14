import { Input, Select, Space, Typography } from "antd";

const DOW_NAMES = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

export const CRON_PRESETS: { label: string; value: string }[] = [
  { label: "每天 03:00", value: "0 3 * * *" },
  { label: "每小时整点", value: "0 * * * *" },
  { label: "每 30 分钟", value: "*/30 * * * *" },
  { label: "每周一 02:00", value: "0 2 * * 1" },
  { label: "每周日 04:00", value: "0 4 * * 0" },
  { label: "每月 1 日 00:00", value: "0 0 1 * *" },
];

type Field = { every: number } | { values: string[] } | "all";

function parseField(v: string): Field {
  if (v === "*") return "all";
  if (/^\*\/\d+$/.test(v)) return { every: Number(v.slice(2)) };
  return { values: v.split(",") };
}

function isAll(f: Field): boolean {
  return f === "all";
}

function everyOf(f: Field): number | null {
  return typeof f === "object" && "every" in f ? f.every : null;
}

function valuesOf(f: Field): string[] | null {
  return typeof f === "object" && "values" in f ? f.values : null;
}

function pad2(v: string): string {
  return /^\d+$/.test(v) ? v.padStart(2, "0") : v;
}

export function describeCron(cron: string): string {
  const parts = cron.trim().split(/\s+/);
  if (parts.length !== 5) return "格式：分 时 日 月 周（5 段）";
  const [mi, ho, dom, mo, dow] = parts.map(parseField);
  const segs: string[] = [];

  const moVals = valuesOf(mo);
  if (moVals) segs.push(`${moVals.join(",")} 月`);
  const domVals = valuesOf(dom);
  if (domVals) segs.push(`每月 ${domVals.join(",")} 日`);
  const dowVals = valuesOf(dow);
  if (dowVals) segs.push(dowVals.map((v) => DOW_NAMES[Number(v)] ?? `周${v}`).join("、"));
  const hasDatePart = Boolean(moVals || domVals || dowVals);

  const miEvery = everyOf(mi);
  const miVals = valuesOf(mi);
  const hoEvery = everyOf(ho);
  const hoVals = valuesOf(ho);

  if (isAll(ho) && isAll(mi)) {
    segs.push("每分钟");
  } else if (isAll(ho) && miEvery !== null) {
    segs.push(`每 ${miEvery} 分钟`);
  } else if (isAll(ho) && miVals && miVals.length === 1 && miVals[0] === "0") {
    segs.push("每小时整点");
  } else if (isAll(ho) && miVals) {
    segs.push(`每小时的第 ${miVals.join(",")} 分钟`);
  } else if (hoEvery !== null) {
    segs.push(`每 ${hoEvery} 小时`);
    if (miVals) segs.push(`第 ${miVals.join(",")} 分钟`);
  } else if (hoVals) {
    const hours = hoVals.map(pad2).join("、");
    if (isAll(mi)) segs.push(`${hours} 点内每分钟`);
    else if (miEvery !== null) segs.push(`${hours} 点起每 ${miEvery} 分钟`);
    else if (miVals) segs.push(`${hours} 点 ${miVals.map(pad2).join(",")} 分`);
  }

  if (!hasDatePart && segs.some((s) => s.includes("点"))) {
    segs.unshift("每天");
  }

  return segs.length ? segs.join("，") : cron;
}

export function isValidCron(cron: string): boolean {
  return /^\S+(\s+\S+){4}$/.test(cron.trim());
}

interface CronInputProps {
  value?: string;
  onChange?: (value: string) => void;
}

export default function CronInput({ value = "", onChange }: CronInputProps) {
  const valid = isValidCron(value);
  return (
    <Space direction="vertical" style={{ width: "100%" }} size={4}>
      <Space.Compact style={{ width: "100%" }}>
        <Select
          style={{ width: 150 }}
          placeholder="快捷选择"
          options={CRON_PRESETS}
          onChange={(v) => onChange?.(v)}
        />
        <Input
          style={{ width: 170, marginLeft: 8 }}
          value={value}
          placeholder="0 3 * * *"
          status={value && !valid ? "error" : undefined}
          onChange={(e) => onChange?.(e.target.value)}
        />
      </Space.Compact>
      <Typography.Text type={valid ? "secondary" : "danger"} style={{ fontSize: 12 }}>
        {value ? describeCron(value) : "选择预设或输入表达式（分 时 日 月 周）"}
      </Typography.Text>
    </Space>
  );
}
