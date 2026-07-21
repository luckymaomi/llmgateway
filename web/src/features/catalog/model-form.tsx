import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, type FormEvent } from 'react'
import { useForm, type DefaultValues } from 'react-hook-form'
import { z } from 'zod'

import { catalogApi, type Model, type ModelCapability, type ModelInput } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

const capabilities: Array<{ value: ModelCapability; label: string }> = [
  { value: 'streaming', label: '流式' },
  { value: 'tools', label: '工具调用' },
  { value: 'reasoning', label: '推理内容' },
  { value: 'structured_output', label: '结构化输出' },
]

const schema = z.object({
  providerId: z.string().min(1, '请选择 Provider'),
  alias: z.string().trim().min(1, '请输入模型别名'),
  upstreamModelId: z.string().trim().min(1, '请输入上游模型 ID'),
  resourceDomain: z.enum(['free', 'professional']),
  capabilities: z
    .array(z.enum(['streaming', 'tools', 'reasoning', 'structured_output']))
    .min(1, '至少选择一项能力'),
  contextTokens: z.number().int().positive('请输入正整数上下文 Token 上限'),
})

type Values = z.infer<typeof schema>

export function ModelForm({
  open,
  onOpenChange,
  model,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  model?: Model
}) {
  const providers = useQuery({
    queryKey: ['providers', 'model-form'],
    queryFn: ({ signal }) => catalogApi.providers({ page: 1, pageSize: 100 }, signal),
    enabled: open,
  })
  const queryClient = useQueryClient()
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: valuesFrom(model) })
  useEffect(() => form.reset(valuesFrom(model)), [form, model, open])
  const mutation = useMutation({
    mutationFn: (input: ModelInput) =>
      model ? catalogApi.updateModel(model.id, input) : catalogApi.createModel(input),
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['models'] })
      onOpenChange(false)
    },
  })

  async function submit(values: Values): Promise<void> {
    try {
      await mutation.mutateAsync({
        providerId: values.providerId,
        alias: values.alias,
        upstreamModelId: values.upstreamModelId,
        resourceDomain: values.resourceDomain,
        capabilities: values.capabilities,
        contextTokens: values.contextTokens,
      })
    } catch {
      // The mutation state renders the typed error.
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

  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => {
        if (!mutation.isPending || nextOpen) onOpenChange(nextOpen)
      }}
      title={model ? '编辑模型' : '添加模型'}
      dismissible={!mutation.isPending}
      footer={
        <>
          <Button
            type="button"
            variant="secondary"
            disabled={mutation.isPending}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button type="submit" form="model-form" disabled={mutation.isPending}>
            保存
          </Button>
        </>
      }
    >
      <form id="model-form" className="form-grid" onSubmit={(event) => void handleSubmit(event)}>
        <Field
          label="Provider"
          htmlFor="model-provider"
          error={form.formState.errors.providerId?.message}
        >
          <NativeSelect id="model-provider" autoFocus {...form.register('providerId')}>
            <option value="">请选择</option>
            {providers.data?.items.map((provider) => (
              <option value={provider.id} key={provider.id}>
                {provider.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="网关别名" htmlFor="model-alias" error={form.formState.errors.alias?.message}>
          <Input id="model-alias" {...form.register('alias')} />
        </Field>
        <Field
          label="上游模型 ID"
          htmlFor="model-upstream"
          error={form.formState.errors.upstreamModelId?.message}
        >
          <Input id="model-upstream" {...form.register('upstreamModelId')} />
        </Field>
        <Field
          label="资源域"
          htmlFor="model-domain"
          error={form.formState.errors.resourceDomain?.message}
        >
          <NativeSelect id="model-domain" {...form.register('resourceDomain')}>
            <option value="free">免费资源域</option>
            <option value="professional">专业资源域</option>
          </NativeSelect>
        </Field>
        <Field
          label="上下文 Token"
          htmlFor="model-context"
          error={form.formState.errors.contextTokens?.message}
        >
          <Input
            id="model-context"
            type="number"
            min={1}
            {...form.register('contextTokens', { valueAsNumber: true })}
          />
        </Field>
        <fieldset className="field field--full">
          <legend className="field__label">能力</legend>
          <div className="check-grid">
            {capabilities.map((capability) => (
              <label key={capability.value}>
                <input
                  type="checkbox"
                  value={capability.value}
                  {...form.register('capabilities')}
                />
                {capability.label}
              </label>
            ))}
          </div>
          {form.formState.errors.capabilities?.message ? (
            <span className="field__error">{form.formState.errors.capabilities.message}</span>
          ) : null}
        </fieldset>
        <FormProblem error={mutation.error ?? providers.error} />
      </form>
    </DialogFrame>
  )
}

function valuesFrom(model?: Model): DefaultValues<Values> {
  return model
    ? {
        providerId: model.providerId,
        alias: model.alias,
        upstreamModelId: model.upstreamModelId,
        resourceDomain: model.resourceDomain,
        capabilities: model.capabilities,
        ...(model.contextTokens !== undefined ? { contextTokens: model.contextTokens } : {}),
      }
    : {
        providerId: '',
        alias: '',
        upstreamModelId: '',
        resourceDomain: 'free',
        capabilities: ['streaming'],
      }
}
