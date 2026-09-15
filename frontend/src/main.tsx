import { Authenticated, Refine } from "@refinedev/core";
import routerProvider from "@refinedev/react-router-v6";
import { CircleUser, Database, FolderOpen, ListChecks, MonitorCheck, Settings as SettingsIcon, Workflow } from "lucide-react";
import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Toaster } from "sonner";

import { AppLayout } from "@/components/layout/app-layout";
import { authProvider } from "@/providers/auth";
import "./index.css";
import { dataProvider } from "@/providers/data";
import { notificationProvider } from "@/providers/notification";
import CheckTasks from "@/pages/check-tasks";
import DataSources from "@/pages/data-sources";
import Login from "@/pages/login";
import Runs from "@/pages/runs";
import SchedulerMonitor from "@/pages/scheduler";
import Settings from "@/pages/settings";
import StorageSources from "@/pages/storage-sources";
import Tasks from "@/pages/tasks";
import Users from "@/pages/users";

const resources = [
  {
    name: "data-sources",
    list: "/data-sources",
    meta: { label: "数据源", icon: React.createElement(FolderOpen) },
  },
  {
    name: "storage-sources",
    list: "/storage-sources",
    meta: { label: "存储源", icon: React.createElement(Database) },
  },
  {
    name: "tasks",
    list: "/tasks",
    meta: { label: "同步任务", icon: React.createElement(Workflow) },
  },
  {
    name: "check-tasks",
    list: "/check-tasks",
    meta: { label: "检查任务", icon: React.createElement(ListChecks) },
  },
  { name: "runs", list: "/runs", meta: { label: "运行记录" } },
  {
    name: "scheduler",
    list: "/scheduler",
    meta: { label: "调度监控", icon: React.createElement(MonitorCheck) },
  },
  {
    name: "users",
    list: "/users",
    meta: { label: "用户管理", icon: React.createElement(CircleUser) },
  },
  {
    name: "system-settings",
    list: "/settings",
    meta: { label: "系统设置", icon: React.createElement(SettingsIcon) },
  },
];

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
    <Refine
      routerProvider={routerProvider}
      dataProvider={dataProvider}
      authProvider={authProvider}
      notificationProvider={notificationProvider}
      resources={resources}
      options={{
        disableTelemetry: true,
        syncWithLocation: false,
        reactQuery: {
          clientConfig: {
            defaultOptions: {
              queries: { refetchOnWindowFocus: false, retry: 1 },
            },
          },
        },
      }}
    >
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          element={
            <Authenticated
              key="app-shell"
              loading={
                <div className="grid min-h-screen place-items-center text-muted-foreground">
                  加载中…
                </div>
              }
              fallback={<Navigate to="/login" replace />}
            >
              <AppLayout />
            </Authenticated>
          }
        >
          <Route index element={<Navigate to="/data-sources" replace />} />
          <Route path="/data-sources" element={<DataSources />} />
          <Route path="/storage-sources" element={<StorageSources />} />
          <Route path="/tasks" element={<Tasks />} />
          <Route path="/check-tasks" element={<CheckTasks />} />
          <Route path="/runs" element={<Runs />} />
          <Route path="/scheduler" element={<SchedulerMonitor />} />
          <Route path="/users" element={<Users />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="*" element={<Navigate to="/data-sources" replace />} />
        </Route>
      </Routes>
      <Toaster richColors position="top-center" closeButton />
    </Refine>
    </BrowserRouter>
  </React.StrictMode>,
);
