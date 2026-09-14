import { useCustom, useInvalidate } from "@refinedev/core";
import {
  Activity,
  CalendarClock,
  CheckCircle2,
  Loader2,
  Play,
  RefreshCw,
  ShieldAlert,
  XCircle,
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { errMessage, http } from "@/lib/api";
import type {
  SchedulerJob,
  SchedulerJobDetail,
  SchedulerJobsOut,
  SchedulerOverview,
} from "@/lib/types";
import { fmtUtc } from "@/lib/utils";

export default function SchedulerMonitor() {
  const invalidate = useInvalidate();
  // /api/scheduler/jobs returns a wrapper object → useCustom (not useList).
  const { data: jobsOut, isLoading } = useCustom<SchedulerJobsOut>({
    url: "/api/scheduler/jobs",
    method: "get",
    queryOptions: { refetchInterval: 5000 },
  });
  const [overview, setOverview] = React.useState<SchedulerOverview | null>(null);
  const [detail, setDetail] = React.useState<SchedulerJobDetail | null>(null);
  const [running, setRunning] = React.useState<string | null>(null);

  const refresh = React.useCallback(() => {
    http
      .get<SchedulerOverview>("/api/scheduler/overview")
      .then((r) => setOverview(r.data))
      .catch(() => {});
  }, []);

  useEffect5s(refresh);

  const jobs = jobsOut?.data.jobs ?? [];

  // Detail drawer live refresh at 2s.
  React.useEffect(() => {
    if (!detail) return;
    const timer = setInterval(() => {
      http
        .get<SchedulerJobDetail>(`/api/scheduler/jobs/${detail.id}`)
        .then((r) => setDetail(r.data))
        .catch(() => {});
    }, 2000);
    return () => clearInterval(timer);
  }, [detail?.id, detail]);

  const runNow = async (job: SchedulerJob) => {
    setRunning(job.id);
    try {
      const out = await http.post<{
        kind: string;
        task_id: number | null;
        run_id?: number;
        check_id?: number;
      }>(`/api/scheduler/jobs/${job.id}/run`);
      toast.success(
        out.data.kind === "task"
          ? `已触发同步任务 #${out.data.task_id}（run #${out.data.run_id ?? "?"}）`
          : `已触发检查任务 #${out.data.task_id}（check #${out.data.check_id ?? "?"}）`,
      );
      invalidate({ resource: "runs", invalidates: ["list"] });
      refresh();
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setRunning(null);
    }
  };

  return (
    <div className="space-y-4">
      {/* Overview cards */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>节点角色</CardDescription>
            <CardTitle className="flex items-center gap-2 text-base">
              {overview?.is_leader ? (
                <>
                  <CheckCircle2 className="h-4 w-4 text-emerald-500" /> Leader
                </>
              ) : (
                <>
                  <ShieldAlert className="h-4 w-4 text-amber-500" /> Standby
                </>
              )}
            </CardTitle>
          </CardHeader>
          <CardContent className="text-xs text-muted-foreground">
            {overview?.leader_id ? `leader: ${overview.leader_id}` : "-"}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>总任务数</CardDescription>
          </CardHeader>
          <CardContent>
            <span className="text-2xl font-semibold tabular-nums">
              {overview?.jobs_total ?? "-"}
            </span>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>用户任务</CardDescription>
          </CardHeader>
          <CardContent>
            <span className="text-2xl font-semibold tabular-nums">
              {overview?.user_jobs ?? "-"}
            </span>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>正在运行</CardDescription>
          </CardHeader>
          <CardContent>
            <span className="flex items-center gap-2 text-2xl font-semibold tabular-nums">
              {overview?.running_now ?? "-"}
              {(overview?.running_now ?? 0) > 0 && (
                <Loader2 className="h-4 w-4 animate-spin text-blue-500" />
              )}
            </span>
          </CardContent>
        </Card>
      </div>

      {/* Jobs table */}
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div>
            <CardTitle className="text-base">调度任务</CardTitle>
            <CardDescription>
              执行历史保存在内存中，服务重启后清零；「运行」走与 trigger
              相同的服务层。
            </CardDescription>
          </div>
          <Button variant="outline" size="sm" onClick={refresh}>
            <RefreshCw /> 刷新
          </Button>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>任务</TableHead>
                <TableHead>计划</TableHead>
                <TableHead>下次执行</TableHead>
                <TableHead>上次完成</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">执行 / 失败</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isLoading && jobs.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="h-24 text-center text-muted-foreground">
                    <Loader2 className="mx-auto h-4 w-4 animate-spin" />
                  </TableCell>
                </TableRow>
              ) : jobs.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="h-24 text-center text-muted-foreground">
                    暂无调度任务
                  </TableCell>
                </TableRow>
              ) : (
                jobs.map((j: SchedulerJob) => (
                  <TableRow key={j.id}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-xs font-medium">{j.name}</span>
                        <Badge variant={j.kind === "internal" ? "secondary" : "outline"}>
                          {j.kind === "internal" ? "内部" : j.kind === "task" ? "同步" : "检查"}
                        </Badge>
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{j.schedule || "-"}</TableCell>
                    <TableCell className="text-xs">{fmtUtc(j.next_run)}</TableCell>
                    <TableCell className="text-xs">
                      {fmtUtc(j.last_run_completed_at)}
                    </TableCell>
                    <TableCell>
                      {j.is_running ? (
                        <Badge variant="info">
                          <Loader2 className="animate-spin" /> 运行中
                        </Badge>
                      ) : j.consecutive_failures > 0 ? (
                        <Badge variant="destructive" title={j.last_error}>
                          <XCircle /> 连续失败 {j.consecutive_failures}
                        </Badge>
                      ) : (
                        <Badge variant="success">
                          <CheckCircle2 /> 正常
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {j.run_count} /{" "}
                      <span className={j.fail_count ? "text-destructive" : ""}>
                        {j.fail_count}
                      </span>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          title="详情"
                          onClick={() =>
                            http
                              .get<SchedulerJobDetail>(`/api/scheduler/jobs/${j.id}`)
                              .then((r) => setDetail(r.data))
                          }
                        >
                          <CalendarClock />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          title={j.kind === "internal" ? "内部任务不可手动运行" : "运行"}
                          disabled={j.kind === "internal" || running === j.id}
                          onClick={() => runNow(j)}
                        >
                          {running === j.id ? (
                            <Loader2 className="animate-spin" />
                          ) : (
                            <Play className="text-emerald-600 dark:text-emerald-400" />
                          )}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {/* Detail drawer */}
      <Sheet open={detail !== null} onOpenChange={(o) => !o && setDetail(null)}>
        <SheetContent>
          <SheetHeader>
            <SheetTitle>任务详情：{detail?.name ?? ""}</SheetTitle>
            <SheetDescription>
              计划 <span className="font-mono">{detail?.schedule || "-"}</span>
              {detail?.last_error && (
                <span className="ml-2 text-destructive">
                  最近错误：{detail.last_error}
                </span>
              )}
            </SheetDescription>
          </SheetHeader>

          {detail && (
            <div className="mt-6 space-y-8">
              <div>
                <h4 className="mb-3 flex items-center gap-2 text-sm font-semibold">
                  <Activity className="h-4 w-4" /> 即将执行
                </h4>
                <ol className="space-y-2 border-l pl-4">
                  {detail.next_runs.map((t) => (
                    <li key={t} className="relative text-sm text-muted-foreground">
                      <span className="absolute -left-[21px] top-1.5 h-2 w-2 rounded-full bg-primary/60" />
                      <span className="font-mono text-xs">{fmtUtc(t)}</span>
                    </li>
                  ))}
                  {detail.next_runs.length === 0 && (
                    <li className="text-sm text-muted-foreground">无计划执行</li>
                  )}
                </ol>
              </div>

              <div>
                <h4 className="mb-3 flex items-center gap-2 text-sm font-semibold">
                  <RefreshCw className="h-4 w-4" /> 最近执行
                </h4>
                {detail.executions.length === 0 ? (
                  <p className="text-sm text-muted-foreground">
                    暂无记录（监控状态保存在内存中）
                  </p>
                ) : (
                  <ol className="space-y-2 border-l pl-4">
                    {[...detail.executions].reverse().map((e, i) => (
                      <li key={`${e.started_at}-${i}`} className="relative">
                        <span
                          className={`absolute -left-[21px] top-1.5 h-2 w-2 rounded-full ${
                            e.error ? "bg-red-500" : "bg-emerald-500"
                          }`}
                        />
                        <div className="text-sm">
                          <span className="font-mono text-xs">{fmtUtc(e.started_at)}</span>
                          <span className="ml-2 text-xs text-muted-foreground">
                            {e.duration_ms}ms
                          </span>
                          {e.error && (
                            <div className="text-xs text-destructive">{e.error}</div>
                          )}
                        </div>
                      </li>
                    ))}
                  </ol>
                )}
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}

/** Interval helper that always calls the latest callback. */
function useEffect5s(fn: () => void) {
  const ref = React.useRef(fn);
  ref.current = fn;
  React.useEffect(() => {
    const tick = () => ref.current();
    tick();
    const timer = setInterval(tick, 5000);
    return () => clearInterval(timer);
  }, []);
}
