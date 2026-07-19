import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { accessApi, ledgerApi } from '@/api'
import { FormProblem } from '@/features/auth/form-problem'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect, Textarea } from '@/components/ui/field'

const schema = z.object({
  ownerId: z.string().min(1, '请选择用户'),
  resourceDomain: z.enum(['free', 'professional']),
  tokenDelta: z
    .number()
    .int()
    .refine((value) => value !== 0, '调整值不能为 0'),
  reason: z.string().trim().min(4, '请填写调整原因'),
})

type Values = z.infer<typeof schema>

export function AdjustmentForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const users = useQuery({
    queryKey: ['users', 'adjustment-form'],
    queryFn: ({ signal }) => accessApi.users({ page: 1, pageSize: 100, status: 'active' }, signal),
    enabled: open,
  })
  const queryClient = useQueryClient()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { ownerId: '', resourceDomain: 'free', tokenDelta: 0, reason: '' },
  })
  const mutation = useMutation({
    mutationFn: (values: Values) =>
      ledgerApi.adjust({ ...values, idempotencyKey: crypto.randomUUID() }),
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['ledger-entries'] })
      onOpenChange(false)
      form.reset({ ownerId: '', resourceDomain: 'free', tokenDelta: 0, reason: '' })
    },
  })
  return (
    <DialogFrame
      open={open}
      onOpenChange={onOpenChange}
      title="人工额度调整"
      description="提交后写入不可变账本事件"
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="submit" form="adjustment-form" disabled={mutation.isPending}>
            提交调整
          </Button>
        </>
      }
    >
      <form
        id="adjustment-form"
        className="form-grid"
        onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
      >
        <Field
          label="用户"
          htmlFor="adjustment-owner"
          error={form.formState.errors.ownerId?.message}
        >
          <NativeSelect id="adjustment-owner" autoFocus {...form.register('ownerId')}>
            <option value="">请选择</option>
            {users.data?.items.map((user) => (
              <option key={user.id} value={user.id}>
                {user.displayName}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field
          label="资源域"
          htmlFor="adjustment-domain"
          error={form.formState.errors.resourceDomain?.message}
        >
          <NativeSelect id="adjustment-domain" {...form.register('resourceDomain')}>
            <option value="free">免费资源域</option>
            <option value="professional">专业资源域</option>
          </NativeSelect>
        </Field>
        <Field
          label="Token 变化"
          htmlFor="adjustment-token"
          hint="增加为正数，扣减为负数"
          error={form.formState.errors.tokenDelta?.message}
        >
          <Input
            id="adjustment-token"
            type="number"
            {...form.register('tokenDelta', { valueAsNumber: true })}
          />
        </Field>
        <Field
          label="原因"
          htmlFor="adjustment-reason"
          error={form.formState.errors.reason?.message}
        >
          <Textarea id="adjustment-reason" rows={3} {...form.register('reason')} />
        </Field>
        <FormProblem error={mutation.error ?? users.error} />
      </form>
    </DialogFrame>
  )
}
