import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent, type MouseEvent } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'

import { ApiProblem, catalogApi, type CredentialInput } from '@/api'
import {
  clearPendingCredentialOperation,
  storePendingCredentialOperation,
} from '@/app/pending-operations'
import { useSession } from '@/app/session'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

import { modelBindingsSchema } from './model-binding-form'
import { ModelBindingsField } from './model-bindings-field'

const optionalPositiveInteger = z
  .number()
  .int()
  .positive()
  .optional()
  .or(z.nan().transform(() => undefined))

const schema = z.object({
  providerId: z.string().min(1, '请选择 Provider'),
  label: z.string().trim().min(2, '请输入凭据名称'),
  secret: z.string().min(8, '凭据至少需要 8 个字符'),
  resourceDomain: z.enum(['free', 'professional']),
  modelBindings: modelBindingsSchema,
  rpmLimit: optionalPositiveInteger,
  tpmLimit: optionalPositiveInteger,
  concurrencyLimit: optionalPositiveInteger,
})

type Values = z.infer<typeof schema>
type Submission = { input: CredentialInput; idempotencyKey: string }

export function CredentialForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const session = useSession()
  const [uncertain, setUncertain] = useState<Submission>()
  const [persistenceFailed, setPersistenceFailed] = useState(false)
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: defaultValues() })
  const providerId = useWatch({ control: form.control, name: 'providerId' })
  const resourceDomain = useWatch({ control: form.control, name: 'resourceDomain' })
  const modelBindings = useWatch({ control: form.control, name: 'modelBindings' })
  const providers = useQuery({
    queryKey: ['providers', 'credential-form'],
    queryFn: ({ signal }) => catalogApi.providers({ page: 1, pageSize: 100 }, signal),
    enabled: open,
  })
  const models = useQuery({
    queryKey: ['models', 'credential-form', providerId, resourceDomain],
    queryFn: ({ signal }) =>
      catalogApi.models(
        { page: 1, pageSize: 100, providerId, resourceDomain, status: 'active' },
        signal,
      ),
    enabled: open && Boolean(providerId),
  })
  const mutation = useMutation({
    gcTime: 0,
    mutationFn: (submission: Submission) =>
      catalogApi.createCredential(submission.input, submission.idempotencyKey),
    async onSuccess() {
      clearPendingCredentialOperation(session.userId)
      await queryClient.invalidateQueries({ queryKey: ['credentials'] })
      resetAndClose()
    },
    onError(error, submission) {
      if (isUnknownOutcome(error)) {
        setUncertain(submission)
      } else {
        clearPendingCredentialOperation(session.userId)
        setUncertain(undefined)
      }
    },
  })

  function resetAndClose(): void {
    mutation.reset()
    setUncertain(undefined)
    setPersistenceFailed(false)
    clearPendingCredentialOperation(session.userId)
    form.reset(defaultValues())
    onOpenChange(false)
  }

  function requestClose(): void {
    if (mutation.isPending || uncertain) return
    resetAndClose()
  }

  async function submit(values: Values): Promise<void> {
    if (uncertain) return
    const submission = { input: inputFrom(values), idempotencyKey: crypto.randomUUID() }
    if (
      !storePendingCredentialOperation(session.userId, {
        idempotencyKey: submission.idempotencyKey,
        providerId: submission.input.providerId,
        label: submission.input.label,
        resourceDomain: submission.input.resourceDomain,
        modelBindings: submission.input.modelBindings,
      })
    ) {
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

  const controlsLocked = mutation.isPending || Boolean(uncertain)

  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) requestClose()
      }}
      title="添加上游凭据"
      description="凭据与所选模型原子保存，密钥提交后不再返回浏览器。"
      width="lg"
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
            <Button type="submit" form="credential-form" disabled={mutation.isPending}>
              {mutation.isPending ? '保存中' : '保存'}
            </Button>
          )}
        </>
      }
    >
      <form
        id="credential-form"
        className="form-grid"
        onSubmit={(event) => void handleSubmit(event)}
      >
        <Field
          label="Provider"
          htmlFor="credential-provider"
          error={form.formState.errors.providerId?.message}
        >
          <NativeSelect
            id="credential-provider"
            autoFocus
            disabled={controlsLocked}
            {...form.register('providerId', {
              onChange: () => form.setValue('modelBindings', []),
            })}
          >
            <option value="">请选择</option>
            {providers.data?.items.map((provider) => (
              <option key={provider.id} value={provider.id}>
                {provider.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="名称" htmlFor="credential-label" error={form.formState.errors.label?.message}>
          <Input id="credential-label" readOnly={controlsLocked} {...form.register('label')} />
        </Field>
        <Field
          label="API Key / 凭据"
          htmlFor="credential-secret"
          error={form.formState.errors.secret?.message}
        >
          <Input
            id="credential-secret"
            type="password"
            autoComplete="new-password"
            readOnly={controlsLocked}
            {...form.register('secret')}
          />
        </Field>
        <Field
          label="资源域"
          htmlFor="credential-domain"
          error={form.formState.errors.resourceDomain?.message}
        >
          <NativeSelect
            id="credential-domain"
            disabled={controlsLocked}
            {...form.register('resourceDomain', {
              onChange: () => form.setValue('modelBindings', []),
            })}
          >
            <option value="free">免费资源域</option>
            <option value="professional">专业资源域</option>
          </NativeSelect>
        </Field>
        <Field
          label="模型路由"
          htmlFor="credential-models"
          className="credential-routing-field"
          error={form.formState.errors.modelBindings?.message}
        >
          <>
            <ModelBindingsField
              id="credential-models"
              models={models.data?.items ?? []}
              value={modelBindings}
              disabled={controlsLocked}
              onChange={(bindings) =>
                form.setValue('modelBindings', bindings, { shouldValidate: true })
              }
            />
            {providerId && !models.isLoading && models.data?.items.length === 0 ? (
              <span className="field__hint">该 Provider 与资源域下没有可用模型。</span>
            ) : null}
          </>
        </Field>
        <Field label="RPM" htmlFor="credential-rpm" error={form.formState.errors.rpmLimit?.message}>
          <Input
            id="credential-rpm"
            type="number"
            min={1}
            readOnly={controlsLocked}
            {...form.register('rpmLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field label="TPM" htmlFor="credential-tpm" error={form.formState.errors.tpmLimit?.message}>
          <Input
            id="credential-tpm"
            type="number"
            min={1}
            readOnly={controlsLocked}
            {...form.register('tpmLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field
          label="并发上限"
          htmlFor="credential-concurrency"
          error={form.formState.errors.concurrencyLimit?.message}
        >
          <Input
            id="credential-concurrency"
            type="number"
            min={1}
            readOnly={controlsLocked}
            {...form.register('concurrencyLimit', { valueAsNumber: true })}
          />
        </Field>
        {uncertain ? (
          <div className="inline-problem" role="alert">
            结果暂时无法确认。重试原操作会使用相同的幂等键，不会创建第二条凭据。
          </div>
        ) : (
          <>
            {persistenceFailed ? (
              <div className="inline-problem" role="alert">
                浏览器无法保存待确认操作，本次未提交。请允许当前标签页使用会话存储后重试。
              </div>
            ) : null}
            <FormProblem error={mutation.error ?? providers.error ?? models.error} />
          </>
        )}
      </form>
    </DialogFrame>
  )
}

function defaultValues(): Values {
  return {
    providerId: '',
    label: '',
    secret: '',
    resourceDomain: 'free',
    modelBindings: [],
  }
}

function inputFrom(values: Values): CredentialInput {
  return {
    providerId: values.providerId,
    label: values.label.trim(),
    secret: values.secret,
    resourceDomain: values.resourceDomain,
    modelBindings: values.modelBindings,
    ...(values.rpmLimit !== undefined ? { rpmLimit: values.rpmLimit } : {}),
    ...(values.tpmLimit !== undefined ? { tpmLimit: values.tpmLimit } : {}),
    ...(values.concurrencyLimit !== undefined ? { concurrencyLimit: values.concurrencyLimit } : {}),
  }
}

function isUnknownOutcome(error: unknown): boolean {
  return (
    error instanceof ApiProblem &&
    (error.code === 'operation_outcome_unknown' || error.code === 'network_unavailable')
  )
}
