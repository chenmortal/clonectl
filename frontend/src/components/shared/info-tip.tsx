import { Info } from "lucide-react";
import * as React from "react";

import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";

/** 字段旁的信息图标,悬停/聚焦显示多行说明。 */
export function InfoTip({
  title,
  children,
}: {
  title?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <TooltipProvider delayDuration={100}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            tabIndex={0}
            aria-label="说明"
            className="inline-flex text-muted-foreground transition-colors hover:text-foreground focus:outline-none"
            onClick={(e) => e.preventDefault()}
          >
            <Info className="h-3.5 w-3.5" />
          </button>
        </TooltipTrigger>
        <TooltipContent
          side="top"
          className="max-w-xs whitespace-pre-line text-left leading-relaxed"
        >
          {title && <div className="mb-1 font-semibold">{title}</div>}
          {children}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
