import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { catalogApi, type Provider } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

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

export function ProviderForm({
  open,
  onOpenChange,
  provider,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  provider?: Provider
}) {
  const queryClient = useQueryClient()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: valuesFrom(provider),
  })
  useEffect(() => form.reset(valuesFrom(provider)), [form, provider, open])
  const mutation = useMutation({
    mutationFn: (values: Values) =>
      provider
        ? catalogApi.updateProvider(provider.id, {
            name: values.name,
            kind: values.kind,
            baseUrl: values.baseUrl,
            expectedUpdatedAt: provider.updatedAt,
          })
        : catalogApi.createProvider(values),
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['providers'] })
      onOpenChange(false)
    },
    onError: () => queryClient.invalidateQueries({ queryKey: ['providers'] }),
  })

  return (
    <DialogFrame
      open={open}
      onOpenChange={onOpenChange}
      dismissible={!mutation.isPending}
      title={provider ? '编辑 Provider' : '添加 Provider'}
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
          <Button type="submit" form="provider-form" disabled={mutation.isPending}>
            {mutation.isPending ? '保存中' : '保存'}
          </Button>
        </>
      }
    >
      <form
        id="provider-form"
        className="form-grid"
        onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
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
          <Input id="provider-name" autoFocus={Boolean(provider)} {...form.register('name')} />
        </Field>
        <Field label="类型" htmlFor="provider-kind" error={form.formState.errors.kind?.message}>
          <NativeSelect id="provider-kind" {...form.register('kind')}>
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
          <Input id="provider-base-url" inputMode="url" {...form.register('baseUrl')} />
        </Field>
        <FormProblem error={mutation.error} />
      </form>
    </DialogFrame>
  )
}

function valuesFrom(provider?: Provider): Values {
  return provider
    ? {
        slug: provider.slug,
        name: provider.name,
        kind: provider.kind,
        baseUrl: provider.baseUrl,
      }
    : { slug: '', name: '', kind: 'openai-compatible', baseUrl: 'https://' }
}
