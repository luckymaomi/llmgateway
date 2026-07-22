import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent, type MouseEvent } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'

import { ApiProblem, catalogApi, type Credential, type CredentialUpdateInput } from '@/api'
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
  label: z.string().trim().min(2, '请输入 API Key 名称'),
  resourceDomain: z.enum(['free', 'professional']),
  modelBindings: modelBindingsSchema,
  rpmLimit: optionalPositiveInteger,
  tpmLimit: optionalPositiveInteger,
  concurrencyLimit: optionalPositiveInteger,
})

type Values = z.infer<typeof schema>
type Submission = { input: CredentialUpdateInput; idempotencyKey: string }

export function CredentialEditForm({
  credential,
  open,
  onOpenChange,
}: {
  credential: Credential
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [uncertain, setUncertain] = useState<Submission>()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: valuesFrom(credential),
  })
  const resourceDomain = useWatch({ control: form.control, name: 'resourceDomain' })
  const modelBindings = useWatch({ control: form.control, name: 'modelBindings' })
  const models = useQuery({
    queryKey: ['models', 'credential-edit', credential.providerId, resourceDomain],
    queryFn: ({ signal }) =>
      catalogApi.models(
        {
          page: 1,
          pageSize: 100,
          providerId: credential.providerId,
          resourceDomain,
          status: 'active',
        },
        signal,
      ),
    enabled: open,
  })
  const mutation = useMutation({
    gcTime: 0,
    mutationFn: (submission: Submission) =>
      catalogApi.updateCredential(credential.id, submission.input, submission.idempotencyKey),
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['credentials'] })
      setUncertain(undefined)
      onOpenChange(false)
    },
    onError(error, submission) {
      setUncertain(isUnknownOutcome(error) ? submission : undefined)
    },
  })

  async function submit(values: Values): Promise<void> {
    if (uncertain) return
    const submission = {
      input: inputFrom(values, credential.updatedAt),
      idempotencyKey: crypto.randomUUID(),
    }
    try {
      await mutation.mutateAsync(submission)
    } catch {
      // The mutation state renders the typed error or recovery action.
    }
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    await form.handleSubmit(submit)(event)
  }

  async function retryUncertain(event: MouseEvent<HTMLButtonElement>): Promise<void> {
    if (!uncertain) return
    event.currentTarget.disabled = true
    try {
      await mutation.mutateAsync(uncertain)
    } catch {
      // Keep the same operation available until its outcome is known.
    } finally {
      event.currentTarget.disabled = false
    }
  }

  const locked = mutation.isPending || Boolean(uncertain)
  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !locked) onOpenChange(false)
      }}
      title="编辑 Provider API Key"
      width="lg"
      dismissible={!locked}
      footer={
        <>
          <Button
            type="button"
            variant="secondary"
            disabled={locked}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          {uncertain ? (
            <Button disabled={mutation.isPending} onClick={(event) => void retryUncertain(event)}>
              {mutation.isPending ? '正在确认' : '确认原更新'}
            </Button>
          ) : (
            <Button type="submit" form="credential-edit-form" disabled={mutation.isPending}>
              {mutation.isPending ? '保存中' : '保存更新'}
            </Button>
          )}
        </>
      }
    >
      <form
        id="credential-edit-form"
        className="form-grid"
        onSubmit={(event) => void handleSubmit(event)}
      >
        <Field label="Provider" htmlFor="credential-edit-provider">
          <Input id="credential-edit-provider" value={credential.providerName} readOnly />
        </Field>
        <Field
          label="名称"
          htmlFor="credential-edit-label"
          error={form.formState.errors.label?.message}
        >
          <Input id="credential-edit-label" readOnly={locked} {...form.register('label')} />
        </Field>
        <Field
          label="资源域"
          htmlFor="credential-edit-domain"
          error={form.formState.errors.resourceDomain?.message}
        >
          <NativeSelect
            id="credential-edit-domain"
            disabled={locked}
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
          htmlFor="credential-edit-models"
          className="credential-routing-field"
          error={form.formState.errors.modelBindings?.message}
        >
          <ModelBindingsField
            id="credential-edit-models"
            models={models.data?.items ?? []}
            value={modelBindings}
            disabled={locked}
            onChange={(bindings) =>
              form.setValue('modelBindings', bindings, { shouldValidate: true })
            }
          />
        </Field>
        <Field
          label="RPM"
          htmlFor="credential-edit-rpm"
          error={form.formState.errors.rpmLimit?.message}
        >
          <Input
            id="credential-edit-rpm"
            type="number"
            min={1}
            readOnly={locked}
            {...form.register('rpmLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field
          label="TPM"
          htmlFor="credential-edit-tpm"
          error={form.formState.errors.tpmLimit?.message}
        >
          <Input
            id="credential-edit-tpm"
            type="number"
            min={1}
            readOnly={locked}
            {...form.register('tpmLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field
          label="并发上限"
          htmlFor="credential-edit-concurrency"
          error={form.formState.errors.concurrencyLimit?.message}
        >
          <Input
            id="credential-edit-concurrency"
            type="number"
            min={1}
            readOnly={locked}
            {...form.register('concurrencyLimit', { valueAsNumber: true })}
          />
        </Field>
        {uncertain ? (
          <div className="inline-problem" role="alert">
            更新结果暂时无法确认。确认原更新会复用同一幂等键，不会写入第二套绑定。
          </div>
        ) : (
          <FormProblem error={mutation.error ?? models.error} />
        )}
      </form>
    </DialogFrame>
  )
}

function valuesFrom(credential: Credential): Values {
  return {
    label: credential.label,
    resourceDomain: credential.resourceDomain,
    modelBindings: credential.modelBindings.map(({ modelId, priority, weight }) => ({
      modelId,
      priority,
      weight,
    })),
    rpmLimit: credential.rpmLimit,
    tpmLimit: credential.tpmLimit,
    concurrencyLimit: credential.concurrencyLimit,
  }
}

function inputFrom(values: Values, expectedUpdatedAt: string): CredentialUpdateInput {
  return {
    label: values.label.trim(),
    resourceDomain: values.resourceDomain,
    modelBindings: values.modelBindings,
    expectedUpdatedAt,
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
