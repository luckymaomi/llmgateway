export function formatNumber(value: number): string {
  return new Intl.NumberFormat('zh-CN').format(value)
}

export function formatTokens(value?: number): string {
  if (value === undefined) return '未知'
  if (Math.abs(value) >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (Math.abs(value) >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return formatNumber(value)
}

export function formatPercent(value?: number): string {
  return value === undefined ? '未知' : `${(value * 100).toFixed(1)}%`
}

export function formatDuration(milliseconds?: number): string {
  if (milliseconds === undefined) return '未知'
  if (milliseconds < 1_000) return `${Math.round(milliseconds)} ms`
  return `${(milliseconds / 1_000).toFixed(2)} s`
}

export function formatDateTime(value?: string): string {
  if (!value) return '未知'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
}

export function formatSignedTokens(value: number): string {
  return `${value > 0 ? '+' : ''}${formatNumber(value)}`
}
