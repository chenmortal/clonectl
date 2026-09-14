import { QuestionCircleOutlined } from "@ant-design/icons";
import { Collapse, Form, Input, InputNumber, Row, Col, Switch, Tooltip, Typography } from "antd";
import { useState } from "react";
import { RcloneOptions } from "../types";

interface FieldDef {
  key: string;
  label: string;
  kind: "number" | "switch" | "text";
  placeholder?: string;
  tip: string;
  advice: string;
}

export const RCLONE_OPTION_FIELDS: FieldDef[] = [
  {
    key: "transfers",
    label: "并行传输数 transfers",
    kind: "number",
    tip: "同时传输的文件数量",
    advice: "默认 4。大文件/低带宽建议 1~4；海量小文件可调至 8~16",
  },
  {
    key: "checkers",
    label: "并行检查数 checkers",
    kind: "number",
    tip: "并行比对文件（大小/修改时间）的线程数",
    advice: "默认 8。文件数量极大时可增至 16~32",
  },
  {
    key: "checkFirst",
    label: "先检查后传输 checkFirst",
    kind: "switch",
    tip: "先完成全部检查再开始传输，便于尽早掌握总工作量",
    advice: "海量小文件场景建议开启；会占用更多内存",
  },
  {
    key: "multiThreadStreams",
    label: "多线程下载流 multiThreadStreams",
    kind: "number",
    tip: "单个大文件多线程下载的流数（需后端支持，如 S3）",
    advice: "0 = 关闭。大文件下载建议 2~4",
  },
  {
    key: "s3UploadConcurrency",
    label: "S3 上传并发 s3UploadConcurrency",
    kind: "number",
    tip: "S3 分片上传时同时上传的分片数",
    advice: "建议 1~4。调高可加快大文件上传，但更占带宽与内存",
  },
  {
    key: "s3ChunkSize",
    label: "S3 分片大小 s3ChunkSize",
    kind: "text",
    placeholder: "64M",
    tip: "S3 分片上传的单片大小（如 64M、256M）",
    advice: "默认 5M。大文件建议 64M~256M；内存占用 ≈ 分片大小 × 上传并发 × transfers",
  },
  {
    key: "retries",
    label: "重试次数 retries",
    kind: "number",
    tip: "单次操作失败后的重试次数",
    advice: "默认 3。网络不稳定建议 3~5",
  },
  {
    key: "dryRun",
    label: "演练模式 dryRun",
    kind: "switch",
    tip: "只模拟执行，不实际写入目标端",
    advice: "首次配置任务或调整同步方向前，建议先开启验证",
  },
];

const KNOWN_KEYS = new Set(RCLONE_OPTION_FIELDS.map((f) => f.key));

interface Props {
  value?: RcloneOptions;
  onChange?: (value: RcloneOptions) => void;
}

export default function RcloneOptionsInput({ value = {}, onChange }: Props) {
  const [opts, setOpts] = useState<RcloneOptions>(() => {
    const known: RcloneOptions = {};
    Object.entries(value).forEach(([k, v]) => {
      if (KNOWN_KEYS.has(k)) known[k] = v;
    });
    return known;
  });
  const [extraText, setExtraText] = useState<string>(() => {
    const extra: RcloneOptions = {};
    Object.entries(value).forEach(([k, v]) => {
      if (!KNOWN_KEYS.has(k)) extra[k] = v;
    });
    return Object.keys(extra).length ? JSON.stringify(extra, null, 2) : "";
  });
  const [extraError, setExtraError] = useState<string | null>(null);

  const emit = (nextOpts: RcloneOptions, nextText: string) => {
    let parsed: RcloneOptions = {};
    let err: string | null = null;
    if (nextText.trim()) {
      try {
        const obj = JSON.parse(nextText) as unknown;
        if (typeof obj !== "object" || obj === null || Array.isArray(obj)) {
          throw new Error("not object");
        }
        parsed = obj as RcloneOptions;
      } catch {
        err = "高级参数不是合法 JSON 对象，暂不生效";
        parsed = {};
      }
    }
    setExtraError(err);
    const merged: RcloneOptions = { ...parsed };
    Object.entries(nextOpts).forEach(([k, v]) => {
      if (v !== undefined && v !== null && v !== "") merged[k] = v;
    });
    onChange?.(merged);
  };

  const setField = (key: string, v: string | number | boolean | null) => {
    const next: RcloneOptions = { ...opts };
    if (v === null || v === "") delete next[key];
    else next[key] = v;
    setOpts(next);
    emit(next, extraText);
  };

  return (
    <div>
      <Row gutter={12}>
        {RCLONE_OPTION_FIELDS.map((f) => (
          <Col span={12} key={f.key}>
            <Form.Item
              label={
                <span>
                  {f.label}{" "}
                  <Tooltip
                    title={
                      <>
                        <div>{f.tip}</div>
                        <div style={{ marginTop: 4, color: "#ffd666" }}>建议：{f.advice}</div>
                      </>
                    }
                  >
                    <QuestionCircleOutlined style={{ color: "#999" }} />
                  </Tooltip>
                </span>
              }
              style={{ marginBottom: 12 }}
            >
              {f.kind === "switch" ? (
                <Switch
                  checked={Boolean(opts[f.key])}
                  onChange={(c) => setField(f.key, c)}
                />
              ) : f.kind === "number" ? (
                <InputNumber
                  style={{ width: "100%" }}
                  min={0}
                  value={opts[f.key] as number | undefined}
                  onChange={(v) => setField(f.key, v)}
                />
              ) : (
                <Input
                  placeholder={f.placeholder}
                  value={opts[f.key] as string | undefined}
                  onChange={(e) => setField(f.key, e.target.value)}
                />
              )}
            </Form.Item>
          </Col>
        ))}
      </Row>
      <Form.Item
        label="高级参数（JSON，可选）"
        style={{ marginBottom: 8 }}
        validateStatus={extraError ? "error" : undefined}
        help={extraError ?? undefined}
      >
        <Input.TextArea
          rows={3}
          placeholder='{"bwlimit": "10M", "ignore_existing": true}'
          value={extraText}
          onChange={(e) => {
            setExtraText(e.target.value);
            emit(opts, e.target.value);
          }}
        />
      </Form.Item>
      <Collapse
        size="small"
        items={[
          {
            key: "help",
            label: "rclone 参数说明与建议",
            children: (
              <Typography>
                {RCLONE_OPTION_FIELDS.map((f) => (
                  <div key={f.key} style={{ marginBottom: 8 }}>
                    <Typography.Text strong>{f.label}</Typography.Text>
                    <div style={{ fontSize: 12, color: "#666" }}>
                      {f.tip}。
                      <Typography.Text type="warning" style={{ fontSize: 12 }}>
                        建议：{f.advice}
                      </Typography.Text>
                    </div>
                  </div>
                ))}
                <div style={{ fontSize: 12, color: "#999" }}>
                  以上参数经后端以 _config 形式下发给 rclone RC（等同 curl 调用 sync/sync 时的 _config
                  字段）；未列出的参数可写入高级参数 JSON。
                </div>
              </Typography>
            ),
          },
        ]}
      />
    </div>
  );
}
