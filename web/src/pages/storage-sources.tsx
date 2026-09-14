import { useInvalidate, useList } from "@refinedev/core";
import { Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import * as React from "react";
import { toast } from "sonner";

import { DataPage } from "@/components/shared/data-page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
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
import { Textarea } from "@/components/ui/textarea";
import { errMessage, http } from "@/lib/api";
import type { StorageSource } from "@/lib/types";
import { fmtDateTime } from "@/lib/utils";

const EXTRA_FIELDS: Record<string, { label: string; placeholder?: string }[]> = {
  s3: [{ label: "Provider", placeholder: "AWS / Minio / Alibaba / Tencent / Other" }],
  cos: [{ label: "Region", placeholder: "ap-guangzhou" }],
  gcs: [{ label: "Project Number" }],
  azureblob: [{ label: "Storage Account" }],
  b2: [{ label: "Account ID" }],
  swift: [{ label: "Auth URL" }],
};

const NAME_RE = /^[a-zA-Z0-9_-]+$/;

export default function StorageSources() {
  const invalidate = useInvalidate();
  const { data, isLoading } = useList<StorageSource>({
    resource: "storage-sources",
  });
  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<StorageSource | null>(null);

  const doDelete = async (s: StorageSource) => {
    if (!confirm(`删除存储源「${s.name}」？`)) return;
    try {
      await http.delete(`/api/storage-sources/${s.id}`);
      toast.success("已删除");
      invalidate({ resource: "storage-sources", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const rows = data?.data.map((s) => [
    <div key="n" className="font-medium">{s.name}</div>,
    <Badge key="t" variant="secondary">{s.type}</Badge>,
    <span key="e" className="font-mono text-xs">{s.endpoint ?? "-"}</span>,
    <span key="r" className="text-xs">{s.region ?? "-"}</span>,
    <span key="t" className="text-xs text-muted-foreground">
      {fmtDateTime(s.updated_at)}
    </span>,
    <div key="a" className="flex justify-end gap-1">
      <Button
        variant="ghost"
        size="icon-sm"
        title="编辑"
        onClick={() => {
          setEditing(s);
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
        onClick={() => doDelete(s)}
      >
        <Trash2 />
      </Button>
    </div>,
  ]);

  return (
    <>
      <DataPage
        title="存储源"
        description="云厂商/后端模板：endpoint、region 与非敏感参数。凭据存放在数据源上。"
        toolbar={
          <Button
            onClick={() => {
              setEditing(null);
              setOpen(true);
            }}
          >
            <Plus /> 新建存储源
          </Button>
        }
        columns={[
          { key: "name", label: "名称" },
          { key: "type", label: "类型" },
          { key: "endpoint", label: "Endpoint" },
          { key: "region", label: "Region" },
          { key: "updated", label: "更新时间" },
          { key: "actions", label: "", className: "text-right" },
        ]}
        rows={rows}
        getKey={(i) => data!.data[i].id}
        loading={isLoading}
      />
      <SourceDialog
        open={open}
        editing={editing}
        onClose={() => setOpen(false)}
      />
    </>
  );
}

function SourceDialog({
  open,
  editing,
  onClose,
}: {
  open: boolean;
  editing: StorageSource | null;
  onClose: () => void;
}) {
  const invalidate = useInvalidate();
  const [name, setName] = React.useState("");
  const [type, setType] = React.useState("s3");
  const [endpoint, setEndpoint] = React.useState("");
  const [region, setRegion] = React.useState("");
  const [extra, setExtra] = React.useState("{}");
  const [submitting, setSubmitting] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setName(editing?.name ?? "");
    setType(editing?.type ?? "s3");
    setEndpoint(editing?.endpoint ?? "");
    setRegion(editing?.region ?? "");
    setExtra(
      editing && Object.keys(editing.extra).length
        ? JSON.stringify(editing.extra, null, 2)
        : "{}",
    );
  }, [open, editing]);

  const submit = async () => {
    if (!NAME_RE.test(name)) {
      toast.error("名称只能包含字母、数字、下划线和中划线");
      return;
    }
    let extraObj: Record<string, unknown> = {};
    try {
      extraObj = JSON.parse(extra || "{}");
    } catch {
      toast.error("高级参数不是合法 JSON");
      return;
    }
    setSubmitting(true);
    const body = {
      name: name.trim(),
      type,
      endpoint: endpoint || null,
      region: region || null,
      extra: extraObj,
    };
    try {
      if (editing) {
        await http.put(`/api/storage-sources/${editing.id}`, body);
        toast.success("存储源已更新");
      } else {
        await http.post("/api/storage-sources", body);
        toast.success("存储源已创建");
      }
      invalidate({ resource: "storage-sources", invalidates: ["list"] });
      onClose();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? "编辑存储源" : "新建存储源"}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>名称</Label>
              <Input
                placeholder="minio-main"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">
                字母/数字/_/- 组合
              </p>
            </div>
            <div className="space-y-1.5">
              <Label>类型</Label>
              <Select value={type} onValueChange={setType}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {Object.keys(EXTRA_FIELDS).map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>Endpoint</Label>
              <Input
                placeholder="http://127.0.0.1:9000"
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Region</Label>
              <Input
                placeholder="可选"
                value={region}
                onChange={(e) => setRegion(e.target.value)}
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>高级参数（JSON，写入 extra）</Label>
            <Textarea
              className="font-mono text-xs"
              rows={4}
              value={extra}
              onChange={(e) => setExtra(e.target.value)}
            />
            {(EXTRA_FIELDS[type] ?? []).length > 0 && (
              <p className="text-[11px] text-muted-foreground">
                {EXTRA_FIELDS[type].map((f) => f.label).join(" / ")} 等字段写在这里
              </p>
            )}
          </div>
        </div>
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
