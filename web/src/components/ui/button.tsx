import { cva, type VariantProps } from 'class-variance-authority'
import { Slot } from 'radix-ui'
import type { ButtonHTMLAttributes, ReactNode } from 'react'

import { cn } from '@/lib/cn'

const buttonVariants = cva('button', {
  variants: {
    variant: {
      primary: 'button--primary',
      secondary: 'button--secondary',
      quiet: 'button--quiet',
      danger: 'button--danger',
    },
    size: {
      sm: 'button--sm',
      md: 'button--md',
    },
  },
  defaultVariants: {
    variant: 'primary',
    size: 'md',
  },
})

interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean
  icon?: ReactNode
}

export function Button({
  asChild,
  variant,
  size,
  icon,
  className,
  children,
  ...props
}: ButtonProps) {
  const Component = asChild ? Slot.Root : 'button'
  return (
    <Component className={cn(buttonVariants({ variant, size }), className)} {...props}>
      {icon ? <span className="button__icon">{icon}</span> : null}
      {asChild ? <Slot.Slottable>{children}</Slot.Slottable> : children}
    </Component>
  )
}
