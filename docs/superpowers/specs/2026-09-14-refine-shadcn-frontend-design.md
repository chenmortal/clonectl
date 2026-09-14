# 前端重写设计：Refine + shadcn-ui

日期：2026-09-14
状态：已确认

## 目标

将 web/ 前端从 React 18 + antd 5 重写为 **Refine（无头核心）+ shadcn-ui + Tailwind**，
功能与后端 API 契约完全对等，视觉采用 shadcn 中性色（zinc）亮暗双主题，注重美观。

## 已确认决策

1. 集成方式：`@refinedev/core` 无头核心 + shadcn 组件自搭布局（不用官方模板）
2. 视觉：zinc 中性 token、亮/暗双主题（跟随系统 + 手动切换并持久化）、蓝色 accent
3. 旧版「存储（legacy storages）」只读页砍掉（后端 GET 接口保留不动）
4. 构建产物保持 `web/dist`（go:embed 单二进制），vite dev proxy 三前缀不变

## 技术栈

Vite 6 + React 18 + TS ｜ @refinedev/core + @refinedev/react-router-v6 + @tanstack/react-query
｜ react-router-dom v6 ｜ Tailwind + shadcn/ui 组件 ｜ lucide-react 图标
｜ react-hook-form + zod ｜ sonner toast ｜ axios

## 架构

```
web/src/
├── main.tsx               # <Refine> 装配（providers + resources + routes）
├── providers/
│   ├── data-provider.ts   # refine 动作 → /api/* 映射 + FastAPI 错误体转换
│   ├── auth-provider.ts   # login/me/logout；token 键名保持 rclone-sync.token
│   └── notification.tsx   # refine notificationProvider → sonner
├── components/
│   ├── layout/            # 侧边栏（可折叠）+ 顶栏（rcd 徽标/主题切换/用户菜单）
│   └── ui/                # shadcn 组件（CLI 生成）
├── pages/                 # login/ data-sources/ storage-sources/ tasks/ check-tasks/
│                          # runs/ scheduler/ users/ settings/
└── lib/                   # api.ts(axios+token)、types.ts、utils.ts
```

## Resources 与页面（9 个）

data-sources（首页）、storage-sources、tasks、check-tasks、runs（同步/检查双 Tab +
live 5s 轮询）、scheduler（监控 5s/详情 2s 轮询）、users、system-settings；砍掉
storages 旧版页。自定义动作（trigger/verify/bindings/调度 RunNow）走 axios +
`useInvalidate`。

## 关键行为保持

- token 键 `rclone-sync.token`；401 → 清 token → /login
- errMessage() 语义（FastAPI detail string/[]）
- 状态彩色 Badge（success 绿 / running 蓝脉冲 / failed 红 / skipped 灰）、Progress 进度条
- 数字 tabular-nums、卡片圆角细边框、表格 hover 高亮

## 验证

npm run build（tsc+vite）→ go build（embed）→ serve + Chrome DevTools 逐页截图走查。
