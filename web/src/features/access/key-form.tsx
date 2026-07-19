import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { accessApi, type CreatedGatewayKey } from '@/api'
import { useSession } from '@/app/session'
import { FormProblem } from '@/features/auth/form-problem'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input, NativeSelect } from '@/components/ui/field'

const schema = z.object({
  ownerId: z.string(),
  name: z.string().trim().min(2, '请输入 Key 名称'),
  authorizedModels: z.string().trim().min(1, '请输入至少一个模型别名'),
  expiresAt: z.string(),
})

type Values = z.infer<typeof schema>

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
  const users = useQuery({
    queryKey: ['users', 'key-form'],
    queryFn: ({ signal }) => accessApi.users({ page: 1, pageSize: 100, status: 'active' }, signal),
    enabled: open && session.role !== 'member',
  })
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      ownerId: session.role === 'member' ? session.userId : '',
      name: '',
      authorizedModels: '',
      expiresAt: '',
    },
  })
  const mutation = useMutation({
    mutationFn: (values: Values) =>
      accessApi.createKey({
        ownerId: session.role === 'member' ? session.userId : values.ownerId,
        name: values.name,
        authorizedModels: values.authorizedModels
          .split(',')
          .map((value) => value.trim())
          .filter(Boolean),
        ...(values.expiresAt ? { expiresAt: new Date(values.expiresAt).toISOString() } : {}),
      }),
    async onSuccess(result) {
      setCreated(result)
      await queryClient.invalidateQueries({ queryKey: ['gateway-keys'] })
    },
  })
  const close = () => {
    setCreated(null)
    setCopied(false)
    form.reset({
      ownerId: session.role === 'member' ? session.userId : '',
      name: '',
      authorizedModels: '',
      expiresAt: '',
    })
    onOpenChange(false)
  }

  if (created) {
    return (
      <DialogFrame
        open={open}
        onOpenChange={(next) => {
          if (!next) close()
        }}
        title="网关 Key 已创建"
        description="明文仅在本次创建结果中展示"
        footer={<Button onClick={close}>完成</Button>}
      >
        <div className="secret-reveal">
          <code data-testid="created-key-secret">{created.secret}</code>
          <Button
            variant="secondary"
            icon={copied ? <Check size={16} /> : <Copy size={16} />}
            onClick={() => {
              void navigator.clipboard.writeText(created.secret)
              setCopied(true)
            }}
          >
            {copied ? '已复制' : '复制 Key'}
          </Button>
        </div>
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

  return (
    <DialogFrame
      open={open}
      onOpenChange={onOpenChange}
      title="创建网关 Key"
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="submit" form="key-form" disabled={mutation.isPending}>
            创建
          </Button>
        </>
      }
    >
      <form
        id="key-form"
        className="form-grid"
        onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
      >
        {session.role !== 'member' ? (
          <Field
            label="所属用户"
            htmlFor="key-owner"
            error={form.formState.errors.ownerId?.message}
          >
            <NativeSelect id="key-owner" autoFocus {...form.register('ownerId')}>
              <option value="">请选择</option>
              {users.data?.items.map((user) => (
                <option key={user.id} value={user.id}>
                  {user.displayName}
                </option>
              ))}
            </NativeSelect>
          </Field>
        ) : null}
        <Field label="名称" htmlFor="key-name" error={form.formState.errors.name?.message}>
          <Input id="key-name" autoFocus={session.role === 'member'} {...form.register('name')} />
        </Field>
        <Field
          label="授权模型"
          htmlFor="key-models"
          hint="多个别名使用英文逗号分隔"
          error={form.formState.errors.authorizedModels?.message}
        >
          <Input id="key-models" {...form.register('authorizedModels')} />
        </Field>
        <Field
          label="到期时间"
          htmlFor="key-expiry"
          error={form.formState.errors.expiresAt?.message}
        >
          <Input id="key-expiry" type="datetime-local" {...form.register('expiresAt')} />
        </Field>
        <FormProblem error={mutation.error ?? users.error} />
      </form>
    </DialogFrame>
  )
}
