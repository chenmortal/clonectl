import type { NotificationProvider } from "@refinedev/core";
import { toast } from "sonner";

/** Bridge refine notifications to sonner toasts. */
export const notificationProvider: NotificationProvider = {
  open: ({ type, message, description, key }) => {
    const opts = { description, id: key ?? undefined };
    if (type === "success") toast.success(message, opts);
    else if (type === "error") toast.error(message, opts);
    else toast(message, opts);
  },
  close: (key) => toast.dismiss(key),
};
