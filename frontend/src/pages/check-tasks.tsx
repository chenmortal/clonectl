import { useInvalidate, useList } from "@refinedev/core";
import { Loader2, Pencil, Play, Plus, Trash2, Users2 } from "lucide-react";
import * as React from "react";
import { toast } from "sonner";

import { CronField } from "@/components/shared/cron-field";
import { DataPage } from "@/components/shared/data-page";
import { DialogSection } from "@/components/shared/dialog-section";
import { InfoTip } from "@/components/shared/info-tip";
import { disableIf, permGate } from "@/components/shared/perm-gate";
import { TaskBindingsSheet } from "@/components/shared/task-bindings-sheet";
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
import { Textarea } from "@/components/ui/textarea";
import { isValidCron } from "@/lib/cron";
import { errMessage, http } from "@/lib/api";
import type {
  CheckOptions,
  CheckTask,
  DataSource,
  StorageSource,
} from "@/lib/types";
import { effectiveTaskPath, fmtDateTime } from "@/lib/utils";

const OPTION_META: { key: keyof CheckOptions; label: string; desc: string }[] = [
  {
    key: "differ",
    label: "differ 内容不一致",
    desc: "在结果中列出两端都有、但内容（哈希）不一致的文件。",
  },
  {
    key: "missingOnDst",
    label: "missingOnDst 目标缺失",
    desc: "列出源端有、目标端没有的文件。",
  },
  {
    key: "missingOnSrc",
    label: "missingOnSrc 源端缺失",
    desc: "列出目标端有、源端没有的文件。",
  },
  {
    key: "error",
    label: "error 读取错误",
    desc: "列出访问或读取失败的文件。",
  },
  {
    key: "match",
    label: "match 完全一致",
    desc: "在结果中列出两端一致的文件。仅用于核对报告，文件量大时报告会很长。",
  },
  {
    key: "oneWay",
    label: "oneWay 单向检查",
    desc: "只检查源端存在的文件：目标端多出的文件不计入差异，检查更容易通过。",
  },
  {
    key: "download",
    label: "download 下载比对",
    desc: "把文件下载到本地逐一算哈希比对。最准确、不依赖远端哈希，但非常慢且耗流量；仅当远端不支持哈希或怀疑哈希缓存不准时开启。",
  },
];

const DEFAULT_CHECK: CheckOptions = {
  differ: true,
  missingOnSrc: true,
  missingOnDst: true,
  error: true,
};

interface CheckForm {
  name: string;
  src: string;
  dst: string;
  srcPath: string;
  dstPath: string;
  cron: string;
  enabled: boolean;
  options: string;
}

