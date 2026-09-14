import { useList } from "@refinedev/core";
import { Loader2, Save, SendHorizonal } from "lucide-react";
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
import { errMessage, http } from "@/lib/api";
import type { SystemSetting } from "@/lib/types";
import { fmtDateTime } from "@/lib/utils";

export default function Settings() {
  const { data, refetch } = useList<SystemSetting>({
    resource: "system-settings",
  });

  const current = data?.data.find((s) => s.key === "alertmanager_url");
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
    <div className="max-w-2xl space-y-4">
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
    </div>
  );
}
