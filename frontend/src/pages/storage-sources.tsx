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
import { errMessage, http } from "@/lib/api";
import type { StorageSource } from "@/lib/types";
import { fmtDateTime } from "@/lib/utils";

// Three backends are supported; each carries its own field set.
//   s3    — S3-compatible object storage (provider / endpoint / region).
//   local — local filesystem rooted at a configurable prefix.
//   redis — Redis instance (standalone / cluster / sentinel / proxy),
//           backed by a remote redis-shake-agent; credentials live on
//           DataSource, not StorageSource.
type SourceType = "s3" | "local" | "redis";

const TYPE_LABEL: Record<SourceType, string> = {
  s3: "S3 对象存储",
  local: "本地文件系统",
  redis: "Redis",
};

const S3_PROVIDERS = ["AWS", "Minio", "Alibaba", "Tencent", "Other"] as const;

const REDIS_MODES = ["standalone", "cluster", "sentinel", "proxy"] as const;
type RedisMode = (typeof REDIS_MODES)[number];

const REDIS_MODE_LABEL: Record<RedisMode, string> = {
  standalone: "单机",
  cluster: "集群",
  sentinel: "哨兵",
  proxy: "代理",
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
    <Badge key="t" variant={s.type === "s3" ? "info" : "secondary"}>
      {TYPE_LABEL[s.type as SourceType] ?? s.type}
    </Badge>,
    s.type === "local" ? (
      <span key="e" className="font-mono text-xs">{s.path ?? "/"}</span>
    ) : (
      <span key="e" className="font-mono text-xs">{s.endpoint ?? "-"}</span>
    ),
    <span key="p" className="text-xs">
      {s.type === "s3" ? String(s.extra?.provider ?? "-") : "-"}
    </span>,
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
        description="S3 对象存储（endpoint + provider + 凭据在数据源上）或本地文件系统（公共路径前缀）。"
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
          { key: "target", label: "Endpoint / 路径前缀" },
          { key: "provider", label: "Provider" },
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
  const [type, setType] = React.useState<SourceType>("s3");
  const [endpoint, setEndpoint] = React.useState("");
  const [region, setRegion] = React.useState("");
  const [provider, setProvider] = React.useState("Minio");
  const [customProvider, setCustomProvider] = React.useState(false);
  const [customProviderVal, setCustomProviderVal] = React.useState("");
  const [path, setPath] = React.useState("");
  const [extra, setExtra] = React.useState("{}");
  // Redis-only fields. The StorageSource.Extra carries the shared
  // topology (mode / addresses / master_name); per-DSN credentials
  // and per-DSN knobs live on DataSource.
  const [redisMode, setRedisMode] = React.useState<RedisMode>("standalone");
  const [redisAddresses, setRedisAddresses] = React.useState("");
  const [redisMaster, setRedisMaster] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setName(editing?.name ?? "");
    const t = (
      editing?.type === "local" ? "local"
      : editing?.type === "redis" ? "redis"
      : "s3"
    ) as SourceType;
    setType(t);
    setEndpoint(editing?.endpoint ?? "");
    setRegion(editing?.region ?? "");
    setPath(editing?.path ?? "");
    const prov = String(editing?.extra?.provider ?? "");
    if (prov && (S3_PROVIDERS as readonly string[]).includes(prov)) {
      setProvider(prov);
      setCustomProvider(false);
      setCustomProviderVal("");
    } else if (prov) {
      setProvider("Other");
      setCustomProvider(true);
      setCustomProviderVal(prov);
    } else {
      setProvider("Minio");
      setCustomProvider(false);
      setCustomProviderVal("");
    }
    const rest = { ...(editing?.extra ?? {}) };
    delete rest.provider;
    // Redis-specific extras live under the same `extra` object so we
    // don't need a separate DB column on StorageSource.
    const extraMode = String(rest.mode ?? "");
    if (t === "redis") {
      const m = (REDIS_MODES as readonly string[]).includes(extraMode)
        ? (extraMode as RedisMode)
        : "standalone";
      setRedisMode(m);
      const addrs = Array.isArray(rest.addresses) ? (rest.addresses as unknown[]).map(String) : [];
      setRedisAddresses(addrs.join("\n"));
      setRedisMaster(String(rest.master_name ?? ""));
    } else {
      setRedisMode("standalone");
      setRedisAddresses("");
      setRedisMaster("");
    }
    delete rest.mode;
    delete rest.addresses;
    delete rest.master_name;
    setExtra(Object.keys(rest).length ? JSON.stringify(rest, null, 2) : "{}");
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

    const body: Record<string, unknown> = {
      name: name.trim(),
      type,
      extra: extraObj,
    };
    if (type === "s3") {
      const prov = customProvider ? customProviderVal.trim() : provider;
      if (!prov) {
        toast.error("请选择或填写 provider");
        return;
      }
      body.endpoint = endpoint || null;
      body.region = region || null;
      body.extra = { ...extraObj, provider: prov };
    } else if (type === "local") {
      // local: endpoint/region must be empty; path is the FS prefix.
      if (!path.trim()) {
        toast.error("请填写本地文件系统路径前缀");
        return;
      }
      body.path = path.trim();
    } else {
      // redis: address list (≥1) + mode, optional master_name for sentinel.
      const addrs = redisAddresses
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      if (addrs.length === 0) {
        toast.error("请填写至少一个 Redis 地址（host:port）");
        return;
      }
      body.extra = {
        ...extraObj,
        mode: redisMode,
        addresses: addrs,
        ...(redisMode === "sentinel" && redisMaster.trim()
          ? { master_name: redisMaster.trim() }
          : {}),
      };
    }

    setSubmitting(true);
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
              <Select
                value={type}
                onValueChange={(v) => setType(v as SourceType)}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="s3">{TYPE_LABEL.s3}</SelectItem>
                  <SelectItem value="local">{TYPE_LABEL.local}</SelectItem>
                  <SelectItem value="redis">{TYPE_LABEL.redis}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {type === "s3" ? (
            <>
              <div className="space-y-1.5">
                <Label>Endpoint</Label>
                <Input
                  placeholder="http://127.0.0.1:9000"
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-1.5">
                  <Label>Provider</Label>
                  {customProvider ? (
                    <div className="flex gap-2">
                      <Input
                        placeholder="rclone provider 名"
                        value={customProviderVal}
                        onChange={(e) => setCustomProviderVal(e.target.value)}
                      />
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setCustomProvider(false);
                          setCustomProviderVal("");
                        }}
                      >
                        取消
                      </Button>
                    </div>
                  ) : (
                    <div className="flex gap-2">
                      <Select value={provider} onValueChange={setProvider}>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {S3_PROVIDERS.map((p) => (
                            <SelectItem key={p} value={p}>
                              {p}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setCustomProvider(true)}
                      >
                        自定义
                      </Button>
                    </div>
                  )}
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
              <p className="text-[11px] text-muted-foreground">
                凭据（Access Key / Secret Key）在「数据源」上按需填写，不存放在存储源。
              </p>
            </>
          ) : type === "local" ? (
            <div className="space-y-1.5">
              <Label>文件系统路径前缀</Label>
              <Input
                className="font-mono"
                placeholder="/srv/rclone-roots"
                value={path}
                onChange={(e) => setPath(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">
                该存储源下所有数据源的路径都解析到此目录之下；无 endpoint、无需凭据。
              </p>
            </div>
          ) : (
            <div className="space-y-3">
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-1.5">
                  <Label>拓扑（mode）</Label>
                  <Select
                    value={redisMode}
                    onValueChange={(v) => setRedisMode(v as RedisMode)}
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {REDIS_MODES.map((m) => (
                        <SelectItem key={m} value={m}>
                          {REDIS_MODE_LABEL[m]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                {redisMode === "sentinel" && (
                  <div className="space-y-1.5">
                    <Label>Master 名称</Label>
                    <Input
                      placeholder="mymaster"
                      value={redisMaster}
                      onChange={(e) => setRedisMaster(e.target.value)}
                    />
                  </div>
                )}
              </div>
              <div className="space-y-1.5">
                <Label>地址（host:port，每行一个，或逗号 / 空格分隔）</Label>
                <textarea
                  className="flex min-h-[80px] w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  rows={3}
                  placeholder={"10.0.0.1:6379\n10.0.0.2:6379"}
                  value={redisAddresses}
                  onChange={(e) => setRedisAddresses(e.target.value)}
                />
                <p className="text-[11px] text-muted-foreground">
                  由远端 redis-shake-agent 实际连接；此存储源仅描述拓扑与种子节点。
                </p>
              </div>
              <p className="text-[11px] text-muted-foreground">
                凭据（密码）在「数据源」页按需填写；同一存储源可被多个数据源复用。
              </p>
            </div>
          )}

          <div className="space-y-1.5">
            <Label>高级参数（JSON，写入 extra）</Label>
            <textarea
              className="flex min-h-[60px] w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              rows={3}
              value={extra}
              onChange={(e) => setExtra(e.target.value)}
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
