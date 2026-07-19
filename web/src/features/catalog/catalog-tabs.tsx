import { SectionTabs } from '@/components/layout'

export function CatalogTabs() {
  return (
    <SectionTabs
      tabs={[
        { label: 'Provider', to: '/providers/providers' },
        { label: '模型', to: '/providers/models' },
        { label: '配置版本', to: '/providers/revisions' },
      ]}
    />
  )
}
