// cron 表达式解析/释义/下次执行时间预览。
//
// 语义对齐后端调度器(gocron + robfig/cron v3 ParseStandard,5 字段):
//   - 仅接受 5 个空白分隔字段(后端 ValidateCron 同样拒绝 @daily 等描述符)
//   - `?` 等价 `*`;月/周支持英文名(jan、mon,大小写不敏感)
//   - `a-b/n`、`*/n`、`n/step`(= n-max/step)、列表
//   - 周 0-6(0=周日),7 非法;月末/周当日均受限时为“或”关系
//   - 执行时刻按 UTC 计算(gocron WithLocation(UTC))

export interface CronField {
  /** 该字段命中的全部取值(升序)。 */
  values: number[];
  /** 是否为“纯通配”(文本 * 或 ?,且步进 ≤1)——参与月末/周的“与/或”判定。 */
  star: boolean;
  /** 是否命中了全部取值(可能来自 a-b/1 这类显式全范围,star 仍为 false)。 */
  all: boolean;
  /** 文本片段,供释义使用。 */
  segments: Segment[];
}

interface Segment {
  kind: "star" | "step" | "range-step" | "range" | "single";
  start: number;
  end: number;
  step: number;
}

export interface CronParse {
  ok: true;
  minute: CronField;
  hour: CronField;
  dom: CronField;
  month: CronField;
  dow: CronField;
}

export type CronParseResult = { ok: true; parse: CronParse } | { ok: false; error: string };

interface Bounds {
  min: number;
  max: number;
  names?: Record<string, number>;
}

const MONTH_NAMES = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];
const DOW_NAMES = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

const BOUNDS = {
  minute: { min: 0, max: 59 },
  hour: { min: 0, max: 23 },
  dom: { min: 1, max: 31 },
  month: { min: 1, max: 12, names: Object.fromEntries(MONTH_NAMES.map((n, i) => [n, i + 1])) },
  dow: { min: 0, max: 6, names: Object.fromEntries(DOW_NAMES.map((n, i) => [n, i])) },
} satisfies Record<string, Bounds>;

const WEEKDAY_ZH = ["日", "一", "二", "三", "四", "五", "六"];

/** 解析单个整数,语义对齐 robfig mustParseInt(strconv.Atoi + 拒绝负数)。 */
function parseIntStrict(s: string): number | null {
  if (!/^[+-]?\d+$/.test(s)) return null;
  const n = Number(s);
  if (!Number.isInteger(n) || n < 0) return null;
  return n;
}

function parseIntOrName(s: string, b: Bounds): number | null {
  if (b.names) {
    const v = b.names[s.toLowerCase()];
    if (v !== undefined) return v;
  }
  return parseIntStrict(s);
}

/** 对齐 robfig getRange:解析一段 a[-b][/step]。 */
function getRange(expr: string, b: Bounds): { segments: Segment[]; star: boolean } | { error: string } {
  const rangeAndStep = expr.split("/");
  if (rangeAndStep.length > 2) return { error: `"${expr}" 中斜杠过多` };

  const lowAndHigh = rangeAndStep[0].split("-");
  if (lowAndHigh.length > 2) return { error: `"${expr}" 中连字符过多` };
  const singleDigit = lowAndHigh.length === 1;

  let start: number, end: number, star = false;
  if (lowAndHigh[0] === "*" || lowAndHigh[0] === "?") {
    start = b.min;
    end = b.max;
    star = true;
  } else {
    const lo = parseIntOrName(lowAndHigh[0], b);
    if (lo === null) return { error: `"${expr}" 不是合法的数值或名称` };
    start = lo;
    if (lowAndHigh.length === 1) {
      end = lo;
    } else {
      const hi = parseIntOrName(lowAndHigh[1], b);
      if (hi === null) return { error: `"${expr}" 不是合法的数值或名称` };
      end = hi;
    }
  }

  let step = 1;
  if (rangeAndStep.length === 2) {
    const s = parseIntStrict(rangeAndStep[1]);
    if (s === null || s === 0) return { error: `"${expr}" 的步进必须是正整数` };
    step = s;
    // robfig:N/step 表示 N-max/step;步进 >1 时不再视作通配。
    if (singleDigit) end = b.max;
    if (step > 1) star = false;
  }

  if (start < b.min) return { error: `"${expr}" 起始值 ${start} 低于最小值 ${b.min}` };
  if (end > b.max) return { error: `"${expr}" 结束值 ${end} 超过最大值 ${b.max}` };
  if (start > end) return { error: `"${expr}" 起始值 ${start} 大于结束值 ${end}` };

  const kind = star ? "star" : singleDigit && rangeAndStep.length === 2 ? "step" : lowAndHigh.length === 2 ? (rangeAndStep.length === 2 ? "range-step" : "range") : "single";
  return { segments: [{ kind, start, end, step }], star };
}

