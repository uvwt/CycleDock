import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Field,
  TextArea,
  TextInput,
} from "../../shared/ui/components";

export function ProjectDialog({
  open,
  onOpenChange,
  name,
  description,
  setName,
  setDescription,
  pending,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  name: string;
  description: string;
  setName: (value: string) => void;
  setDescription: (value: string) => void;
  pending: boolean;
  onSubmit: () => void;
}) {
  const { t } = useTranslation();

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-slate-950/35" />
        <Dialog.Content
          aria-describedby={undefined}
          className="fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-[440px] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-xl"
        >
          <header className="flex shrink-0 items-center justify-between border-b border-slate-100 px-5 py-4">
            <Dialog.Title className="text-base font-semibold text-slate-900">
              {t("新建项目")}
            </Dialog.Title>
            <Dialog.Close
              type="button"
              aria-label={t("关闭")}
              className="rounded-lg p-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
            >
              <X className="size-4" />
            </Dialog.Close>
          </header>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              onSubmit();
            }}
            className="min-h-0 space-y-5 overflow-y-auto p-5"
          >
            <Field label={t("项目名称")}>
              <TextInput
                autoFocus
                required
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </Field>
            <Field label={t("项目描述")}>
              <TextArea
                rows={3}
                value={description}
                onChange={(event) => setDescription(event.target.value)}
              />
            </Field>
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close asChild>
                <Button type="button">{t("取消")}</Button>
              </Dialog.Close>
              <Button
                variant="primary"
                type="submit"
                disabled={!name.trim() || pending}
              >
                {t("创建项目")}
              </Button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
