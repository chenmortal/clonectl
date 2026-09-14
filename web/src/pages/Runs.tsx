import { ReloadOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Collapse,
  Descriptions,
  Drawer,
  Empty,
  message,
  Progress,
  Select,
  Space,
  Spin,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import dayjs from "dayjs";
import { useCallback, useEffect, useState } from "react";
import {
  errMessage,
  getCheck,
  getRun,
  listCheckTasks,
  listChecks,
  listRuns,
  listTasks,
} from "../api";
import { CheckDetail, CheckRun, CheckTask, RunDetail, RunStatus, SyncRun, SyncTask } from "../types";

const STATUS_META: Record<RunStatus, { color: string; label: string }> = {
  pending: { color: "default", label: "等待中" },
  running: { color: "processing", label: "运行中" },
  success: { color: "success", label: "成功" },
  failed: { color: "error", label: "失败" },
  skipped: { color: "warning", label: "已跳过" },
};

function StatusTag({ status }: { status: RunStatus }) {
  const meta = STATUS_META[status];
  return <Tag color={meta.color}>{meta.label}</Tag>;
}

function fmtBytes(n?: number): string {
  if (n === undefined || Number.isNaN(n)) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}

function fmtTime(v: string | null): string {
  return v ? dayjs(v).format("MM-DD HH:mm:ss") : "-";
}

const preStyle: React.CSSProperties = {
  whiteSpace: "pre-wrap",
  wordBreak: "break-all",
  maxHeight: 320,
  overflow: "auto",
  margin: 0,
  fontSize: 12,
  fontFamily: "Menlo, Monaco, Consolas, monospace",
};

function RunDetailDrawer({
  runId,
  tasks,
  onClose,
}: {
  runId: number | null;
  tasks: SyncTask[];
  onClose: () => void;
}) {
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (runId === null) {
      setDetail(null);
      return;
    }
    setLoading(true);
    getRun(runId)
      .then(setDetail)
      .catch((e) => message.error(errMessage(e)))
      .finally(() => setLoading(false));
  }, [runId]);

  const taskName = (id: number) => tasks.find((t) => t.id === id)?.name ?? `任务#${id}`;
  const stats = detail?.stats ?? {};
  const jobStatus = stats.jobStatus as Record<string, unknown> | undefined;
  const jobStats = stats.jobStats as Record<string, unknown> | undefined;

  return (
    <Drawer
      title={runId !== null ? `执行详情 run #${runId}` : ""}
      width={620}
      open={runId !== null}
      onClose={onClose}
    >
      <Spin spinning={loading}>
        {detail && (
          <Space direction="vertical" style={{ width: "100%" }} size="middle">
            <Descriptions size="small" column={2} bordered>
              <Descriptions.Item label="任务">{taskName(detail.task_id)}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <StatusTag status={detail.status} />
              </Descriptions.Item>
              <Descriptions.Item label="触发方式">
                {detail.trigger === "schedule" ? "调度" : "手动"}
              </Descriptions.Item>
              <Descriptions.Item label="rclone jobid">{detail.job_id ?? "-"}</Descriptions.Item>
              <Descriptions.Item label="开始时间">{fmtTime(detail.started_at)}</Descriptions.Item>
              <Descriptions.Item label="结束时间">{fmtTime(detail.finished_at)}</Descriptions.Item>
            </Descriptions>

            {detail.error && (
              <Alert
                type="error"
                showIcon
                message="错误日志"
                description={<pre style={preStyle}>{detail.error}</pre>}
              />
            )}

            {detail.status === "running" && detail.live?.stats && (
              <Descriptions size="small" column={2} bordered title="实时进度">
                <Descriptions.Item label="已传输">
                  {fmtBytes(Number(detail.live.stats.bytes))} / {fmtBytes(Number(detail.live.stats.totalBytes))}
                </Descriptions.Item>
                <Descriptions.Item label="速度">
                  {detail.live.stats.speed !== undefined ? `${fmtBytes(Number(detail.live.stats.speed))}/s` : "-"}
                </Descriptions.Item>
                <Descriptions.Item label="当前文件" span={2}>
                  {detail.live.stats.transferring?.[0]?.name ?? "-"}
                </Descriptions.Item>
              </Descriptions>
            )}

            {(stats.transfers !== undefined || stats.bytes !== undefined) && (
              <Descriptions size="small" column={2} bordered title="传输统计">
                <Descriptions.Item label="文件数">{String(stats.transfers ?? 0)}</Descriptions.Item>
                <Descriptions.Item label="错误数">{String(stats.errors ?? 0)}</Descriptions.Item>
                <Descriptions.Item label="传输字节">{fmtBytes(Number(stats.bytes))}</Descriptions.Item>
                <Descriptions.Item label="耗时">
                  {Number(stats.elapsedTime ?? 0).toFixed(1)}s
                </Descriptions.Item>
              </Descriptions>
            )}

            {(jobStatus || jobStats) && (
              <Collapse
                size="small"
                items={[
                  ...(jobStatus
                    ? [
                        {
                          key: "jobStatus",
                          label: "rclone job/status 原始输出",
                          children: <pre style={preStyle}>{JSON.stringify(jobStatus, null, 2)}</pre>,
                        },
                      ]
                    : []),
                  ...(jobStats
                    ? [
                        {
                          key: "jobStats",
                          label: "rclone core/stats 原始输出",
                          children: <pre style={preStyle}>{JSON.stringify(jobStats, null, 2)}</pre>,
                        },
                      ]
                    : []),
                ]}
              />
            )}
          </Space>
        )}
      </Spin>
    </Drawer>
  );
}