export default function CheckTasks() {
  const invalidate = useInvalidate();
  const { data, isLoading } = useList<CheckTask>({
    resource: "check-tasks",
    queryOptions: { refetchInterval: 15000 },
  });
  const { data: dsList } = useList<DataSource>({ resource: "data-sources" });
  const { data: srcList } = useList<StorageSource>({
    resource: "storage-sources",
  });

  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<CheckTask | null>(null);
  const [triggering, setTriggering] = React.useState<number | null>(null);
  const [binding, setBinding] = React.useState<CheckTask | null>(null);

  const dsName = (id: number | null) =>
    id === null ? "-" : dsList?.data.find((d) => d.id === id)?.name ?? `#${id}`;
  // The path rclone actually receives: storage prefix (local) + data
  // source path + task subpath.
  const taskPath = (dsId: number | null, sub: string) => {
    const ds = dsId === null ? undefined : dsList?.data.find((d) => d.id === dsId);
    if (!ds) return sub || "/";
    const src = srcList?.data.find((s) => s.id === ds.storage_source_id);
    return effectiveTaskPath(src, ds.path, sub) || "/";
  };

  const trigger = async (t: CheckTask) => {
    setTriggering(t.id);
    try {
      const { data: c } = await http.post(`/api/check-tasks/${t.id}/trigger`);
      toast.success(`已触发（check #${c.id}）`);
      invalidate({ resource: "checks", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setTriggering(null);
    }
  };

  const doDelete = async (t: CheckTask) => {
    if (!confirm(`删除检查任务「${t.name}」及其全部记录？`)) return;
    try {
      await http.delete(`/api/check-tasks/${t.id}`);
      toast.success("已删除");
      invalidate({ resource: "check-tasks", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const rows = data?.data.map((t) => [
    <div key="n" className="space-y-0.5">
      <div className="font-medium">{t.name}</div>
      <div className="flex gap-1">
        <Badge variant={t.enabled ? "success" : "secondary"}>
          {t.enabled ? "启用" : "停用"}
        </Badge>
        <Badge variant="outline">手动</Badge>
        {t.tool_kind && t.tool_kind !== "rclone" && (
          <Badge variant="secondary">{t.tool_kind}</Badge>
        )}
        {t.cron && <Badge variant="info">cron</Badge>}
      </div>
    </div>,
    <span key="s" className="font-mono text-xs">
      {dsName(t.src_data_source_id)}
      <span className="text-muted-foreground">
        :{taskPath(t.src_data_source_id, t.src_path)}
      </span>
    </span>,
    <span key="d" className="font-mono text-xs">
      {dsName(t.dst_data_source_id)}
      <span className="text-muted-foreground">
        :{taskPath(t.dst_data_source_id, t.dst_path)}
      </span>
    </span>,
    <span key="c" className="font-mono text-xs">
      {t.cron ?? "手动"}
    </span>,
    <span key="t" className="text-xs text-muted-foreground">
      {fmtDateTime(t.updated_at)}
    </span>,
    <div key="a" className="flex justify-end gap-1">
      <Button
        variant="ghost"
        size="icon-sm"
        title="立即检查"
        disabled={triggering === t.id || permGate(t.current_user_permission, "write").disabled}
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
        {...disableIf(permGate(t.current_user_permission, "write"))}
        onClick={() => {
          if (permGate(t.current_user_permission, "write").disabled) return;
          setEditing(t);
          setOpen(true);
        }}
      >
        <Pencil />
      </Button>
      <Button
        variant="ghost"
        size="icon-sm"
        {...disableIf(permGate(t.current_user_permission, "admin"))}
        onClick={() => {
          if (permGate(t.current_user_permission, "admin").disabled) return;
          setBinding(t);
        }}
      >
        <Users2 />
      </Button>
      <Button
        variant="ghost"
        size="icon-sm"
        className="text-destructive"
        {...disableIf(permGate(t.current_user_permission, "admin"))}
        onClick={() => {
          if (permGate(t.current_user_permission, "admin").disabled) return;
          doDelete(t);
        }}
      >
        <Trash2 />
      </Button>
    </div>,
  ]);

  return (
    <>
      <DataPage
        title="检查任务"
        description="rclone operations/check 一致性检查：比对两端差异，不修改文件。"
        toolbar={
          <Button
            onClick={() => {
              setEditing(null);
              setOpen(true);
            }}
          >
            <Plus /> 新建检查任务
          </Button>
        }
        columns={[
          { key: "name", label: "任务" },
          { key: "src", label: "源" },
          { key: "dst", label: "目标" },
          { key: "cron", label: "计划" },
          { key: "updated", label: "更新时间" },
          { key: "actions", label: "", className: "text-right" },
        ]}
        rows={rows}
        getKey={(i) => data!.data[i].id}
        loading={isLoading}
      />
      <CheckDialog
        open={open}
        editing={editing}
        dataSources={dsList?.data ?? []}
        storageSources={srcList?.data ?? []}
        onClose={() => setOpen(false)}
      />
      {binding && (
        <TaskBindingsSheet
          task={binding}
          kind="check"
          onClose={() => setBinding(null)}
        />
      )}
    </>
  );
}

function CheckDialog({
  open,
  editing,
  dataSources,
  storageSources,
  onClose,
}: {
  open: boolean;
  editing: CheckTask | null;
  dataSources: DataSource[];
  storageSources: StorageSource[];
  onClose: () => void;
}) {
  const invalidate = useInvalidate();
  const [form, setForm] = React.useState<CheckForm>(emptyForm());
  const [submitting, setSubmitting] = React.useState(false);

  // Live preview of the path handed to rclone for each side.
  const rclonePath = (dsId: string, sub: string) => {
    const ds = dataSources.find((d) => String(d.id) === dsId);
    if (!ds) return null;
    const src = storageSources.find((s) => s.id === ds.storage_source_id);
    return effectiveTaskPath(src, ds.path, sub) || "/";
  };

  function emptyForm(): CheckForm {
    return {
      name: "",
      src: "",
      dst: "",
      srcPath: "",
      dstPath: "",
      cron: "",
      enabled: true,
      options: JSON.stringify(DEFAULT_CHECK, null, 0),
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
            cron: editing.cron ?? "",
            enabled: editing.enabled,
            options: JSON.stringify(editing.check_options ?? {}, null, 0),
          }
        : emptyForm(),
    );
  }, [open, editing]);

  const set = <K extends keyof CheckForm>(k: K, v: CheckForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  const toggleOption = (key: keyof CheckOptions) => {
    try {
      const opts = JSON.parse(form.options || "{}") as CheckOptions;
      opts[key] = !opts[key];
      set("options", JSON.stringify(opts, null, 0));
    } catch {
      toast.error("check 参数不是合法 JSON，无法切换开关");
    }
  };

  const submit = async () => {
    if (!form.name.trim()) return toast.error("请输入任务名称");
    if (!form.src || !form.dst) return toast.error("请选择源与目标数据源");
    if (form.cron.trim() && !isValidCron(form.cron.trim())) {
      return toast.error("cron 不合法：必须是 5 个空格分隔的字段（留空表示仅手动）");
    }
    let options: CheckOptions;
    try {
      options = JSON.parse(form.options || "{}");
    } catch {
      return toast.error("check 参数不是合法 JSON");
    }
    setSubmitting(true);
    const body = {
      name: form.name.trim(),
      src_data_source_id: Number(form.src),
      dst_data_source_id: Number(form.dst),
      src_path: form.srcPath,
      dst_path: form.dstPath,
      cron: form.cron.trim() || null,
      enabled: form.enabled,
      check_options: options,
    };
    try {
      if (editing) {
        await http.put(`/api/check-tasks/${editing.id}`, body);
        toast.success("检查任务已更新");
      } else {
        await http.post("/api/check-tasks", body);
        toast.success("检查任务已创建");
      }
      invalidate({ resource: "check-tasks", invalidates: ["list"] });
      onClose();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  let parsedOptions: CheckOptions = {};
  try {
    parsedOptions = JSON.parse(form.options || "{}");
  } catch {
    // 输入中间态允许非法 JSON，提交时统一校验
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[88vh] max-w-3xl gap-5 overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{editing ? "编辑检查任务" : "新建检查任务"}</DialogTitle>
          <DialogDescription>
            调用 rclone operations/check 比对两端差异，只报告、不修改任何文件。
          </DialogDescription>
        </DialogHeader>

        <DialogSection title="基本设置">
          <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>任务名称</Label>
              <Input
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
                placeholder="daily-check"
              />
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
                placeholder="/data"
                value={form.srcPath}
                onChange={(e) => set("srcPath", e.target.value)}
              />
              {rclonePath(form.src, form.srcPath) && (
                <p className="font-mono text-[11px] text-muted-foreground">
                  rclone 路径:{rclonePath(form.src, form.srcPath)}
                </p>
              )}
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
              {rclonePath(form.dst, form.dstPath) && (
                <p className="font-mono text-[11px] text-muted-foreground">
                  rclone 路径:{rclonePath(form.dst, form.dstPath)}
                </p>
              )}
            </div>
          </div>
        </DialogSection>

        <DialogSection
          title="执行计划"
          hint="调度时区：UTC"
        >
          <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2">
            <CronField
              label="Cron 计划"
              optional
              emptyHint="留空 = 不定时，仅手动或作为同步前检查触发"
              value={form.cron}
              onChange={(v) => set("cron", v)}
            />
            <div className="flex items-start justify-between rounded-lg border p-3">
              <div>
                <Label>启用</Label>
                <p className="text-xs text-muted-foreground">停用后不参与计划与同步前检查</p>
              </div>
              <Switch
                checked={form.enabled}
                onCheckedChange={(v) => set("enabled", v)}
              />
            </div>
          </div>
        </DialogSection>

        <DialogSection title="检查选项" hint="勾选哪些差异类型会写入检查报告">
          <div className="grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-2 lg:grid-cols-3">
            {OPTION_META.map(({ key, label, desc }) => (
              <div key={String(key)} className="flex items-center gap-1.5">
                <label className="flex flex-1 cursor-pointer items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5 accent-primary"
                    checked={Boolean(parsedOptions[key])}
                    onChange={() => toggleOption(key)}
                  />
                  <span className="truncate">{label}</span>
                </label>
                <InfoTip title={label}>{desc}</InfoTip>
              </div>
            ))}
          </div>

          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">
              其他参数（JSON，含 SUM 清单模式：checkFileHash + checkFileFs + checkFileRemote）
            </Label>
            <Textarea
              className="font-mono text-xs"
              rows={2}
              value={form.options}
              onChange={(e) => set("options", e.target.value)}
            />
            <p className="text-[11px] text-muted-foreground">
              SUM 清单模式：设置 checkFileHash + checkFileFs + checkFileRemote（源端被忽略），
              只按清单文件比对。
            </p>
          </div>
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