function parseField(text: string, b: Bounds): { field: CronField } | { error: string } {
  // robfig 用 strings.FieldsFunc 按逗号切分,空段会被丢弃(“1,,2” 等价 “1,2”)。
  const parts = text.split(",").filter((p) => p.length > 0);
  if (parts.length === 0) return { error: "字段不能为空" };

  const values = new Set<number>();
  const segments: Segment[] = [];
  let star = false;
  for (const part of parts) {
    const r = getRange(part, b);
    if ("error" in r) return r;
    star = star || r.star;
    segments.push(...r.segments);
    for (const seg of r.segments) {
      for (let v = seg.start; v <= seg.end; v += seg.step) values.add(v);
    }
  }
  const list = [...values].sort((a, b2) => a - b2);
  return {
    field: {
      values: list,
      star,
      all: list.length === b.max - b.min + 1,
      segments,
    },
  };
}

export function parseCron(expr: string): CronParseResult {
  const fields = expr.trim().split(/\s+/);
  if (fields.length !== 5) {
    return { ok: false, error: "必须是 5 个空格分隔的字段:分 时 日 月 周(不支持 @daily 等简写)" };
  }
  if (fields[0].toUpperCase().startsWith("TZ=") || fields[0].toUpperCase().startsWith("CRON_TZ=")) {
    return { ok: false, error: "不支持 TZ=/CRON_TZ= 前缀,调度固定按 UTC 执行" };
  }
  const parsed: CronField[] = [];
  const keys = ["minute", "hour", "dom", "month", "dow"] as const;
  for (let i = 0; i < 5; i++) {
    const r = parseField(fields[i], BOUNDS[keys[i]]);
    if ("error" in r) return { ok: false, error: `第 ${i + 1} 个字段(${keys[i] === "dom" ? "日" : keys[i] === "dow" ? "周" : keys[i] === "month" ? "月" : keys[i] === "hour" ? "时" : "分"})${r.error.startsWith('"') ? ":" : ""}${r.error}` };
    parsed.push(r.field);
  }
  return {
    ok: true,
    parse: { ok: true, minute: parsed[0], hour: parsed[1], dom: parsed[2], month: parsed[3], dow: parsed[4] },
  };
}

export function isValidCron(expr: string): boolean {
  return parseCron(expr).ok;
}

const pad2 = (n: number) => String(n).padStart(2, "0");
const hhmm = (h: number, m: number) => `${pad2(h)}:${pad2(m)}`;

function listValues(field: CronField, fmt: (n: number) => string, max = 5): string | null {
  if (field.values.length === 0) return null;
  if (field.values.length > max) return null;
  return field.values.map(fmt).join("、");
}

/** 分钟/小时字段的“时刻”描述;返回 null 表示不适合短语化(交给通配处理)。 */
function describeTimePart(f: CronField, unit: "分" | "时"): string | null {
  const seg = f.segments;
  if (seg.length === 1) {
    const s = seg[0];
    if (s.kind === "single") return unit === "时" ? `${s.start} 点` : `第 ${s.start} 分`;
    if (s.kind === "range") return unit === "时" ? `${s.start} 到 ${s.end} 点` : `第 ${s.start} 到 ${s.end} 分`;
  }
  const l = listValues(f, (n) => (unit === "时" ? `${n} 点` : `${n}`), 4);
  if (l) return unit === "时" ? l : `第 ${l} 分`;
  return null;
}

