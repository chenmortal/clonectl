import {
  Button,
  Card,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useCallback, useEffect, useState } from "react";
import {
  errMessage,
  type UserOut,
  type UserRole,
  createUser,
  deleteUser,
  listUsers,
  me,
  resetPassword,
  updateUser,
} from "../api";

const { Title } = Typography;

const ROLES: UserRole[] = ["admin", "edit", "view"];
const ROLE_LABEL: Record<UserRole, string> = {
  admin: "管理员",
  edit: "可编辑",
  view: "只读",
};
const ROLE_COLOR: Record<UserRole, string> = {
  admin: "red",
  edit: "blue",
  view: "default",
};

export default function Users() {
  const [users, setUsers] = useState<UserOut[]>([]);
  const [me_, setMe] = useState<{ id: number; role: UserRole } | null>(null);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<UserOut | null>(null);
  const [creating, setCreating] = useState(false);
  const [resetting, setResetting] = useState<UserOut | null>(null);
  const [form] = Form.useForm();
  const [pwForm] = Form.useForm();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [list, whoami] = await Promise.all([listUsers(), me()]);
      setUsers(list);
      setMe({ id: whoami.id, role: whoami.role });
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const onCreate = async (values: {
    username: string;
    password: string;
    role: UserRole;
  }) => {
    try {
      await createUser(values);
      message.success("已创建");
      setCreating(false);
      form.resetFields();
      load();
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const onUpdate = async (
    id: number,
    data: { role?: UserRole; disabled?: boolean },
  ) => {
    try {
      await updateUser(id, data);
      message.success("已更新");
      setEditing(null);
      load();
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const onDelete = async (id: number) => {
    try {
      await deleteUser(id);
      message.success("已删除");
      load();
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  const onReset = async (values: { new_password: string }) => {
    if (!resetting) return;
    try {
      await resetPassword(resetting.id, values.new_password);
      message.success("密码已重置");
      setResetting(null);
      pwForm.resetFields();
    } catch (e) {
      message.error(errMessage(e));
    }
  };

  return (
    <Card
      title={
        <Space>
          <Title level={4} style={{ margin: 0 }}>
            用户管理
          </Title>
        </Space>
      }
      extra={
        <Button type="primary" onClick={() => setCreating(true)}>
          新建用户
        </Button>
      }
    >
      <Table<UserOut>
        rowKey="id"
        loading={loading}
        dataSource={users}
        pagination={false}
        columns={[
          { title: "ID", dataIndex: "id", width: 60 },
          { title: "用户名", dataIndex: "username" },
          {
            title: "角色",
            dataIndex: "role",
            render: (r: UserRole) => (
              <Tag color={ROLE_COLOR[r]}>{ROLE_LABEL[r]}</Tag>
            ),
          },
          {
            title: "状态",
            dataIndex: "disabled_at",
            render: (d: string | null) =>
              d ? <Tag color="default">已停用</Tag> : <Tag color="green">启用</Tag>,
          },
          {
            title: "最近登录",
            dataIndex: "last_login_at",
            render: (v: string | null) => (v ? new Date(v).toLocaleString() : "—"),
          },
          {
            title: "操作",
            width: 280,
            render: (_: unknown, u: UserOut) => (
              <Space>
                <Button size="small" onClick={() => setEditing(u)}>
                  编辑
                </Button>
                <Button size="small" onClick={() => setResetting(u)}>
                  重置密码
                </Button>
                <Popconfirm
                  title="确定删除此用户?"
                  okText="删除"
                  cancelText="取消"
                  okButtonProps={{ danger: true }}
                  onConfirm={() => onDelete(u.id)}
                >
                  <Button size="small" danger disabled={me_?.id === u.id}>
                    删除
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />

      <Modal
        title="新建用户"
        open={creating}
        onCancel={() => setCreating(false)}
        onOk={() => form.submit()}
        okText="创建"
        cancelText="取消"
        destroyOnClose
      >
        <Form form={form} layout="vertical" onFinish={onCreate}>
          <Form.Item name="username" label="用户名" rules={[{ required: true, min: 1 }]}>
            <Input />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[{ required: true, min: 8, message: "至少 8 位" }]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item name="role" label="角色" rules={[{ required: true }]}>
            <Select options={ROLES.map((r) => ({ value: r, label: ROLE_LABEL[r] }))} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`编辑用户 ${editing?.username ?? ""}`}
        open={editing !== null}
        onCancel={() => setEditing(null)}
        onOk={() => editing && onUpdate(editing.id, {
          role: form.getFieldValue("role") as UserRole,
          disabled: form.getFieldValue("disabled") as boolean,
        })}
        okText="保存"
        cancelText="取消"
        destroyOnClose
        afterOpenChange={(open) => {
          if (open && editing) {
            form.setFieldsValue({ role: editing.role, disabled: !!editing.disabled_at });
          }
        }}
      >
        <Form form={form} layout="vertical" initialValues={{ role: "view", disabled: false }}>
          <Form.Item name="role" label="角色">
            <Select options={ROLES.map((r) => ({ value: r, label: ROLE_LABEL[r] }))} />
          </Form.Item>
          <Form.Item name="disabled" label="状态" valuePropName="checked">
            <Select
              options={[
                { value: false, label: "启用" },
                { value: true, label: "停用" },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`重置 ${resetting?.username ?? ""} 的密码`}
        open={resetting !== null}
        onCancel={() => setResetting(null)}
        onOk={() => pwForm.submit()}
        okText="重置"
        cancelText="取消"
        destroyOnClose
      >
        <Form form={pwForm} layout="vertical" onFinish={onReset}>
          <Form.Item
            name="new_password"
            label="新密码"
            rules={[{ required: true, min: 8, message: "至少 8 位" }]}
          >
            <Input.Password />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
}
