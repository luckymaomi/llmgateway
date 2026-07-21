import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState, type FormEvent, type MouseEvent } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { accessApi, ApiProblem, type CreatedInvitation, type InvitationInput } from '@/api'
import {
  clearPendingInvitationOperation,
  loadPendingInvitationOperation,
  storePendingInvitationOperation,
} from '@/app/pending-operations'
import { useSession } from '@/app/session'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

const schema = z.object({
  expiresAt: z.string().min(1, '请选择到期时间'),
})

type Values = z.infer<typeof schema>
type CopyState = 'idle' | 'copied' | 'failed'
type Submission = { input: InvitationInput; idempotencyKey: string }
const pendingSubmissionSchema = z.object({
  input: z.object({
    expiresAt: z.iso.datetime(),
  }),
  idempotencyKey: z.string().uuid(),
})

export function InvitationForm({
  open,
  onOpenChange,
  onPendingChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onPendingChange: (pending: boolean) => void
}) {
  const session = useSession()
  const queryClient = useQueryClient()
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<unknown>()
  const [created, setCreated] = useState<CreatedInvitation | null>(null)
  const [copyState, setCopyState] = useState<CopyState>('idle')
  const [uncertain, setUncertain] = useState<Submission | undefined>(() =>
    readPendingSubmission(session.userId),
  )
  const [persistenceFailed, setPersistenceFailed] = useState(false)
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: valuesFromPending(uncertain),
  })

  async function submit(values: Values): Promise<void> {
    if (uncertain) return
    const submission: Submission = {
      input: {
        expiresAt: new Date(values.expiresAt).toISOString(),
      },
      idempotencyKey: crypto.randomUUID(),
    }
    if (!storePendingInvitationOperation(session.userId, submission)) {
      setPersistenceFailed(true)
      return
    }
    setPersistenceFailed(false)
    onPendingChange(true)
    await run(submission)
  }

  async function run(submission: Submission): Promise<void> {
    setSubmitting(true)
    setSubmitError(undefined)
    try {
      const result = await accessApi.createInvitation(submission.input, submission.idempotencyKey)
      clearPendingInvitationOperation(session.userId)
      setUncertain(undefined)
      onPendingChange(false)
      setCreated(result)
      setCopyState('idle')
      form.reset(defaultValues())
      void queryClient.invalidateQueries({ queryKey: ['invitations'] })
    } catch (error) {
      setSubmitError(error)
      if (isUnknownOutcome(error)) {
        setUncertain(submission)
        onPendingChange(true)
      } else {
        clearPendingInvitationOperation(session.userId)
        setUncertain(undefined)
        onPendingChange(false)
      }
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
    if (submitting || uncertain) return
    clearPendingInvitationOperation(session.userId)
    onPendingChange(false)
    setCreated(null)
    setCopyState('idle')
    setSubmitError(undefined)
    setPersistenceFailed(false)
    form.reset(defaultValues())
    onOpenChange(false)
  }

  async function retryUncertain(event: MouseEvent<HTMLButtonElement>): Promise<void> {
    if (!uncertain || event.currentTarget.dataset.submissionPending === 'true') return
    const button = event.currentTarget
    button.dataset.submissionPending = 'true'
    try {
      await run(uncertain)
    } finally {
      delete button.dataset.submissionPending
    }
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

  const controlsLocked = submitting || Boolean(uncertain)

  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) close()
      }}
      title="创建邀请"
      dismissible={!controlsLocked}
      footer={
        uncertain ? (
          <Button disabled={submitting} onClick={(event) => void retryUncertain(event)}>
            {submitting ? '正在确认' : '确认原操作'}
          </Button>
        ) : (
          <>
            <Button variant="secondary" disabled={submitting} onClick={close}>
              取消
            </Button>
            <Button type="submit" form="invitation-form" disabled={submitting}>
              {submitting ? '创建中' : '创建'}
            </Button>
          </>
        )
      }
    >
      <form
        id="invitation-form"
        className="form-grid"
        onSubmit={(event) => void handleSubmit(event)}
      >
        <Field
          label="到期时间"
          htmlFor="invitation-expiry"
          error={form.formState.errors.expiresAt?.message}
        >
          <Input
            id="invitation-expiry"
            autoFocus
            type="datetime-local"
            readOnly={controlsLocked}
            {...form.register('expiresAt')}
          />
        </Field>
        {uncertain ? (
          <div className="inline-problem" role="alert">
            <strong>创建结果暂时无法确认。</strong>
            <span>请确认原操作；系统会使用同一幂等键，不会创建第二条邀请。</span>
            {submitError instanceof ApiProblem && submitError.requestId ? (
              <span>Request ID：{submitError.requestId}</span>
            ) : null}
          </div>
        ) : (
          <>
            {persistenceFailed ? (
              <div className="inline-problem" role="alert">
                浏览器无法保存待确认操作，本次未提交。请允许当前标签页使用会话存储后重试。
              </div>
            ) : null}
            <FormProblem error={submitError} />
          </>
        )}
      </form>
    </DialogFrame>
  )
}

function defaultExpiry(): string {
  const date = new Date(Date.now() + 7 * 24 * 60 * 60 * 1_000)
  return localDateTime(date)
}

function defaultValues(): Values {
  return { expiresAt: defaultExpiry() }
}

function readPendingSubmission(userId: string): Submission | undefined {
  const parsed = pendingSubmissionSchema.safeParse(loadPendingInvitationOperation(userId))
  if (parsed.success) return parsed.data
  clearPendingInvitationOperation(userId)
  return undefined
}

function valuesFromPending(pending?: Submission): Values {
  if (!pending) return defaultValues()
  return {
    expiresAt: localDateTime(new Date(pending.input.expiresAt)),
  }
}

function localDateTime(date: Date): string {
  const offsetMilliseconds = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offsetMilliseconds).toISOString().slice(0, 16)
}

function isUnknownOutcome(error: unknown): boolean {
  if (error instanceof DOMException && error.name === 'AbortError') return true
  return error instanceof ApiProblem && (error.retryable || error.status >= 500)
}
