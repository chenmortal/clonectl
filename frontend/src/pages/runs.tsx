import { useInvalidate, useOne, useList } from "@refinedev/core";
import { AlertTriangle, Download, RefreshCw } from "lucide-react";
import * as React from "react";
import { toast } from "sonner";

import { DataPage, StatusDot } from "@/components/shared/data-page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Progress } from "@/components/ui/progress";
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
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type {
  CheckDetail,
  LiveStats,
  RunDetail,
  RunStatus,
  SyncTask,
  CheckTask,
} from "@/lib/types";
import { fmtBytes, fmtDateTime, fmtDuration } from "@/lib/utils";

const STATUSES: RunStatus[] = ["running", "success", "failed", "skipped", "pending"];

interface RunPage<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

function statusTone(s: RunStatus) {
  switch (s) {
    case "success":
      return "success" as const;
    case "failed":
      return "failed" as const;
    case "running":
      return "running" as const;
    case "pending":
      return "pending" as const;
    default:
      return "skipped" as const;
  }
}

export default function Runs() {
  return (
    <Tabs defaultValue="sync">
      <TabsList>
        <TabsTrigger value="sync">同步记录</TabsTrigger>
        <TabsTrigger value="checks">检查记录</TabsTrigger>
      </TabsList>
      <TabsContent value="sync">
        <SyncRuns />
      </TabsContent>
      <TabsContent value="checks">
        <CheckRuns />
      </TabsContent>
    </Tabs>
  );
}

// --- sync runs ------------------------------------------------------------

