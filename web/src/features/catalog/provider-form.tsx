import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { ApiProblem, catalogApi, type ProviderRecord } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

import {
  buildProviderRebase,
  type ProviderConflictChoice,
  type ProviderConflictState,
  type ProviderEditableField,
} from './provider-conflict-rebase'
import { ProviderConflictRecovery } from './provider-conflict-recovery'
import {
  createProviderOperation,
  hasUnknownProviderOutcome,
  type ProviderOperation,
} from './provider-mutation'
import { ProviderOperationRecovery } from './provider-operation-recovery'

const schema = z.object({
  slug: z
    .string()
    .trim()
    .regex(/^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$/, '使用 3-64 位小写字母、数字或连字符'),
  name: z.string().trim().min(2, '请输入 Provider 名称').max(100, '名称不能超过 100 个字符'),
  kind: z.enum(['openai-compatible', 'zhipu', 'deepseek', 'agnes']),
  baseUrl: z
    .url('请输入有效 HTTPS URL')
    .refine((value) => value.startsWith('https://'), '必须使用 HTTPS')
    .refine(
      (value) => !value.includes('?') && !value.includes('#'),
      'Base URL 不能包含查询参数或片段',
    ),
})

type Values = z.infer<typeof schema>
type ProviderWriteVariables = { values: Values; opened?: ProviderRecord }
type Submission = ProviderOperation<ProviderWriteVariables>

