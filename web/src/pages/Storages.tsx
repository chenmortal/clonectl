import { PlusOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Drawer,
  Form,
  Input,
  message,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import dayjs from "dayjs";
import { useCallback, useEffect, useMemo, useState } from "react";
import { createStorage, deleteStorage, errMessage, listStorages, updateStorage } from "../api";
import { BACKENDS, backendOf, StorageConfig } from "../types";

interface FormValues {
  name: string;
  type: string;
  provider?: string;
  fields?: Record<string, string>;
  extra?: string;
}

export default function Storages() {
  const [storages, setStorages] = useState<StorageConfig[]>([]);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<StorageConfig | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<FormValues>();
  const typeWatch = Form.useWatch("type", form);
  const backend = backendOf(typeWatch ?? "");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setStorages(await listStorages());
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
    form.setFieldsValue({ type: "s3" });
    setOpen(true);
  };

  const openEdit = (s: StorageConfig) => {
    setEditing(s);
    const b = backendOf(s.type);
    const fields: Record<string, string> = {};
    const known = new Set<string>(["provider", ...(b?.fields.map((f) => f.key) ?? [])]);
    const extra: Record<string, string> = {};
    Object.entries(s.parameters).forEach(([k, v]) => {
      if (known.has(k)) fields[k] = v;
      else extra[k] = v;
    });
    form.setFieldsValue({
      name: s.name,
      type: s.type,
      provider: s.parameters.provider,
      fields,
      extra: Object.keys(extra).length ? JSON.stringify(extra, null, 2) : undefined,
    });
    setOpen(true);
  };

  const submit = async () => {
    const values = await form.validateFields();
    const parameters: Record<string, string> = {};
    if (values.provider) parameters.provider = values.provider;
    Object.entries(values.fields ?? {}).forEach(([k, v]) => {
      if (v) parameters[k] = v;
    });
    if (values.extra?.trim()) {
      try {
        Object.assign(parameters, JSON.parse(values.extra));
      } catch {
        message.error("高级参数不是合法 JSON");
        return;
      }
    }
    setSubmitting(true);
    try {
      if (editing) {
        await updateStorage(editing.id, { name: values.name, type: values.type, parameters });
        message.success("已更新并同步到 rclone rcd");
      } else {
        await createStorage({ name: values.name, type: values.type, parameters });
        message.success("已创建并同步到 rclone rcd");
      }
      setOpen(false);
      load();
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  const remove = async (s: StorageConfig) => {
    try {
      await deleteStorage(s.id);
      message.success("已删除，并已从 rclone rcd 移除");
      load();
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const columns = useMemo(
    () => [
      { title: "ID", dataIndex: "id", width: 60 },
      { title: "名称", dataIndex: "name" },
      {
        title: "类型",
        dataIndex: "type",
        width: 100,
        render: (t: string) => <Tag color="blue">{t}</Tag>,
      },
      {
        title: "关键参数",
        render: (_: unknown, s: StorageConfig) => (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {[s.parameters.provider, s.parameters.endpoint, s.parameters.region]
              .filter(Boolean)
              .join(" · ") || "-"}
          </Typography.Text>
        ),
      },
      {
        title: "创建时间",
        dataIndex: "created_at",
        width: 170,
        render: (v: string) => dayjs(v).format("YYYY-MM-DD HH:mm:ss"),
      },
      {
        title: "操作",
        width: 140,
        render: (_: unknown, s: StorageConfig) => (
          <Space>
            <a onClick={() => openEdit(s)}>编辑</a>
            <Popconfirm title={`删除存储 ${s.name}？将同时从 rclone 移除`} onConfirm={() => remove(s)}>
              <a style={{ color: "#ff4d4f" }}>删除</a>
            </Popconfirm>
          </Space>
        ),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [storages],
  );

  return (
    <Card
      title="存储管理（旧版）"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          新建存储
        </Button>
      }
    >
      <Alert
        type="warning"
        showIcon
        style={{ marginBottom: 16 }}
        message="本页是旧版存储管理的只读视图。新建/编辑/删除已停用(返回 410 Gone)。请改用「存储源」+「数据源」:存储源保存 type/endpoint,数据源挂 AK/SK + 路径。"
      />
      <Table rowKey="id" loading={loading} columns={columns} dataSource={storages} pagination={false} />

      <Drawer
        title={editing ? `编辑存储：${editing.name}` : "新建存储"}
        width={480}
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
          <Form.Item
            name="name"
            label="Remote 名称"
            rules={[{ required: true, pattern: /^[a-zA-Z0-9_-]+$/, message: "字母/数字/下划线/横线" }]}
          >
            <Input placeholder="local-s3" />
          </Form.Item>
          <Form.Item name="type" label="存储类型" rules={[{ required: true }]}>
            <Select
              options={BACKENDS.map((b) => ({ value: b.type, label: b.label }))}
              onChange={() => form.setFieldValue("provider", undefined)}
            />
          </Form.Item>
          {backend?.providers && (
            <Form.Item name="provider" label="Provider" rules={[{ required: true }]}>
              <Select options={backend.providers.map((p) => ({ value: p, label: p }))} />
            </Form.Item>
          )}
          {backend?.fields.map((f) => (
            <Form.Item
              key={f.key}
              name={["fields", f.key]}
              label={f.label}
              rules={f.required ? [{ required: true, message: `请输入 ${f.label}` }] : undefined}
            >
              {f.secret ? <Input.Password placeholder={f.placeholder} /> : <Input placeholder={f.placeholder} />}
            </Form.Item>
          ))}
          <Form.Item
            name="extra"
            label="高级参数（JSON，可选）"
            tooltip="其余 rclone 参数，如 {&quot;no_check_bucket&quot;:&quot;true&quot;}"
          >
            <Input.TextArea rows={4} placeholder='{"key": "value"}' />
          </Form.Item>
        </Form>
      </Drawer>
    </Card>
  );
}
