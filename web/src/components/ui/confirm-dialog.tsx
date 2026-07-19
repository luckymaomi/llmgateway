import { AlertDialog } from 'radix-ui'

import { Button } from './button'

interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  confirmLabel: string
  onConfirm: () => void
  pending?: boolean
  danger?: boolean
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  onConfirm,
  pending,
  danger,
}: ConfirmDialogProps) {
  return (
    <AlertDialog.Root open={open} onOpenChange={onOpenChange}>
      <AlertDialog.Portal>
        <AlertDialog.Overlay className="dialog-overlay" />
        <AlertDialog.Content className="dialog-content dialog-content--sm">
          <AlertDialog.Title className="dialog-title">{title}</AlertDialog.Title>
          <AlertDialog.Description className="dialog-description">
            {description}
          </AlertDialog.Description>
          <footer className="dialog-footer dialog-footer--plain">
            <AlertDialog.Cancel asChild>
              <Button type="button" variant="secondary">
                返回
              </Button>
            </AlertDialog.Cancel>
            <AlertDialog.Action asChild>
              <Button
                type="button"
                variant={danger ? 'danger' : 'primary'}
                onClick={onConfirm}
                disabled={pending}
              >
                {pending ? '提交中' : confirmLabel}
              </Button>
            </AlertDialog.Action>
          </footer>
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  )
}
