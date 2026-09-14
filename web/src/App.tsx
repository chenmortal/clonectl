import {
  AuditOutlined,
  DashboardOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  FolderOpenOutlined,
  LogoutOutlined,
  ScheduleOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons";
import {
  Badge,
  Button,
  ConfigProvider,
  Dropdown,
  Layout,
  Menu,
  Spin,
  Typography,
} from "antd";
import zhCN from "antd/locale/zh_CN";
import { useEffect, useState } from "react";
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import {
  errMessage,
  getAuthToken,
  healthz,
  me,
  setAuthToken,
  type MeOut,
} from "./api";
import CheckTasks from "./pages/CheckTasks";
import DataSources from "./pages/DataSources";
import Login from "./pages/Login";
import Runs from "./pages/Runs";
import SchedulerMonitor from "./pages/SchedulerMonitor";
import StorageSources from "./pages/StorageSources";
import Storages from "./pages/Storages";
import SystemSettings from "./pages/SystemSettings";
import Tasks from "./pages/Tasks";
import Users from "./pages/Users";

const { Header, Sider, Content } = Layout;
const { Text } = Typography;

type AuthState = "loading" | "ok" | "anon";

function RequireAuth({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<AuthState>("loading");
  useEffect(() => {
    if (!getAuthToken()) {
      setState("anon");
      return;
    }
    me()
      .then(() => setState("ok"))
      .catch(() => setState("anon"));
  }, []);
  if (state === "loading") {
    return (
      <div style={{ minHeight: "100vh", display: "grid", placeItems: "center" }}>
        <Spin size="large" />
      </div>
    );
  }
  if (state === "anon") {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        path="/*"
        element={
          <RequireAuth>
            <Layout_ />
          </RequireAuth>
        }
      />
    </Routes>
  );
}

function Layout_() {
  const navigate = useNavigate();
  const location = useLocation();
  const [reachable, setReachable] = useState<boolean | null>(null);
  const [me_, setMe] = useState<MeOut | null>(null);

  useEffect(() => {
    const timer = setInterval(() => {
      healthz()
        .then((h) => setReachable(h.rclone_reachable))
        .catch(() => setReachable(false));
    }, 10000);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    me()
      .then(setMe)
      .catch((e) => {
        // RequireAuth will redirect to /login on token failure
        console.error(errMessage(e));
      });
  }, []);

  const items = [
    ...(me_?.role === "admin"
      ? [
          { key: "/storage-sources", icon: <DatabaseOutlined />, label: "存储源" },
          { key: "/system-settings", icon: <SettingOutlined />, label: "系统设置" },
        ]
      : []),
    { key: "/data-sources", icon: <FolderOpenOutlined />, label: "数据源" },
    { key: "/tasks", icon: <ScheduleOutlined />, label: "同步任务" },
    { key: "/check-tasks", icon: <AuditOutlined />, label: "检查任务" },
    { key: "/scheduler", icon: <DashboardOutlined />, label: "调度监控" },
    { key: "/runs", icon: <CloudServerOutlined />, label: "监控与记录" },
    { key: "/storages", icon: <DatabaseOutlined />, label: "存储（旧版）" },
    ...(me_?.role === "admin"
      ? [{ key: "/users", icon: <TeamOutlined />, label: "用户管理" }]
      : []),
  ];

  const logout = () => {
    setAuthToken(null);
    navigate("/login", { replace: true });
  };

  return (
    <ConfigProvider locale={zhCN}>
      <Layout style={{ minHeight: "100vh" }}>
        <Sider theme="dark" width={200}>
          <div
            style={{
              color: "#fff",
              fontSize: 16,
              fontWeight: 600,
              padding: "16px",
              textAlign: "center",
            }}
          >
            rclone-sync
          </div>
          <Menu
            theme="dark"
            mode="inline"
            selectedKeys={[
              location.pathname.startsWith("/runs") ? "/runs" : location.pathname,
            ]}
            items={items}
            onClick={(e) => navigate(e.key)}
          />
        </Sider>
        <Layout>
          <Header
            style={{
              background: "#fff",
              padding: "0 24px",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
            }}
          >
            <span>
              <Badge
                status={
                  reachable ? "success" : reachable === false ? "error" : "processing"
                }
              />
              <span style={{ marginLeft: 8 }}>
                {reachable
                  ? "rclone rcd 已连接"
                  : reachable === false
                    ? "rclone rcd 不可达"
                    : "检测中…"}
              </span>
            </span>
            {me_ && (
              <Dropdown
                menu={{
                  items: [
                    {
                      key: "role",
                      label: (
                        <Text type="secondary">角色:{me_.role}</Text>
                      ),
                      disabled: true,
                    },
                    { type: "divider" },
                    {
                      key: "logout",
                      icon: <LogoutOutlined />,
                      label: "登出",
                      onClick: logout,
                    },
                  ],
                }}
              >
                <Button type="text" icon={<UserOutlined />}>
                  {me_.username}
                </Button>
              </Dropdown>
            )}
          </Header>
          <Content style={{ margin: 16 }}>
            <Routes>
              <Route path="/" element={<Navigate to="/data-sources" replace />} />
              <Route path="/storage-sources" element={<StorageSources />} />
              <Route path="/data-sources" element={<DataSources />} />
              <Route path="/system-settings" element={<SystemSettings />} />
              <Route path="/storages" element={<Storages />} />
              <Route path="/tasks" element={<Tasks />} />
              <Route path="/check-tasks" element={<CheckTasks />} />
              <Route path="/runs" element={<Runs />} />
              <Route path="/scheduler" element={<SchedulerMonitor />} />
              <Route path="/users" element={<Users />} />
            </Routes>
          </Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
