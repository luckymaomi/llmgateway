import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { accessApi } from '@/api'
import { FormProblem } from '@/features/auth/form-problem'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'

const schema = z.object({
  role: z.enum(['operator', 'member']),
  expiresAt: z.string().min(1, '请选择到期时间'),
})

type Values = z.infer<typeof schema>

export function InvitationForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { role: 'member', expiresAt: defaultExpiry() },
  })
  const mutation = useMutation({
    mutationFn: accessApi.createInvitation,
    async onSuccess() {
      await queryClient.invalidateQueries({ queryKey: ['invitations'] })
      onOpenChange(false)
      form.reset({ role: 'member', expiresAt: defaultExpiry() })
    },
  })
  return (
    <DialogFrame
      open={open}
      onOpenChange={onOpenChange}
      title="创建邀请"
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="submit" form="invitation-form" disabled={mutation.isPending}>
            创建
          </Button>
        </>
      }
    >
      <form
        id="invitation-form"
        className="form-grid"
        onSubmit={form.handleSubmit((values) =>
          mutation.mutate({ ...values, expiresAt: new Date(values.expiresAt).toISOString() }),
        )}
      >
        <Field label="角色" htmlFor="invitation-role" error={form.formState.errors.role?.message}>
          <NativeSelect id="invitation-role" autoFocus {...form.register('role')}>
            <option value="member">成员</option>
            <option value="operator">运维人员</option>
          </NativeSelect>
        </Field>
        <Field
          label="到期时间"
          htmlFor="invitation-expiry"
          error={form.formState.errors.expiresAt?.message}
        >
          <Input id="invitation-expiry" type="datetime-local" {...form.register('expiresAt')} />
        </Field>
        <FormProblem error={mutation.error} />
      </form>
    </DialogFrame>
  )
}

function defaultExpiry(): string {
  const date = new Date(Date.now() + 7 * 24 * 60 * 60 * 1_000)
  return date.toISOString().slice(0, 16)
}
