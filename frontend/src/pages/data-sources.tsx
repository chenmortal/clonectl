import { useInvalidate, useList } from "@refinedev/core";
import {
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  ShieldCheck,
  Trash2,
  Users2,
} from "lucide-react";
import * as React from "react";
import { toast } from "sonner";
import { DataPage, StatusDot } from "@/components/shared/data-page";
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
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { errMessage, http } from "@/lib/api";
import type {
  DataSource,
  DataSourceBinding,
  StorageSource,
  UserOut,
} from "@/lib/types";
import { effectiveDSPath, fmtDateTime } from "@/lib/utils";

export default function DataSources() {
  const invalidate = useInvalidate();
  const { data, isLoading } = useList<DataSource>({
    resource: "data-sources",
    queryOptions: { refetchInterval: 15000 },
  });
  const { data: sources } = useList<StorageSource>({
    resource: "storage-sources",
  });

  const [dialogOpen, setDialogOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<DataSource | null>(null);
  const [binding, setBinding] = React.useState<DataSource | null>(null);
  const [verifying, setVerifying] = React.useState<number | null>(null);

  const openCreate = () => {
    setEditing(null);
    setDialogOpen(true);
  };
  const openEdit = (ds: DataSource) => {
    setEditing(ds);
    setDialogOpen(true);
  };

  const verify = async (ds: DataSource) => {
    setVerifying(ds.id);
    try {
      const { data: v } = await http.post(`/api/data-sources/${ds.id}/verify`);
      if (v.read_ok && v.write_ok && !v.error) {
        toast.success(`${ds.name}：读写验证通过`);
      } else {
        toast.error(`${ds.name}：${v.error ?? "验证失败"}`);
      }
      invalidate({ resource: "data-sources", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setVerifying(null);
    }
  };

  const doDelete = async (ds: DataSource) => {
    if (!confirm(`删除数据源「${ds.name}」？`)) return;
    try {
      await http.delete(`/api/data-sources/${ds.id}`);
      toast.success("已删除");
      invalidate({ resource: "data-sources", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const sourceOf = (id: number) => sources?.data.find((s) => s.id === id);
  const rows = data?.data.map((ds) => {
    const src = sourceOf(ds.storage_source_id);
    return [
      <div key="n" className="space-y-0.5">
        <div className="font-medium">{ds.name}</div>
        {ds.description && (
          <div className="text-xs text-muted-foreground">{ds.description}</div>
        )}
      </div>,
      <Badge key="s" variant="outline">
        {src ? `${src.name} · ${src.type}` : `#${ds.storage_source_id}`}
      </Badge>,
      <div key="p" className="space-y-0.5 font-mono text-xs">
        <div>{effectiveDSPath(src, ds.path)}</div>
        {src?.type === "local" && (
          <div className="text-[11px] text-muted-foreground">
            存储源内相对路径：{ds.path}
          </div>
        )}
      </div>,
      ds.last_verified_ok === null ? (
        <span className="text-xs text-muted-foreground">未验证</span>
      ) : ds.last_verified_ok ? (
        <StatusDot tone="success">通过</StatusDot>
      ) : (
        <StatusDot tone="failed">失败</StatusDot>
      ),
      <span key="t" className="text-xs text-muted-foreground">
        {fmtDateTime(ds.updated_at)}
      </span>,
      <div key="a" className="flex justify-end gap-1">
        <Button
          variant="ghost"
          size="icon-sm"
          title="验证读写"
          disabled={verifying === ds.id}
          onClick={() => verify(ds)}
        >
          {verifying === ds.id ? (
            <Loader2 className="animate-spin" />
          ) : (
            <ShieldCheck className="text-emerald-600 dark:text-emerald-400" />
          )}
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          title="权限绑定"
          onClick={() => setBinding(ds)}
        >
          <Users2 />
        </Button>
        <Button variant="ghost" size="icon-sm" title="编辑" onClick={() => openEdit(ds)}>
          <Pencil />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          title="删除"
          className="text-destructive"
          onClick={() => doDelete(ds)}
        >
          <Trash2 />
        </Button>
      </div>,
    ];
  });

  return (
    <>
      <DataPage
        title="数据源"
        description="数据源 = 存储源上的具体 bucket/路径 + 凭据；同步与检查任务按数据源引用。"
        toolbar={
          <Button onClick={openCreate}>
            <Plus /> 新建数据源
          </Button>
        }
        columns={[
          { key: "name", label: "名称" },
          { key: "source", label: "存储源" },
          { key: "path", label: "路径" },
          { key: "verify", label: "验证" },
          { key: "updated", label: "更新时间" },
          { key: "actions", label: "", className: "text-right" },
        ]}
        rows={rows}
        getKey={(i) => data!.data[i].id}
        loading={isLoading}
      />

      <DataSourceDialog
        open={dialogOpen}
        editing={editing}
        sources={sources?.data ?? []}
        onClose={() => setDialogOpen(false)}
      />

      {binding && <BindingsSheet ds={binding} onClose={() => setBinding(null)} />}
    </>
  );
}

function DataSourceDialog({
  open,
  editing,
  sources,
  onClose,
}: {
  open: boolean;
  editing: DataSource | null;
  sources: StorageSource[];
  onClose: () => void;
}) {
  const invalidate = useInvalidate();
  const [sourceId, setSourceId] = React.useState("");
  const [name, setName] = React.useState("");
  const [path, setPath] = React.useState("");
  const [ak, setAk] = React.useState("");
  const [sk, setSk] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setSourceId(editing ? String(editing.storage_source_id) : "");
    setName(editing?.name ?? "");
    setPath(editing?.path ?? "");
    setAk(editing?.access_key_id ?? "");
    setSk(editing?.secret_access_key ?? "");
    setDescription(editing?.description ?? "");
  }, [open, editing]);

  const selected = sources.find((s) => String(s.id) === sourceId);
  const needsCreds = selected ? selected.type !== "local" : true;
  const nameError = name.trim() ? "" : "请输入名称";
  const pathError = path.trim() ? "" : "请输入路径";

  const submit = async () => {
    if (nameError || pathError || !sourceId) {
      toast.error(!sourceId ? "请选择存储源" : nameError || pathError);
      return;
    }
    setSubmitting(true);
    const body: Record<string, unknown> = {
      name: name.trim(),
      storage_source_id: Number(sourceId),
      path: path.trim(),
      description: description || null,
    };
    if (needsCreds || ak || sk) {
      body.access_key_id = ak || null;
      body.secret_access_key = sk || null;
    }
    try {
      if (editing) {
        await http.put(`/api/data-sources/${editing.id}`, body);
        toast.success("数据源已更新");
      } else {
        await http.post("/api/data-sources", body);
        toast.success("数据源已创建");
      }
      invalidate({ resource: "data-sources", invalidates: ["list"] });
      onClose();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{editing ? "编辑数据源" : "新建数据源"}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>名称</Label>
              <Input
                placeholder="my-bucket"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>存储源</Label>
              <Select value={sourceId} onValueChange={setSourceId}>
                <SelectTrigger>
                  <SelectValue placeholder="选择存储源…" />
                </SelectTrigger>
                <SelectContent>
                  {sources.map((s) => (
                    <SelectItem key={s.id} value={String(s.id)}>
                      {s.name}（{s.type}）
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="space-y-1.5">
            <Label>路径</Label>
            <Input
              placeholder="/bucket 或 /bucket/prefix"
              value={path}
              onChange={(e) => setPath(e.target.value)}
            />
            {selected && (
              <p className="font-mono text-[11px] text-muted-foreground">
                实际传给 rclone：{effectiveDSPath(selected, path) || "/"}
              </p>
            )}
          </div>

          {needsCreds && (
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label className="gap-1">
                  <KeyRound className="h-3.5 w-3.5" /> Access Key ID
                </Label>
                <Input
                  autoComplete="off"
                  value={ak}
                  onChange={(e) => setAk(e.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label className="gap-1">
                  <KeyRound className="h-3.5 w-3.5" /> Secret Access Key
                </Label>
                <Input
                  type="password"
                  autoComplete="new-password"
                  value={sk}
                  onChange={(e) => setSk(e.target.value)}
                />
              </div>
            </div>
          )}

          <div className="space-y-1.5">
            <Label>描述</Label>
            <Input
              placeholder="可选"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
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

const PERM_LABEL: Record<string, string> = {
  read: "read 可查看",
  write: "write 可编辑",
  admin: "admin 全权",
};

function BindingsSheet({ ds, onClose }: { ds: DataSource; onClose: () => void }) {
  const [bindings, setBindings] = React.useState<DataSourceBinding[] | null>(null);
  const [userId, setUserId] = React.useState("");
  const [permission, setPermission] = React.useState("read");
  const { data: users } = useList<UserOut>({ resource: "users" });

  const load = React.useCallback(() => {
    http
      .get<DataSourceBinding[]>(`/api/data-sources/${ds.id}/bindings`)
      .then((r) => setBindings(r.data))
      .catch((e) => toast.error(errMessage(e)));
  }, [ds.id]);

  React.useEffect(load, [load]);

  const add = async () => {
    try {
      await http.post(`/api/data-sources/${ds.id}/bindings`, {
        user_id: Number(userId),
        permission,
      });
      toast.success("绑定已添加");
      setUserId("");
      load();
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const update = async (b: DataSourceBinding, permission: string) => {
    try {
      await http.put(`/api/data-sources/${ds.id}/bindings/${b.id}`, { permission });
      load();
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const remove = async (b: DataSourceBinding) => {
    try {
      await http.delete(`/api/data-sources/${ds.id}/bindings/${b.id}`);
      toast.success("绑定已移除");
      load();
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const nameOf = (id: number) =>
    users?.data.find((u) => u.id === id)?.username ?? `#${id}`;

  return (
    <Sheet open onOpenChange={(o) => !o && onClose()}>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>权限绑定：{ds.name}</SheetTitle>
          <SheetDescription>
            read 可查看；write 可编辑与验证；admin 可管理绑定与删除。owner 与
            admin 天然拥有全部权限。
          </SheetDescription>
        </SheetHeader>

        <div className="mt-6 space-y-6">
          <div className="flex items-end gap-2">
            <div className="flex-1 space-y-1.5">
              <Label>用户</Label>
              <Select value={userId} onValueChange={setUserId}>
                <SelectTrigger>
                  <SelectValue placeholder="选择用户…" />
                </SelectTrigger>
                <SelectContent>
                  {users?.data.map((u) => (
                    <SelectItem key={u.id} value={String(u.id)}>
                      {u.username}（{u.role}）
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="w-32 space-y-1.5">
              <Label>权限</Label>
              <Select value={permission} onValueChange={setPermission}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {Object.entries(PERM_LABEL).map(([v, label]) => (
                    <SelectItem key={v} value={v}>
                      {label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button onClick={add} disabled={!userId}>
              添加
            </Button>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>用户</TableHead>
                <TableHead>权限</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {!bindings ? (
                <TableRow>
                  <TableCell colSpan={3} className="text-center">
                    <Loader2 className="mx-auto h-4 w-4 animate-spin text-muted-foreground" />
                  </TableCell>
                </TableRow>
              ) : bindings.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={3}
                    className="text-center text-muted-foreground"
                  >
                    暂无绑定
                  </TableCell>
                </TableRow>
              ) : (
                bindings.map((b) => (
                  <TableRow key={b.id}>
                    <TableCell>{nameOf(b.user_id)}</TableCell>
                    <TableCell>
                      <Select value={b.permission} onValueChange={(v) => update(b, v)}>
                        <SelectTrigger className="h-8 w-28">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {Object.entries(PERM_LABEL).map(([v, label]) => (
                            <SelectItem key={v} value={v}>
                              {label}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-destructive"
                        onClick={() => remove(b)}
                      >
                        <Trash2 />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </SheetContent>
    </Sheet>
  );
}