function MonitorPanel({
  tasks,
  onDetail,
}: {
  tasks: SyncTask[];
  onDetail: (id: number) => void;
}) {
  const [details, setDetails] = useState<RunDetail[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const running = await listRuns({ status: "running", limit: 50 });
      const ds = await Promise.all(running.map((r) => getRun(r.id)));
      setDetails(ds);
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const timer = setInterval(load, 5000);
    return () => clearInterval(timer);
  }, [load]);

  const taskName = (id: number) => tasks.find((t) => t.id === id)?.name ?? `任务#${id}`;

  if (!loading && details.length === 0) {
    return <Empty description="当前没有运行中的同步任务" />;
  }

  return (
    <Space direction="vertical" style={{ width: "100%" }} size="middle">
      <Button icon={<ReloadOutlined />} onClick={load} loading={loading} size="small">
        刷新（每 5 秒自动）
      </Button>
      {details.map((d) => {
        const stats = d.live?.stats;
        const pct =
          stats?.totalBytes && stats.bytes !== undefined
            ? Math.min(100, Math.round((stats.bytes / stats.totalBytes) * 100))
            : 0;
        const current = stats?.transferring?.[0];
        return (
          <Card
            key={d.id}
            size="small"
            title={`run #${d.id} · ${taskName(d.task_id)}`}
            extra={
              <Space>
                <StatusTag status={d.status} />
                <a onClick={() => onDetail(d.id)}>详情</a>
              </Space>
            }
          >
            <Progress percent={current?.percentage ?? pct} status="active" />
            <Descriptions size="small" column={3}>
              <Descriptions.Item label="已传输">
                {fmtBytes(stats?.bytes)} / {fmtBytes(stats?.totalBytes)}
              </Descriptions.Item>
              <Descriptions.Item label="速度">
                {stats?.speed !== undefined ? `${fmtBytes(stats.speed)}/s` : "-"}
              </Descriptions.Item>
              <Descriptions.Item label="剩余时间">
                {stats?.eta !== undefined ? `${stats.eta}s` : "-"}
              </Descriptions.Item>
              <Descriptions.Item label="开始时间">{fmtTime(d.started_at)}</Descriptions.Item>
              <Descriptions.Item label="rclone jobid">{d.job_id ?? "-"}</Descriptions.Item>
              <Descriptions.Item label="错误数">{stats?.errors ?? 0}</Descriptions.Item>
            </Descriptions>
            {current && (
              <Tooltip title={current.name}>
                <Typography.Text type="secondary" ellipsis style={{ maxWidth: "100%", display: "block" }}>
                  正在传输：{current.name}
                </Typography.Text>
              </Tooltip>
            )}
          </Card>
        );
      })}
    </Space>
  );
}

