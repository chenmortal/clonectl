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
} from "antd";
import { useCallback, useEffect, useState } from "react";
import {
  createStorageSource,
  deleteStorageSource,
  errMessage,
  listStorageSources,
  updateStorageSource,
} from "../api";
import { BACKENDS, backendOf, StorageSource } from "../types";

interface FormValues {
  name: string;
  type: string;
  provider?: string;
  endpoint?: string;
  region?: string;
  /** Local backend: optional root directory. */
  root?: string;
  extra?: string;
}

export default function StorageSources() {
  const [items, setItems] = useState<StorageSource[]>([]);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<StorageSource | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<FormValues>();
  const typeWatch = Form.useWatch("type", form);
  const backend = backendOf(typeWatch ?? "");
  const hasProviders = !!backend?.providers?.length;

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await listStorageSources());
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

  const openEdit = (s: StorageSource) => {
    setEditing(s);
    const extra = (s.extra ?? {}) as Record<string, unknown>;
    const { provider, root, ...restExtra } = extra;
    form.setFieldsValue({
      name: s.name,
      type: s.type,
      provider: typeof provider === "string" ? provider : undefined,
      endpoint: s.endpoint ?? undefined,
      region: s.region ?? undefined,
      root: typeof root === "string" ? root : undefined,
      extra: Object.keys(restExtra).length
        ? JSON.stringify(restExtra, null, 2)
        : undefined,
    });
    setOpen(true);
  };

  const submit = async () => {
    const values = await form.validateFields();
    let extra: Record<string, unknown> = {};
    if (values.extra?.trim()) {
      try {
        extra = JSON.parse(values.extra);
      } catch {
        message.error("高级参数不是合法 JSON");
        return;
      }
    }
    if (values.provider) {
      extra.provider = values.provider;
    }
    if (values.root) {
      extra.root = values.root;
    }
    setSubmitting(true);
    try {
      if (editing) {
        await updateStorageSource(editing.id, {
          name: values.name,
          type: values.type,
          endpoint: values.endpoint || null,
          region: values.region || null,
          extra,
        });
        message.success("已更新");
      } else {
        await createStorageSource({
          name: values.name,
          type: values.type,
          endpoint: values.endpoint || null,
          region: values.region || null,
          extra,
        });
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

  return (
    <Card
      title="存储源（管理员维护）"
      extra={
        <Space>
          <Button type="primary" onClick={openCreate}>
            新建存储源
          </Button>
        </Space>
      }
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={
          typeWatch === "local"
            ? "本地文件系统：只需要填 root 目录（可选），数据源那里直接填路径。AK/SK 不填。"
            : "存储源只保存 type + endpoint 等连接信息；AK/SK 填在数据源里。"
        }
      />
      <Table<StorageSource>
        rowKey="id"
        loading={loading}
        dataSource={items}
        pagination={false}
        columns={[
          { title: "ID", dataIndex: "id", width: 60 },
          { title: "名称", dataIndex: "name" },
          {
            title: "类型",
            dataIndex: "type",
            render: (t: string) => <Tag>{backendOf(t)?.label ?? t}</Tag>,
          },
          { title: "Endpoint", dataIndex: "endpoint", ellipsis: true },
          { title: "Region", dataIndex: "region", width: 120 },
          {
            title: "操作",
            width: 160,
            render: (_, s) => (
              <Space>
                <Button size="small" onClick={() => openEdit(s)}>
                  编辑
                </Button>
                <Popconfirm
                  title="删除存储源?"
                  description="如果还有数据源引用它，删除会被拒绝。"
                  onConfirm={async () => {
                    try {
                      await deleteStorageSource(s.id);
                      message.success("已删除");
                      load();
                    } catch (e) {
                      message.error(errMessage(e));
                    }
                  }}
                >
                  <Button size="small" danger>
                    删除
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />

      <Drawer
        title={editing ? "编辑存储源" : "新建存储源"}
        open={open}
        width={520}
        onClose={() => setOpen(false)}
        extra={
          <Space>
            <Button onClick={() => setOpen(false)}>取消</Button>
            <Button type="primary" loading={submitting} onClick={submit}>
              保存
            </Button>
          </Space>
        }
      >
        <Form layout="vertical" form={form}>
          <Form.Item
            name="name"
            label="名称"
            rules={[
              { required: true, message: "请输入名称" },
              { pattern: /^[a-zA-Z0-9_-]+$/, message: "只允许字母数字下划线短横" },
            ]}
          >
            <Input placeholder="prod-s3" disabled={!!editing} />
          </Form.Item>
          <Form.Item name="type" label="类型" rules={[{ required: true }]}>
            <Select
              options={BACKENDS.map((b) => ({ value: b.type, label: b.label }))}
              disabled={!!editing}
            />
          </Form.Item>
          {hasProviders && (
            <Form.Item
              name="provider"
              label="Provider"
              tooltip='rclone S3 后端需要显式指定 provider,例如 "Alibaba"、"Minio"。会写入 extra.provider。'
            >
              <Select
                allowClear
                placeholder="选择 S3 provider (Alibaba / Minio / …)"
                options={backend!.providers!.map((p) => ({
                  value: p,
                  label: p,
                }))}
              />
            </Form.Item>
          )}
          {typeWatch !== "local" && (
            <>
              <Form.Item name="endpoint" label="Endpoint">
                <Input
                  placeholder={
                    backend?.fields.find((f) => f.key === "endpoint")?.placeholder
                  }
                />
              </Form.Item>
              <Form.Item name="region" label="Region">
                <Input
                  placeholder={
                    backend?.fields.find((f) => f.key === "region")?.placeholder
                  }
                />
              </Form.Item>
            </>
          )}
          {typeWatch === "local" && (
            <Form.Item
              name="root"
              label="Root 目录（可选）"
              tooltip="rclone local 后端的 root 参数。留空表示使用 rcd 主机整个文件系统,任务路径解析为绝对路径。"
            >
              <Input placeholder="/var/data" />
            </Form.Item>
          )}
          <Form.Item
            name="extra"
            label="高级参数 (JSON)"
            tooltip='非密 provider 参数,例如 {"provider":"Minio","no_check_bucket":"true"}'
          >
            <Input.TextArea rows={4} placeholder='{"provider":"Minio"}' />
          </Form.Item>
        </Form>
      </Drawer>
    </Card>
  );
}
