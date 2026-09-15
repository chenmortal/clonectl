export interface StorageConfig {
  id: number;
  name: string;
  type: string;
  parameters: Record<string, string>;
  created_at: string;
  updated_at: string;
}

// --- v2: StorageSource + DataSource --------------------------------------


export interface StorageSource {
  id: number;
  name: string;
  type: string;
  endpoint: string | null;
  region: string | null;
  /** local backend only: shared filesystem root prefix */
  path: string | null;
  extra: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface DataSource {
  id: number;
  name: string;
  storage_source_id: number;
  path: string;
  access_key_id: string;
  /** Server returns this in full; UI MUST redact when displaying. */
  secret_access_key: string;
  description: string | null;
  owner_user_id: number;
  last_verified_at: string | null;
  last_verified_ok: boolean | null;
  created_at: string;
  updated_at: string;
}

export type DataSourcePermission = "read" | "write" | "admin";

export interface DataSourceBinding {
  id: number;
  data_source_id: number;
  user_id: number;
  permission: DataSourcePermission;
  created_at: string;
  created_by_user_id: number | null;
}

export interface DataSourceVerifyOut {
  read_ok: boolean;
  write_ok: boolean;
  error: string | null;
  probed_at: string;
}

export interface SystemSetting {
  key: string;
  value: string;
  updated_at: string;
  updated_by_user_id: number | null;
}

export interface AlertmanagerTestOut {
  sent: boolean;
  status_code: number;
  error: string | null;
}

export type RcloneOptions = Record<string, string | number | boolean>;

export interface SyncTask {
  id: number;
  name: string;
  src_data_source_id: number | null;
  dst_data_source_id: number | null;
  src_storage_id: number | null;
  dst_storage_id: number | null;
  src_path: string;
  dst_path: string;
  mode: "sync" | "copy";
  cron: string;
  enabled: boolean;
  rclone_options: RcloneOptions;
  pre_check_task_id: number | null;
  created_at: string;
  updated_at: string;
}

export interface CheckTask {
  id: number;
  name: string;
  src_data_source_id: number | null;
  dst_data_source_id: number | null;
  src_storage_id: number | null;
  dst_storage_id: number | null;
  src_path: string;
  dst_path: string;
  cron: string | null;
  enabled: boolean;
  check_options: CheckOptions;
  created_at: string;
  updated_at: string;
}

export interface CheckOptions {
  oneWay?: boolean;
  download?: boolean;
  combined?: boolean;
  missingOnSrc?: boolean;
  missingOnDst?: boolean;
  match?: boolean;
  differ?: boolean;
  error?: boolean;
  checkFileHash?: string;
  checkFileFs?: string;
  checkFileRemote?: string;
  [key: string]: string | number | boolean | undefined;
}

export interface CheckResult {
  success?: boolean;
  status?: string;
  hashType?: string;
  combined?: string[];
  missingOnSrc?: string[];
  missingOnDst?: string[];
  match?: string[];
  differ?: string[];
  error?: string[];
}

export interface CheckRun {
  id: number;
  task_id: number;
  job_id: number | null;
  status: RunStatus;
  trigger: "schedule" | "manual";
  started_at: string | null;
  finished_at: string | null;
  error: string | null;
  result: CheckResult | null;
}

export interface CheckDetail extends CheckRun {
  live?: { status?: Record<string, unknown>; error?: string } | null;
}

export type RunStatus = "pending" | "running" | "success" | "failed" | "skipped";

export interface SyncRun {
  id: number;
  task_id: number;
  job_id: number | null;
  status: RunStatus;
  trigger: "schedule" | "manual";
  started_at: string | null;
  finished_at: string | null;
  error: string | null;
  stats: Record<string, unknown> | null;
}

export interface LiveStats {
  bytes?: number;
  totalBytes?: number;
  speed?: number;
  eta?: number;
  errors?: number;
  transferring?: { name: string; percentage: number; size: number; speed?: number }[];
  [key: string]: unknown;
}

export interface RunDetail extends SyncRun {
  live?: { status?: Record<string, unknown>; stats?: LiveStats; error?: string } | null;
}

export interface HealthOut {
  status: string;
  rclone_reachable: boolean;
}

export interface FieldDef {
  key: string;
  label: string;
  required?: boolean;
  secret?: boolean;
  placeholder?: string;
  tooltip?: string;
}

export interface BackendDef {
  type: string;
  label: string;
  providers?: string[];
  fields: FieldDef[];
}

export const BACKENDS: BackendDef[] = [
  {
    type: "s3",
    label: "S3 兼容（AWS / MinIO / 阿里 / 腾讯 / 华为云 OBS…）",
    providers: ["AWS", "Minio", "Alibaba", "Tencent", "HuaweiObs", "Other"],
    fields: [
      { key: "endpoint", label: "Endpoint", placeholder: "http://127.0.0.1:9000" },
      { key: "access_key_id", label: "Access Key ID", required: true },
      { key: "secret_access_key", label: "Secret Access Key", required: true, secret: true },
      { key: "region", label: "Region" },
    ],
  },
  {
    type: "local",
    label: "本地文件系统",
    fields: [
      {
        key: "root",
        label: "Root 目录（可选）",
        placeholder: "/var/data",
        tooltip: "rclone 的 local 后端 root 参数。留空表示使用 rcd 主机整个文件系统，任务路径解析为绝对路径。",
      },
    ],
  },
  {
    type: "cos",
    label: "腾讯云 COS",
    fields: [
      { key: "region", label: "Region", required: true, placeholder: "ap-guangzhou" },
      { key: "access_key_id", label: "SecretId", required: true },
      { key: "secret_access_key", label: "SecretKey", required: true, secret: true },
    ],
  },
  {
    type: "gcs",
    label: "Google Cloud Storage",
    fields: [
      { key: "project_number", label: "Project Number" },
      { key: "bucket_policy_only", label: "Bucket Policy Only", placeholder: "true" },
    ],
  },
  {
    type: "azureblob",
    label: "Azure Blob Storage",
    fields: [
      { key: "account", label: "Storage Account", required: true },
      { key: "key", label: "Account Key", required: true, secret: true },
      { key: "endpoint", label: "自定义 Endpoint" },
    ],
  },
  {
    type: "b2",
    label: "Backblaze B2",
    fields: [
      { key: "account", label: "Account ID / Application Key ID", required: true },
      { key: "key", label: "Application Key", required: true, secret: true },
    ],
  },
  {
    type: "swift",
    label: "OpenStack Swift",
    fields: [
      { key: "auth", label: "Auth URL", required: true },
      { key: "user", label: "User", required: true },
      { key: "key", label: "Key", required: true, secret: true },
    ],
  },
];

export function backendOf(type: string): BackendDef | undefined {
  return BACKENDS.find((b) => b.type === type);
}

// --- Scheduler monitoring (Go backend, RFC3339Z timestamps) ---------------

export type SchedulerJobKind = "task" | "check" | "internal";

export interface SchedulerJob {
  id: string;
  name: string;
  kind: SchedulerJobKind;
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

export interface UserOut {
  id: number;
  username: string;
  role: "admin" | "edit" | "view";
  created_at: string;
  last_login_at: string | null;
  disabled_at: string | null;
}