function describeDayPart(p: CronParse): string {
  const domText = p.dom.star ? null : describeDayField(p.dom, "dom");
  const dowText = p.dow.star ? null : describeDayField(p.dow, "dow");
  if (!domText && !dowText) return "每天";
  // robfig 语义:日与周同时受限时为“或”。
  if (domText && dowText) return `${domText}或${dowText}`;
  return domText ?? dowText!;
}

function describeDayField(f: CronField, kind: "dom" | "dow"): string {
  const fmt = kind === "dom" ? (n: number) => `${n} 日` : (n: number) => `周${WEEKDAY_ZH[n]}`;
  const seg = f.segments[0];
  if (f.segments.length === 1 && seg?.kind === "range") {
    return kind === "dom" ? `每月 ${seg.start} 到 ${seg.end} 日` : `周${WEEKDAY_ZH[seg.start]}至周${WEEKDAY_ZH[seg.end]}`;
  }
  if (kind === "dom" && f.segments.length === 1 && seg && (seg.kind === "range-step" || seg.kind === "step")) {
    return `每月 ${seg.start} 日起每 ${seg.step} 天`;
  }
  const l = listValues(f, fmt, 8);
  if (l) return kind === "dom" ? `每月 ${l}` : l;
  return kind === "dom" ? "指定日期" : "指定星期";
}

function describeMonthPart(p: CronParse): string | null {
  if (p.month.star) return null;
  const l = listValues(p.month, (n) => `${n} 月`, 6);
  if (l) return l;
  const seg = p.month.segments[0];
  if (p.month.segments.length === 1 && seg) {
    if (seg.kind === "range" || seg.kind === "range-step") return `${seg.start} 到 ${seg.end} 月`;
    if (seg.kind === "step") return `从 ${seg.start} 月起每 ${seg.step} 个月`;
  }
  return "指定月份";
}

/** 生成一句直白的中文释义,如 “每 3 分钟执行一次”、“每周一 02:00(UTC) 执行一次”。 */
export function describeCron(expr: string): { ok: true; text: string } | { ok: false; error: string } {
  const r = parseCron(expr);
  if (!r.ok) return r;
  const p = r.parse;

  const day = describeDayPart(p);
  const month = describeMonthPart(p);
  const dayPrefix = month
    ? `${month}的${day.replace(/^每月/, "")}`
    : day;

  // 时刻/频率描述:优先“每 X”频率型,再有限时刻型(HH:MM 列表),最后短语拼接。
  const parts =
    describeFreqPart(p) ??
    renderTimes(p) ??
    ([p.hour.star ? null : describeTimePart(p.hour, "时"), describeTimePart(p.minute, "分")]
      .filter(Boolean)
      .join("") || "按计划");
  const timeText = parts;

  // “每分钟 / 每 N 分钟 / 每小时…” 这类频率描述无需“每天”前缀。
  const freqSelfContained =
    timeText.startsWith("每分钟") ||
    timeText.startsWith("每小时") ||
    /^每 \d+ (分钟|小时)/.test(timeText);
  if (freqSelfContained && dayPrefix === "每天") {
    return { ok: true, text: `${timeText}执行一次` };
  }
  if (dayPrefix === "每天") {
    return { ok: true, text: `每天 ${timeText} 执行一次` };
  }
  return { ok: true, text: `${dayPrefix} ${timeText} 执行一次` };
}

