import axios from "axios";
import type {
  AlertmanagerTestOut,
  CheckDetail,
  CheckRun,
  CheckTask,
  DataSource,
  DataSourceBinding,
  DataSourcePermission,
  DataSourceVerifyOut,
  HealthOut,
  RunDetail,
  RunStatus,
  StorageConfig,
  StorageSource,
  SyncRun,
  SyncTask,
  SystemSetting,
} from "./types";

const http = axios.create({ baseURL: "", timeout: 30000 });

// ---------------------------------------------------------------------------
// Auth: token storage + request/response interceptors
// ---------------------------------------------------------------------------

const TOKEN_KEY = "rclone-sync.token";

export const getAuthToken = (): string | null => localStorage.getItem(TOKEN_KEY);

export const setAuthToken = (token: string | null): void => {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token);
  } else {
    localStorage.removeItem(TOKEN_KEY);
  }
};

http.interceptors.request.use((cfg) => {
  const token = getAuthToken();
  if (token) {
    cfg.headers = cfg.headers ?? {};
    cfg.headers.Authorization = `Bearer ${token}`;
  }
  return cfg;
});

http.interceptors.response.use(
  (r) => r,
  (err) => {
    if (axios.isAxiosError(err) && err.response?.status === 401) {
      setAuthToken(null);
      if (window.location.pathname !== "/login") {
        window.location.href = "/login";
      }
    }
    return Promise.reject(err);
  },
);

export function errMessage(e: unknown): string {
  if (axios.isAxiosError(e)) {
    const detail = e.response?.data?.detail;
    if (typeof detail === "string") return detail;
    if (Array.isArray(detail) && detail[0]?.msg) return detail[0].msg;
    return e.message;
  }
  return String(e);
}

// ---------------------------------------------------------------------------
// Auth / user management
// ---------------------------------------------------------------------------

export type UserRole = "admin" | "edit" | "view";

export interface MeOut {
  id: number;
  username: string;
  role: UserRole;
  last_login_at: string | null;
}

export interface UserOut {
  id: number;
  username: string;
  role: UserRole;
  created_at: string;
  last_login_at: string | null;
  disabled_at: string | null;
}

export interface TokenOut {
  access_token: string;
  token_type: string;
  expires_in: number;
  role: UserRole;
}

export const login = (username: string, password: string) =>
  http
    .post<TokenOut>("/api/auth/login", { username, password })
    .then((r) => r.data);

export const me = () => http.get<MeOut>("/api/auth/me").then((r) => r.data);

export const changePassword = (old_password: string, new_password: string) =>
  http
    .post("/api/auth/change-password", { old_password, new_password })
    .then((r) => r.data);

export const listUsers = () => http.get<UserOut[]>("/api/users").then((r) => r.data);

export const createUser = (data: {
  username: string;
  password: string;
  role: UserRole;
}) => http.post<UserOut>("/api/users", data).then((r) => r.data);

export const updateUser = (
  id: number,
  data: { role?: UserRole; disabled?: boolean },
) => http.put<UserOut>(`/api/users/${id}`, data).then((r) => r.data);

export const deleteUser = (id: number) => http.delete(`/api/users/${id}`);

export const resetPassword = (id: number, new_password: string) =>
  http
    .post(`/api/users/${id}/reset-password`, { new_password })
    .then((r) => r.data);

// ---------------------------------------------------------------------------
// Domain endpoints
// ---------------------------------------------------------------------------

export const listStorages = () =>
  http.get<StorageConfig[]>("/api/storages").then((r) => r.data);

// Legacy write helpers — backend returns 410 Gone for all of these.
// Kept so existing call sites still type-check; callers should display
// the deprecation notice to the user.
export const createStorage = (data: {
  name: string;
  type: string;
  parameters: Record<string, string>;
}) => http.post<StorageConfig>("/api/storages", data).then((r) => r.data);

