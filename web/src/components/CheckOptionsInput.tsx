import { Col, Form, Input, Row, Switch, Tooltip, Typography } from "antd";
import { CheckOptions } from "../types";

interface SwitchFieldDef {
  key: keyof CheckOptions & string;
  label: string;
  tip: string;
  defaultValue: boolean;
}

const SWITCH_FIELDS: SwitchFieldDef[] = [
  { key: "oneWay", label: "单向检查 oneWay", tip: "仅检查源端文件是否存在于目标端", defaultValue: false },
  { key: "download", label: "下载比对 download", tip: "下载两端数据逐字节比对（不支持 hash 的存储适用，较慢）", defaultValue: false },
  { key: "differ", label: "报告不一致 differ", tip: "报告所有不匹配的文件", defaultValue: true },
  { key: "missingOnSrc", label: "报告源端缺失 missingOnSrc", tip: "报告所有源端缺失的文件", defaultValue: true },
  { key: "missingOnDst", label: "报告目标端缺失 missingOnDst", tip: "报告所有目标端缺失的文件", defaultValue: true },
  { key: "error", label: "报告错误 error", tip: "报告所有读取/哈希出错的文件", defaultValue: true },
  { key: "match", label: "报告匹配 match", tip: "报告所有匹配的文件（大目录慎用）", defaultValue: false },
  { key: "combined", label: "合并报告 combined", tip: "生成变更的合并报告", defaultValue: false },
];

interface TextFieldDef {
  key: keyof CheckOptions & string;
  label: string;
  tip: string;
  placeholder: string;
}

const TEXT_FIELDS: TextFieldDef[] = [
  {
    key: "checkFileHash",
    label: "SUM 哈希类型 checkFileHash",
    tip: "将 checkFileFs:checkFileRemote 指向的文件作为 SUM 哈希清单（如 md5/sha1）；启用后以清单为源端比对目标端，源存储配置不再使用",
    placeholder: "md5",
  },
  {
    key: "checkFileFs",
    label: "SUM 文件存储 checkFileFs",
    tip: "SUM 清单文件所在的 remote，如 local-s3",
    placeholder: "local-s3",
  },
  {
    key: "checkFileRemote",
    label: "SUM 文件路径 checkFileRemote",
    tip: "SUM 清单文件的路径，如 /checksums/files.md5",
    placeholder: "/checksums/files.md5",
  },
];

interface Props {
  value?: CheckOptions;
  onChange?: (value: CheckOptions) => void;
}

export default function CheckOptionsInput({ value = {}, onChange }: Props) {
  const getBool = (f: SwitchFieldDef) => {
    const v = value[f.key];
    return typeof v === "boolean" ? v : f.defaultValue;
  };

  const set = (key: string, v: string | boolean) => {
    const next = { ...value };
    if (v === "" || v === false && !SWITCH_FIELDS.some((s) => s.key === key)) {
      delete next[key];
    } else {
      next[key] = v;
    }
    onChange?.(next);
  };

  return (
    <div>
      <Row gutter={12}>
        {SWITCH_FIELDS.map((f) => (
          <Col span={12} key={f.key}>
            <Form.Item
              label={
                <Tooltip title={f.tip}>
                  <span>{f.label}</span>
                </Tooltip>
              }
              style={{ marginBottom: 10 }}
            >
              <Switch checked={getBool(f)} onChange={(c) => set(f.key, c)} />
            </Form.Item>
          </Col>
        ))}
      </Row>
      <Row gutter={12}>
        {TEXT_FIELDS.map((f) => (
          <Col span={8} key={f.key}>
            <Form.Item
              label={
                <Tooltip title={f.tip}>
                  <span style={{ fontSize: 12 }}>{f.label}</span>
                </Tooltip>
              }
              style={{ marginBottom: 10 }}
            >
              <Input
                allowClear
                placeholder={f.placeholder}
                value={typeof value[f.key] === "string" ? (value[f.key] as string) : ""}
                onChange={(e) => set(f.key, e.target.value)}
              />
            </Form.Item>
          </Col>
        ))}
      </Row>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        检查基于 rclone operations/check：比对两端文件大小与哈希，不修改源和目标。SUM
        清单模式（checkFileHash）下源端配置不生效。未设置项使用 rclone 默认值。
      </Typography.Text>
    </div>
  );
}
