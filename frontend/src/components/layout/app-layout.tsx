import {
  ChevronLeft,
  Circle,
  FolderOpen,
  LogOut,
  Moon,
  PlayCircle,
  Sun,
  Users,
  Database,
  ListChecks,
  MonitorCheck,
  Settings,
  Workflow,
  Activity,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Link, Outlet, useLocation } from "react-router-dom";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { TooltipProvider } from "@/components/ui/tooltip";
import { healthz } from "@/lib/api";
import { cn } from "@/lib/utils";
import { useGetIdentity, useLogout } from "@refinedev/core";
import type { Identity } from "@/providers/auth";
import { useSiteTitle } from "@/providers/site";

const COLLAPSE_KEY = "rclone-sync.sidebar";

interface MenuItem {
  to: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  adminOnly?: boolean;
}

const MENU: MenuItem[] = [
  { to: "/data-sources", label: "数据源", icon: FolderOpen },
  { to: "/storage-sources", label: "存储源", icon: Database, adminOnly: true },
  { to: "/tasks", label: "同步任务", icon: Workflow },
  { to: "/check-tasks", label: "检查任务", icon: ListChecks },
  { to: "/runs", label: "运行记录", icon: Activity },
  { to: "/scheduler", label: "调度监控", icon: MonitorCheck },
  { to: "/users", label: "用户管理", icon: Users, adminOnly: true },
  { to: "/settings", label: "系统设置", icon: Settings, adminOnly: true },
];

function useTheme(): [boolean, () => void] {
  const [dark, setDark] = useState(
    () => document.documentElement.classList.contains("dark"),
  );
  const toggle = useCallback(() => {
    const next = !document.documentElement.classList.contains("dark");
    document.documentElement.classList.toggle("dark", next);
    localStorage.setItem("rclone-sync.theme", next ? "dark" : "light");
    setDark(next);
  }, []);
  return [dark, toggle];
}

function RcdBadge() {
  const [reachable, setReachable] = useState<boolean | null>(null);
  useEffect(() => {
    const tick = () =>
      healthz()
        .then((h) => setReachable(h.rclone_reachable))
        .catch(() => setReachable(false));
    tick();
    const timer = setInterval(tick, 10000);
    return () => clearInterval(timer);
  }, []);
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
      <Circle
        className={cn(
          "h-2 w-2 fill-current",
          reachable === true && "text-emerald-500",
          reachable === false && "text-red-500",
          reachable === null && "text-amber-400 animate-pulse",
        )}
      />
      rclone rcd
      {reachable === false && " 不可达"}
    </span>
  );
}

export function AppLayout() {
  const [collapsed, setCollapsed] = useState(
    () => localStorage.getItem(COLLAPSE_KEY) === "1",
  );
  const [dark, toggleTheme] = useTheme();
  const location = useLocation();
  const { mutate: logout } = useLogout();
  const { data: identity } = useGetIdentity<Identity>();
  const siteTitle = useSiteTitle();

  const toggleCollapse = () => {
    setCollapsed((c) => {
      localStorage.setItem(COLLAPSE_KEY, c ? "0" : "1");
      return !c;
    });
  };

  const items = MENU.filter((m) => !m.adminOnly || identity?.role === "admin");
  const current = MENU.find((m) => location.pathname.startsWith(m.to));

  return (
    <TooltipProvider delayDuration={200}>
      <div className="flex min-h-screen">
        {/* Sidebar */}
        <aside
          className={cn(
            "fixed inset-y-0 left-0 z-30 flex flex-col border-r bg-card transition-[width] duration-200",
            collapsed ? "w-14" : "w-52",
          )}
        >
          <div className="flex h-14 items-center gap-2 border-b px-3">
            <PlayCircle className="h-6 w-6 shrink-0 text-primary" />
            {!collapsed && (
              <span className="truncate font-semibold tracking-tight">
                {siteTitle}
              </span>
            )}
          </div>
          <nav className="flex-1 space-y-1 overflow-y-auto p-2">
            {items.map(({ to, label, icon: Icon }) => {
              const active = location.pathname.startsWith(to);
              const link = (
                <Link
                  to={to}
                  className={cn(
                    "flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors",
                    active
                      ? "bg-primary/10 font-medium text-primary"
                      : "text-muted-foreground hover:bg-accent hover:text-foreground",
                    collapsed && "justify-center px-0",
                  )}
                >
                  <Icon className="h-4 w-4 shrink-0" />
                  {!collapsed && label}
                </Link>
              );
              return collapsed ? (
                <span key={to} title={label} className="block">
                  {link}
                </span>
              ) : (
                <span key={to}>{link}</span>
              );
            })}
          </nav>
          <div className="border-t p-2">
            <Button
              variant="ghost"
              size={collapsed ? "icon" : "sm"}
              className={cn("w-full text-muted-foreground", !collapsed && "justify-start")}
              onClick={toggleCollapse}
            >
              <ChevronLeft
                className={cn("h-4 w-4 transition-transform", collapsed && "rotate-180")}
              />
              {!collapsed && "收起"}
            </Button>
          </div>
        </aside>

        {/* Main column */}
        <div
          className={cn(
            "flex min-h-screen flex-1 flex-col transition-[margin] duration-200",
            collapsed ? "ml-14" : "ml-52",
          )}
        >
          <header className="sticky top-0 z-20 flex h-14 items-center justify-between border-b bg-background/80 px-6 backdrop-blur">
            <h1 className="text-sm font-semibold">{current?.label ?? siteTitle}</h1>
            <div className="flex items-center gap-4">
              <RcdBadge />
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={toggleTheme}
                title={dark ? "切换亮色" : "切换暗色"}
              >
                {dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
              </Button>
              {identity && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="sm" className="gap-2">
                      <span className="flex h-6 w-6 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary">
                        {identity.username.slice(0, 1).toUpperCase()}
                      </span>
                      {identity.username}
                      <Badge variant="outline" className="px-1.5 text-[10px]">
                        {identity.role}
                      </Badge>
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="w-40">
                    <DropdownMenuLabel className="text-xs">
                      角色：{identity.role}
                    </DropdownMenuLabel>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem onClick={() => logout()}>
                      <LogOut /> 退出登录
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </div>
          </header>
          <main className="flex-1 p-6">
            <Outlet />
          </main>
        </div>
      </div>
    </TooltipProvider>
  );
}
