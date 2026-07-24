import { DropdownMenu } from 'radix-ui'
import { Ellipsis } from 'lucide-react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'

import { cn } from '@/lib/cn'

interface TableActionProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string
  icon: ReactNode
  tone?: 'default' | 'positive' | 'warning' | 'danger'
}

export function TableAction({
  label,
  icon,
  tone = 'default',
  className,
  ...props
}: TableActionProps) {
  return (
    <button
      type="button"
      className={cn('table-action', `table-action--${tone}`, className)}
      aria-label={label}
      {...props}
    >
      {icon}
      <span>{label}</span>
    </button>
  )
}

export function RowActionMenu({
  children,
  label = '更多',
}: {
  children: ReactNode
  label?: string
}) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <TableAction label={label} icon={<Ellipsis size={17} />} />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content className="row-action-menu" align="end" sideOffset={6}>
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

export function RowActionItem({
  children,
  icon,
  danger,
  disabled,
  onSelect,
}: {
  children: ReactNode
  icon: ReactNode
  danger?: boolean
  disabled?: boolean
  onSelect: () => void
}) {
  return (
    <DropdownMenu.Item
      className={cn('row-action-menu__item', danger && 'row-action-menu__item--danger')}
      disabled={Boolean(disabled)}
      onSelect={onSelect}
    >
      {icon}
      <span>{children}</span>
    </DropdownMenu.Item>
  )
}

export function RowActionSeparator() {
  return <DropdownMenu.Separator className="row-action-menu__separator" />
}
