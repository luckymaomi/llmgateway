import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { LogIn } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { authApi } from '@/api'
import { AuthPanel } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Field, Input } from '@/components/ui/field'

import { FormProblem } from './form-problem'

const schema = z.object({
  email: z.email('请输入有效邮箱'),
  password: z.string().min(1, '请输入密码'),
})

type FormValues = z.infer<typeof schema>

export function LoginPage({ redirectTo = '/overview' }: { redirectTo?: string }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { email: '', password: '' },
  })
  const login = useMutation({
    mutationFn: authApi.login,
    async onSuccess(session) {
      queryClient.setQueryData(['session'], session)
      await navigate({ to: redirectTo, replace: true })
    },
  })

  return (
    <AuthPanel title="登录" subtitle="使用 LLMGateway 管理会话">
      <form className="form-stack" onSubmit={form.handleSubmit((values) => login.mutate(values))}>
        <Field label="邮箱" htmlFor="login-email" error={form.formState.errors.email?.message}>
          <Input
            id="login-email"
            type="email"
            autoComplete="email"
            autoFocus
            {...form.register('email')}
          />
        </Field>
        <Field
          label="密码"
          htmlFor="login-password"
          error={form.formState.errors.password?.message}
        >
          <Input
            id="login-password"
            type="password"
            autoComplete="current-password"
            {...form.register('password')}
          />
        </Field>
        <FormProblem error={login.error} />
        <Button type="submit" icon={<LogIn size={16} />} disabled={login.isPending}>
          {login.isPending ? '正在登录' : '登录'}
        </Button>
      </form>
      <footer className="auth-panel__footer">
        <span>持有邀请码</span>
        <Link to="/register">注册</Link>
      </footer>
    </AuthPanel>
  )
}
