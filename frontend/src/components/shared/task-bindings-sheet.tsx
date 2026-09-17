import { Loader2, Trash2 } from "lucide-react";
import * as React from "react";
import { toast } from "sonner";

import { http, errMessage } from "@/lib/api";
import type {
  CheckTask,
  CheckTaskBinding,
  SyncTask,
  SyncTaskBinding,
  TaskPermission,
  UserOut,
} from "@/lib/types";

import { Button } from "@/components/ui/button";
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
import { useList } from "@refinedev/core";

const PERM_LABEL: Record<TaskPermission, string> = {
  read: "read 可查看",
  write: "write 可编辑",
  admin: "admin 全权",
};

export type TaskKind = "sync" | "check";

interface BaseTask {
  id: number;
  name: string;
}

export function TaskBindingsSheet({
  task,
  kind,
  onClose,
}: {
  task: BaseTask;
  kind: TaskKind;
  onClose: () => void;
}) {
  const [bindings, setBindings] = React.useState<
    (SyncTaskBinding | CheckTaskBinding)[] | null
  >(null);
  const [userId, setUserId] = React.useState("");
  const [permission, setPermission] = React.useState<TaskPermission>("read");
  const { data: users } = useList<UserOut>({ resource: "users" });

  const url = (suffix = "") =>
    kind === "sync"
      ? `/api/tasks/${task.id}/bindings${suffix}`
      : `/api/check-tasks/${task.id}/bindings${suffix}`;

  const load = React.useCallback(() => {
    http
      .get(url())
      .then((r) => setBindings(r.data))
      .catch((e) => toast.error(errMessage(e)));
  }, [task.id, kind]);

  React.useEffect(load, [load]);

  const add = async () => {
    if (!userId) return;
    try {
      await http.post(url(), { user_id: Number(userId), permission });
      toast.success("绑定已添加");
      setUserId("");
      load();
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const update = async (b: SyncTaskBinding | CheckTaskBinding, p: TaskPermission) => {
    try {
      await http.put(url(`/${b.id}`), { permission: p });
      load();
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const remove = async (b: SyncTaskBinding | CheckTaskBinding) => {
    try {
      await http.delete(url(`/${b.id}`));
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
          <SheetTitle>权限绑定：{task.name}</SheetTitle>
          <SheetDescription>
            read 可查看运行记录；write 可编辑任务、触发运行；admin 可管理绑定与删除任务。
            创建者与全局 admin 天然拥有全部权限。
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
              <Select value={permission} onValueChange={(v) => setPermission(v as TaskPermission)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(Object.keys(PERM_LABEL) as TaskPermission[]).map((v) => (
                    <SelectItem key={v} value={v}>
                      {PERM_LABEL[v]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button onClick={add} disabled={!userId}>添加</Button>
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
                      <Select
                        value={b.permission}
                        onValueChange={(v) => update(b, v as TaskPermission)}
                      >
                        <SelectTrigger className="h-8 w-28">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {(Object.keys(PERM_LABEL) as TaskPermission[]).map((v) => (
                            <SelectItem key={v} value={v}>
                              {PERM_LABEL[v]}
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
