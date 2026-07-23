import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Save } from 'lucide-react'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { siteProfileApi } from '@/api'
import { siteProfileQuery } from '@/app/site-profile'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Field, Input, Textarea } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

const schema = z.object({
  name: z.string().trim().min(2).max(80),
  description: z.string().trim().max(240),
  contact: z.string().trim().max(200),
  expectedVersion: z.number().int().positive(),
})

type Values = z.infer<typeof schema>

export function SettingsPage() {
  const profile = useQuery(siteProfileQuery)
  const queryClient = useQueryClient()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: '', description: '', contact: '', expectedVersion: 1 },
  })
  useEffect(() => {
    if (profile.data) form.reset({ ...profile.data, expectedVersion: profile.data.version })
  }, [form, profile.data])
  const update = useMutation({
    mutationFn: siteProfileApi.update,
    onSuccess(value) {
      queryClient.setQueryData(siteProfileQuery.queryKey, value)
      form.reset({ ...value, expectedVersion: value.version })
    },
    async onError() {
      await queryClient.invalidateQueries({ queryKey: siteProfileQuery.queryKey })
    },
  })
  return (
    <Page>
      <PageHeader title="站点设置" />
      <PageSection title="站点资料">
        <form
          className="settings-form"
          onSubmit={form.handleSubmit((value) => update.mutate(value))}
        >
          <Field label="站点名称" htmlFor="site-name" error={form.formState.errors.name?.message}>
            <Input id="site-name" autoComplete="organization" {...form.register('name')} />
          </Field>
          <Field
            label="联系信息"
            htmlFor="site-contact"
            error={form.formState.errors.contact?.message}
          >
            <Input id="site-contact" {...form.register('contact')} />
          </Field>
          <Field
            label="简短说明"
            htmlFor="site-description"
            error={form.formState.errors.description?.message}
            className="field--full"
          >
            <Textarea id="site-description" rows={3} {...form.register('description')} />
          </Field>
          <input type="hidden" {...form.register('expectedVersion', { valueAsNumber: true })} />
          <FormProblem error={profile.error ?? update.error} />
          <div className="settings-form__actions">
            {update.isSuccess ? (
              <span className="inline-success" role="status">
                <Check size={15} /> 已保存
              </span>
            ) : null}
            <Button
              type="submit"
              icon={<Save size={16} />}
              disabled={profile.isLoading || update.isPending || !profile.data}
            >
              保存
            </Button>
          </div>
        </form>
      </PageSection>
    </Page>
  )
}
