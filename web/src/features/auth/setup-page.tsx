import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowRight, ShieldCheck } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { authApi } from '@/api'
import { AuthPanel } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Field, Input } from '@/components/ui/field'
import { LoadingState } from '@/components/ui/state'

import { FormProblem } from './form-problem'

const schema = z
  .object({
    displayName: z.string().trim().min(2, '请输入至少 2 个字符'),
    email: z.email('请输入有效邮箱'),
    password: z.string().min(12, '密码至少 12 位'),
    confirmation: z.string(),
  })
  .refine((values) => values.password === values.confirmation, {
    path: ['confirmation'],
    message: '两次密码不一致',
  })

type FormValues = z.infer<typeof schema>

export function SetupPage() {
  const status = useQuery({
    queryKey: ['setup-status'],
    queryFn: authApi.setupStatus,
    retry: false,
  })
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { displayName: '', email: '', password: '', confirmation: '' },
  })
  const bootstrap = useMutation({
    mutationFn: authApi.bootstrap,
    async onSuccess(session) {
      queryClient.setQueryData(['session'], session)
      await navigate({ to: '/providers/providers', replace: true })
    },
  })

  if (status.isLoading) return <LoadingState label="正在检查初始化状态" />

  if (status.data && !status.data.required) {
    return (
      <AuthPanel title="系统已经初始化">
        <Button asChild icon={<ArrowRight size={16} />}>
          <Link to="/login">进入登录</Link>
        </Button>
      </AuthPanel>
    )
  }

  return (
    <AuthPanel title="初始化 LLMGateway" subtitle="创建首位管理员">
      <div className="auth-marker">
        <ShieldCheck size={18} />
        本次提交完成后初始化入口关闭
      </div>
      <form
        className="form-stack"
        onSubmit={form.handleSubmit((values) =>
          bootstrap.mutate({
            displayName: values.displayName,
            email: values.email,
            password: values.password,
          }),
        )}
      >
        <Field
          label="管理员名称"
          htmlFor="setup-name"
          error={form.formState.errors.displayName?.message}
        >
          <Input id="setup-name" autoComplete="name" {...form.register('displayName')} />
        </Field>
        <Field label="邮箱" htmlFor="setup-email" error={form.formState.errors.email?.message}>
          <Input id="setup-email" type="email" autoComplete="email" {...form.register('email')} />
        </Field>
        <Field
          label="密码"
          htmlFor="setup-password"
          error={form.formState.errors.password?.message}
        >
          <Input
            id="setup-password"
            type="password"
            autoComplete="new-password"
            {...form.register('password')}
          />
        </Field>
        <Field
          label="确认密码"
          htmlFor="setup-confirmation"
          error={form.formState.errors.confirmation?.message}
        >
          <Input
            id="setup-confirmation"
            type="password"
            autoComplete="new-password"
            {...form.register('confirmation')}
          />
        </Field>
        <FormProblem error={bootstrap.error ?? status.error} />
        <Button type="submit" disabled={bootstrap.isPending}>
          {bootstrap.isPending ? '正在创建' : '创建管理员'}
        </Button>
      </form>
    </AuthPanel>
  )
}
