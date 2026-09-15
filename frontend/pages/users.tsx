import { useGetIdentity, useInvalidate, useList } from "@refinedev/core";
import { KeyRound, Loader2, Plus, Trash2, UserCog } from "lucide-react";
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
import { Switch } from "@/components/ui/switch";
import { errMessage, http } from "@/lib/api";
import type { UserOut } from "@/lib/types";
import type { Identity } from "@/providers/auth";
import { fmtDateTime } from "@/lib/utils";

const ROLES = ["admin", "edit", "view"] as const;

export default function Users() {
  const invalidate = useInvalidate();
  const { data: me } = useGetIdentity<Identity>();
  const { data, isLoading } = useList<UserOut>({ resource: "users" });

  const [createOpen, setCreateOpen] = React.useState(false);
  const [resetFor, setResetFor] = React.useState<UserOut | null>(null);

  const setDisabled = async (u: UserOut, disabled: boolean) => {
    try {
      await http.put(`/api/users/${u.id}`, { disabled });
      toast.success(disabled ? `已禁用 ${u.username}` : `已启用 ${u.username}`);
      invalidate({ resource: "users", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const doDelete = async (u: UserOut) => {
    if (!confirm(`删除用户「${u.username}」？`)) return;
    try {
      await http.delete(`/api/users/${u.id}`);
      toast.success("已删除");
      invalidate({ resource: "users", invalidates: ["list"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const rows = data?.data.map((u) => [
    <div key="n" className="space-y-0.5">
      <div className="flex items-center gap-2 font-medium">
        {u.username}
        {u.id === me?.id && <Badge variant="outline">当前</Badge>}
      </div>
      {u.disabled_at && <Badge variant="destructive">已禁用</Badge>}
    </div>,
    <Badge key="r" variant="secondary">
      {u.role}
    </Badge>,
    <span key="c" className="text-xs text-muted-foreground">
      {fmtDateTime(u.created_at)}
    </span>,
    <span key="l" className="text-xs text-muted-foreground">
      {fmtDateTime(u.last_login_at)}
    </span>,
    <div key="s" className="flex items-center gap-2">
      <Switch
        checked={!u.disabled_at}
        disabled={u.id === me?.id}
        onCheckedChange={(v) => setDisabled(u, !v)}
      />
    </div>,
    <div key="a" className="flex justify-end gap-1">
      <Button
        variant="ghost"
        size="icon-sm"
        title="重置密码"
        onClick={() => setResetFor(u)}
      >
        <KeyRound />
      </Button>
      <Button
        variant="ghost"
        size="icon-sm"
        title="删除"
        className="text-destructive"
        disabled={u.id === me?.id}
        onClick={() => doDelete(u)}
      >
        <Trash2 />
      </Button>
    </div>,
  ]);

  return (
    <>
      <DataPage
        title="用户管理"
        description="三角色：admin 全权；edit 可创建/触发任务；view 只读。"
        toolbar={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus /> 新建用户
          </Button>
        }
        columns={[
          { key: "username", label: "用户" },
          { key: "role", label: "角色" },
          { key: "created", label: "创建时间" },
          { key: "last", label: "最后登录" },
          { key: "enabled", label: "启用" },
          { key: "actions", label: "", className: "text-right" },
        ]}
        rows={rows}
        getKey={(i) => data!.data[i].id}
        loading={isLoading}
      />

      <CreateUserDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
      />
      <ResetPasswordDialog
        user={resetFor}
        onClose={() => setResetFor(null)}
      />
    </>
  );
}

function CreateUserDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const invalidate = useInvalidate();
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [role, setRole] = React.useState("view");
  const [submitting, setSubmitting] = React.useState(false);

  React.useEffect(() => {
    if (open) {
      setUsername("");
      setPassword("");
      setRole("view");
    }
  }, [open]);

  const submit = async () => {
    if (username.length < 1 || username.length > 64)
      return toast.error("用户名 1-64 个字符");
    if (password.length < 8 || password.length > 128)
      return toast.error("密码 8-128 个字符");
    setSubmitting(true);
    try {
      await http.post("/api/users", { username, password, role });
      toast.success(`用户 ${username} 已创建`);
      invalidate({ resource: "users", invalidates: ["list"] });
      onClose();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>新建用户</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label>用户名</Label>
            <Input value={username} onChange={(e) => setUsername(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>密码（8-128 位）</Label>
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label>角色</Label>
            <Select value={role} onValueChange={setRole}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ROLES.map((r) => (
                  <SelectItem key={r} value={r}>
                    {r}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting && <Loader2 className="animate-spin" />}
            创建
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ResetPasswordDialog({
  user,
  onClose,
}: {
  user: UserOut | null;
  onClose: () => void;
}) {
  const [password, setPassword] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);

  React.useEffect(() => {
    if (user) setPassword("");
  }, [user]);

  const submit = async () => {
    if (!user) return;
    if (password.length < 8 || password.length > 128)
      return toast.error("密码 8-128 个字符");
    setSubmitting(true);
    try {
      await http.post(`/api/users/${user.id}/reset-password`, {
        new_password: password,
      });
      toast.success(`已重置 ${user.username} 的密码`);
      onClose();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={user !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <UserCog className="h-4 w-4" /> 重置密码：{user?.username}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label>新密码（8-128 位）</Label>
          <Input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting && <Loader2 className="animate-spin" />}
            重置
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