export const updateStorage = (
  id: number,
  data: Partial<{ name: string; type: string; parameters: Record<string, string> }>,
) => http.put<StorageConfig>(`/api/storages/${id}`, data).then((r) => r.data);

export const deleteStorage = (id: number) =>
  http.delete(`/api/storages/${id}`);

// ---------------------------------------------------------------------------
// v2: StorageSource + DataSource + bindings
// ---------------------------------------------------------------------------

export const listStorageSources = () =>
  http.get<StorageSource[]>("/api/storage-sources").then((r) => r.data);

export const createStorageSource = (data: {
  name: string;
  type: string;
  endpoint?: string | null;
  region?: string | null;
  extra?: Record<string, unknown>;
}) => http.post<StorageSource>("/api/storage-sources", data).then((r) => r.data);

export const updateStorageSource = (
  id: number,
  data: Partial<{
    name: string;
    type: string;
    endpoint: string | null;
    region: string | null;
    extra: Record<string, unknown>;
  }>,
) => http.put<StorageSource>(`/api/storage-sources/${id}`, data).then((r) => r.data);

export const deleteStorageSource = (id: number) =>
  http.delete(`/api/storage-sources/${id}`);

export const listDataSources = () =>
  http.get<DataSource[]>("/api/data-sources").then((r) => r.data);

export const createDataSource = (data: {
  name: string;
  storage_source_id: number;
  path: string;
  // Optional: required only for non-local storage sources; the API rejects
  // requests missing AK/SK when the linked source isn't local.
  access_key_id?: string | null;
  secret_access_key?: string | null;
  description?: string | null;
}) => http.post<DataSource>("/api/data-sources", data).then((r) => r.data);

export const updateDataSource = (
  id: number,
  data: Partial<{
    name: string;
    storage_source_id: number;
    path: string;
    access_key_id: string | null;
    secret_access_key: string | null;
    description: string | null;
  }>,
) => http.put<DataSource>(`/api/data-sources/${id}`, data).then((r) => r.data);

export const deleteDataSource = (id: number) =>
  http.delete(`/api/data-sources/${id}`);

export const verifyDataSource = (id: number) =>
  http
    .post<DataSourceVerifyOut>(`/api/data-sources/${id}/verify`)
    .then((r) => r.data);

export const listBindings = (dsId: number) =>
  http
    .get<DataSourceBinding[]>(`/api/data-sources/${dsId}/bindings`)
    .then((r) => r.data);

export const createBinding = (
  dsId: number,
  data: { user_id: number; permission: DataSourcePermission },
) =>
  http
    .post<DataSourceBinding>(`/api/data-sources/${dsId}/bindings`, data)
    .then((r) => r.data);

export const updateBinding = (
  dsId: number,
  bid: number,
  data: { permission: DataSourcePermission },
) =>
  http
    .put<DataSourceBinding>(
      `/api/data-sources/${dsId}/bindings/${bid}`,
      data,
    )
    .then((r) => r.data);

export const deleteBinding = (dsId: number, bid: number) =>
  http.delete(`/api/data-sources/${dsId}/bindings/${bid}`);

// ---------------------------------------------------------------------------
// v2: System settings + AlertManager
// ---------------------------------------------------------------------------

export const listSystemSettings = () =>
  http.get<SystemSetting[]>("/api/system-settings").then((r) => r.data);

export const getSystemSetting = (key: string) =>
  http.get<SystemSetting>(`/api/system-settings/${key}`).then((r) => r.data);

export const putSystemSetting = (key: string, value: string) =>
  http.put<SystemSetting>(`/api/system-settings/${key}`, { value }).then((r) => r.data);

export const testAlertmanager = (data?: {
  alertname?: string;
  labels?: Record<string, string>;
}) =>
  http
    .post<AlertmanagerTestOut>(
      "/api/system-settings/internal/alertmanager-test",
      data ?? { alertname: "ManualTest" },
    )
    .then((r) => r.data);

export const listTasks = () => http.get<SyncTask[]>("/api/tasks").then((r) => r.data);

