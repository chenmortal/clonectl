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
  Tooltip,
} from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  createBinding,
  createDataSource,
  deleteBinding,
  deleteDataSource,
  errMessage,
  listBindings,
  listDataSources,
  listStorageSources,
  listUsers,
  updateBinding,
  updateDataSource,
  verifyDataSource,
  type UserOut,
} from "../api";
import {
  DataSource,
  DataSourceBinding,
  DataSourcePermission,
  DataSourceVerifyOut,
  StorageSource,
} from "../types";

interface FormValues {
  name: string;
  storage_source_id: number;
  path: string;
  access_key_id?: string;
  secret_access_key?: string;
  description?: string;
}

const REDACTED = "••••••••";

export default function DataSources() {
  const [items, setItems] = useState<DataSource[]>([]);
  const [sources, setSources] = useState<StorageSource[]>([]);
  const [users, setUsers] = useState<UserOut[]>([]);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<DataSource | null>(null);
  const [revealSecret, setRevealSecret] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [verifyResult, setVerifyResult] = useState<{
    id: number;
    result: DataSourceVerifyOut;
  } | null>(null);
  const [form] = Form.useForm<FormValues>();
  const [activeDsId, setActiveDsId] = useState<number | null>(null);
  const [bindings, setBindings] = useState<DataSourceBinding[]>([]);
  const [bindingPerm, setBindingPerm] = useState<DataSourcePermission>("read");
  const [bindingUserId, setBindingUserId] = useState<number | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [dss, srcs, us] = await Promise.all([
        listDataSources(),
        listStorageSources(),
        listUsers().catch(() => [] as UserOut[]),
      ]);
      setItems(dss);
      setSources(srcs);
      setUsers(us);
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const sourcesById = useMemo(() => {
    const m = new Map<number, StorageSource>();
    sources.forEach((s) => m.set(s.id, s));
    return m;
  }, [sources]);

  const storageSourceIdWatch = Form.useWatch("storage_source_id", form);
  // Hide AK/SK for local-FS data sources (rclone's local backend
  // ignores credentials entirely).
  const selectedSourceType =
    sourcesById.get(storageSourceIdWatch ?? -1)?.type;
  const isLocal = selectedSourceType === "local";

  const openCreate = () => {
    setEditing(null);
    setRevealSecret(false);
    form.resetFields();
    if (sources[0]) form.setFieldsValue({ storage_source_id: sources[0].id });
    setOpen(true);
  };

  const openEdit = (ds: DataSource) => {
    setEditing(ds);
    setRevealSecret(false);
    form.setFieldsValue({
      name: ds.name,
      storage_source_id: ds.storage_source_id,
      path: ds.path,
      access_key_id: ds.access_key_id,
      secret_access_key: ds.secret_access_key,
      description: ds.description ?? undefined,
    });
    setOpen(true);
  };

  const submit = async () => {
    const values = await form.validateFields();
    setSubmitting(true);
    try {
      if (editing) {
        await updateDataSource(editing.id, {
          name: values.name,
          storage_source_id: values.storage_source_id,
          path: values.path,
          access_key_id: values.access_key_id || null,
          secret_access_key: values.secret_access_key || null,
          description: values.description || null,
        });
        message.success("已更新");
      } else {
        await createDataSource({
          name: values.name,
          storage_source_id: values.storage_source_id,
          path: values.path,
          access_key_id: values.access_key_id || null,
          secret_access_key: values.secret_access_key || null,
          description: values.description || undefined,
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

  const doVerify = async (ds: DataSource) => {
    try {
      const result = await verifyDataSource(ds.id);
      setVerifyResult({ id: ds.id, result });
      load();
      if (result.error) {
        message.warning(`部分失败:${result.error}`);
      } else {
        message.success("验证通过");
      }
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const openBindings = async (ds: DataSource) => {
    setActiveDsId(ds.id);
    try {
      setBindings(await listBindings(ds.id));
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const addBinding = async () => {
    if (activeDsId == null || bindingUserId == null) return;
    try {
      await createBinding(activeDsId, {
        user_id: bindingUserId,
        permission: bindingPerm,
      });
      setBindings(await listBindings(activeDsId));
      setBindingUserId(null);
      message.success("已添加绑定");
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const changePerm = async (b: DataSourceBinding, p: DataSourcePermission) => {
    if (activeDsId == null) return;
    try {
      await updateBinding(activeDsId, b.id, { permission: p });
      setBindings(await listBindings(activeDsId));
      message.success("已更新");
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const removeBinding = async (b: DataSourceBinding) => {
    if (activeDsId == null) return;
    try {
      await deleteBinding(activeDsId, b.id);
      setBindings(await listBindings(activeDsId));
      message.success("已删除绑定");
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  return (
    <Card
      title="数据源"
      extra={
        <Space>
          <Button type="primary" onClick={openCreate}>
            新建数据源
          </Button>
        </Space>
      }
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={
          isLocal
            ? "本地文件系统数据源：只需要路径,不需要 AK/SK。"
            : "数据源 = 存储源 + 路径 + 凭据。先在「存储源」里建好模板,再在这里挂上 AK/SK。"
        }
      />
      <Table<DataSource>
        rowKey="id"
        loading={loading}
        dataSource={items}
        pagination={false}
        columns={[
          { title: "ID", dataIndex: "id", width: 60 },
          { title: "名称", dataIndex: "name" },
          {
            title: "存储源",
            render: (_, ds) => {
              const src = sourcesById.get(ds.storage_source_id);
              return src ? `${src.name} (${src.type})` : `#${ds.storage_source_id}`;
            },
          },
          { title: "路径", dataIndex: "path", ellipsis: true },
          {
            title: "AK",
            dataIndex: "access_key_id",
            ellipsis: true,
            width: 180,
          },
          {
            title: "上次验证",
            width: 160,
            render: (_, ds) => {
              if (!ds.last_verified_at) return <Tag>未验证</Tag>;
              const ok = ds.last_verified_ok;
              return (
                <Tooltip title={ds.last_verified_at}>
                  <Tag color={ok ? "green" : "red"}>
                    {ok ? "OK" : "失败"}
                  </Tag>
                </Tooltip>
              );
            },
          },
          {
            title: "操作",
            width: 280,
            render: (_, ds) => (
              <Space>
                <Button size="small" onClick={() => openEdit(ds)}>
                  编辑
                </Button>
                <Button size="small" onClick={() => openBindings(ds)}>
                  权限
                </Button>
                <Button size="small" onClick={() => doVerify(ds)}>
                  验证
                </Button>
                <Popconfirm
                  title="删除数据源?"
                  onConfirm={async () => {
                    try {
                      await deleteDataSource(ds.id);
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
        title={editing ? "编辑数据源" : "新建数据源"}
        open={open}
        width={560}
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
            rules={[{ required: true, message: "请输入名称" }]}
          >
            <Input placeholder="my-bucket" />
          </Form.Item>
          <Form.Item
            name="storage_source_id"
            label="存储源"
            rules={[{ required: true, message: "请选择存储源" }]}
          >
            <Select
              options={sources.map((s) => ({
                value: s.id,
                label: `${s.name} (${s.type})`,
              }))}
            />
          </Form.Item>
          <Form.Item
            name="path"
            label="路径"
            rules={[{ required: true, message: "请输入路径" }]}
          >
            <Input placeholder="bucket/sub/dir" />
          </Form.Item>
          {!isLocal && (
            <>
              <Form.Item
                name="access_key_id"
                label="Access Key ID"
                rules={[{ required: true }]}
              >
                <Input />
              </Form.Item>
              <Form.Item
                name="secret_access_key"
                label={
                  <Space>
                    Secret Access Key
                    {editing && (
                      <Button
                        type="link"
                        size="small"
                        onClick={() => setRevealSecret((r) => !r)}
                      >
                        {revealSecret ? "隐藏" : "显示"}
                      </Button>
                    )}
                  </Space>
                }
                rules={[{ required: true }]}
              >
                <Input type={revealSecret ? "text" : "password"} />
              </Form.Item>
            </>
          )}
          <Form.Item name="description" label="说明">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Drawer>

      <Drawer
        title={`权限 - 数据源 #${activeDsId ?? ""}`}
        open={activeDsId != null}
        width={520}
        onClose={() => setActiveDsId(null)}
      >
        <Space style={{ marginBottom: 12 }}>
          <Select
            placeholder="选择用户"
            style={{ width: 200 }}
            value={bindingUserId ?? undefined}
            onChange={setBindingUserId}
            options={users.map((u) => ({ value: u.id, label: u.username }))}
          />
          <Select
            value={bindingPerm}
            onChange={setBindingPerm}
            options={[
              { value: "read", label: "只读" },
              { value: "write", label: "读写" },
              { value: "admin", label: "管理员" },
            ]}
            style={{ width: 120 }}
          />
          <Button type="primary" onClick={addBinding}>
            添加
          </Button>
        </Space>
        <Table<DataSourceBinding>
          rowKey="id"
          dataSource={bindings}
          pagination={false}
          columns={[
            {
              title: "用户",
              dataIndex: "user_id",
              render: (uid: number) =>
                users.find((u) => u.id === uid)?.username ?? `#${uid}`,
            },
            {
              title: "权限",
              dataIndex: "permission",
              render: (p: DataSourcePermission, b) => (
                <Select
                  value={p}
                  style={{ width: 110 }}
                  onChange={(v) => changePerm(b, v)}
                  options={[
                    { value: "read", label: "只读" },
                    { value: "write", label: "读写" },
                    { value: "admin", label: "管理员" },
                  ]}
                />
              ),
            },
            {
              title: "操作",
              width: 80,
              render: (_, b) => (
                <Popconfirm title="删除绑定?" onConfirm={() => removeBinding(b)}>
                  <Button size="small" danger>
                    删除
                  </Button>
                </Popconfirm>
              ),
            },
          ]}
        />
      </Drawer>

      {verifyResult && (
        <Alert
          style={{ marginTop: 16 }}
          type={
            verifyResult.result.read_ok && verifyResult.result.write_ok
              ? "success"
              : "warning"
          }
          showIcon
          message={`验证 #${verifyResult.id}`}
          description={
            <>
              read_ok={String(verifyResult.result.read_ok)} write_ok=
              {String(verifyResult.result.write_ok)}
              {verifyResult.result.error && (
                <pre style={{ marginTop: 8 }}>{verifyResult.result.error}</pre>
              )}
            </>
          }
        />
      )}
    </Card>
  );
}

// Note: REDACTED is a sentinel for future UI masking; current schema returns
// the secret in full per backend contract (DataSource.secret_access_key is
// always non-empty) and the UI exposes a "Show" toggle.
export const __REDACTED = REDACTED;
