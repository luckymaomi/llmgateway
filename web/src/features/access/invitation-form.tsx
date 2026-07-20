import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { accessApi, type CreatedInvitation } from '@/api'
import { FormProblem } from '@/features/auth/form-problem'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'

const schema = z.object({
  role: z.enum(['operator', 'member']),
  expiresAt: z.string().min(1, '请选择到期时间'),
})

type Values = z.infer<typeof schema>
type CopyState = 'idle' | 'copied' | 'failed'

export function InvitationForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<unknown>()
  const [created, setCreated] = useState<CreatedInvitation | null>(null)
  const [copyState, setCopyState] = useState<CopyState>('idle')
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { role: 'member', expiresAt: defaultExpiry() },
  })

  async function submit(values: Values): Promise<void> {
    setSubmitting(true)
    setSubmitError(undefined)
    try {
      const result = await accessApi.createInvitation({
        ...values,
        expiresAt: new Date(values.expiresAt).toISOString(),
      })
      setCreated(result)
      setCopyState('idle')
      form.reset(defaultValues())
      void queryClient.invalidateQueries({ queryKey: ['invitations'] })
    } catch (error) {
      setSubmitError(error)
    } finally {
      setSubmitting(false)
    }
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    const formElement = event.currentTarget
    if (formElement.dataset.submissionPending === 'true') return
    formElement.dataset.submissionPending = 'true'
    try {
      await form.handleSubmit(submit)(event)
    } finally {
      delete formElement.dataset.submissionPending
    }
  }

  async function copyInvitation(): Promise<void> {
    if (!created) return
    setCopyState('idle')
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable')
      await navigator.clipboard.writeText(created.code)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
  }

  function close(): void {
    if (submitting) return
    setCreated(null)
    setCopyState('idle')
    setSubmitError(undefined)
    form.reset(defaultValues())
    onOpenChange(false)
  }

  if (created) {
    return (
      <DialogFrame
        open={open}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) close()
        }}
        title="邀请已创建"
        description="完整邀请码仅在本次创建结果中显示，关闭后无法再次查看。"
        footer={<Button onClick={close}>完成</Button>}
      >
        <div className="form-stack">
          <div className="secret-reveal">
            <code aria-label="完整邀请码" data-testid="created-invitation-code">
              {created.code}
            </code>
            <Button
              variant="secondary"
              icon={copyState === 'copied' ? <Check size={16} /> : <Copy size={16} />}
              onClick={() => void copyInvitation()}
            >
              {copyState === 'copied'
                ? '已复制'
                : copyState === 'failed'
                  ? '重新复制'
                  : '复制邀请码'}
            </Button>
          </div>
          {copyState === 'failed' ? (
            <div className="inline-problem" role="alert">
              无法写入剪贴板。请手动选择并复制上方邀请码，然后重试。
            </div>
          ) : null}
        </div>
      </DialogFrame>
    )
  }

  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) close()
      }}
      title="创建邀请"
      dismissible={!submitting}
      footer={
        <>
          <Button variant="secondary" disabled={submitting} onClick={close}>
            取消
          </Button>
          <Button type="submit" form="invitation-form" disabled={submitting}>
            {submitting ? '创建中' : '创建'}
          </Button>
        </>
      }
    >
      <form
        id="invitation-form"
        className="form-grid"
        onSubmit={(event) => void handleSubmit(event)}
      >
        <Field label="角色" htmlFor="invitation-role" error={form.formState.errors.role?.message}>
          <NativeSelect id="invitation-role" autoFocus {...form.register('role')}>
            <option value="member">成员</option>
            <option value="operator">运维人员</option>
          </NativeSelect>
        </Field>
        <Field
          label="到期时间"
          htmlFor="invitation-expiry"
          error={form.formState.errors.expiresAt?.message}
        >
          <Input id="invitation-expiry" type="datetime-local" {...form.register('expiresAt')} />
        </Field>
        <FormProblem error={submitError} />
      </form>
    </DialogFrame>
  )
}

function defaultExpiry(): string {
  const date = new Date(Date.now() + 7 * 24 * 60 * 60 * 1_000)
  return date.toISOString().slice(0, 16)
}

function defaultValues(): Values {
  return { role: 'member', expiresAt: defaultExpiry() }
}