function SyncRuns() {
  const [taskFilter, setTaskFilter] = React.useState("");
  const [statusFilter, setStatusFilter] = React.useState("");
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(20);
  const { data: tasks } = useList<SyncTask>({ resource: "tasks" });

  const [pageData, setPageData] = React.useState<RunPage<RunDetail>>({
    items: [], total: 0, page: 1, page_size: 20,
  });
  const [loading, setLoading] = React.useState(true);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const { http } = await import("@/lib/api");
      const { data } = await http.get<RunPage<RunDetail>>("/api/runs", {
        params: {
          ...(taskFilter ? { task_id: taskFilter } : {}),
          ...(statusFilter ? { status: statusFilter } : {}),
          page, page_size: pageSize,
        },
      });
      setPageData(data);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [taskFilter, statusFilter, page, pageSize]);

  React.useEffect(() => {
    load();
    const id = setInterval(load, 8000);
    return () => clearInterval(id);
  }, [load]);

  React.useEffect(() => { setPage(1); }, [taskFilter, statusFilter, pageSize]);

  const [openId, setOpenId] = React.useState<number | null>(null);
  const taskName = (id: number) =>
    tasks?.data.find((t) => t.id === id)?.name ?? `#${id}`;

  const exportCSV = async () => {
    try {
      const { http } = await import("@/lib/api");
      const res = await http.get("/api/runs/export.csv", {
        params: {
          ...(taskFilter ? { task_id: taskFilter } : {}),
          ...(statusFilter ? { status: statusFilter } : {}),
        },
        responseType: "blob",
      });
      const blob = new Blob([res.data], { type: "text/csv" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `sync-runs-${new Date().toISOString().slice(0, 10)}.csv`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const rows = pageData.items.map((r) => [
    <span key="i" className="text-xs text-muted-foreground">
      #{r.id}
    </span>,
    <span key="t" className="font-medium">
      {taskName(r.task_id)}
    </span>,
    <StatusDot key="s" tone={statusTone(r.status)}>
      {r.status}
    </StatusDot>,
    <Badge key="tr" variant="outline">
      {r.trigger}
    </Badge>,
    <span key="st" className="text-xs text-muted-foreground">
      {fmtDateTime(r.started_at ?? r.finished_at)}
    </span>,
    <div key="e" className="max-w-56 truncate text-xs" title={r.error ?? ""}>
      {r.error ? (
        <span className="text-destructive">{r.error}</span>
      ) : (
        <span className="text-muted-foreground">
          {r.status === "success"
            ? fmtBytes((r.stats as Record<string, number>)?.bytes)
            : "-"}
        </span>
      )}
    </div>,
    <div key="a" className="flex justify-end">
      <Button
        variant="ghost"
        size="icon-sm"
        title="详情"
        onClick={() => setOpenId(r.id)}
      >
        <RefreshCw className="hidden" />
        查看
      </Button>
    </div>,
  ]);

  return (
    <>
      <DataPage
        title="同步运行记录"
        toolbar={
          <>
            <Select value={taskFilter} onValueChange={setTaskFilter}>
              <SelectTrigger className="w-44">
                <SelectValue placeholder="全部任务" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">全部任务</SelectItem>
                {tasks?.data.map((t) => (
                  <SelectItem key={t.id} value={String(t.id)}>
                    {t.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-32">
                <SelectValue placeholder="全部状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">全部状态</SelectItem>
                {STATUSES.map((s) => (
                  <SelectItem key={s} value={s}>
                    {s}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="outline" size="sm" onClick={exportCSV}>
              <Download /> 导出 CSV
            </Button>
          </>
        }
        columns={[
          { key: "id", label: "ID" },
          { key: "task", label: "任务" },
          { key: "status", label: "状态" },
          { key: "trigger", label: "触发" },
          { key: "started", label: "开始时间" },
          { key: "error", label: "信息" },
          { key: "actions", label: "", className: "text-right" },
        ]}
        rows={rows}
        getKey={(i) => pageData.items[i].id}
        loading={loading}
        paging={{
          page: pageData.page,
          pageSize: pageData.page_size,
          total: pageData.total,
          onPageChange: (p) => setPage(p),
          onPageSizeChange: (s) => setPageSize(s),
        }}
      />
      {openId !== null && (
        <RunDetailSheet runId={openId} onClose={() => setOpenId(null)} />
      )}
    </>
  );
}

function RunDetailSheet({ runId, onClose }: { runId: number; onClose: () => void }) {
  const invalidate = useInvalidate();
  // Live: refine useOne with 5s refetch — mirrors the old 5s poll loop.
  const { data, refetch } = useOne<RunDetail>({
    resource: "runs",
    id: runId,
    queryOptions: {
      refetchInterval: (q) => {
        const status = (q as unknown as { state?: { data?: { data?: { status?: string } } } })?.state?.data?.data?.status;
        return status === "running" ? 5000 : false;
      },
    },
  });
  const run = data?.data;
  const [stats, setStats] = React.useState(0);
  React.useEffect(() => {
    if (run?.status === "success") {
      invalidate({ resource: "runs", invalidates: ["list"] });
      setStats((s) => s + 1);
    }
  }, [run?.status]);
  void stats;

  const live = run?.live?.stats as LiveStats | undefined;
  const pct =
    live && live.totalBytes
      ? Math.min(100, Math.round(((live.bytes ?? 0) / live.totalBytes) * 100))
      : null;

  return (
    <Sheet open onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="overflow-y-auto">
        <SheetHeader>
          <SheetTitle>
            运行 #{run?.id ?? runId} · {run?.status ?? "…"}
          </SheetTitle>
          <SheetDescription>
            {run && `开始 ${fmtDateTime(run.started_at)} · 结束 ${fmtDateTime(run.finished_at)}`}
          </SheetDescription>
        </SheetHeader>

        {run?.status === "running" && (
          <div className="mt-6 space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="flex items-center gap-2 text-blue-600 dark:text-blue-400">
                <RefreshCw className="h-3.5 w-3.5 animate-spin" /> 实时进度（5s 刷新）
              </span>
              <span className="font-mono text-xs">
                {pct !== null ? `${pct}%` : "统计中…"}
              </span>
            </div>
            <Progress value={pct ?? undefined} />
            {live && (
              <div className="grid grid-cols-2 gap-2 text-xs text-muted-foreground sm:grid-cols-4">
                <div>
                  已传 {fmtBytes(live.bytes)} / {fmtBytes(live.totalBytes)}
                </div>
                <div>速度 {fmtBytes(live.speed)}/s</div>
                <div>传输 {String(live.transfers ?? "-")}</div>
                <div>检查 {String(live.checks ?? "-")}</div>
              </div>
            )}
            {live?.transferring && live.transferring.length > 0 && (
              <p className="truncate rounded-md bg-muted px-2 py-1 font-mono text-xs">
                {live.transferring[0].name}
              </p>
            )}
          </div>
        )}

        {run?.error && (
          <div className="mt-6 flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <span className="whitespace-pre-wrap">{run.error}</span>
          </div>
        )}

        {run?.stats && (
          <div className="mt-6">
            <h4 className="mb-2 text-sm font-semibold">统计</h4>
            <Table>
              <TableBody>
                {Object.entries(run.stats as Record<string, unknown>)
                  .filter(([k]) => k !== "jobStatus" && k !== "jobStats")
                  .map(([k, v]) => (
                    <TableRow key={k}>
                      <TableCell className="w-32 font-mono text-xs text-muted-foreground">
                        {k}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {k === "bytes" || k === "totalBytes"
                          ? fmtBytes(Number(v))
                          : k === "elapsedTime"
                            ? fmtDuration(Number(v))
                            : String(v)}
                      </TableCell>
                    </TableRow>
                  ))}
              </TableBody>
            </Table>
          </div>
        )}

        <div className="mt-6 flex justify-end">
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            <RefreshCw /> 刷新
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

// --- check runs -----------------------------------------------------------

function CheckRuns() {
  const [taskFilter, setTaskFilter] = React.useState("");
  const [statusFilter, setStatusFilter] = React.useState("");
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(20);
  const { data: tasks } = useList<CheckTask>({ resource: "check-tasks" });

  const [pageData, setPageData] = React.useState<RunPage<CheckDetail>>({
    items: [], total: 0, page: 1, page_size: 20,
  });
  const [loading, setLoading] = React.useState(true);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const { http } = await import("@/lib/api");
      const { data } = await http.get<RunPage<CheckDetail>>("/api/checks", {
        params: {
          ...(taskFilter ? { task_id: taskFilter } : {}),
          ...(statusFilter ? { status: statusFilter } : {}),
          page, page_size: pageSize,
        },
      });
      setPageData(data);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [taskFilter, statusFilter, page, pageSize]);

  React.useEffect(() => {
    load();
    const id = setInterval(load, 8000);
    return () => clearInterval(id);
  }, [load]);

  React.useEffect(() => { setPage(1); }, [taskFilter, statusFilter, pageSize]);

  const exportCSV = async () => {
    try {
      const { http } = await import("@/lib/api");
      const res = await http.get("/api/checks/export.csv", {
        params: {
          ...(taskFilter ? { task_id: taskFilter } : {}),
          ...(statusFilter ? { status: statusFilter } : {}),
        },
        responseType: "blob",
      });
      const blob = new Blob([res.data], { type: "text/csv" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `check-runs-${new Date().toISOString().slice(0, 10)}.csv`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const [openId, setOpenId] = React.useState<number | null>(null);
  const taskName = (id: number) =>
    tasks?.data.find((t) => t.id === id)?.name ?? `#${id}`;

  const rows = pageData.items.map((c) => [
    <span key="i" className="text-xs text-muted-foreground">
      #{c.id}
    </span>,
    <span key="t" className="font-medium">
      {taskName(c.task_id)}
    </span>,
    <StatusDot key="s" tone={statusTone(c.status)}>
      {c.status}
    </StatusDot>,
    <span key="st" className="text-xs text-muted-foreground">
      {fmtDateTime(c.started_at ?? c.finished_at)}
    </span>,
    <div key="e" className="max-w-64 truncate text-xs" title={c.error ?? ""}>
      {c.error ? (
        <span className="text-destructive">{c.error}</span>
      ) : (
        <span className="text-muted-foreground">两端一致</span>
      )}
    </div>,
    <div key="a" className="flex justify-end">
      <Button variant="ghost" size="sm" onClick={() => setOpenId(c.id)}>
        查看
      </Button>
    </div>,
  ]);

  return (
    <>
      <DataPage
        title="检查记录"
        toolbar={
          <>
            <Select value={taskFilter} onValueChange={setTaskFilter}>
              <SelectTrigger className="w-44">
                <SelectValue placeholder="全部任务" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">全部任务</SelectItem>
                {tasks?.data.map((t) => (
                  <SelectItem key={t.id} value={String(t.id)}>
                    {t.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-32">
                <SelectValue placeholder="全部状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">全部状态</SelectItem>
                {STATUSES.map((s) => (
                  <SelectItem key={s} value={s}>
                    {s}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="outline" size="sm" onClick={exportCSV}>
              <Download /> 导出 CSV
            </Button>
          </>
        }
        columns={[
          { key: "id", label: "ID" },
          { key: "task", label: "任务" },
          { key: "status", label: "状态" },
          { key: "started", label: "开始时间" },
          { key: "info", label: "结果" },
          { key: "actions", label: "", className: "text-right" },
        ]}
        rows={rows}
        getKey={(i) => pageData.items[i].id}
        loading={loading}
        paging={{
          page: pageData.page,
          pageSize: pageData.page_size,
          total: pageData.total,
          onPageChange: (p) => setPage(p),
          onPageSizeChange: (s) => setPageSize(s),
        }}
      />
      {openId !== null && (
        <CheckDetailSheet checkId={openId} onClose={() => setOpenId(null)} />
      )}
    </>
  );
}

function CheckDetailSheet({
  checkId,
  onClose,
}: {
  checkId: number;
  onClose: () => void;
}) {
  const { data } = useOne<CheckDetail>({
    resource: "checks",
    id: checkId,
    queryOptions: {
      refetchInterval: (q) => {
        const status = (q as unknown as { state?: { data?: { data?: { status?: string } } } })?.state?.data?.data?.status;
        return status === "running" ? 5000 : false;
      },
    },
  });
  const check = data?.data;
  const result = check?.result;

  const RESULT_KEYS = ["differ", "missingOnSrc", "missingOnDst", "error", "match"] as const;
  const sections: { key: string; label: string; tone: string }[] = [
    { key: "differ", label: "不一致（differ）", tone: "text-destructive" },
    { key: "missingOnSrc", label: "源端缺失（missingOnSrc）", tone: "text-amber-600" },
    { key: "missingOnDst", label: "目标端缺失（missingOnDst）", tone: "text-amber-600" },
    { key: "error", label: "错误（error）", tone: "text-destructive" },
    { key: "match", label: "一致（match）", tone: "text-emerald-600" },
  ];
  void RESULT_KEYS;

  return (
    <Sheet open onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="overflow-y-auto">
        <SheetHeader>
          <SheetTitle>
            检查 #{check?.id ?? checkId} · {check?.status ?? "…"}
          </SheetTitle>
          <SheetDescription>
            {check && `开始 ${fmtDateTime(check.started_at)} · 结束 ${fmtDateTime(check.finished_at)}`}
          </SheetDescription>
        </SheetHeader>

        {check?.error && (
          <div className="mt-6 flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            {check.error}
          </div>
        )}

        {result && (
          <div className="mt-6 space-y-4">
            {sections.map(({ key, label, tone }) => {
              const list = ((result as Record<string, unknown> | undefined)?.[key] as string[] | undefined) ?? [];
              if (list.length === 0) return null;
              return (
                <div key={String(key)}>
                  <h4 className="mb-1.5 text-sm font-semibold">
                    {label}{" "}
                    <Badge variant="outline" className="ml-1">
                      {list.length}
                    </Badge>
                  </h4>
                  <div className="max-h-48 overflow-y-auto rounded-md border">
                    <Table>
                      <TableBody>
                        {list.slice(0, 200).map((f, i) => (
                          <TableRow key={i}>
                            <TableCell
                              className={`font-mono text-xs ${tone}`}
                            >
                              {f}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                </div>
              );
            })}
            {sections.every(
              ({ key }) => (((result as Record<string, unknown> | undefined)?.[key] as string[] | undefined) ?? []).length === 0,
            ) && (
              <p className="text-sm text-muted-foreground">输出无差异明细（两端一致或检查项未开启）。</p>
            )}
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}