function HistoryPanel({
  tasks,
  onDetail,
}: {
  tasks: SyncTask[];
  onDetail: (id: number) => void;
}) {
  const [runs, setRuns] = useState<SyncRun[]>([]);
  const [loading, setLoading] = useState(false);
  const [taskId, setTaskId] = useState<number | undefined>();
  const [status, setStatus] = useState<RunStatus | undefined>();
  const [limit, setLimit] = useState(100);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setRuns(await listRuns({ task_id: taskId, status, limit }));
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, [taskId, status, limit]);

  useEffect(() => {
    load();
  }, [load]);

  const taskName = (id: number) => tasks.find((t) => t.id === id)?.name ?? `任务#${id}`;

  const columns = [
    { title: "ID", dataIndex: "id", width: 60 },
    { title: "任务", dataIndex: "task_id", render: (v: number) => taskName(v) },
    { title: "状态", dataIndex: "status", width: 90, render: (s: RunStatus) => <StatusTag status={s} /> },
    {
      title: "触发",
      dataIndex: "trigger",
      width: 80,
      render: (t: string) => (t === "schedule" ? "调度" : "手动"),
    },
    { title: "开始", dataIndex: "started_at", width: 120, render: fmtTime },
    { title: "结束", dataIndex: "finished_at", width: 120, render: fmtTime },
    {
      title: "统计",
      width: 180,
      render: (_: unknown, r: SyncRun) =>
        r.stats
          ? `${Number(r.stats.transfers ?? 0)} 文件 / ${fmtBytes(Number(r.stats.bytes))} / ${Number(r.stats.elapsedTime ?? 0).toFixed(1)}s`
          : "-",
    },
    {
      title: "错误",
      dataIndex: "error",
      ellipsis: true,
      render: (e: string | null, r: SyncRun) =>
        e ? (
          <a onClick={() => onDetail(r.id)}>
            <Typography.Text type="danger" ellipsis style={{ maxWidth: 220 }}>
              {e}
            </Typography.Text>
          </a>
        ) : (
          "-"
        ),
    },
    {
      title: "操作",
      width: 70,
      render: (_: unknown, r: SyncRun) => <a onClick={() => onDetail(r.id)}>详情</a>,
    },
  ];

  return (
    <Space direction="vertical" style={{ width: "100%" }} size="middle">
      <Space wrap>
        <Select
          allowClear
          placeholder="按任务过滤"
          style={{ width: 180 }}
          value={taskId}
          onChange={setTaskId}
          options={tasks.map((t) => ({ value: t.id, label: t.name }))}
        />
        <Select
          allowClear
          placeholder="按状态过滤"
          style={{ width: 140 }}
          value={status}
          onChange={setStatus}
          options={Object.entries(STATUS_META).map(([k, v]) => ({ value: k, label: v.label }))}
        />
        <Select
          style={{ width: 120 }}
          value={limit}
          onChange={setLimit}
          options={[50, 100, 200, 500].map((n) => ({ value: n, label: `最近 ${n} 条` }))}
        />
        <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>
          查询
        </Button>
      </Space>
      <Table rowKey="id" loading={loading} columns={columns} dataSource={runs} pagination={{ pageSize: 20 }} />
    </Space>
  );
}

