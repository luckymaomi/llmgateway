import { Tooltip } from 'radix-ui'
import type { ButtonHTMLAttributes, ReactNode } from 'react'

import { cn } from '@/lib/cn'

interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string
  children: ReactNode
}

export function IconButton({ label, className, children, ...props }: IconButtonProps) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger asChild>
        <button className={cn('icon-button', className)} aria-label={label} {...props}>
          {children}
        </button>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content className="tooltip" sideOffset={7}>
          {label}
          <Tooltip.Arrow className="tooltip__arrow" />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  )
}
