import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { catalogApi, type Credential, type CredentialInput } from '@/api'
import { FormProblem } from '@/features/auth/form-problem'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'

const optionalPositiveInteger = z
  .number()
  .int()
  .positive()
  .optional()
  .or(z.nan().transform(() => undefined))

const schema = z.object({
  providerId: z.string().min(1, '请选择 Provider'),
  label: z.string().trim().min(2, '请输入凭据名称'),
  secret: z.string(),
  resourceDomain: z.enum(['free', 'professional']),
  authorizedModels: z.string().trim().min(1, '请输入至少一个模型别名'),
  rpmLimit: optionalPositiveInteger,
  tpmLimit: optionalPositiveInteger,
  concurrencyLimit: optionalPositiveInteger,
  fixedProxy: z.string().trim(),
})

type Values = z.infer<typeof schema>

export function CredentialForm({
  open,
  onOpenChange,
  credential,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  credential?: Credential
}) {
  const providers = useQuery({
    queryKey: ['providers', 'credential-form'],
    queryFn: ({ signal }) => catalogApi.providers({ page: 1, pageSize: 100 }, signal),
    enabled: open,
  })
  const queryClient = useQueryClient()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: valuesFrom(credential),
  })
  useEffect(() => form.reset(valuesFrom(credential)), [credential, form, open])
  const mutation = useMutation({
    mutationFn: (values: Values) => {
      const input = inputFrom(values, Boolean(credential))
      return credential
        ? catalogApi.updateCredential(credential.id, input)
        : catalogApi.createCredential(input)
    },
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['credentials'] })
      onOpenChange(false)
    },
  })

  return (
    <DialogFrame
      open={open}
      onOpenChange={onOpenChange}
      title={credential ? '编辑上游凭据' : '添加上游凭据'}
      description={credential ? '留空密钥表示保持当前密文' : '密钥在提交后不再返回浏览器'}
      width="lg"
      footer={
        <>
          <Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="submit" form="credential-form" disabled={mutation.isPending}>
            保存
          </Button>
        </>
      }
    >
      <form
        id="credential-form"
        className="form-grid"
        onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
      >
        <Field
          label="Provider"
          htmlFor="credential-provider"
          error={form.formState.errors.providerId?.message}
        >
          <NativeSelect id="credential-provider" autoFocus {...form.register('providerId')}>
            <option value="">请选择</option>
            {providers.data?.items.map((provider) => (
              <option key={provider.id} value={provider.id}>
                {provider.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="名称" htmlFor="credential-label" error={form.formState.errors.label?.message}>
          <Input id="credential-label" {...form.register('label')} />
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
            {...form.register('secret')}
          />
        </Field>
        <Field
          label="资源域"
          htmlFor="credential-domain"
          error={form.formState.errors.resourceDomain?.message}
        >
          <NativeSelect id="credential-domain" {...form.register('resourceDomain')}>
            <option value="free">免费资源域</option>
            <option value="professional">专业资源域</option>
          </NativeSelect>
        </Field>
        <Field
          label="授权模型"
          htmlFor="credential-models"
          hint="多个别名使用英文逗号分隔"
          error={form.formState.errors.authorizedModels?.message}
        >
          <Input id="credential-models" {...form.register('authorizedModels')} />
        </Field>
        <Field
          label="固定代理"
          htmlFor="credential-proxy"
          error={form.formState.errors.fixedProxy?.message}
        >
          <Input
            id="credential-proxy"
            inputMode="url"
            placeholder="https://proxy.example"
            {...form.register('fixedProxy')}
          />
        </Field>
        <Field label="RPM" htmlFor="credential-rpm" error={form.formState.errors.rpmLimit?.message}>
          <Input
            id="credential-rpm"
            type="number"
            min={1}
            {...form.register('rpmLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field label="TPM" htmlFor="credential-tpm" error={form.formState.errors.tpmLimit?.message}>
          <Input
            id="credential-tpm"
            type="number"
            min={1}
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
            {...form.register('concurrencyLimit', { valueAsNumber: true })}
          />
        </Field>
        <FormProblem error={mutation.error ?? providers.error} />
      </form>
    </DialogFrame>
  )
}

function valuesFrom(credential?: Credential): Values {
  return credential
    ? {
        providerId: credential.providerId,
        label: credential.label,
        secret: '',
        resourceDomain: credential.resourceDomain,
        authorizedModels: credential.authorizedModels.join(', '),
        fixedProxy: credential.fixedProxy ?? '',
        rpmLimit: credential.rpmLimit,
        tpmLimit: credential.tpmLimit,
        concurrencyLimit: credential.concurrencyLimit,
      }
    : {
        providerId: '',
        label: '',
        secret: '',
        resourceDomain: 'free',
        authorizedModels: '',
        fixedProxy: '',
      }
}

function inputFrom(values: Values, editing: boolean): CredentialInput {
  const secret = values.secret.trim()
  if (!editing && !secret) throw new Error('新增凭据必须填写密钥')
  return {
    providerId: values.providerId,
    label: values.label,
    secret,
    resourceDomain: values.resourceDomain,
    authorizedModels: values.authorizedModels
      .split(',')
      .map((value) => value.trim())
      .filter(Boolean),
    ...(values.rpmLimit !== undefined ? { rpmLimit: values.rpmLimit } : {}),
    ...(values.tpmLimit !== undefined ? { tpmLimit: values.tpmLimit } : {}),
    ...(values.concurrencyLimit !== undefined ? { concurrencyLimit: values.concurrencyLimit } : {}),
    ...(values.fixedProxy ? { fixedProxy: values.fixedProxy } : {}),
  }
}
