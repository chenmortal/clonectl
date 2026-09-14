import { PlayCircleOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Button,
  Card,
  Drawer,
  Form,
  Input,
  message,
  Popconfirm,
  Radio,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from "antd";
import { useCallback, useEffect, useState } from "react";
import {
  createTask,
  deleteTask,
  errMessage,
  listCheckTasks,
  listDataSources,
  listTasks,
  triggerTask,
  updateTask,
} from "../api";
import CronInput, { describeCron } from "../components/CronInput";
import RcloneOptionsInput from "../components/RcloneOptionsInput";
import { CheckTask, DataSource, RcloneOptions, SyncTask } from "../types";

interface FormValues {
  name: string;
  src_data_source_id: number;
  src_path: string;
  dst_data_source_id: number;
  dst_path: string;
  mode: "sync" | "copy";
  cron: string;
  enabled: boolean;
  rclone_options?: RcloneOptions;
  pre_check_task_id?: number | null;
}

export default function Tasks() {
  const [tasks, setTasks] = useState<SyncTask[]>([]);
  const [dataSources, setDataSources] = useState<DataSource[]>([]);
  const [checkTasks, setCheckTasks] = useState<CheckTask[]>([]);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<SyncTask | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<FormValues>();

  const dsName = useCallback(
    (id: number | null | undefined) => {
      if (id == null) return "-";
      return dataSources.find((s) => s.id === id)?.name ?? `#${id}`;
    },
    [dataSources],
  );

  const checkTaskName = useCallback(
    (id: number | null) =>
      id === null ? null : checkTasks.find((c) => c.id === id)?.name ?? `#${id}`,
    [checkTasks],
  );

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [t, ds, ct] = await Promise.all([
        listTasks(),
        listDataSources(),
        listCheckTasks(),
      ]);
      setTasks(t);
      setDataSources(ds);
      setCheckTasks(ct);
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ mode: "sync", enabled: true, cron: "0 3 * * *" });
    setOpen(true);
  };

  const openEdit = (t: SyncTask) => {
    setEditing(t);
    // Map v2 fields; legacy src_storage_id/dst_storage_id stays in the
    // backend row but the new form uses data_source_id only.
    form.setFieldsValue({
      name: t.name,
      src_data_source_id: t.src_data_source_id ?? undefined,
      src_path: t.src_path,
      dst_data_source_id: t.dst_data_source_id ?? undefined,
      dst_path: t.dst_path,
      mode: t.mode,
      cron: t.cron,
      enabled: t.enabled,
      rclone_options: t.rclone_options ?? {},
      pre_check_task_id: t.pre_check_task_id ?? undefined,
    });
    setOpen(true);
  };

  const submit = async () => {
    const values = await form.validateFields();
    const payload = {
      ...values,
      rclone_options: values.rclone_options ?? {},
      pre_check_task_id: values.pre_check_task_id ?? null,
      src_storage_id: null,
      dst_storage_id: null,
    };
    setSubmitting(true);
    try {
      if (editing) {
        await updateTask(editing.id, payload);
        message.success("已更新，调度已重注册");
      } else {
        await createTask(payload);
        message.success("已创建，调度已注册");
      }
      setOpen(false);
      load();
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  const toggleEnabled = async (t: SyncTask, enabled: boolean) => {
    try {
      await updateTask(t.id, { enabled });
      message.success(enabled ? "已启用" : "已停用");
      load();
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const trigger = async (t: SyncTask) => {
    try {
      const run = await triggerTask(t.id);
      message.success(`已触发：run #${run.id}（${run.status}）`);
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const remove = async (t: SyncTask) => {
    try {
      await deleteTask(t.id);
      message.success("已删除");
      load();
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const columns = [
    { title: "ID", dataIndex: "id", width: 60 },
    { title: "名称", dataIndex: "name" },
    {
      title: "源 → 目标",
      render: (_: unknown, t: SyncTask) =>
        `${dsName(t.src_data_source_id)}:${t.src_path} → ${dsName(t.dst_data_source_id)}:${t.dst_path}`,
    },
    {
      title: "模式",
      dataIndex: "mode",
      width: 80,
      render: (m: string) => <Tag color={m === "sync" ? "volcano" : "geekblue"}>{m}</Tag>,
    },
    {
      title: "Cron",
      dataIndex: "cron",
      width: 140,
      render: (c: string) => (
        <Tooltip title={describeCron(c)}>
          <code>{c}</code>
        </Tooltip>
      ),
    },
    {
      title: "前置检查",
      width: 130,
      render: (_: unknown, t: SyncTask) => {
        const name = checkTaskName(t.pre_check_task_id);
        return name ? (
          <Tooltip title="每次同步前执行该检查任务：两端一致则跳过同步，发现差异则继续同步，检查出错则阻止同步">
            <Tag color="purple">{name}</Tag>
          </Tooltip>
        ) : (
          "-"
        );
      },
    },
    {
      title: "启用",
      dataIndex: "enabled",
      width: 70,
      render: (v: boolean, t: SyncTask) => <Switch size="small" checked={v} onChange={(c) => toggleEnabled(t, c)} />,
    },
    {
      title: "操作",
      width: 190,
      render: (_: unknown, t: SyncTask) => (
        <Space>
          <Tooltip title="立即触发一次同步">
            <a onClick={() => trigger(t)}>
              <PlayCircleOutlined /> 同步
            </a>
          </Tooltip>
          <a onClick={() => openEdit(t)}>编辑</a>
          <Popconfirm title={`删除任务 ${t.name}？历史记录将级联删除`} onConfirm={() => remove(t)}>
            <a style={{ color: "#ff4d4f" }}>删除</a>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card
      title="同步任务"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          新建任务
        </Button>
      }
    >
      <Table rowKey="id" loading={loading} columns={columns} dataSource={tasks} pagination={false} />

      <Drawer
        title={editing ? `编辑任务：${editing.name}` : "新建任务"}
        width={760}
        open={open}
        onClose={() => setOpen(false)}
        destroyOnClose
        extra={
          <Button type="primary" loading={submitting} onClick={submit}>
            保存
          </Button>
        }
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="任务名称" rules={[{ required: true }]}>
            <Input placeholder="nightly-backup" />
          </Form.Item>
          <Space.Compact style={{ width: "100%" }}>
            <Form.Item name="src_data_source_id" label="源数据源" rules={[{ required: true }]} style={{ width: "45%" }}>
              <Select
                options={dataSources.map((d) => ({
                  value: d.id,
                  label: `${d.name} (${d.path})`,
                }))}
              />
            </Form.Item>
            <Form.Item name="src_path" label="源路径" rules={[{ required: true }]} style={{ width: "55%", marginLeft: 8 }}>
              <Input placeholder="/data" />
            </Form.Item>
          </Space.Compact>
          <Space.Compact style={{ width: "100%" }}>
            <Form.Item name="dst_data_source_id" label="目标数据源" rules={[{ required: true }]} style={{ width: "45%" }}>
              <Select
                options={dataSources.map((d) => ({
                  value: d.id,
                  label: `${d.name} (${d.path})`,
                }))}
              />
            </Form.Item>
            <Form.Item name="dst_path" label="目标路径" rules={[{ required: true }]} style={{ width: "55%", marginLeft: 8 }}>
              <Input placeholder="/backup" />
            </Form.Item>
          </Space.Compact>
          <Form.Item name="mode" label="同步模式" rules={[{ required: true }]}>
            <Radio.Group>
              <Radio value="sync">sync（镜像，删除目标多余文件）</Radio>
              <Radio value="copy">copy（仅复制，不删除）</Radio>
            </Radio.Group>
          </Form.Item>
          <Form.Item
            name="cron"
            label="调度周期"
            rules={[
              { required: true, message: "请选择预设或输入 cron 表达式" },
              { pattern: /^\S+(\s+\S+){4}$/, message: "需为 5 段 cron 表达式" },
            ]}
          >
            <CronInput />
          </Form.Item>
          <Form.Item name="enabled" label="启用调度" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="rclone_options" label="rclone 同步参数（可选）" tooltip="以 _config 下发给 rclone RC，等同 curl 调用 sync/sync 的 _config 字段">
            <RcloneOptionsInput />
          </Form.Item>
          <Form.Item
            name="pre_check_task_id"
            label="同步前一致性检查（可选）"
            tooltip="每次同步前先执行所选检查任务：两端一致则跳过本次同步；发现差异则继续同步；检查本身出错则阻止同步"
          >
            <Select
              allowClear
              placeholder="不启用"
              options={checkTasks.map((c) => ({ value: c.id, label: c.name }))}
            />
          </Form.Item>
        </Form>
      </Drawer>
    </Card>
  );
}
