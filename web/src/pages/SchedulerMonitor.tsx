import {
  CaretRightOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  DashboardOutlined,
  ReloadOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import {
  Badge,
  Button,
  Card,
  Drawer,
  Space,
  Statistic,
  Table,
  Tag,
  Timeline,
  Tooltip,
  Typography,
  message,
} from "antd";
import { useCallback, useEffect, useState } from "react";
import {
  errMessage,
  getSchedulerJob,
  listSchedulerJobs,
  runSchedulerJob,
  schedulerOverview,
  type SchedulerJob,
  type SchedulerJobDetail,
  type SchedulerOverview,
} from "../api";

const { Text } = Typography;

const KIND_LABEL: Record<string, string> = {
  task: "同步任务",
  check: "检查任务",
  internal: "内部",
};

function fmtTime(v: string | null): string {
  if (!v || v.startsWith("0001-01-01")) return "-";
  return v.replace("T", " ").replace("Z", "");
}

export default function SchedulerMonitor() {
  const [jobs, setJobs] = useState<SchedulerJob[]>([]);
  const [overview, setOverview] = useState<SchedulerOverview | null>(null);
  const [loading, setLoading] = useState(false);
  const [detail, setDetail] = useState<SchedulerJobDetail | null>(null);
  const [messageApi, contextHolder] = message.useMessage();

  const refresh = useCallback(async () => {
    try {
      const [j, o] = await Promise.all([listSchedulerJobs(), schedulerOverview()]);
      setJobs(j.jobs);
      setOverview(o);
    } catch (e) {
      messageApi.error(errMessage(e));
    }
  }, [messageApi]);

  // List + overview every 5s; the open detail drawer refreshes every 2s.
  useEffect(() => {
    setLoading(true);
    refresh().finally(() => setLoading(false));
    const timer = setInterval(refresh, 5000);
    return () => clearInterval(timer);
  }, [refresh]);

  useEffect(() => {
    if (!detail) return;
    const timer = setInterval(() => {
      getSchedulerJob(detail.id)
        .then(setDetail)
        .catch(() => {});
    }, 2000);
    return () => clearInterval(timer);
  }, [detail]);

  const runNow = async (job: SchedulerJob) => {
    try {
      const out = await runSchedulerJob(job.id);
      messageApi.success(
        out.kind === "task"
          ? `已触发同步任务 #${out.task_id}（run #${out.run_id ?? "?"}）`
          : `已触发检查任务 #${out.task_id}（check #${out.check_id ?? "?"}）`,
      );
      refresh();
    } catch (e) {
      messageApi.error(errMessage(e));
    }
  };

  const columns = [
    {
      title: "任务",
      dataIndex: "name",
      key: "name",
      render: (_: unknown, j: SchedulerJob) => (
        <Space>
          {j.is_running ? (
            <Badge status="processing" />
          ) : j.consecutive_failures > 0 ? (
            <Badge status="error" />
          ) : (
            <Badge status="success" />
          )}
          <Text code>{j.name}</Text>
          <Tag>{KIND_LABEL[j.kind] ?? j.kind}</Tag>
        </Space>
      ),
    },
    {
      title: "计划",
      dataIndex: "schedule",
      key: "schedule",
      render: (v: string) => <Text code>{v || "-"}</Text>,
    },
    {
      title: "下次执行",
      dataIndex: "next_run",
      key: "next_run",
      render: (v: string | null) => fmtTime(v),
    },
    {
      title: "上次开始",
      dataIndex: "last_run_started_at",
      key: "last_run_started_at",
      render: (v: string | null) => fmtTime(v),
    },
    {
      title: "状态",
      key: "state",
      render: (_: unknown, j: SchedulerJob) =>
        j.is_running ? (
          <Tag icon={<SyncOutlined spin />} color="processing">
            运行中
          </Tag>
        ) : j.consecutive_failures > 0 ? (
          <Tooltip title={j.last_error || "连续失败"}>
            <Tag icon={<CloseCircleOutlined />} color="error">
              连续失败 {j.consecutive_failures}
            </Tag>
          </Tooltip>
        ) : (
          <Tag icon={<CheckCircleOutlined />} color="success">
            正常
          </Tag>
        ),
    },
    {
      title: "执行 / 失败",
      key: "counts",
      render: (_: unknown, j: SchedulerJob) => (
        <span>
          {j.run_count} /{" "}
          <Text type={j.fail_count > 0 ? "danger" : undefined}>{j.fail_count}</Text>
        </span>
      ),
    },
    {
      title: "操作",
      key: "actions",
      render: (_: unknown, j: SchedulerJob) => (
        <Space>
          <Button
            size="small"
            icon={<CaretRightOutlined />}
            disabled={j.kind === "internal"}
            onClick={() => runNow(j)}
          >
            运行
          </Button>
          <Button size="small" onClick={() => getSchedulerJob(j.id).then(setDetail)}>
            详情
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div style={{ padding: 24 }}>
      {contextHolder}
      <Card style={{ marginBottom: 16 }}>
        <Space size={32} wrap>
          <Statistic
            title="节点角色"
            valueRender={() => (
              <span>
                {overview?.is_leader ? (
                  <Tag color="green">Leader</Tag>
                ) : (
                  <Tag color="orange">Standby</Tag>
                )}
                {overview?.leader_id && (
                  <Text type="secondary" style={{ marginLeft: 8 }}>
                    leader: {overview.leader_id}
                  </Text>
                )}
              </span>
            )}
          />
          <Statistic title="总任务数" value={overview?.jobs_total ?? "-"} />
          <Statistic title="用户任务" value={overview?.user_jobs ?? "-"} />
          <Statistic
            title="正在运行"
            value={overview?.running_now ?? "-"}
            valueStyle={overview?.running_now ? { color: "#1677ff" } : undefined}
          />
          <Button icon={<ReloadOutlined />} loading={loading} onClick={refresh}>
            刷新
          </Button>
        </Space>
      </Card>

      <Table
        rowKey="id"
        columns={columns}
        dataSource={jobs}
        loading={loading && jobs.length === 0}
        pagination={false}
        footer={() => (
          <Text type="secondary" style={{ fontSize: 12 }}>
            <DashboardOutlined /> 执行历史保存在内存中，服务重启后清零；手动「运行」走与
            trigger 相同的服务层（有并发保护与运行记录）。
          </Text>
        )}
      />

      <Drawer
        title={detail ? `任务详情：${detail.name}` : ""}
        width={560}
        open={detail !== null}
        onClose={() => setDetail(null)}
      >
        {detail && (
          <>
            <Space direction="vertical" size={4} style={{ marginBottom: 16 }}>
              <Text>
                计划：<Text code>{detail.schedule || "-"}</Text>
              </Text>
              <Text>
                标签：
                {detail.tags.map((t) => (
                  <Tag key={t}>{t}</Tag>
                ))}
              </Text>
              {detail.last_error && (
                <Text type="danger">最近错误：{detail.last_error}</Text>
              )}
            </Space>

            <Typography.Title level={5}>即将执行</Typography.Title>
            <Timeline
              items={detail.next_runs.map((t) => ({
                children: fmtTime(t),
                color: "blue",
              }))}
            />

            <Typography.Title level={5}>最近执行</Typography.Title>
            {detail.executions.length === 0 ? (
              <Text type="secondary">暂无记录（监控状态保存在内存中）</Text>
            ) : (
              <Timeline
                items={[...detail.executions].reverse().map((e) => ({
                  color: e.error ? "red" : "green",
                  children: (
                    <span>
                      {fmtTime(e.started_at)} · {e.duration_ms}ms
                      {e.error && (
                        <Text type="danger" style={{ marginLeft: 8 }}>
                          {e.error}
                        </Text>
                      )}
                    </span>
                  ),
                }))}
              />
            )}
          </>
        )}
      </Drawer>
    </div>
  );
}