/** 频率型描述;返回 null 表示不是简单的“每 X”模式。 */
function describeFreqPart(p: CronParse): string | null {
  const m = p.minute.segments.length === 1 ? p.minute.segments[0] : null;
  const h = p.hour.segments.length === 1 ? p.hour.segments[0] : null;
  const minuteStepText = (s: Segment) => (s.start === 0 ? `每 ${s.step} 分钟` : `从第 ${s.start} 分起每 ${s.step} 分钟`);
  const hourStepText = (s: Segment) => (s.start === 0 ? `每 ${s.step} 小时` : `从 ${s.start} 点起每 ${s.step} 小时`);
  const minuteList = !p.minute.star && p.minute.values.length > 0 && p.minute.values.length <= 4
    ? p.minute.values.join("、")
    : null;

  if (p.minute.star && p.hour.star) return "每分钟";
  // 双步进:*/3 */3 * * * → 每 3 小时内的每 3 分钟
  if (m?.kind === "step" && m.start === 0 && h && (h.kind === "step" || h.kind === "range-step") && h.start === 0) {
    return `每 ${h.step} 小时内的每 ${m.step} 分钟`;
  }
  // 小时步进 + 有限分钟:0 */3 * * * → 每 3 小时的第 0 分
  if (h && (h.kind === "step" || h.kind === "range-step") && minuteList) {
    return `${hourStepText(h)}的第 ${minuteList} 分`;
  }
  // 分钟步进 + 有限小时:*/10 8-20 * * * → 8 到 20 点内每 10 分钟
  if (m?.kind === "step" && !p.hour.star) {
    const hourTxt = describeTimePart(p.hour, "时");
    if (hourTxt) return `${hourTxt}内${minuteStepText(m)}`;
  }
  // 分钟步进 + 小时通配:*/3 * * * * → 每 3 分钟
  if (m?.kind === "step" && p.hour.star) return minuteStepText(m);
  // 分钟通配 + 小时步进:* */3 * * * → 每 3 小时的每分钟
  if (p.minute.star && h && (h.kind === "step" || h.kind === "range-step")) {
    return `${hourStepText(h)}的每分钟`;
  }
  // 小时通配 + 有限分钟:0 * * * * → 每小时第 0 分
  if (p.hour.star && minuteList) return `每小时第 ${minuteList} 分`;
  return null;
}

/** 把有限的时/分组合渲染成 HH:MM 列表;无法穷举时返回 null。 */
function renderTimes(p: CronParse): string | null {
  if (p.hour.star || p.minute.star) return null;
  if (p.hour.values.length === 0 || p.minute.values.length === 0) return null;
  if (p.hour.values.length * p.minute.values.length > 6) return null;
  const times: string[] = [];
  for (const h of p.hour.values) for (const m of p.minute.values) times.push(hhmm(h, m));
  return times.join("、");
}

/**
 * 计算接下来的 n 次执行时间(UTC)。
 * 对齐 robfig SpecSchedule.Next 的匹配规则(含月末/周“或”语义)。
 * 返回值为 UTC 毫秒时间戳。
 */
export function nextCronRuns(expr: string, n: number, from: Date = new Date()): number[] | null {
  const r = parseCron(expr);
  if (!r.ok) return null;
  const p = r.parse;
  const matches = (f: CronField, v: number) => f.values.includes(v);

  const out: number[] = [];
  // 从下一分钟开始,逐天扫描;12 年上限足以覆盖 3 个闰年 2 月 29 日这类稀疏匹配。
  const start = new Date(from.getTime());
  start.setUTCSeconds(0, 0);
  const day = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth(), start.getUTCDate()));
  for (let d = 0; d < 366 * 12 && out.length < n; d++) {
    const month = day.getUTCMonth() + 1;
    const domV = day.getUTCDate();
    const dowV = day.getUTCDay();
    if (matches(p.month, month)) {
      const domMatch = matches(p.dom, domV);
      const dowMatch = matches(p.dow, dowV);
      const dayOK = p.dom.star || p.dow.star ? domMatch && dowMatch : domMatch || dowMatch;
      if (dayOK) {
        for (const h of p.hour.values) {
          for (const m of p.minute.values) {
            const t = Date.UTC(day.getUTCFullYear(), day.getUTCMonth(), day.getUTCDate(), h, m, 0, 0);
            if (t > start.getTime()) {
              out.push(t);
              if (out.length >= n) break;
            }
          }
          if (out.length >= n) break;
        }
      }
    }
    day.setUTCDate(day.getUTCDate() + 1);
  }
  return out;
}
