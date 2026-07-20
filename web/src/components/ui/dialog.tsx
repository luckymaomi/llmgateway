import { Dialog } from 'radix-ui'
import { X } from 'lucide-react'
import type { ReactNode } from 'react'

import { IconButton } from './icon-button'

interface DialogFrameProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  children: ReactNode
  footer?: ReactNode
  width?: 'sm' | 'md' | 'lg'
  dismissible?: boolean
}

export function DialogFrame({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  width = 'md',
  dismissible = true,
}: DialogFrameProps) {
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(nextOpen) => (dismissible || nextOpen) && onOpenChange(nextOpen)}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content
          className={`dialog-content dialog-content--${width}`}
          onEscapeKeyDown={(event) => {
            if (!dismissible) event.preventDefault()
          }}
          onPointerDownOutside={(event) => {
            if (!dismissible) event.preventDefault()
          }}
        >
          <header className="dialog-header">
            <div>
              <Dialog.Title className="dialog-title">{title}</Dialog.Title>
              {description ? (
                <Dialog.Description className="dialog-description">
                  {description}
                </Dialog.Description>
              ) : null}
            </div>
            {dismissible ? (
              <Dialog.Close asChild>
                <IconButton label="关闭">
                  <X size={18} />
                </IconButton>
              </Dialog.Close>
            ) : null}
          </header>
          <div className="dialog-body">{children}</div>
          {footer ? <footer className="dialog-footer">{footer}</footer> : null}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
