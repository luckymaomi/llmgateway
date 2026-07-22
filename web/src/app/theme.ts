import { useEffect, useState } from 'react'

export type ThemePreference = 'system' | 'light' | 'dark'

const storageKey = 'llmgateway.theme'

export function readThemePreference(): ThemePreference {
  const stored = localStorage.getItem(storageKey)
  return stored === 'light' || stored === 'dark' ? stored : 'system'
}

export function applyThemePreference(preference: ThemePreference): void {
  const dark =
    preference === 'dark' ||
    (preference === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
}

export function setThemePreference(preference: ThemePreference): void {
  if (preference === 'system') localStorage.removeItem(storageKey)
  else localStorage.setItem(storageKey, preference)
  applyThemePreference(preference)
}

export function useThemePreference(): [ThemePreference, (value: ThemePreference) => void] {
  const [preference, setPreference] = useState(readThemePreference)
  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const apply = () => applyThemePreference(preference)
    apply()
    media.addEventListener('change', apply)
    return () => media.removeEventListener('change', apply)
  }, [preference])
  return [
    preference,
    (value) => {
      setThemePreference(value)
      setPreference(value)
    },
  ]
}
