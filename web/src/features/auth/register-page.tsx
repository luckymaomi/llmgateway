import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { UserRoundPlus } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { authApi } from '@/api'
import { AuthPanel } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Field, Input } from '@/components/ui/field'

import { FormProblem } from './form-problem'

const schema = z.object({
  invitation: z.string().trim().min(6, '请输入有效邀请码'),
  displayName: z.string().trim().min(2, '请输入至少 2 个字符'),
  email: z.email('请输入有效邮箱'),
  password: z.string().min(12, '密码至少 12 位'),
})

type FormValues = z.infer<typeof schema>

export function RegisterPage({ invitation = '' }: { invitation?: string }) {
  const navigate = useNavigate()
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { invitation, displayName: '', email: '', password: '' },
  })
  const register = useMutation({
    mutationFn: authApi.register,
    async onSuccess(result) {
      await navigate({
        to: result.status === 'active' ? '/login' : '/pending-review',
        replace: true,
      })
    },
  })

  return (
    <AuthPanel title="邀请注册" subtitle="提交后由管理员审核">
      <form
        className="form-stack"
        onSubmit={form.handleSubmit((values) => register.mutate(values))}
      >
        <Field
          label="邀请码"
          htmlFor="register-invitation"
          error={form.formState.errors.invitation?.message}
        >
          <Input id="register-invitation" autoComplete="off" {...form.register('invitation')} />
        </Field>
        <Field
          label="显示名称"
          htmlFor="register-name"
          error={form.formState.errors.displayName?.message}
        >
          <Input id="register-name" autoComplete="name" {...form.register('displayName')} />
        </Field>
        <Field label="邮箱" htmlFor="register-email" error={form.formState.errors.email?.message}>
          <Input
            id="register-email"
            type="email"
            autoComplete="email"
            {...form.register('email')}
          />
        </Field>
        <Field
          label="密码"
          htmlFor="register-password"
          error={form.formState.errors.password?.message}
        >
          <Input
            id="register-password"
            type="password"
            autoComplete="new-password"
            {...form.register('password')}
          />
        </Field>
        <FormProblem error={register.error} />
        <Button type="submit" icon={<UserRoundPlus size={16} />} disabled={register.isPending}>
          {register.isPending ? '正在提交' : '提交注册'}
        </Button>
      </form>
      <footer className="auth-panel__footer">
        <span>已有账号</span>
        <Link to="/login">登录</Link>
      </footer>
    </AuthPanel>
  )
}
