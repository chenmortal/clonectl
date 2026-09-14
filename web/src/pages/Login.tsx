import { LockOutlined, UserOutlined } from "@ant-design/icons";
import { Button, Card, Form, Input, Space, Typography, message } from "antd";
import { useState } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { errMessage, getAuthToken, login, setAuthToken } from "../api";

const { Title } = Typography;

export default function Login() {
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);

  if (getAuthToken()) {
    return <Navigate to="/" replace />;
  }

  const onFinish = async (values: { username: string; password: string }) => {
    setSubmitting(true);
    try {
      const { access_token, role } = await login(values.username, values.password);
      setAuthToken(access_token);
      message.success(`已登录为 ${values.username} (${role})`);
      navigate("/", { replace: true });
    } catch (e) {
      message.error(errMessage(e) || "登录失败");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "#f0f2f5",
      }}
    >
      <Card style={{ width: 380 }}>
        <Space direction="vertical" size="large" style={{ width: "100%" }}>
          <Title level={3} style={{ textAlign: "center", margin: 0 }}>
            rclone-sync 登录
          </Title>
          <Form layout="vertical" onFinish={onFinish} autoComplete="off">
            <Form.Item
              name="username"
              rules={[{ required: true, message: "请输入用户名" }]}
            >
              <Input prefix={<UserOutlined />} placeholder="用户名" autoFocus />
            </Form.Item>
            <Form.Item
              name="password"
              rules={[{ required: true, message: "请输入密码" }]}
            >
              <Input.Password prefix={<LockOutlined />} placeholder="密码" />
            </Form.Item>
            <Form.Item style={{ marginBottom: 0 }}>
              <Button type="primary" htmlType="submit" loading={submitting} block>
                登录
              </Button>
            </Form.Item>
          </Form>
        </Space>
      </Card>
    </div>
  );
}
