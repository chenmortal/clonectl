import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  message,
  Modal,
  Space,
  Table,
  Tag,
} from "antd";
import { useCallback, useEffect, useState } from "react";
import {
  errMessage,
  listSystemSettings,
  putSystemSetting,
  testAlertmanager,
} from "../api";
import { SystemSetting } from "../types";

export default function SystemSettings() {
  const [items, setItems] = useState<SystemSetting[]>([]);
  const [loading, setLoading] = useState(false);
  const [editing, setEditing] = useState<SystemSetting | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [testing, setTesting] = useState(false);
  const [form] = Form.useForm<{ value: string }>();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await listSystemSettings());
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const openEdit = (s: SystemSetting) => {
    setEditing(s);
    form.setFieldsValue({ value: s.key === "alertmanager_url" ? "" : s.value });
  };

  const submit = async () => {
    if (!editing) return;
    const { value } = await form.validateFields();
    setSubmitting(true);
    try {
      const isAlertKey = editing.key === "alertmanager_url";
      // For alertmanager_url, the form is always blank (don't echo secret).
      // Send the typed value as-is (including empty = disable).
      const payload = isAlertKey ? value : value;
      await putSystemSetting(editing.key, payload);
      message.success("已保存");
      setEditing(null);
      load();
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  const sendTest = async () => {
    setTesting(true);
    try {
      const r = await testAlertmanager({ alertname: "ManualTest" });
      if (r.sent) {
        message.success(`已发送 (HTTP ${r.status_code})`);
      } else {
        message.error(`发送失败:${r.error ?? "未知"}`);
      }
    } catch (e) {
      message.error(errMessage(e));
    } finally {
      setTesting(false);
    }
  };

  return (
    <Card title="系统设置" extra={<Tag color="blue">仅管理员</Tag>}>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="alertmanager_url 变更立即生效;留空表示关闭告警。"
      />
      <Table<SystemSetting>
        rowKey="key"
        loading={loading}
        dataSource={items}
        pagination={false}
        columns={[
          { title: "键", dataIndex: "key", width: 220 },
          {
            title: "值",
            dataIndex: "value",
            ellipsis: true,
            render: (v: string, row) =>
              row.key === "alertmanager_url" ? (
                v ? <code>{v}</code> : <Tag>已关闭</Tag>
              ) : (
                <code>{v}</code>
              ),
          },
          {
            title: "更新时间",
            dataIndex: "updated_at",
            width: 200,
          },
          {
            title: "操作",
            width: 220,
            render: (_, s) => (
              <Space>
                <Button size="small" onClick={() => openEdit(s)}>
                  编辑
                </Button>
                {s.key === "alertmanager_url" && s.value && (
                  <Button size="small" onClick={sendTest} loading={testing}>
                    发送测试告警
                  </Button>
                )}
              </Space>
            ),
          },
        ]}
      />

      <Modal
        title={editing ? `编辑 ${editing.key}` : "编辑"}
        open={editing != null}
        onCancel={() => setEditing(null)}
        onOk={submit}
        confirmLoading={submitting}
        okText="保存"
        cancelText="取消"
      >
        <Form layout="vertical" form={form}>
          {editing?.key === "alertmanager_url" && (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 12 }}
              message="出于安全考虑,这里不会回显当前 URL;留空 = 关闭告警。"
            />
          )}
          <Form.Item
            name="value"
            label="值"
            rules={[
              {
                validator: async (_, v: string) => {
                  if (editing?.key === "alertmanager_url" && v) {
                    try {
                      new URL(v);
                    } catch {
                      throw new Error("不是合法的 URL");
                    }
                  }
                },
              },
            ]}
          >
            <Input
              placeholder={
                editing?.key === "alertmanager_url"
                  ? "http://alertmanager.local:9093/api/v1/alerts"
                  : ""
              }
            />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
}
