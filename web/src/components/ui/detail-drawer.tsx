import { Dialog } from 'radix-ui'
import { X } from 'lucide-react'
import type { ReactNode } from 'react'

import { IconButton } from './icon-button'

export function DetailDrawer({
  open,
  onOpenChange,
  title,
  subtitle,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  subtitle?: string | undefined
  children: ReactNode
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="detail-drawer">
          <header className="detail-drawer__header">
            <div>
              <Dialog.Title>{title}</Dialog.Title>
              {subtitle ? <Dialog.Description>{subtitle}</Dialog.Description> : null}
            </div>
            <Dialog.Close asChild>
              <IconButton label="关闭详情" showTooltip={false}>
                <X size={18} />
              </IconButton>
            </Dialog.Close>
          </header>
          <div className="detail-drawer__body">{children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
