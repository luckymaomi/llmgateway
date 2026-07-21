import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState, type FormEvent, type MouseEvent } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import {
  accessApi,
  ApiProblem,
  catalogApi,
  type CreatedGatewayKey,
  type GatewayKeyInput,
} from '@/api'
import { useSession } from '@/app/session'
import {
  clearPendingGatewayKeyOperation,
  loadPendingGatewayKeyOperation,
  storePendingGatewayKeyOperation,
} from '@/app/pending-operations'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

const schema = z.object({
  ownerId: z.string().uuid('请选择所属用户'),
  name: z.string().trim().min(2, '请输入 Key 名称'),
  authorizedModelIds: z.array(z.string().uuid()).min(1, '请选择至少一个模型'),
  expiresAt: z.string(),
})

type Values = z.infer<typeof schema>
type Submission = { input: GatewayKeyInput; idempotencyKey: string }
const pendingSubmissionSchema = z.object({
  input: z.object({
    ownerId: z.string().uuid(),
    name: z.string(),
    authorizedModelIds: z.array(z.string().uuid()).min(1),
    expiresAt: z.string().optional(),
  }),
  idempotencyKey: z.string().uuid(),
})

export function KeyForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const session = useSession()
  const queryClient = useQueryClient()
  const [created, setCreated] = useState<CreatedGatewayKey | null>(null)
  const [copied, setCopied] = useState(false)
  const [copyFailed, setCopyFailed] = useState(false)
  const [uncertain, setUncertain] = useState<Submission | undefined>(() =>
    readPendingSubmission(session.userId),
  )
  const [persistenceFailed, setPersistenceFailed] = useState(false)
  const users = useQuery({
    queryKey: ['users', 'key-form'],
    queryFn: ({ signal }) => accessApi.users({ page: 1, pageSize: 100, status: 'active' }, signal),
    enabled: open && session.role === 'administrator',
  })
  const activeConfiguration = useQuery({
    queryKey: ['configuration-active'],
    queryFn: ({ signal }) => catalogApi.activeConfiguration(signal),
    enabled: open && session.role === 'administrator',
  })
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: valuesFromPending(uncertain),
  })
  const mutation = useMutation({
    gcTime: 0,
    mutationFn: (submission: Submission) =>
      accessApi.createKey(submission.input, submission.idempotencyKey),
    async onSuccess(result) {
      clearPendingGatewayKeyOperation(session.userId)
      setUncertain(undefined)
      setCreated(result)
      await queryClient.invalidateQueries({ queryKey: ['gateway-keys'] })
    },
    onError(error, submission) {
      if (isUnknownOutcome(error)) {
        setUncertain(submission)
      } else {
        clearPendingGatewayKeyOperation(session.userId)
        setUncertain(undefined)
      }
    },
  })

  function resetAndClose(): void {
    mutation.reset()
    setCreated(null)
    setCopied(false)
    setCopyFailed(false)
    setPersistenceFailed(false)
    setUncertain(undefined)
    clearPendingGatewayKeyOperation(session.userId)
    form.reset(defaultValues())
    onOpenChange(false)
  }

  function requestClose(): void {
    if (mutation.isPending || uncertain) return
    resetAndClose()
  }

  async function submit(values: Values): Promise<void> {
    if (uncertain) return
    const input: GatewayKeyInput = {
      ownerId: values.ownerId,
      name: values.name.trim(),
      authorizedModelIds: values.authorizedModelIds,
      ...(values.expiresAt ? { expiresAt: new Date(values.expiresAt).toISOString() } : {}),
    }
    const submission = { input, idempotencyKey: crypto.randomUUID() }
    if (!storePendingGatewayKeyOperation(session.userId, submission)) {
      setPersistenceFailed(true)
      return
    }
    setPersistenceFailed(false)
    setUncertain(undefined)
    try {
      await mutation.mutateAsync(submission)
    } catch {
      // The mutation state renders the typed error or recovery action.
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

  async function retryUncertain(event: MouseEvent<HTMLButtonElement>): Promise<void> {
    if (!uncertain || event.currentTarget.dataset.submissionPending === 'true') return
    const button = event.currentTarget
    button.dataset.submissionPending = 'true'
    try {
      await mutation.mutateAsync(uncertain)
    } catch {
      // The same recovery action remains available while the outcome is unknown.
    } finally {
      delete button.dataset.submissionPending
    }
  }

  if (session.role !== 'administrator') return null

  if (created) {
    return (
      <DialogFrame
        open={open}
        onOpenChange={(next) => {
          if (!next) requestClose()
        }}
        title="网关 Key 已创建"
        description="明文仅在本次创建结果中展示"
        footer={<Button onClick={requestClose}>完成</Button>}
      >
        <div className="secret-reveal">
          <code data-testid="created-key-secret">{created.secret}</code>
          <Button
            variant="secondary"
            icon={copied ? <Check size={16} /> : <Copy size={16} />}
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(created.secret)
                setCopied(true)
                setCopyFailed(false)
              } catch {
                setCopyFailed(true)
              }
            }}
          >
            {copied ? '已复制' : '复制 Key'}
          </Button>
        </div>
        {copyFailed ? (
          <div className="inline-problem" role="alert">
            浏览器未允许写入剪贴板。
          </div>
        ) : null}
        <dl className="fact-list">
          <div>
            <dt>名称</dt>
            <dd>{created.key.name}</dd>
          </div>
          <div>
            <dt>前缀</dt>
            <dd>{created.key.prefix}</dd>
          </div>
          <div>
            <dt>模型授权</dt>
            <dd>{created.key.authorizedModels.join(', ')}</dd>
          </div>
        </dl>
      </DialogFrame>
    )
  }

  const controlsLocked = mutation.isPending || Boolean(uncertain)
  const models = activeConfiguration.data?.models ?? []

  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) requestClose()
      }}
      title="创建网关 Key"
      description="模型授权来自当前已发布配置。"
      dismissible={!controlsLocked}
      footer={
        <>
          <Button
            type="button"
            variant="secondary"
            disabled={controlsLocked}
            onClick={requestClose}
          >
            取消
          </Button>
          {uncertain ? (
            <Button disabled={mutation.isPending} onClick={(event) => void retryUncertain(event)}>
              {mutation.isPending ? '正在确认' : '重试原操作'}
            </Button>
          ) : (
            <Button type="submit" form="key-form" disabled={mutation.isPending}>
              {mutation.isPending ? '创建中' : '创建'}
            </Button>
          )}
        </>
      }
    >
      <form id="key-form" className="form-grid" onSubmit={(event) => void handleSubmit(event)}>
        <Field label="所属用户" htmlFor="key-owner" error={form.formState.errors.ownerId?.message}>
          <NativeSelect
            id="key-owner"
            autoFocus
            disabled={controlsLocked}
            {...form.register('ownerId')}
          >
            <option value="">请选择</option>
            {users.data?.items.map((user) => (
              <option key={user.id} value={user.id}>
                {user.displayName}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="名称" htmlFor="key-name" error={form.formState.errors.name?.message}>
          <Input id="key-name" readOnly={controlsLocked} {...form.register('name')} />
        </Field>
        <Field
          label="授权模型"
          htmlFor="key-models"
          error={form.formState.errors.authorizedModelIds?.message}
        >
          <div id="key-models" className="check-grid">
            {models.map((model) => (
              <label key={model.id}>
                <input
                  type="checkbox"
                  value={model.id}
                  disabled={controlsLocked}
                  {...form.register('authorizedModelIds')}
                />
                <span>
                  {model.alias} · {model.providerName}
                </span>
              </label>
            ))}
            {!activeConfiguration.isLoading && models.length === 0 ? (
              <span className="field__hint">当前没有可授权的已发布模型。</span>
            ) : null}
          </div>
        </Field>
        <Field
          label="到期时间"
          htmlFor="key-expiry"
          error={form.formState.errors.expiresAt?.message}
        >
          <Input
            id="key-expiry"
            type="datetime-local"
            readOnly={controlsLocked}
            {...form.register('expiresAt')}
          />
        </Field>
        {uncertain ? (
          <div className="inline-problem" role="alert">
            创建结果暂时无法确认。请使用原操作重试，系统会返回同一条 Key。
          </div>
        ) : (
          <>
            {persistenceFailed ? (
              <div className="inline-problem" role="alert">
                浏览器无法保存待确认操作，本次未提交。请允许当前标签页使用会话存储后重试。
              </div>
            ) : null}
            <FormProblem error={mutation.error ?? users.error ?? activeConfiguration.error} />
          </>
        )}
      </form>
    </DialogFrame>
  )
}

function defaultValues(): Values {
  return { ownerId: '', name: '', authorizedModelIds: [], expiresAt: '' }
}

function readPendingSubmission(userId: string): Submission | undefined {
  const parsed = pendingSubmissionSchema.safeParse(loadPendingGatewayKeyOperation(userId))
  if (!parsed.success) {
    clearPendingGatewayKeyOperation(userId)
    return undefined
  }
  const restored = parsed.data
  return {
    input: {
      ownerId: restored.input.ownerId,
      name: restored.input.name,
      authorizedModelIds: restored.input.authorizedModelIds,
      ...(restored.input.expiresAt ? { expiresAt: restored.input.expiresAt } : {}),
    },
    idempotencyKey: restored.idempotencyKey,
  }
}

function valuesFromPending(pending?: Submission): Values {
  if (!pending) return defaultValues()
  return {
    ownerId: pending.input.ownerId,
    name: pending.input.name,
    authorizedModelIds: pending.input.authorizedModelIds,
    expiresAt: pending.input.expiresAt ? localDateTime(pending.input.expiresAt) : '',
  }
}

function isUnknownOutcome(error: unknown): boolean {
  return (
    error instanceof ApiProblem &&
    (error.code === 'operation_outcome_unknown' || error.code === 'network_unavailable')
  )
}

function localDateTime(value: string): string {
  const date = new Date(value)
  const offsetMilliseconds = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offsetMilliseconds).toISOString().slice(0, 16)
}
