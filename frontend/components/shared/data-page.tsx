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
 * Data volume is small (≤ hundreds), so paging is client-side.
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
  footer?: React.ReactNode;
}) {
  const [page, setPage] = React.useState(1);
  React.useEffect(() => setPage(1), [rows]);

  const total = rows?.length ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  const safePage = Math.min(page, pageCount);
  const slice = rows?.slice((safePage - 1) * pageSize, safePage * pageSize);

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
                <TableRow key={getKey((safePage - 1) * pageSize + i)}>
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
      {total > pageSize && (
        <CardFooter className="justify-between border-t px-6 py-3">
          <span className="text-xs text-muted-foreground">
            共 {total} 条 · 第 {safePage}/{pageCount} 页
          </span>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="icon-sm"
              disabled={safePage <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
            >
              <ChevronLeft />
            </Button>
            <Button
              variant="outline"
              size="icon-sm"
              disabled={safePage >= pageCount}
              onClick={() => setPage((p) => Math.min(pageCount, p + 1))}
            >
              <ChevronRight />
            </Button>
          </div>
        </CardFooter>
      )}
      {footer}
    </Card>
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
