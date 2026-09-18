import { useList } from "@refinedev/core";
import {
  Download,
  Loader2,
  RefreshCw,
  Save,
  SendHorizonal,
} from "lucide-react";
import * as React from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { downloadLog, errMessage, getLogTail, http } from "@/lib/api";
import type { LogTail, SystemSetting } from "@/lib/types";
import { fmtDateTime } from "@/lib/utils";
import { refreshSiteTitle } from "@/providers/site";

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB"];
  let v = n;
  let i = -1;
  do {
    v /= 1024;
    i++;
  } while (v >= 1024 && i < units.length - 1);
  return `${v.toFixed(1)} ${units[i]}`;
}

export default function Settings() {
  const { data, refetch } = useList<SystemSetting>({
    resource: "system-settings",
  });

  return (
    <div className="max-w-2xl space-y-4">
      <SiteTitleCard
        current={data?.data.find((s) => s.key === "site_title")}
        refetch={refetch}
      />
      <AlertmanagerCard
        current={data?.data.find((s) => s.key === "alertmanager_url")}
        refetch={refetch}
      />
      <LogCard />
    </div>
  );
}

function SiteTitleCard({
  current,
  refetch,
}: {
  current?: SystemSetting;
  refetch: () => void;
}) {
  const [title, setTitle] = React.useState("");
  const [dirty, setDirty] = React.useState(false);
  const [saving, setSaving] = React.useState(false);

  React.useEffect(() => {
    if (current !== undefined && !dirty) setTitle(current.value);
  }, [current, dirty]);

  const save = async () => {
    setSaving(true);
    try {
      await http.put("/api/system-settings/site_title", { value: title });
      toast.success("已保存，标题即时生效");
      setDirty(false);
      refetch();
      refreshSiteTitle();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">站点标题</CardTitle>
        <CardDescription>
          应用整体名称，显示在侧边栏、登录页与浏览器标签。留空使用默认
          clonectl，最长 100 字符。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <Label htmlFor="site-title">标题名称</Label>
            {current?.updated_at && (
              <span className="text-[11px] text-muted-foreground">
                更新于 {fmtDateTime(current.updated_at)}
              </span>
            )}
          </div>
          <Input
            id="site-title"
            placeholder="clonectl"
            value={title}
            maxLength={100}
            onChange={(e) => {
              setTitle(e.target.value);
              setDirty(true);
            }}
          />
        </div>
        <div className="flex items-center justify-between">
          <Badge variant={title.trim() ? "success" : "secondary"}>
            {title.trim() ? title.trim() : "默认标题"}
          </Badge>
          <Button onClick={save} disabled={saving || !dirty}>
            {saving ? <Loader2 className="animate-spin" /> : <Save />}
            保存
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function AlertmanagerCard({
  current,
  refetch,
}: {
  current?: SystemSetting;
  refetch: () => void;
}) {
  const [url, setUrl] = React.useState("");
  const [dirty, setDirty] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [testing, setTesting] = React.useState(false);

  React.useEffect(() => {
    if (current !== undefined && !dirty) setUrl(current.value);
  }, [current, dirty]);

  const save = async () => {
    setSaving(true);
    try {
      await http.put("/api/system-settings/alertmanager_url", { value: url });
      toast.success("已保存");
      setDirty(false);
      refetch();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const test = async () => {
    setTesting(true);
    try {
      const { data: out } = await http.post<{
        sent: boolean;
        status_code: number;
        error: string | null;
      }>("/api/system-settings/internal/alertmanager-test");
      if (out.sent) {
        toast.success(`测试告警已送达（HTTP ${out.status_code}）`);
      } else {
        toast.error(`发送失败：${out.error ?? `HTTP ${out.status_code}`}`);
      }
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setTesting(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Alertmanager 告警</CardTitle>
        <CardDescription>
          同步/检查失败时发送 Alertmanager v4 webhook；任务恢复成功后发送
          resolved。留空 = 禁用。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <Label htmlFor="am-url">Webhook URL</Label>
            {current?.updated_at && (
              <span className="text-[11px] text-muted-foreground">
                更新于 {fmtDateTime(current.updated_at)}
              </span>
            )}
          </div>
          <Input
            id="am-url"
            placeholder="http://alertmanager:9093/api/v2/alerts"
            className="font-mono text-xs"
            value={url}
            onChange={(e) => {
              setUrl(e.target.value);
              setDirty(true);
            }}
          />
          {url && !/^https?:\/\//.test(url) && (
            <p className="text-xs text-destructive">
              必须是 http(s) 开头的完整 URL
            </p>
          )}
        </div>
        <div className="flex items-center justify-between">
          {current && (
            <Badge variant={current.value ? "success" : "secondary"}>
              {current.value ? "已启用" : "未配置（禁用）"}
            </Badge>
          )}
          <div className="flex gap-2">
            <Button variant="outline" onClick={test} disabled={testing || !url}>
              {testing ? (
                <Loader2 className="animate-spin" />
              ) : (
                <SendHorizonal />
              )}
              发送测试告警
            </Button>
            <Button onClick={save} disabled={saving || !dirty}>
              {saving ? <Loader2 className="animate-spin" /> : <Save />}
              保存
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

const LOG_LINE_OPTIONS = [200, 500, 1000, 2000];

function LogCard() {
  const [lines, setLines] = React.useState(500);
  const [log, setLog] = React.useState<LogTail | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [downloading, setDownloading] = React.useState(false);
  const [auto, setAuto] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      setLog(await getLogTail(lines));
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, [lines]);

  React.useEffect(() => {
    load();
  }, [load]);

  React.useEffect(() => {
    if (!auto) return;
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [auto, load]);

  const download = async () => {
    setDownloading(true);
    try {
      await downloadLog();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setDownloading(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">运行日志</CardTitle>
        <CardDescription>
          rclone rcd 子进程日志（rcd.log），同步/检查的全部输出都在这里；可下载
          完整文件排查问题。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <Select
            value={String(lines)}
            onValueChange={(v) => setLines(Number(v))}
          >
            <SelectTrigger className="w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {LOG_LINE_OPTIONS.map((n) => (
                <SelectItem key={n} value={String(n)}>
                  尾部 {n} 行
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex items-center gap-2">
            <Switch checked={auto} onCheckedChange={setAuto} id="log-auto" />
            <Label htmlFor="log-auto" className="text-xs text-muted-foreground">
              5 秒自动刷新
            </Label>
          </div>
          <div className="ml-auto flex gap-2">
            <Button variant="outline" onClick={download} disabled={downloading || !log?.exists}>
              {downloading ? (
                <Loader2 className="animate-spin" />
              ) : (
                <Download />
              )}
              下载完整日志
            </Button>
            <Button variant="outline" onClick={load} disabled={loading}>
              {loading ? (
                <Loader2 className="animate-spin" />
              ) : (
                <RefreshCw />
              )}
              刷新
            </Button>
          </div>
        </div>
        {log && !log.exists ? (
          <p className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
            日志文件不存在——外部 rcd 模式或托管 rcd 尚未启动过。
          </p>
        ) : (
          <>
            <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
              {log?.truncated && (
                <Badge variant="secondary">内容过长，仅保留尾部 1MB</Badge>
              )}
              {log && (
                <span>
                  {fmtBytes(log.size)}
                  {log.mod_time && ` · 更新于 ${fmtDateTime(log.mod_time)}`}
                </span>
              )}
            </div>
            <pre className="max-h-96 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs leading-relaxed">
              {log?.content || "（暂无日志）"}
            </pre>
          </>
        )}
      </CardContent>
    </Card>
  );
}
