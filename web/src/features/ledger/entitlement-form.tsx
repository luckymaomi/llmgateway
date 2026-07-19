import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { accessApi, ledgerApi } from '@/api'
import { FormProblem } from '@/features/auth/form-problem'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'

const optionalInteger = z
  .number()
  .int()
  .positive()
  .optional()
  .or(z.nan().transform(() => undefined))
const schema = z.object({
  ownerId: z.string().min(1, '请选择用户'),
  planKind: z.enum(['token', 'coding']),
  resourceDomain: z.enum(['free', 'professional']),
  modelAliases: z.string().trim().min(1, '请输入模型别名'),
  tokenLimit: optionalInteger,
  rpmLimit: optionalInteger,
  tpmLimit: optionalInteger,
  concurrencyLimit: z.number().int().positive(),
  startsAt: z.string().min(1, '请选择开始时间'),
  expiresAt: z.string().min(1, '请选择到期时间'),
})
type Values = z.infer<typeof schema>

export function EntitlementForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const users = useQuery({
    queryKey: ['users', 'entitlement-form'],
    queryFn: ({ signal }) => accessApi.users({ page: 1, pageSize: 100, status: 'active' }, signal),
    enabled: open,
  })
  const queryClient = useQueryClient()
  const [defaultValues] = useState<Values>(createDefaultValues)
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues })
  const mutation = useMutation({
    mutationFn: (values: Values) =>
      ledgerApi.createEntitlement({
        ownerId: values.ownerId,
        planKind: values.planKind,
        resourceDomain: values.resourceDomain,
        modelAliases: values.modelAliases
          .split(',')
          .map((value) => value.trim())
          .filter(Boolean),
        concurrencyLimit: values.concurrencyLimit,
        startsAt: new Date(values.startsAt).toISOString(),
        expiresAt: new Date(values.expiresAt).toISOString(),
        ...(values.tokenLimit !== undefined ? { tokenLimit: values.tokenLimit } : {}),
        ...(values.rpmLimit !== undefined ? { rpmLimit: values.rpmLimit } : {}),
        ...(values.tpmLimit !== undefined ? { tpmLimit: values.tpmLimit } : {}),
      }),
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['entitlements'] })
      onOpenChange(false)
    },
  })
  return (
    <DialogFrame
      open={open}
      onOpenChange={onOpenChange}
      title="分配额度或套餐"
      width="lg"
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="submit" form="entitlement-form" disabled={mutation.isPending}>
            分配
          </Button>
        </>
      }
    >
      <form
        id="entitlement-form"
        className="form-grid"
        onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
      >
        <Field
          label="用户"
          htmlFor="entitlement-owner"
          error={form.formState.errors.ownerId?.message}
        >
          <NativeSelect id="entitlement-owner" autoFocus {...form.register('ownerId')}>
            <option value="">请选择</option>
            {users.data?.items.map((user) => (
              <option key={user.id} value={user.id}>
                {user.displayName}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field
          label="类型"
          htmlFor="entitlement-kind"
          error={form.formState.errors.planKind?.message}
        >
          <NativeSelect id="entitlement-kind" {...form.register('planKind')}>
            <option value="token">Token Plan</option>
            <option value="coding">Coding Plan</option>
          </NativeSelect>
        </Field>
        <Field
          label="资源域"
          htmlFor="entitlement-domain"
          error={form.formState.errors.resourceDomain?.message}
        >
          <NativeSelect id="entitlement-domain" {...form.register('resourceDomain')}>
            <option value="free">免费资源域</option>
            <option value="professional">专业资源域</option>
          </NativeSelect>
        </Field>
        <Field
          label="模型范围"
          htmlFor="entitlement-models"
          error={form.formState.errors.modelAliases?.message}
        >
          <Input id="entitlement-models" {...form.register('modelAliases')} />
        </Field>
        <Field
          label="Token 上限"
          htmlFor="entitlement-token"
          error={form.formState.errors.tokenLimit?.message}
        >
          <Input
            id="entitlement-token"
            type="number"
            min={1}
            {...form.register('tokenLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field
          label="RPM"
          htmlFor="entitlement-rpm"
          error={form.formState.errors.rpmLimit?.message}
        >
          <Input
            id="entitlement-rpm"
            type="number"
            min={1}
            {...form.register('rpmLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field
          label="TPM"
          htmlFor="entitlement-tpm"
          error={form.formState.errors.tpmLimit?.message}
        >
          <Input
            id="entitlement-tpm"
            type="number"
            min={1}
            {...form.register('tpmLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field
          label="并发上限"
          htmlFor="entitlement-concurrency"
          error={form.formState.errors.concurrencyLimit?.message}
        >
          <Input
            id="entitlement-concurrency"
            type="number"
            min={1}
            {...form.register('concurrencyLimit', { valueAsNumber: true })}
          />
        </Field>
        <Field
          label="开始时间"
          htmlFor="entitlement-start"
          error={form.formState.errors.startsAt?.message}
        >
          <Input id="entitlement-start" type="datetime-local" {...form.register('startsAt')} />
        </Field>
        <Field
          label="到期时间"
          htmlFor="entitlement-expiry"
          error={form.formState.errors.expiresAt?.message}
        >
          <Input id="entitlement-expiry" type="datetime-local" {...form.register('expiresAt')} />
        </Field>
        <FormProblem error={mutation.error ?? users.error} />
      </form>
    </DialogFrame>
  )
}

function localDateTime(date: Date): string {
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
}

function createDefaultValues(): Values {
  const now = new Date()
  return {
    ownerId: '',
    planKind: 'token',
    resourceDomain: 'free',
    modelAliases: '',
    concurrencyLimit: 1,
    startsAt: localDateTime(now),
    expiresAt: localDateTime(new Date(now.getTime() + 30 * 24 * 60 * 60 * 1_000)),
  }
}