export function ProviderForm({
  open,
  onOpenChange,
  provider,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  provider?: ProviderRecord
}) {
  const queryClient = useQueryClient()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: valuesFrom(provider),
  })
  const [submissionSnapshot, setSubmissionSnapshot] = useState<ProviderRecord | undefined>(provider)
  const [conflict, setConflict] = useState<ProviderConflictState | undefined>()
  const [conflictReadError, setConflictReadError] = useState<unknown>()
  const [uncertainOperation, setUncertainOperation] = useState<Submission | undefined>()
  const [readingLatest, setReadingLatest] = useState(false)
  const mutation = useMutation({
    mutationFn: ({ variables, idempotencyKey }: Submission) => {
      const { values, opened } = variables
      return provider
        ? catalogApi.updateProvider(
            provider.id,
            {
              name: values.name,
              kind: values.kind,
              baseUrl: values.baseUrl,
              expectedUpdatedAt: opened?.updatedAt ?? provider.updatedAt,
            },
            idempotencyKey,
          )
        : catalogApi.createProvider(values, idempotencyKey)
    },
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['providers'] })
      close()
    },
    onError(error, operation) {
      const submission = operation.variables
      if (
        provider &&
        submission.opened &&
        error instanceof ApiProblem &&
        error.code === 'conflict'
      ) {
        const recovery: ProviderConflictState = {
          opened: submission.opened,
          draft: submission.values,
          choices: {},
        }
        setConflict(recovery)
        setUncertainOperation(undefined)
        setConflictReadError(undefined)
        void readLatestProvider(provider.id, recovery)
      } else if (hasUnknownProviderOutcome(error)) {
        setUncertainOperation(operation)
        form.reset(operation.variables.values)
      } else {
        setUncertainOperation(undefined)
      }
      void queryClient.invalidateQueries({ queryKey: ['providers'] })
    },
  })
  const currentServerSnapshot = conflict?.latest ?? conflict?.opened ?? submissionSnapshot
  const routingLocked = currentServerSnapshot?.status === 'enabled'
  const conflictRebase = conflict?.latest
    ? buildProviderRebase(conflict.opened, conflict.draft, conflict.latest, conflict.choices)
    : undefined

  async function readLatestProvider(
    providerID: string,
    recovery: ProviderConflictState | undefined = conflict,
  ): Promise<void> {
    if (!recovery) return
    setReadingLatest(true)
    try {
      setConflictReadError(undefined)
      const latest = await catalogApi.provider(providerID)
      const next: ProviderConflictState = { ...recovery, latest, choices: {} }
      setConflict(next)
      form.reset(buildProviderRebase(next.opened, next.draft, latest, next.choices).values)
    } catch (error) {
      setConflictReadError(error)
    } finally {
      setReadingLatest(false)
    }
  }

  function close(): void {
    mutation.reset()
    setConflict(undefined)
    setConflictReadError(undefined)
    setUncertainOperation(undefined)
    setReadingLatest(false)
    onOpenChange(false)
  }

  function reloadLatest(): void {
    if (!conflict?.latest) return
    form.reset(valuesFrom(conflict.latest))
    setSubmissionSnapshot(conflict.latest)
    setConflict(undefined)
    setConflictReadError(undefined)
    setUncertainOperation(undefined)
    setReadingLatest(false)
    mutation.reset()
  }

  function submitNewOperation(values: Values, opened?: ProviderRecord): void {
    setConflict(undefined)
    setConflictReadError(undefined)
    setUncertainOperation(undefined)
    setReadingLatest(false)
    mutation.mutate(
      createProviderOperation({
        values: { ...values },
        ...(opened ? { opened: { ...opened } } : {}),
      }),
    )
  }

  function retryOriginalOperation(): void {
    if (uncertainOperation) mutation.mutate(uncertainOperation)
  }

  function chooseConflict(field: ProviderEditableField, choice: ProviderConflictChoice): void {
    if (!conflict?.latest) return
    const latest = conflict.latest
    const next: ProviderConflictState = {
      ...conflict,
      choices: { ...conflict.choices, [field]: choice },
    }
    setConflict(next)
    form.reset(buildProviderRebase(next.opened, next.draft, latest, next.choices).values)
  }

  function retryLatest(): void {
    if (!conflict?.latest || !conflictRebase || conflictRebase.unresolvedFields.length > 0) return
    const latest = conflict.latest
    setSubmissionSnapshot(latest)
    submitNewOperation(conflictRebase.values, latest)
  }

  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) onOpenChange(true)
        else close()
      }}
      dismissible={!mutation.isPending}
      title={provider ? '编辑 Provider' : '添加 Provider'}
      footer={
        <>
          <Button type="button" variant="secondary" disabled={mutation.isPending} onClick={close}>
            取消
          </Button>
          {provider && conflict ? (
            conflict.latest ? (
              <>
                <Button type="button" variant="secondary" onClick={reloadLatest}>
                  重新载入
                </Button>
                <Button
                  type="button"
                  disabled={
                    mutation.isPending || (conflictRebase?.unresolvedFields.length ?? 0) > 0
                  }
                  title={
                    (conflictRebase?.unresolvedFields.length ?? 0) > 0
                      ? '请先处理每个同字段冲突'
                      : undefined
                  }
                  onClick={retryLatest}
                >
                  {mutation.isPending ? '保存中' : '保存合并结果'}
                </Button>
              </>
            ) : (
              <Button
                type="button"
                disabled={readingLatest}
                onClick={() => void readLatestProvider(provider.id, conflict)}
              >
                {readingLatest ? '正在读取最新事实' : '重新读取最新事实'}
              </Button>
            )
          ) : uncertainOperation ? (
            <Button
              type="submit"
              form="provider-form"
              disabled={mutation.isPending || !form.formState.isDirty}
            >
              {mutation.isPending ? '保存中' : '保存修改为新操作'}
            </Button>
          ) : (
            <Button type="submit" form="provider-form" disabled={mutation.isPending}>
              {mutation.isPending ? '保存中' : '保存'}
            </Button>
          )}
        </>
      }
    >
      <form
        id="provider-form"
        className="form-grid"
        onSubmit={form.handleSubmit((values) => submitNewOperation(values, submissionSnapshot))}
      >
        <Field label="标识" htmlFor="provider-slug" error={form.formState.errors.slug?.message}>
          <Input
            id="provider-slug"
            autoFocus
            readOnly={Boolean(provider)}
            {...form.register('slug')}
          />
        </Field>
        <Field label="名称" htmlFor="provider-name" error={form.formState.errors.name?.message}>
          <Input
            id="provider-name"
            autoFocus={Boolean(provider)}
            readOnly={mutation.isPending || Boolean(conflict)}
            {...form.register('name')}
          />
        </Field>
        <Field label="类型" htmlFor="provider-kind" error={form.formState.errors.kind?.message}>
          <NativeSelect
            id="provider-kind"
            disabled={routingLocked || mutation.isPending || Boolean(conflict)}
            {...form.register('kind')}
          >
            <option value="openai-compatible">OpenAI-compatible</option>
            <option value="zhipu">智谱 GLM</option>
            <option value="deepseek">DeepSeek</option>
            <option value="agnes">Agnes</option>
          </NativeSelect>
        </Field>
        <Field
          label="Base URL"
          htmlFor="provider-base-url"
          error={form.formState.errors.baseUrl?.message}
        >
          <Input
            id="provider-base-url"
            inputMode="url"
            readOnly={routingLocked || mutation.isPending || Boolean(conflict)}
            {...form.register('baseUrl')}
          />
        </Field>
        {uncertainOperation ? (
          <ProviderOperationRecovery
            error={mutation.error}
            pending={mutation.isPending}
            onRetry={retryOriginalOperation}
          />
        ) : (
          <FormProblem error={mutation.error} />
        )}
        {conflict && conflictReadError ? <FormProblem error={conflictReadError} /> : null}
        {conflict?.latest ? (
          <ProviderConflictRecovery
            state={{ ...conflict, latest: conflict.latest }}
            onChoice={chooseConflict}
          />
        ) : null}
      </form>
    </DialogFrame>
  )
}

function valuesFrom(provider?: ProviderRecord): Values {
  return provider
    ? {
        slug: provider.slug,
        name: provider.name,
        kind: provider.kind,
        baseUrl: provider.baseUrl,
      }
    : { slug: '', name: '', kind: 'openai-compatible', baseUrl: 'https://' }
}