const CHECK_LIST_KEYS: { key: "differ" | "missingOnSrc" | "missingOnDst" | "error" | "match" | "combined"; label: string }[] = [
  { key: "differ", label: "不一致文件 differ" },
  { key: "missingOnSrc", label: "源端缺失 missingOnSrc" },
  { key: "missingOnDst", label: "目标端缺失 missingOnDst" },
  { key: "error", label: "错误文件 error" },
  { key: "match", label: "匹配文件 match" },
  { key: "combined", label: "合并报告 combined" },
];

function CheckDetailDrawer({
  checkId,
  checkTasks,
  onClose,
}: {
  checkId: number | null;
  checkTasks: CheckTask[];
  onClose: () => void;
}) {
  const [detail, setDetail] = useState<CheckDetail | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (checkId === null) {
      setDetail(null);
      return;
    }
    setLoading(true);
    getCheck(checkId)
      .then(setDetail)
      .catch((e) => message.error(errMessage(e)))
      .finally(() => setLoading(false));
  }, [checkId]);

  const taskName = (id: number) => checkTasks.find((t) => t.id === id)?.name ?? `检查任务#${id}`;
  const result = detail?.result;

  return (
    <Drawer
      title={checkId !== null ? `检查详情 check #${checkId}` : ""}
      width={640}
      open={checkId !== null}
      onClose={onClose}
    >
      <Spin spinning={loading}>
        {detail && (
          <Space direction="vertical" style={{ width: "100%" }} size="middle">
            <Descriptions size="small" column={2} bordered>
              <Descriptions.Item label="任务">{taskName(detail.task_id)}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <StatusTag status={detail.status} />
              </Descriptions.Item>
              <Descriptions.Item label="触发方式">
                {detail.trigger === "schedule" ? "调度" : "手动"}
              </Descriptions.Item>
              <Descriptions.Item label="rclone jobid">{detail.job_id ?? "-"}</Descriptions.Item>
              <Descriptions.Item label="开始时间">{fmtTime(detail.started_at)}</Descriptions.Item>
              <Descriptions.Item label="结束时间">{fmtTime(detail.finished_at)}</Descriptions.Item>
              {result?.hashType && (
                <Descriptions.Item label="哈希类型">{result.hashType}</Descriptions.Item>
              )}
              {result?.status && (
                <Descriptions.Item label="rclone 结论">{result.status}</Descriptions.Item>
              )}
            </Descriptions>

            {detail.error && (
              <Alert
                type="error"
                showIcon
                message="检查未通过"
                description={<pre style={preStyle}>{detail.error}</pre>}
              />
            )}

            {result && (
              <Collapse
                size="small"
                items={CHECK_LIST_KEYS.filter((k) => (result[k.key] ?? []).length > 0).map((k) => ({
                  key: k.key,
                  label: `${k.label}（${(result[k.key] ?? []).length}）`,
                  children: (
                    <pre style={preStyle}>{(result[k.key] ?? []).slice(0, 2000).join("\n")}</pre>
                  ),
                }))}
              />
            )}

            {detail.status === "running" && detail.live?.status && (
              <Alert type="info" message="检查进行中" description={<pre style={preStyle}>{JSON.stringify(detail.live.status, null, 2)}</pre>} />
            )}
          </Space>
        )}
      </Spin>
    </Drawer>
  );
}

