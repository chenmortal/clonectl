import { ChevronLeft, ChevronRight } from "lucide-react";
import * as React from "react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";

/**
 * Standard list-page chrome: card + title + toolbar + paginated table.
 *
 * Pagination is client-side by default (rows are sliced in-place); pass
 * `paging` to opt into server-driven pagination where rows already
 * represent the current page and `total` is the server-reported row count.
 */
export function DataPage({
  title,
  description,
  toolbar,
  columns,
  rows,
  getKey,
  loading,
  empty = "暂无数据",
  pageSize = 15,
  paging,
  footer,
}: {
  title: string;
  description?: string;
  toolbar?: React.ReactNode;
  columns: { key: string; label: string; className?: string }[];
  rows: React.ReactNode[][] | null | undefined;
  getKey: (index: number) => React.Key;
  loading?: boolean;
  empty?: string;
  pageSize?: number;
  paging?: {
    page: number;
    pageSize: number;
    total: number;
    onPageChange: (page: number) => void;
    onPageSizeChange?: (size: number) => void;
  };
  footer?: React.ReactNode;
}) {
  const isServer = !!paging;
  const [clientPage, setClientPage] = React.useState(1);
  React.useEffect(() => setClientPage(1), [rows]);

  const effectiveSize = paging?.pageSize ?? pageSize;
  const total = isServer ? paging!.total : (rows?.length ?? 0);
  const pageCount = Math.max(1, Math.ceil(total / effectiveSize));
  const currentPage = isServer ? Math.min(Math.max(1, paging!.page), pageCount) : clientPage;
  const slice = isServer
    ? rows
    : rows?.slice((currentPage - 1) * effectiveSize, currentPage * effectiveSize);

  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between space-y-0">
        <div className="space-y-1.5">
          <CardTitle>{title}</CardTitle>
          {description && <CardDescription>{description}</CardDescription>}
        </div>
        {toolbar && <div className="flex items-center gap-2">{toolbar}</div>}
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              {columns.map((c) => (
                <TableHead key={c.key} className={c.className}>
                  {c.label}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && (!rows || rows.length === 0) ? (
              <TableRow>
                <TableCell colSpan={columns.length} className="h-24 text-center text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : !slice || slice.length === 0 ? (
              <TableRow>
                <TableCell colSpan={columns.length} className="h-24 text-center text-muted-foreground">
                  {empty}
                </TableCell>
              </TableRow>
            ) : (
              slice.map((cells, i) => (
                <TableRow key={getKey(i)}>
                  {cells.map((cell, ci) => (
                    <TableCell key={columns[ci].key} className={columns[ci].className}>
                      {cell}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </CardContent>
      {total > 0 && (
        <Pager
          page={currentPage}
          pageSize={effectiveSize}
          total={total}
          pageCount={pageCount}
          onPageChange={
            isServer
              ? (p) => paging!.onPageChange(p)
              : (p) => setClientPage(p)
          }
          onPageSizeChange={
            isServer && paging?.onPageSizeChange
              ? (s) => paging!.onPageSizeChange!(s)
              : isServer
                ? undefined
                : undefined
          }
        />
      )}
      {footer}
    </Card>
  );
}

function Pager({
  page,
  pageSize,
  total,
  pageCount,
  onPageChange,
  onPageSizeChange,
}: {
  page: number;
  pageSize: number;
  total: number;
  pageCount: number;
  onPageChange: (p: number) => void;
  onPageSizeChange?: (s: number) => void;
}) {
  const [jump, setJump] = React.useState("");
  React.useEffect(() => setJump(""), [page]);
  const submitJump = () => {
    const n = Number(jump);
    if (n >= 1 && n <= pageCount) onPageChange(n);
  };
  return (
    <CardFooter className="flex-row items-center justify-between gap-2 border-t px-6 py-3 text-xs text-muted-foreground">
      <span>
        共 <b className="text-foreground">{total}</b> 条 · 第 {page} / {pageCount} 页
      </span>
      <div className="flex items-center gap-2">
        {onPageSizeChange && (
          <Select value={String(pageSize)} onValueChange={(v) => onPageSizeChange(Number(v))}>
            <SelectTrigger className="h-8 w-24">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {[10, 15, 20, 50, 100].map((n) => (
                <SelectItem key={n} value={String(n)}>{n}/页</SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <Button
          variant="outline"
          size="icon-sm"
          disabled={page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          <ChevronLeft />
        </Button>
        <Button
          variant="outline"
          size="icon-sm"
          disabled={page >= pageCount}
          onClick={() => onPageChange(page + 1)}
        >
          <ChevronRight />
        </Button>
        <Input
          className="h-8 w-16 text-center"
          value={jump}
          onChange={(e) => setJump(e.target.value.replace(/\D/g, ""))}
          onKeyDown={(e) => {
            if (e.key === "Enter") submitJump();
          }}
          placeholder="页"
        />
        <Button variant="outline" size="sm" onClick={submitJump}>跳转</Button>
      </div>
    </CardFooter>
  );
}

/** Status pill used across runs/checks/scheduler tables. */
export function StatusDot({
  tone,
  children,
}: {
  tone: "success" | "failed" | "running" | "skipped" | "pending";
  children: React.ReactNode;
}) {
  const tones: Record<typeof tone, string> = {
    success: "text-emerald-600 dark:text-emerald-400",
    failed: "text-red-600 dark:text-red-400",
    running: "text-blue-600 dark:text-blue-400",
    skipped: "text-muted-foreground",
    pending: "text-amber-600 dark:text-amber-400",
  };
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-sm font-medium", tones[tone])}>
      <span
        className={cn(
          "h-1.5 w-1.5 rounded-full bg-current",
          tone === "running" && "animate-pulse",
        )}
      />
      {children}
    </span>
  );
}
