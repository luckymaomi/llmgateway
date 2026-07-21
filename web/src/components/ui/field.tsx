import type {
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from 'react'

import { cn } from '@/lib/cn'

interface FieldProps {
  label: string
  htmlFor: string
  className?: string | undefined
  error?: string | undefined
  hint?: string | undefined
  children: ReactNode
}

export function Field({ label, htmlFor, className, error, hint, children }: FieldProps) {
  return (
    <div className={cn('field', className)}>
      <label className="field__label" htmlFor={htmlFor}>
        {label}
      </label>
      {children}
      {error ? (
        <span className="field__error" role="alert">
          {error}
        </span>
      ) : hint ? (
        <span className="field__hint">{hint}</span>
      ) : null}
    </div>
  )
}

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn('input', className)} {...props} />
}

export function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn('textarea', className)} {...props} />
}

export function NativeSelect({
  className,
  children,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cn('select', className)} {...props}>
      {children}
    </select>
  )
}