function ChecksPanel({
  checkTasks,
  onDetail,
}: {
  checkTasks: CheckTask[];
  onDetail: (id: number) => void;
}) {
  const [checks, setChecks] = useState<CheckRun[]>([]);
  const [loading, setLoading] = useState(false);
  const [taskId, setTaskId] = useState<number | undefined>();
  const [status, setStatus] = useState<RunStatus | undefined>();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setChecks(await listChecks({ task_id: taskId, status, limit: 100 }));
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, [taskId, status]);

  useEffect(() => {
    load();
    const timer = setInterval(load, 5000);
    return () => clearInterval(timer);
  }, [load]);

  const taskName = (id: number) => checkTasks.find((t) => t.id === id)?.name ?? `检查任务#${id}`;

  const columns = [
    { title: "ID", dataIndex: "id", width: 60 },
    { title: "检查任务", dataIndex: "task_id", render: (v: number) => taskName(v) },
    { title: "状态", dataIndex: "status", width: 90, render: (s: RunStatus) => <StatusTag status={s} /> },
    {
      title: "触发",
      dataIndex: "trigger",
      width: 80,
      render: (t: string) => (t === "schedule" ? "调度" : "手动"),
    },
    { title: "开始", dataIndex: "started_at", width: 120, render: fmtTime },
    { title: "结束", dataIndex: "finished_at", width: 120, render: fmtTime },
    {
      title: "结果摘要",
      render: (_: unknown, c: CheckRun) => {
        if (!c.result) return "-";
        const r = c.result;
        return `差异 ${r.differ?.length ?? 0} / 源缺失 ${r.missingOnSrc?.length ?? 0} / 目标缺失 ${r.missingOnDst?.length ?? 0} / 错误 ${r.error?.length ?? 0}`;
      },
    },
    {
      title: "错误",
      dataIndex: "error",
      ellipsis: true,
      render: (e: string | null, c: CheckRun) =>
        e ? (
          <a onClick={() => onDetail(c.id)}>
            <Typography.Text type="danger" ellipsis style={{ maxWidth: 200 }}>
              {e}
            </Typography.Text>
          </a>
        ) : (
          "-"
        ),
    },
    {
      title: "操作",
      width: 70,
      render: (_: unknown, c: CheckRun) => <a onClick={() => onDetail(c.id)}>详情</a>,
    },
  ];

  return (
    <Space direction="vertical" style={{ width: "100%" }} size="middle">
      <Space wrap>
        <Select
          allowClear
          placeholder="按检查任务过滤"
          style={{ width: 180 }}
          value={taskId}
          onChange={setTaskId}
          options={checkTasks.map((t) => ({ value: t.id, label: t.name }))}
        />
        <Select
          allowClear
          placeholder="按状态过滤"
          style={{ width: 140 }}
          value={status}
          onChange={setStatus}
          options={Object.entries(STATUS_META).map(([k, v]) => ({ value: k, label: v.label }))}
        />
        <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>
          查询（每 5 秒自动）
        </Button>
      </Space>
      <Table rowKey="id" loading={loading} columns={columns} dataSource={checks} pagination={{ pageSize: 20 }} />
    </Space>
  );
}

export default function Runs() {
  const [tasks, setTasks] = useState<SyncTask[]>([]);
  const [checkTasks, setCheckTasks] = useState<CheckTask[]>([]);
  const [detailId, setDetailId] = useState<number | null>(null);
  const [checkDetailId, setCheckDetailId] = useState<number | null>(null);

  useEffect(() => {
    listTasks()
      .then(setTasks)
      .catch((e) => message.error(errMessage(e)));
    listCheckTasks()
      .then(setCheckTasks)
      .catch((e) => message.error(errMessage(e)));
  }, []);

  return (
    <Card title="监控与记录">
      <Tabs
        items={[
          {
            key: "monitor",
            label: "实时监控",
            children: <MonitorPanel tasks={tasks} onDetail={setDetailId} />,
          },
          {
            key: "history",
            label: "历史记录",
            children: <HistoryPanel tasks={tasks} onDetail={setDetailId} />,
          },
          {
            key: "checks",
            label: "一致性检查",
            children: <ChecksPanel checkTasks={checkTasks} onDetail={setCheckDetailId} />,
          },
        ]}
      />
      <RunDetailDrawer runId={detailId} tasks={tasks} onClose={() => setDetailId(null)} />
      <CheckDetailDrawer
        checkId={checkDetailId}
        checkTasks={checkTasks}
        onClose={() => setCheckDetailId(null)}
      />
    </Card>
  );
}
