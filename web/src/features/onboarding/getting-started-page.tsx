import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  Activity,
  Check,
  KeyRound,
  PackageCheck,
  ServerCog,
  UsersRound,
  WalletCards,
} from 'lucide-react'

import { operationsApi, type AdministratorOverview } from '@/api'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { ErrorState, LoadingState } from '@/components/ui/state'

import {
  useOnboardingTour,
  type OnboardingRoute,
  type OnboardingTarget,
} from './onboarding-tour-context'

interface SetupStep {
  label: string
  to:
    | '/resource-pools'
    | '/provider-keys'
    | '/plans'
    | '/members'
    | '/subscriptions'
    | '/api-keys'
    | '/api-logs'
  ready: boolean
  icon: typeof ServerCog
  target: OnboardingTarget
}

export function GettingStartedPage() {
  const { startTour } = useOnboardingTour()
  const query = useQuery({
    queryKey: ['operations-overview'],
    queryFn: ({ signal }) => operationsApi.overview(signal),
  })
  if (query.isLoading)
    return (
      <Page>
        <LoadingState label="正在读取服务状态" />
      </Page>
    )
  if (query.error || !query.data || query.data.scope !== 'administrator')
    return (
      <Page>
        <ErrorState
          error={query.error ?? new Error('当前账号不能访问管理员新手指引')}
          onRetry={() => void query.refetch()}
        />
      </Page>
    )

  const steps = setupSteps(query.data)
  const next = steps.find((step) => !step.ready)
  const completed = steps.filter((step) => step.ready).length
  return (
    <Page className="onboarding-home">
      <PageHeader
        eyebrow={`${completed} / ${steps.length} 项已完成`}
        title={next ? '下一步配置' : '服务已可用'}
        actions={
          next ? (
            <Button icon={<next.icon size={16} />} onClick={() => startTour(next.target)}>
              引导我完成
            </Button>
          ) : (
            <Button asChild variant="secondary" icon={<Activity size={16} />}>
              <Link to="/operations">查看运行状态</Link>
            </Button>
          )
        }
      />
      <PageSection>
        <div className="onboarding-path" aria-label="首次配置进度">
          {steps.map((step, index) => (
            <div className="onboarding-phase" data-ready={step.ready} key={step.to}>
              <span className="onboarding-phase__index">{String(index + 1).padStart(2, '0')}</span>
              <span className="onboarding-phase__icon" aria-hidden="true">
                <step.icon size={21} />
              </span>
              <div>
                <strong>{step.label}</strong>
              </div>
              <span className="onboarding-phase__status">
                {step.ready ? (
                  <>
                    <Check size={14} /> 已完成
                  </>
                ) : next === step ? (
                  '当前任务'
                ) : (
                  '待完成'
                )}
              </span>
            </div>
          ))}
        </div>
      </PageSection>
    </Page>
  )
}

function setupSteps(overview: AdministratorOverview): SetupStep[] {
  const resources = overview.resources
  return [
    {
      label: '创建资源池',
      to: '/resource-pools',
      ready: resources.activeResourcePoolCount > 0,
      icon: ServerCog,
      target: target('/resource-pools', '创建资源池', 'create-resource-pool'),
    },
    {
      label: '添加并探测上游 API Key',
      to: '/provider-keys',
      ready: resources.activeCredentialCount > 0 && resources.successfulCredentialProbeCount > 0,
      icon: KeyRound,
      target: target('/provider-keys', '添加并探测上游 API Key', 'create-provider-key'),
    },
    {
      label: '发布套餐',
      to: '/plans',
      ready: resources.activeServicePlanCount > 0,
      icon: PackageCheck,
      target: target('/plans', '发布套餐', 'create-plan'),
    },
    {
      label: '创建成员',
      to: '/members',
      ready: resources.activeMemberCount > 0,
      icon: UsersRound,
      target: target('/members', '创建成员', 'create-member'),
    },
    {
      label: '分配订阅',
      to: '/subscriptions',
      ready: resources.activeSubscriptionCount > 0,
      icon: WalletCards,
      target: target('/subscriptions', '分配订阅', 'create-subscription'),
    },
    {
      label: '创建 API 密钥',
      to: '/api-keys',
      ready: resources.activeApiKeyCount > 0,
      icon: KeyRound,
      target: target('/api-keys', '创建 API 密钥', 'create-api-key'),
    },
    {
      label: '验证统一 API',
      to: '/api-keys',
      ready: resources.hasCompletedRequest,
      icon: Activity,
      target: target('/api-keys', '验证统一 API', 'test-api-key', '测试请求'),
    },
  ]
}

function target(
  to: OnboardingRoute,
  label: string,
  action: string,
  actionLabel = label,
): OnboardingTarget {
  return { to, label, action, actionLabel }
}