export const createTask = (data: Omit<SyncTask, "id" | "created_at" | "updated_at">) =>
  http.post<SyncTask>("/api/tasks", data).then((r) => r.data);

export const updateTask = (id: number, data: Partial<Omit<SyncTask, "id" | "created_at" | "updated_at">>) =>
  http.put<SyncTask>(`/api/tasks/${id}`, data).then((r) => r.data);

export const deleteTask = (id: number) => http.delete(`/api/tasks/${id}`);

export const triggerTask = (id: number) =>
  http.post<SyncRun>(`/api/tasks/${id}/trigger`).then((r) => r.data);

export const listRuns = (params: { task_id?: number; status?: RunStatus; limit?: number }) =>
  http.get<SyncRun[]>("/api/runs", { params }).then((r) => r.data);

export const getRun = (id: number) => http.get<RunDetail>(`/api/runs/${id}`).then((r) => r.data);

export const listCheckTasks = () =>
  http.get<CheckTask[]>("/api/check-tasks").then((r) => r.data);

export const createCheckTask = (data: Omit<CheckTask, "id" | "created_at" | "updated_at">) =>
  http.post<CheckTask>("/api/check-tasks", data).then((r) => r.data);

export const updateCheckTask = (
  id: number,
  data: Partial<Omit<CheckTask, "id" | "created_at" | "updated_at">>,
) => http.put<CheckTask>(`/api/check-tasks/${id}`, data).then((r) => r.data);

export const deleteCheckTask = (id: number) => http.delete(`/api/check-tasks/${id}`);

export const triggerCheckTask = (id: number) =>
  http.post<CheckRun>(`/api/check-tasks/${id}/trigger`).then((r) => r.data);

export const listChecks = (params: { task_id?: number; status?: RunStatus; limit?: number }) =>
  http.get<CheckRun[]>("/api/checks", { params }).then((r) => r.data);

export const getCheck = (id: number) =>
  http.get<CheckDetail>(`/api/checks/${id}`).then((r) => r.data);

export const healthz = () => http.get<HealthOut>("/healthz").then((r) => r.data);

// ---------------------------------------------------------------------------
// Scheduler monitoring (gocron-based)
// ---------------------------------------------------------------------------

export interface SchedulerJob {
  id: string;
  name: string;
  kind: "task" | "check" | "internal";
  task_id: number | null;
  tags: string[];
  schedule: string;
  next_run: string | null;
  last_run_started_at: string | null;
  last_run_completed_at: string | null;
  is_running: boolean;
  last_error: string;
  run_count: number;
  fail_count: number;
  consecutive_failures: number;
}

export interface SchedulerJobExecution {
  started_at: string;
  duration_ms: number;
  error: string;
}

export interface SchedulerJobDetail extends SchedulerJob {
  next_runs: string[];
  executions: SchedulerJobExecution[];
}

export interface SchedulerJobsOut {
  node_id: string;
  is_leader: boolean;
  scheduler_running: boolean;
  jobs: SchedulerJob[];
  note: string;
}

export interface SchedulerOverview {
  is_leader: boolean;
  leader_id: string | null;
  node_id: string;
  cluster_name: string;
  jobs_total: number;
  user_jobs: number;
  running_now: number;
}

export interface SchedulerRunNowOut {
  job_id: string;
  kind: "task" | "check";
  task_id: number | null;
  run_id?: number;
  check_id?: number;
}

export const listSchedulerJobs = () =>
  http.get<SchedulerJobsOut>("/api/scheduler/jobs").then((r) => r.data);

export const getSchedulerJob = (id: string) =>
  http.get<SchedulerJobDetail>(`/api/scheduler/jobs/${id}`).then((r) => r.data);

export const runSchedulerJob = (id: string) =>
  http.post<SchedulerRunNowOut>(`/api/scheduler/jobs/${id}/run`).then((r) => r.data);

export const schedulerOverview = () =>
  http.get<SchedulerOverview>("/api/scheduler/overview").then((r) => r.data);
