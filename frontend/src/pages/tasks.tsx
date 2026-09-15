import { useInvalidate, useList } from "@refinedev/core";
import { Loader2, Pencil, Play, Plus, Trash2 } from "lucide-react";
import * as React from "react";
import { toast } from "sonner";

import { CronField } from "@/components/shared/cron-field";
import { DataPage } from "@/components/shared/data-page";
import { DialogSection } from "@/components/shared/dialog-section";
import { RcloneOptionsField } from "@/components/shared/rclone-options-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import { isValidCron } from "@/lib/cron";
import { errMessage, http } from "@/lib/api";
import type { CheckTask, DataSource, SyncTask } from "@/lib/types";
import { fmtDateTime } from "@/lib/utils";

interface TaskForm {
  name: string;
  src: string;
  dst: string;
  srcPath: string;
  dstPath: string;
  mode: "sync" | "copy";
  cron: string;
  enabled: boolean;
  options: string;
  preCheck: string;
}

export default function Tasks() {
  const invalidate = useInvalidate();
  const { data, isLoading } = useList<SyncTask>({
    resource: "tasks",
    queryOptions: { refetchInterval: 15000 },
  });
  const { data: dsList } = useList<DataSource>({ resource: "data-sources" });
  const { data: checks } = useList<CheckTask>({ resource: "check-tasks" });

  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<SyncTask | null>(null);
  const [triggering, setTriggering] = React.useState<number | null>(null);

  const dsName = (id: number | null) =>
    id === null ? "-" : dsList?.data.find((d) => d.id === id)?.name ?? `#${id}`;
  const checkName = (id: number | null) =>
    id === null ? null : checks?.data.find((c) => c.id === id)?.name ?? `#${id}`;

  const trigger = async (t: SyncTask) => {
    setTriggering(t.id);
    try {
      const { data: run } = await http.post(`/api/tasks/${t.id}/trigger`);
      toast.success(
        run.status === "skipped"
          ? `已跳过：${run.error ?? ""}`
          : `已触发（run #${run.id}）`,
      );
      invalidate({ resource: "runs", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setTriggering(null);
    }
  };

  const doDelete = async (t: SyncTask) => {
    if (!confirm(`删除任务「${t.name}」及其全部运行记录？`)) return;
    try {
      await http.delete(`/api/tasks/${t.id}`);
      toast.success("已删除");
      invalidate({ resource: "tasks", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const rows = data?.data.map((t) => {
    const pre = checkName(t.pre_check_task_id);
    return [
      <div key="n" className="space-y-0.5">
        <div className="font-medium">{t.name}</div>
        <div className="flex items-center gap-1">
          <Badge variant={t.enabled ? "success" : "secondary"}>
            {t.enabled ? "启用" : "停用"}
          </Badge>
          <Badge variant="outline">{t.mode}</Badge>
          {pre && <Badge variant="info">前检：{pre}</Badge>}
        </div>
      </div>,
      <span key="s" className="font-mono text-xs">
        {dsName(t.src_data_source_id)}
        <span className="text-muted-foreground">:{t.src_path || "/"}</span>
      </span>,
      <span key="d" className="font-mono text-xs">
        {dsName(t.dst_data_source_id)}
        <span className="text-muted-foreground">:{t.dst_path || "/"}</span>
      </span>,
      <span key="c" className="font-mono text-xs">
        {t.cron}
      </span>,
      <span key="t" className="text-xs text-muted-foreground">
        {fmtDateTime(t.updated_at)}
      </span>,
      <div key="a" className="flex justify-end gap-1">
        <Button
          variant="ghost"
          size="icon-sm"
          title="立即运行"
          disabled={triggering === t.id}
          onClick={() => trigger(t)}
        >
          {triggering === t.id ? (
            <Loader2 className="animate-spin" />
          ) : (
            <Play className="text-emerald-600 dark:text-emerald-400" />
          )}
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          title="编辑"
          onClick={() => {
            setEditing(t);
            setOpen(true);
          }}
        >
          <Pencil />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          title="删除"
          className="text-destructive"
          onClick={() => doDelete(t)}
        >
          <Trash2 />
        </Button>
      </div>,
    ];
  });

  return (
    <>
      <DataPage
        title="同步任务"
        description="按 cron 计划在两个数据源之间执行 rclone sync/copy。"
        toolbar={
          <Button
            onClick={() => {
              setEditing(null);
              setOpen(true);
            }}
          >
            <Plus /> 新建任务
          </Button>
        }
        columns={[
          { key: "name", label: "任务" },
          { key: "src", label: "源" },
          { key: "dst", label: "目标" },
          { key: "cron", label: "Cron" },
          { key: "updated", label: "更新时间" },
          { key: "actions", label: "", className: "text-right" },
        ]}
        rows={rows}
        getKey={(i) => data!.data[i].id}
        loading={isLoading}
      />
      <TaskDialog
        open={open}
        editing={editing}
        dataSources={dsList?.data ?? []}
        checkTasks={checks?.data ?? []}
        onClose={() => setOpen(false)}
      />
    </>
  );
}

function TaskDialog({
  open,
  editing,
  dataSources,
  checkTasks,
  onClose,
}: {
  open: boolean;
  editing: SyncTask | null;
  dataSources: DataSource[];
  checkTasks: CheckTask[];
  onClose: () => void;
}) {
  const invalidate = useInvalidate();
  const [form, setForm] = React.useState<TaskForm>(emptyForm());
  const [submitting, setSubmitting] = React.useState(false);

  function emptyForm(): TaskForm {
    return {
      name: "",
      src: "",
      dst: "",
      srcPath: "",
      dstPath: "",
      mode: "sync",
      cron: "0 3 * * *",
      enabled: true,
      options: "{}",
      preCheck: "",
    };
  }

  React.useEffect(() => {
    if (!open) return;
    setForm(
      editing
        ? {
            name: editing.name,
            src: String(editing.src_data_source_id ?? ""),
            dst: String(editing.dst_data_source_id ?? ""),
            srcPath: editing.src_path,
            dstPath: editing.dst_path,
            mode: editing.mode,
            cron: editing.cron,
            enabled: editing.enabled,
            options: JSON.stringify(editing.rclone_options ?? {}, null, 0),
            preCheck: editing.pre_check_task_id ? String(editing.pre_check_task_id) : "",
          }
        : emptyForm(),
    );
  }, [open, editing]);

  const set = <K extends keyof TaskForm>(k: K, v: TaskForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  const submit = async () => {
    if (!form.name.trim()) return toast.error("请输入任务名称");
    if (!form.src || !form.dst) return toast.error("请选择源与目标数据源");
    if (!isValidCron(form.cron))
      return toast.error("cron 不合法：必须是 5 个空格分隔的字段（分 时 日 月 周）");
    let options: Record<string, unknown> = {};
    try {
      options = JSON.parse(form.options || "{}");
    } catch {
      return toast.error("rclone 参数不是合法 JSON");
    }
    setSubmitting(true);
    const body = {
      name: form.name.trim(),
      src_data_source_id: Number(form.src),
      dst_data_source_id: Number(form.dst),
      src_path: form.srcPath,
      dst_path: form.dstPath,
      mode: form.mode,
      cron: form.cron.trim(),
      enabled: form.enabled,
      rclone_options: options,
      pre_check_task_id: form.preCheck ? Number(form.preCheck) : null,
    };
    try {
      if (editing) {
        await http.put(`/api/tasks/${editing.id}`, body);
        toast.success("任务已更新");
      } else {
        await http.post("/api/tasks", body);
        toast.success("任务已创建");
      }
      invalidate({ resource: "tasks", invalidates: ["list"] });
      onClose();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[88vh] max-w-3xl gap-5 overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{editing ? "编辑同步任务" : "新建同步任务"}</DialogTitle>
          <DialogDescription>
            按 cron 计划在两个数据源之间执行 rclone sync/copy，计划时间按 UTC 计算。
          </DialogDescription>
        </DialogHeader>

        <DialogSection title="基本设置">
          <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>任务名称</Label>
              <Input
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
                placeholder="nightly-backup"
              />
            </div>
            <div className="space-y-1.5">
              <Label>模式</Label>
              <Select
                value={form.mode}
                onValueChange={(v) => set("mode", v as "sync" | "copy")}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="sync">sync（镜像，删除多余文件）</SelectItem>
                  <SelectItem value="copy">copy（仅复制）</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>源数据源</Label>
              <Select value={form.src} onValueChange={(v) => set("src", v)}>
                <SelectTrigger>
                  <SelectValue placeholder="选择…" />
                </SelectTrigger>
                <SelectContent>
                  {dataSources.map((d) => (
                    <SelectItem key={d.id} value={String(d.id)}>
                      {d.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>源子路径</Label>
              <Input
                placeholder="/data（数据源路径下的子路径）"
                value={form.srcPath}
                onChange={(e) => set("srcPath", e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>目标数据源</Label>
              <Select value={form.dst} onValueChange={(v) => set("dst", v)}>
                <SelectTrigger>
                  <SelectValue placeholder="选择…" />
                </SelectTrigger>
                <SelectContent>
                  {dataSources.map((d) => (
                    <SelectItem key={d.id} value={String(d.id)}>
                      {d.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>目标子路径</Label>
              <Input
                placeholder="/data"
                value={form.dstPath}
                onChange={(e) => set("dstPath", e.target.value)}
              />
            </div>
          </div>
        </DialogSection>

        <DialogSection title="执行计划" hint="调度时区：UTC">
          <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2">
            <CronField value={form.cron} onChange={(v) => set("cron", v)} />
            <div className="space-y-4">
              <div className="flex items-center justify-between rounded-lg border p-3">
                <div>
                  <Label>启用</Label>
                  <p className="text-xs text-muted-foreground">停用后不再按计划执行</p>
                </div>
                <Switch
                  checked={form.enabled}
                  onCheckedChange={(v) => set("enabled", v)}
                />
              </div>
              <div className="space-y-1.5">
                <Label>同步前一致性检查</Label>
                <Select value={form.preCheck} onValueChange={(v) => set("preCheck", v)}>
                  <SelectTrigger>
                    <SelectValue placeholder="不绑定" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="">不绑定</SelectItem>
                    {checkTasks.map((c) => (
                      <SelectItem key={c.id} value={String(c.id)}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <p className="text-[11px] text-muted-foreground">
                  两端一致跳过同步；发现差异继续；检查出错阻止同步
                </p>
              </div>
            </div>
          </div>
        </DialogSection>

        <DialogSection
          title="rclone 参数（可选）"
          hint="键名与 rclone 文档一致，后端按 _config/_filter 传递"
        >
          <RcloneOptionsField
            value={form.options}
            onChange={(v) => set("options", v)}
          />
        </DialogSection>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting && <Loader2 className="animate-spin" />}
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
