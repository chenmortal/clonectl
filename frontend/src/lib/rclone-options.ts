// rclone 同步参数目录:仅收录经过核实的键(rclone v1.7x ConfigInfo /
// filter.Options,rc 的 _config/_filter 只认 Go 字段名,后端负责翻译)。
// 每个条目带直白的中文释义:含义、默认值、开启/关闭或调高/调低的影响。

export type RcloneOptionType = "bool" | "int" | "size" | "duration" | "bwlimit" | "string";

export interface RcloneOptionDef {
  /** rclone 规范名(与文档/CLI 旗标一致),存储在 rclone_options 中。 */
  key: string;
  label: string;
  group: "性能" | "带宽与请求频率" | "比对与覆盖" | "安全防护" | "范围与过滤" | "重试";
  type: RcloneOptionType;
  placeholder?: string;
  /** 一句话含义。 */
  help: string;
  /** 默认值(rclone 内置)。 */
  default: string;
  /** bool 型:开启/关闭的影响;数值/大小型:调高/调低的影响。 */
  effect: string;
}

export const RCLONE_OPTION_DEFS: RcloneOptionDef[] = [
  // --- 性能 -------------------------------------------------------------
  {
    key: "transfers",
    label: "transfers 并行传输文件数",
    group: "性能",
    type: "int",
    placeholder: "4",
    help: "同时传输的文件数量上限。",
    default: "4",
    effect: "调高:整体更快,但内存、连接数、对端压力成倍增加,容易触发云厂商限流;调低:更稳妥,速度变慢。",
  },
  {
    key: "checkers",
    label: "checkers 并行比对文件数",
    group: "性能",
    type: "int",
    placeholder: "8",
    help: "同时比对(检查哪些文件需要传输)的并发数。",
    default: "8",
    effect: "调高:差异比对阶段更快,API 请求更密集,可能限流;调低:比对变慢但更温和。",
  },
  {
    key: "fast_list",
    label: "fast_list 递归列表",
    group: "性能",
    type: "bool",
    help: "用一次递归列举代替逐个目录列举(需后端支持,如 S3)。",
    default: "关闭",
    effect: "开启:大目录显著加快、API 次数大幅减少,但内存占用更高;关闭:逐目录列举,更省内存。",
  },
  {
    key: "buffer_size",
    label: "buffer_size 传输缓冲",
    group: "性能",
    type: "size",
    placeholder: "16M",
    help: "每个传输任务的内存读缓冲。",
    default: "16M(× transfers)",
    effect: "调高:小文件多时更快,总内存≈缓冲×transfers,注意内存上限;调低:省内存,可能稍慢。",
  },
  {
    key: "check_first",
    label: "check_first 先比对后传输",
    group: "性能",
    type: "bool",
    help: "先完成全部差异比对,再开始传输。",
    default: "关闭(边比对边传输)",
    effect: "开启:进度更可预测、传输更连续,但开始传输前的等待更久;关闭:尽快开始传数据。",
  },

  // --- 带宽与请求频率 ---------------------------------------------------
  {
    key: "bwlimit",
    label: "bwlimit 带宽限制",
    group: "带宽与请求频率",
    type: "bwlimit",
    placeholder: "10M 或 off",
    help: "传输速率上限,如 10M(每秒 10 MiB)、500K;off 为不限速。",
    default: "不限速",
    effect: "调低:占用带宽少、业务影响小,同步时间变长;调高/off:全速传输。",
  },
  {
    key: "tpslimit",
    label: "tpslimit API 每秒请求数",
    group: "带宽与请求频率",
    type: "int",
    placeholder: "10",
    help: "每秒 API 事务数上限,0 表示不限制。",
    default: "0(不限)",
    effect: "调低:对 API 配额友好、避免 429 限流,但整体变慢;调高/不限:更快,可能触发对端限流。",
  },

  // --- 比对与覆盖 -------------------------------------------------------
  {
    key: "checksum",
    label: "checksum 用哈希比对",
    group: "比对与覆盖",
    type: "bool",
    help: "用内容哈希(而非大小+修改时间)判断文件是否一致。",
    default: "关闭(按大小+修改时间)",
    effect: "开启:判定更准确,但更慢、API 消耗大;关闭:速度快,极少数内容变化但大小时间不变的情况会漏掉。",
  },
  {
    key: "size_only",
    label: "size_only 仅比大小",
    group: "比对与覆盖",
    type: "bool",
    help: "只比较文件大小,不看修改时间与内容。",
    default: "关闭",
    effect: "开启:比对最快,内容变了但大小没变的文件会被跳过;关闭:按大小+修改时间判断。",
  },
  {
    key: "ignore_existing",
    label: "ignore_existing 跳过已存在",
    group: "比对与覆盖",
    type: "bool",
    help: "目标端已存在的文件一律跳过,不做任何比对。",
    default: "关闭",
    effect: "开启:增量备份快、绝不覆盖已有文件,但目标端的旧内容/损坏文件不会被修正;关闭:正常比对并覆盖不一致文件。",
  },
  {
    key: "update",
    label: "update 不覆盖更新的文件",
    group: "比对与覆盖",
    type: "bool",
    help: "目标端文件比源端新时跳过,不覆盖。",
    default: "关闭(以源端为准)",
    effect: "开启:保护目标端较新的修改(适合双向使用场景);关闭:严格镜像源端。",
  },
  {
    key: "modify_window",
    label: "modify_window 时间容差",
    group: "比对与覆盖",
    type: "duration",
    placeholder: "2s",
    help: "判断两个修改时间“相等”的容差(文件系统时间精度粗,如 FAT 2s)。",
    default: "自动(按后端精度)",
    effect: "调大:减少因时间精度造成的反复重传;调小:更严格,可能多传。一般无需设置。",
  },

  // --- 安全防护 ---------------------------------------------------------
  {
    key: "dry_run",
    label: "dry_run 试运行",
    group: "安全防护",
    type: "bool",
    help: "只模拟执行、输出将要做的事,不实际改动任何文件。",
    default: "关闭",
    effect: "开启:零风险验证任务与过滤规则;关闭:真正执行复制/删除。验证后记得关闭!",
  },
  {
    key: "max_delete",
    label: "max_delete 删除文件数上限",
    group: "安全防护",
    type: "int",
    placeholder: "100",
    help: "sync 模式一次运行最多允许删除的文件个数,超出则中止(-1 不限,0 禁止任何删除)。",
    default: "-1(不限)",
    effect: "调低:源端误清空时目标端不会被跟着清空,防误删保险;设 -1:完全跟随源端(风险高)。",
  },
  {
    key: "max_transfer",
    label: "max_transfer 单次传输量上限",
    group: "安全防护",
    type: "size",
    placeholder: "100G",
    help: "一次运行最多传输的数据量,达到后停止(-1 不限)。",
    default: "-1(不限)",
    effect: "调低:控制流量/费用峰值;设 -1:一次同步完全部数据。",
  },
  {
    key: "immutable",
    label: "immutable 不可变模式",
    group: "安全防护",
    type: "bool",
    help: "两端文件都视为不可修改,发现任何改动即报错中止。",
    default: "关闭",
    effect: "开启:绝不修改文件,适合归档审计;关闭:正常同步。",
  },

  // --- 范围与过滤(_filter)---------------------------------------------
  {
    key: "max_depth",
    label: "max_depth 递归深度",
    group: "范围与过滤",
    type: "int",
    placeholder: "-1",
    help: "目录递归层数上限,-1 表示不限制。",
    default: "-1(不限)",
    effect: "调小:只同步前几层目录;设 -1:同步整棵目录树。",
  },
  {
    key: "min_size",
    label: "min_size 最小文件大小",
    group: "范围与过滤",
    type: "size",
    placeholder: "1k",
    help: "小于该大小的文件跳过(如 1k)。",
    default: "不过滤",
    effect: "调高:排除碎片/临时小文件;调低/关闭:小文件也同步。",
  },
  {
    key: "max_size",
    label: "max_size 最大文件大小",
    group: "范围与过滤",
    type: "size",
    placeholder: "10G",
    help: "大于该大小的文件跳过(如 10G)。",
    default: "不过滤",
    effect: "调低:排除超大文件,控制时长与空间;调高:覆盖更大文件。",
  },
  {
    key: "min_age",
    label: "min_age 文件最老时限",
    group: "范围与过滤",
    type: "duration",
    placeholder: "1h",
    help: "只处理“至少已存在这么久”的文件(如 1h = 只同步 1 小时前修改的)。",
    default: "不过滤",
    effect: "调高:跳过正在写入的新文件,避免同步到不完整数据;调低:新文件立即纳入。",
  },
  {
    key: "max_age",
    label: "max_age 文件最新时限",
    group: "范围与过滤",
    type: "duration",
    placeholder: "24h",
    help: "只处理最近该时长内修改过的文件(如 24h = 只同步最近一天的改动)。",
    default: "不过滤",
    effect: "调低:窗口小、跑得快;调高:覆盖更长时间段的历史改动。",
  },

  // --- 重试 -------------------------------------------------------------
  {
    key: "retries",
    label: "retries 整体重试次数",
    group: "重试",
    type: "int",
    placeholder: "3",
    help: "整趟同步失败后的整体重试次数。",
    default: "3",
    effect: "调高:网络不稳时成功率更高,耗时更久;调低:失败更快暴露。",
  },
  {
    key: "low_level_retries",
    label: "low_level_retries 底层重试次数",
    group: "重试",
    type: "int",
    placeholder: "10",
    help: "单个 HTTP 请求失败的低层级重试次数。",
    default: "10",
    effect: "调高:对抖动网络更耐受;调低:坏请求更快报错。",
  },
];

export const RCLONE_OPTION_GROUPS = [
  "性能",
  "带宽与请求频率",
  "比对与覆盖",
  "安全防护",
  "范围与过滤",
  "重试",
] as const;
