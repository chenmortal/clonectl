import { AuditOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Button,
  Card,
  Drawer,
  Form,
  Input,
  message,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
} from "antd";
import { useCallback, useEffect, useState } from "react";
import {
  createCheckTask,
  deleteCheckTask,
  errMessage,
  listCheckTasks,
  listDataSources,
  triggerCheckTask,
  updateCheckTask,
} from "../api";
import CheckOptionsInput from "../components/CheckOptionsInput";
import CronInput, { describeCron } from "../components/CronInput";
import { CheckOptions, CheckTask, DataSource } from "../types";

interface FormValues {
  name: string;
  src_data_source_id: number;
  src_path: string;
  dst_data_source_id: number;
  dst_path: string;
  cron?: string;
  enabled: boolean;
  check_options?: CheckOptions;
}

export default function CheckTasks() {
  const [checkTasks, setCheckTasks] = useState<CheckTask[]>([]);
  const [dataSources, setDataSources] = useState<DataSource[]>([]);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<CheckTask | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<FormValues>();

  const dsName = useCallback(
    (id: number | null | undefined) => {
      if (id == null) return "-";
      return dataSources.find((s) => s.id === id)?.name ?? `#${id}`;
    },
    [dataSources],
  );

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [ct, ds] = await Promise.all([listCheckTasks(), listDataSources()]);
      setCheckTasks(ct);
      setDataSources(ds);
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
    form.setFieldsValue({ enabled: true });
    setOpen(true);
  };

  const openEdit = (t: CheckTask) => {
    setEditing(t);
    form.setFieldsValue({
      name: t.name,
      src_data_source_id: t.src_data_source_id ?? undefined,
      src_path: t.src_path,
      dst_data_source_id: t.dst_data_source_id ?? undefined,
      dst_path: t.dst_path,
      cron: t.cron ?? undefined,
      enabled: t.enabled,
      check_options: t.check_options ?? {},
    });
    setOpen(true);
  };

  const submit = async () => {
    const values = await form.validateFields();
    const payload = {
      ...values,
      check_options: values.check_options ?? {},
      cron: values.cron?.trim() || null,
      src_storage_id: null,
      dst_storage_id: null,
    };
    setSubmitting(true);
    try {
      if (editing) {
        await updateCheckTask(editing.id, payload);
        message.success("已更新，调度已重注册");
      } else {
        await createCheckTask(payload);
        message.success("已创建");
      }
      setOpen(false);
      load();
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  const toggleEnabled = async (t: CheckTask, enabled: boolean) => {
    try {
      await updateCheckTask(t.id, { enabled });
      message.success(enabled ? "已启用" : "已停用");
      load();
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const trigger = async (t: CheckTask) => {
    try {
      const c = await triggerCheckTask(t.id);
      message.success(`已发起一致性检查：check #${c.id}（${c.status}）`);
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const remove = async (t: CheckTask) => {
    try {
      await deleteCheckTask(t.id);
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
      render: (_: unknown, t: CheckTask) =>
        `${dsName(t.src_data_source_id)}:${t.src_path} → ${dsName(t.dst_data_source_id)}:${t.dst_path}`,
    },
    {
      title: "周期",
      dataIndex: "cron",
      width: 140,
      render: (c: string | null) =>
        c ? (
          <Tooltip title={describeCron(c)}>
            <code>{c}</code>
          </Tooltip>
        ) : (
          "仅手动/前置"
        ),
    },
    {
      title: "启用",
      dataIndex: "enabled",
      width: 70,
      render: (v: boolean, t: CheckTask) => (
        <Switch size="small" checked={v} onChange={(c) => toggleEnabled(t, c)} />
      ),
    },
    {
      title: "操作",
      width: 200,
      render: (_: unknown, t: CheckTask) => (
        <Space>
          <Tooltip title="立即发起一次一致性检查">
            <a onClick={() => trigger(t)}>
              <AuditOutlined /> 检查
            </a>
          </Tooltip>
          <a onClick={() => openEdit(t)}>编辑</a>
          <Popconfirm
            title={`删除检查任务 ${t.name}？检查记录将级联删除`}
            onConfirm={() => remove(t)}
          >
            <a style={{ color: "#ff4d4f" }}>删除</a>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card
      title="检查任务"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          新建检查任务
        </Button>
      }
    >
      <Table rowKey="id" loading={loading} columns={columns} dataSource={checkTasks} pagination={false} />

      <Drawer
        title={editing ? `编辑检查任务：${editing.name}` : "新建检查任务"}
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
          <Form.Item name="name" label="检查任务名称" rules={[{ required: true }]}>
            <Input placeholder="daily-consistency-check" />
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
          <Form.Item
            name="cron"
            label="周期检查（可选）"
            tooltip="留空表示不启用周期检查（仍可手动触发或被同步任务作为前置检查引用）"
            rules={[
              {
                validator: (_, v: string | undefined) =>
                  !v?.trim() || /^\S+(\s+\S+){4}$/.test(v.trim())
                    ? Promise.resolve()
                    : Promise.reject(new Error("需为 5 段 cron 表达式")),
              },
            ]}
          >
            <CronInput />
          </Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="check_options" label="检查参数">
            <CheckOptionsInput />
          </Form.Item>
        </Form>
      </Drawer>
    </Card>
  );
}
