import * as React from "react";

import { cn } from "@/lib/utils";

/** 弹窗内的一节:带小节标题与右侧提示,拉开分区间距。 */
export function DialogSection({
  title,
  hint,
  children,
  className,
}: {
  title: React.ReactNode;
  hint?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("space-y-3", className)}>
      <div className="flex items-baseline justify-between gap-2 border-b pb-1.5">
        <h4 className="text-sm font-semibold leading-none">{title}</h4>
        {hint && <span className="text-[11px] text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </section>
  );
}
